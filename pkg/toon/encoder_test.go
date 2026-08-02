package toon

import (
	"bytes"
	"errors"
	"testing"

	"github.com/kamil5b/pgschema2toon/pkg/schema"
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

func TestEncodeRejectsNil(t *testing.T) {
	if err := Encode(&bytes.Buffer{}, nil); err == nil {
		t.Fatal("expected nil database error")
	}
}

type errorWriter struct{ err error }

func (w errorWriter) Write([]byte) (int, error) { return 0, w.err }
