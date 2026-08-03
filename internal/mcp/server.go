// Package mcp implements the small MCP JSON-RPC surface used by db2toon.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"

	"github.com/kamil5b/db2toon/internal/service"
)

type Server struct {
	in  io.Reader
	out io.Writer
}

func NewServer(in io.Reader, out io.Writer) *Server { return &Server{in: in, out: out} }

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}
type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   any             `json:"error,omitempty"`
}

func (s *Server) Serve(ctx context.Context) error {
	scanner := bufio.NewScanner(s.in)
	scanner.Buffer(make([]byte, 1024), 1<<20)
	enc := json.NewEncoder(s.out)
	for scanner.Scan() {
		var req rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			if err := enc.Encode(rpcResponse{JSONRPC: "2.0", Error: map[string]any{"code": -32700, "message": "invalid JSON"}}); err != nil {
				return err
			}
			continue
		}
		if req.Method == "notifications/initialized" {
			continue
		}
		resp := s.handle(ctx, req)
		if err := enc.Encode(resp); err != nil {
			return err
		}
	}
	return scanner.Err()
}

func (s *Server) handle(ctx context.Context, req rpcRequest) rpcResponse {
	response := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		response.Result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{"tools": map[string]any{}}, "serverInfo": map[string]string{"name": "db2toon", "version": "1"}}
	case "tools/list":
		response.Result = map[string]any{"tools": []any{toolDefinition()}}
	case "tools/call":
		response.Result = s.callTool(ctx, req.Params)
	default:
		response.Error = map[string]any{"code": -32601, "message": "method not found"}
	}
	return response
}

func toolDefinition() map[string]any {
	return map[string]any{"name": "db2toon.extract_schema", "description": "Read-only database schema extraction rendered as TOON. Supported dialect: postgres. Credentials are never returned in errors.", "inputSchema": map[string]any{"type": "object", "required": []string{"dialect", "db"}, "properties": map[string]any{
		"dialect": map[string]any{"type": "string", "enum": []string{"postgres"}}, "db": map[string]any{"type": "string", "description": "Database connection URL or path"},
		"options": map[string]any{"type": "object", "properties": map[string]any{"schemas": map[string]any{"type": "array", "items": map[string]string{"type": "string"}}, "include_views": map[string]string{"type": "boolean"}, "include_system": map[string]string{"type": "boolean"}, "include_partitioned": map[string]string{"type": "boolean"}, "example_sample": map[string]any{"type": "integer", "minimum": 0}, "example_sample_ordered": map[string]string{"type": "boolean"}, "exclude_tables": map[string]any{"type": "array", "items": map[string]string{"type": "string"}}, "exclude_example_tables": map[string]any{"type": "array", "items": map[string]string{"type": "string"}}, "exclude_example_fields": map[string]any{"type": "array", "items": map[string]string{"type": "string"}}, "seed": map[string]string{"type": "integer"}, "timeout": map[string]string{"type": "string"}, "max_output_bytes": map[string]any{"type": "integer"}}},
	}}}
}

func (s *Server) callTool(ctx context.Context, raw json.RawMessage) map[string]any {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &call); err != nil || call.Name != "db2toon.extract_schema" {
		return map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": "invalid tool call"}}}
	}
	var req service.Request
	if err := json.Unmarshal(call.Arguments, &req); err != nil {
		return map[string]any{"isError": true, "content": []any{map[string]string{"type": "text", "text": "invalid tool arguments"}}}
	}
	result, toolErr := service.Extract(ctx, req)
	if toolErr != nil {
		return map[string]any{"isError": true, "structuredError": toolErr, "content": []any{map[string]string{"type": "text", "text": toolErr.Message}}}
	}
	return map[string]any{"isError": false, "content": []any{map[string]string{"type": "text", "text": result}}}
}
