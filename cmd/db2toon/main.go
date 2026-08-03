package main

import (
	"fmt"
	"io"
	"os"

	"github.com/kamil5b/db2toon/internal/cli"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	return cli.Run(args, stdout, "db2toon", "")
}
