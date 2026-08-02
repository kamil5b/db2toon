//go:build integration

package main

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kamil5b/pgschema2toon/internal/database"
	pgadapter "github.com/kamil5b/pgschema2toon/internal/database/postgres"
	"github.com/kamil5b/pgschema2toon/pkg/schema"
)

func TestPostgresSchemaToToon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	connectionString, containerID := startPostgres(t, ctx)
	t.Cleanup(func() {
		if output, err := exec.Command("docker", "rm", "-f", containerID).CombinedOutput(); err != nil {
			t.Errorf("terminate PostgreSQL container: %v: %s", err, output)
		}
	})

	conn := connectPostgres(t, ctx, connectionString)
	t.Cleanup(func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Errorf("close PostgreSQL connection: %v", err)
		}
	})

	const fixture = `
CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    email character varying(255) NOT NULL UNIQUE,
    display_name text
);
COMMENT ON TABLE users IS 'Application users';
COMMENT ON COLUMN users.email IS 'Login email';

CREATE TABLE posts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    author_id bigint NOT NULL REFERENCES users(id),
    title character varying(200) NOT NULL,
    published_at timestamp with time zone
);
CREATE INDEX posts_author_id_idx ON posts USING btree (author_id);
`
	if _, err := conn.Exec(ctx, fixture); err != nil {
		t.Fatalf("create schema fixture: %v", err)
	}

	extractor, err := pgadapter.New(ctx, connectionString)
	if err != nil {
		t.Fatalf("create extractor: %v", err)
	}
	t.Cleanup(func() { _ = extractor.Close(context.Background()) })
	db, err := extractor.Extract(ctx, database.ExtractOptions{Schemas: []string{"public"}})
	if err != nil {
		t.Fatalf("extract schema: %v", err)
	}
	if len(db.Schemas) != 1 || len(db.Schemas[0].Tables) != 2 {
		t.Fatalf("unexpected database: %#v", db)
	}
	posts := findTable(t, db.Schemas[0].Tables, "posts")
	users := findTable(t, db.Schemas[0].Tables, "users")
	if posts.PrimaryKey == nil || strings.Join(posts.PrimaryKey.Columns, ",") != "id" {
		t.Fatalf("posts primary key: %#v", posts.PrimaryKey)
	}
	if len(posts.ForeignKeys) != 1 || posts.ForeignKeys[0].ReferencedTable != "users" {
		t.Fatalf("posts foreign keys: %#v", posts.ForeignKeys)
	}
	if len(posts.Indexes) != 1 || posts.Indexes[0].Method != "btree" {
		t.Fatalf("posts indexes: %#v", posts.Indexes)
	}
	if len(users.Uniques) != 1 {
		t.Fatalf("users uniques: %#v", users.Uniques)
	}
	if users.Columns[0].Identity != "a" {
		t.Fatalf("users identity: %#v", users.Columns[0])
	}
	if db.Schemas[0].Tables[0].Name != "posts" {
		t.Fatalf("tables are not ordered: %#v", db.Schemas[0].Tables)
	}
	if users.Comment != "Application users" {
		t.Fatalf("users comment = %q", users.Comment)
	}
	if users.Columns[1].Comment != "Login email" {
		t.Fatalf("email comment = %q", users.Columns[1].Comment)
	}
	if posts.Columns[3].NativeType != "timestamp with time zone" {
		t.Fatalf("timestamp type = %q", posts.Columns[3].NativeType)
	}
	if posts.Columns[1].Nullable {
		t.Fatal("author_id should be required")
	}
	if !posts.Columns[3].Nullable {
		t.Fatal("published_at should be nullable")
	}
	if posts.ForeignKeys[0].OnDelete != "NO ACTION" {
		t.Fatalf("delete action = %q", posts.ForeignKeys[0].OnDelete)
	}
	if posts.Indexes[0].Keys[0] != "author_id" {
		t.Fatalf("index keys: %#v", posts.Indexes[0].Keys)
	}
	if users.Schema != "public" {
		t.Fatalf("schema = %q", users.Schema)
	}
	if users.Name != "users" {
		t.Fatalf("table = %q", users.Name)
	}
	if users.PrimaryKey.Name == "" {
		t.Fatal("primary key name is empty")
	}
	if users.Uniques[0].Name == "" {
		t.Fatal("unique name is empty")
	}
	if len(users.Indexes) == 0 || !users.Indexes[0].ConstraintBacked {
		t.Fatalf("constraint-backed indexes: %#v", users.Indexes)
	}
	if users.Indexes[0].Definition == "" {
		t.Fatal("index definition is empty")
	}
	if users.Columns[0].NativeType != "bigint" {
		t.Fatalf("id type = %q", users.Columns[0].NativeType)
	}
	if users.Columns[0].Nullable {
		t.Fatal("identity primary key should be required")
	}
	if users.Columns[1].Default != "" {
		t.Fatalf("email default = %q", users.Columns[1].Default)
	}
	if users.Columns[0].Generated != "" {
		t.Fatalf("id generated = %q", users.Columns[0].Generated)
	}
	if got := strings.Join(users.PrimaryKey.Columns, ","); got != "id" {
		t.Fatalf("users primary key columns = %q", got)
	}
	if got := strings.Join(users.Uniques[0].Columns, ","); got != "email" {
		t.Fatalf("unique columns = %q", got)
	}
	if posts.Comment != "" {
		t.Fatalf("posts comment = %q", posts.Comment)
	}
	if len(posts.Checks) != 0 {
		t.Fatalf("posts checks: %#v", posts.Checks)
	}
	if len(posts.Exclusions) != 0 {
		t.Fatalf("posts exclusions: %#v", posts.Exclusions)
	}
	if posts.Indexes[0].Predicate != "" {
		t.Fatalf("index predicate = %q", posts.Indexes[0].Predicate)
	}
	if len(posts.Indexes[0].IncludedColumns) != 0 {
		t.Fatalf("included columns: %#v", posts.Indexes[0].IncludedColumns)
	}
	if posts.Indexes[0].Unique {
		t.Fatal("posts index should not be unique")
	}
	if posts.Indexes[0].ConstraintBacked {
		t.Fatal("posts index should not back a constraint")
	}
	if len(db.Schemas[0].Tables) != 2 {
		t.Fatal("unexpected table count")
	}
	if posts.Name != "posts" {
		t.Fatal("wrong posts table")
	}
	if len(posts.Columns) != 4 {
		t.Fatalf("posts columns: %#v", posts.Columns)
	}
	if len(users.Columns) != 3 {
		t.Fatalf("users columns: %#v", users.Columns)
	}
	if posts.ForeignKeys[0].LocalColumns[0] != "author_id" || posts.ForeignKeys[0].ReferencedColumns[0] != "id" {
		t.Fatalf("foreign key columns: %#v", posts.ForeignKeys[0])
	}
}

