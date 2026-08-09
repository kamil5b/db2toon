// Command dbml2toon converts a DBML file to TOON.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kamil5b/db2toon/pkg/dbml"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout io.Writer) error {
	fs := flag.NewFlagSet("dbml2toon", flag.ContinueOnError)
	outPath := fs.String("out", "", "output file (default stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 1 {
		return fmt.Errorf("usage: dbml2toon [file.dbml] [-out schema.toon]")
	}
	in := stdin
	var input *os.File
	if fs.NArg() == 1 {
		var err error
		input, err = os.Open(fs.Arg(0))
		if err != nil {
			return fmt.Errorf("open input: %w", err)
		}
		defer input.Close()
		in = input
	}
	out := stdout
	var output *os.File
	if *outPath != "" {
		var err error
		output, err = os.Create(*outPath)
		if err != nil {
			return fmt.Errorf("create output: %w", err)
		}
		out = output
	}
	if err := dbml.Convert(out, in); err != nil {
		if output != nil {
			_ = output.Close()
		}
		return err
	}
	if output != nil {
		if err := output.Close(); err != nil {
			return fmt.Errorf("close output: %w", err)
		}
	}
	return nil
}
