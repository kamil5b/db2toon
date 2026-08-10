# db2toon development plan

## Goal

Evolve `db2toon` from a PostgreSQL-specific proof of concept into a database-neutral schema extraction and TOON rendering tool while preserving the current `pg2toon` CLI behavior.

## Target architecture

```text
Database-specific catalogs
          |
          v
Database extractor adapter
          |
          v
Canonical schema model
          |
          v
Database-independent TOON encoder
```

Suggested packages:

```text
cmd/db2toon/
    main.go
cmd/pg2toon/
    main.go              # compatibility wrapper

internal/database/
    extractor.go
    postgres/
        extractor.go
        queries.go
    mysql/
    sqlite/
    mongodb/
    clickhouse/
    cockroachdb/
    neo4j/
    redis/
    cassandra/
    duckdb/
    timescaledb/
    elasticsearch/
    opensearch/
    dynamodb/
    snowflake/
    bigquery/
    redshift/
    influxdb/
    tidb/
    yugabytedb/
    firebird/
    db2/

pkg/schema/
    model.go

pkg/toon/
    encoder.go
    encoder_test.go
```

## Canonical schema-model expansion

Add first-class model and TOON support for metadata currently discarded by the
relational adapters:

1. **Enums** — schema-qualified enum types and their ordered values, including
   columns that reference them.
2. **Triggers** — trigger name, timing, events, target table, enabled state,
   and definition where the database exposes it.
3. **Functions and procedures** — schema-qualified routine name, kind,
   arguments, return type, language, and definition where the database exposes
   it.

Each relational adapter should declare support for these capabilities and add
representative conformance cases. The TOON encoder should retain these objects
in explicit sections rather than flattening them into table comments or native
type strings.

## Phase 1: Additional databases

Database support is divided into priority tiers. Each adapter must pass a
shared conformance suite and publish its capabilities.

Testing tags:

- `[Testable: Testcontainers]` — run integration tests against a Docker container in CI.
- `[Testable: direct]` — test using a temporary local database or embedded engine.
- `[Testable: emulator+cloud]` — use a local emulator for normal CI and a real provider contract test separately.
- `[Testable: cloud]` — use mocks plus opt-in authenticated provider tests; no complete local substitute is assumed.

### Priority 1: Testcontainers adapters

These should follow the direct adapters and run against real database services
in ordinary Docker-based CI.

1. **TimescaleDB** `[Testable: Testcontainers]` — cover hypertables, continuous aggregates, compression, and retention policies.
2. **ClickHouse** `[Testable: Testcontainers]` — databases, tables, engines, columns, sorting keys, and materialized views.
3. **MongoDB** `[Testable: Testcontainers]` — collections, indexes, validators, and sampled document shapes.
4. **Neo4j** `[Testable: Testcontainers]` — labels, relationship types, property keys, and constraints.
5. **Cassandra** `[Testable: Testcontainers]` — keyspaces, tables, partition keys, clustering keys, and indexes.
6. **Elasticsearch/OpenSearch** `[Testable: Testcontainers]` — indices, mappings, aliases, templates, and data streams.
7. **InfluxDB** `[Testable: Testcontainers]` — organizations, buckets, measurements, tags, fields, and retention policies.
8. **Redis** `[Testable: Testcontainers]` — configured data structures, key patterns, TTLs, and sampled key metadata where discovery is available.
9. **TiDB/YugabyteDB** `[Testable: Testcontainers]` — reuse MySQL/PostgreSQL compatibility where safe and document distributed features.
10. **SQL Server / MSSQL** `[Testable: Testcontainers]` — use `sys.*` catalogs
    and extended properties; cover schemas, tables, views, columns, keys,
    indexes, constraints, triggers, functions, procedures, and user-defined
    types where representable.
11. **Firebird** `[Testable: Testcontainers]` — cover relational catalogs and vendor-specific metadata.
12. **Oracle** `[Testable: Testcontainers]` — implement if required by users,
    subject to image licensing and CI constraints; cover schemas, tables,
    views, constraints, indexes, triggers, functions, procedures, and Oracle
    enum-like domain/type constructs where representable.
13. **IBM Db2** `[Testable: Testcontainers]` — cover relational catalogs and vendor-specific metadata, subject to image licensing and CI constraints.

### Priority 2: The rest

1. **DynamoDB** `[Testable: emulator+cloud]` — tables, partition/sort keys, secondary indexes, streams, and inferred item attributes.
2. **BigQuery** `[Testable: emulator+cloud]` — projects, datasets, tables,
    views, nested fields, partitions, clustering, routines, and table
    functions. BigQuery has no native triggers or enum type.
3. **Snowflake** `[Testable: cloud]` — databases, schemas, tables, views,
    stages, warehouse metadata, user-defined functions/procedures, and
    streams/tasks where representable. Snowflake has no native triggers or
    enum type.
4. **Redshift** `[Testable: cloud]` — schemas, tables, distribution keys, sort keys, and encodings.

Cross-database considerations:

- Keep the canonical model extensible without forcing non-relational systems into tables and foreign keys.
- Define capability flags for schemas, comments, constraints, indexes, relationships, sampling, and transactions.
- Extend capability flags and the shared conformance suite for enums, triggers,
  functions, and procedures. Distinguish native support from vendor-specific
  approximations, such as Oracle domain/type constructs.
- Use read-only discovery queries and configurable sampling limits where full schema inference scans data.
- Add per-adapter connection options without leaking vendor-specific flags into unrelated adapters.
- Require every adapter, driver, CLI binary, and test suite to build and run with `CGO_ENABLED=0`.
- Prefer pure-Go drivers; reject or isolate adapters that require native libraries instead of weakening the common build contract.
- Enforce `CGO_ENABLED=0` in local Make targets, pull-request CI, integration CI, and release builds.
- Cloud and search adapters must support explicit credential/configuration boundaries.
- Search and key/value adapters should distinguish declared metadata from fields inferred by sampling.
- Include representative test containers or managed test fixtures where practical.

## Pull request strategy

1. **Priority 1 Testcontainers adapters.**
2. **Priority 2 remaining adapters.**

Keeping these changes separated makes review easier and prevents the abstraction work from hiding PostgreSQL correctness regressions.