func startPostgres(t *testing.T, ctx context.Context) (string, string) {
	t.Helper()
	output, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--detach",
		"--env", "POSTGRES_DB=schema_test",
		"--env", "POSTGRES_USER=schema_test",
		"--env", "POSTGRES_PASSWORD=schema_test",
		"--publish", "127.0.0.1::5432",
		"postgres:16-alpine",
	).CombinedOutput()
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v: %s", err, output)
	}
	containerID := strings.TrimSpace(string(output))

	output, err = exec.CommandContext(ctx, "docker", "port", containerID, "5432/tcp").CombinedOutput()
	if err != nil {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
		t.Fatalf("get PostgreSQL container port: %v: %s", err, output)
	}
	address := strings.TrimSpace(string(output))
	separator := strings.LastIndexByte(address, ':')
	if separator < 0 {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
		t.Fatalf("unexpected PostgreSQL container port %q", address)
	}
	port, err := strconv.Atoi(address[separator+1:])
	if err != nil {
		_ = exec.Command("docker", "rm", "-f", containerID).Run()
		t.Fatalf("parse PostgreSQL container port %q: %v", address, err)
	}
	return fmt.Sprintf("postgres://schema_test:schema_test@127.0.0.1:%d/schema_test?sslmode=disable", port), containerID
}

func connectPostgres(t *testing.T, ctx context.Context, connectionString string) *pgx.Conn {
	t.Helper()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		conn, err := pgx.Connect(ctx, connectionString)
		if err == nil {
			return conn
		}
		lastErr = err
		select {
		case <-ctx.Done():
			t.Fatalf("connect to PostgreSQL: %v (last connection error: %v)", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}

func findTable(t *testing.T, tables []schema.Table, name string) schema.Table {
	t.Helper()
	for _, table := range tables {
		if table.Name == name {
			return table
		}
	}
	t.Fatalf("table %q not found", name)
	return schema.Table{}
}
