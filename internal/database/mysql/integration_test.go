//go:build integration

package mysql

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
	"github.com/testcontainers/testcontainers-go"
	mysqlcontainer "github.com/testcontainers/testcontainers-go/modules/mysql"
)

func TestMySQLSchemaExtraction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := mysqlcontainer.Run(ctx, "mysql:8.0.36",
		mysqlcontainer.WithDatabase("schema_test"),
		mysqlcontainer.WithUsername("schema_test"),
		mysqlcontainer.WithPassword("schema_test"),
	)
	if err != nil {
		t.Fatalf("start MySQL container: %v", err)
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Errorf("terminate MySQL container: %v", err)
		}
	})

	dsn, err := container.ConnectionString(ctx, "parseTime=true", "multiStatements=true")
	if err != nil {
		t.Fatalf("get MySQL connection string: %v", err)
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatalf("connect to MySQL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	const fixture = `
CREATE TABLE users (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  email VARCHAR(255) NOT NULL,
  display_name TEXT,
  UNIQUE KEY users_email_unique (email),
  CHECK (CHAR_LENGTH(email) > 0)
);
CREATE TABLE posts (
  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
  author_id BIGINT NOT NULL,
  title VARCHAR(200) NOT NULL DEFAULT 'untitled',
  CONSTRAINT posts_author_fk FOREIGN KEY (author_id) REFERENCES users(id)
    ON UPDATE CASCADE ON DELETE CASCADE
);
CREATE INDEX posts_title_idx ON posts(title);
CREATE VIEW post_titles AS SELECT id, title FROM posts;
INSERT INTO users (email, display_name) VALUES ('alice@example.com', 'Alice'), ('bob@example.com', 'Bob');
INSERT INTO posts (author_id, title) VALUES (1, 'Hello'), (2, 'World');
`
	if _, err := conn.ExecContext(ctx, fixture); err != nil {
		t.Fatalf("create MySQL fixture: %v", err)
	}

	extractor, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("create extractor: %v", err)
	}
	t.Cleanup(func() { _ = extractor.Close(context.Background()) })
	db, err := extractor.Extract(ctx, database.ExtractOptions{
		Schemas:              []string{"schema_test"},
		IncludeViews:         true,
		ExampleSample:        1,
		ExcludeExampleFields: []string{"users.email"},
	})
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
	if len(posts.ForeignKeys) != 1 || posts.ForeignKeys[0].Name != "posts_author_fk" || posts.ForeignKeys[0].OnDelete != "CASCADE" {
		t.Fatalf("posts foreign key: %#v", posts.ForeignKeys)
	}
	if !hasIndex(posts.Indexes, "posts_title_idx") {
		t.Fatalf("posts indexes: %#v", posts.Indexes)
	}
	if posts.Example == nil || len(posts.Example.Rows) != 1 {
		t.Fatalf("posts examples: %#v", posts.Example)
	}
	if users.Example == nil || strings.Join(users.Example.Columns, ",") != "id,display_name" {
		t.Fatalf("excluded example field: %#v", users.Example)
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

func hasIndex(indexes []schema.Index, name string) bool {
	for _, index := range indexes {
		if index.Name == name {
			return true
		}
	}
	return false
}
