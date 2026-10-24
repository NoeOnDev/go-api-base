# Comandos del proyecto. Ejecutá "make" o "make help" para ver la lista.

# Carga .env si existe, para no tener que exportar las variables a mano en desarrollo.
# Nota: make interpreta "$" en los valores; evitá ese carácter en las contraseñas de .env.
ifneq (,$(wildcard .env))
include .env
export
endif

# Herramientas de desarrollo (sqlc, goose y govulncheck), versionadas en tools/go.mod.
# Los comandos de goose llevan @ para que make no imprima la URL con la contraseña.
TOOL  := go tool -modfile=tools/go.mod
GOOSE := $(TOOL) goose -dir migrations postgres "$(DATABASE_URL)"

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

.PHONY: create-admin
create-admin: ## Crea un administrador: make create-admin email=admin@ejemplo.com name="Admin"
	@test -n "$(email)" || { echo "Falta el email: make create-admin email=admin@ejemplo.com"; exit 1; }
	go run ./cmd/api create-admin -email "$(email)" -name "$(or $(name),Administrador)"

.PHONY: sqlc
sqlc: ## Regenera internal/store a partir de migraciones y consultas
	$(TOOL) sqlc generate

.PHONY: tidy
tidy: ## Ordena las dependencias de go.mod y tools/go.mod
	go mod tidy
	cd tools && go mod tidy

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
	go test -p 1 ./...

.PHONY: vuln
vuln: ## Busca vulnerabilidades conocidas en el código y sus dependencias
	$(TOOL) govulncheck ./...

.PHONY: check
check: ## Todo lo que debe pasar antes de commitear
	@echo "==> gofmt"
	@test -z "$$(gofmt -l .)" || { echo "Archivos sin formatear (corré make fmt):"; gofmt -l .; exit 1; }
	@echo "==> sqlc (código generado al día)"
	@$(TOOL) sqlc diff || { echo "internal/store no coincide con las consultas: corré make sqlc"; exit 1; }
	@echo "==> go vet"
	@go vet ./...
	@echo "==> go test"
	@go test -p 1 ./...
	@echo "==> govulncheck"
	@$(TOOL) govulncheck ./...

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

.PHONY: migrate-new
migrate-new: ## Crea una migración: make migrate-new name=create_products
	@test -n "$(name)" || { echo "Falta el nombre: make migrate-new name=create_products"; exit 1; }
	$(TOOL) goose -dir migrations -s create $(name) sql
