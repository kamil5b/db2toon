// Package mcp implements the MCP JSON-RPC surface used by db2toon.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"strings"
	"sync"

	"github.com/kamil5b/db2toon/internal/service"
)

const protocolVersion = "2024-11-05"

type Server struct {
	in  io.Reader
	out io.Writer

	mu     sync.Mutex
	active map[string]context.CancelFunc
	write  sync.Mutex
}

func NewServer(in io.Reader, out io.Writer) *Server {
	return &Server{in: in, out: out, active: make(map[string]context.CancelFunc)}
}

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
	var wg sync.WaitGroup
	for scanner.Scan() {
		var req rpcRequest
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			if err := s.writeResponse(rpcResponse{JSONRPC: "2.0", Error: rpcError(-32700, "invalid JSON")}); err != nil {
				return err
			}
			continue
		}
		if req.JSONRPC != "2.0" || req.Method == "" {
			if !isNotification(req) {
				if err := s.writeResponse(rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcError(-32600, "invalid request")}); err != nil {
					return err
				}
			}
			continue
		}
		if strings.HasPrefix(req.Method, "notifications/") || isNotification(req) {
			s.handleNotification(req)
			continue
		}

		requestCtx, cancel := context.WithCancel(ctx)
		s.register(req.ID, cancel)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer s.unregister(req.ID)
			response := s.handle(requestCtx, req)
			_ = s.writeResponse(response)
		}()
	}
	wg.Wait()
	return scanner.Err()
}

func (s *Server) writeResponse(response rpcResponse) error {
	s.write.Lock()
	defer s.write.Unlock()
	return json.NewEncoder(s.out).Encode(response)
}

func (s *Server) register(id json.RawMessage, cancel context.CancelFunc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[string(id)] = cancel
}

func (s *Server) unregister(id json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, string(id))
}

func (s *Server) handleNotification(req rpcRequest) {
	if req.Method != "notifications/cancelled" && req.Method != "$/cancelRequest" {
		return
	}
	var params struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(req.Params, &params) != nil {
		return
	}
	s.mu.Lock()
	cancel := s.active[string(params.RequestID)]
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func isNotification(req rpcRequest) bool { return len(req.ID) == 0 || string(req.ID) == "null" }

func (s *Server) handle(ctx context.Context, req rpcRequest) rpcResponse {
	response := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		var params struct {
			ProtocolVersion string         `json:"protocolVersion"`
			Capabilities    map[string]any `json:"capabilities"`
			ClientInfo      map[string]any `json:"clientInfo"`
			Meta            map[string]any `json:"_meta"`
		}
		if err := decodeStrict(req.Params, &params); err != nil || (params.ProtocolVersion != "" && params.ProtocolVersion != protocolVersion) {
			response.Error = rpcError(-32602, "unsupported protocol version")
			return response
		}
		response.Result = map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]string{"name": "db2toon", "version": "1"},
		}
	case "tools/list":
		response.Result = map[string]any{"tools": []any{toolDefinition()}}
	case "tools/call":
		response.Result = s.callTool(ctx, req.Params)
	default:
		response.Error = rpcError(-32601, "method not found")
	}
	return response
}

func toolDefinition() map[string]any {
	stringArray := func(description string) map[string]any {
		return map[string]any{"type": "array", "items": map[string]any{"type": "string", "minLength": 1}, "description": description}
	}
	options := map[string]any{
		"type": "object", "additionalProperties": false, "description": "Optional schema extraction settings.",
		"properties": map[string]any{
			"schemas":                stringArray("Schemas to inspect. Defaults to public for PostgreSQL."),
			"include_views":          map[string]any{"type": "boolean", "default": false, "description": "Include supported views and materialized views."},
			"include_partitioned":    map[string]any{"type": "boolean", "default": false, "description": "Include partitioned tables when supported."},
			"exclude_tables":         stringArray("Tables to omit entirely. Values may be table or schema.table."),
			"example_sample":         map[string]any{"type": "integer", "minimum": 0, "default": 0, "description": "Number of example rows per table. Example rows may contain sensitive data."},
			"example_sample_ordered": map[string]any{"type": "boolean", "default": false, "description": "Use deterministic ordering when selecting example rows."},
			"exclude_example_tables": stringArray("Tables that may appear in the schema but must not return example rows."),
			"exclude_example_fields": stringArray("Qualified fields to omit from example rows, such as public.users.password_hash."),
			"seed":                   map[string]any{"type": "integer", "default": 0, "description": "Seed used for reproducible sampling when supported."},
			"timeout":                map[string]any{"type": "string", "default": "30s", "description": "Maximum extraction duration as a Go duration."},
			"max_output_bytes":       map[string]any{"type": "integer", "minimum": 1024, "maximum": service.MaxOutputBytes, "default": service.MaxOutputBytes, "description": "Maximum TOON response size in bytes."},
		},
	}
	return map[string]any{
		"name":        "db2toon.extract_schema",
		"description": "Extract a database schema in compact TOON format using read-only metadata queries. Use this tool for tables, columns, relationships, constraints, indexes, views, or database structure. Schema-only extraction is the default. Request example rows only when explicitly asked. This tool does not execute arbitrary SQL or modify the database.",
		"inputSchema": map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"dialect", "db"},
			"properties": map[string]any{
				"dialect": map[string]any{"type": "string", "enum": []string{"postgres"}, "description": "Database engine. Currently only PostgreSQL is supported."},
				"db":      map[string]any{"type": "string", "minLength": 1, "description": "Database connection URL. Credentials are never returned in tool output or errors."},
				"options": options,
			},
		},
	}
}

func (s *Server) callTool(ctx context.Context, raw json.RawMessage) map[string]any {
	var call struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Meta      map[string]any  `json:"_meta"`
	}
	if err := decodeStrict(raw, &call); err != nil || call.Name != "db2toon.extract_schema" {
		return toolError(&service.Error{Code: "INVALID_ARGUMENT", Message: "invalid tool call", Retryable: false})
	}
	var req service.Request
	if err := decodeStrict(call.Arguments, &req); err != nil {
		return toolError(&service.Error{Code: "INVALID_ARGUMENT", Message: "invalid tool arguments", Retryable: false})
	}
	result, toolErr := service.Extract(ctx, req)
	if toolErr != nil {
		return toolError(toolErr)
	}
	return map[string]any{"isError": false, "content": []any{map[string]string{"type": "text", "text": result}}}
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func toolError(err *service.Error) map[string]any {
	return map[string]any{"isError": true, "structuredError": err, "content": []any{map[string]string{"type": "text", "text": err.Message}}}
}

func rpcError(code int, message string) map[string]any {
	return map[string]any{"code": code, "message": message}
}
