// Package sqlite contains the SQLite database adapter.
//
// SQLite exposes a single main schema by default. Attached databases can be
// selected explicitly with ExtractOptions.Schemas. SQLite has no catalog
// comment metadata, and declared types are reported as written because SQLite
// applies type affinity at runtime.
package sqlite
