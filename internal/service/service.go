// Package service contains the database extraction orchestration shared by
// local commands and LLM tool adapters.
package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	db2toon "github.com/kamil5b/db2toon"
	"github.com/kamil5b/db2toon/pkg/toon"
)

const DefaultTimeout = 30 * time.Second
const MaxOutputBytes = 4 << 20

type Request struct {
	Dialect string  `json:"dialect"`
	DB      string  `json:"db"`
	Dump    string  `json:"dump,omitempty"`
	Options Options `json:"options"`
}

type Options struct {
	Schemas              []string `json:"schemas,omitempty"`
	IncludeViews         bool     `json:"include_views,omitempty"`
	IncludePartitioned   bool     `json:"include_partitioned,omitempty"`
	ExampleSample        int      `json:"example_sample,omitempty"`
	ExampleSampleOrdered bool     `json:"example_sample_ordered,omitempty"`
	ExcludeTables        []string `json:"exclude_tables,omitempty"`
	ExcludeExampleTables []string `json:"exclude_example_tables,omitempty"`
	ExcludeExampleFields []string `json:"exclude_example_fields,omitempty"`
	Seed                 int64    `json:"seed,omitempty"`
	Timeout              string   `json:"timeout,omitempty"`
	MaxOutputBytes       int      `json:"max_output_bytes,omitempty"`
}

type Capabilities struct {
	Dialect string   `json:"dialect"`
	Options []string `json:"options"`
}

func CapabilitiesFor(dialect string) (Capabilities, bool) {
	switch strings.ToLower(dialect) {
	case "postgres":
		return Capabilities{Dialect: "postgres", Options: []string{
			"schemas", "include_views", "include_partitioned", "example_sample",
			"example_sample_ordered", "exclude_tables", "exclude_example_tables",
			"exclude_example_fields", "seed",
		}}, true
	case "sqlite":
		return Capabilities{Dialect: "sqlite", Options: []string{"schemas", "include_views", "example_sample", "exclude_tables", "exclude_example_tables", "exclude_example_fields"}}, true
	case "duckdb":
		return Capabilities{Dialect: "duckdb", Options: []string{"schemas", "include_views", "example_sample", "exclude_tables", "exclude_example_tables", "exclude_example_fields"}}, true
	case "mysql", "mariadb":
		return Capabilities{Dialect: "mysql", Options: []string{"schemas", "include_views", "example_sample", "exclude_tables", "exclude_example_tables", "exclude_example_fields"}}, true
	case "cockroachdb":
		return Capabilities{Dialect: "cockroachdb", Options: []string{
			"schemas", "include_views", "include_partitioned", "example_sample",
			"example_sample_ordered", "exclude_tables", "exclude_example_tables",
			"exclude_example_fields", "seed",
		}}, true
	case "mssql", "sqlserver":
		return Capabilities{Dialect: "mssql", Options: []string{
			"schemas", "include_views", "example_sample", "exclude_tables",
			"exclude_example_tables", "exclude_example_fields",
		}}, true
	case "oracle":
		return Capabilities{Dialect: "oracle", Options: []string{"schemas", "include_views", "example_sample", "exclude_tables", "exclude_example_tables", "exclude_example_fields"}}, true
	default:
		return Capabilities{}, false
	}
}

type Error struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

func (e *Error) Error() string { return e.Message }

func Extract(ctx context.Context, req Request) (string, *Error) {
	dialect := strings.ToLower(strings.TrimSpace(req.Dialect))
	if dialect == "" {
		return "", &Error{"INVALID_ARGUMENT", "dialect is required", false}
	}
	if (req.DB == "") == (req.Dump == "") {
		return "", &Error{"INVALID_ARGUMENT", "exactly one of db and dump is required", false}
	}
	if _, ok := CapabilitiesFor(dialect); !ok {
		return "", &Error{"UNSUPPORTED_DIALECT", fmt.Sprintf("unsupported database dialect %q", dialect), false}
	}
	if req.Options.ExampleSample < 0 {
		return "", &Error{"INVALID_ARGUMENT", "example_sample must not be negative", false}
	}
	timeout := DefaultTimeout
	if req.Options.Timeout != "" {
		parsed, err := time.ParseDuration(req.Options.Timeout)
		if err != nil || parsed <= 0 {
			return "", &Error{"INVALID_ARGUMENT", "timeout must be a positive duration", false}
		}
		timeout = parsed
	}
	limit := MaxOutputBytes
	if req.Options.MaxOutputBytes != 0 {
		if req.Options.MaxOutputBytes < 1024 || req.Options.MaxOutputBytes > MaxOutputBytes {
			return "", &Error{"INVALID_ARGUMENT", fmt.Sprintf("max_output_bytes must be between 1024 and %d", MaxOutputBytes), false}
		}
		limit = req.Options.MaxOutputBytes
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	db, err := db2toon.Extract(ctx, db2toon.Request{Dialect: dialect, DB: req.DB, Dump: req.Dump, Options: db2toon.Options{
		Schemas: req.Options.Schemas, IncludeViews: req.Options.IncludeViews, IncludePartitioned: req.Options.IncludePartitioned,
		ExampleSample: req.Options.ExampleSample, ExampleSampleOrdered: req.Options.ExampleSampleOrdered,
		ExcludeTables: req.Options.ExcludeTables, ExcludeExampleTables: req.Options.ExcludeExampleTables,
		ExcludeExampleFields: req.Options.ExcludeExampleFields, Seed: req.Options.Seed,
	}})
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", &Error{"TIMEOUT", "schema extraction timed out", true}
		}
		return "", &Error{"EXTRACTION_FAILED", "schema extraction failed", true}
	}
	var output bytes.Buffer
	if err := toon.Encode(&output, db); err != nil {
		return "", &Error{"INTERNAL_ERROR", "unable to encode schema", false}
	}
	if output.Len() > limit {
		return "", &Error{"OUTPUT_TOO_LARGE", fmt.Sprintf("TOON output exceeds the %d-byte limit", limit), false}
	}
	return output.String(), nil
}
