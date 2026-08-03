//go:build integration

package main

import (
	"bytes"
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kamil5b/pgschema2toon/internal/database"
	pgadapter "github.com/kamil5b/pgschema2toon/internal/database/postgres"
	"github.com/kamil5b/pgschema2toon/pkg/schema"
	"github.com/kamil5b/pgschema2toon/pkg/toon"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPostgresSchemaToToon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := postgres.Run(
		ctx,
		"pgvector/pgvector:pg16",
		postgres.WithDatabase("schema_test"),
		postgres.WithUsername("schema_test"),
		postgres.WithPassword("schema_test"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate PostgreSQL container: %v", err)
		}
	})

	connectionString, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get PostgreSQL connection string: %v", err)
	}

	conn, err := pgx.Connect(ctx, connectionString)
	if err != nil {
		t.Fatalf("connect to PostgreSQL: %v", err)
	}
	t.Cleanup(func() {
		if err := conn.Close(context.Background()); err != nil {
			t.Errorf("close PostgreSQL connection: %v", err)
		}
	})

	const fixture = `
CREATE EXTENSION vector;

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
    reviewer_id bigint REFERENCES users(id) ON DELETE SET NULL,
    title character varying(200) NOT NULL,
    published_at timestamp with time zone,
    CONSTRAINT posts_title_check CHECK (length(title) > 0)
);
CREATE INDEX posts_author_id_idx ON posts USING btree (author_id);
CREATE INDEX posts_title_partial_idx ON posts USING btree (lower(title)) WHERE title <> '';

INSERT INTO users (email, display_name) VALUES
    ('alice@example.com', 'Alice'),
    ('bob@example.com', 'Bob');
INSERT INTO posts (author_id, title) VALUES
    (1, 'Alice post'),
    (2, 'Bob post');

CREATE TABLE tenants (
    id bigint PRIMARY KEY,
    name text NOT NULL
);
CREATE TABLE tenant_accounts (
    tenant_id bigint NOT NULL,
    account_id bigint NOT NULL,
    name text NOT NULL,
    PRIMARY KEY (tenant_id, account_id),
    UNIQUE (tenant_id, name),
    FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE TABLE account_events (
    tenant_id bigint NOT NULL,
    account_id bigint NOT NULL,
    event_id bigint NOT NULL,
    payload jsonb NOT NULL,
    PRIMARY KEY (tenant_id, account_id, event_id),
    FOREIGN KEY (tenant_id, account_id)
        REFERENCES tenant_accounts(tenant_id, account_id)
        ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT account_events_payload_check CHECK (jsonb_typeof(payload) = 'object')
);
CREATE TABLE user_profiles (
    user_id bigint PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    bio text
);
CREATE TABLE tags (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name text NOT NULL UNIQUE
);
CREATE TABLE post_tags (
    post_id bigint NOT NULL REFERENCES posts(id) ON DELETE CASCADE,
    tag_id bigint NOT NULL REFERENCES tags(id) ON DELETE CASCADE,
    PRIMARY KEY (post_id, tag_id)
);
CREATE TABLE uuid_documents (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    body jsonb NOT NULL
);
CREATE TABLE json_documents (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    body jsonb NOT NULL
);
CREATE INDEX json_documents_body_gin_idx ON json_documents USING gin (body);
CREATE TABLE vector_documents (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    embedding vector(3) NOT NULL
);
CREATE INDEX vector_documents_embedding_hnsw_idx
    ON vector_documents USING hnsw (embedding vector_l2_ops);

INSERT INTO tenants (id, name) VALUES (1, 'Acme');
INSERT INTO tenant_accounts (tenant_id, account_id, name) VALUES (1, 10, 'Primary');
INSERT INTO account_events (tenant_id, account_id, event_id, payload)
VALUES (1, 10, 1, '{"type":"created"}');
INSERT INTO user_profiles (user_id, bio) VALUES (1, 'Alice profile');
INSERT INTO tags (name) VALUES ('go'), ('postgres');
INSERT INTO post_tags (post_id, tag_id) VALUES (1, 1), (1, 2);
INSERT INTO uuid_documents (body) VALUES ('{"kind":"note"}');
INSERT INTO json_documents (body) VALUES ('{"kind":"note"}');
INSERT INTO vector_documents (embedding) VALUES ('[1,2,3]');
`
	if _, err := conn.Exec(ctx, fixture); err != nil {
		t.Fatalf("create schema fixture: %v", err)
	}

	extractor, err := pgadapter.New(ctx, connectionString)
	if err != nil {
		t.Fatalf("create extractor: %v", err)
	}
	t.Cleanup(func() { _ = extractor.Close(context.Background()) })
	db, err := extractor.Extract(ctx, database.ExtractOptions{
		Schemas:              []string{"public"},
		ExampleSample:        2,
		ExampleSampleOrdered: true,
		Seed:                 42,
	})
	if err != nil {
		t.Fatalf("extract schema: %v", err)
	}
	if len(db.Schemas) != 1 || len(db.Schemas[0].Tables) != 11 {
		t.Fatalf("unexpected database: %#v", db)
	}
	posts := findTable(t, db.Schemas[0].Tables, "posts")
	users := findTable(t, db.Schemas[0].Tables, "users")
	if posts.PrimaryKey == nil || strings.Join(posts.PrimaryKey.Columns, ",") != "id" {
		t.Fatalf("posts primary key: %#v", posts.PrimaryKey)
	}
	if len(posts.ForeignKeys) != 2 || posts.ForeignKeys[0].ReferencedTable != "users" {
		t.Fatalf("posts foreign keys: %#v", posts.ForeignKeys)
	}
	if len(posts.Indexes) != 2 || posts.Indexes[0].Method != "btree" {
		t.Fatalf("posts indexes: %#v", posts.Indexes)
	}
	if len(users.Uniques) != 1 {
		t.Fatalf("users uniques: %#v", users.Uniques)
	}
	if users.Columns[0].Identity != "a" {
		t.Fatalf("users identity: %#v", users.Columns[0])
	}
	tableNames := make([]string, 0, len(db.Schemas[0].Tables))
	for _, table := range db.Schemas[0].Tables {
		tableNames = append(tableNames, table.Name)
	}
	if !sort.StringsAreSorted(tableNames) {
		t.Fatalf("tables are not ordered: %#v", tableNames)
	}
	if users.Comment != "Application users" {
		t.Fatalf("users comment = %q", users.Comment)
	}
	if users.Columns[1].Comment != "Login email" {
		t.Fatalf("email comment = %q", users.Columns[1].Comment)
	}
	if posts.Columns[4].NativeType != "timestamp with time zone" {
		t.Fatalf("timestamp type = %q", posts.Columns[4].NativeType)
	}
	if posts.Columns[1].Nullable {
		t.Fatal("author_id should be required")
	}
	if !posts.Columns[4].Nullable {
		t.Fatal("published_at should be nullable")
	}
	if !posts.Columns[2].Nullable {
		t.Fatal("reviewer_id should be nullable")
	}
	if posts.ForeignKeys[0].OnDelete != "NO ACTION" {
		t.Fatalf("delete action = %q", posts.ForeignKeys[0].OnDelete)
	}
	if posts.Indexes[0].Keys[0] != "author_id" || posts.Indexes[1].Keys[0] != "lower(title::text)" {
		t.Fatalf("index keys: %#v", posts.Indexes)
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
	if len(posts.Checks) != 1 {
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
	if len(db.Schemas[0].Tables) != 11 {
		t.Fatal("unexpected table count")
	}
	if posts.Name != "posts" {
		t.Fatal("wrong posts table")
	}
	if len(posts.Columns) != 5 {
		t.Fatalf("posts columns: %#v", posts.Columns)
	}
	if len(users.Columns) != 3 {
		t.Fatalf("users columns: %#v", users.Columns)
	}
	if users.Example == nil || len(users.Example.Rows) != 2 {
		t.Fatalf("users examples: %#v", users.Example)
	}
	if got := strings.Join(users.Example.Columns, ","); got != "id,email,display_name" {
		t.Fatalf("users example columns = %q", got)
	}
	if got := users.Example.Rows[0][1]; got != "alice@example.com" {
		t.Fatalf("first user example email = %#v", got)
	}
	if got := users.Example.Rows[1][1]; got != "bob@example.com" {
		t.Fatalf("second user example email = %#v", got)
	}
	if posts.Example == nil || len(posts.Example.Rows) != 2 {
		t.Fatalf("posts examples: %#v", posts.Example)
	}

	var output bytes.Buffer
	if err := toon.Encode(&output, db); err != nil {
		t.Fatalf("encode examples: %v", err)
	}
	toonOutput := output.String()
	t.Logf("TOON output:\n%s", toonOutput)
	if !strings.Contains(toonOutput, "@example[2]{id,email,display_name}:\n  1,alice@example.com,Alice\n  2,bob@example.com,Bob") {
		t.Fatalf("users examples missing from TOON output:\n%s", toonOutput)
	}
	accounts := findTable(t, db.Schemas[0].Tables, "tenant_accounts")
	if accounts.PrimaryKey == nil || strings.Join(accounts.PrimaryKey.Columns, ",") != "tenant_id,account_id" {
		t.Fatalf("composite primary key: %#v", accounts.PrimaryKey)
	}
	events := findTable(t, db.Schemas[0].Tables, "account_events")
	if len(events.ForeignKeys) != 1 || strings.Join(events.ForeignKeys[0].LocalColumns, ",") != "tenant_id,account_id" {
		t.Fatalf("composite foreign key: %#v", events.ForeignKeys)
	}
	if events.ForeignKeys[0].OnUpdate != "CASCADE" || events.ForeignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("composite foreign key actions: %#v", events.ForeignKeys[0])
	}
	if len(events.Checks) != 1 {
		t.Fatalf("event checks: %#v", events.Checks)
	}
	profile := findTable(t, db.Schemas[0].Tables, "user_profiles")
	if len(profile.Uniques) != 0 || len(profile.ForeignKeys) != 1 {
		t.Fatalf("one-to-one relationship: %#v", profile)
	}
	join := findTable(t, db.Schemas[0].Tables, "post_tags")
	if len(join.ForeignKeys) != 2 || join.PrimaryKey == nil {
		t.Fatalf("many-to-many join table: %#v", join)
	}
	uuidDocuments := findTable(t, db.Schemas[0].Tables, "uuid_documents")
	if uuidDocuments.Columns[0].Default != "gen_random_uuid()" {
		t.Fatalf("uuid default: %q", uuidDocuments.Columns[0].Default)
	}
	jsonDocuments := findTable(t, db.Schemas[0].Tables, "json_documents")
	if len(jsonDocuments.Indexes) != 1 || jsonDocuments.Indexes[0].Method != "gin" {
		t.Fatalf("jsonb GIN index: %#v", jsonDocuments.Indexes)
	}
	vectorDocuments := findTable(t, db.Schemas[0].Tables, "vector_documents")
	if len(vectorDocuments.Indexes) != 1 || vectorDocuments.Indexes[0].Method != "hnsw" {
		t.Fatalf("vector HNSW index: %#v", vectorDocuments.Indexes)
	}
	withoutVectorExamples, err := extractor.Extract(ctx, database.ExtractOptions{
		Schemas:              []string{"public"},
		ExampleSample:        2,
		ExampleSampleOrdered: true,
		ExcludeExampleTables: []string{"vector_documents"},
	})
	if err != nil {
		t.Fatalf("extract schema without vector examples: %v", err)
	}
	if findTable(t, withoutVectorExamples.Schemas[0].Tables, "vector_documents").Example != nil {
		t.Fatal("vector_documents should not have examples")
	}
	withoutJSONDocuments, err := extractor.Extract(ctx, database.ExtractOptions{
		Schemas:       []string{"public"},
		ExcludeTables: []string{"json_documents"},
	})
	if err != nil {
		t.Fatalf("extract schema without json documents: %v", err)
	}
	if len(withoutJSONDocuments.Schemas[0].Tables) != 10 {
		t.Fatalf("excluded table count = %d, want 10", len(withoutJSONDocuments.Schemas[0].Tables))
	}
	if hasTable(withoutJSONDocuments.Schemas[0].Tables, "json_documents") {
		t.Fatal("json_documents should be excluded")
	}
	withoutUserEmailExamples, err := extractor.Extract(ctx, database.ExtractOptions{
		Schemas:              []string{"public"},
		ExampleSample:        2,
		ExampleSampleOrdered: true,
		ExcludeExampleFields: []string{"public.users.email"},
	})
	if err != nil {
		t.Fatalf("extract schema without user email examples: %v", err)
	}
	userEmailExcluded := findTable(t, withoutUserEmailExamples.Schemas[0].Tables, "users")
	if userEmailExcluded.Example == nil || strings.Join(userEmailExcluded.Example.Columns, ",") != "id,display_name" {
		t.Fatalf("example columns after field exclusion = %q", strings.Join(userEmailExcluded.Example.Columns, ","))
	}
	if len(userEmailExcluded.Columns) != 3 {
		t.Fatalf("schema columns changed after field exclusion: %#v", userEmailExcluded.Columns)
	}
	if posts.ForeignKeys[0].LocalColumns[0] != "author_id" || posts.ForeignKeys[0].ReferencedColumns[0] != "id" {
		t.Fatalf("foreign key columns: %#v", posts.ForeignKeys[0])
	}
}

func hasTable(tables []schema.Table, name string) bool {
	for _, table := range tables {
		if table.Name == name {
			return true
		}
	}
	return false
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
