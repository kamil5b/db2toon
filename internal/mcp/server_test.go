package mcp

import (
	"bytes"
	"context"
	"encoding/json"
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
	if strings.Contains(text, "secret:password@") || strings.Contains(text, "example.invalid") {
		t.Fatalf("connection details leaked: %s", text)
	}
}

func TestToolSchemaHasStrictMetadata(t *testing.T) {
	definition := toolDefinition()
	schema := definition["inputSchema"].(map[string]any)
	if schema["additionalProperties"] != false {
		t.Fatal("top-level schema must reject unknown properties")
	}
	options := schema["properties"].(map[string]any)["options"].(map[string]any)
	if options["additionalProperties"] != false {
		t.Fatal("options schema must reject unknown properties")
	}
	if !strings.Contains(definition["description"].(string), "read-only metadata") {
		t.Fatal("tool description is incomplete")
	}
}

func TestToolRejectsUnknownArgumentsWithStableError(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"db2toon.extract_schema","arguments":{"dialect":"postgres","db":"unused","unexpected":true}}}` + "\n"
	var output bytes.Buffer
	if err := NewServer(strings.NewReader(input), &output).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	var response struct {
		Result struct {
			StructuredError struct {
				Code      string `json:"code"`
				Retryable bool   `json:"retryable"`
			} `json:"structuredError"`
		} `json:"result"`
	}
	if err := json.Unmarshal(output.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Result.StructuredError.Code != "INVALID_ARGUMENT" || response.Result.StructuredError.Retryable {
		t.Fatalf("unexpected error: %+v", response.Result.StructuredError)
	}
}

func TestInitializeNegotiatesProtocolAndHandlesNotifications(t *testing.T) {
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{"tools":{}},"clientInfo":{"name":"test-client","version":"1"}}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	var output bytes.Buffer
	if err := NewServer(strings.NewReader(input), &output).Serve(context.Background()); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "protocolVersion") != 1 || !strings.Contains(output.String(), "2024-11-05") {
		t.Fatalf("unexpected lifecycle output: %s", output.String())
	}
}
