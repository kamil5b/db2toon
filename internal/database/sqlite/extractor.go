package sqlite

import (
	"context"
	"database/sql"

	"github.com/kamil5b/db2toon/constants"
	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/internal/database/sqlutil"
	"github.com/kamil5b/db2toon/pkg/schema"
	_ "modernc.org/sqlite"
)

type Extractor struct{ *sqlutil.Extractor }

func New(ctx context.Context, dsn string) (*Extractor, error) {
	db, err := sql.Open(constants.DialectSQLite, dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return &Extractor{sqlutil.New(db, constants.DialectSQLite)}, nil
}

func NewFromDump(ctx context.Context, path string) (database.Extractor, error) {
	return sqlutil.NewDumpExtractor(ctx, path, constants.DialectSQLite, constants.SchemaMain)
}
func (e *Extractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	return e.Extractor.Extract(ctx, opts)
}
