// Package db2toon extracts database schemas into a database-neutral model and
// optionally renders that model as TOON.
//
// Provide exactly one of Request.DB and Request.Dump. Dump files are parsed
// offline and are never executed.
package db2toon
