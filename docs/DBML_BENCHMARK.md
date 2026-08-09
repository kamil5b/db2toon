# DBML conversion benchmark

The integration benchmark creates a PostgreSQL 16 testcontainer containing a
schema with one-to-many and composite relationships, primary and unique keys,
partial, expression, multicolumn, and GIN indexes, comments, checks, JSON data,
defaults, and records. It compares these paths:

1. PostgreSQL -> `db2toon`
2. PostgreSQL -> `db2dbml`
3. PostgreSQL -> `db2dbml` -> `dbml2toon`

Install the official DBML CLI and run the benchmark once per operation. This
uses the PostgreSQL command documented in the
[db2dbml guide](https://docs.dbdocs.io/features/generate-dbml-from-db/):

```bash
npm install -g @dbml/cli
make benchmark-dbml
```

Alternatively, run the **DBML benchmark** workflow from the GitHub Actions
page. The manual workflow installs `@dbml/cli`, verifies that `db2dbml` is
available, runs the Testcontainers benchmark, and uploads the complete output
as an artifact. Keeping it manual avoids adding a container-backed performance
test to every pull request.

The result reports elapsed time and a `tokens` metric for each output. The
portable token counter treats words, numbers, and punctuation as tokens; it is
deliberately independent of any particular LLM vocabulary. The verbose output
also reports the three token totals and a line-oriented diff between direct
`db2toon` output and the `db2dbml` -> `dbml2toon` output. Records are present to
make the container realistic, but the comparison uses schema-only output
because `db2dbml` does not export table records.
