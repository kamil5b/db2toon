# db2toon

A CLI tool that converts database schemas into the Toon schema definition format.

## Overview

`db2toon` connects to a database and extracts schema information (tables, columns, types, constraints, indexes, routines, triggers, and examples), then converts it into the human-readable Toon format for database design documentation and visualization. PostgreSQL, SQLite, DuckDB, MySQL/MariaDB, CockroachDB, Microsoft SQL Server, and Oracle are supported. `pg2toon` remains a PostgreSQL compatibility command.

## Features

- **Schema Extraction**: Automatically extracts tables, columns, and metadata from PostgreSQL, SQLite, DuckDB, MySQL/MariaDB, CockroachDB, and Microsoft SQL Server
- **Type Normalization**: Simplifies PostgreSQL types (e.g., `character varying` → `varchar`)
- **Relationship Mapping**: Converts foreign key constraints to inline references or multi-column references
- **Comment Preservation**: Includes comments where the database exposes them; SQLite does not have catalog comments
- **Index Documentation**: Extracts and documents database indexes
- **Database Objects**: Preserves supported enums, triggers, routines, and
  installed extensions in explicit TOON sections
- **Cross-Platform**: Builds without CGO for Linux, macOS, and Windows (amd64 and arm64)
- **DBML Adapter**: Converts DBML files (or standard input) into the same TOON format

## Installation

### From Source

```bash
git clone https://github.com/kamil5b/db2toon.git
cd db2toon
CGO_ENABLED=0 go build -o output/db2toon ./cmd/db2toon
CGO_ENABLED=0 go build -o output/pg2toon ./cmd/pg2toon
CGO_ENABLED=0 go build -o output/dbml2toon ./cmd/dbml2toon
```

### From Releases

