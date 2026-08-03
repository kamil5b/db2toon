# pgschema2toon

A CLI tool that converts PostgreSQL database schemas into the Toon schema definition format.

## Overview

`pgschema2toon` connects to a PostgreSQL database and extracts schema information (tables, columns, types, constraints, indexes, and comments), then converts it into the human-readable Toon format for database design documentation and visualization.

## Features

- **Schema Extraction**: Automatically extracts tables, columns, and metadata from PostgreSQL
- **Type Normalization**: Simplifies PostgreSQL types (e.g., `character varying` → `varchar`)
- **Relationship Mapping**: Converts foreign key constraints to inline references or multi-column references
- **Comment Preservation**: Includes table and column comments in the output
- **Index Documentation**: Extracts and documents database indexes
- **Cross-Platform**: Builds without CGO for Linux, macOS, and Windows (amd64 and arm64)

## Installation

### From Source

```bash
git clone https://github.com/kamil5b/pgschema2toon.git
cd pgschema2toon
go build -o pg2toon ./cmd/pg2toon
```

### From Releases

Download pre-built binaries from the [releases page](https://github.com/kamil5b/pgschema2toon/releases) for your platform.

## Usage

### Basic Usage

```bash
./pg2toon -db "postgresql://user:password@localhost/dbname"
```

### Save to File

```bash
./pg2toon -db "postgresql://user:password@localhost/dbname" -out schema.toon
```

Include up to two sample rows per table in the TOON output, using a stable
ordering and a reproducible sample seed:

```bash
./pg2toon -db "postgresql://user:password@localhost/dbname" \
  -example-sample=2 -example-sample-ordered=true -seed=42
```

The default `-example-sample=0` omits `@example` sections.

Select multiple schemas, include partitioned tables, and change the default
30-second operation timeout with:

```bash
./pg2toon -db "$DATABASE_URL" -schema audit
./pg2toon -db "$DATABASE_URL" -schemas public,audit -include-partitioned -timeout 1m
```

### Flags

- `-db string`: PostgreSQL connection URL (required)
- `-out string`: Output file path (optional, defaults to stdout)
- `-schema string`: A single schema to extract (defaults to `public`)
- `-schemas string`: Comma-separated schemas to extract; cannot be combined with `-schema`
- `-include-partitioned`: Include PostgreSQL partitioned tables
- `-exclude-tables string`: Comma-separated tables to exclude entirely; accepts `table` or `schema.table`
- `-exclude-example-tables string`: Comma-separated tables to exclude from `@example` sampling
- `-exclude-example-fields string`: Comma-separated qualified fields to exclude from examples, such as `public.users.password_hash`
- `-example-sample int`: Number of sample rows to include per table (defaults to `0`)
- `-example-sample-ordered`: Select sample rows using deterministic ordering (defaults to `false`)
- `-seed int`: Seed for reproducible sample selection (defaults to `0`)
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
- PostgreSQL 9.4+ (for JSON aggregation functions)
- Valid PostgreSQL connection string

## License

MIT
