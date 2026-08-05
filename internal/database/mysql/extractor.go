package mysql

import (
	"context"
	"database/sql"

	_ "github.com/go-sql-driver/mysql"
	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/internal/database/sqlutil"
	"github.com/kamil5b/db2toon/pkg/schema"
)

// Extractor reads MySQL and MariaDB metadata through information_schema.
type Extractor struct{ *sqlutil.Extractor }

func New(ctx context.Context, dsn string) (*Extractor, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Extractor{sqlutil.New(db, "mysql")}, nil
}

func NewFromDump(ctx context.Context, path string) (database.Extractor, error) {
	return sqlutil.NewDumpExtractor(ctx, path, "mysql", "")
}

func (e *Extractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	return e.Extractor.Extract(ctx, opts)
}
