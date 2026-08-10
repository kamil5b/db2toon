//go:build integration

package mssql

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestSQLServerSchemaExtraction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	container, err := testcontainers.Run(ctx, "mcr.microsoft.com/mssql/server:2022-latest",
		testcontainers.WithExposedPorts("1433/tcp"),
		testcontainers.WithEnv(map[string]string{
			"ACCEPT_EULA":       "Y",
			"MSSQL_SA_PASSWORD": "Db2toonPassw0rd!",
		}),
		testcontainers.WithWaitStrategy(wait.ForLog("SQL Server is now ready for client connections.")),
	)
	if err != nil {
		t.Fatalf("start SQL Server container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatal(err)
	}
	port, err := container.MappedPort(ctx, "1433/tcp")
	if err != nil {
		t.Fatal(err)
	}
	dsn := fmt.Sprintf("sqlserver://sa:Db2toonPassw0rd!@%s:%s?database=master&encrypt=disable", host, port.Port())
	var extractor *Extractor
	deadline := time.Now().Add(30 * time.Second)
	for {
		extractor, err = New(ctx, dsn)
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("connect to SQL Server: %v", err)
	}
	t.Cleanup(func() { _ = extractor.Close(context.Background()) })
	if _, err := extractor.db.ExecContext(ctx, `CREATE SCHEMA app`); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := extractor.db.ExecContext(ctx, `CREATE TYPE app.account_code FROM nvarchar(24) NOT NULL`); err != nil {
		t.Fatalf("create type: %v", err)
	}
	if _, err := extractor.db.ExecContext(ctx, `CREATE SEQUENCE app.invoice_number AS bigint START WITH 100 INCREMENT BY 5`); err != nil {
		t.Fatalf("create sequence: %v", err)
	}
	if _, err := extractor.db.ExecContext(ctx, `
CREATE TABLE app.users (
 id bigint IDENTITY(1,1) NOT NULL CONSTRAINT pk_users PRIMARY KEY,
 email nvarchar(255) NOT NULL CONSTRAINT uq_users_email UNIQUE,
 display_name nvarchar(100) NULL,
 created_at datetime2 NOT NULL CONSTRAINT df_users_created_at DEFAULT SYSUTCDATETIME(),
 CONSTRAINT ck_users_email CHECK (LEN(email) > 0)
);
CREATE TABLE app.posts (
 id bigint IDENTITY(1,1) NOT NULL CONSTRAINT pk_posts PRIMARY KEY,
 author_id bigint NOT NULL,
 title nvarchar(200) NOT NULL,
 CONSTRAINT fk_posts_author FOREIGN KEY (author_id) REFERENCES app.users(id) ON DELETE CASCADE
);
CREATE INDEX ix_posts_title ON app.posts(title);
EXEC sys.sp_addextendedproperty @name=N'MS_Description', @value=N'Application users', @level0type=N'SCHEMA', @level0name=N'app', @level1type=N'TABLE', @level1name=N'users';
INSERT INTO app.users (email, display_name) VALUES (N'alice@example.com', N'Alice'), (N'bob@example.com', N'Bob');
INSERT INTO app.posts (author_id, title) VALUES (1, N'Hello'), (2, N'World');`); err != nil {
		t.Fatalf("create fixture: %v", err)
	}
	for _, statement := range []string{
		"CREATE VIEW app.user_names AS SELECT id, display_name FROM app.users",
		"CREATE FUNCTION app.user_count() RETURNS int AS BEGIN RETURN (SELECT COUNT(*) FROM app.users); END",
		"CREATE PROCEDURE app.list_users AS SELECT id, email FROM app.users",
		"CREATE TRIGGER app.users_audit ON app.users AFTER INSERT, UPDATE AS BEGIN SET NOCOUNT ON; END",
		"CREATE SYNONYM app.people FOR app.users",
	} {
		if _, err := extractor.db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("create fixture object: %v", err)
		}
	}
	db, err := extractor.Extract(ctx, database.ExtractOptions{Schemas: []string{"app"}, IncludeViews: true, ExampleSample: 2, ExampleSampleOrdered: true})
	if err != nil {
		t.Fatalf("extract schema: %v", err)
	}
	if len(db.Schemas) != 1 || len(db.Schemas[0].Tables) != 3 {
		t.Fatalf("database: %#v", db)
	}
	if len(db.Schemas[0].Types) != 1 || db.Schemas[0].Types[0].Name != "account_code" || db.Schemas[0].Types[0].NativeType != "nvarchar" {
		t.Fatalf("types: %#v", db.Schemas[0].Types)
	}
	if len(db.Schemas[0].Sequences) != 1 || db.Schemas[0].Sequences[0].Name != "invoice_number" || db.Schemas[0].Sequences[0].Increment != "5" {
		t.Fatalf("sequences: %#v", db.Schemas[0].Sequences)
	}
	if len(db.Schemas[0].Synonyms) != 1 || db.Schemas[0].Synonyms[0].Name != "people" {
		t.Fatalf("synonyms: %#v", db.Schemas[0].Synonyms)
	}
	view := findTable(db.Schemas[0].Tables, "user_names")
	if view.Kind != "view" || !strings.Contains(view.Definition, "CREATE VIEW") {
		t.Fatalf("view: %#v", view)
	}
	users := findTable(db.Schemas[0].Tables, "users")
	posts := findTable(db.Schemas[0].Tables, "posts")
	if users.Comment != "Application users" || users.PrimaryKey == nil || strings.Join(users.PrimaryKey.Columns, ",") != "id" {
		t.Fatalf("users: %#v", users)
	}
	if users.Columns[0].Identity != "d" || users.Columns[1].NativeType != "nvarchar(255)" {
		t.Fatalf("columns: %#v", users.Columns)
	}
	if len(users.Uniques) != 1 || len(users.Checks) != 1 {
		t.Fatalf("constraints: %#v", users)
	}
	if len(users.Triggers) != 1 || users.Triggers[0].Timing != "AFTER" || strings.Join(users.Triggers[0].Events, ",") != "INSERT,UPDATE" {
		t.Fatalf("triggers: %#v", users.Triggers)
	}
	if len(posts.ForeignKeys) != 1 || posts.ForeignKeys[0].OnDelete != "CASCADE" || posts.ForeignKeys[0].ReferencedSchema != "app" {
		t.Fatalf("foreign keys: %#v", posts.ForeignKeys)
	}
	if len(posts.Indexes) != 1 || posts.Indexes[0].Name != "ix_posts_title" {
		t.Fatalf("indexes: %#v", posts.Indexes)
	}
	if users.Example == nil || len(users.Example.Rows) != 2 {
		t.Fatalf("examples: %#v", users.Example)
	}
	if len(db.Schemas[0].Routines) != 2 || db.Schemas[0].Routines[0].Name != "list_users" || db.Schemas[0].Routines[1].Name != "user_count" {
		t.Fatalf("routines: %#v", db.Schemas[0].Routines)
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
