package database

import (
	"context"

	"github.com/kamil5b/pgschema2toon/pkg/schema"
)

type ExtractOptions struct {
	Schemas            []string
	IncludeViews       bool
	IncludeSystem      bool
	IncludePartitioned bool
}

type Extractor interface {
	Extract(context.Context, ExtractOptions) (*schema.Database, error)
	Close(context.Context) error
}
