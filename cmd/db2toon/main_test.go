package main

import (
	"io"
	"strings"
	"testing"
)

func TestRunRequiresDialect(t *testing.T) {
	err := cliRun([]string{})
	if err == nil || !strings.Contains(err.Error(), "usage: db2toon <dialect>") {
		t.Fatalf("run() error = %v, want dialect usage error", err)
	}
}

func TestRunRejectsUnsupportedDialect(t *testing.T) {
	err := cliRun([]string{"oracle", "-db", "unused"})
	if err == nil || !strings.Contains(err.Error(), `unsupported database dialect "oracle"`) {
		t.Fatalf("run() error = %v, want unsupported dialect error", err)
	}
}

func TestRunAcceptsMSSQL(t *testing.T) {
	err := cliRun([]string{"mssql", "-db", "invalid"})
	if err == nil || strings.Contains(err.Error(), "unsupported database dialect") {
		t.Fatalf("run() error = %v, want connection failure after accepting mssql", err)
	}
}

func TestRunRequiresDatabaseURL(t *testing.T) {
	err := cliRun([]string{"postgres"})
	if err == nil || !strings.Contains(err.Error(), "usage: db2toon -db <url>") {
		t.Fatalf("run() error = %v, want database URL usage error", err)
	}
}

func TestRunRejectsInvalidOptionsBeforeConnecting(t *testing.T) {
	err := cliRun([]string{"postgres", "-db", "unused", "-timeout", "0s"})
	if err == nil || !strings.Contains(err.Error(), "timeout must be greater than zero") {
		t.Fatalf("run() error = %v, want timeout validation error", err)
	}
}

func cliRun(args []string) error {
	return run(args, io.Discard)
}
