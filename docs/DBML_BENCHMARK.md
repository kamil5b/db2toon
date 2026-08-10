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
npm install
make benchmark-dbml
```

Alternatively, run the **DBML benchmark** workflow from the GitHub Actions
page. The manual workflow installs `@dbml/cli`, verifies that `db2dbml` is
available, runs the Testcontainers benchmark, and uploads the complete output
as an artifact. Keeping it manual avoids adding a container-backed performance
test to every pull request.

The result reports elapsed time and three token metrics for each output:

- `local_tokens` uses the repository's deterministic lexical counter.
- `openai_tokens` uses the offline `cl100k_base` encoding from `tiktoken`.
- `anthropic_tokens` uses the offline `@anthropic-ai/tokenizer` package.

None of these counters makes an API request or needs an API key. Token counts
depend on a model's vocabulary, so the OpenAI and Anthropic values identify the
local tokenizer used rather than claiming to represent every model from those
providers. The verbose output also reports all nine token totals and a
line-oriented diff between direct `db2toon` output and the `db2dbml` ->
`dbml2toon` output. Records are present to make the container realistic, but
the comparison uses schema-only output because `db2dbml` does not export table
records.

## Example result

One single-operation run of the benchmark produced:

| Path | Time | Local tokens | OpenAI tokens | Anthropic tokens |
| --- | ---: | ---: | ---: | ---: |
| PostgreSQL -> `db2toon` | 106.35 ms | 614 | 748 | 842 |
| PostgreSQL -> `db2dbml` | 1.49 s | 898 | 1,029 | 1,152 |
| `dbml2toon` | 218.40 µs | 481 | 640 | 720 |

Compared with direct `db2toon`, `db2dbml` used 46.3% more local tokens, 37.6%
more OpenAI tokens, and 36.8% more Anthropic tokens. The `dbml2toon` output
used 21.7%, 14.4%, and 14.5% fewer tokens respectively. These percentages use
the direct `db2toon` count as the baseline.

The direct and round-trip TOON outputs differed in 68 of 75 compared line
positions (90.7%). The round trip changed table ordering, native type
spellings, identity metadata, constraint/index representation, and the
ordering of columns in some composite foreign keys. Treat these values as an
illustrative local run rather than a performance baseline: container startup,
hardware, installed DBML CLI version, and benchmark iteration count affect the
result.
