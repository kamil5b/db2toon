package postgres

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
)

func TestDumpExtractorParsesSchemaAndExamples(t *testing.T) {
	dump := `CREATE SCHEMA app;
CREATE TABLE app."User" (
  "id" integer NOT NULL,
  name text DEFAULT 'guest',
  CONSTRAINT user_pkey PRIMARY KEY ("id"),
  CONSTRAINT user_name_key UNIQUE (name),
  CONSTRAINT user_name_check CHECK (length(name) > 0)
);
CREATE TABLE public.events (id bigint NOT NULL, user_id integer);
ALTER TABLE ONLY public.events ADD CONSTRAINT events_user_fkey FOREIGN KEY (user_id) REFERENCES app."User"("id") ON DELETE CASCADE;
COMMENT ON TABLE public.events IS 'event log';
COMMENT ON COLUMN public.events.id IS 'event identifier';
CREATE UNIQUE INDEX events_id_idx ON public.events USING btree (id);
COPY public.events (id, user_id) FROM stdin;
1	10
2	11
3	12
\.
INSERT INTO app."User" ("id", name) VALUES (10, 'Ada'), (11, 'Bob');
`
	path := filepath.Join(t.TempDir(), "schema.sql")
	if err := os.WriteFile(path, []byte(dump), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := NewFromDump(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close(context.Background())
	db, err := e.Extract(context.Background(), database.ExtractOptions{Schemas: []string{"public", "app"}, ExampleSample: 2, ExampleSampleOrdered: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Schemas) != 2 || len(db.Schemas[0].Tables) != 1 || len(db.Schemas[1].Tables) != 1 {
		t.Fatalf("unexpected schemas: %#v", db)
	}
	events, users := db.Schemas[0].Tables[0], db.Schemas[1].Tables[0]
	if events.Schema != "public" {
		events, users = users, events
	}
	if events.Comment != "event log" || events.Columns[0].Comment != "event identifier" || len(events.Indexes) != 1 {
		t.Fatalf("metadata not parsed: %#v", events)
	}
	if len(events.ForeignKeys) != 1 || events.ForeignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("foreign key not parsed: %#v", events.ForeignKeys)
	}
	if events.Example == nil || len(events.Example.Rows) != 2 {
		t.Fatalf("COPY examples not parsed: %#v", events.Example)
	}
	if users.PrimaryKey == nil || len(users.Uniques) != 1 || len(users.Checks) != 1 || users.Example == nil || len(users.Example.Rows) != 2 {
		t.Fatalf("user metadata/examples not parsed: %#v", users)
	}
}

func TestDumpExtractorAppliesExampleExclusions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE public.t (id integer, secret text);\nINSERT INTO public.t (id, secret) VALUES (1, 'x');\n"), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := NewFromDump(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := e.Extract(context.Background(), database.ExtractOptions{ExampleSample: 1, ExcludeExampleFields: []string{"t.secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := db.Schemas[0].Tables[0].Example.Columns; len(got) != 1 || got[0] != "id" {
		t.Fatalf("columns = %#v", got)
	}
}

func TestDumpExtractorParsesEnumsRoutinesTriggersViewsAndSequences(t *testing.T) {
	dump := `CREATE SCHEMA app;
CREATE TYPE app.job_status AS ENUM ('queued', 'running', 'done');
CREATE SEQUENCE app.job_number START WITH 100 INCREMENT BY 5;
CREATE TABLE app.jobs (id bigint PRIMARY KEY, status app.job_status NOT NULL);
CREATE VIEW app.active_jobs AS SELECT id FROM app.jobs WHERE status = 'running';
CREATE FUNCTION app.set_job_status() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  RETURN NEW;
END;
$$;
CREATE PROCEDURE app.archive_jobs() LANGUAGE sql AS $$ DELETE FROM app.jobs; $$;
CREATE TRIGGER jobs_status_trigger BEFORE INSERT OR UPDATE ON app.jobs FOR EACH ROW EXECUTE FUNCTION app.set_job_status();
`
	path := filepath.Join(t.TempDir(), "objects.sql")
	if err := os.WriteFile(path, []byte(dump), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := NewFromDump(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := e.Extract(context.Background(), database.ExtractOptions{Schemas: []string{"app"}, IncludeViews: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Schemas) != 1 || len(db.Schemas[0].Tables) != 2 {
		t.Fatalf("database: %#v", db)
	}
	s := db.Schemas[0]
	if len(s.Enums) != 1 || strings.Join(s.Enums[0].Values, ",") != "queued,running,done" {
		t.Fatalf("enums: %#v", s.Enums)
	}
	if len(s.Sequences) != 1 || s.Sequences[0].Name != "job_number" || s.Sequences[0].Increment != "5" {
		t.Fatalf("sequences: %#v", s.Sequences)
	}
	if len(s.Routines) != 2 {
		t.Fatalf("routines: %#v", s.Routines)
	}
	jobs := findDumpTable(s.Tables, "jobs")
	if len(jobs.Triggers) != 1 || jobs.Triggers[0].Name != "jobs_status_trigger" {
		t.Fatalf("triggers: %#v", jobs.Triggers)
	}
	if view := findDumpTable(s.Tables, "active_jobs"); view.Kind != "view" {
		t.Fatalf("view: %#v", view)
	}
}

func findDumpTable(tables []schema.Table, name string) schema.Table {
	for _, table := range tables {
		if table.Name == name {
			return table
		}
	}
	return schema.Table{}
}
