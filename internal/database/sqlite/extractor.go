package sqlite

import (
	"context"
	"database/sql"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/internal/database/sqlutil"
	"github.com/kamil5b/db2toon/pkg/schema"
	_ "modernc.org/sqlite"
)

type Extractor struct{ *sqlutil.Extractor }

func New(ctx context.Context, dsn string) (*Extractor, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Extractor{sqlutil.New(db, "sqlite")}, nil
}

func NewFromDump(ctx context.Context, path string) (database.Extractor, error) {
	return sqlutil.NewDumpExtractor(ctx, path, "sqlite", "main")
}
func (e *Extractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	return e.Extractor.Extract(ctx, opts)
}
