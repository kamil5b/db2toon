package main

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/kamil5b/db2toon/internal/cli"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	return cli.Run(args, stdout, "pg2toon", "postgres")
}

// These helpers remain local for compatibility with the existing pg2toon
// command tests while the actual CLI implementation is shared with db2toon.
func splitSchemas(value string) []string { return splitCommaSeparated(value) }

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
