package sqlutil

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
)

// DumpExtractor is the common file-backed implementation used by SQL
// dialects whose exports use ordinary CREATE/ALTER/INSERT statements.
type DumpExtractor struct{ path, dialect, defaultSchema string }

func NewDumpExtractor(ctx context.Context, path, dialect, defaultSchema string) (database.Extractor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("open %s dump: %w", dialect, err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("open %s dump: path is a directory", dialect)
	}
	return &DumpExtractor{path: path, dialect: dialect, defaultSchema: defaultSchema}, nil
}
func (e *DumpExtractor) Close(context.Context) error { return nil }
func (e *DumpExtractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	b, err := os.ReadFile(e.path)
	if err != nil {
		return nil, fmt.Errorf("read %s dump: %w", e.dialect, err)
	}
	defaultSchema := e.defaultSchema
	if defaultSchema == "" && len(opts.Schemas) == 1 {
		defaultSchema = opts.Schemas[0]
	}
	d := &genericDump{tables: map[string]*schema.Table{}, rows: map[string][][]any{}, schemas: map[string]*schema.Schema{}, defaultSchema: defaultSchema}
	if err := d.parse(string(b)); err != nil {
		return nil, err
	}
	return d.result(ctx, opts), nil
}

type genericDump struct {
	tables        map[string]*schema.Table
	order         []string
	rows          map[string][][]any
	schemas       map[string]*schema.Schema
	schemaOrder   []string
	defaultSchema string
	currentSchema string
}

func (d *genericDump) parse(input string) error {
	// SQL Server uses GO and Oracle uses a slash on its own line as batch
	// separators. Converting either to a semicolon lets the statement splitter
	// preserve the following CREATE statement after routine bodies.
	input = regexp.MustCompile(`(?m)^\s*(?:GO|/)\s*$`).ReplaceAllString(input, ";")
	for _, raw := range genericStatements(normalizeMySQLDelimiters(input)) {
		raw = strings.ReplaceAll(raw, "\x00", ";")
		s := strings.TrimSpace(raw)
		if s == "" {
			continue
		}
		u := strings.ToUpper(s)
		switch {
		case strings.HasPrefix(u, "USE "):
			d.currentSchema = genericUnquote(strings.TrimSpace(strings.TrimSuffix(s[4:], ";")))
		case strings.HasPrefix(u, "CREATE DATABASE"), strings.HasPrefix(u, "CREATE SCHEMA"):
			d.currentSchema = genericCreateNamespace(s)
		case strings.HasPrefix(u, "CREATE TABLE"):
			if err := d.createTable(s); err != nil {
				return err
			}
		case strings.HasPrefix(u, "ALTER TABLE"):
			d.alter(s)
		case strings.HasPrefix(u, "CREATE ") && strings.Contains(u, " INDEX "):
			d.index(s)
		case strings.HasPrefix(u, "COMMENT ON"):
			d.comment(s)
		case strings.HasPrefix(u, "INSERT INTO"):
			d.insert(s)
		case strings.HasPrefix(u, "CREATE VIEW"):
			d.view(s)
		case strings.HasPrefix(u, "CREATE FUNCTION"), strings.HasPrefix(u, "CREATE PROCEDURE"), strings.HasPrefix(u, "CREATE OR REPLACE FUNCTION"), strings.HasPrefix(u, "CREATE OR REPLACE PROCEDURE"):
			d.routine(s)
		case strings.HasPrefix(u, "CREATE TRIGGER"), strings.HasPrefix(u, "CREATE OR REPLACE TRIGGER"):
			d.trigger(s)
		case strings.HasPrefix(u, "CREATE SEQUENCE"):
			d.sequence(s)
		case strings.HasPrefix(u, "CREATE TYPE"):
			d.userType(s)
		case strings.HasPrefix(u, "CREATE SYNONYM"):
			d.synonym(s)
		case strings.HasPrefix(u, "CREATE MATERIALIZED VIEW"):
			d.object(s, "materialized_view", `(?is)^CREATE\s+MATERIALIZED\s+VIEW\s+([^\s]+)`)
		case strings.HasPrefix(u, "CREATE PACKAGE BODY"):
			d.object(s, "package_body", `(?is)^CREATE\s+PACKAGE\s+BODY\s+([^\s]+)`)
		case strings.HasPrefix(u, "CREATE PACKAGE"):
			d.object(s, "package", `(?is)^CREATE\s+PACKAGE\s+([^\s]+)`)
		case strings.HasPrefix(u, "CREATE DATABASE LINK"):
			d.object(s, "database_link", `(?is)^CREATE\s+DATABASE\s+LINK\s+([^\s]+)`)
		}
	}
	return nil
}

