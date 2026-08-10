package postgres

import (
	"context"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
)

type dumpState struct {
	tables      map[string]*schema.Table
	order       []string
	rows        map[string][][]any
	schemas     map[string]*schema.Schema
	schemaOrder []string
}

var qualifiedRE = regexp.MustCompile(`(?is)^(?:CREATE\s+TABLE\s+)(?:IF\s+NOT\s+EXISTS\s+)?(.+?)\s*\((.*)\)$`)
var alterRE = regexp.MustCompile(`(?is)^ALTER\s+TABLE\s+(?:ONLY\s+)?([^\s]+)\s+ADD\s+(?:CONSTRAINT\s+([^\s]+)\s+)?(.+)$`)

func parseDump(input string) (*dumpState, error) {
	d := &dumpState{tables: map[string]*schema.Table{}, rows: map[string][][]any{}, schemas: map[string]*schema.Schema{}}
	for _, stmt := range splitDumpStatements(input) {
		s := strings.TrimSpace(stmt.sql)
		if s == "" {
			continue
		}
		upper := strings.ToUpper(s)
		switch {
		case strings.HasPrefix(upper, "CREATE TABLE"):
			if err := d.createTable(s, stmt.line); err != nil {
				return nil, err
			}
		case strings.HasPrefix(upper, "ALTER TABLE"):
			if err := d.alterTable(s, stmt.line); err != nil {
				return nil, err
			}
		case strings.HasPrefix(upper, "COMMENT ON"):
			d.comment(s)
		case strings.HasPrefix(upper, "CREATE ") && strings.Contains(upper, " INDEX "):
			d.index(s)
		case strings.HasPrefix(upper, "INSERT INTO"):
			d.insert(s)
		case strings.HasPrefix(upper, "COPY "):
			d.copy(s)
		case strings.HasPrefix(upper, "CREATE TYPE") && strings.Contains(upper, " AS ENUM"):
			d.enum(s)
		case strings.HasPrefix(upper, "CREATE SEQUENCE"):
			d.sequence(s)
		case strings.HasPrefix(upper, "CREATE VIEW"), strings.HasPrefix(upper, "CREATE OR REPLACE VIEW"):
			d.view(s)
		case strings.HasPrefix(upper, "CREATE FUNCTION"), strings.HasPrefix(upper, "CREATE OR REPLACE FUNCTION"), strings.HasPrefix(upper, "CREATE PROCEDURE"), strings.HasPrefix(upper, "CREATE OR REPLACE PROCEDURE"):
			d.routine(s)
		case strings.HasPrefix(upper, "CREATE TRIGGER"):
			d.trigger(s)
		}
	}
	return d, nil
}

type dumpStatement struct {
	sql  string
	line int
}

