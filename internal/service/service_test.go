package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractValidatesWithoutConnecting(t *testing.T) {
	tests := []struct {
		name    string
		request Request
		want    string
	}{
		{"missing dialect", Request{DB: "postgres://user:secret@host/db"}, "dialect is required"},
		{"unsupported dialect", Request{Dialect: "snowflake", DB: "unused"}, "unsupported database dialect"},
		{"bad timeout", Request{Dialect: "postgres", DB: "unused", Options: Options{Timeout: "nope"}}, "positive duration"},
		{"negative sample", Request{Dialect: "postgres", DB: "unused", Options: Options{ExampleSample: -1}}, "must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Extract(context.Background(), tt.request)
			if err == nil || !strings.Contains(err.Message, tt.want) {
				t.Fatalf("error = %#v, want %q", err, tt.want)
			}
		})
	}
}

func TestCapabilities(t *testing.T) {
	capabilities, ok := CapabilitiesFor("POSTGRES")
	if !ok || capabilities.Dialect != "postgres" || len(capabilities.Options) == 0 {
		t.Fatalf("unexpected capabilities: %#v, %v", capabilities, ok)
	}
	if capabilities, ok := CapabilitiesFor("mysql"); !ok || capabilities.Dialect != "mysql" {
		t.Fatalf("mysql should be advertised: %#v, %v", capabilities, ok)
	}
	if capabilities, ok := CapabilitiesFor("sqlserver"); !ok || capabilities.Dialect != "mssql" {
		t.Fatalf("SQL Server should be advertised: %#v, %v", capabilities, ok)
	}
	if capabilities, ok := CapabilitiesFor("oracle"); !ok || capabilities.Dialect != "oracle" {
		t.Fatalf("Oracle should be advertised: %#v, %v", capabilities, ok)
	}
}

func TestExtractRequiresExactlyOneSource(t *testing.T) {
	for name, req := range map[string]Request{
		"neither": {Dialect: "postgres"},
		"both":    {Dialect: "postgres", DB: "unused", Dump: "unused.sql"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Extract(context.Background(), req)
			if err == nil || !strings.Contains(err.Message, "exactly one") {
				t.Fatalf("error = %#v", err)
			}
		})
	}
}

func TestExtractFromPostgresDump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE public.accounts (id integer NOT NULL);\n"), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := Extract(context.Background(), Request{Dialect: "postgres", Dump: path})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "[accounts]") {
		t.Fatalf("output = %q", output)
	}
}

func TestExtractFromSQLiteDump(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.sql")
	if writeErr := os.WriteFile(path, []byte("CREATE TABLE t (id integer);"), 0600); writeErr != nil {
		t.Fatal(writeErr)
	}
	output, err := Extract(context.Background(), Request{Dialect: "sqlite", Dump: path})
	if err != nil || !strings.Contains(output, "[main.t]") {
		t.Fatalf("output=%q error=%#v", output, err)
	}
}
