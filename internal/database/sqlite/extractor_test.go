package sqlite

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
)

func TestExtractLocalDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.db")
	e, err := New(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close(context.Background())
	_, err = e.ExtractorDB().ExecContext(context.Background(), `
CREATE TABLE parent (id INTEGER PRIMARY KEY, code TEXT NOT NULL UNIQUE);
CREATE TABLE child (id INTEGER PRIMARY KEY, parent_id INTEGER, note TEXT DEFAULT 'ok',
  FOREIGN KEY (parent_id) REFERENCES parent(id) ON DELETE CASCADE,
  CHECK (length(note) > 0));
CREATE UNIQUE INDEX child_note_idx ON child(note);
CREATE VIEW child_view AS SELECT id, note FROM child;
`)
	if err != nil {
		t.Fatal(err)
	}
	db, err := e.Extract(context.Background(), database.ExtractOptions{Schemas: []string{"main"}, IncludeViews: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Schemas) != 1 || len(db.Schemas[0].Tables) != 3 {
		t.Fatalf("unexpected tables: %#v", db)
	}
	child := findTable(db.Schemas[0].Tables, "child")
	if child.PrimaryKey == nil || len(child.PrimaryKey.Columns) != 1 || child.PrimaryKey.Columns[0] != "id" {
		t.Fatalf("primary key: %#v", child.PrimaryKey)
	}
	if len(child.ForeignKeys) != 1 || child.ForeignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("foreign keys: %#v", child.ForeignKeys)
	}
	if len(child.Indexes) != 1 || child.Indexes[0].Name != "child_note_idx" {
		t.Fatalf("indexes: %#v", child.Indexes)
	}
	if child.Columns[2].Default != "'ok'" || child.Columns[1].Nullable != true {
		t.Fatalf("columns: %#v", child.Columns)
	}
}

func TestExtractDefaultsToMainAndOmitsViews(t *testing.T) {
	e := newTestExtractor(t)
	if _, err := e.ExtractorDB().Exec(`
CREATE TABLE accounts (id INTEGER PRIMARY KEY, email TEXT NOT NULL);
CREATE VIEW account_view AS SELECT id, email FROM accounts;
`); err != nil {
		t.Fatal(err)
	}

	db, err := e.Extract(context.Background(), database.ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Schemas) != 1 || db.Schemas[0].Name != "main" {
		t.Fatalf("unexpected default schema: %#v", db.Schemas)
	}
	if len(db.Schemas[0].Tables) != 1 || db.Schemas[0].Tables[0].Name != "accounts" {
		t.Fatalf("views should be omitted by default: %#v", db.Schemas[0].Tables)
	}
}