Download pre-built binaries from the [releases page](https://github.com/kamil5b/db2toon/releases) for your platform.

### Go package

The module also exposes a public Go API for callers that want the canonical
schema model instead of invoking a command:

```go
import (
    "context"
    "os"

    "github.com/kamil5b/db2toon"
)

func extract() error {
    db, err := db2toon.Extract(context.Background(), db2toon.Request{
        Dialect: "postgres",
        Dump:    "./schema.sql",
        Options: db2toon.Options{ExampleSample: 2},
    })
    if err != nil {
        return err
    }
    return db2toon.Encode(os.Stdout, db)
}
```

Set exactly one of `Request.DB` or `Request.Dump`. Dump contents are parsed
offline and never executed. The public API returns `*schema.Database`, so
callers may inspect or transform the model before encoding it.

## Usage

### LLM tool integration

Build and run the MCP-compatible stdio server:

```bash
CGO_ENABLED=0 go build -o output/db2toon-mcp ./cmd/db2toon-mcp
./output/db2toon-mcp
```

The server exposes `db2toon.extract_schema`. Its required argument is
`dialect` (`postgres`, `sqlite`, `duckdb`, `mysql`, `mariadb`, `cockroachdb`, `mssql`, `sqlserver`, or `oracle`).
Provide exactly one of `db` or `dump`; optional extraction settings are
supplied in an `options` object. Dump files are parsed offline and never
executed. The tool is read-only, uses a 30-second default timeout, and limits
responses to 4 MiB. Set `options.timeout` and `options.max_output_bytes` to
lower limits when needed. Connection strings are never included in tool errors
or results.

### Basic Usage

```bash
./db2toon postgres -db "postgresql://user:password@localhost/dbname"

# SQLite database file
./db2toon sqlite -db ./schema.db

# Plain-text SQL dump (PostgreSQL, SQLite, DuckDB, MySQL/MariaDB, and CockroachDB)
./db2toon postgres -dump ./schema.sql
./db2toon sqlite -dump ./schema.sql
./db2toon mysql -dump ./schema.sql

# DuckDB database file (requires libduckdb at runtime)
./db2toon duckdb -db ./analytics.duckdb

# Microsoft SQL Server (defaults to dbo)
./db2toon mssql -db 'sqlserver://sa:password@localhost:1433?database=app&encrypt=disable'

# Oracle (defaults to the current schema)
./db2toon oracle -db 'oracle://app:password@localhost:1521/FREEPDB1'

# Compatibility command; PostgreSQL is selected automatically.
./pg2toon -db "postgresql://user:password@localhost/dbname"

# Convert DBML, either from a file or standard input.
./dbml2toon schema.dbml
cat schema.dbml | ./dbml2toon -out schema.toon
```

### Save to File

```bash
./db2toon postgres -db "postgresql://user:password@localhost/dbname" -out schema.toon
```

Include up to two sample rows per PostgreSQL table in the TOON output, using a
stable ordering and a reproducible sample seed:

```bash
./db2toon postgres -db "postgresql://user:password@localhost/dbname" \
  -example-sample=2 -example-sample-ordered=true -seed=42
```

The default `-example-sample=0` omits `@example` sections.

SQLite and DuckDB also support `-example-sample`, but currently use a simple
`LIMIT` query. `-example-sample-ordered` and `-seed` are currently effective
only for PostgreSQL.

Select multiple schemas, include partitioned tables, and change the default
30-second operation timeout with:

```bash
./db2toon postgres -db "$DATABASE_URL" -schema audit
./db2toon postgres -db "$DATABASE_URL" -schemas public,audit -include-partitioned -timeout 1m

# SQLite and DuckDB default to the `main` schema. SQL Server defaults to `dbo`.
./db2toon sqlite -db ./schema.db -schema main
./db2toon duckdb -db ./analytics.duckdb -schema analytics
```

### Flags

- `-db string`: Database connection URL or local database path; mutually exclusive with `-dump`
- `-dump string`: Plain-text SQL dump path; mutually exclusive with `-db`
- `dialect`: `postgres`, `sqlite`, `duckdb`, `mysql`, `mariadb`, `cockroachdb`, `mssql`, or `sqlserver` for `db2toon`; `pg2toon` always uses PostgreSQL
- `-out string`: Output file path (optional, defaults to stdout)
- `-schema string`: A single schema to extract (defaults to `public` for PostgreSQL, `main` for SQLite/DuckDB, and `dbo` for SQL Server)
- `-schemas string`: Comma-separated schemas to extract; cannot be combined with `-schema`
- `-include-partitioned`: Include PostgreSQL partitioned tables
- `-include-views`: Include supported views
- `-exclude-tables string`: Comma-separated tables to exclude entirely; accepts `table` or `schema.table`
- `-exclude-example-tables string`: Comma-separated tables to exclude from `@example` sampling
- `-exclude-example-fields string`: Comma-separated qualified fields to exclude from examples, such as `public.users.password_hash`
- `-example-sample int`: Number of sample rows to include per table (defaults to `0`)
- `-example-sample-ordered`: Select sample rows using deterministic ordering for PostgreSQL (defaults to `false`)
- `-seed int`: Seed for reproducible PostgreSQL sample selection (defaults to `0`; currently ignored by SQLite/DuckDB)
- `-timeout duration`: Connection and extraction timeout (defaults to `30s`)

Dump mode currently supports plain-text SQL exports for PostgreSQL, SQLite,
DuckDB, MySQL/MariaDB, and CockroachDB. SQL Server currently supports live
connections only. Common tables, columns, constraints,
indexes, comments, and bounded `INSERT` examples are parsed without executing
the dump. Triggers and other metadata without a canonical TOON representation
are ignored.

## Output Format

The Toon format provides a clean, human-readable schema definition:

```
[users]
# User accounts table

  id int {pk}
  email varchar {req}
  name varchar
  created_at timestamptz {req}

@indices
  idx_email: ON users USING btree (email)

@example[2]{id,email,name,created_at}:
  1,alice@example.com,Alice,2026-01-10T09:00:00Z
  2,bob@example.com,Bob,2026-01-11T10:30:00Z

[posts]
# Blog posts

  id int {pk}
  user_id int {req} -> users(id)
  title varchar {req}
  content text
  published_at timestamptz

[comments]
# Post comments

  id int {pk}
  post_id int {req} -> posts(id)
  user_id int {req} -> users(id)
  content text {req}
  created_at timestamptz {req}
```

### Format Elements

- `[TableName]`: Table definition
- `# comment`: Table or column comments
- `name type {tags}`: Column definition with optional tags
  - `{pk}`: Primary key
  - `{req}`: Required (NOT NULL)
  - Multiple tags: `{pk,req}`
- `-> table(column)`: Foreign key reference (inline for single columns)
- `@indices`: Section for database indexes
- `@example[n]{columns}:`: Up to `n` sampled rows from the table
- `// comment`: Inline column comment

## Requirements

- Go 1.26.0 or later
- PostgreSQL 9.4+ (for JSON aggregation functions), SQLite, DuckDB, MySQL/MariaDB, CockroachDB, or Microsoft SQL Server 2022+
- A valid database connection string or local database path
- DuckDB also requires a compatible `libduckdb` shared library at runtime

## License

MIT
