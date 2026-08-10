package sqlutil

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
)

// Extractor contains the database/sql implementation shared by the two local
// SQL adapters. It deliberately uses only catalog/read-only queries.
type Extractor struct {
	db      *sql.DB
	dialect string
}

func New(db *sql.DB, dialect string) *Extractor  { return &Extractor{db: db, dialect: dialect} }
func (e *Extractor) DB() *sql.DB                 { return e.db }
func (e *Extractor) Close(context.Context) error { return e.db.Close() }

func (e *Extractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	sch := opts.Schemas
	if len(sch) == 0 {
		if e.dialect == "mysql" {
			var current sql.NullString
			if err := e.db.QueryRowContext(ctx, "SELECT DATABASE()").Scan(&current); err != nil {
				return nil, fmt.Errorf("query current database: %w", err)
			}
			if !current.Valid || current.String == "" {
				return &schema.Database{}, nil
			}
			sch = []string{current.String}
		} else {
			sch = []string{"main"}
		}
	}
	var out schema.Database
	for _, name := range sch {
		tables, err := e.tables(ctx, name, opts)
		if err != nil {
			return nil, err
		}
		if len(tables) == 0 {
			continue
		}
		s := schema.Schema{Name: name}
		for i := range tables {
			if err := e.populate(ctx, &tables[i], opts); err != nil {
				return nil, err
			}
			s.Tables = append(s.Tables, tables[i])
		}
		if e.dialect == "mysql" {
			if err := e.mysqlRoutines(ctx, &s); err != nil {
				return nil, err
			}
		}
		if e.dialect == "duckdb" {
			if err := e.duckDBEnums(ctx, &s); err != nil {
				return nil, err
			}
		}
		out.Schemas = append(out.Schemas, s)
	}
	return &out, nil
}

func (e *Extractor) tables(ctx context.Context, namespace string, opts database.ExtractOptions) ([]schema.Table, error) {
	var q string
	if e.dialect == "sqlite" {
		q = fmt.Sprintf("SELECT name, type, '' FROM %s.sqlite_master WHERE type IN ('table'%s) AND name NOT LIKE 'sqlite_%%' ORDER BY name", quoteIdent(namespace), viewsSQL(opts.IncludeViews, e.dialect))
	} else if e.dialect == "mysql" {
		q = "SELECT table_name, table_type, COALESCE(table_comment,'') FROM information_schema.tables WHERE table_schema = ? AND table_type IN ('BASE TABLE','LOCAL TEMPORARY','TEMPORARY'" + viewsSQL(opts.IncludeViews, e.dialect) + ") ORDER BY table_name"
	} else {
		q = "SELECT table_name, table_type, COALESCE('','') FROM information_schema.tables WHERE table_schema = ? AND table_type IN ('BASE TABLE','LOCAL TEMPORARY','TEMPORARY'" + viewsSQL(opts.IncludeViews, e.dialect) + ") ORDER BY table_name"
	}
	args := []any{}
	if e.dialect == "duckdb" || e.dialect == "mysql" {
		args = append(args, namespace)
	}
	rows, err := e.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("query %s tables: %w", e.dialect, err)
	}
	defer rows.Close()
	var result []schema.Table
	for rows.Next() {
		var name, kind, comment string
		if err := rows.Scan(&name, &kind, &comment); err != nil {
			return nil, err
		}
		t := schema.Table{Schema: namespace, Name: name, Comment: comment}
		if kind == "VIEW" || kind == "VIEW TABLE" {
			t.Comment = ""
		}
		if excluded(t, opts.ExcludeTables) {
			continue
		}
		result = append(result, t)
	}
	return result, rows.Err()
}

func viewsSQL(include bool, dialect string) string {
	if include {
		if dialect == "sqlite" {
			return ", 'view'"
		}
		return ", 'VIEW'"
	}
	return ""
}

func (e *Extractor) populate(ctx context.Context, t *schema.Table, opts database.ExtractOptions) error {
	var err error
	if e.dialect == "sqlite" {
		err = e.populateSQLite(ctx, t, opts)
	} else {
		err = e.populateDuckDB(ctx, t, opts)
	}
	if err != nil {
		return err
	}
	if opts.ExampleSample > 0 && !excluded(*t, opts.ExcludeExampleTables) {
		return e.loadExample(ctx, t, opts)
	}
	return nil
}

