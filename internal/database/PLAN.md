# Database extraction plan

This document covers extraction from live databases and offline database dumps.
Every implementation should produce the shared `pkg/schema.Database` model so
the TOON encoder and service layer remain database-independent.

## Input modes

The product should support two sources:

1. **Database URL or local database path** — the existing adapter-based flow.
2. **SQL dump** — an offline parser selected by dialect and dump format.

Example CLI usage:

```text
# Existing live database mode
db2toon postgres -db "$DATABASE_URL" -out schema.toon

# Offline dump mode
db2toon postgres -dump ./schema.sql -out schema.toon
```

The CLI must require exactly one of `-db` and `-dump`. Dump contents must never
be executed against a database.

Dump mode must retain the existing `example_sample` behavior. A full dump can
provide both schema metadata and representative rows, so the offline path must
support bounded example extraction rather than becoming schema-only.

## Shared architecture

```text
Database URL ---> database adapter --+
                                    |
SQL dump ------> dump parser -------+--> canonical schema --> TOON encoder
```

Live adapters and dump parsers should expose equivalent extraction semantics
where the source contains enough information. Source-specific capabilities must
be explicit rather than silently producing incomplete data.

### Extractor interface

Dump-backed extractors must implement the existing `database.Extractor`
interface from `internal/database/extractor.go`:

```go
type Extractor interface {
    Extract(context.Context, ExtractOptions) (*schema.Database, error)
    Close(context.Context) error
}
```

They must honor the shared `database.ExtractOptions`, including schemas,
partition/view selection, table exclusions, example sampling, example field
exclusions, and seed/ordering semantics where those options are meaningful for
an offline source. `Close` should be safe to call and should be a no-op for a
file-backed extractor unless it owns an opened file or parser resource.

The service should depend only on `database.Extractor`; it must not branch on
the concrete live or dump extractor after construction.

## Dump adapter roadmap

### PostgreSQL — first implementation

Support plain-text `pg_dump` output, including DDL mixed with data and
PostgreSQL session or ownership statements.

Parse:

- schemas;
- tables and quoted identifiers;
- columns, native types, nullability, defaults, and generated expressions;
- comments;
- primary keys;
- foreign keys;
- unique constraints;
- check constraints;
- indexes;
- representable partition metadata.

Parse `INSERT` and `COPY` data sufficiently to populate bounded examples when
`example_sample` is requested. Ignore data after the per-table sample limit is
reached. Ignore statements that do not contribute to schema or examples,
including `GRANT`, ownership, extension setup, and session settings.

Initially defer custom, tar, directory, and other binary PostgreSQL dump
formats. Schema-only dumps should still produce valid schema output, but they
must naturally contain no examples.

Follow the existing adapter-per-dialect layout rather than introducing a
generic `internal/database/dump` package:

```text
internal/database/postgres/
    extractor.go          # existing live database extractor
    dump.go               # dump-backed Extractor implementation/constructor
    dump_parser.go        # PostgreSQL dump parsing helpers
    dump_test.go
    testdata/
```

The PostgreSQL package should expose a dump constructor alongside `New`, for
example `NewFromDump(ctx, path)`, returning `database.Extractor`. The service
layer can then select `postgres.New` for `DB` input or `postgres.NewFromDump`
for `Dump` input without adding a second extraction architecture. Shared SQL
tokenization or value-conversion helpers may live in the existing
`internal/database/sqlutil` package when they are genuinely dialect-
independent.

### SQLite

Keep SQLite dump support inside `internal/database/sqlite`, alongside its
existing live extractor. Add a dump constructor and SQLite-specific parser
files there. Support SQLite SQL export files containing `CREATE TABLE`,
`CREATE INDEX`, and related schema statements. Preserve SQLite-specific type
declarations and table-level constraints. Account for SQLite's relaxed typing,
quoted names, triggers, and `sqlite_sequence` without treating internal tables
as ordinary application tables by default.

### MySQL and MariaDB

Keep MySQL/MariaDB dump support inside `internal/database/mysql`, reusing the
same adapter boundary for both live and dump input. Support plain SQL dumps
with `CREATE DATABASE`, `CREATE TABLE`, `ALTER TABLE`, indexes, comments, and
foreign keys. Handle `AUTO_INCREMENT`, backtick-quoted identifiers,
engine/charset options, versioned comments, and MariaDB/MySQL syntax
differences without executing statements.

