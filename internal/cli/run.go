package cli

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

	"github.com/kamil5b/db2toon/internal/database"
	"github.com/kamil5b/db2toon/internal/database/postgres"
	"github.com/kamil5b/db2toon/pkg/toon"
)

// Run executes the database-neutral CLI. fixedDialect is used by compatibility
// commands that should not expose dialect selection to their users.
func Run(args []string, stdout io.Writer, commandName, fixedDialect string) error {
	dialect := fixedDialect
	if dialect == "" {
		if len(args) == 0 {
			return fmt.Errorf("usage: %s <dialect> -db <url>", commandName)
		}
		dialect = strings.ToLower(args[0])
		args = args[1:]
	}
	if dialect != "postgres" {
		return fmt.Errorf("unsupported database dialect %q (supported: postgres)", dialect)
	}

	flags := flag.NewFlagSet(commandName, flag.ContinueOnError)
	dbURL := flags.String("db", "", "Postgres URL")
	output := flags.String("out", "", "output file (default stdout)")
	schemaName := flags.String("schema", "", "schema to extract (default public)")
	schemaNames := flags.String("schemas", "", "comma-separated schemas to extract")
	includePartitioned := flags.Bool("include-partitioned", false, "include partitioned tables")
	excludeTables := flags.String("exclude-tables", "", "comma-separated tables to exclude")
	excludeExampleTables := flags.String("exclude-example-tables", "", "comma-separated tables to exclude from examples")
	excludeExampleFields := flags.String("exclude-example-fields", "", "comma-separated fields to exclude from examples")
	exampleSample := flags.Int("example-sample", 0, "number of sample rows to include per table")
	exampleSampleOrdered := flags.Bool("example-sample-ordered", false, "order sample rows deterministically")
	seed := flags.Int64("seed", 0, "seed for reproducible sample selection")
	timeout := flags.Duration("timeout", 30*time.Second, "connection and extraction timeout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *dbURL == "" {
		return fmt.Errorf("usage: %s -db <url>", commandName)
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
	schemas := splitCommaSeparated(schemaNames)
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
