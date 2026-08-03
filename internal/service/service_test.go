package service

import (
	"context"
	"strings"
	"testing"
)

func TestExtractValidatesWithoutConnecting(t *testing.T) {
	tests := []struct {
		name    string
		request Request
		want    string
	}{
		{"missing dialect", Request{DB: "postgres://user:secret@host/db"}, "dialect is required"},
		{"unsupported dialect", Request{Dialect: "oracle", DB: "unused"}, "unsupported database dialect"},
		{"bad timeout", Request{Dialect: "postgres", DB: "unused", Options: Options{Timeout: "nope"}}, "positive duration"},
		{"negative sample", Request{Dialect: "postgres", DB: "unused", Options: Options{ExampleSample: -1}}, "must not be negative"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Extract(context.Background(), tt.request)
			if err == nil || !strings.Contains(err.Message, tt.want) {
				t.Fatalf("error = %#v, want %q", err, tt.want)
			}
		})
	}
}

func TestCapabilities(t *testing.T) {
	capabilities, ok := CapabilitiesFor("POSTGRES")
	if !ok || capabilities.Dialect != "postgres" || len(capabilities.Options) == 0 {
		t.Fatalf("unexpected capabilities: %#v, %v", capabilities, ok)
	}
	if capabilities, ok := CapabilitiesFor("mysql"); !ok || capabilities.Dialect != "mysql" {
		t.Fatalf("mysql should be advertised: %#v, %v", capabilities, ok)
	}
}
