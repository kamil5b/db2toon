// Package dbml parses DBML schemas into db2toon's canonical schema model.
package dbml

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/kamil5b/db2toon/pkg/schema"
)

var tableHeader = regexp.MustCompile(`(?i)^table\s+([^\s\[]+)`)
var referenceOperator = regexp.MustCompile(`\s+\??\s*(<>|>|<|-)\s*\??\s+`)

// Parse reads a DBML document. It supports tables, column settings, indexes,
// notes, and inline or top-level references.
func Parse(r io.Reader) (*schema.Database, error) {
	if r == nil {
		return nil, fmt.Errorf("dbml: nil reader")
	}
	p := parser{db: &schema.Database{}}
	s := bufio.NewScanner(r)
	// DBML defaults and notes can be considerably longer than Scanner's default.
	s.Buffer(make([]byte, 4096), 1024*1024)
	for s.Scan() {
		p.line++
		line := strings.TrimSpace(stripComment(s.Text()))
		if line == "" {
			continue
		}
		if err := p.consume(line); err != nil {
			return nil, fmt.Errorf("dbml: line %d: %w", p.line, err)
		}
	}
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("dbml: read: %w", err)
	}
	if p.table != nil {
		return nil, fmt.Errorf("dbml: unterminated table %q", p.table.Name)
	}
	for _, ref := range p.refs {
		if err := p.addReference(ref); err != nil {
			return nil, err
		}
	}
	return p.db, nil
}

type parser struct {
	db        *schema.Database
	table     *schema.Table
	inIndexes bool
	line      int
	refs      []string
	pk        []string
}

func (p *parser) consume(line string) error {
	low := strings.ToLower(line)
	if p.table == nil {
		if strings.HasPrefix(low, "project ") || strings.HasPrefix(low, "enum ") || line == "{" || line == "}" {
			return nil
		}
		if strings.HasPrefix(low, "ref") {
			p.refs = append(p.refs, line)
			return nil
		}
		m := tableHeader.FindStringSubmatch(line)
		if m == nil {
			return nil
		} // DBML permits project-level metadata.
		name := unquoteIdentifier(m[1])
		namespace, name := splitName(name)
		t := schema.Table{Schema: namespace, Name: name}
		p.schema(namespace).Tables = append(p.schema(namespace).Tables, t)
		p.table = &p.schema(namespace).Tables[len(p.schema(namespace).Tables)-1]
		p.pk = nil
		return nil
	}
	if p.inIndexes {
		if line == "}" {
			p.inIndexes = false
			return nil
		}
		return p.index(line)
	}
	if line == "}" {
		if len(p.pk) > 0 {
			p.table.PrimaryKey = &schema.PrimaryKey{Columns: append([]string(nil), p.pk...)}
		}
		p.table = nil
		return nil
	}
	if strings.HasPrefix(low, "indexes") {
		p.inIndexes = true
		return nil
	}
	if strings.HasPrefix(low, "note:") {
		p.table.Comment = scalar(strings.TrimSpace(line[len("note:"):]))
		return nil
	}
	return p.column(line)
}

func (p *parser) column(line string) error {
	base, settings := splitSettings(line)
	fields := fieldsQuoted(base)
	if len(fields) < 2 {
		return fmt.Errorf("invalid column declaration %q", line)
	}
	c := schema.Column{Name: unquoteIdentifier(fields[0]), NativeType: strings.Join(fields[1:], " "), Nullable: true}
	for _, setting := range splitComma(settings) {
		setting = strings.TrimSpace(setting)
		low := strings.ToLower(setting)
		switch {
		case low == "pk" || low == "primary key":
			p.pk = append(p.pk, c.Name)
		case low == "not null":
			c.Nullable = false
		case low == "null":
			c.Nullable = true
		case low == "increment":
			c.Identity = "d"
		case low == "unique":
			p.table.Uniques = append(p.table.Uniques, schema.UniqueConstraint{Name: c.Name + "_unique", Columns: []string{c.Name}})
		case strings.HasPrefix(low, "default:"):
			c.Default = scalar(strings.TrimSpace(setting[len("default:"):]))
		case strings.HasPrefix(low, "note:"):
			c.Comment = scalar(strings.TrimSpace(setting[len("note:"):]))
		case strings.HasPrefix(low, "ref:"):
			p.refs = append(p.refs, "Ref: "+p.fullTable()+"."+c.Name+" "+strings.TrimSpace(setting[len("ref:"):]))
		}
	}
	p.table.Columns = append(p.table.Columns, c)
	return nil
}

func (p *parser) index(line string) error {
	base, settings := splitSettings(line)
	cols := trimParens(strings.TrimSpace(base))
	idx := schema.Index{Keys: splitNames(cols), Method: "btree"}
	for _, setting := range splitComma(settings) {
		s := strings.TrimSpace(setting)
		low := strings.ToLower(s)
		if low == "unique" {
			idx.Unique = true
		}
		if strings.HasPrefix(low, "name:") {
			idx.Name = scalar(strings.TrimSpace(s[len("name:"):]))
		}
		if strings.HasPrefix(low, "type:") {
			idx.Method = scalar(strings.TrimSpace(s[len("type:"):]))
		}
	}
	if idx.Name == "" {
		idx.Name = "idx_" + p.table.Name + "_" + strings.Join(idx.Keys, "_")
	}
	idx.Definition = indexDefinition(p.table.Name, idx)
	p.table.Indexes = append(p.table.Indexes, idx)
	return nil
}

