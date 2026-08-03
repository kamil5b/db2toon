package duckdb

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
)

func TestExtractLocalDatabase(t *testing.T) {
	e := newTestExtractor(t)
	db := e.DB()
	if _, err := db.Exec(`CREATE SCHEMA analytics; CREATE TABLE analytics.events (id INTEGER PRIMARY KEY, name VARCHAR NOT NULL); CREATE VIEW analytics.event_names AS SELECT name FROM analytics.events`); err != nil {
		t.Fatal(err)
	}
	got, err := e.Extract(context.Background(), database.ExtractOptions{Schemas: []string{"analytics"}, IncludeViews: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Schemas) != 1 || len(got.Schemas[0].Tables) != 2 {
		t.Fatalf("unexpected extraction: %#v", got)
	}
	if events := findTable(got.Schemas[0].Tables, "events"); events.PrimaryKey == nil || len(events.Columns) != 2 || events.Columns[1].NativeType != "VARCHAR" {
		t.Fatalf("table metadata: %#v", events)
	}
}

func TestExtractConstraintsExamplesAndExclusions(t *testing.T) {
	e := newTestExtractor(t)
	if _, err := e.DB().Exec(`
CREATE TABLE customers (
  id INTEGER PRIMARY KEY,
  email VARCHAR NOT NULL UNIQUE,
  secret VARCHAR
);
CREATE TABLE ignored (id INTEGER PRIMARY KEY);
INSERT INTO customers VALUES (1, 'one@example.com', 'one'), (2, 'two@example.com', 'two'), (3, 'three@example.com', 'three');
`); err != nil {
		t.Fatal(err)
	}

	got, err := e.Extract(context.Background(), database.ExtractOptions{
		ExampleSample:        2,
		ExcludeExampleFields: []string{"customers.secret"},
		ExcludeExampleTables: []string{"ignored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	customers := findTable(got.Schemas[0].Tables, "customers")
	if customers.PrimaryKey == nil || len(customers.Uniques) != 1 {
		t.Fatalf("constraints: %#v", customers)
	}
	if customers.Example == nil || len(customers.Example.Rows) != 2 || len(customers.Example.Columns) != 2 {
		t.Fatalf("examples: %#v", customers.Example)
	}
	if findTable(got.Schemas[0].Tables, "ignored").Name != "" {
		t.Fatal("excluded example table was extracted")
	}
}

func newTestExtractor(t *testing.T) *Extractor {
	t.Helper()
	e, err := New(context.Background(), filepath.Join(t.TempDir(), "schema.duckdb"))
	if err != nil {
		t.Skipf("DuckDB shared library unavailable: %v", err)
	}
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	return e
}

func findTable(tables []schema.Table, name string) schema.Table {
	for _, table := range tables {
		if table.Name == name {
			return table
		}
	}
	return schema.Table{}
}
