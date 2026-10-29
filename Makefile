# Project commands. Run "make" or "make help" to see the list.

# Loads .env if it exists, so the variables don't have to be exported by hand in development.
# Note: make interprets "$" in the values; avoid that character in the passwords of .env.
ifneq (,$(wildcard .env))
include .env
export
endif

# Development tools with a pinned version. "go run package@version" downloads them
# (verified against Go's checksum database) and builds them the first time; after that
# it uses the cache. They add nothing to the application's go.mod.
SQLC_VERSION        := v1.31.1
GOOSE_VERSION       := v3.29.0
GOVULNCHECK_VERSION := v1.9.0
STATICCHECK_VERSION := v0.8.2
# It must match the version of github.com/riverqueue/river in go.mod.
RIVER_VERSION       := v0.49.0

SQLC        := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
GOOSE_CLI   := go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
STATICCHECK := go run honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
RIVER_CLI   := go run github.com/riverqueue/river/cmd/river@$(RIVER_VERSION)

# The goose commands carry @ so make does not print the URL with the password.
GOOSE := $(GOOSE_CLI) -dir migrations postgres "$(DATABASE_URL)"

.DEFAULT_GOAL := help

.PHONY: help
help: ## Shows this help
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## --- Development

.PHONY: run
run: ## Starts the API (applies the migrations on startup)
	go run ./cmd/api

.PHONY: build
build: ## Builds a small static binary in bin/api
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/api ./cmd/api

.PHONY: key
key: ## Generates a new key for ENCRYPTION_KEY (see docs/encryption.md)
	@go run ./cmd/api generate-key

.PHONY: rotate-keys
rotate-keys: ## Encrypts again with ENCRYPTION_KEY what is under previous keys and reports what is left
	go run ./cmd/api rotate-keys

.PHONY: create-admin
create-admin: ## Creates an administrator: make create-admin email=admin@example.com name="Admin"
	@test -n "$(email)" || { echo "Missing the email: make create-admin email=admin@example.com"; exit 1; }
	go run ./cmd/api create-admin -email "$(email)" -name "$(or $(name),Administrator)"

.PHONY: sqlc
sqlc: ## Regenerates internal/store from the migrations and queries
	@# All of internal/store is generated. It is deleted first, because sqlc does not delete the code of a query that no longer exists.
	rm -f internal/store/*.go
	$(SQLC) generate

.PHONY: tidy
tidy: ## Tidies the dependencies in go.mod
	go mod tidy

.PHONY: clean
clean: ## Deletes the built binaries
	rm -rf bin

## --- Quality

.PHONY: fmt
fmt: ## Formats the code
	gofmt -w .

.PHONY: vet
vet: ## Looks for common mistakes with go vet
	go vet ./...

.PHONY: lint
lint: ## Looks for bugs and unused code with staticcheck
	$(STATICCHECK) ./...

.PHONY: test
# -p 1 runs the packages one at a time: the integration tests share the test database
# and empty tables, so in parallel they would step on each other.
test: ## Runs the tests (the database ones use TEST_DATABASE_URL)
	@test -n "$(TEST_DATABASE_URL)" || echo "WARNING: TEST_DATABASE_URL is not set: the integration tests will NOT run (they are skipped). See .env.example."
	go test -p 1 ./...

.PHONY: vuln
vuln: ## Looks for known vulnerabilities in the code and its dependencies
	$(GOVULNCHECK) ./...

.PHONY: check
check: ## Everything that must pass before committing
	@echo "==> gofmt"
	@test -z "$$(gofmt -l .)" || { echo "Unformatted files (run make fmt):"; gofmt -l .; exit 1; }
	@echo "==> sqlc (generated code up to date)"
	@$(SQLC) diff || { echo "internal/store does not match the queries: run make sqlc"; exit 1; }
	@for f in internal/store/*.sql.go; do test -f internal/db/queries/$$(basename $$f .go) || { echo "$$f belongs to a query that no longer exists: run make sqlc"; exit 1; }; done
	@echo "==> go vet"
	@go vet ./...
	@echo "==> staticcheck"
	@$(STATICCHECK) ./...
	@echo "==> go test"
	@go test -p 1 ./...
	@echo "==> govulncheck"
	@$(GOVULNCHECK) ./...

## --- Migrations (in development; the API also migrates on startup)

.PHONY: migrate-up
migrate-up: ## Applies the pending migrations
	@$(GOOSE) up

.PHONY: migrate-down
migrate-down: ## Reverts the last migration
	@$(GOOSE) down

.PHONY: migrate-status
migrate-status: ## Shows which migrations are applied
	@$(GOOSE) status

.PHONY: river-migration
river-migration: ## SQL of River's migrations for goose: make river-migration [version=N]
	@if [ -n "$(version)" ]; then \
		echo "-- +goose Up"; echo "-- +goose StatementBegin"; $(RIVER_CLI) migrate-get --version $(version) --up; echo "-- +goose StatementEnd"; \
		echo; echo "-- +goose Down"; echo "-- +goose StatementBegin"; $(RIVER_CLI) migrate-get --version $(version) --down; echo "-- +goose StatementEnd"; \
	else \
		$(RIVER_CLI) migrate-get --all --exclude-version 1 --up; \
	fi

.PHONY: migrate-new
migrate-new: ## Creates a migration: make migrate-new name=create_products
	@test -n "$(name)" || { echo "Missing the name: make migrate-new name=create_products"; exit 1; }
	$(GOOSE_CLI) -dir migrations -s create $(name) sql