func (p *parser) addReference(line string) error {
	colon := strings.Index(line, ":")
	if colon < 0 {
		return fmt.Errorf("dbml: invalid reference %q", line)
	}
	expr, settings := splitSettings(strings.TrimSpace(line[colon+1:]))
	operator := referenceOperator.FindStringSubmatchIndex(expr)
	if operator == nil {
		return fmt.Errorf("dbml: invalid reference %q", line)
	}
	op := expr[operator[2]:operator[3]]
	left, err := endpoint(expr[:operator[0]])
	if err != nil {
		return err
	}
	right, err := endpoint(expr[operator[1]:])
	if err != nil {
		return err
	}
	if op == "<" {
		left, right = right, left
	}
	local := p.findTable(left.schema, left.table)
	if local == nil {
		return fmt.Errorf("dbml: reference table %q not found", left.qualified())
	}
	fk := schema.ForeignKey{LocalColumns: left.columns, ReferencedSchema: right.schema, ReferencedTable: right.table, ReferencedColumns: right.columns}
	for _, setting := range splitComma(settings) {
		s := strings.TrimSpace(setting)
		low := strings.ToLower(s)
		if strings.HasPrefix(low, "delete:") {
			fk.OnDelete = action(s[len("delete:"):])
		}
		if strings.HasPrefix(low, "update:") {
			fk.OnUpdate = action(s[len("update:"):])
		}
	}
	local.ForeignKeys = append(local.ForeignKeys, fk)
	return nil
}

type refEndpoint struct {
	schema, table string
	columns       []string
}

func (e refEndpoint) qualified() string {
	if e.schema == "" {
		return e.table
	}
	return e.schema + "." + e.table
}
func endpoint(raw string) (refEndpoint, error) {
	raw = strings.TrimSpace(raw)
	dot := strings.LastIndex(raw, ".")
	if dot < 0 {
		return refEndpoint{}, fmt.Errorf("dbml: invalid reference endpoint %q", raw)
	}
	tablePart, columns := raw[:dot], trimParens(raw[dot+1:])
	namespace, table := splitName(tablePart)
	return refEndpoint{namespace, table, splitNames(columns)}, nil
}

func (p *parser) schema(name string) *schema.Schema {
	for i := range p.db.Schemas {
		if p.db.Schemas[i].Name == name {
			return &p.db.Schemas[i]
		}
	}
	p.db.Schemas = append(p.db.Schemas, schema.Schema{Name: name})
	return &p.db.Schemas[len(p.db.Schemas)-1]
}
func (p *parser) findTable(namespace, name string) *schema.Table {
	for i := range p.db.Schemas {
		if strings.EqualFold(p.db.Schemas[i].Name, namespace) {
			for j := range p.db.Schemas[i].Tables {
				if strings.EqualFold(p.db.Schemas[i].Tables[j].Name, name) {
					return &p.db.Schemas[i].Tables[j]
				}
			}
		}
	}
	return nil
}
func (p *parser) fullTable() string {
	if p.table.Schema == "" {
		return p.table.Name
	}
	return p.table.Schema + "." + p.table.Name
}
func splitName(v string) (string, string) {
	v = strings.TrimSpace(v)
	if i := strings.LastIndex(v, "."); i >= 0 {
		return unquoteIdentifier(v[:i]), unquoteIdentifier(v[i+1:])
	}
	return "", unquoteIdentifier(v)
}
func trimParens(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "(") && strings.HasSuffix(v, ")") {
		return strings.TrimSpace(v[1 : len(v)-1])
	}
	return v
}
func splitNames(v string) []string {
	xs := splitComma(v)
	for i := range xs {
		xs[i] = unquoteIdentifier(strings.TrimSpace(xs[i]))
	}
	return xs
}
func scalar(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && ((v[0] == '\'' && v[len(v)-1] == '\'') || (v[0] == '"' && v[len(v)-1] == '"') || (v[0] == '`' && v[len(v)-1] == '`')) {
		return v[1 : len(v)-1]
	}
	return v
}
func unquoteIdentifier(v string) string { return scalar(strings.TrimSpace(v)) }
func action(v string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(scalar(v)), "_", " "))
}
func indexDefinition(table string, i schema.Index) string {
	u := ""
	if i.Unique {
		u = "UNIQUE "
	}
	return u + "ON " + table + " USING " + i.Method + " (" + strings.Join(i.Keys, ", ") + ")"
}

func stripComment(s string) string {
	quote := rune(0)
	for i, r := range s {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			continue
		}
		if r == '/' && i+1 < len(s) && s[i+1] == '/' {
			return s[:i]
		}
	}
	return s
}
func splitSettings(s string) (string, string) {
	quote := rune(0)
	depth := 0
	start := -1
	for i, r := range s {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			continue
		}
		if r == '[' {
			if depth == 0 {
				start = i
			}
			depth++
		}
		if r == ']' {
			depth--
			if depth == 0 && start >= 0 {
				return strings.TrimSpace(s[:start]), s[start+1 : i]
			}
		}
	}
	return strings.TrimSpace(s), ""
}
func splitComma(s string) []string {
	var out []string
	start := 0
	quote := rune(0)
	depth := 0
	for i, r := range s {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			continue
		}
		if r == '(' {
			depth++
		}
		if r == ')' {
			depth--
		}
		if r == ',' && depth == 0 {
			out = append(out, strings.TrimSpace(s[start:i]))
			start = i + 1
		}
	}
	if strings.TrimSpace(s[start:]) != "" {
		out = append(out, strings.TrimSpace(s[start:]))
	}
	return out
}
func fieldsQuoted(s string) []string {
	var out []string
	var b strings.Builder
	quote := rune(0)
	for _, r := range s {
		if quote != 0 {
			b.WriteRune(r)
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' || r == '`' {
			quote = r
			b.WriteRune(r)
		} else if r == ' ' || r == '\t' {
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		} else {
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}