func (d *genericDump) createTable(s string) error {
	re := regexp.MustCompile(`(?is)^CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?(.+?)\s*\((.*)\)\s*;?$`)
	m := re.FindStringSubmatch(strings.TrimSpace(s))
	if len(m) < 3 {
		return fmt.Errorf("malformed CREATE TABLE")
	}
	sch, name := genericQualified(m[1], d)
	key := sch + "." + name
	t := &schema.Table{Schema: sch, Name: name}
	d.namespace(sch)
	if strings.Contains(strings.ToUpper(s), "PARTITION BY") {
		d.namespace(sch).Objects = append(d.namespace(sch).Objects, schema.Object{Name: name, Kind: "partitioned_table", Definition: s})
	}
	d.tables[key] = t
	d.order = append(d.order, key)
	for _, p := range genericSplit(m[2], ',') {
		d.part(t, strings.TrimSpace(p))
	}
	return nil
}
func (d *genericDump) part(t *schema.Table, p string) {
	u := strings.ToUpper(p)
	name, rest := genericLeading(p)
	if strings.HasPrefix(u, "CONSTRAINT ") {
		n, x := genericLeading(strings.TrimSpace(p[len("CONSTRAINT "):]))
		d.constraint(t, n, x)
		return
	}
	if strings.HasPrefix(u, "PRIMARY KEY") || strings.HasPrefix(u, "UNIQUE") || strings.HasPrefix(u, "FOREIGN KEY") || strings.HasPrefix(u, "CHECK") {
		d.constraint(t, "", p)
		return
	}
	if name == "" {
		return
	}
	col := schema.Column{Name: name, NativeType: genericType(rest), Nullable: !strings.Contains(strings.ToUpper(rest), " NOT NULL")}
	up := strings.ToUpper(rest)
	if i := strings.Index(up, " DEFAULT "); i >= 0 {
		col.Default = strings.TrimSpace(rest[i+9:])
	}
	if i := strings.Index(up, " GENERATED "); i >= 0 {
		col.Generated = strings.TrimSpace(rest[i+10:])
	}
	if strings.Contains(up, "AUTO_INCREMENT") || strings.Contains(up, "GENERATED ALWAYS AS IDENTITY") {
		col.Identity = "a"
	}
	if i := strings.Index(up, " COMMENT "); i >= 0 {
		col.Comment = genericSQLString(strings.TrimSpace(rest[i+9:]))
	}
	t.Columns = append(t.Columns, col)
}
func genericType(s string) string {
	u := strings.ToUpper(s)
	cut := len(s)
	for _, x := range []string{" NOT NULL", " NULL", " DEFAULT ", " COMMENT ", " AUTO_INCREMENT", " GENERATED ", " PRIMARY KEY", " UNIQUE"} {
		if i := strings.Index(u, x); i >= 0 && i < cut {
			cut = i
		}
	}
	return strings.TrimSpace(s[:cut])
}
func (d *genericDump) constraint(t *schema.Table, n, p string) {
	u := strings.ToUpper(p)
	cols := genericParen(p)
	switch {
	case strings.HasPrefix(u, "PRIMARY KEY"):
		t.PrimaryKey = &schema.PrimaryKey{Name: n, Columns: genericIdentifiers(cols)}
	case strings.HasPrefix(u, "UNIQUE"):
		t.Uniques = append(t.Uniques, schema.UniqueConstraint{Name: n, Columns: genericIdentifiers(cols)})
	case strings.HasPrefix(u, "CHECK"):
		t.Checks = append(t.Checks, schema.CheckConstraint{Name: n, Expression: p})
	case strings.HasPrefix(u, "FOREIGN KEY"):
		r := regexp.MustCompile(`(?is)REFERENCES\s+([^\s(]+)\s*\(([^)]*)\)`).FindStringSubmatch(p)
		if len(r) > 2 {
			rs, rn := genericQualified(r[1], d)
			t.ForeignKeys = append(t.ForeignKeys, schema.ForeignKey{Name: n, LocalColumns: genericIdentifiers(cols), ReferencedSchema: rs, ReferencedTable: rn, ReferencedColumns: genericIdentifiers(r[2]), OnUpdate: genericAction(p, "UPDATE"), OnDelete: genericAction(p, "DELETE")})
		}
	}
}
func genericAction(s, w string) string {
	m := regexp.MustCompile(`(?is)` + w + `\s+(NO ACTION|RESTRICT|CASCADE|SET NULL|SET DEFAULT)`).FindStringSubmatch(s)
	if len(m) > 1 {
		return strings.ToUpper(m[1])
	}
	return ""
}
func (d *genericDump) alter(s string) {
	m := regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+(?:ONLY\s+)?([^\s]+)\s+ADD\s+(?:CONSTRAINT\s+([^\s]+)\s+)?(.+)`).FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(s), ";"))
	if len(m) < 4 {
		return
	}
	key := d.key(m[1])
	if t := d.tables[key]; t != nil {
		d.constraint(t, genericUnquote(m[2]), m[3])
	}
}
func (d *genericDump) index(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+(UNIQUE\s+)?INDEX\s+([^\s]+)\s+ON\s+([^\s(]+)(?:\s+USING\s+(\w+))?\s*\((.*)\)`).FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(s), ";"))
	if len(m) < 6 {
		return
	}
	if t := d.tables[d.key(m[3])]; t != nil {
		t.Indexes = append(t.Indexes, schema.Index{Name: genericUnquote(m[2]), Unique: m[1] != "", Definition: s, Method: m[4], Keys: genericIdentifiers(m[5])})
	}
}
func (d *genericDump) view(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+(?:OR\s+REPLACE\s+)?VIEW\s+([^\s]+)`).FindStringSubmatch(s)
	if len(m) < 2 {
		return
	}
	sch, name := genericQualified(m[1], d)
	d.namespace(sch)
	t := &schema.Table{Schema: sch, Name: name, Kind: "view", Definition: s}
	d.tables[sch+"."+name] = t
	d.order = append(d.order, sch+"."+name)
}
func (d *genericDump) routine(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+(?:OR\s+REPLACE\s+)?(FUNCTION|PROCEDURE)\s+([^\s(]+)`).FindStringSubmatch(s)
	if len(m) < 3 {
		return
	}
	sch, name := genericQualified(m[2], d)
	d.namespace(sch).Routines = append(d.namespace(sch).Routines, schema.Routine{Name: name, Kind: strings.ToLower(m[1]), Definition: s})
}
func (d *genericDump) trigger(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+(?:OR\s+REPLACE\s+)?TRIGGER\s+([^\s]+)\s+(?:ON\s+)?([^\s]+)?\s*(.*)`).FindStringSubmatch(s)
	if len(m) < 3 {
		return
	}
	sch, name := genericQualified(m[1], d)
	body := m[3]
	tableMatch := regexp.MustCompile(`(?is)\bON\s+([^\s]+)`).FindStringSubmatch(s)
	if len(tableMatch) < 2 {
		tableMatch = regexp.MustCompile(`(?is)\b(?:BEFORE|AFTER|INSTEAD\s+OF)\s+(?:INSERT|UPDATE|DELETE).*?\bON\s+([^\s]+)`).FindStringSubmatch(s)
	}
	if len(tableMatch) < 2 {
		return
	}
	key := d.key(tableMatch[1])
	t := d.tables[key]
	if t == nil {
		return
	}
	tr := schema.Trigger{Name: name, Enabled: true, Definition: s}
	u := strings.ToUpper(s)
	if strings.Contains(u, "INSTEAD OF") {
		tr.Timing = "INSTEAD OF"
	} else if strings.Contains(u, "BEFORE") {
		tr.Timing = "BEFORE"
	} else {
		tr.Timing = "AFTER"
	}
	for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
		if strings.Contains(u, event) {
			tr.Events = append(tr.Events, event)
		}
	}
	_ = sch
	_ = body
	t.Triggers = append(t.Triggers, tr)
}
func (d *genericDump) sequence(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+SEQUENCE\s+([^\s]+)`).FindStringSubmatch(s)
	if len(m) < 2 {
		return
	}
	sch, name := genericQualified(m[1], d)
	q := schema.Sequence{Name: name, NativeType: "NUMBER"}
	if x := regexp.MustCompile(`(?is)\bINCREMENT\s+BY\s+([^\s;]+)`).FindStringSubmatch(s); len(x) > 1 {
		q.Increment = x[1]
	}
	if x := regexp.MustCompile(`(?is)\bSTART\s+WITH\s+([^\s;]+)`).FindStringSubmatch(s); len(x) > 1 {
		q.Start = x[1]
	}
	d.namespace(sch).Sequences = append(d.namespace(sch).Sequences, q)
}
func (d *genericDump) userType(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+TYPE\s+([^\s]+)\s+(?:AS\s+)?(?:OBJECT\s*)?(?:FROM\s+)?([^\s(]+)?`).FindStringSubmatch(s)
	if len(m) < 2 {
		return
	}
	sch, name := genericQualified(m[1], d)
	typ := schema.UserDefinedType{Name: name, Kind: "object"}
	if len(m) > 2 && m[2] != "" {
		typ.NativeType = m[2]
		if strings.Contains(strings.ToUpper(s), " FROM ") {
			typ.Kind = "alias"
		}
	}
	d.namespace(sch).Types = append(d.namespace(sch).Types, typ)
}
func (d *genericDump) synonym(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+SYNONYM\s+([^\s]+)\s+FOR\s+([^\s;]+)`).FindStringSubmatch(s)
	if len(m) < 3 {
		return
	}
	sch, name := genericQualified(m[1], d)
	d.namespace(sch).Synonyms = append(d.namespace(sch).Synonyms, schema.Synonym{Name: name, Target: m[2]})
}
func (d *genericDump) object(s, kind, expression string) {
	m := regexp.MustCompile(expression).FindStringSubmatch(s)
	if len(m) < 2 {
		return
	}
	sch, name := genericQualified(m[1], d)
	d.namespace(sch).Objects = append(d.namespace(sch).Objects, schema.Object{Name: name, Kind: kind, Definition: s})
}
func (d *genericDump) comment(s string) {
	m := regexp.MustCompile(`(?is)^COMMENT ON\s+(TABLE|COLUMN)\s+([^ ]+)\s+IS\s+(.+)`).FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(s), ";"))
	if len(m) < 4 {
		return
	}
	q := genericDotted(m[2])
	if m[1] == "TABLE" {
		if t := d.tables[d.key(m[2])]; t != nil {
			t.Comment = genericSQLString(m[3])
		}
	} else if len(q) == 3 {
		if t := d.tables[q[0]+"."+q[1]]; t != nil {
			for i := range t.Columns {
				if t.Columns[i].Name == q[2] {
					t.Columns[i].Comment = genericSQLString(m[3])
				}
			}
		}
	}
}
func (d *genericDump) insert(s string) {
	m := regexp.MustCompile(`(?is)^INSERT\s+INTO\s+([^ (]+)\s*(?:\(([^)]*)\))?\s+VALUES\s*(.*)`).FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(s), ";"))
	if len(m) < 4 {
		return
	}
	t := d.tables[d.key(m[1])]
	if t == nil {
		return
	}
	cols := genericIdentifiers(m[2])
	if len(cols) == 0 {
		for _, c := range t.Columns {
			cols = append(cols, c.Name)
		}
	}
	for _, g := range genericGroups(m[3]) {
		v := genericSplit(g, ',')
		row := make([]any, len(t.Columns))
		for i, n := range cols {
			for j, c := range t.Columns {
				if c.Name == n && i < len(v) {
					row[j] = genericValue(v[i], c.NativeType)
				}
			}
		}
		d.rows[d.key(m[1])] = append(d.rows[d.key(m[1])], row)
	}
}
func (d *genericDump) result(ctx context.Context, o database.ExtractOptions) *schema.Database {
	out := &schema.Database{}
	allow := map[string]bool{}
	for _, s := range o.Schemas {
		allow[s] = true
	}
	for _, name := range d.schemaOrder {
		if len(allow) > 0 && !allow[name] {
			continue
		}
		copied := *d.schemas[name]
		out.Schemas = append(out.Schemas, copied)
	}
	for _, k := range d.order {
		if ctx.Err() != nil {
			break
		}
		t := *d.tables[k]
		if len(allow) > 0 && !allow[t.Schema] {
			continue
		}
		if genericExcluded(t, o.ExcludeTables) {
			continue
		}
		if o.ExampleSample > 0 && !genericExcluded(t, o.ExcludeExampleTables) {
			rows := d.rows[k]
			if o.ExampleSampleOrdered {
				sort.SliceStable(rows, func(i, j int) bool { return fmt.Sprint(rows[i]) < fmt.Sprint(rows[j]) })
			}
			if len(rows) > o.ExampleSample {
				rows = rows[:o.ExampleSample]
			}
			ex := &schema.Example{}
			for _, c := range t.Columns {
				if !genericFieldExcluded(t, c.Name, o.ExcludeExampleFields) {
					ex.Columns = append(ex.Columns, c.Name)
					ex.ColumnTypes = append(ex.ColumnTypes, c.NativeType)
				}
			}
			for _, r := range rows {
				rr := []any{}
				for i, c := range t.Columns {
					if !genericFieldExcluded(t, c.Name, o.ExcludeExampleFields) {
						rr = append(rr, r[i])
					}
				}
				ex.Rows = append(ex.Rows, rr)
			}
			if len(ex.Columns) > 0 {
				t.Example = ex
			}
		}
		for i := range out.Schemas {
			if out.Schemas[i].Name == t.Schema {
				out.Schemas[i].Tables = append(out.Schemas[i].Tables, t)
				break
			}
		}
	}
	return out
}