func splitDumpStatements(input string) []dumpStatement {
	var out []dumpStatement
	start, line, startLine := 0, 1, 1
	quote, dollar := byte(0), ""
	for i := 0; i < len(input); i++ {
		c := input[i]
		if c == '\n' {
			line++
		}
		if dollar != "" {
			if strings.HasPrefix(input[i:], dollar) {
				i += len(dollar) - 1
				dollar = ""
			}
			continue
		}
		if quote != 0 {
			if c == quote {
				if i+1 < len(input) && input[i+1] == quote {
					i++
				} else {
					quote = 0
				}
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '$' {
			if end := strings.IndexByte(input[i+1:], '$'); end >= 0 {
				tag := input[i : i+end+2]
				if strings.IndexFunc(tag[1:len(tag)-1], func(r rune) bool {
					return !(r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9')
				}) < 0 {
					dollar = tag
					i += len(tag) - 1
					continue
				}
			}
		}
		if c == ';' {
			// COPY data follows the header and is terminated by a line containing
			// only \.; its contents are not SQL statements and may contain ;.
			if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(input[start:i])), "COPY ") {
				rest := input[i+1:]
				if marker := strings.Index(rest, "\n\\.\n"); marker >= 0 {
					end := i + 1 + marker + len("\n\\.")
					out = append(out, dumpStatement{input[start:end], startLine})
					line += strings.Count(input[i+1:end], "\n")
					start, startLine, i = end, line, end-1
					continue
				}
			}
			out = append(out, dumpStatement{input[start:i], startLine})
			start = i + 1
			startLine = line
		}
	}
	if strings.TrimSpace(input[start:]) != "" {
		out = append(out, dumpStatement{input[start:], startLine})
	}
	return out
}

func (d *dumpState) createTable(s string, line int) error {
	m := qualifiedRE.FindStringSubmatch(strings.TrimSpace(strings.TrimSuffix(s, ";")))
	if m == nil {
		return fmt.Errorf("PostgreSQL dump line %d: malformed CREATE TABLE", line)
	}
	schemaName, name := splitIdentifier(m[1])
	if schemaName == "" {
		schemaName = "public"
	}
	key := schemaName + "." + name
	t := &schema.Table{Schema: schemaName, Name: name}
	d.namespace(schemaName)
	d.tables[key] = t
	d.order = append(d.order, key)
	for _, part := range splitTopLevel(m[2], ',') {
		d.tablePart(t, strings.TrimSpace(part))
	}
	return nil
}
func (d *dumpState) tablePart(t *schema.Table, p string) {
	u := strings.ToUpper(p)
	name, rest := leadingIdentifier(p)
	if strings.HasPrefix(u, "CONSTRAINT ") {
		cname, tail := leadingIdentifier(strings.TrimSpace(p[len("CONSTRAINT "):]))
		d.constraint(t, cname, tail)
		return
	}
	if strings.HasPrefix(u, "PRIMARY KEY") || strings.HasPrefix(u, "FOREIGN KEY") || strings.HasPrefix(u, "UNIQUE") || strings.HasPrefix(u, "CHECK") {
		d.constraint(t, "", p)
		return
	}
	if name == "" {
		return
	}
	typ := strings.TrimSpace(rest)
	nullable := !strings.Contains(strings.ToUpper(typ), " NOT NULL")
	typ = removeColumnOptions(typ)
	col := schema.Column{Name: name, NativeType: typ, Nullable: nullable}
	up := strings.ToUpper(rest)
	if i := strings.Index(up, " DEFAULT "); i >= 0 {
		col.Default = strings.TrimSpace(rest[i+9:])
		col.NativeType = removeColumnOptions(strings.TrimSpace(rest[:i]))
	}
	if i := strings.Index(up, " GENERATED "); i >= 0 {
		col.Generated = strings.TrimSpace(rest[i+10:])
		col.NativeType = removeColumnOptions(strings.TrimSpace(rest[:i]))
	}
	if strings.Contains(up, " GENERATED ALWAYS AS IDENTITY") || strings.Contains(up, " GENERATED BY DEFAULT AS IDENTITY") {
		col.Identity = "a"
	}
	t.Columns = append(t.Columns, col)
}
func removeColumnOptions(s string) string {
	u := strings.ToUpper(s)
	for _, x := range []string{" NOT NULL", " NULL", " COLLATE ", " GENERATED ", " DEFAULT ", " PRIMARY KEY", " UNIQUE"} {
		if i := strings.Index(u, x); i >= 0 {
			s = s[:i]
			u = strings.ToUpper(s)
		}
	}
	return strings.TrimSpace(s)
}
func (d *dumpState) constraint(t *schema.Table, name, p string) {
	u := strings.ToUpper(p)
	cols := betweenParens(p)
	if strings.HasPrefix(u, "PRIMARY KEY") {
		t.PrimaryKey = &schema.PrimaryKey{Name: name, Columns: splitIdentifiers(cols)}
	} else if strings.HasPrefix(u, "UNIQUE") {
		t.Uniques = append(t.Uniques, schema.UniqueConstraint{Name: name, Columns: splitIdentifiers(cols)})
	} else if strings.HasPrefix(u, "CHECK") {
		t.Checks = append(t.Checks, schema.CheckConstraint{Name: name, Expression: p})
	} else if strings.HasPrefix(u, "FOREIGN KEY") {
		local := firstParenthesized(p)
		ref := regexp.MustCompile(`(?is)REFERENCES\s+([^\s(]+)\s*\(([^)]*)\)`).FindStringSubmatch(p)
		if len(ref) > 2 {
			rs, rn := splitIdentifier(ref[1])
			if rs == "" {
				rs = t.Schema
			}
			f := schema.ForeignKey{Name: name, LocalColumns: splitIdentifiers(local), ReferencedSchema: rs, ReferencedTable: rn, ReferencedColumns: splitIdentifiers(ref[2]), OnUpdate: action(p, "UPDATE"), OnDelete: action(p, "DELETE")}
			t.ForeignKeys = append(t.ForeignKeys, f)
		}
	}
}
func action(s, word string) string {
	re := regexp.MustCompile(`(?is)` + word + `\s+(NO ACTION|RESTRICT|CASCADE|SET NULL|SET DEFAULT)`)
	m := re.FindStringSubmatch(s)
	if len(m) > 1 {
		return strings.ToUpper(m[1])
	}
	return ""
}
func (d *dumpState) alterTable(s string, line int) error {
	m := alterRE.FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(s), ";"))
	if m == nil {
		return fmt.Errorf("PostgreSQL dump line %d: malformed ALTER TABLE", line)
	}
	key := normalizeQualified(m[1])
	t := d.tables[key]
	if t == nil {
		return nil
	}
	d.constraint(t, unquote(m[2]), m[3])
	return nil
}
func (d *dumpState) comment(s string) {
	re := regexp.MustCompile(`(?is)^COMMENT ON\s+(TABLE|COLUMN)\s+([^ ]+)\s+IS\s+(.+)$`)
	m := re.FindStringSubmatch(strings.TrimSpace(strings.TrimSuffix(s, ";")))
	if len(m) < 4 {
		return
	}
	value := sqlString(m[3])
	if m[1] == "TABLE" {
		if t := d.tables[normalizeQualified(m[2])]; t != nil {
			t.Comment = value
		}
	} else {
		q := splitDotted(m[2])
		if len(q) == 3 {
			if t := d.tables[q[0]+"."+q[1]]; t != nil {
				for i := range t.Columns {
					if t.Columns[i].Name == q[2] {
						t.Columns[i].Comment = value
					}
				}
			}
		}
	}
}
func (d *dumpState) index(s string) {
	re := regexp.MustCompile(`(?is)^CREATE\s+(UNIQUE\s+)?INDEX\s+([^ ]+)\s+ON\s+([^ (]+)(?:\s+USING\s+(\w+))?\s*\((.*)\)`)
	m := re.FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(s), ";"))
	if len(m) < 6 {
		return
	}
	if t := d.tables[normalizeQualified(m[3])]; t != nil {
		t.Indexes = append(t.Indexes, schema.Index{Name: unquote(m[2]), Unique: m[1] != "", Definition: s, Method: m[4], Keys: splitIdentifiers(m[5])})
	}
}
func (d *dumpState) insert(s string) {
	re := regexp.MustCompile(`(?is)^INSERT\s+INTO\s+([^ (]+)\s*\(([^)]*)\)\s+VALUES\s*(.*)$`)
	m := re.FindStringSubmatch(strings.TrimSuffix(strings.TrimSpace(s), ";"))
	if len(m) < 4 {
		return
	}
	key := normalizeQualified(m[1])
	t := d.tables[key]
	if t == nil {
		return
	}
	cols := splitIdentifiers(m[2])
	for _, group := range splitValueGroups(m[3]) {
		vals := parseValues(group)
		row := make([]any, len(t.Columns))
		for i, c := range cols {
			for j, col := range t.Columns {
				if col.Name == c && i < len(vals) {
					row[j] = convertDumpValue(vals[i], col.NativeType)
				}
			}
		}
		d.rows[key] = append(d.rows[key], row)
	}
}
func (d *dumpState) copy(s string) {
	lines := strings.Split(s, "\n")
	head := strings.TrimSpace(lines[0])
	fields := strings.Fields(head)
	if len(fields) < 2 {
		return
	}
	key := normalizeQualified(fields[1])
	t := d.tables[key]
	if t == nil {
		return
	}
	cols := t.Columns
	if i := strings.Index(head, "("); i >= 0 {
		if j := strings.Index(head[i:], ")"); j >= 0 {
			names := splitIdentifiers(head[i+1 : i+j])
			cols = nil
			for _, n := range names {
				for _, c := range t.Columns {
					if c.Name == n {
						cols = append(cols, c)
					}
				}
			}
		}
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "\\." {
			break
		}
		parts := strings.Split(line, "\t")
		row := make([]any, len(t.Columns))
		for i, v := range parts {
			if i < len(cols) {
				for j, c := range t.Columns {
					if c.Name == cols[i].Name {
						row[j] = convertCopyValue(v, c.NativeType)
					}
				}
			}
		}
		d.rows[key] = append(d.rows[key], row)
	}
}
func (d *dumpState) apply(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	out := &schema.Database{}
	allowed := map[string]bool{}
	for _, s := range opts.Schemas {
		allowed[s] = true
	}
	for _, name := range d.schemaOrder {
		if len(allowed) == 0 || allowed[name] {
			copied := *d.schemas[name]
			out.Schemas = append(out.Schemas, copied)
		}
	}
	for _, key := range d.order {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		t := *d.tables[key]
		if len(allowed) > 0 && !allowed[t.Schema] {
			continue
		}
		if excludedTable(t, opts.ExcludeTables) {
			continue
		}
		if opts.ExampleSample > 0 && !excludedTable(t, opts.ExcludeExampleTables) {
			rows := d.rows[key]
			if opts.ExampleSampleOrdered {
				sort.SliceStable(rows, func(i, j int) bool { return fmt.Sprint(rows[i]) < fmt.Sprint(rows[j]) })
			}
			if len(rows) > opts.ExampleSample {
				rows = rows[:opts.ExampleSample]
			}
			ex := &schema.Example{}
			for _, c := range t.Columns {
				if !excludedExampleField(t.Schema, t.Name, c.Name, opts.ExcludeExampleFields) {
					ex.Columns = append(ex.Columns, c.Name)
					ex.ColumnTypes = append(ex.ColumnTypes, c.NativeType)
				}
			}
			for _, r := range rows {
				var rr []any
				for i, c := range t.Columns {
					if !excludedExampleField(t.Schema, t.Name, c.Name, opts.ExcludeExampleFields) {
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
	return out, nil
}

func (d *dumpState) namespace(name string) *schema.Schema {
	if d.schemas[name] == nil {
		d.schemas[name] = &schema.Schema{Name: name}
		d.schemaOrder = append(d.schemaOrder, name)
	}
	return d.schemas[name]
}

func (d *dumpState) enum(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+TYPE\s+([^\s]+)\s+AS\s+ENUM\s*\((.*)\)`).FindStringSubmatch(s)
	if len(m) < 3 {
		return
	}
	sch, name := splitIdentifier(m[1])
	if sch == "" {
		sch = "public"
	}
	values := []string{}
	for _, v := range splitTopLevel(m[2], ',') {
		values = append(values, sqlString(v))
	}
	d.namespace(sch).Enums = append(d.namespace(sch).Enums, schema.Enum{Name: name, Values: values})
}
func (d *dumpState) sequence(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+SEQUENCE\s+([^\s]+)`).FindStringSubmatch(s)
	if len(m) < 2 {
		return
	}
	sch, name := splitIdentifier(m[1])
	if sch == "" {
		sch = "public"
	}
	q := schema.Sequence{Name: name, NativeType: "bigint"}
	if x := regexp.MustCompile(`(?is)\bINCREMENT\s+(?:BY\s+)?([^\s;]+)`).FindStringSubmatch(s); len(x) > 1 {
		q.Increment = x[1]
	}
	if x := regexp.MustCompile(`(?is)\bSTART\s+(?:WITH\s+)?([^\s;]+)`).FindStringSubmatch(s); len(x) > 1 {
		q.Start = x[1]
	}
	d.namespace(sch).Sequences = append(d.namespace(sch).Sequences, q)
}
func (d *dumpState) view(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+(?:OR\s+REPLACE\s+)?VIEW\s+([^\s]+)`).FindStringSubmatch(s)
	if len(m) < 2 {
		return
	}
	sch, name := splitIdentifier(m[1])
	if sch == "" {
		sch = "public"
	}
	d.namespace(sch)
	key := sch + "." + name
	d.tables[key] = &schema.Table{Schema: sch, Name: name, Kind: "view", Definition: s}
	d.order = append(d.order, key)
}
func (d *dumpState) routine(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+(?:OR\s+REPLACE\s+)?(FUNCTION|PROCEDURE)\s+([^\s(]+)`).FindStringSubmatch(s)
	if len(m) < 3 {
		return
	}
	sch, name := splitIdentifier(m[2])
	if sch == "" {
		sch = "public"
	}
	d.namespace(sch).Routines = append(d.namespace(sch).Routines, schema.Routine{Name: name, Kind: strings.ToLower(m[1]), Definition: s})
}
func (d *dumpState) trigger(s string) {
	m := regexp.MustCompile(`(?is)^CREATE\s+TRIGGER\s+([^\s]+).*?\bON\s+([^\s]+)`).FindStringSubmatch(s)
	if len(m) < 3 {
		return
	}
	key := normalizeQualified(m[2])
	t := d.tables[key]
	if t == nil {
		return
	}
	tr := schema.Trigger{Name: unquote(m[1]), Enabled: true, Definition: s}
	u := strings.ToUpper(s)
	if strings.Contains(u, "BEFORE") {
		tr.Timing = "BEFORE"
	} else if strings.Contains(u, "INSTEAD OF") {
		tr.Timing = "INSTEAD OF"
	} else {
		tr.Timing = "AFTER"
	}
	for _, event := range []string{"INSERT", "UPDATE", "DELETE", "TRUNCATE"} {
		if strings.Contains(u, event) {
			tr.Events = append(tr.Events, event)
		}
	}
	t.Triggers = append(t.Triggers, tr)
}

func splitTopLevel(s string, sep byte) []string {
	var out []string
	start, depth := 0, 0
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
		if s[i] == '\'' || s[i] == '"' {
			q = s[i]
		} else if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
		} else if s[i] == sep && depth == 0 {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	return append(out, s[start:])
}
func splitValueGroups(s string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range s {
		if c == '(' {
			if depth == 0 {
				start = i + 1
			}
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 {
				out = append(out, s[start:i])
			}
		}
	}
	return out
}
func parseValues(s string) []string { return splitTopLevel(s, ',') }
func splitIdentifiers(s string) []string {
	var out []string
	for _, v := range splitTopLevel(s, ',') {
		out = append(out, unquote(strings.TrimSpace(v)))
	}
	return out
}
func splitDotted(s string) []string {
	parts := splitTopLevel(s, '.')
	for i := range parts {
		parts[i] = unquote(strings.TrimSpace(parts[i]))
	}
	return parts
}
func leadingIdentifier(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	if s[0] == '"' {
		if i := strings.Index(s[1:], "\""); i >= 0 {
			return unquote(s[:i+2]), strings.TrimSpace(s[i+2:])
		}
	}
	i := strings.IndexAny(s, " \t\n(")
	if i < 0 {
		return unquote(s), ""
	}
	return unquote(s[:i]), strings.TrimSpace(s[i:])
}
func splitIdentifier(s string) (string, string) {
	s = strings.TrimSpace(s)
	parts := splitTopLevel(s, '.')
	if len(parts) == 1 {
		return "", unquote(parts[0])
	}
	return unquote(parts[len(parts)-2]), unquote(parts[len(parts)-1])
}
func normalizeQualified(s string) string {
	a, b := splitIdentifier(s)
	if a == "" {
		a = "public"
	}
	return a + "." + b
}
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		return strings.ReplaceAll(s[1:len(s)-1], `"`, `"`)
	}
	return s
}
func betweenParens(s string) string {
	i := strings.Index(s, "(")
	j := strings.LastIndex(s, ")")
	if i < 0 || j < i {
		return ""
	}
	return s[i+1 : j]
}
func firstParenthesized(s string) string {
	i := strings.IndexByte(s, '(')
	if i < 0 {
		return ""
	}
	depth := 0
	var quote byte
	for j := i; j < len(s); j++ {
		if quote != 0 {
			if s[j] == quote {
				quote = 0
			}
			continue
		}
		if s[j] == '\'' || s[j] == '"' {
			quote = s[j]
			continue
		}
		if s[j] == '(' {
			depth++
		}
		if s[j] == ')' {
			depth--
			if depth == 0 {
				return s[i+1 : j]
			}
		}
	}
	return ""
}
func sqlString(s string) string {
	s = strings.TrimSpace(s)
	if len(s) >= 2 && s[0] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}
func convertDumpValue(v, typ string) any {
	v = strings.TrimSpace(v)
	if strings.EqualFold(v, "NULL") {
		return nil
	}
	if len(v) >= 2 && v[0] == '\'' {
		v = sqlString(v)
	}
	typ = strings.ToLower(typ)
	if strings.Contains(typ, "int") {
		if n, e := strconv.ParseInt(v, 10, 64); e == nil {
			return n
		}
	}
	if strings.Contains(typ, "numeric") || strings.Contains(typ, "decimal") || strings.Contains(typ, "double") || strings.Contains(typ, "real") {
		if n, e := strconv.ParseFloat(v, 64); e == nil {
			return n
		}
	}
	if strings.Contains(typ, "bool") {
		return strings.EqualFold(v, "true") || v == "t" || v == "1"
	}
	if t, e := time.Parse(time.RFC3339, v); e == nil {
		return t
	}
	return v
}
func convertCopyValue(v, typ string) any {
	if v == `\N` {
		return nil
	}
	v = strings.ReplaceAll(v, `\t`, "\t")
	v = strings.ReplaceAll(v, `\n`, "\n")
	if strings.HasPrefix(v, `\\x`) {
		if b, e := hex.DecodeString(v[2:]); e == nil {
			return b
		}
	}
	return convertDumpValue(v, typ)
}
