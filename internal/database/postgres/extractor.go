package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/kamil5b/pgschema2toon/internal/database"
	"github.com/kamil5b/pgschema2toon/pkg/schema"
)

type Extractor struct {
	conn *pgx.Conn
}

func New(ctx context.Context, dsn string) (*Extractor, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return &Extractor{conn: conn}, nil
}

func (e *Extractor) Close(ctx context.Context) error {
	return e.conn.Close(ctx)
}

func (e *Extractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	schemas := opts.Schemas
	if len(schemas) == 0 {
		schemas = []string{"public"}
	}

	relkinds := []string{"r"}
	if opts.IncludePartitioned {
		relkinds = append(relkinds, "p")
	}
	if opts.IncludeViews {
		relkinds = append(relkinds, "v", "m")
	}

	rows, err := e.conn.Query(ctx, `
SELECT n.nspname, c.relname, COALESCE(obj_description(c.oid, 'pg_class'), '')
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = ANY($1)
  AND c.relkind = ANY($2)
ORDER BY n.nspname, c.relname`, schemas, relkinds)
	if err != nil {
		return nil, fmt.Errorf("query tables: %w", err)
	}
	defer rows.Close()

	var tables []schema.Table
	for rows.Next() {
		var table schema.Table
		if err := rows.Scan(&table.Schema, &table.Name, &table.Comment); err != nil {
			return nil, fmt.Errorf("scan table: %w", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tables: %w", err)
	}
	rows.Close()

	bySchema := make(map[string]*schema.Schema, len(schemas))
	order := make([]string, 0, len(schemas))
	for i := range tables {
		table := &tables[i]
		if err := e.populateTable(ctx, table); err != nil {
			return nil, err
		}
		s, ok := bySchema[table.Schema]
		if !ok {
			s = &schema.Schema{Name: table.Schema}
			bySchema[table.Schema] = s
			order = append(order, table.Schema)
		}
		s.Tables = append(s.Tables, *table)
	}

	db := &schema.Database{}
	for _, name := range order {
		db.Schemas = append(db.Schemas, *bySchema[name])
	}
	return db, nil
}

func (e *Extractor) populateTable(ctx context.Context, table *schema.Table) error {
	if err := e.loadColumns(ctx, table); err != nil {
		return err
	}
	if err := e.loadPrimaryKey(ctx, table); err != nil {
		return err
	}
	if err := e.loadForeignKeys(ctx, table); err != nil {
		return err
	}
	if err := e.loadUniques(ctx, table); err != nil {
		return err
	}
	if err := e.loadChecks(ctx, table); err != nil {
		return err
	}
	if err := e.loadExclusions(ctx, table); err != nil {
		return err
	}
	return e.loadIndexes(ctx, table)
}

func (e *Extractor) loadColumns(ctx context.Context, table *schema.Table) error {
	rows, err := e.conn.Query(ctx, `
SELECT a.attname,
       format_type(a.atttypid, a.atttypmod),
       NOT a.attnotnull,
       COALESCE(pg_get_expr(ad.adbin, ad.adrelid), ''),
       COALESCE(col_description(a.attrelid, a.attnum), ''),
       a.attidentity,
       a.attgenerated
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
WHERE n.nspname = $1 AND c.relname = $2
  AND a.attnum > 0 AND NOT a.attisdropped
ORDER BY a.attnum`, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("query columns for %s.%s: %w", table.Schema, table.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var c schema.Column
		if err := rows.Scan(&c.Name, &c.NativeType, &c.Nullable, &c.Default, &c.Comment, &c.Identity, &c.Generated); err != nil {
			return fmt.Errorf("scan column for %s.%s: %w", table.Schema, table.Name, err)
		}
		table.Columns = append(table.Columns, c)
	}
	return rows.Err()
}

func (e *Extractor) loadPrimaryKey(ctx context.Context, table *schema.Table) error {
	var name string
	var columns []string
	err := e.conn.QueryRow(ctx, `
SELECT con.conname,
       array_agg(att.attname ORDER BY key.ordinality)
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN unnest(con.conkey) WITH ORDINALITY AS key(attnum, ordinality) ON true
JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = key.attnum
WHERE n.nspname = $1 AND c.relname = $2 AND con.contype = 'p'
GROUP BY con.conname`, table.Schema, table.Name).Scan(&name, &columns)
	if err == pgx.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("query primary key for %s.%s: %w", table.Schema, table.Name, err)
	}
	table.PrimaryKey = &schema.PrimaryKey{Name: name, Columns: columns}
	return nil
}

func (e *Extractor) loadForeignKeys(ctx context.Context, table *schema.Table) error {
	rows, err := e.conn.Query(ctx, `
SELECT con.conname,
       array_agg(local_att.attname ORDER BY local_key.ordinality),
       ref_ns.nspname,
       ref_class.relname,
       array_agg(ref_att.attname ORDER BY local_key.ordinality),
       CASE con.confupdtype WHEN 'a' THEN 'NO ACTION' WHEN 'r' THEN 'RESTRICT' WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' END,
       CASE con.confdeltype WHEN 'a' THEN 'NO ACTION' WHEN 'r' THEN 'RESTRICT' WHEN 'c' THEN 'CASCADE' WHEN 'n' THEN 'SET NULL' WHEN 'd' THEN 'SET DEFAULT' END
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN pg_class ref_class ON ref_class.oid = con.confrelid
JOIN pg_namespace ref_ns ON ref_ns.oid = ref_class.relnamespace
JOIN unnest(con.conkey) WITH ORDINALITY AS local_key(attnum, ordinality) ON true
JOIN unnest(con.confkey) WITH ORDINALITY AS ref_key(attnum, ordinality) ON ref_key.ordinality = local_key.ordinality
JOIN pg_attribute local_att ON local_att.attrelid = con.conrelid AND local_att.attnum = local_key.attnum
JOIN pg_attribute ref_att ON ref_att.attrelid = con.confrelid AND ref_att.attnum = ref_key.attnum
WHERE n.nspname = $1 AND c.relname = $2 AND con.contype = 'f'
GROUP BY con.conname, ref_ns.nspname, ref_class.relname, con.confupdtype, con.confdeltype
ORDER BY con.conname`, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("query foreign keys for %s.%s: %w", table.Schema, table.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var fk schema.ForeignKey
		if err := rows.Scan(&fk.Name, &fk.LocalColumns, &fk.ReferencedSchema, &fk.ReferencedTable, &fk.ReferencedColumns, &fk.OnUpdate, &fk.OnDelete); err != nil {
			return fmt.Errorf("scan foreign key for %s.%s: %w", table.Schema, table.Name, err)
		}
		table.ForeignKeys = append(table.ForeignKeys, fk)
	}
	return rows.Err()
}

func (e *Extractor) loadUniques(ctx context.Context, table *schema.Table) error {
	rows, err := e.conn.Query(ctx, `
SELECT con.conname, array_agg(att.attname ORDER BY key.ordinality)
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
JOIN unnest(con.conkey) WITH ORDINALITY AS key(attnum, ordinality) ON true
JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = key.attnum
WHERE n.nspname = $1 AND c.relname = $2 AND con.contype = 'u'
GROUP BY con.conname
ORDER BY con.conname`, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("query unique constraints for %s.%s: %w", table.Schema, table.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var u schema.UniqueConstraint
		if err := rows.Scan(&u.Name, &u.Columns); err != nil {
			return err
		}
		table.Uniques = append(table.Uniques, u)
	}
	return rows.Err()
}

func (e *Extractor) loadChecks(ctx context.Context, table *schema.Table) error {
	rows, err := e.conn.Query(ctx, `
SELECT con.conname, pg_get_constraintdef(con.oid)
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND con.contype = 'c'
ORDER BY con.conname`, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("query check constraints for %s.%s: %w", table.Schema, table.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var c schema.CheckConstraint
		if err := rows.Scan(&c.Name, &c.Expression); err != nil {
			return err
		}
		table.Checks = append(table.Checks, c)
	}
	return rows.Err()
}

func (e *Extractor) loadExclusions(ctx context.Context, table *schema.Table) error {
	rows, err := e.conn.Query(ctx, `
SELECT con.conname, pg_get_constraintdef(con.oid)
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND con.contype = 'x'
ORDER BY con.conname`, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("query exclusion constraints for %s.%s: %w", table.Schema, table.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var exclusion schema.ExclusionConstraint
		if err := rows.Scan(&exclusion.Name, &exclusion.Definition); err != nil {
			return fmt.Errorf("scan exclusion constraint for %s.%s: %w", table.Schema, table.Name, err)
		}
		table.Exclusions = append(table.Exclusions, exclusion)
	}
	return rows.Err()
}

func (e *Extractor) loadIndexes(ctx context.Context, table *schema.Table) error {
	rows, err := e.conn.Query(ctx, `
SELECT idx.relname,
       pg_get_indexdef(idx.oid),
       i.indisunique,
       EXISTS (SELECT 1 FROM pg_constraint con WHERE con.conindid = i.indexrelid),
       am.amname,
       ARRAY(SELECT pg_get_indexdef(i.indexrelid, key_position, true)
             FROM generate_series(1, i.indnkeyatts) AS key_position),
       ARRAY(SELECT a.attname
             FROM unnest(i.indkey) WITH ORDINALITY AS key(attnum, position)
             JOIN pg_attribute a ON a.attrelid = i.indrelid AND a.attnum = key.attnum
             WHERE key.position > i.indnkeyatts
             ORDER BY key.position),
       COALESCE(pg_get_expr(i.indpred, i.indrelid), '')
FROM pg_index i
JOIN pg_class tbl ON tbl.oid = i.indrelid
JOIN pg_namespace n ON n.oid = tbl.relnamespace
JOIN pg_class idx ON idx.oid = i.indexrelid
JOIN pg_am am ON am.oid = idx.relam
WHERE n.nspname = $1 AND tbl.relname = $2 AND NOT i.indisprimary
ORDER BY idx.relname`, table.Schema, table.Name)
	if err != nil {
		return fmt.Errorf("query indexes for %s.%s: %w", table.Schema, table.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var i schema.Index
		if err := rows.Scan(&i.Name, &i.Definition, &i.Unique, &i.ConstraintBacked, &i.Method, &i.Keys, &i.IncludedColumns, &i.Predicate); err != nil {
			return err
		}
		table.Indexes = append(table.Indexes, i)
	}
	return rows.Err()
}
