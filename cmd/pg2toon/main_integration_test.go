//go:build integration

package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/kamil5b/pgschema2toon/pkg/toon"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestPostgresSchemaToToon(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := postgres.Run(
		ctx,
		"postgres:16-alpine",
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

	var rawSchema []byte
	if err := conn.QueryRow(ctx, extractQuery).Scan(&rawSchema); err != nil {
		t.Fatalf("extract PostgreSQL schema: %v", err)
	}

	got, err := toon.ToToon(rawSchema)
	if err != nil {
		t.Fatalf("convert schema to TOON: %v", err)
	}

	assertContains(t, got, "[users]")
	assertContains(t, got, "# Application users")
	assertContains(t, got, "id bigint {pk,req}")
	assertContains(t, got, "email varchar(255) {req} // Login email")
	assertContains(t, got, "[posts]")
	assertContains(t, got, "author_id bigint {req} -> users(id)")
	assertContains(t, got, "published_at timestamptz")
	assertContains(t, got, "posts_author_id_idx: btree (author_id)")

	if postsIndex, usersIndex := strings.Index(got, "[posts]"), strings.Index(got, "[users]"); postsIndex < 0 || usersIndex < 0 || postsIndex > usersIndex {
		t.Fatalf("expected tables to be ordered alphabetically; output:\n%s", got)
	}
}

func assertContains(t *testing.T, output, expected string) {
	t.Helper()
	if !strings.Contains(output, expected) {
		t.Fatalf("expected output to contain %q; output:\n%s", expected, output)
	}
}
