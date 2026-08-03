package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/kamil5b/pgschema2toon/internal/database"
	"github.com/kamil5b/pgschema2toon/internal/database/postgres"
	"github.com/kamil5b/pgschema2toon/pkg/toon"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("pg2toon", flag.ContinueOnError)
	dbURL := flags.String("db", "", "Postgres URL")
	output := flags.String("out", "", "output file (default stdout)")
	schemaName := flags.String("schema", "", "schema to extract (default public)")
	schemaNames := flags.String("schemas", "", "comma-separated schemas to extract")
	includePartitioned := flags.Bool("include-partitioned", false, "include partitioned tables")
	excludeTables := flags.String("exclude-tables", "", "comma-separated tables to exclude")
	excludeExampleTables := flags.String("exclude-example-tables", "", "comma-separated tables to exclude from examples")
	excludeExampleFields := flags.String("exclude-example-field", "", "comma-separated fields to exclude from examples")
	exampleSample := flags.Int("example-sample", 0, "number of sample rows to include per table")
	exampleSampleOrdered := flags.Bool("example-sample-ordered", false, "order sample rows deterministically")
	seed := flags.Int64("seed", 0, "seed for reproducible sample selection")
	timeout := flags.Duration("timeout", 30*time.Second, "connection and extraction timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dbURL == "" {
		return fmt.Errorf("usage: pg2toon -db <url>")
	}
	if *timeout <= 0 {
		return fmt.Errorf("timeout must be greater than zero")
	}
	if *exampleSample < 0 {
		return fmt.Errorf("example-sample must not be negative")
	}
	schemas, err := selectedSchemas(*schemaName, *schemaNames)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, *timeout)
	defer cancel()

	extractor, err := postgres.New(ctx, *dbURL)
	if err != nil {
		return fmt.Errorf("DB connection error: %w", err)
	}
	defer extractor.Close(context.Background())

	db, err := extractor.Extract(ctx, database.ExtractOptions{
		Schemas:              schemas,
		IncludePartitioned:   *includePartitioned,
		ExcludeTables:        splitCommaSeparated(*excludeTables),
		ExcludeExampleTables: splitCommaSeparated(*excludeExampleTables),
		ExcludeExampleFields: splitCommaSeparated(*excludeExampleFields),
		ExampleSample:        *exampleSample,
		ExampleSampleOrdered: *exampleSampleOrdered,
		Seed:                 *seed,
	})
	if err != nil {
		return fmt.Errorf("schema extraction failed: %w", err)
	}

	w := stdout
	var file *os.File
	if *output != "" {
		file, err = os.Create(*output)
		if err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		w = file
	}
	if err := toon.Encode(w, db); err != nil {
		if file != nil {
			_ = file.Close()
		}
		return fmt.Errorf("write output: %w", err)
	}
	if file != nil {
		if err := file.Close(); err != nil {
			return fmt.Errorf("close output: %w", err)
		}
	}
	return nil
}

func splitSchemas(value string) []string {
	return splitCommaSeparated(value)
}

func splitCommaSeparated(value string) []string {
	var result []string
	for _, name := range strings.Split(value, ",") {
		if name = strings.TrimSpace(name); name != "" {
			result = append(result, name)
		}
	}
	return result
}

func selectedSchemas(schemaName, schemaNames string) ([]string, error) {
	schemaName = strings.TrimSpace(schemaName)
	schemas := splitSchemas(schemaNames)
	if schemaName != "" && len(schemas) != 0 {
		return nil, fmt.Errorf("schema and schemas flags cannot be used together")
	}
	if schemaName != "" {
		return []string{schemaName}, nil
	}
	if len(schemas) != 0 {
		return schemas, nil
	}
	return []string{"public"}, nil
}
