# MCP Integration and Registry Plan

## Purpose

Make `db2toon` easy and safe for LLM clients such as Claude, Cursor, VS Code, and other MCP-compatible hosts to use for read-only database schema inspection.

The project already includes a local stdio MCP server:

```text
db2toon-mcp
```

It currently exposes:

```text
db2toon.extract_schema
```

This document defines the work needed to make that server production-ready, easy to install, and publishable in the official MCP Registry.

---

## Goals

1. Preserve the existing `db2toon` and `pg2toon` CLI interfaces.
2. Maintain a dedicated `db2toon-mcp` binary for MCP clients.
3. Let LLM clients discover and invoke schema extraction reliably.
4. Keep database credentials and schema data local by default.
5. Prevent accidental sample-data exposure.
6. Publish `db2toon-mcp` in the official MCP Registry.
7. Support additional database adapters without changing the MCP tool contract unnecessarily.

## Non-goals

For the initial release, `db2toon-mcp` will not:

- execute arbitrary SQL;
- mutate a database;
- provide a centrally hosted database proxy;
- store database credentials;
- support every database engine;
- return table rows unless examples are explicitly requested.

---

## Recommended Architecture

```text
Claude / Cursor / VS Code
          |
          | MCP over stdio
          v
     db2toon-mcp
          |
          v
   internal/service
          |
          v
 database adapter
          |
          v
 PostgreSQL / MySQL / SQLite / other
```

The MCP server must reuse the same extraction service and TOON encoder as the CLI. It should not shell out to `db2toon`.

### Binaries

```text
db2toon       General CLI
pg2toon       PostgreSQL compatibility CLI
db2toon-mcp   Local MCP stdio server
```

---

## MCP Tool Contract

### Initial tool

```text
db2toon.extract_schema
```

### Recommended tool description

The description must be returned by the MCP server in its `tools/list` response. It is part of the machine-facing API, not only README documentation.

```text
Extract a database schema in compact TOON format using read-only metadata
queries. Use this tool when answering questions about tables, columns,
relationships, constraints, indexes, views, or database structure. Schema-only
extraction is the default. Request example rows only when the user explicitly
asks for sample data and understands that examples may contain sensitive data.
This tool does not execute arbitrary SQL or modify the database.
```

### Recommended input schema

```json
{
  "type": "object",
  "additionalProperties": false,
  "required": ["dialect", "db"],
  "properties": {
    "dialect": {
      "type": "string",
      "enum": ["postgres"],
      "description": "Database engine. Currently only PostgreSQL is supported."
    },
    "db": {
      "type": "string",
      "minLength": 1,
      "description": "Database connection URL or path. Credentials must never be returned in tool output or errors."
    },
    "options": {
      "type": "object",
      "additionalProperties": false,
      "description": "Optional schema extraction settings.",
      "properties": {
        "schemas": {
          "type": "array",
          "items": {
            "type": "string",
            "minLength": 1
          },
          "description": "Schemas to inspect. Defaults to public for PostgreSQL."
        },
        "include_views": {
          "type": "boolean",
          "default": false,
          "description": "Include supported views and materialized views."
        },
        "include_partitioned": {
          "type": "boolean",
          "default": false,
          "description": "Include partitioned tables when supported."
        },
        "exclude_tables": {
          "type": "array",
          "items": {
            "type": "string"
          },
          "description": "Tables to omit entirely. Values may be table or schema.table."
        },
        "example_sample": {
          "type": "integer",
          "minimum": 0,
          "default": 0,
          "description": "Number of example rows per table. Defaults to 0. Example rows may contain sensitive application data and should only be requested explicitly."
        },
        "example_sample_ordered": {
          "type": "boolean",
          "default": false,
          "description": "Use deterministic ordering when selecting example rows."
        },
        "exclude_example_tables": {
          "type": "array",
          "items": {
            "type": "string"
          },
          "description": "Tables that may appear in the schema but must not return example rows."
        },
        "exclude_example_fields": {
          "type": "array",
          "items": {
            "type": "string"
          },
          "description": "Qualified fields to omit from example rows, such as public.users.password_hash."
        },
        "seed": {
          "type": "integer",
          "default": 0,
          "description": "Seed used for reproducible sampling when supported."
        },
        "timeout": {
          "type": "string",
          "default": "30s",
          "description": "Maximum extraction duration as a Go duration, such as 10s or 1m."
        },
        "max_output_bytes": {
          "type": "integer",
          "minimum": 1024,
          "maximum": 4194304,
          "default": 4194304,
          "description": "Maximum TOON response size in bytes. The server maximum is 4 MiB."
        }
      }
    }
  }
}
```

### Result

The initial result remains TOON text in MCP tool content.

A later version may optionally support a structured JSON representation, but that should be added through a versioned argument or a separate output-format field.

### Error behavior

Errors are returned as structured MCP tool results and must never expose the
connection URL.

Recommended stable error codes:

```text
INVALID_ARGUMENT
UNSUPPORTED_DIALECT
CONNECTION_FAILED
EXTRACTION_FAILED
TIMEOUT
OUTPUT_TOO_LARGE
INTERNAL_ERROR
```

Each error includes:

```json
{
  "code": "CONNECTION_FAILED",
  "message": "unable to connect to the database",
  "retryable": true
}
```

---

## Claude Integration

A user should be able to configure Claude Desktop to launch the local MCP binary.

Example:

```json
{
  "mcpServers": {
    "db2toon": {
      "command": "/absolute/path/to/db2toon-mcp"
    }
  }
}
```

Windows example:

```json
{
  "mcpServers": {
    "db2toon": {
      "command": "C:\\tools\\db2toon\\db2toon-mcp.exe"
    }
  }
}
```

Claude launches the process, calls `tools/list`, sees `db2toon.extract_schema`, and invokes it when a user asks about database structure.

Example user request:

```text
Review my PostgreSQL schema and identify foreign-key columns that may need indexes.
```

Example tool arguments:

```json
{
  "dialect": "postgres",
  "db": "postgresql://user:password@localhost/app",
  "options": {
    "schemas": ["public"],
    "include_views": true,
    "example_sample": 0
  }
}
```

---

## Credential Handling

Passing `db` in every tool call is functional but not ideal. It makes the connection string visible to the MCP client and part of tool-call history.

### Phase 1

Keep the current `db` argument for compatibility.

Requirements:

- redact credentials from all errors;
- never include the DSN in tool results;
- never write the DSN to stdout;
- avoid logging it to stderr;
- document that users should not paste production credentials into ordinary chat messages.

### Phase 2: Named connections

Add optional local configuration so the LLM chooses a connection name instead of receiving the DSN.

Example configuration:

```json
{
  "connections": {
    "local-app": {
      "dialect": "postgres",
      "db_env": "LOCAL_APP_DATABASE_URL",
      "default_schemas": ["public"]
    },
    "analytics": {
      "dialect": "postgres",
      "db_env": "ANALYTICS_DATABASE_URL",
      "default_schemas": ["analytics"]
    }
  }
}
```

Example MCP configuration:

```json
{
  "mcpServers": {
    "db2toon": {
      "command": "/usr/local/bin/db2toon-mcp",
      "env": {
        "DB2TOON_CONFIG": "/home/user/.config/db2toon/connections.json",
        "LOCAL_APP_DATABASE_URL": "postgresql://user:password@localhost/app"
      }
    }
  }
}
```

The tool contract can then support:

```json
{
  "connection": "local-app",
  "options": {
    "example_sample": 0
  }
}
```

Rules:

- accept either `connection` or raw `db`, not both;
- prefer named connections in documentation;
- resolve secrets only inside the MCP process;
- ensure config-file permissions are documented;
- never expose environment-variable values.

---

## Safety Requirements

### Database access

- Read-only metadata extraction only.
- No arbitrary SQL tool.
- No schema mutation.
- No write transaction.
- Apply a hard timeout.
- Apply a hard output-size limit.
- Support cancellation when the MCP client disconnects.

### Example data

- `example_sample` defaults to `0`.
- Tool and parameter descriptions must warn about sensitive data.
- Whole-table and field exclusions must be honored.
- Prefer excluding fields in the generated `SELECT` list rather than reading them and filtering afterward.
- Consider an optional deny-by-default examples policy for named connections.

Example future connection policy:

```json
{
  "connections": {
    "production": {
      "dialect": "postgres",
      "db_env": "PRODUCTION_DATABASE_URL",
      "allow_examples": false
    }
  }
}
```

### Network protection

If a remote HTTP mode is ever added:

- protect against SSRF;
- restrict outbound destinations;
- add authentication and tenant isolation;
- do not become a public database proxy;
- support private networking deliberately;
- maintain audit logs without logging credentials.

The first registry release should remain a local stdio server.

---

## Protocol Compliance

Before registry publication, verify that `db2toon-mcp` correctly handles at least:

- `initialize`;
- initialized lifecycle behavior;
- `tools/list`;
- `tools/call`;
- unknown methods;
- invalid JSON-RPC;
- invalid tool names;
- invalid arguments;
- cancellation or context termination where supported;
- protocol version negotiation;
- JSON-RPC IDs and error responses.

Avoid implementing only a line-based subset that works in one integration test but fails against standard MCP clients.

Prefer using a maintained Go MCP SDK if it provides stronger interoperability than the custom server, provided the dependency remains reasonably small and auditable.

---

## Testing Plan

### Unit tests

- Tool name and description are returned by `tools/list`.
- Input schema contains descriptions, defaults, enums, limits, and `additionalProperties: false`.
- Unknown arguments are rejected.
- Unsupported dialects return `UNSUPPORTED_DIALECT`.
- Negative sample counts are rejected.
- Excessive output limits are rejected.
- Credentials are redacted from every error path.
- Examples default to disabled.
- Output limit is enforced.
- Timeout is enforced.
- Unknown tool calls return a protocol-compliant error.

### Integration tests

Use Testcontainers for PostgreSQL:

- launch PostgreSQL;
- create tables, foreign keys, indexes, comments, views, JSONB, and sensitive columns;
- start the MCP server through stdin/stdout;
- perform initialization;
- call `tools/list`;
- call `db2toon.extract_schema`;
- verify TOON output;
- verify excluded tables and fields;
- verify sample rows are absent by default;
- verify credentials are absent from responses;
- verify timeout and output-size behavior.

### Client smoke tests

Test released binaries with:

- Claude Desktop;
- Cursor;
- VS Code or another MCP-compatible client;
- MCP Inspector or the current official debugging tool.

Document the versions used for each release.

The Testcontainers integration suite provides protocol compatibility profiles
for Claude, Cursor, and MCP Inspector against a real PostgreSQL container. It
does not launch those proprietary or desktop clients; actual client smoke
testing remains a release-validation task.

---

## Packaging Strategy

The official MCP Registry stores metadata, not binary artifacts. `db2toon-mcp`
is distributed as a public multi-platform OCI image on GHCR.

### Preferred initial distribution

Continue publishing native binaries through GitHub Releases:

```text
db2toon
pg2toon
db2toon-mcp
```

Target platforms:

```text
linux/amd64
linux/arm64
darwin/amd64
darwin/arm64
windows/amd64
windows/arm64
```

Also publish:

- SHA-256 checksums;
- release notes;
- provenance or signatures when practical;
- an SBOM when practical.

### Registry packaging

The release workflow builds and pushes `ghcr.io/kamil5b/db2toon` as a
multi-platform OCI image. The image contains the
`io.modelcontextprotocol.server.name` ownership label required by the
registry. The workflow updates `server.json` with the release tag before
publishing it through GitHub OIDC.

---

## Official MCP Registry Plan

The Registry is currently in preview. Its schemas, CLI behavior, and data may change before general availability.

### Proposed registry identity

Using GitHub authentication:

```text
io.github.kamil5b/db2toon-mcp
```

This name must remain stable after publication.

### Registry metadata description

Keep the registry description concise:

```text
Read-only database schema extraction to compact TOON for LLM tools.
```

### Proposed `server.json` shape

The exact package section must be generated and validated using the current `mcp-publisher` version at publication time.

Illustrative example only:

```json
{
  "$schema": "https://static.modelcontextprotocol.io/schemas/2025-12-11/server.schema.json",
  "name": "io.github.kamil5b/db2toon-mcp",
  "description": "Read-only database schema extraction to compact TOON for LLM tools.",
  "repository": {
    "url": "https://github.com/kamil5b/db2toon",
    "source": "github"
  },
  "version": "0.1.0",
  "packages": [
    {
      "registryType": "oci",
      "identifier": "ghcr.io/kamil5b/db2toon:v0.0.0",
      "transport": {
        "type": "stdio"
      }
    }
  ]
}
```

Do not copy this unchanged. Run:

```bash
mcp-publisher init
```

and use the schema produced by the installed publisher version.

### Manual publication steps

1. Create a tagged `db2toon` release.
2. Publish the installable package or artifact referenced by `server.json`.
3. Install the latest official `mcp-publisher`.
4. Generate or refresh metadata:

   ```bash
   mcp-publisher init
   ```

5. Validate:
   - server name;
   - package identifier;
   - package version;
   - stdio transport;
   - repository URL;
   - description length;
   - package verification metadata.

6. Authenticate using GitHub OIDC in Actions, or GitHub login locally:

   ```bash
   mcp-publisher login github
   ```

7. Publish:

   ```bash
   mcp-publisher publish
   ```

8. Verify through the Registry search API:

   ```bash
   curl "https://registry.modelcontextprotocol.io/v0.1/servers?search=io.github.kamil5b/db2toon-mcp"
   ```

9. Test installation from at least one registry-aware MCP client or downstream registry.

### Automated publication

After one successful manual release, add a GitHub Actions workflow triggered by version tags.

The workflow should:

1. run all tests;
2. build release binaries;
3. publish the Registry-supported package;
4. verify package availability;
5. update or validate `server.json`;
6. authenticate using the officially supported CI method;
7. run `mcp-publisher publish`;
8. query the Registry API to verify the new version.

Avoid automating publication until the manual process is proven.

---

## Proposed Repository Files

```text
MCP.md
server.json
docs/
  MCP_CLIENTS.md
  SECURITY.md
cmd/
  db2toon-mcp/
internal/
  mcp/
  service/
.github/
  workflows/
    release.yml
    mcp-registry.yml
```

### README changes

Add a concise section:

```text
Use db2toon with Claude and other MCP clients through db2toon-mcp.
```

Link to:

- `MCP.md` for architecture and roadmap;
- `docs/MCP_CLIENTS.md` for installation;
- `SECURITY.md` for credential and data-safety guidance;
- the official Registry listing after publication.

---

## Versioning and Compatibility

Treat these as public API:

- MCP server name;
- tool names;
- argument names;
- argument meanings;
- result format;
- structured error codes;
- registry server name.

Guidelines:

- do not rename `db2toon.extract_schema` casually;
- add optional arguments compatibly;
- use explicit versions for breaking result-format changes;
- keep PostgreSQL behavior stable while adding new dialects;
- publish release notes for tool-schema changes;
- include tool-contract snapshot tests.

Future dialects should reuse the same tool:

```json
{
  "dialect": "mysql",
  "db": "..."
}
```

Capabilities that differ by database should be documented and, later, exposed through a discovery tool or capability metadata.

---

## Future Tools

Do not add these until there is a concrete use case:

```text
db2toon.list_connections
db2toon.list_schemas
db2toon.capabilities
db2toon.compare_schemas
db2toon.validate_toon
```

Prefer a small number of focused tools over a large catch-all tool.

The next likely addition is:

```text
db2toon.capabilities
```

It could report supported dialects and options without opening a database connection.

---

## Milestones

### Milestone 1: Tool quality

- [x] Improve `tools/list` tool description.
- [x] Add descriptions to every input property.
- [x] Reject unknown arguments.
- [x] Confirm MCP lifecycle and protocol compliance.
- [x] Add structured stable errors.
- [ ] Add tool-contract snapshot tests.
- [x] Ensure examples default to disabled.
- [x] Confirm DSNs are redacted everywhere.

### Milestone 2: Client readiness

- [x] Add Claude Desktop configuration instructions.
- [ ] Add Windows, macOS, and Linux examples.
- [ ] Test with Claude Desktop.
- [ ] Test with Cursor.
- [ ] Test with MCP Inspector.
- [x] Publish downloadable `db2toon-mcp` binaries.
- [ ] Publish checksums.

### Milestone 3: Secure connection configuration

- [ ] Design named connection configuration.
- [ ] Support secrets from environment variables.
- [ ] Add connection-level example policies.
- [ ] Keep raw `db` input for compatibility.
- [ ] Document config-file permissions.

### Milestone 4: Registry packaging

- [x] Confirm current Registry-supported package types.
- [x] Select the OCI/GHCR package strategy for Go binaries.
- [x] Publish the installable package.
- [ ] Add package verification metadata.
- [ ] Generate `server.json` using `mcp-publisher init`.
- [ ] Validate registry identity `io.github.kamil5b/db2toon-mcp`.

### Milestone 5: Registry publication

- [ ] Tag a release.
- [ ] Authenticate with GitHub.
- [ ] Run `mcp-publisher publish`.
- [ ] Verify the Registry API result.
- [ ] Test installation from a registry-aware client.
- [ ] Add the Registry listing to README.

### Milestone 6: Release automation

- [ ] Automate package and binary publication.
- [ ] Automate Registry publication.
- [ ] Verify publication after each release.
- [ ] Document recovery steps for failed publication.

---

## Definition of Done

`db2toon-mcp` is ready for official Registry publication when:

- Claude can discover and call it without custom prompting;
- the tool description clearly explains when and when not to use it;
- every input field has a precise description;
- the server behaves correctly through standard MCP initialization and tool calls;
- examples are disabled by default;
- credentials never appear in results, logs, or errors;
- released binaries work on the documented platforms;
- the installable package referenced by `server.json` is public;
- `server.json` validates against the current official schema;
- publication succeeds with `mcp-publisher`;
- the Registry API returns the published version;
- installation is documented and tested.

---

## Official References

- MCP Registry overview: https://modelcontextprotocol.io/registry/about
- Registry publishing quickstart: https://modelcontextprotocol.io/registry/quickstart
- Official Registry API: https://registry.modelcontextprotocol.io
- MCP specification and documentation: https://modelcontextprotocol.io
- Registry source and publisher releases: https://github.com/modelcontextprotocol/registry

Because the Registry is in preview, verify the current schema and publisher workflow before every publication.
