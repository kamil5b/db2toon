package oracle

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
	_ "github.com/sijms/go-ora/v2"
)

// Extractor reads user-visible Oracle metadata through ALL_* catalog views.
type Extractor struct{ db *sql.DB }

func New(ctx context.Context, dsn string) (*Extractor, error) {
	db, err := sql.Open("oracle", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Extractor{db: db}, nil
}

func (e *Extractor) Close(context.Context) error { return e.db.Close() }

func (e *Extractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	schemas := opts.Schemas
	if len(schemas) == 0 {
		var owner string
		if err := e.db.QueryRowContext(ctx, "SELECT SYS_CONTEXT('USERENV', 'CURRENT_SCHEMA') FROM dual").Scan(&owner); err != nil {
			return nil, err
		}
		schemas = []string{owner}
	}
	db := &schema.Database{Schemas: make([]schema.Schema, 0, len(schemas))}
	for _, owner := range schemas {
		s, err := e.extractSchema(ctx, strings.ToUpper(owner), opts)
		if err != nil {
			return nil, err
		}
		db.Schemas = append(db.Schemas, s)
	}
	return db, nil
}

func (e *Extractor) extractSchema(ctx context.Context, owner string, opts database.ExtractOptions) (schema.Schema, error) {
	s := schema.Schema{Name: owner}
	if err := e.loadSequences(ctx, &s); err != nil {
		return s, err
	}
	if err := e.loadRoutines(ctx, &s); err != nil {
		return s, err
	}
	rows, err := e.db.QueryContext(ctx, `SELECT o.object_name,o.object_type,c.comments FROM all_objects o LEFT JOIN all_tab_comments c ON c.owner=o.owner AND c.table_name=o.object_name WHERE o.owner=:1 AND o.object_type IN ('TABLE','VIEW') AND (:2=1 OR o.object_type='TABLE') ORDER BY o.object_name`, owner, boolToInt(opts.IncludeViews))
	if err != nil {
		return s, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, kind string
		var comment sql.NullString
		if err := rows.Scan(&name, &kind, &comment); err != nil {
			return s, err
		}
		if excluded(opts.ExcludeTables, owner, name) {
			continue
		}
		t := schema.Table{Schema: owner, Name: name, Comment: comment.String}
		if kind == "VIEW" {
			t.Kind = "view"
		}
		if err := e.loadColumns(ctx, &t); err != nil {
			return s, err
		}
		if kind == "TABLE" {
			if err := e.loadConstraints(ctx, &t); err != nil {
				return s, err
			}
			if err := e.loadIndexes(ctx, &t); err != nil {
				return s, err
			}
			if err := e.loadTriggers(ctx, &t); err != nil {
				return s, err
			}
			if opts.ExampleSample > 0 && !excluded(opts.ExcludeExampleTables, owner, name) {
				if err := e.loadExample(ctx, &t, opts); err != nil {
					return s, err
				}
			}
		}
		s.Tables = append(s.Tables, t)
	}
	return s, rows.Err()
}

func (e *Extractor) loadColumns(ctx context.Context, t *schema.Table) error {
	rows, err := e.db.QueryContext(ctx, `SELECT c.column_name, c.data_type || CASE WHEN c.data_type IN ('VARCHAR2','NVARCHAR2','CHAR','NCHAR') THEN '(' || c.char_length || ')' WHEN c.data_type='NUMBER' AND c.data_precision IS NOT NULL THEN '(' || c.data_precision || CASE WHEN c.data_scale IS NOT NULL THEN ',' || c.data_scale END || ')' ELSE '' END, c.nullable, c.data_default, cm.comments FROM all_tab_columns c LEFT JOIN all_col_comments cm ON cm.owner=c.owner AND cm.table_name=c.table_name AND cm.column_name=c.column_name WHERE c.owner=:1 AND c.table_name=:2 ORDER BY c.column_id`, t.Schema, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var c schema.Column
		var nullable string
		var defaultValue, comment sql.NullString
		if err := rows.Scan(&c.Name, &c.NativeType, &nullable, &defaultValue, &comment); err != nil {
			return err
		}
		c.Nullable = nullable == "Y"
		c.Default, c.Comment = defaultValue.String, comment.String
		t.Columns = append(t.Columns, c)
	}
	return rows.Err()
}

func (e *Extractor) loadConstraints(ctx context.Context, t *schema.Table) error {
	rows, err := e.db.QueryContext(ctx, `SELECT ac.constraint_name,ac.constraint_type,acc.column_name,ac.r_owner,ac.r_constraint_name,ac.delete_rule,ac.search_condition FROM all_constraints ac LEFT JOIN all_cons_columns acc ON acc.owner=ac.owner AND acc.constraint_name=ac.constraint_name WHERE ac.owner=:1 AND ac.table_name=:2 AND ac.constraint_type IN ('P','U','R','C') ORDER BY ac.constraint_name,acc.position`, t.Schema, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	type item struct {
		kind, ro, rc, del, expr string
		cols                    []string
	}
	items := map[string]*item{}
	order := []string{}
	for rows.Next() {
		var n string
		var i item
		var col sql.NullString
		var ro, rc, del, expr sql.NullString
		if err := rows.Scan(&n, &i.kind, &col, &ro, &rc, &del, &expr); err != nil {
			return err
		}
		i.ro, i.rc, i.del, i.expr = ro.String, rc.String, del.String, expr.String
		if items[n] == nil {
			items[n] = &i
			order = append(order, n)
		}
		if col.Valid {
			items[n].cols = append(items[n].cols, col.String)
		}
	}
	for _, n := range order {
		i := items[n]
		switch i.kind {
		case "P":
			t.PrimaryKey = &schema.PrimaryKey{Name: n, Columns: i.cols}
		case "U":
			t.Uniques = append(t.Uniques, schema.UniqueConstraint{Name: n, Columns: i.cols})
		case "C":
			t.Checks = append(t.Checks, schema.CheckConstraint{Name: n, Expression: i.expr})
		case "R":
			var rt string
			err := e.db.QueryRowContext(ctx, "SELECT table_name FROM all_constraints WHERE owner=:1 AND constraint_name=:2", i.ro, i.rc).Scan(&rt)
			if err != nil {
				return err
			}
			t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{Name: n, LocalColumns: i.cols, ReferencedSchema: i.ro, ReferencedTable: rt, OnDelete: i.del})
		}
	}
	return rows.Err()
}

func (e *Extractor) loadIndexes(ctx context.Context, t *schema.Table) error {
	rows, err := e.db.QueryContext(ctx, `SELECT i.index_name,i.uniqueness,i.index_type,ic.column_name FROM all_indexes i JOIN all_ind_columns ic ON ic.index_owner=i.owner AND ic.index_name=i.index_name WHERE i.table_owner=:1 AND i.table_name=:2 ORDER BY i.index_name,ic.column_position`, t.Schema, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	by := map[string]*schema.Index{}
	order := []string{}
	for rows.Next() {
		var n, u, m, c string
		if err := rows.Scan(&n, &u, &m, &c); err != nil {
			return err
		}
		if by[n] == nil {
			by[n] = &schema.Index{Name: n, Unique: u == "UNIQUE", Method: strings.ToLower(m)}
			order = append(order, n)
		}
		by[n].Keys = append(by[n].Keys, c)
	}
	for _, n := range order {
		t.Indexes = append(t.Indexes, *by[n])
	}
	return rows.Err()
}
func (e *Extractor) loadTriggers(ctx context.Context, t *schema.Table) error {
	rows, err := e.db.QueryContext(ctx, `SELECT trigger_name,triggering_event,trigger_type,status,trigger_body FROM all_triggers WHERE table_owner=:1 AND table_name=:2 ORDER BY trigger_name`, t.Schema, t.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var tr schema.Trigger
		var events, typ, status string
		if err := rows.Scan(&tr.Name, &events, &typ, &status, &tr.Definition); err != nil {
			return err
		}
		tr.Enabled = status == "ENABLED"
		if strings.Contains(typ, "BEFORE") {
			tr.Timing = "BEFORE"
		} else if strings.Contains(typ, "INSTEAD OF") {
			tr.Timing = "INSTEAD OF"
		} else {
			tr.Timing = "AFTER"
		}
		tr.Events = strings.Fields(strings.ReplaceAll(events, " OR ", " "))
		t.Triggers = append(t.Triggers, tr)
	}
	return rows.Err()
}
func (e *Extractor) loadRoutines(ctx context.Context, s *schema.Schema) error {
	rows, err := e.db.QueryContext(ctx, `SELECT object_name,object_type FROM all_procedures WHERE owner=:1 AND object_type IN ('FUNCTION','PROCEDURE') AND subprogram_id=0 ORDER BY object_name`, s.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var r schema.Routine
		if err := rows.Scan(&r.Name, &r.Kind); err != nil {
			return err
		}
		r.Kind = strings.ToLower(r.Kind)
		s.Routines = append(s.Routines, r)
	}
	return rows.Err()
}
func (e *Extractor) loadSequences(ctx context.Context, s *schema.Schema) error {
	rows, err := e.db.QueryContext(ctx, `SELECT sequence_name,TO_CHAR(min_value),TO_CHAR(max_value),TO_CHAR(increment_by),cycle_flag FROM all_sequences WHERE sequence_owner=:1 ORDER BY sequence_name`, s.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var q schema.Sequence
		var cycle string
		q.NativeType = "NUMBER"
		if err := rows.Scan(&q.Name, &q.Minimum, &q.Maximum, &q.Increment, &cycle); err != nil {
			return err
		}
		q.Cyclic = cycle == "Y"
		s.Sequences = append(s.Sequences, q)
	}
	return rows.Err()
}
func (e *Extractor) loadExample(ctx context.Context, t *schema.Table, opts database.ExtractOptions) error {
	cols := []string{}
	for _, c := range t.Columns {
		if !excludedField(opts.ExcludeExampleFields, t.Schema, t.Name, c.Name) {
			cols = append(cols, c.Name)
		}
	}
	if len(cols) == 0 {
		return nil
	}
	query := fmt.Sprintf("SELECT %s FROM %s WHERE ROWNUM <= :1", quoted(cols), quote(t.Schema)+"."+quote(t.Name))
	rows, err := e.db.QueryContext(ctx, query, opts.ExampleSample)
	if err != nil {
		return err
	}
	defer rows.Close()
	ex := &schema.Example{Columns: cols}
	for rows.Next() {
		v := make([]any, len(cols))
		d := make([]any, len(cols))
		for i := range v {
			d[i] = &v[i]
		}
		if err := rows.Scan(d...); err != nil {
			return err
		}
		ex.Rows = append(ex.Rows, v)
	}
	t.Example = ex
	return rows.Err()
}
func quote(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func quoted(xs []string) string {
	r := make([]string, len(xs))
	for i, x := range xs {
		r[i] = quote(x)
	}
	return strings.Join(r, ",")
}
func boolToInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
func excluded(xs []string, s, n string) bool {
	for _, x := range xs {
		if strings.EqualFold(strings.TrimSpace(x), n) || strings.EqualFold(strings.TrimSpace(x), s+"."+n) {
			return true
		}
	}
	return false
}
func excludedField(xs []string, s, t, c string) bool {
	for _, x := range xs {
		if strings.EqualFold(strings.TrimSpace(x), s+"."+t+"."+c) {
			return true
		}
	}
	return false
}

var _ database.Extractor = (*Extractor)(nil)
