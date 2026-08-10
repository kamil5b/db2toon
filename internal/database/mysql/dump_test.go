package mysql

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/kamil5b/db2toon/internal/database"
)

func TestDumpExtractorParsesDelimitedRoutinesAndTriggers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schema.sql")
	const dump = `CREATE TABLE users (id bigint NOT NULL PRIMARY KEY, name varchar(100));
DELIMITER $$
CREATE FUNCTION user_count() RETURNS bigint DETERMINISTIC
BEGIN
  RETURN 1;
END$$
CREATE TRIGGER users_normalize BEFORE INSERT ON users FOR EACH ROW
BEGIN
  SET NEW.name = lower(NEW.name);
END$$
DELIMITER ;
`
	if err := os.WriteFile(path, []byte(dump), 0600); err != nil {
		t.Fatal(err)
	}
	e, err := NewFromDump(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	db, err := e.Extract(context.Background(), database.ExtractOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(db.Schemas) != 1 || len(db.Schemas[0].Routines) != 1 {
		t.Fatalf("routines: %#v", db)
	}
	if len(db.Schemas[0].Tables) != 1 || len(db.Schemas[0].Tables[0].Triggers) != 1 {
		t.Fatalf("triggers: %#v", db.Schemas[0].Tables)
	}
}
