# db2toon

A CLI tool that converts database schemas into the Toon schema definition format.

## Overview

`db2toon` connects to a database and extracts schema information (tables, columns, types, constraints, indexes, and examples), then converts it into the human-readable Toon format for database design documentation and visualization. PostgreSQL, SQLite, and DuckDB are supported. `pg2toon` remains a PostgreSQL compatibility command.

## Features

- **Schema Extraction**: Automatically extracts tables, columns, and metadata from PostgreSQL, SQLite, and DuckDB
- **Type Normalization**: Simplifies PostgreSQL types (e.g., `character varying` → `varchar`)
- **Relationship Mapping**: Converts foreign key constraints to inline references or multi-column references
- **Comment Preservation**: Includes comments where the database exposes them; SQLite does not have catalog comments
- **Index Documentation**: Extracts and documents database indexes
- **Cross-Platform**: Builds without CGO for Linux, macOS, and Windows (amd64 and arm64)

## Installation

### From Source

```bash
git clone https://github.com/kamil5b/db2toon.git
cd db2toon
CGO_ENABLED=0 go build -o output/db2toon ./cmd/db2toon
CGO_ENABLED=0 go build -o output/pg2toon ./cmd/pg2toon
```

### From Releases

Download pre-built binaries from the [releases page](https://github.com/kamil5b/db2toon/releases) for your platform.

## Usage

### LLM tool integration

Build and run the MCP-compatible stdio server:

```bash
CGO_ENABLED=0 go build -o output/db2toon-mcp ./cmd/db2toon-mcp
./output/db2toon-mcp
```

The server exposes `db2toon.extract_schema`. Its required arguments are
`dialect` (`postgres`, `sqlite`, or `duckdb`) and `db`; optional extraction settings are supplied in
an `options` object. The tool is read-only, uses a 30-second default timeout,
and limits responses to 4 MiB. Set `options.timeout` and
`options.max_output_bytes` to lower limits when needed. Connection strings are
never included in tool errors or results.

### Basic Usage

```bash
./db2toon postgres -db "postgresql://user:password@localhost/dbname"

# SQLite database file
./db2toon sqlite -db ./schema.db

# DuckDB database file (requires libduckdb at runtime)
./db2toon duckdb -db ./analytics.duckdb

# Compatibility command; PostgreSQL is selected automatically.
./pg2toon -db "postgresql://user:password@localhost/dbname"
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

# SQLite and DuckDB default to the `main` schema.
./db2toon sqlite -db ./schema.db -schema main
./db2toon duckdb -db ./analytics.duckdb -schema analytics
```

### Flags

- `-db string`: Database connection URL or local database path (required)
- `dialect`: `postgres`, `sqlite`, or `duckdb` for `db2toon`; `pg2toon` always uses PostgreSQL
- `-out string`: Output file path (optional, defaults to stdout)
- `-schema string`: A single schema to extract (defaults to `public` for PostgreSQL and `main` for SQLite/DuckDB)
- `-schemas string`: Comma-separated schemas to extract; cannot be combined with `-schema`
- `-include-partitioned`: Include PostgreSQL partitioned tables
- `-exclude-tables string`: Comma-separated tables to exclude entirely; accepts `table` or `schema.table`
- `-exclude-example-tables string`: Comma-separated tables to exclude from `@example` sampling
- `-exclude-example-fields string`: Comma-separated qualified fields to exclude from examples, such as `public.users.password_hash`
- `-example-sample int`: Number of sample rows to include per table (defaults to `0`)
- `-example-sample-ordered`: Select sample rows using deterministic ordering for PostgreSQL (defaults to `false`)
- `-seed int`: Seed for reproducible PostgreSQL sample selection (defaults to `0`; currently ignored by SQLite/DuckDB)
- `-timeout duration`: Connection and extraction timeout (defaults to `30s`)

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
- PostgreSQL 9.4+ (for JSON aggregation functions), SQLite, or DuckDB
- A valid database connection string or local database path
- DuckDB also requires a compatible `libduckdb` shared library at runtime

## License

MIT