func TestExtractTriggers(t *testing.T) {
	e := newTestExtractor(t)
	if _, err := e.ExtractorDB().Exec(`
CREATE TABLE jobs (id INTEGER PRIMARY KEY, name TEXT NOT NULL);
CREATE TRIGGER jobs_validate_name BEFORE INSERT ON jobs
BEGIN
  SELECT CASE WHEN NEW.name = '' THEN RAISE(ABORT, 'name is required') END;
END;
`); err != nil {
		t.Fatal(err)
	}

	db, err := e.Extract(context.Background(), database.ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	jobs := findTable(db.Schemas[0].Tables, "jobs")
	if len(jobs.Triggers) != 1 || jobs.Triggers[0].Name != "jobs_validate_name" || jobs.Triggers[0].Timing != "BEFORE" || !sameStrings(jobs.Triggers[0].Events, []string{"INSERT"}) || !jobs.Triggers[0].Enabled {
		t.Fatalf("triggers: %#v", jobs.Triggers)
	}
}

func TestExtractCompositeKeysAndConstraints(t *testing.T) {
	e := newTestExtractor(t)
	if _, err := e.ExtractorDB().Exec(`
CREATE TABLE parent (
  tenant_id INTEGER NOT NULL,
  parent_id INTEGER NOT NULL,
  code TEXT NOT NULL,
  PRIMARY KEY (tenant_id, parent_id),
  UNIQUE (tenant_id, code),
  CHECK (length(code) > 0)
);
CREATE TABLE child (
  tenant_id INTEGER NOT NULL,
  parent_id INTEGER NOT NULL,
  child_id INTEGER NOT NULL,
  PRIMARY KEY (tenant_id, parent_id, child_id),
  FOREIGN KEY (tenant_id, parent_id) REFERENCES parent (tenant_id, parent_id)
    ON UPDATE CASCADE ON DELETE SET NULL
);
`); err != nil {
		t.Fatal(err)
	}

	db, err := e.Extract(context.Background(), database.ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	parent := findTable(db.Schemas[0].Tables, "parent")
	if parent.PrimaryKey == nil || !sameStrings(parent.PrimaryKey.Columns, []string{"tenant_id", "parent_id"}) {
		t.Fatalf("composite primary key: %#v", parent.PrimaryKey)
	}
	if len(parent.Uniques) != 1 || !sameStrings(parent.Uniques[0].Columns, []string{"tenant_id", "code"}) {
		t.Fatalf("unique constraint: %#v", parent.Uniques)
	}
	if len(parent.Checks) != 1 || parent.Checks[0].Expression != "CHECK (length(code) > 0)" {
		t.Fatalf("check constraint: %#v", parent.Checks)
	}
	child := findTable(db.Schemas[0].Tables, "child")
	if len(child.ForeignKeys) != 1 {
		t.Fatalf("foreign keys: %#v", child.ForeignKeys)
	}
	fk := child.ForeignKeys[0]
	if !sameStrings(fk.LocalColumns, []string{"tenant_id", "parent_id"}) || !sameStrings(fk.ReferencedColumns, []string{"tenant_id", "parent_id"}) || fk.OnUpdate != "CASCADE" || fk.OnDelete != "SET NULL" {
		t.Fatalf("composite foreign key: %#v", fk)
	}
}

func TestExtractExamplesAndExclusions(t *testing.T) {
	e := newTestExtractor(t)
	if _, err := e.ExtractorDB().Exec(`
CREATE TABLE users (id INTEGER PRIMARY KEY, email TEXT, password TEXT);
CREATE TABLE ignored (id INTEGER PRIMARY KEY);
INSERT INTO users VALUES (1, 'one@example.com', 'one'), (2, 'two@example.com', 'two'), (3, 'three@example.com', 'three');
`); err != nil {
		t.Fatal(err)
	}

	db, err := e.Extract(context.Background(), database.ExtractOptions{
		ExampleSample:        2,
		ExcludeExampleFields: []string{"users.password"},
		ExcludeTables:        []string{"ignored"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if findTable(db.Schemas[0].Tables, "ignored").Name != "" {
		t.Fatal("excluded table was extracted")
	}
	users := findTable(db.Schemas[0].Tables, "users")
	if users.Example == nil || len(users.Example.Rows) != 2 {
		t.Fatalf("examples: %#v", users.Example)
	}
	if !sameStrings(users.Example.Columns, []string{"id", "email"}) {
		t.Fatalf("excluded example field: %#v", users.Example.Columns)
	}
	for _, row := range users.Example.Rows {
		if len(row) != 2 {
			t.Fatalf("example row still contains excluded field: %#v", row)
		}
	}
}

func TestExcludeExampleTable(t *testing.T) {
	e := newTestExtractor(t)
	if _, err := e.ExtractorDB().Exec(`CREATE TABLE events (id INTEGER PRIMARY KEY); INSERT INTO events VALUES (1);`); err != nil {
		t.Fatal(err)
	}
	db, err := e.Extract(context.Background(), database.ExtractOptions{ExampleSample: 1, ExcludeExampleTables: []string{"events"}})
	if err != nil {
		t.Fatal(err)
	}
	if table := findTable(db.Schemas[0].Tables, "events"); table.Example != nil {
		t.Fatalf("excluded example table has examples: %#v", table.Example)
	}
}

func TestExtractAttachedDatabase(t *testing.T) {
	mainPath := filepath.Join(t.TempDir(), "main.db")
	attachedPath := filepath.Join(t.TempDir(), "attached.db")
	e, err := New(context.Background(), mainPath)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close(context.Background())
	db := e.ExtractorDB()
	if _, err = db.Exec("ATTACH DATABASE ? AS extra", attachedPath); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("CREATE TABLE extra.events (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatal(err)
	}
	got, err := e.Extract(context.Background(), database.ExtractOptions{Schemas: []string{"extra"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Schemas) != 1 || len(got.Schemas[0].Tables) != 1 || got.Schemas[0].Tables[0].Name != "events" {
		t.Fatalf("unexpected attached schema: %#v", got)
	}
	if _, err := os.Stat(attachedPath); err != nil {
		t.Fatalf("attached database was not created: %v", err)
	}
}

// ExtractorDB is intentionally narrow test access; production callers use Extract.
func (e *Extractor) ExtractorDB() *sql.DB { return e.DB() }

func findTable(tables []schema.Table, name string) schema.Table {
	for _, table := range tables {
		if table.Name == name {
			return table
		}
	}
	return schema.Table{}
}

func newTestExtractor(t *testing.T) *Extractor {
	t.Helper()
	e, err := New(context.Background(), filepath.Join(t.TempDir(), "schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = e.Close(context.Background()) })
	return e
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
