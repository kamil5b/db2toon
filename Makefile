GO ?= go
OUTPUT_DIR ?= output
BINARY ?= $(OUTPUT_DIR)/db2toon
COMPAT_BINARY ?= $(OUTPUT_DIR)/pg2toon
MCP_BINARY ?= $(OUTPUT_DIR)/db2toon-mcp
DBML_BINARY ?= $(OUTPUT_DIR)/dbml2toon
OUT ?= $(OUTPUT_DIR)/schema.toon
DB_URL ?=
OPTS ?=

.PHONY: build test test-integration test-integration-mysql test-integration-mssql test-integration-oracle test-integration-cockroachdb test-integration-duckdb test-all benchmark-dbml run

build:
	@mkdir -p $(OUTPUT_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BINARY) ./cmd/db2toon
	CGO_ENABLED=0 $(GO) build -o $(COMPAT_BINARY) ./cmd/pg2toon
	CGO_ENABLED=0 $(GO) build -o $(MCP_BINARY) ./cmd/db2toon-mcp
	CGO_ENABLED=0 $(GO) build -o $(DBML_BINARY) ./cmd/dbml2toon

test:
	CGO_ENABLED=0 $(GO) test -v ./...

test-integration:
	CGO_ENABLED=0 $(GO) test -v -tags=integration ./internal/database/postgres
	CGO_ENABLED=0 $(GO) test -v -tags=integration ./internal/database/mysql
	CGO_ENABLED=0 $(GO) test -v -tags=integration ./internal/database/mssql
	CGO_ENABLED=0 $(GO) test -v -tags=integration ./internal/database/cockroachdb
	$(MAKE) test-integration-duckdb

test-integration-cockroachdb:
	CGO_ENABLED=0 $(GO) test -v -tags=integration ./internal/database/cockroachdb

test-integration-duckdb:
	arch=$$(uname -m); case "$$arch" in x86_64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; esac; docker build --build-arg TARGETARCH=$$arch -f internal/database/duckdb/Dockerfile.test -t db2toon-duckdb-test .
	docker run --rm -v "$$(pwd):/workspace" -w /workspace db2toon-duckdb-test env CGO_ENABLED=0 go test -v ./internal/database/duckdb

test-integration-mysql:
	CGO_ENABLED=0 $(GO) test -v -tags=integration ./internal/database/mysql

test-integration-mssql:
	CGO_ENABLED=0 $(GO) test -v -tags=integration ./internal/database/mssql

test-integration-oracle:
	CGO_ENABLED=0 $(GO) test -v -tags=integration ./internal/database/oracle

test-all: test test-integration

benchmark-dbml:
	CGO_ENABLED=0 $(GO) test -tags=integration -run '^$$' -bench BenchmarkDBMLPipeline -benchtime=1x -v .

run: build
	@test -n "$(DB_URL)" || (echo "DB_URL is required" >&2; exit 1)
	@mkdir -p $(OUTPUT_DIR)
	$(BINARY) postgres -db "$(DB_URL)" -out "$(OUT)" $(OPTS)
