package constants

import "time"

// Database dialect identifiers accepted by db2toon.
const (
	DialectPostgres    = "postgres"
	DialectSQLite      = "sqlite"
	DialectDuckDB      = "duckdb"
	DialectMySQL       = "mysql"
	DialectMariaDB     = "mariadb"
	DialectCockroachDB = "cockroachdb"
	DialectMSSQL       = "mssql"
	DialectSQLServer   = "sqlserver"
	DialectOracle      = "oracle"
)

// Default schemas used by database engines.
const (
	SchemaPublic = "public"
	SchemaMain   = "main"
	SchemaDBO    = "dbo"
)

const (
	DefaultTimeout       = 30 * time.Second
	MinimumOutputBytes   = 1024
	MaximumOutputBytes   = 4 << 20
	JSONRPCVersion       = "2.0"
	MCPProtocolVersion   = "2024-11-05"
	MCPServerName        = "db2toon"
	MCPServerVersion     = "1"
	MCPExtractSchemaTool = "db2toon.extract_schema"
)

const (
	JSONRPCParseError       = -32700
	JSONRPCInvalidRequest   = -32600
	JSONRPCMethodNotFound   = -32601
	JSONRPCInvalidParams    = -32602
	InitialScannerBuffer    = 1024
	MaximumScannerTokenSize = 1 << 20
)
