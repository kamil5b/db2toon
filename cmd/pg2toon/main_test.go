package main

import (
	"reflect"
	"testing"
)

func TestSelectedSchemas(t *testing.T) {
	tests := []struct {
		name    string
		schema  string
		schemas string
		want    []string
		wantErr bool
	}{
		{name: "public by default", want: []string{"public"}},
		{name: "one configured schema", schema: " audit ", want: []string{"audit"}},
		{name: "multiple configured schemas", schemas: " app, audit ", want: []string{"app", "audit"}},
		{name: "conflicting flags", schema: "app", schemas: "audit", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := selectedSchemas(tt.schema, tt.schemas)
			if (err != nil) != tt.wantErr {
				t.Fatalf("selectedSchemas() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("selectedSchemas() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestSplitCommaSeparated(t *testing.T) {
	got := splitCommaSeparated(" users, public.posts, ,audit.logs ")
	want := []string{"users", "public.posts", "audit.logs"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("splitCommaSeparated() = %#v, want %#v", got, want)
	}
}
