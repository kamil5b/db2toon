//go:build integration

package cockroachdb

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestCockroachDBSchemaExtraction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := testcontainers.Run(ctx, "cockroachdb/cockroach:v25.4.13",
		testcontainers.WithExposedPorts("26257/tcp"),
		testcontainers.WithEntrypoint("/cockroach/cockroach"),
		testcontainers.WithCmd("start-single-node", "--insecure", "--http-addr=0.0.0.0:8080"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("26257/tcp")),
	)
	if err != nil {
		t.Fatalf("start CockroachDB container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate CockroachDB container: %v", err)
		}
	})

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("get CockroachDB host: %v", err)
	}
	port, err := container.MappedPort(ctx, "26257/tcp")
	if err != nil {
		t.Fatalf("get CockroachDB port: %v", err)
	}
	dsn := fmt.Sprintf("postgresql://root@%s/defaultdb?sslmode=disable", fmt.Sprintf("%s:%s", host, port.Port()))

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to CockroachDB: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	if _, err := conn.Exec(ctx, `
CREATE SCHEMA app;
CREATE TABLE app.users (
  id INT PRIMARY KEY,
  email STRING NOT NULL UNIQUE,
  display_name STRING,
  CONSTRAINT users_email_check CHECK (length(email) > 0)
);
CREATE TABLE app.posts (
  id INT PRIMARY KEY,
  author_id INT NOT NULL REFERENCES app.users(id) ON DELETE CASCADE,
  title STRING NOT NULL
);
CREATE INDEX posts_title_idx ON app.posts(title);
CREATE VIEW app.post_titles AS SELECT id, title FROM app.posts;
INSERT INTO app.users VALUES (1, 'alice@example.com', 'Alice'), (2, 'bob@example.com', 'Bob');
INSERT INTO app.posts VALUES (1, 1, 'Hello'), (2, 2, 'World');
`); err != nil {
		t.Fatalf("create CockroachDB fixture: %v", err)
	}

	extractor, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("create extractor: %v", err)
	}
	t.Cleanup(func() { _ = extractor.Close(context.Background()) })
	db, err := extractor.Extract(ctx, database.ExtractOptions{Schemas: []string{"app"}, IncludeViews: true, ExampleSample: 1, ExampleSampleOrdered: true})
	if err != nil {
		t.Fatalf("extract schema: %v", err)
	}
	if len(db.Schemas) != 1 || len(db.Schemas[0].Tables) != 3 {
		t.Fatalf("unexpected database: %#v", db)
	}
	users := findTable(db.Schemas[0].Tables, "users")
	posts := findTable(db.Schemas[0].Tables, "posts")
	if users.PrimaryKey == nil || len(users.Uniques) != 1 || len(users.Checks) != 1 {
		t.Fatalf("users constraints: %#v", users)
	}
	if len(posts.ForeignKeys) != 1 || posts.ForeignKeys[0].ReferencedSchema != "app" || posts.ForeignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("posts foreign key: %#v", posts.ForeignKeys)
	}
	if len(posts.Indexes) != 1 || posts.Indexes[0].Name != "posts_title_idx" {
		t.Fatalf("posts indexes: %#v", posts.Indexes)
	}
	if posts.Example == nil || len(posts.Example.Rows) != 1 {
		t.Fatalf("posts examples: %#v", posts.Example)
	}
	if !strings.Contains(strings.ToUpper(posts.Example.ColumnTypes[0]), "INT") {
		t.Fatalf("posts example types: %#v", posts.Example.ColumnTypes)
	}
}

func findTable(tables []schema.Table, name string) schema.Table {
	for _, table := range tables {
		if table.Name == name {
			return table
		}
	}
	return schema.Table{}
}
