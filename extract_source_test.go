package db2toon

import "testing"

func TestDatabaseName(t *testing.T) {
	tests := []struct {
		name    string
		dialect string
		req     Request
		want    string
	}{
		{name: "dump", dialect: "oracle", req: Request{Dump: "/tmp/app-schema.sql"}, want: "app-schema"},
		{name: "DuckDB dump", dialect: "duckdb", req: Request{Dump: "/tmp/analytics.sql"}, want: "analytics"},
		{name: "CockroachDB dump", dialect: "cockroachdb", req: Request{Dump: "/tmp/cluster.sql"}, want: "cluster"},
		{name: "postgres URL", dialect: "postgres", req: Request{DB: "postgres://user:password@db.example/app"}, want: "app"},
		{name: "CockroachDB URL", dialect: "cockroachdb", req: Request{DB: "postgresql://root@db.example:26257/app?sslmode=disable"}, want: "app"},
		{name: "oracle URL", dialect: "oracle", req: Request{DB: "oracle://app:password@db.example:1521/FREEPDB1"}, want: "FREEPDB1"},
		{name: "SQL Server URL", dialect: "mssql", req: Request{DB: "sqlserver://sa:password@db.example:1433?database=app"}, want: "app"},
		{name: "SQL Server DSN", dialect: "mssql", req: Request{DB: "server=db.example;initial catalog=app;user id=sa"}, want: "app"},
		{name: "MySQL DSN", dialect: "mysql", req: Request{DB: "user:password@tcp(db.example:3306)/app?parseTime=true"}, want: "app"},
		{name: "SQLite file", dialect: "sqlite", req: Request{DB: "/tmp/app.db"}, want: "app"},
		{name: "DuckDB file", dialect: "duckdb", req: Request{DB: "/tmp/analytics.duckdb"}, want: "analytics"},
		{name: "DuckDB memory", dialect: "duckdb", req: Request{DB: ":memory:"}, want: "memory"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := databaseName(test.dialect, test.req); got != test.want {
				t.Fatalf("databaseName() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestCanonicalDialect(t *testing.T) {
	if got := canonicalDialect("sqlserver"); got != "mssql" {
		t.Fatalf("canonicalDialect(sqlserver) = %q", got)
	}
}
