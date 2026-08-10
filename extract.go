package db2toon

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/internal/database/cockroachdb"
	"github.com/kamil5b/db2toon/internal/database/duckdb"
	"github.com/kamil5b/db2toon/internal/database/mssql"
	"github.com/kamil5b/db2toon/internal/database/mysql"
	"github.com/kamil5b/db2toon/internal/database/oracle"
	"github.com/kamil5b/db2toon/internal/database/postgres"
	"github.com/kamil5b/db2toon/internal/database/sqlite"
	"github.com/kamil5b/db2toon/pkg/schema"
	"github.com/kamil5b/db2toon/pkg/toon"
)

// Request describes one schema extraction source. Exactly one of DB and Dump
// must be set.
type Request struct {
	Dialect string
	DB      string
	Dump    string
	Options Options
}

// Options controls schema selection and bounded example extraction.
type Options struct {
	Schemas              []string
	IncludeViews         bool
	IncludePartitioned   bool
	ExampleSample        int
	ExampleSampleOrdered bool
	ExcludeTables        []string
	ExcludeExampleTables []string
	ExcludeExampleFields []string
	Seed                 int64
}

// Extract reads a live database or parses a supported SQL dump into the
// canonical schema model. It does not encode or write output.
func Extract(ctx context.Context, req Request) (*schema.Database, error) {
	dialect := strings.ToLower(strings.TrimSpace(req.Dialect))
	if dialect == "" {
		return nil, &Error{Operation: "validate request", Err: errDialectRequired}
	}
	if (req.DB == "") == (req.Dump == "") {
		return nil, &Error{Operation: "validate request", Dialect: dialect, Err: errExactlyOneSource}
	}
	if req.Options.ExampleSample < 0 {
		return nil, &Error{Operation: "validate request", Dialect: dialect, Err: errNegativeSample}
	}
	if err := ctx.Err(); err != nil {
		return nil, &Error{Operation: "extract", Dialect: dialect, Err: err}
	}

	extractor, err := newExtractor(ctx, dialect, req)
	if err != nil {
		return nil, &Error{Operation: "open source", Dialect: dialect, Source: sourceKind(req), Err: err}
	}
	defer extractor.Close(context.Background())
	db, err := extractor.Extract(ctx, database.ExtractOptions{
		Schemas: req.Options.Schemas, IncludeViews: req.Options.IncludeViews, IncludePartitioned: req.Options.IncludePartitioned,
		ExampleSample: req.Options.ExampleSample, ExampleSampleOrdered: req.Options.ExampleSampleOrdered,
		ExcludeTables: req.Options.ExcludeTables, ExcludeExampleTables: req.Options.ExcludeExampleTables,
		ExcludeExampleFields: req.Options.ExcludeExampleFields, Seed: req.Options.Seed,
	})
	if err != nil {
		return nil, &Error{Operation: "extract schema", Dialect: dialect, Source: sourceKind(req), Err: err}
	}
	return db, nil
}

// Encode writes a canonical schema as TOON.
func Encode(w io.Writer, db *schema.Database) error { return toon.Encode(w, db) }

func newExtractor(ctx context.Context, dialect string, req Request) (database.Extractor, error) {
	if req.Dump != "" {
		switch dialect {
		case "postgres":
			return postgres.NewFromDump(ctx, req.Dump)
		case "sqlite":
			return sqlite.NewFromDump(ctx, req.Dump)
		case "duckdb":
			return duckdb.NewFromDump(ctx, req.Dump)
		case "mysql", "mariadb":
			return mysql.NewFromDump(ctx, req.Dump)
		case "cockroachdb":
			return cockroachdb.NewFromDump(ctx, req.Dump)
		default:
			return nil, fmt.Errorf("unsupported database dialect %q", dialect)
		}
	}
	switch dialect {
	case "postgres":
		return postgres.New(ctx, req.DB)
	case "sqlite":
		return sqlite.New(ctx, req.DB)
	case "duckdb":
		return duckdb.New(ctx, req.DB)
	case "mysql", "mariadb":
		return mysql.New(ctx, req.DB)
	case "cockroachdb":
		return cockroachdb.New(ctx, req.DB)
	case "mssql", "sqlserver":
		return mssql.New(ctx, req.DB)
	case "oracle":
		return oracle.New(ctx, req.DB)
	default:
		return nil, fmt.Errorf("unsupported database dialect %q", dialect)
	}
}

func sourceKind(req Request) string {
	if req.Dump != "" {
		return "dump"
	}
	return "database"
}
