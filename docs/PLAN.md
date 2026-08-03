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
    elasticsearch/
    opensearch/
    dynamodb/
    snowflake/
    bigquery/
    redshift/
    influxdb/
    timescaledb/
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

## Phase 1: Additional databases

Database support is divided into priority tiers. Each adapter must pass a
shared conformance suite and publish its capabilities.

Testing tags:

- `[Testable: Testcontainers]` — run integration tests against a Docker container in CI.
- `[Testable: direct]` — test using a temporary local database or embedded engine.
- `[Testable: emulator+cloud]` — use a local emulator for normal CI and a real provider contract test separately.
- `[Testable: cloud]` — use mocks plus opt-in authenticated provider tests; no complete local substitute is assumed.

### Priority 1: Direct local adapters

1. **SQLite** `[Testable: direct]` — expose assumptions around schemas, comments, and type affinity.
2. **DuckDB** `[Testable: direct]` — cover attached databases, schemas, tables, views, types, and analytical features.

Direct-adapter tests must be cleanupable:

- Create database files inside Go's `t.TempDir()` whenever possible.
- Register cleanup immediately after opening a database or creating a temporary resource.
- Close connections before cleanup and verify temporary files are removed when the adapter requires explicit deletion.
- Never write direct-test databases to the repository root or a tracked fixture directory.
- If a tool or driver creates local artifacts outside `t.TempDir()`, use an ignored test-artifact path and clean it in test teardown.

### Priority 2: Testcontainers adapters

These should follow the direct adapters and run against real database services
in ordinary Docker-based CI.

1. **MySQL/MariaDB** `[Testable: Testcontainers]` — use `information_schema` plus vendor-specific metadata.
2. **CockroachDB** `[Testable: Testcontainers]` — reuse PostgreSQL compatibility where safe and document differences.
3. **TimescaleDB** `[Testable: Testcontainers]` — cover hypertables, continuous aggregates, compression, and retention policies.
4. **ClickHouse** `[Testable: Testcontainers]` — databases, tables, engines, columns, sorting keys, and materialized views.
5. **MongoDB** `[Testable: Testcontainers]` — collections, indexes, validators, and sampled document shapes.
6. **Neo4j** `[Testable: Testcontainers]` — labels, relationship types, property keys, and constraints.
7. **Cassandra** `[Testable: Testcontainers]` — keyspaces, tables, partition keys, clustering keys, and indexes.
8. **Elasticsearch/OpenSearch** `[Testable: Testcontainers]` — indices, mappings, aliases, templates, and data streams.
9. **InfluxDB** `[Testable: Testcontainers]` — organizations, buckets, measurements, tags, fields, and retention policies.
10. **Redis** `[Testable: Testcontainers]` — configured data structures, key patterns, TTLs, and sampled key metadata where discovery is available.
11. **TiDB/YugabyteDB** `[Testable: Testcontainers]` — reuse MySQL/PostgreSQL compatibility where safe and document distributed features.
12. **SQL Server** `[Testable: Testcontainers]` — use `sys.*` catalogs and extended properties.
13. **Firebird** `[Testable: Testcontainers]` — cover relational catalogs and vendor-specific metadata.
14. **Oracle** `[Testable: Testcontainers]` — implement if required by users, subject to image licensing and CI constraints.
15. **IBM Db2** `[Testable: Testcontainers]` — cover relational catalogs and vendor-specific metadata, subject to image licensing and CI constraints.

### Priority 3: The rest

1. **DynamoDB** `[Testable: emulator+cloud]` — tables, partition/sort keys, secondary indexes, streams, and inferred item attributes.
2. **BigQuery** `[Testable: emulator+cloud]` — projects, datasets, tables, views, nested fields, partitions, and clustering.
3. **Snowflake** `[Testable: cloud]` — databases, schemas, tables, views, stages, and warehouse metadata.
4. **Redshift** `[Testable: cloud]` — schemas, tables, distribution keys, sort keys, and encodings.

Cross-database considerations:

- Keep the canonical model extensible without forcing non-relational systems into tables and foreign keys.
- Define capability flags for schemas, comments, constraints, indexes, relationships, sampling, and transactions.
- Use read-only discovery queries and configurable sampling limits where full schema inference scans data.
- Add per-adapter connection options without leaking vendor-specific flags into unrelated adapters.
- Require every adapter, driver, CLI binary, and test suite to build and run with `CGO_ENABLED=0`.
- Prefer pure-Go drivers; reject or isolate adapters that require native libraries instead of weakening the common build contract.
- Enforce `CGO_ENABLED=0` in local Make targets, pull-request CI, integration CI, and release builds.
- Cloud and search adapters must support explicit credential/configuration boundaries.
- Search and key/value adapters should distinguish declared metadata from fields inferred by sampling.
- Include representative test containers or managed test fixtures where practical.

## Pull request strategy

1. **Priority 1 direct adapters.**
2. **Priority 2 Testcontainers adapters.**
3. **Priority 3 remaining adapters.**

Keeping these changes separated makes review easier and prevents the abstraction work from hiding PostgreSQL correctness regressions.
