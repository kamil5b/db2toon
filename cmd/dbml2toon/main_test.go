package main

import (
	"strings"
	"testing"
)

func TestRunReadsStandardInput(t *testing.T) {
	var out strings.Builder
	err := run(nil, strings.NewReader("Table users {\n id int [pk]\n}\n"), &out)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := out.String(), "[users]\n  id int {pk}\n\n"; got != want {
		t.Fatalf("run() = %q, want %q", got, want)
	}
}

func TestRunRejectsExtraArguments(t *testing.T) {
	err := run([]string{"one.dbml", "two.dbml"}, strings.NewReader(""), &strings.Builder{})
	if err == nil || !strings.Contains(err.Error(), "usage: dbml2toon") {
		t.Fatalf("run() error = %v, want usage error", err)
	}
}
