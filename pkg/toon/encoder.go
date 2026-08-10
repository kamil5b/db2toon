// Package toon renders a database-neutral schema model as TOON text.
package toon

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kamil5b/db2toon/pkg/schema"
)

var plainIdentifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_$]*$`)

// Encode writes db in deterministic schema, table, and member order. Slice
// ordering in the model is significant; extractors are responsible for it.
func Encode(w io.Writer, db *schema.Database) error {
	if w == nil {
		return fmt.Errorf("toon: nil writer")
	}
	if db == nil {
		return fmt.Errorf("toon: nil database")
	}
	e := encoder{w: w, multipleSchemas: len(db.Schemas) > 1}
	if err := e.extensions(db.Extensions); err != nil {
		return err
	}
	for _, namespace := range db.Schemas {
		if err := e.schemaObjects(namespace); err != nil {
			return err
		}
		for _, table := range namespace.Tables {
			if err := e.table(namespace.Name, table); err != nil {
				return err
			}
		}
	}
	return nil
}

func (e encoder) extensions(extensions []schema.Extension) error {
	if len(extensions) == 0 {
		return nil
	}
	if err := e.printf("@extensions\n"); err != nil {
		return err
	}
	for _, extension := range extensions {
		attributes := make([]string, 0, 2)
		if extension.Version != "" {
			attributes = append(attributes, "version="+extension.Version)
		}
		if extension.Schema != "" {
			attributes = append(attributes, "schema="+identifier(extension.Schema))
		}
		if err := e.printf("  %s", identifier(extension.Name)); err != nil {
			return err
		}
		if len(attributes) > 0 {
			if err := e.printf(" {%s}", strings.Join(attributes, ",")); err != nil {
				return err
			}
		}
		if err := e.printf("\n"); err != nil {
			return err
		}
	}
	return e.printf("\n")
}

func (e encoder) schemaObjects(namespace schema.Schema) error {
	for _, enum := range namespace.Enums {
		if err := e.printf("@enum %s:\n", e.schemaObjectName(namespace.Name, enum.Name)); err != nil {
			return err
		}
		for _, value := range enum.Values {
			if err := e.printf("  %s\n", identifier(value)); err != nil {
				return err
			}
		}
		if err := e.printf("\n"); err != nil {
			return err
		}
	}
	for _, typ := range namespace.Types {
		if err := e.printf("@type %s %s", e.schemaObjectName(namespace.Name, typ.Name), strings.ToLower(typ.Kind)); err != nil {
			return err
		}
		if typ.NativeType != "" {
			if err := e.printf(" -> %s", shrink(typ.NativeType)); err != nil {
				return err
			}
		}
		if err := e.printf("\n\n"); err != nil {
			return err
		}
	}
	for _, sequence := range namespace.Sequences {
		if err := e.printf("@sequence %s %s", e.schemaObjectName(namespace.Name, sequence.Name), shrink(sequence.NativeType)); err != nil {
			return err
		}
		attributes := make([]string, 0, 5)
		if sequence.Start != "" {
			attributes = append(attributes, "start="+sequence.Start)
		}
		if sequence.Increment != "" {
			attributes = append(attributes, "increment="+sequence.Increment)
		}
		if sequence.Minimum != "" {
			attributes = append(attributes, "min="+sequence.Minimum)
		}
		if sequence.Maximum != "" {
			attributes = append(attributes, "max="+sequence.Maximum)
		}
		if sequence.Cyclic {
			attributes = append(attributes, "cycle")
		}
		if len(attributes) > 0 {
			if err := e.printf(" {%s}", strings.Join(attributes, ",")); err != nil {
				return err
			}
		}
		if err := e.printf("\n\n"); err != nil {
			return err
		}
	}
	for _, synonym := range namespace.Synonyms {
		if err := e.printf("@synonym %s -> %s\n\n", e.schemaObjectName(namespace.Name, synonym.Name), singleLine(synonym.Target)); err != nil {
			return err
		}
	}
	if len(namespace.Objects) > 0 {
		if err := e.printf("@objects\n"); err != nil {
			return err
		}
		for _, object := range namespace.Objects {
			if err := e.printf("  %s %s", strings.ToLower(object.Kind), e.schemaObjectName(namespace.Name, object.Name)); err != nil {
				return err
			}
			if len(object.Properties) > 0 {
				properties := make([]string, 0, len(object.Properties))
				for _, property := range object.Properties {
					properties = append(properties, identifier(property.Name)+"="+singleLine(property.Value))
				}
				if err := e.printf(" {%s}", strings.Join(properties, ",")); err != nil {
					return err
				}
			}
			if err := e.printf("\n"); err != nil {
				return err
			}
			for _, line := range commentLines(object.Definition) {
				if err := e.printf("    %s\n", line); err != nil {
					return err
				}
			}
		}
		if err := e.printf("\n"); err != nil {
			return err
		}
	}
	for _, routine := range namespace.Routines {
		kind := strings.ToLower(routine.Kind)
		if kind == "" {
			kind = "function"
		}
		if err := e.printf("@routine %s %s(%s)", kind, e.schemaObjectName(namespace.Name, routine.Name), singleLine(routine.Arguments)); err != nil {
			return err
		}
		if routine.ReturnType != "" {
			if err := e.printf(" -> %s", shrink(routine.ReturnType)); err != nil {
				return err
			}
		}
		if routine.Language != "" {
			if err := e.printf(" {language=%s}", identifier(strings.ToLower(routine.Language))); err != nil {
				return err
			}
		}
		if err := e.printf(":\n"); err != nil {
			return err
		}
		for _, line := range commentLines(routine.Definition) {
			if err := e.printf("  %s\n", line); err != nil {
				return err
			}
		}
		if err := e.printf("\n"); err != nil {
			return err
		}
	}
	return nil
}

func (e encoder) schemaObjectName(namespace, name string) string {
	if e.multipleSchemas || (namespace != "" && namespace != "public") {
		return identifier(namespace) + "." + identifier(name)
	}
	return identifier(name)
}

type encoder struct {
	w               io.Writer
	multipleSchemas bool
}

func (e encoder) printf(format string, args ...any) error {
	_, err := fmt.Fprintf(e.w, format, args...)
	return err
}

func (e encoder) table(namespace string, table schema.Table) error {
	name := identifier(table.Name)
	if e.multipleSchemas || (namespace != "" && namespace != "public") {
		name = identifier(namespace) + "." + name
	}
	if err := e.printf("[%s]", name); err != nil {
		return err
	}
	if table.Kind != "" && table.Kind != "table" {
		if err := e.printf(" {%s}", strings.ToLower(table.Kind)); err != nil {
			return err
		}
	}
	if err := e.printf("\n"); err != nil {
		return err
	}
	for _, line := range commentLines(table.Comment) {
		if err := e.printf("# %s\n", line); err != nil {
			return err
		}
	}
	if table.Definition != "" {
		if err := e.printf("@definition\n"); err != nil {
			return err
		}
		for _, line := range commentLines(table.Definition) {
			if err := e.printf("  %s\n", line); err != nil {
				return err
			}
		}
	}

	pk := make(map[string]bool)
	if table.PrimaryKey != nil {
		for _, column := range table.PrimaryKey.Columns {
			pk[column] = true
		}
	}
	inline := make(map[string]schema.ForeignKey)
	for _, fk := range table.ForeignKeys {
		if len(fk.LocalColumns) == 1 && len(fk.ReferencedColumns) == 1 {
			inline[fk.LocalColumns[0]] = fk
		}
	}
	for _, column := range table.Columns {
		tags := make([]string, 0, 4)
		if pk[column.Name] {
			tags = append(tags, "pk")
		}
		if !column.Nullable {
			tags = append(tags, "req")
		}
		if column.Identity != "" {
			tags = append(tags, "identity="+identityName(column.Identity))
		}
		if column.Generated != "" {
			tags = append(tags, "generated="+generatedName(column.Generated))
		}
		tagText := ""
		if len(tags) != 0 {
			tagText = " {" + strings.Join(tags, ",") + "}"
		}
		if err := e.printf("  %s %s%s", identifier(column.Name), shrink(column.NativeType), tagText); err != nil {
			return err
		}
		if fk, ok := inline[column.Name]; ok {
			if err := e.printf(" -> %s", reference(table.Schema, fk)); err != nil {
				return err
			}
			if err := e.actions(fk); err != nil {
				return err
			}
		}
		if column.Default != "" {
			if err := e.printf(" = %s", singleLine(column.Default)); err != nil {
				return err
			}
		}
		comments := commentLines(column.Comment)
		if len(comments) > 0 {
			if err := e.printf(" // %s", comments[0]); err != nil {
				return err
			}
		}
		if err := e.printf("\n"); err != nil {
			return err
		}
		if len(comments) > 1 {
			for _, line := range comments[1:] {
				if err := e.printf("  // %s\n", line); err != nil {
					return err
				}
			}
		}
	}

	for _, fk := range table.ForeignKeys {
		if len(fk.LocalColumns) == 1 && len(fk.ReferencedColumns) == 1 {
			continue
		}
		if err := e.printf("  ref (%s) -> %s", identifiers(fk.LocalColumns), reference(table.Schema, fk)); err != nil {
			return err
		}
		if err := e.actions(fk); err != nil {
			return err
		}
		if err := e.printf("\n"); err != nil {
			return err
		}
	}
	if len(table.Uniques)+len(table.Checks)+len(table.Exclusions) > 0 {
		if err := e.printf("@constraints\n"); err != nil {
			return err
		}
		for _, unique := range table.Uniques {
			if err := e.printf("  %s: unique (%s)\n", identifier(unique.Name), identifiers(unique.Columns)); err != nil {
				return err
			}
		}
		for _, check := range table.Checks {
			if err := e.printf("  %s: %s\n", identifier(check.Name), singleLine(check.Expression)); err != nil {
				return err
			}
		}
		for _, exclusion := range table.Exclusions {
			if err := e.printf("  %s: %s\n", identifier(exclusion.Name), singleLine(exclusion.Definition)); err != nil {
				return err
			}
		}
	}
	if len(table.Indexes) > 0 {
		if err := e.printf("@indices\n"); err != nil {
			return err
		}
		for _, index := range table.Indexes {
			definition := indexDefinition(index)
			if err := e.printf("  %s: %s\n", identifier(index.Name), definition); err != nil {
				return err
			}
		}
	}
	if len(table.Triggers) > 0 {
		if err := e.printf("@triggers\n"); err != nil {
			return err
		}
		for _, trigger := range table.Triggers {
			timing := strings.ToLower(trigger.Timing)
			events := make([]string, len(trigger.Events))
			for i, event := range trigger.Events {
				events[i] = strings.ToLower(event)
			}
			if err := e.printf("  %s: %s %s", identifier(trigger.Name), timing, strings.Join(events, ",")); err != nil {
				return err
			}
			if !trigger.Enabled {
				if err := e.printf(" {disabled}"); err != nil {
					return err
				}
			}
			if err := e.printf("\n"); err != nil {
				return err
			}
			for _, line := range commentLines(trigger.Definition) {
				if err := e.printf("    %s\n", line); err != nil {
					return err
				}
			}
		}
	}
	if table.Example != nil {
		if err := e.printf("@example[%d]{%s}:\n", len(table.Example.Rows), identifiers(table.Example.Columns)); err != nil {
			return err
		}
		for _, row := range table.Example.Rows {
			values := make([]string, len(row))
			for i, value := range row {
				var columnType string
				if i < len(table.Example.ColumnTypes) {
					columnType = table.Example.ColumnTypes[i]
				}
				values[i] = exampleValue(value, columnType)
			}
			if err := e.printf("  %s\n", strings.Join(values, ",")); err != nil {
				return err
			}
		}
	}
	return e.printf("\n")
}

func exampleValue(value any, columnType string) string {
	switch value := value.(type) {
	case nil:
		return "null"
	case []byte:
		if strings.EqualFold(strings.TrimSpace(columnType), "uuid") && len(value) == 16 {
			return formatUUID(value)
		}
		return quoteExampleValue(string(value))
	case [16]byte:
		if strings.EqualFold(strings.TrimSpace(columnType), "uuid") {
			return formatUUID(value[:])
		}
		return quoteExampleValue(string(value[:]))
	case time.Time:
		return quoteExampleValue(value.Format(time.RFC3339Nano))
	case string:
		return quoteExampleValue(value)
	default:
		if encoded, err := json.Marshal(value); err == nil {
			text := string(encoded)
			if strings.HasPrefix(text, "{") || strings.HasPrefix(text, "[") {
				return quoteExampleValue(text)
			}
		}
		return fmt.Sprint(value)
	}
}

func formatUUID(value []byte) string {
	return hex.EncodeToString(value[0:4]) + "-" +
		hex.EncodeToString(value[4:6]) + "-" +
		hex.EncodeToString(value[6:8]) + "-" +
		hex.EncodeToString(value[8:10]) + "-" +
		hex.EncodeToString(value[10:16])
}

func quoteExampleValue(value string) string {
	if value != "null" && value != "" && !strings.ContainsAny(value, ",{}[]:\"\r\n\t ") {
		return value
	}
	return strconv.Quote(value)
}

func (e encoder) actions(fk schema.ForeignKey) error {
	var actions []string
	if fk.OnUpdate != "" && fk.OnUpdate != "NO ACTION" {
		actions = append(actions, "on_update="+strings.ToLower(strings.ReplaceAll(fk.OnUpdate, " ", "_")))
	}
	if fk.OnDelete != "" && fk.OnDelete != "NO ACTION" {
		actions = append(actions, "on_delete="+strings.ToLower(strings.ReplaceAll(fk.OnDelete, " ", "_")))
	}
	if len(actions) != 0 {
		return e.printf(" {%s}", strings.Join(actions, ","))
	}
	return nil
}

func reference(localSchema string, fk schema.ForeignKey) string {
	table := identifier(fk.ReferencedTable)
	if fk.ReferencedSchema != "" && fk.ReferencedSchema != localSchema {
		table = identifier(fk.ReferencedSchema) + "." + table
	}
	return table + "(" + identifiers(fk.ReferencedColumns) + ")"
}

func identifier(value string) string {
	if plainIdentifier.MatchString(value) {
		return value
	}
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
func identifiers(values []string) string {
	quoted := make([]string, len(values))
	for i, value := range values {
		quoted[i] = identifier(value)
	}
	return strings.Join(quoted, ",")
}
func commentLines(value string) []string {
	if value == "" {
		return nil
	}
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.Split(value, "\n")
}
func singleLine(value string) string { return strings.Join(commentLines(value), `\n`) }
func identityName(value string) string {
	if value == "a" {
		return "always"
	}
	if value == "d" {
		return "default"
	}
	return value
}
func generatedName(value string) string {
	if value == "s" {
		return "stored"
	}
	return value
}

func shrink(value string) string {
	value = strings.ReplaceAll(value, "character varying", "varchar")
	value = strings.ReplaceAll(value, "timestamp with time zone", "timestamptz")
	return strings.TrimSpace(value)
}

func indexDefinition(index schema.Index) string {
	if index.Method != "" && len(index.Keys) != 0 {
		definition := index.Method + " (" + strings.Join(index.Keys, ", ") + ")"
		if index.Unique {
			definition = "unique " + definition
		}
		if len(index.IncludedColumns) != 0 {
			definition += " INCLUDE (" + identifiers(index.IncludedColumns) + ")"
		}
		if index.Predicate != "" {
			definition += " WHERE " + singleLine(index.Predicate)
		}
		return definition
	}
	parts := strings.SplitN(index.Definition, " USING ", 2)
	if len(parts) == 2 {
		return parts[1]
	}
	return singleLine(index.Definition)
}
