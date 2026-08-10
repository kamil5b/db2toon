package toon

import (
	"bytes"
	"errors"
	"testing"

	"github.com/kamil5b/db2toon/pkg/schema"
)

func TestEncodeGolden(t *testing.T) {
	db := &schema.Database{Schemas: []schema.Schema{{Name: "app data", Tables: []schema.Table{{
		Schema: "app data", Name: `order"items`, Comment: "First line\r\nsecond line",
		Columns: []schema.Column{
			{Name: "tenant id", NativeType: "bigint", Identity: "a"},
			{Name: "code", NativeType: "character varying(20)", Default: "'new'::character varying", Comment: "one\ntwo"},
		},
		PrimaryKey:  &schema.PrimaryKey{Name: "order_pk", Columns: []string{"tenant id", "code"}},
		ForeignKeys: []schema.ForeignKey{{Name: "order_parent_fk", LocalColumns: []string{"tenant id", "code"}, ReferencedSchema: "core", ReferencedTable: "parents", ReferencedColumns: []string{"tenant", "code"}, OnUpdate: "CASCADE", OnDelete: "SET NULL"}},
		Uniques:     []schema.UniqueConstraint{{Name: "unique code", Columns: []string{"code"}}},
		Checks:      []schema.CheckConstraint{{Name: "valid_code", Expression: "CHECK ((code <> ''::text))"}},
		Indexes:     []schema.Index{{Name: "partial code", Method: "btree", Keys: []string{"lower((code)::text)"}, Predicate: "code IS NOT NULL"}},
	}}}}}

	const want = `["app data"."order""items"]
# First line
# second line
  "tenant id" bigint {pk,req,identity=always}
  code varchar(20) {pk,req} = 'new'::character varying // one
  // two
  ref ("tenant id",code) -> core.parents(tenant,code) {on_update=cascade,on_delete=set_null}
@constraints
  "unique code": unique (code)
  valid_code: CHECK ((code <> ''::text))
@indices
  "partial code": btree (lower((code)::text)) WHERE code IS NOT NULL

`
	var got bytes.Buffer
	if err := Encode(&got, db); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if got.String() != want {
		t.Fatalf("unexpected output\n--- got ---\n%s--- want ---\n%s", got.String(), want)
	}
}

func TestEncodePropagatesWriterError(t *testing.T) {
	errSentinel := errors.New("full")
	err := Encode(errorWriter{errSentinel}, &schema.Database{Schemas: []schema.Schema{{Tables: []schema.Table{{Name: "x"}}}}})
	if !errors.Is(err, errSentinel) {
		t.Fatalf("Encode error = %v, want %v", err, errSentinel)
	}
}

func TestEncodeExamples(t *testing.T) {
	db := &schema.Database{Schemas: []schema.Schema{{Tables: []schema.Table{{
		Name:    "users",
		Columns: []schema.Column{{Name: "id", NativeType: "int", Nullable: true}, {Name: "name", NativeType: "text", Nullable: true}, {Name: "note", NativeType: "text", Nullable: true}},
		Example: &schema.Example{
			Columns: []string{"id", "name", "note"},
			Rows:    [][]any{{int64(1), "Alice", nil}, {int64(2), "Bob", "has, comma"}},
		},
	}}}}}

	var got bytes.Buffer
	if err := Encode(&got, db); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := "[users]\n  id int\n  name text\n  note text\n@example[2]{id,name,note}:\n  1,Alice,null\n  2,Bob,\"has, comma\"\n\n"
	if got.String() != want {
		t.Fatalf("unexpected output\n--- got ---\n%s--- want ---\n%s", got.String(), want)
	}
}