### DuckDB

Keep DuckDB dump support inside `internal/database/duckdb`, with its own dump
constructor and parser. Support SQL exports containing schemas, tables,
columns, constraints, views, and indexes where represented by the dump format.
Preserve DuckDB native types and nested type expressions. Define behavior for
statements that are valid in DuckDB but not in the common relational model.

### CockroachDB

Keep CockroachDB dump support inside `internal/database/cockroachdb`. Reuse
PostgreSQL parsing helpers only where syntax is compatible, then add coverage
for CockroachDB-specific constraints, indexes, computed columns, and
partitioning. Keep the dialect-specific behavior separate from the PostgreSQL
adapter when the resulting schema semantics differ.

### Non-relational databases

Do not force document, graph, search, or key/value exports into SQL dump
parsers. Add format-specific importers only after defining how their metadata
maps to the canonical model and which fields are declared versus inferred from
sampled data.

## Service and CLI integration

- Extend `service.Request` with a dump source or an equivalent input-source
  abstraction.
- Select the live adapter for `DB` and the dialect-specific parser for `Dump`.
- Reuse schema selection, table exclusions, TOON encoding, and output limits.
- Validate dump file existence and readability before parsing.
- Return structured errors with dialect, format, and line/statement context.
- Apply `example_sample`, `exclude_example_tables`, and
  `exclude_example_fields` to rows parsed from the dump.
- Define deterministic behavior for `example_sample_ordered` and `seed`; dump
  input should not use random database queries.
- Preserve `pg2toon` behavior and all existing database URL behavior.

## Testing strategy

The primary dump-extraction tests should reuse the existing database integration
tests instead of maintaining a second, unrelated set of hand-written schemas.
For each supported SQL dialect where a native dump tool is available:

1. Start the existing test database/container.
2. Apply the same schema, constraints, indexes, comments, and representative
   data currently used by the live extractor tests.
3. Run the database's native dump/export command to create a plain SQL dump in
   a temporary test directory.
4. Extract the schema directly from the live database using the existing
   extractor.
5. Extract the schema from the generated dump using the new dump extractor.
6. Compare both canonical `pkg/schema.Database` values, accounting only for
   explicitly documented source limitations.
7. Encode both results to TOON and compare the output where ordering and
   unsupported metadata are equivalent.

For PostgreSQL, the initial integration flow should use a full plain-text
`pg_dump` so the generated dump contains both DDL and representative data.
Schema-only dumps should be covered as a separate compatibility case. Verify
that `example_sample` produces examples from `COPY` data, and from `INSERT`
data where that dump form is supported. The dump extractor must never execute
the generated dump.

Keep focused parser tests for syntax cases that are difficult to create through
the integration setup. These fixture-based unit tests should cover:

- basic tables and multiple schemas;
- quoted identifiers;
- nullable columns, defaults, generated columns, and native types;
- composite primary and foreign keys;
- unique and check constraints;
- indexes and comments;
- mixed schema/data dumps;
- bounded examples from `COPY` and `INSERT` data;
- dialect-specific noise and unsupported statements;
- malformed SQL, empty dumps, schema-only dumps, and data-only dumps;
- schema/table filtering and deterministic TOON output.

Add CLI and service tests for missing or conflicting input sources, unsupported
dialect/format combinations, unreadable files, source-specific options, and
regressions in the live-database path. The existing live integration tests must
remain in place; dump tests should extend them with a second extraction path,
including equivalent `example_sample` assertions, not replace direct database
coverage.

## Documentation and capability metadata

Document supported dump formats separately from live database adapters. Expose
capabilities for features such as comments, constraints, indexes, views,
partitioning, and example rows so callers can distinguish unavailable offline
features from empty metadata.

## Follow-up work

- Support additional dump data encodings and type conversions for example rows.
- Additional PostgreSQL dump formats through a dedicated reader or documented
  conversion step.
- Dump support for each remaining SQL dialect after its grammar and fixtures
  are defined.
- Shared parser conformance tests across SQL dialects.
