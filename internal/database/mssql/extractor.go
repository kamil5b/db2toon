package mssql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/kamil5b/db2toon/constants"
	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/internal/database/sqlutil"
	"github.com/kamil5b/db2toon/pkg/schema"
	_ "github.com/microsoft/go-mssqldb"
)

// Extractor reads SQL Server metadata from its sys catalog views.
type Extractor struct{ db *sql.DB }

func New(ctx context.Context, dsn string) (*Extractor, error) {
	db, err := sql.Open(constants.DialectSQLServer, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Extractor{db: db}, nil
}

// NewFromDump parses a plain-text SQL Server schema dump without executing it.
func NewFromDump(ctx context.Context, path string) (database.Extractor, error) {
	return sqlutil.NewDumpExtractor(ctx, path, constants.DialectMSSQL, constants.SchemaDBO)
}

func (e *Extractor) Close(context.Context) error { return e.db.Close() }

func (e *Extractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	schemas := opts.Schemas
	if len(schemas) == 0 {
		schemas = []string{constants.SchemaDBO}
	}
	db := &schema.Database{Schemas: make([]schema.Schema, 0, len(schemas))}
	for _, name := range schemas {
		s, err := e.extractSchema(ctx, name, opts)
		if err != nil {
			return nil, err
		}
		db.Schemas = append(db.Schemas, s)
	}
	return db, nil
}

func (e *Extractor) extractSchema(ctx context.Context, schemaName string, opts database.ExtractOptions) (schema.Schema, error) {
	result := schema.Schema{Name: schemaName}
	if err := e.db.QueryRowContext(ctx, `SELECT 1 FROM sys.schemas WHERE name = @p1`, schemaName).Scan(new(int)); err == sql.ErrNoRows {
		return result, fmt.Errorf("SQL Server schema %q not found", schemaName)
	} else if err != nil {
		return result, err
	}
	if err := e.loadRoutines(ctx, &result); err != nil {
		return result, err
	}
	if err := e.loadTypes(ctx, &result); err != nil {
		return result, err
	}
	if err := e.loadSequences(ctx, &result); err != nil {
		return result, err
	}
	if err := e.loadSynonyms(ctx, &result); err != nil {
		return result, err
	}
	rows, err := e.db.QueryContext(ctx, `
SELECT o.name, o.object_id, o.type, CAST(ep.value AS nvarchar(max)), COALESCE(sm.definition, '')
FROM sys.objects o
JOIN sys.schemas s ON s.schema_id = o.schema_id
LEFT JOIN sys.extended_properties ep ON ep.major_id = o.object_id AND ep.minor_id = 0 AND ep.name = 'MS_Description'
LEFT JOIN sys.sql_modules sm ON sm.object_id = o.object_id
WHERE s.name = @p1 AND o.type IN ('U', 'V') AND (@p2 = 1 OR o.type = 'U')
ORDER BY o.name`, schemaName, boolToInt(opts.IncludeViews))
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, objectType, definition string
		var objectID int
		var comment sql.NullString
		if err := rows.Scan(&name, &objectID, &objectType, &comment, &definition); err != nil {
			return result, err
		}
		if excludedTable(opts.ExcludeTables, schemaName, name) {
			continue
		}
		t := schema.Table{Schema: schemaName, Name: name, Comment: comment.String}
		if strings.TrimSpace(objectType) == "V" {
			t.Kind, t.Definition = "view", definition
		}
		if err := e.loadTable(ctx, &t, objectID); err != nil {
			return result, err
		}
		if opts.ExampleSample > 0 && !excludedTable(opts.ExcludeExampleTables, schemaName, name) {
			if err := e.loadExample(ctx, &t, opts); err != nil {
				return result, err
			}
		}
		result.Tables = append(result.Tables, t)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	return result, nil
}

func (e *Extractor) loadTable(ctx context.Context, table *schema.Table, objectID int) error {
	if err := e.loadColumns(ctx, table, objectID); err != nil {
		return err
	}
	if err := e.loadConstraints(ctx, table, objectID); err != nil {
		return err
	}
	if err := e.loadIndexes(ctx, table, objectID); err != nil {
		return err
	}
	return e.loadTriggers(ctx, table, objectID)
}

func (e *Extractor) loadColumns(ctx context.Context, table *schema.Table, objectID int) error {
	rows, err := e.db.QueryContext(ctx, `
SELECT c.name,
 CASE WHEN ty.is_user_defined = 1 THEN SCHEMA_NAME(ty.schema_id) + '.' + ty.name
      WHEN ty.name IN ('varchar','char','varbinary','binary') THEN ty.name + '(' + CASE WHEN c.max_length = -1 THEN 'max' ELSE CONVERT(varchar(10), c.max_length) END + ')'
      WHEN ty.name IN ('nvarchar','nchar') THEN ty.name + '(' + CASE WHEN c.max_length = -1 THEN 'max' ELSE CONVERT(varchar(10), c.max_length / 2) END + ')'
      WHEN ty.name IN ('decimal','numeric') THEN ty.name + '(' + CONVERT(varchar(10), c.precision) + ',' + CONVERT(varchar(10), c.scale) + ')'
      WHEN ty.name IN ('datetime2','datetimeoffset','time') THEN ty.name + '(' + CONVERT(varchar(10), c.scale) + ')'
      ELSE ty.name END,
 c.is_nullable, COALESCE(OBJECT_DEFINITION(c.default_object_id), ''), CAST(ep.value AS nvarchar(max)), c.is_identity, c.is_computed
FROM sys.columns c JOIN sys.types ty ON ty.user_type_id = c.user_type_id
LEFT JOIN sys.extended_properties ep ON ep.major_id = c.object_id AND ep.minor_id = c.column_id AND ep.name = 'MS_Description'
WHERE c.object_id = @p1 ORDER BY c.column_id`, objectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c schema.Column
		var nullable, identity, computed bool
		var comment sql.NullString
		if err := rows.Scan(&c.Name, &c.NativeType, &nullable, &c.Default, &comment, &identity, &computed); err != nil {
			return err
		}
		c.Nullable, c.Comment = nullable, comment.String
		if identity {
			c.Identity = "d"
		}
		if computed {
			c.Generated = "s"
		}
		table.Columns = append(table.Columns, c)
	}
	return rows.Err()
}

func (e *Extractor) loadConstraints(ctx context.Context, table *schema.Table, objectID int) error {
	rows, err := e.db.QueryContext(ctx, `
SELECT kc.name, kc.type, c.name
FROM sys.key_constraints kc
JOIN sys.index_columns ic ON ic.object_id = kc.parent_object_id AND ic.index_id = kc.unique_index_id AND ic.key_ordinal > 0
JOIN sys.columns c ON c.object_id = ic.object_id AND c.column_id = ic.column_id
WHERE kc.parent_object_id = @p1 ORDER BY kc.name, ic.key_ordinal`, objectID)
	if err != nil {
		return err
	}
	constraints := map[string][]string{}
	kinds := map[string]string{}
	order := []string{}
	for rows.Next() {
		var n, k, c string
		if err := rows.Scan(&n, &k, &c); err != nil {
			rows.Close()
			return err
		}
		if _, ok := kinds[n]; !ok {
			order = append(order, n)
			kinds[n] = k
		}
		constraints[n] = append(constraints[n], c)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, n := range order {
		if kinds[n] == "PK" {
			table.PrimaryKey = &schema.PrimaryKey{Name: n, Columns: constraints[n]}
		} else {
			table.Uniques = append(table.Uniques, schema.UniqueConstraint{Name: n, Columns: constraints[n]})
		}
	}
	rows, err = e.db.QueryContext(ctx, `SELECT name, definition FROM sys.check_constraints WHERE parent_object_id = @p1 ORDER BY name`, objectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c schema.CheckConstraint
		if err := rows.Scan(&c.Name, &c.Expression); err != nil {
			return err
		}
		table.Checks = append(table.Checks, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return e.loadForeignKeys(ctx, table, objectID)
}

func (e *Extractor) loadForeignKeys(ctx context.Context, table *schema.Table, objectID int) error {
	rows, err := e.db.QueryContext(ctx, `
SELECT fk.name, ps.name, pt.name, fk.update_referential_action_desc, fk.delete_referential_action_desc, pc.name, rc.name
FROM sys.foreign_keys fk
JOIN sys.foreign_key_columns fkc ON fkc.constraint_object_id = fk.object_id
JOIN sys.tables pt ON pt.object_id = fk.referenced_object_id JOIN sys.schemas ps ON ps.schema_id = pt.schema_id
JOIN sys.columns pc ON pc.object_id = fkc.parent_object_id AND pc.column_id = fkc.parent_column_id
JOIN sys.columns rc ON rc.object_id = fkc.referenced_object_id AND rc.column_id = fkc.referenced_column_id
WHERE fk.parent_object_id = @p1 ORDER BY fk.name, fkc.constraint_column_id`, objectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byName := map[string]*schema.ForeignKey{}
	order := []string{}
	for rows.Next() {
		var n string
		var fk schema.ForeignKey
		var local, remote string
		if err := rows.Scan(&n, &fk.ReferencedSchema, &fk.ReferencedTable, &fk.OnUpdate, &fk.OnDelete, &local, &remote); err != nil {
			return err
		}
		if byName[n] == nil {
			fk.Name = n
			fk.OnUpdate = strings.ReplaceAll(fk.OnUpdate, "_", " ")
			fk.OnDelete = strings.ReplaceAll(fk.OnDelete, "_", " ")
			byName[n] = &fk
			order = append(order, n)
		}
		byName[n].LocalColumns = append(byName[n].LocalColumns, local)
		byName[n].ReferencedColumns = append(byName[n].ReferencedColumns, remote)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, n := range order {
		table.ForeignKeys = append(table.ForeignKeys, *byName[n])
	}
	return nil
}

func (e *Extractor) loadIndexes(ctx context.Context, table *schema.Table, objectID int) error {
	rows, err := e.db.QueryContext(ctx, `
SELECT i.name, i.type_desc, i.is_unique, i.has_filter, COALESCE(i.filter_definition, ''), ic.is_included_column, c.name,
 CASE WHEN EXISTS (SELECT 1 FROM sys.key_constraints kc WHERE kc.parent_object_id=i.object_id AND kc.unique_index_id=i.index_id) THEN 1 ELSE 0 END
FROM sys.indexes i JOIN sys.index_columns ic ON ic.object_id=i.object_id AND ic.index_id=i.index_id
JOIN sys.columns c ON c.object_id=ic.object_id AND c.column_id=ic.column_id
WHERE i.object_id=@p1 AND i.name IS NOT NULL AND i.is_primary_key=0 ORDER BY i.name, ic.key_ordinal, ic.index_column_id`, objectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byName := map[string]*schema.Index{}
	order := []string{}
	for rows.Next() {
		var n, method, predicate, col string
		var unique, filtered, included, backed bool
		if err := rows.Scan(&n, &method, &unique, &filtered, &predicate, &included, &col, &backed); err != nil {
			return err
		}
		if byName[n] == nil {
			byName[n] = &schema.Index{Name: n, Method: strings.ToLower(strings.ReplaceAll(method, " ", "_")), Unique: unique, ConstraintBacked: backed}
			if filtered {
				byName[n].Predicate = predicate
			}
			order = append(order, n)
		}
		if included {
			byName[n].IncludedColumns = append(byName[n].IncludedColumns, col)
		} else {
			byName[n].Keys = append(byName[n].Keys, col)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, n := range order {
		i := byName[n]
		i.Definition = fmt.Sprintf("%s (%s)", i.Method, strings.Join(i.Keys, ", "))
		table.Indexes = append(table.Indexes, *i)
	}
	return nil
}

func (e *Extractor) loadTriggers(ctx context.Context, table *schema.Table, objectID int) error {
	rows, err := e.db.QueryContext(ctx, `SELECT tr.name, tr.is_disabled, tr.is_instead_of_trigger, sm.definition, te.type_desc FROM sys.triggers tr JOIN sys.sql_modules sm ON sm.object_id=tr.object_id LEFT JOIN sys.trigger_events te ON te.object_id=tr.object_id WHERE tr.parent_id=@p1 ORDER BY tr.name, te.type_desc`, objectID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byName := map[string]*schema.Trigger{}
	order := []string{}
	for rows.Next() {
		var n, def string
		var disabled, instead bool
		var event sql.NullString
		if err := rows.Scan(&n, &disabled, &instead, &def, &event); err != nil {
			return err
		}
		if byName[n] == nil {
			timing := "AFTER"
			if instead {
				timing = "INSTEAD OF"
			}
			byName[n] = &schema.Trigger{Name: n, Timing: timing, Enabled: !disabled, Definition: def}
			order = append(order, n)
		}
		if event.Valid {
			byName[n].Events = append(byName[n].Events, strings.TrimSuffix(event.String, "_EVENT"))
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, n := range order {
		table.Triggers = append(table.Triggers, *byName[n])
	}
	return nil
}

func (e *Extractor) loadRoutines(ctx context.Context, s *schema.Schema) error {
	rows, err := e.db.QueryContext(ctx, `SELECT o.name, o.type, COALESCE(sm.definition, '') FROM sys.objects o JOIN sys.schemas sc ON sc.schema_id=o.schema_id LEFT JOIN sys.sql_modules sm ON sm.object_id=o.object_id WHERE sc.name=@p1 AND o.type IN ('P','FN','IF','TF') ORDER BY o.name`, s.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r schema.Routine
		var typ string
		if err := rows.Scan(&r.Name, &typ, &r.Definition); err != nil {
			return err
		}
		if typ == "P" {
			r.Kind = "procedure"
		} else {
			r.Kind = "function"
		}
		s.Routines = append(s.Routines, r)
	}
	return rows.Err()
}

func (e *Extractor) loadTypes(ctx context.Context, s *schema.Schema) error {
	rows, err := e.db.QueryContext(ctx, `
SELECT t.name, CASE WHEN t.is_table_type = 1 THEN 'table' ELSE 'alias' END, COALESCE(bt.name, '')
FROM sys.types t
JOIN sys.schemas sc ON sc.schema_id = t.schema_id
LEFT JOIN sys.types bt ON bt.user_type_id = t.system_type_id AND bt.user_type_id = bt.system_type_id
WHERE sc.name = @p1 AND t.is_user_defined = 1
ORDER BY t.name`, s.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var typ schema.UserDefinedType
		if err := rows.Scan(&typ.Name, &typ.Kind, &typ.NativeType); err != nil {
			return err
		}
		s.Types = append(s.Types, typ)
	}
	return rows.Err()
}

func (e *Extractor) loadSequences(ctx context.Context, s *schema.Schema) error {
	rows, err := e.db.QueryContext(ctx, `
SELECT seq.name, ty.name, CONVERT(nvarchar(40), seq.start_value), CONVERT(nvarchar(40), seq.increment),
       CONVERT(nvarchar(40), seq.minimum_value), CONVERT(nvarchar(40), seq.maximum_value), seq.is_cycling
FROM sys.sequences seq JOIN sys.schemas sc ON sc.schema_id = seq.schema_id
JOIN sys.types ty ON ty.user_type_id = seq.user_type_id
WHERE sc.name = @p1 ORDER BY seq.name`, s.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sequence schema.Sequence
		if err := rows.Scan(&sequence.Name, &sequence.NativeType, &sequence.Start, &sequence.Increment, &sequence.Minimum, &sequence.Maximum, &sequence.Cyclic); err != nil {
			return err
		}
		s.Sequences = append(s.Sequences, sequence)
	}
	return rows.Err()
}

func (e *Extractor) loadSynonyms(ctx context.Context, s *schema.Schema) error {
	rows, err := e.db.QueryContext(ctx, `SELECT sy.name, sy.base_object_name FROM sys.synonyms sy JOIN sys.schemas sc ON sc.schema_id = sy.schema_id WHERE sc.name = @p1 ORDER BY sy.name`, s.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var synonym schema.Synonym
		if err := rows.Scan(&synonym.Name, &synonym.Target); err != nil {
			return err
		}
		s.Synonyms = append(s.Synonyms, synonym)
	}
	return rows.Err()
}

func (e *Extractor) loadExample(ctx context.Context, table *schema.Table, opts database.ExtractOptions) error {
	columns := make([]string, 0, len(table.Columns))
	types := make([]string, 0, len(table.Columns))
	for _, c := range table.Columns {
		if !excludedField(opts.ExcludeExampleFields, table.Schema, table.Name, c.Name) {
			columns = append(columns, c.Name)
			types = append(types, c.NativeType)
		}
	}
	if len(columns) == 0 {
		return nil
	}
	query := fmt.Sprintf("SELECT TOP (@p1) %s FROM %s", quotedList(columns), quote(table.Schema)+"."+quote(table.Name))
	if opts.ExampleSampleOrdered {
		query += " ORDER BY " + quotedList(columns)
	}
	rows, err := e.db.QueryContext(ctx, query, opts.ExampleSample)
	if err != nil {
		return err
	}
	defer rows.Close()
	example := &schema.Example{Columns: columns, ColumnTypes: types}
	for rows.Next() {
		values := make([]any, len(columns))
		dest := make([]any, len(columns))
		for i := range values {
			dest[i] = &values[i]
		}
		if err := rows.Scan(dest...); err != nil {
			return err
		}
		for i, v := range values {
			if b, ok := v.([]byte); ok {
				values[i] = string(b)
			}
		}
		example.Rows = append(example.Rows, values)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	table.Example = example
	return nil
}

func quote(name string) string { return "[" + strings.ReplaceAll(name, "]", "]]") + "]" }
func quotedList(names []string) string {
	values := make([]string, len(names))
	for i, n := range names {
		values[i] = quote(n)
	}
	return strings.Join(values, ", ")
}
func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func excludedTable(values []string, schemaName, table string) bool {
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == table || v == schemaName+"."+table {
			return true
		}
	}
	return false
}
func excludedField(values []string, schemaName, table, column string) bool {
	for _, v := range values {
		if strings.TrimSpace(v) == schemaName+"."+table+"."+column {
			return true
		}
	}
	return false
}

var _ database.Extractor = (*Extractor)(nil)
