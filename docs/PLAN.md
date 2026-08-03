# pgschema2toon development plan

## Goal

Evolve `pgschema2toon` from a PostgreSQL-specific proof of concept into a database-neutral schema extraction and TOON rendering tool while preserving the current CLI behavior.

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
cmd/pg2toon/
    main.go

internal/database/
    extractor.go
    factory.go
    postgres/
        extractor.go
        queries.go

pkg/schema/
    model.go

pkg/toon/
    encoder.go
    encoder_test.go
```

## Phase 5: Additional databases

Recommended order:

1. SQLite, to expose assumptions around schemas, comments, and type affinity.
2. MySQL/MariaDB, using `information_schema` plus vendor-specific metadata.
3. SQL Server, using `sys.*` catalogs and extended properties.
4. Oracle, if required.

Each adapter must pass a shared conformance suite and publish its capabilities.

## Pull request strategy

1. **SQLite adapter.**
2. **MySQL adapter.**

Keeping these changes separated makes review easier and prevents the abstraction work from hiding PostgreSQL correctness regressions.