func (e *Extractor) loadExample(ctx context.Context, t *schema.Table, opts database.ExtractOptions) error {
	rows, err := e.db.QueryContext(ctx, fmt.Sprintf("SELECT * FROM %s.%s LIMIT ?", e.quoteIdent(t.Schema), e.quoteIdent(t.Name)), opts.ExampleSample)
	if err != nil {
		return fmt.Errorf("query examples for %s.%s: %w", t.Schema, t.Name, err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return err
	}
	types, err := rows.ColumnTypes()
	if err != nil {
		return err
	}
	example := &schema.Example{}
	for i, name := range columns {
		if excludedField(t.Schema, t.Name, name, opts.ExcludeExampleFields) {
			continue
		}
		example.Columns = append(example.Columns, name)
		example.ColumnTypes = append(example.ColumnTypes, types[i].DatabaseTypeName())
	}
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(values))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return err
		}
		row := make([]any, 0, len(example.Columns))
		for i, name := range columns {
			if !excludedField(t.Schema, t.Name, name, opts.ExcludeExampleFields) {
				row = append(row, values[i])
			}
		}
		example.Rows = append(example.Rows, row)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(example.Columns) > 0 {
		t.Example = example
	}
	return nil
}

func (e *Extractor) populateSQLite(ctx context.Context, t *schema.Table, opts database.ExtractOptions) error {
	q := fmt.Sprintf("SELECT name, COALESCE(type,''), [notnull], COALESCE(dflt_value,''), pk FROM %s.pragma_table_xinfo(?) WHERE hidden=0 ORDER BY cid", quoteIdent(t.Schema))
	rows, err := e.db.QueryContext(ctx, q, t.Name)
	if err != nil {
		return fmt.Errorf("query columns for %s.%s: %w", t.Schema, t.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		var c schema.Column
		var notnull, pk int
		if err := rows.Scan(&c.Name, &c.NativeType, &notnull, &c.Default, &pk); err != nil {
			return err
		}
		c.Nullable = notnull == 0
		t.Columns = append(t.Columns, c)
		if pk > 0 {
			if t.PrimaryKey == nil {
				t.PrimaryKey = &schema.PrimaryKey{}
			}
			t.PrimaryKey.Columns = append(t.PrimaryKey.Columns, c.Name)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if t.PrimaryKey != nil {
		t.PrimaryKey.Name = t.Name + "_pkey"
	}
	if err := e.sqliteKeys(ctx, t, opts); err != nil {
		return err
	}
	if err := e.sqliteTriggers(ctx, t); err != nil {
		return err
	}
	var createSQL string
	if err := e.db.QueryRowContext(ctx, fmt.Sprintf("SELECT COALESCE(sql,'') FROM %s.sqlite_master WHERE name=?", quoteIdent(t.Schema)), t.Name).Scan(&createSQL); err == nil {
		t.Checks = sqliteChecks(t.Name, createSQL)
	}
	return nil
}

func (e *Extractor) sqliteKeys(ctx context.Context, t *schema.Table, opts database.ExtractOptions) error {
	rows, err := e.db.QueryContext(ctx, fmt.Sprintf("SELECT id, seq, [table], [from], [to], on_update, on_delete FROM %s.pragma_foreign_key_list(?) ORDER BY id, seq", quoteIdent(t.Schema)), t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	byID := map[int]int{}
	for rows.Next() {
		var id, seq int
		var ref, local, remote, up, del string
		if err := rows.Scan(&id, &seq, &ref, &local, &remote, &up, &del); err != nil {
			return err
		}
		position, ok := byID[id]
		if !ok {
			position = len(t.ForeignKeys)
			byID[id] = position
			t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{Name: fmt.Sprintf("%s_fk_%d", t.Name, id+1), ReferencedSchema: t.Schema, ReferencedTable: ref, OnUpdate: up, OnDelete: del})
		}
		t.ForeignKeys[position].LocalColumns = append(t.ForeignKeys[position].LocalColumns, local)
		t.ForeignKeys[position].ReferencedColumns = append(t.ForeignKeys[position].ReferencedColumns, remote)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	idx, err := e.db.QueryContext(ctx, fmt.Sprintf("SELECT name, [unique], origin FROM %s.pragma_index_list(?) ORDER BY name", quoteIdent(t.Schema)), t.Name)
	if err != nil {
		return err
	}
	defer idx.Close()
	for idx.Next() {
		var name, origin string
		var unique int
		if err := idx.Scan(&name, &unique, &origin); err != nil {
			return err
		}
		if origin == "pk" {
			continue
		}
		ir, err := e.db.QueryContext(ctx, fmt.Sprintf("SELECT name FROM %s.pragma_index_info(?) ORDER BY seqno", quoteIdent(t.Schema)), name)
		if err != nil {
			return err
		}
		var keys []string
		for ir.Next() {
			var k string
			if err := ir.Scan(&k); err != nil {
				ir.Close()
				return err
			}
			keys = append(keys, k)
		}
		ir.Close()
		if err := ir.Err(); err != nil {
			return err
		}
		i := schema.Index{Name: name, Unique: unique != 0, Method: "btree", Keys: keys}
		t.Indexes = append(t.Indexes, i)
		if i.Unique && origin == "u" {
			t.Uniques = append(t.Uniques, schema.UniqueConstraint{Name: name, Columns: keys})
		}
	}
	return idx.Err()
}

var sqliteTriggerEvent = regexp.MustCompile(`(?is)\b(BEFORE|AFTER|INSTEAD\s+OF)\s+(INSERT|UPDATE|DELETE)\b`)

func (e *Extractor) sqliteTriggers(ctx context.Context, t *schema.Table) error {
	rows, err := e.db.QueryContext(ctx, fmt.Sprintf("SELECT name, COALESCE(sql,'') FROM %s.sqlite_master WHERE type='trigger' AND tbl_name=? ORDER BY name", quoteIdent(t.Schema)), t.Name)
	if err != nil {
		return fmt.Errorf("query triggers for %s.%s: %w", t.Schema, t.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		trigger := schema.Trigger{Enabled: true}
		if err := rows.Scan(&trigger.Name, &trigger.Definition); err != nil {
			return fmt.Errorf("scan trigger for %s.%s: %w", t.Schema, t.Name, err)
		}
		if match := sqliteTriggerEvent.FindStringSubmatch(trigger.Definition); match != nil {
			trigger.Timing = strings.ToUpper(strings.Join(strings.Fields(match[1]), " "))
			trigger.Events = []string{strings.ToUpper(match[2])}
		}
		t.Triggers = append(t.Triggers, trigger)
	}
	return rows.Err()
}

func (e *Extractor) populateDuckDB(ctx context.Context, t *schema.Table, opts database.ExtractOptions) error {
	query := "SELECT column_name, data_type, is_nullable, COALESCE(column_default,'') FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position"
	if e.dialect == "mysql" {
		query = "SELECT column_name, data_type, is_nullable, COALESCE(column_default,''), COALESCE(column_comment,'') FROM information_schema.columns WHERE table_schema = ? AND table_name = ? ORDER BY ordinal_position"
	}
	rows, err := e.db.QueryContext(ctx, query, t.Schema, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c schema.Column
		var nullable string
		if e.dialect == "mysql" {
			if err := rows.Scan(&c.Name, &c.NativeType, &nullable, &c.Default, &c.Comment); err != nil {
				return err
			}
		} else if err := rows.Scan(&c.Name, &c.NativeType, &nullable, &c.Default); err != nil {
			return err
		}
		c.Nullable = nullable == "YES"
		t.Columns = append(t.Columns, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := e.duckConstraints(ctx, t, opts); err != nil {
		return err
	}
	if e.dialect == "mysql" {
		if err := e.mysqlForeignKeys(ctx, t); err != nil {
			return err
		}
		if err := e.mysqlChecks(ctx, t); err != nil {
			return err
		}
		if err := e.mysqlIndexes(ctx, t); err != nil {
			return err
		}
		return e.mysqlTriggers(ctx, t)
	}
	return nil
}

func (e *Extractor) mysqlChecks(ctx context.Context, t *schema.Table) error {
	rows, err := e.db.QueryContext(ctx, "SELECT c.constraint_name, c.check_clause FROM information_schema.check_constraints c JOIN information_schema.table_constraints tc ON tc.constraint_schema = c.constraint_schema AND tc.constraint_name = c.constraint_name WHERE c.constraint_schema = ? AND tc.table_name = ? ORDER BY c.constraint_name", t.Schema, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var check schema.CheckConstraint
		if err := rows.Scan(&check.Name, &check.Expression); err != nil {
			return err
		}
		t.Checks = append(t.Checks, check)
	}
	return rows.Err()
}

func (e *Extractor) mysqlIndexes(ctx context.Context, t *schema.Table) error {
	rows, err := e.db.QueryContext(ctx, "SELECT index_name, non_unique, seq_in_index, column_name, index_type FROM information_schema.statistics WHERE table_schema = ? AND table_name = ? AND index_name <> 'PRIMARY' ORDER BY index_name, seq_in_index", t.Schema, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	positions := map[string]int{}
	for rows.Next() {
		var name, column, method string
		var nonUnique, seq int
		if err := rows.Scan(&name, &nonUnique, &seq, &column, &method); err != nil {
			return err
		}
		position, ok := positions[name]
		if !ok {
			position = len(t.Indexes)
			positions[name] = position
			t.Indexes = append(t.Indexes, schema.Index{Name: name, Unique: nonUnique == 0, Method: strings.ToLower(method)})
		}
		t.Indexes[position].Keys = append(t.Indexes[position].Keys, column)
	}
	return rows.Err()
}

func (e *Extractor) mysqlForeignKeys(ctx context.Context, t *schema.Table) error {
	rows, err := e.db.QueryContext(ctx, `SELECT k.constraint_name, k.column_name, k.referenced_table_name, k.referenced_column_name, COALESCE(rc.update_rule, 'NO ACTION'), COALESCE(rc.delete_rule, 'NO ACTION')
FROM information_schema.key_column_usage k
LEFT JOIN information_schema.referential_constraints rc ON rc.constraint_schema = k.constraint_schema AND rc.constraint_name = k.constraint_name AND rc.table_name = k.table_name
WHERE k.table_schema = ? AND k.table_name = ? AND k.referenced_table_name IS NOT NULL ORDER BY k.constraint_name, k.ordinal_position`, t.Schema, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	positions := map[string]int{}
	for rows.Next() {
		var name, local, refTable, refColumn, update, delete string
		if err := rows.Scan(&name, &local, &refTable, &refColumn, &update, &delete); err != nil {
			return err
		}
		position, ok := positions[name]
		if !ok {
			position = len(t.ForeignKeys)
			positions[name] = position
			t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{Name: name, ReferencedSchema: t.Schema, ReferencedTable: refTable, OnUpdate: update, OnDelete: delete})
		}
		t.ForeignKeys[position].LocalColumns = append(t.ForeignKeys[position].LocalColumns, local)
		t.ForeignKeys[position].ReferencedColumns = append(t.ForeignKeys[position].ReferencedColumns, refColumn)
	}
	return rows.Err()
}

func (e *Extractor) mysqlTriggers(ctx context.Context, t *schema.Table) error {
	rows, err := e.db.QueryContext(ctx, `SELECT trigger_name, action_timing, event_manipulation, action_statement
FROM information_schema.triggers
WHERE trigger_schema = ? AND event_object_table = ?
ORDER BY trigger_name`, t.Schema, t.Name)
	if err != nil {
		return fmt.Errorf("query triggers for %s.%s: %w", t.Schema, t.Name, err)
	}
	defer rows.Close()
	for rows.Next() {
		trigger := schema.Trigger{Enabled: true}
		var event string
		if err := rows.Scan(&trigger.Name, &trigger.Timing, &event, &trigger.Definition); err != nil {
			return fmt.Errorf("scan trigger for %s.%s: %w", t.Schema, t.Name, err)
		}
		trigger.Timing = strings.ToUpper(trigger.Timing)
		trigger.Events = []string{strings.ToUpper(event)}
		t.Triggers = append(t.Triggers, trigger)
	}
	return rows.Err()
}

func (e *Extractor) mysqlRoutines(ctx context.Context, namespace *schema.Schema) error {
	rows, err := e.db.QueryContext(ctx, `SELECT r.routine_name,
       LOWER(r.routine_type),
       COALESCE(GROUP_CONCAT(CONCAT_WS(' ', p.parameter_mode, p.parameter_name, p.dtd_identifier)
                             ORDER BY p.ordinal_position SEPARATOR ', '), ''),
       COALESCE(r.dtd_identifier, ''),
       COALESCE(r.routine_body, ''),
       COALESCE(r.routine_definition, '')
FROM information_schema.routines r
LEFT JOIN information_schema.parameters p
  ON p.specific_schema = r.routine_schema
 AND p.specific_name = r.specific_name
 AND p.ordinal_position > 0
WHERE r.routine_schema = ?
GROUP BY r.specific_name, r.routine_name, r.routine_type, r.dtd_identifier, r.routine_body, r.routine_definition
ORDER BY r.routine_name, r.specific_name`, namespace.Name)
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

func (e *Extractor) duckDBEnums(ctx context.Context, namespace *schema.Schema) error {
	rows, err := e.db.QueryContext(ctx, `SELECT type_name, unnest(labels) AS label
FROM duckdb_types()
WHERE schema_name = ?
ORDER BY type_name`, namespace.Name)
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

func (e *Extractor) duckConstraints(ctx context.Context, t *schema.Table, opts database.ExtractOptions) error {
	rows, err := e.db.QueryContext(ctx, "SELECT constraint_name, constraint_type FROM information_schema.table_constraints WHERE table_schema = ? AND table_name = ? ORDER BY constraint_name", t.Schema, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name, typ string
		if err := rows.Scan(&name, &typ); err != nil {
			return err
		}
		kr, err := e.db.QueryContext(ctx, "SELECT column_name FROM information_schema.key_column_usage WHERE constraint_schema = ? AND table_name = ? AND constraint_name = ? ORDER BY ordinal_position", t.Schema, t.Name, name)
		if err != nil {
			return err
		}
		var cols []string
		for kr.Next() {
			var c string
			if err := kr.Scan(&c); err != nil {
				kr.Close()
				return err
			}
			cols = append(cols, c)
		}
		if err := kr.Err(); err != nil {
			kr.Close()
			return err
		}
		kr.Close()
		if typ == "PRIMARY KEY" {
			t.PrimaryKey = &schema.PrimaryKey{Name: name, Columns: cols}
		} else if typ == "UNIQUE" {
			t.Uniques = append(t.Uniques, schema.UniqueConstraint{Name: name, Columns: cols})
		}
	}
	return rows.Err()
}

func excluded(t schema.Table, xs []string) bool {
	for _, x := range xs {
		if x == t.Name || x == t.Schema+"."+t.Name {
			return true
		}
	}
	return false
}
func excludedField(s, t, field string, xs []string) bool {
	for _, x := range xs {
		if x == s+"."+t+"."+field || x == t+"."+field {
			return true
		}
	}
	return false
}

var checkStart = regexp.MustCompile(`(?i)\bcheck\s*\(`)

func sqliteChecks(table, createSQL string) []schema.CheckConstraint {
	var checks []schema.CheckConstraint
	for start := 0; ; {
		loc := checkStart.FindStringIndex(createSQL[start:])
		if loc == nil {
			break
		}
		open := start + loc[1] - 1
		depth := 0
		end := -1
		for i := open; i < len(createSQL); i++ {
			switch createSQL[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			break
		}
		checks = append(checks, schema.CheckConstraint{Name: fmt.Sprintf("%s_check_%d", table, len(checks)+1), Expression: "CHECK " + createSQL[open:end+1]})
		start = end + 1
	}
	return checks
}
func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

func (e *Extractor) quoteIdent(s string) string {
	if e.dialect == "mysql" {
		return "`" + strings.ReplaceAll(s, "`", "``") + "`"
	}
	return quoteIdent(s)
}