func TestEncodeEnumsRoutinesAndTriggers(t *testing.T) {
	db := &schema.Database{Extensions: []schema.Extension{{Name: "vector", Version: "0.8.1", Schema: "public"}}, Schemas: []schema.Schema{{Name: "public",
		Enums: []schema.Enum{{Name: "job_status", Values: []string{"queued", "running", "done"}}},
		Routines: []schema.Routine{{
			Name: "active_job_count", Kind: "function", Arguments: "", ReturnType: "bigint", Language: "sql",
			Definition: "SELECT count(*) FROM jobs WHERE status = 'running'",
		}},
		Tables: []schema.Table{{
			Name: "jobs", Columns: []schema.Column{{Name: "status", NativeType: "job_status", Nullable: false}},
			Triggers: []schema.Trigger{{
				Name: "jobs_updated_at", Timing: "BEFORE", Events: []string{"UPDATE"}, Enabled: true,
				Definition: "EXECUTE FUNCTION set_updated_at()",
			}},
		}},
	}}}

	var got bytes.Buffer
	if err := Encode(&got, db); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	const want = `@extensions
  vector {version=0.8.1,schema=public}

@enum job_status:
  queued
  running
  done

@routine function active_job_count() -> bigint {language=sql}:
  SELECT count(*) FROM jobs WHERE status = 'running'

[jobs]
  status job_status {req}
@triggers
  jobs_updated_at: before update
    EXECUTE FUNCTION set_updated_at()

`
	if got.String() != want {
		t.Fatalf("unexpected output\n--- got ---\n%s--- want ---\n%s", got.String(), want)
	}
}

func TestEncodeDatabaseMetadata(t *testing.T) {
	db := &schema.Database{Dialect: "oracle", Name: "FREEPDB1", Schemas: []schema.Schema{{Name: "APP"}}}
	var got bytes.Buffer
	if err := Encode(&got, db); err != nil {
		t.Fatal(err)
	}
	if want := "@database FREEPDB1 {dialect=oracle}\n\n"; got.String() != want {
		t.Fatalf("output = %q, want %q", got.String(), want)
	}
}

func TestEncodeSchemaObjects(t *testing.T) {
	db := &schema.Database{Schemas: []schema.Schema{{Name: "app", Types: []schema.UserDefinedType{{Name: "account_code", Kind: "alias", NativeType: "nvarchar"}}, Sequences: []schema.Sequence{{Name: "invoice_number", NativeType: "bigint", Start: "100", Increment: "5"}}, Synonyms: []schema.Synonym{{Name: "people", Target: "[app].[users]"}}}}}
	var got bytes.Buffer
	if err := Encode(&got, db); err != nil {
		t.Fatal(err)
	}
	const want = "@type app.account_code alias -> nvarchar\n\n@sequence app.invoice_number bigint {start=100,increment=5}\n\n@synonym app.people -> [app].[users]\n\n"
	if got.String() != want {
		t.Fatalf("output = %q, want %q", got.String(), want)
	}
}

func TestEncodeStructuredExampleValues(t *testing.T) {
	db := &schema.Database{Schemas: []schema.Schema{{Tables: []schema.Table{{
		Name:    "documents",
		Columns: []schema.Column{{Name: "id", NativeType: "uuid", Nullable: true}, {Name: "body", NativeType: "jsonb", Nullable: true}},
		Example: &schema.Example{
			Columns:     []string{"id", "body"},
			ColumnTypes: []string{"uuid", "jsonb"},
			Rows:        [][]any{{[16]byte{0x31, 0xd6, 0x09, 0x9e, 0x7b, 0xb9, 0x4c, 0x12, 0xb5, 0xe4, 0xbe, 0xa7, 0x72, 0xc1, 0xc7, 0x2b}, map[string]any{"kind": "note"}}},
		},
	}}}}}

	var got bytes.Buffer
	if err := Encode(&got, db); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	want := "[documents]\n  id uuid\n  body jsonb\n@example[1]{id,body}:\n  31d6099e-7bb9-4c12-b5e4-bea772c1c72b,\"{\\\"kind\\\":\\\"note\\\"}\"\n\n"
	if got.String() != want {
		t.Fatalf("unexpected output\n--- got ---\n%s--- want ---\n%s", got.String(), want)
	}
}

func TestEncodeRejectsNil(t *testing.T) {
	if err := Encode(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("expected nil database error")
	}
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }
