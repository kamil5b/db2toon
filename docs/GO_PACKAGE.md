# Public Go package plan

## Goal

Make `db2toon` usable as a normal Go library while preserving the existing
`db2toon`, `pg2toon`, and MCP entry points.

External users should be able to extract a canonical `pkg/schema.Database`
from either a live database or a supported SQL dump, then optionally encode it
as TOON without importing repository-private packages.

## Current gap

The extraction service and all database adapters are under `internal/`, so
they cannot be imported by programs outside this module. `pkg/schema` and
`pkg/toon` are public, but there is no public extraction facade.

The CLI and MCP layers also currently expose transport-oriented request and
error behavior that should not become the library API by accident.

## Public package shape

Add a small stable facade at the module root, or in a dedicated public package
such as `pkg/db2toon`:

```text
db2toon/
    extract.go          # public extraction facade
    options.go          # public request and option types
    errors.go           # typed, safe errors

pkg/schema/
    model.go            # canonical output model
pkg/toon/
    encoder.go          # TOON encoder

internal/service/       # CLI/MCP orchestration and output limits
internal/database/      # live and dump adapters
```

Prefer the module-root package if the import path `github.com/kamil5b/db2toon`
is intended to be the primary user experience. Keep `pkg/schema` and
`pkg/toon` available for callers that need model or encoder-level control.

## Proposed API

The first public API should return the canonical model and leave formatting to
the caller:

```go
package db2toon

type Request struct {
    Dialect string
    DB      string
    Dump    string
    Options Options
}

type Options struct {
    Schemas              []string
    IncludeViews         bool
    IncludePartitioned   bool
    ExampleSample        int
    ExampleSampleOrdered bool
    ExcludeTables        []string
    ExcludeExampleTables []string
    ExcludeExampleFields []string
    Seed                 int64
}

func Extract(ctx context.Context, req Request) (*schema.Database, error)
func Encode(w io.Writer, db *schema.Database) error
```

API rules:

- Require exactly one of `Request.DB` and `Request.Dump`.
- Normalize dialect names case-insensitively.
- Never execute dump contents.
- Honor `context.Context` throughout connection, file, parsing, and sampling
  work.
- Return a database-neutral `*schema.Database`.
- Keep output size limits and transport concerns out of the core library API.
- Make slices in `Options` caller-owned inputs; adapters must not mutate them.
- Document which options are meaningful for each dialect and source type.

Optional convenience functions may be added after the core API is stable:

```go
func ExtractFromDB(ctx context.Context, dialect, dsn string, opts Options) (*schema.Database, error)
func ExtractFromDump(ctx context.Context, dialect, path string, opts Options) (*schema.Database, error)
```

These should delegate to `Extract` rather than create a second architecture.

## Error contract

Define exported error types that retain safe machine-readable context without
including passwords, DSNs, or dump contents:

```go
type Error struct {
    Operation string
    Dialect   string
    Source    string // database or dump, never the secret-bearing value
    Line      int
    Statement int
    Err       error
}
```

The error should support `errors.Is`/`errors.As` through `Unwrap`. Use sentinel
or typed errors for invalid requests, unsupported dialects/formats, unreadable
files, connection failures, parse failures, timeouts, and canceled contexts.

The CLI and MCP service may continue translating these errors into their
existing structured error responses, but the public package must not depend on
MCP or CLI types.

## Adapter boundary

Keep the existing internal interface as the implementation seam:

```go
type Extractor interface {
    Extract(context.Context, database.ExtractOptions) (*schema.Database, error)
    Close(context.Context) error
}
```

The public facade should select adapters internally. External callers should
not need to know whether an extractor is live, file-backed, PostgreSQL-based,
or implemented through a shared SQL parser.

Each supported dialect must continue to own its constructor and parser:

```text
internal/database/postgres/
internal/database/sqlite/
internal/database/mysql/
internal/database/duckdb/
internal/database/cockroachdb/
```

Shared tokenization and value conversion may remain in `internal/database/sqlutil`
when the behavior is genuinely dialect-independent.

## Encoding API

Keep TOON encoding independently usable:

```go
var db schema.Database
db, err := db2toon.Extract(ctx, request)
if err != nil { ... }
if err := toon.Encode(os.Stdout, db); err != nil { ... }
```

`db2toon.Encode` may be a thin convenience alias, but `pkg/toon.Encode` should
remain the implementation and support deterministic output from the canonical
model.

Do not put file output, stdout handling, or maximum response size into the
core encoder or extractor.

## Compatibility plan

1. Add the public facade without changing current CLI or MCP behavior.
2. Refactor `internal/service.Extract` to call the public facade where this
   does not create an import cycle.
3. Preserve `pg2toon` as a fixed-dialect compatibility wrapper.
4. Preserve JSON field names and MCP request validation.
5. Keep existing `pkg/schema` field names stable for the first public release.
6. Add Go package documentation and examples before declaring the API stable.
7. Tag the first public API version only after the exported surface has been
   reviewed; avoid promising compatibility for internal adapter packages.

If the module is published before the API is stable, use an explicit
pre-v1 contract and document that breaking changes may occur before `v1.0.0`.

## Testing strategy

Add public-package tests for:

- live extraction selection with a mocked or local adapter seam;
- PostgreSQL, SQLite, MySQL/MariaDB, DuckDB, and CockroachDB dump selection;
- exactly-one `DB`/`Dump` validation;
- unsupported dialect and format errors;
- unreadable dump paths;
- cancellation and timeout propagation;
- all example and exclusion options;
- deterministic ordered dump examples;
- absence of credentials and dump contents in returned errors;
- TOON encoding through the public facade.

Add compile-only examples that import the package from an external-style
package, so accidental use of `internal/` does not go unnoticed.

Run the public API tests with:

```text
go test ./...
go vet ./...
CGO_ENABLED=0 go test ./...
```

## Documentation and release work

Update:

- `README.md` with library installation and usage;
- Go package comments for every exported identifier;
- examples under `example_test.go`;
- supported live and dump capability documentation;
- release notes describing the initial public API;
- MCP and CLI docs only where the shared behavior changes.

The release workflow should continue building all binaries and publishing the
MCP image. Library API versioning should follow Go module conventions; do not
tie the library API version to the MCP registry metadata unless that is an
intentional product decision.

## Implementation phases

1. Introduce public request/options and error types.
2. Add the public extraction facade and adapter selection.
3. Add public TOON convenience encoding, if desired, without duplicating the
   encoder.
4. Refactor CLI and MCP service calls to use the facade.
5. Add external-style examples, API contract tests, and Go documentation.
6. Review exported names and compatibility guarantees before the first stable
   release.
