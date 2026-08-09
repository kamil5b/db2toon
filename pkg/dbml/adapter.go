package dbml

import (
	"io"

	"github.com/kamil5b/db2toon/pkg/toon"
)

// Convert parses DBML from r and writes its TOON representation to w.
func Convert(w io.Writer, r io.Reader) error {
	db, err := Parse(r)
	if err != nil {
		return err
	}
	return toon.Encode(w, db)
}