func (d *genericDump) namespace(name string) *schema.Schema {
	if d.schemas[name] == nil {
		d.schemas[name] = &schema.Schema{Name: name}
		d.schemaOrder = append(d.schemaOrder, name)
	}
	return d.schemas[name]
}

func (d *genericDump) key(s string) string { a, b := genericQualified(s, d); return a + "." + b }
func genericQualified(s string, d *genericDump) (string, string) {
	q := genericDotted(strings.TrimSpace(s))
	if len(q) > 1 {
		return q[len(q)-2], q[len(q)-1]
	}
	sch := d.currentSchema
	if sch == "" {
		sch = d.defaultSchema
	}
	return sch, genericUnquote(q[0])
}
func genericCreateNamespace(s string) string {
	f := strings.Fields(s)
	if len(f) < 3 {
		return ""
	}
	return genericUnquote(strings.TrimSuffix(f[2], ";"))
}
func genericDotted(s string) []string {
	p := genericSplitByte(s, '.')
	for i := range p {
		p[i] = genericUnquote(strings.TrimSpace(p[i]))
	}
	return p
}
func genericUnquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 1 && ((s[0] == '`' && s[len(s)-1] == '`') || (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '[' && s[len(s)-1] == ']')) {
		return s[1 : len(s)-1]
	}
	return s
}
func genericLeading(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if s[0] == '`' || s[0] == '"' || s[0] == '[' {
		q := s[0]
		end := byte(q)
		if q == '[' {
			end = ']'
		}
		if i := strings.IndexByte(s[1:], end); i >= 0 {
			return genericUnquote(s[:i+2]), strings.TrimSpace(s[i+2:])
		}
	}
	i := strings.IndexAny(s, " \t\n(")
	if i < 0 {
		return genericUnquote(s), ""
	}
	return genericUnquote(s[:i]), strings.TrimSpace(s[i:])
}
func genericParen(s string) string {
	i := strings.IndexByte(s, '(')
	j := strings.LastIndexByte(s, ')')
	if i < 0 || j < i {
		return ""
	}
	return s[i+1 : j]
}
func genericIdentifiers(s string) []string {
	var r []string
	for _, p := range genericSplit(s, ',') {
		if strings.TrimSpace(p) != "" {
			r = append(r, genericUnquote(strings.TrimSpace(p)))
		}
	}
	return r
}
func genericSplit(s string, sep byte) []string { return genericSplitByte(s, sep) }
func genericSplitByte(s string, sep byte) []string {
	var r []string
	start, depth := 0, 0
	var q byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if q != 0 {
			if c == q {
				if i+1 < len(s) && s[i+1] == q {
					i++
				} else {
					q = 0
				}
			}
			continue
		}
		if c == '\'' || c == '"' || c == '`' || c == '[' {
			q = c
			continue
		}
		if c == '(' {
			depth++
		}
		if c == ')' {
			depth--
		}
		if c == sep && depth == 0 {
			r = append(r, s[start:i])
			start = i + 1
		}
	}
	return append(r, s[start:])
}
func genericGroups(s string) []string {
	var r []string
	for _, p := range genericSplit(s, ',') {
		p = strings.TrimSpace(p)
		if strings.HasPrefix(p, "(") && strings.HasSuffix(p, ")") {
			r = append(r, p[1:len(p)-1])
		}
	}
	return r
}
func genericSQLString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}
func genericValue(s, typ string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.EqualFold(s, "NULL") {
		return nil
	}
	if s[0:1] == "'" {
		s = genericSQLString(s)
	}
	l := strings.ToLower(typ)
	if strings.Contains(l, "int") {
		if n, e := strconv.ParseInt(s, 10, 64); e == nil {
			return n
		}
	}
	if strings.Contains(l, "real") || strings.Contains(l, "double") || strings.Contains(l, "float") || strings.Contains(l, "decimal") || strings.Contains(l, "numeric") {
		if n, e := strconv.ParseFloat(s, 64); e == nil {
			return n
		}
	}
	if l == "boolean" || strings.Contains(l, "bool") {
		return strings.EqualFold(s, "true") || s == "1"
	}
	return s
}
func genericExcluded(t schema.Table, x []string) bool {
	for _, v := range x {
		if v == t.Name || v == t.Schema+"."+t.Name {
			return true
		}
	}
	return false
}
func genericFieldExcluded(t schema.Table, n string, x []string) bool {
	for _, v := range x {
		if v == t.Schema+"."+t.Name+"."+n || v == t.Name+"."+n {
			return true
		}
	}
	return false
}
func genericStatements(s string) []string {
	var r []string
	start := 0
	var q byte
	for i := 0; i < len(s); i++ {
		if q != 0 {
			if s[i] == q {
				if i+1 < len(s) && s[i+1] == q {
					i++
				} else {
					q = 0
				}
			}
			continue
		}
		if s[i] == '\'' || s[i] == '"' || s[i] == '`' {
			q = s[i]
			continue
		}
		if s[i] == ';' {
			r = append(r, s[start:i])
			start = i + 1
		}
	}
	if strings.TrimSpace(s[start:]) != "" {
		r = append(r, s[start:])
	}
	return r
}

// MySQL routine dumps switch DELIMITER so semicolons inside a body are not
// statement terminators. Protect those semicolons before generic splitting.
func normalizeMySQLDelimiters(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	delimiter := ";"
	var out strings.Builder
	for _, line := range strings.SplitAfter(input, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(strings.ToUpper(trimmed), "DELIMITER ") {
			delimiter = strings.TrimSpace(trimmed[len("DELIMITER "):])
			continue
		}
		if delimiter != ";" {
			line = strings.ReplaceAll(line, ";", "\x00")
			withoutNL := strings.TrimRight(line, "\r\n")
			if strings.HasSuffix(withoutNL, delimiter) {
				line = strings.TrimSuffix(withoutNL, delimiter) + ";" + line[len(withoutNL):]
			}
		}
		out.WriteString(line)
	}
	return out.String()
}
