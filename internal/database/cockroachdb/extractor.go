package cockroachdb

import (
	"context"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/internal/database/postgres"
	"github.com/kamil5b/db2toon/pkg/schema"
)

// Extractor uses CockroachDB's PostgreSQL wire protocol and compatible catalogs.
// CockroachDB-specific distributed metadata is intentionally outside the
// canonical relational model for this adapter.
type Extractor struct{ *postgres.Extractor }

func New(ctx context.Context, dsn string) (*Extractor, error) {
	e, err := postgres.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	return &Extractor{e}, nil
}
func (e *Extractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	return e.Extractor.Extract(ctx, opts)
}
