package postgres

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/kamil5b/db2toon/constants"
	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
)

type Extractor struct {
	conn      *pgx.Conn
	cockroach bool
}

func New(ctx context.Context, dsn string) (*Extractor, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return &Extractor{conn: conn}, nil
}

// NewCockroach creates an extractor for CockroachDB's PostgreSQL-compatible
// wire protocol and its catalog-specific schema inspection statements.
func NewCockroach(ctx context.Context, dsn string) (*Extractor, error) {
	e, err := New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	e.cockroach = true
	return e, nil
}

func (e *Extractor) Close(ctx context.Context) error {
	return e.conn.Close(ctx)
}

func (e *Extractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	schemas := opts.Schemas
	if len(schemas) == 0 {
		schemas = []string{constants.SchemaPublic}
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
		if excludedTable(table, opts.ExcludeTables) {
			continue
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
		if err := e.populateTable(ctx, table, opts); err != nil {
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
	if err := e.loadExtensions(ctx, db); err != nil {
		return nil, err
	}
	for _, name := range order {
		if err := e.loadEnums(ctx, bySchema[name]); err != nil {
			return nil, err
		}
		if err := e.loadRoutines(ctx, bySchema[name]); err != nil {
			return nil, err
		}
		db.Schemas = append(db.Schemas, *bySchema[name])
	}
	return db, nil
}

func (e *Extractor) loadExtensions(ctx context.Context, db *schema.Database) error {
	if e.cockroach {
		// CockroachDB has no PostgreSQL-compatible extension registry.
		return nil
	}
	rows, err := e.conn.Query(ctx, `
SELECT ext.extname, ext.extversion, n.nspname
FROM pg_extension ext
JOIN pg_namespace n ON n.oid = ext.extnamespace
ORDER BY ext.extname`)
	if err != nil {
		return fmt.Errorf("query extensions: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var extension schema.Extension
		if err := rows.Scan(&extension.Name, &extension.Version, &extension.Schema); err != nil {
			return fmt.Errorf("scan extension: %w", err)
		}
		db.Extensions = append(db.Extensions, extension)
	}
	return rows.Err()
}

func (e *Extractor) populateTable(ctx context.Context, table *schema.Table, opts database.ExtractOptions) error {
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
	if err := e.loadIndexes(ctx, table); err != nil {
		return err
	}
	if err := e.loadTriggers(ctx, table); err != nil {
		return err
	}
	if opts.ExampleSample > 0 && !excludedTable(*table, opts.ExcludeExampleTables) {
		return e.loadExample(ctx, table, opts)
	}
	return nil
}

func (e *Extractor) loadEnums(ctx context.Context, namespace *schema.Schema) error {
	rows, err := e.conn.Query(ctx, `
SELECT typ.typname, enum.enumlabel
FROM pg_type typ
JOIN pg_namespace n ON n.oid = typ.typnamespace
JOIN pg_enum enum ON enum.enumtypid = typ.oid
WHERE n.nspname = $1
ORDER BY typ.typname, enum.enumsortorder`, namespace.Name)
	if err != nil {
		return fmt.Errorf("query enums for schema %s: %w", namespace.Name, err)
	}
	defer rows.Close()
	positions := map[string]int{}
	for rows.Next() {
		var name, value string
		if err := rows.Scan(&name, &value); err != nil {
			return fmt.Errorf("scan enum for schema %s: %w", namespace.Name, err)
		}
		position, ok := positions[name]
		if !ok {
			position = len(namespace.Enums)
			positions[name] = position
			namespace.Enums = append(namespace.Enums, schema.Enum{Name: name})
		}
		namespace.Enums[position].Values = append(namespace.Enums[position].Values, value)
	}
	return rows.Err()
}

func (e *Extractor) loadRoutines(ctx context.Context, namespace *schema.Schema) error {
	rows, err := e.conn.Query(ctx, `
SELECT p.proname,
       CASE p.prokind WHEN 'p' THEN 'procedure' ELSE 'function' END,
       pg_get_function_arguments(p.oid),
       pg_get_function_result(p.oid),
       l.lanname,
       pg_get_functiondef(p.oid)
FROM pg_proc p
JOIN pg_namespace n ON n.oid = p.pronamespace
JOIN pg_language l ON l.oid = p.prolang
WHERE n.nspname = $1
  AND p.prokind IN ('f', 'p')
  AND NOT EXISTS (
      SELECT 1 FROM pg_depend d
      WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e'
  )
ORDER BY p.proname, p.oid`, namespace.Name)
	if err != nil {
		return fmt.Errorf("query routines for schema %s: %w", namespace.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var routine schema.Routine
		if err := rows.Scan(&routine.Name, &routine.Kind, &routine.Arguments, &routine.ReturnType, &routine.Language, &routine.Definition); err != nil {
			return fmt.Errorf("scan routine for schema %s: %w", namespace.Name, err)
		}
		namespace.Routines = append(namespace.Routines, routine)
	}
	return rows.Err()
}

func excludedTable(table schema.Table, exclusions []string) bool {
	for _, exclusion := range exclusions {
		if exclusion == table.Name || exclusion == table.Schema+"."+table.Name {
			return true
		}
	}
	return false
}

func excludedExampleField(tableSchema, tableName, fieldName string, exclusions []string) bool {
	qualifiedField := tableSchema + "." + tableName + "." + fieldName
	for _, exclusion := range exclusions {
		if exclusion == qualifiedField || exclusion == tableName+"."+fieldName {
			return true
		}
	}
	return false
}

func (e *Extractor) loadExample(ctx context.Context, table *schema.Table, opts database.ExtractOptions) error {
	if len(table.Columns) == 0 {
		return nil
	}

	qualifiedTable := pgx.Identifier{table.Schema, table.Name}.Sanitize()
	query := "SELECT t.* FROM " + qualifiedTable + " AS t"
	args := []any{opts.ExampleSample}
	if opts.ExampleSampleOrdered {
		columns := make([]string, 0, len(table.Columns))
		for _, column := range table.Columns {
			columns = append(columns, pgx.Identifier{column.Name}.Sanitize())
		}
		query += " ORDER BY " + strings.Join(columns, ", ")
	} else if opts.Seed != 0 {
		query += " ORDER BY md5(row_to_json(t)::text || $2)"
		args = append(args, strconv.FormatInt(opts.Seed, 10))
	} else {
		query += " ORDER BY random()"
	}
	query += " LIMIT $1"

	rows, err := e.conn.Query(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("query examples for %s.%s: %w", table.Schema, table.Name, err)
	}
	defer rows.Close()

	example := &schema.Example{
		Columns:     make([]string, 0, len(table.Columns)),
		ColumnTypes: make([]string, 0, len(table.Columns)),
	}
	indices := make([]int, 0, len(table.Columns))
	for i, column := range table.Columns {
		if excludedExampleField(table.Schema, table.Name, column.Name, opts.ExcludeExampleFields) {
			continue
		}
		indices = append(indices, i)
		example.Columns = append(example.Columns, column.Name)
		example.ColumnTypes = append(example.ColumnTypes, column.NativeType)
	}
	if len(indices) == 0 {
		return nil
	}
	for rows.Next() {
		values := make([]any, len(table.Columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return fmt.Errorf("scan examples for %s.%s: %w", table.Schema, table.Name, err)
		}
		row := make([]any, 0, len(indices))
		for _, index := range indices {
			row = append(row, values[index])
		}
		example.Rows = append(example.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate examples for %s.%s: %w", table.Schema, table.Name, err)
	}
	table.Example = example
	return nil
}

func (e *Extractor) loadColumns(ctx context.Context, table *schema.Table) error {
	rows, err := e.conn.Query(ctx, `
SELECT a.attname,
       format_type(a.atttypid, a.atttypmod),
       NOT a.attnotnull,
       COALESCE(pg_get_expr(ad.adbin, ad.adrelid), ''),
       COALESCE(col_description(a.attrelid, a.attnum), ''),
       a.attidentity::text,
       a.attgenerated::text
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

func (e *Extractor) loadTriggers(ctx context.Context, table *schema.Table) error {
	if e.cockroach {
		return e.loadCockroachTriggers(ctx, table)
	}
	rows, err := e.conn.Query(ctx, `
SELECT tg.tgname, tg.tgtype::int, tg.tgenabled <> 'D', pg_get_triggerdef(tg.oid)
FROM pg_trigger tg
JOIN pg_class c ON c.oid = tg.tgrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = $1 AND c.relname = $2 AND NOT tg.tgisinternal
	ORDER BY tg.tgname`, table.Schema, table.Name)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42883" {
			// CockroachDB exposes pg_trigger but not pg_get_triggerdef. Its
			// trigger definitions therefore cannot be represented faithfully.
			return nil
		}
		return fmt.Errorf("query triggers for %s.%s: %w", table.Schema, table.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var trigger schema.Trigger
		var triggerType int
		if err := rows.Scan(&trigger.Name, &triggerType, &trigger.Enabled, &trigger.Definition); err != nil {
			return fmt.Errorf("scan trigger for %s.%s: %w", table.Schema, table.Name, err)
		}
		trigger.Timing = postgresTriggerTiming(triggerType)
		trigger.Events = postgresTriggerEvents(triggerType)
		table.Triggers = append(table.Triggers, trigger)
	}
	return rows.Err()
}

var cockroachTriggerHeader = regexp.MustCompile(`(?i)\b(BEFORE|AFTER)\s+(.+?)\s+ON\s`)

func (e *Extractor) loadCockroachTriggers(ctx context.Context, table *schema.Table) error {
	qualifiedTable := pgx.Identifier{table.Schema, table.Name}.Sanitize()
	rows, err := e.conn.Query(ctx, "SHOW TRIGGERS FROM "+qualifiedTable)
	if err != nil {
		return fmt.Errorf("query triggers for %s.%s: %w", table.Schema, table.Name, err)
	}
	var triggers []schema.Trigger
	for rows.Next() {
		var trigger schema.Trigger
		if err := rows.Scan(&trigger.Name, &trigger.Enabled); err != nil {
			rows.Close()
			return fmt.Errorf("scan trigger for %s.%s: %w", table.Schema, table.Name, err)
		}
		triggers = append(triggers, trigger)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, trigger := range triggers {
		qualifiedTrigger := pgx.Identifier{trigger.Name}.Sanitize()
		var objectName string
		if err := e.conn.QueryRow(ctx, "SHOW CREATE TRIGGER "+qualifiedTrigger+" ON "+qualifiedTable).Scan(&objectName, &trigger.Definition); err != nil {
			return fmt.Errorf("query trigger definition for %s.%s: %w", table.Schema, trigger.Name, err)
		}
		trigger.Timing, trigger.Events = cockroachTriggerDetails(trigger.Definition)
		table.Triggers = append(table.Triggers, trigger)
	}
	return rows.Err()
}

func cockroachTriggerDetails(definition string) (string, []string) {
	match := cockroachTriggerHeader.FindStringSubmatch(definition)
	if match == nil {
		return "", nil
	}
	var events []string
	for _, token := range strings.Fields(strings.ToUpper(match[2])) {
		if token == "INSERT" || token == "UPDATE" || token == "DELETE" {
			events = append(events, token)
		}
	}
	return strings.ToUpper(match[1]), events
}

func postgresTriggerTiming(triggerType int) string {
	switch {
	case triggerType&64 != 0:
		return "INSTEAD OF"
	case triggerType&2 != 0:
		return "BEFORE"
	default:
		return "AFTER"
	}
}

func postgresTriggerEvents(triggerType int) []string {
	var events []string
	if triggerType&4 != 0 {
		events = append(events, "INSERT")
	}
	if triggerType&16 != 0 {
		events = append(events, "UPDATE")
	}
	if triggerType&8 != 0 {
		events = append(events, "DELETE")
	}
	if triggerType&32 != 0 {
		events = append(events, "TRUNCATE")
	}
	return events
}
