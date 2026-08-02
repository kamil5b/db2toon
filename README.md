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
- `// comment`: Inline column comment

## Requirements

- Go 1.26.0 or later
- PostgreSQL 9.4+ (for JSON aggregation functions)
- Valid PostgreSQL connection string

## License

MIT
