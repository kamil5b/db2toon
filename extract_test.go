package db2toon_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	db2toon "github.com/kamil5b/db2toon"
)

func TestExtractFromDumpAsExternalPackage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.sql")
	dump := "CREATE TABLE public.users (id integer NOT NULL, secret text);\nINSERT INTO public.users (id, secret) VALUES (1, 'hidden');\n"
	if err := os.WriteFile(path, []byte(dump), 0600); err != nil {
		t.Fatal(err)
	}
	db, err := db2toon.Extract(context.Background(), db2toon.Request{Dialect: "postgres", Dump: path, Options: db2toon.Options{ExampleSample: 1, ExcludeExampleFields: []string{"users.secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Schemas) != 1 || len(db.Schemas[0].Tables) != 1 {
		t.Fatalf("unexpected schema: %#v", db)
	}
	if db.Dialect != "postgres" || db.Name != "schema" {
		t.Fatalf("database metadata = %#v", db)
	}
	var output bytes.Buffer
	if err := db2toon.Encode(&output, db); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "@database schema {dialect=postgres}") || !strings.Contains(output.String(), "[users]") || strings.Contains(output.String(), "hidden") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestExtractValidatesPublicRequest(t *testing.T) {
	_, err := db2toon.Extract(context.Background(), db2toon.Request{Dialect: "postgres", DB: "postgres://user:password@example.invalid/db", Dump: "schema.sql"})
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("error = %v", err)
	}
	if strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "schema.sql") {
		t.Fatalf("sensitive value leaked: %v", err)
	}
	var typed *db2toon.Error
	if !errors.As(err, &typed) {
		t.Fatalf("error type = %T", err)
	}
}
