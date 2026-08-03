# LLM Tool Integration

`db2toon-mcp` exposes PostgreSQL schema extraction through an MCP-compatible
JSON-RPC stdio server. It uses the same extraction and TOON encoding pipeline
as the `db2toon` and `pg2toon` CLIs.

## Build and run

```bash
CGO_ENABLED=0 go build -o output/db2toon-mcp ./cmd/db2toon-mcp
./output/db2toon-mcp
```

Configure the MCP client to launch `output/db2toon-mcp` with no additional
arguments. The server reads JSON-RPC messages from stdin and writes responses
to stdout. Logs and diagnostics should be directed to stderr by the host.

## Tool

The server advertises one tool:

```text
db2toon.extract_schema
```

Required arguments:

- `dialect`: currently `postgres`
- `db`: PostgreSQL connection URL

Optional `options` fields:

- `schemas`: schemas to extract; defaults to `public`
- `include_views`
- `include_partitioned`
- `example_sample`
- `example_sample_ordered`
- `exclude_tables`
- `exclude_example_tables`
- `exclude_example_fields`
- `seed`
- `timeout`: Go duration such as `10s` or `1m`
- `max_output_bytes`: lower response limit, up to 4 MiB

Example tool call arguments:

```json
{
  "dialect": "postgres",
  "db": "postgresql://user:password@localhost/app",
  "options": {
    "schemas": ["public", "audit"],
    "include_views": true,
    "example_sample": 2,
    "example_sample_ordered": true,
    "timeout": "20s",
    "max_output_bytes": 1048576
  }
}
```

The result is TOON text in the tool response.

## Safety and limits

Schema discovery is read-only. The default operation timeout is 30 seconds,
and the maximum response size is 4 MiB. Callers can choose lower limits.
Unsupported options and adapter failures return structured errors. Connection
strings and credentials are not included in error messages or tool results.

The MCP server currently supports PostgreSQL only. Additional database
adapters should be added to the shared service and capability model before
being exposed through this interface.

## Testing

Run unit tests with:

```bash
CGO_ENABLED=0 go test ./...
```

The PostgreSQL-backed MCP integration test requires Docker:

```bash
CGO_ENABLED=0 go test -v -tags=integration ./internal/mcp
```
