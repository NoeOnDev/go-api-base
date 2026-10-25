# Comandos del proyecto. Ejecutá "make" o "make help" para ver la lista.

# Carga .env si existe, para no tener que exportar las variables a mano en desarrollo.
# Nota: make interpreta "$" en los valores; evitá ese carácter en las contraseñas de .env.
ifneq (,$(wildcard .env))
include .env
export
endif

# Herramientas de desarrollo con versión fija. "go run paquete@versión" las descarga
# (verificadas contra la base de checksums de Go) y las compila la primera vez; después
# usa la caché. No agregan nada al go.mod de la aplicación.
SQLC_VERSION        := v1.31.1
GOOSE_VERSION       := v3.29.0
GOVULNCHECK_VERSION := v1.9.0
# Tiene que coincidir con la versión de github.com/riverqueue/river del go.mod.
RIVER_VERSION       := v0.49.0

SQLC        := go run github.com/sqlc-dev/sqlc/cmd/sqlc@$(SQLC_VERSION)
GOOSE_CLI   := go run github.com/pressly/goose/v3/cmd/goose@$(GOOSE_VERSION)
GOVULNCHECK := go run golang.org/x/vuln/cmd/govulncheck@$(GOVULNCHECK_VERSION)
RIVER_CLI   := go run github.com/riverqueue/river/cmd/river@$(RIVER_VERSION)

# Los comandos de goose llevan @ para que make no imprima la URL con la contraseña.
GOOSE := $(GOOSE_CLI) -dir migrations postgres "$(DATABASE_URL)"

.DEFAULT_GOAL := help

.PHONY: help
help: ## Muestra esta ayuda
	@grep -hE '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

## --- Desarrollo

.PHONY: run
run: ## Levanta la API (aplica las migraciones al arrancar)
	go run ./cmd/api

.PHONY: build
build: ## Compila un binario estático y liviano en bin/api
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/api ./cmd/api

.PHONY: key
key: ## Genera una clave nueva para ENCRYPTION_KEY (ver docs/encryption.md)
	@go run ./cmd/api generate-key

.PHONY: rotate-keys
rotate-keys: ## Vuelve a cifrar con ENCRYPTION_KEY lo que esté con claves anteriores e informa qué falta
	go run ./cmd/api rotate-keys

.PHONY: create-admin
create-admin: ## Crea un administrador: make create-admin email=admin@ejemplo.com name="Admin"
	@test -n "$(email)" || { echo "Falta el email: make create-admin email=admin@ejemplo.com"; exit 1; }
	go run ./cmd/api create-admin -email "$(email)" -name "$(or $(name),Administrador)"

.PHONY: sqlc
sqlc: ## Regenera internal/store a partir de migraciones y consultas
	$(SQLC) generate

.PHONY: tidy
tidy: ## Ordena las dependencias de go.mod
	go mod tidy

.PHONY: clean
clean: ## Borra los binarios compilados
	rm -rf bin

## --- Calidad

.PHONY: fmt
fmt: ## Formatea el código
	gofmt -w .

.PHONY: vet
vet: ## Busca errores comunes con go vet
	go vet ./...

.PHONY: test
# -p 1 corre los paquetes de a uno: los tests de integración comparten la base de pruebas
# y vacían tablas, así que en paralelo se pisarían entre sí.
test: ## Corre los tests (los de base de datos usan TEST_DATABASE_URL)
	@test -n "$(TEST_DATABASE_URL)" || echo "AVISO: TEST_DATABASE_URL no está definida: los tests de integración NO van a correr (se saltean). Ver .env.example."
	go test -p 1 ./...

.PHONY: vuln
vuln: ## Busca vulnerabilidades conocidas en el código y sus dependencias
	$(GOVULNCHECK) ./...

.PHONY: check
check: ## Todo lo que debe pasar antes de commitear
	@echo "==> gofmt"
	@test -z "$$(gofmt -l .)" || { echo "Archivos sin formatear (corré make fmt):"; gofmt -l .; exit 1; }
	@echo "==> sqlc (código generado al día)"
	@$(SQLC) diff || { echo "internal/store no coincide con las consultas: corré make sqlc"; exit 1; }
	@echo "==> go vet"
	@go vet ./...
	@echo "==> go test"
	@go test -p 1 ./...
	@echo "==> govulncheck"
	@$(GOVULNCHECK) ./...

## --- Migraciones (en desarrollo; la API también migra al arrancar)

.PHONY: migrate-up
migrate-up: ## Aplica las migraciones pendientes
	@$(GOOSE) up

.PHONY: migrate-down
migrate-down: ## Revierte la última migración
	@$(GOOSE) down

.PHONY: migrate-status
migrate-status: ## Muestra qué migraciones están aplicadas
	@$(GOOSE) status

.PHONY: river-migration
river-migration: ## SQL de las migraciones de River para goose: make river-migration [version=N]
	@if [ -n "$(version)" ]; then \
		echo "-- +goose Up"; echo "-- +goose StatementBegin"; $(RIVER_CLI) migrate-get --version $(version) --up; echo "-- +goose StatementEnd"; \
		echo; echo "-- +goose Down"; echo "-- +goose StatementBegin"; $(RIVER_CLI) migrate-get --version $(version) --down; echo "-- +goose StatementEnd"; \
	else \
		$(RIVER_CLI) migrate-get --all --exclude-version 1 --up; \
	fi

.PHONY: migrate-new
migrate-new: ## Crea una migración: make migrate-new name=create_products
	@test -n "$(name)" || { echo "Falta el nombre: make migrate-new name=create_products"; exit 1; }
	$(GOOSE_CLI) -dir migrations -s create $(name) sql
