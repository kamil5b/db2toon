package mcp

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestServerListsToolAndDoesNotLeakConnectionString(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"db2toon.extract_schema","arguments":{"dialect":"postgres","db":"postgres://secret:password@example.invalid/db"}}}` + "\n"
	var output bytes.Buffer
	if err := NewServer(strings.NewReader(input), &output).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "db2toon.extract_schema") {
		t.Fatalf("tool missing: %s", text)
	}
	if strings.Contains(text, "password") || strings.Contains(text, "example.invalid") {
		t.Fatalf("connection details leaked: %s", text)
	}
}
