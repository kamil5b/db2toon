package postgres

import (
	"context"
	"fmt"
	"os"

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/pkg/schema"
)

// DumpExtractor extracts metadata and bounded examples from a plain-text
// PostgreSQL dump. It never executes dump contents.
type DumpExtractor struct{ path string }

func NewFromDump(ctx context.Context, path string) (database.Extractor, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if info, err := os.Stat(path); err != nil || info.IsDir() {
		if err != nil {
			return nil, fmt.Errorf("open PostgreSQL dump: %w", err)
		}
		return nil, fmt.Errorf("open PostgreSQL dump: path is a directory")
	}
	return &DumpExtractor{path: path}, nil
}

func (e *DumpExtractor) Close(context.Context) error { return nil }

func (e *DumpExtractor) Extract(ctx context.Context, opts database.ExtractOptions) (*schema.Database, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	contents, err := os.ReadFile(e.path)
	if err != nil {
		return nil, fmt.Errorf("read PostgreSQL dump: %w", err)
	}
	parsed, err := parseDump(string(contents))
	if err != nil {
		return nil, err
	}
	return parsed.apply(ctx, opts)
}
