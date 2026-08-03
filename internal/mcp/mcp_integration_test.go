//go:build integration

package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

func TestExtractSchemaToolAgainstPostgres(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("tool_test"), postgres.WithUsername("tool_test"),
		postgres.WithPassword("tool_test"), postgres.BasicWaitStrategies())
	if err != nil {
		t.Fatalf("start PostgreSQL container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("get connection string: %v", err)
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	if _, err := conn.Exec(ctx, `CREATE TABLE tool_users (id integer PRIMARY KEY, email text NOT NULL)`); err != nil {
		t.Fatalf("create fixture: %v", err)
	}

	profiles := []struct {
		name         string
		capabilities map[string]any
	}{
		{name: "claude", capabilities: map[string]any{"tools": map[string]any{}}},
		{name: "cursor", capabilities: map[string]any{"roots": map[string]any{"listChanged": true}, "tools": map[string]any{}}},
		{name: "mcp-inspector", capabilities: map[string]any{}},
	}
	for _, profile := range profiles {
		t.Run(profile.name, func(t *testing.T) {
			requests := []map[string]any{
				{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
					"protocolVersion": "2024-11-05", "capabilities": profile.capabilities, "clientInfo": map[string]any{"name": profile.name, "version": "test"},
				}},
				{"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
				{"jsonrpc": "2.0", "id": 3, "method": "tools/call",
					"params": map[string]any{"name": "db2toon.extract_schema", "arguments": map[string]any{
						"dialect": "postgres", "db": dsn,
					}},
				},
			}
			var input bytes.Buffer
			for _, request := range requests {
				encoded, err := json.Marshal(request)
				if err != nil {
					t.Fatalf("encode request: %v", err)
				}
				input.Write(encoded)
				input.WriteByte('\n')
			}
			var output bytes.Buffer
			if err := NewServer(&input, &output).Serve(ctx); err != nil {
				t.Fatalf("invoke tool: %v", err)
			}
			if !strings.Contains(output.String(), `"protocolVersion":"2024-11-05"`) || !strings.Contains(output.String(), "db2toon.extract_schema") || !strings.Contains(output.String(), "[tool_users]") || strings.Contains(output.String(), `"isError":true`) {
				t.Fatalf("unexpected tool output: %s", output.String())
			}
		})
	}
}
