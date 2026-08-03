package main

import (
	"context"
	"fmt"
	"os"

	"github.com/kamil5b/db2toon/internal/mcp"
)

func main() {
	if err := mcp.NewServer(os.Stdin, os.Stdout).Serve(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
