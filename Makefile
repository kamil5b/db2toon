GO ?= go
OUTPUT_DIR ?= output
BINARY ?= $(OUTPUT_DIR)/db2toon
COMPAT_BINARY ?= $(OUTPUT_DIR)/pg2toon
OUT ?= $(OUTPUT_DIR)/schema.toon
DB_URL ?=
OPTS ?=

.PHONY: build test test-integration test-all run

build:
	@mkdir -p $(OUTPUT_DIR)
	CGO_ENABLED=0 $(GO) build -o $(BINARY) ./cmd/db2toon
	CGO_ENABLED=0 $(GO) build -o $(COMPAT_BINARY) ./cmd/pg2toon

test:
	CGO_ENABLED=0 $(GO) test -v ./...

test-integration:
	CGO_ENABLED=0 $(GO) test -v -tags=integration ./internal/database/postgres

test-all: test test-integration

run: build
	@test -n "$(DB_URL)" || (echo "DB_URL is required" >&2; exit 1)
	@mkdir -p $(OUTPUT_DIR)
	$(BINARY) postgres -db "$(DB_URL)" -out "$(OUT)" $(OPTS)
