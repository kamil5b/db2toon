package dbml

import (
	"strings"
	"testing"
)

func TestConvert(t *testing.T) {
	input := `
Table public.users {
  id integer [pk, increment]
  email varchar(255) [not null, unique, note: 'Login address']
  Note: 'User accounts'
}
Table public.posts {
  id bigint [pk]
  author_id integer [not null, ref: > public.users.id]
  title varchar [default: 'Untitled']
  indexes {
    (author_id, title) [name: 'posts_author_title', unique]
  }
}
Ref: public.posts.author_id > public.users.id [delete: cascade]
`
	var out strings.Builder
	if err := Convert(&out, strings.NewReader(input)); err != nil {
		t.Fatal(err)
	}
	want := `[users]
# User accounts
  id integer {pk,identity=default}
  email varchar(255) {req} // Login address
@constraints
  email_unique: unique (email)

[posts]
  id bigint {pk}
  author_id integer {req} -> users(id) {on_delete=cascade}
  title varchar = Untitled
@indices
  posts_author_title: unique btree (author_id, title)

`
	got := out.String()
	if got != want {
		t.Fatalf("Convert() =\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestParseCompositeReference(t *testing.T) {
	input := `Table parent {
  a int [pk]
  b int [pk]
}
Table child {
  a int
  b int
}
Ref: child.(a,b) > parent.(a,b) [update: no action, delete: set null]
`
	db, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	fk := db.Schemas[0].Tables[1].ForeignKeys[0]
	if got := strings.Join(fk.LocalColumns, ","); got != "a,b" {
		t.Fatalf("local columns = %q, want a,b", got)
	}
	if fk.OnDelete != "SET NULL" || fk.OnUpdate != "NO ACTION" {
		t.Fatalf("actions = %q/%q", fk.OnDelete, fk.OnUpdate)
	}
}

func TestParseUnknownReferenceTable(t *testing.T) {
	_, err := Parse(strings.NewReader("Ref: missing.id > users.id\n"))
	if err == nil || !strings.Contains(err.Error(), `reference table "missing" not found`) {
		t.Fatalf("Parse() error = %v", err)
	}
}
