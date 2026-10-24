# Cómo agregar un recurso

Esta guía usa como ejemplo un recurso `products` (productos), copiando el patrón de `internal/notes`.

## 1. Migración

```bash
make migrate-new name=create_products
```

Crea `migrations/0000N_create_products.sql`. Completalo:

```sql
-- +goose Up
CREATE TABLE products (
    id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    name       text           NOT NULL,
    price      numeric(12, 2) NOT NULL,
    created_at timestamptz    NOT NULL DEFAULT now(),
    updated_at timestamptz    NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE products;
```

Aplicala con `make migrate-up`, o simplemente reiniciando la API, que migra al arrancar.

## 2. Consultas

Creá `internal/db/queries/products.sql` copiando `notes.sql` y adaptando columnas y nombres:

```sql
-- name: GetProduct :one
SELECT id, name, price, created_at, updated_at
FROM products
WHERE id = $1;
```

El comentario `-- name: Nombre :tipo` define la función que se va a generar:

| Tipo | Devuelve |
|---|---|
| `:one` | una fila; `pgx.ErrNoRows` si no existe |
| `:many` | un slice de filas (vacío si no hay) |
| `:exec` | solo error |
| `:execrows` | cantidad de filas afectadas |

Para nombrar parámetros, usá `sqlc.arg('nombre')` en lugar de `$1`, como en `ListNotes`.

## 3. Generar el código

```bash
make sqlc
```

Genera en `internal/store` el struct `Product` y funciones como `GetProduct(ctx, id)`. **No edites esos archivos**: se regeneran.

Si sqlc no reconoce un tipo de columna como querés (por ejemplo `numeric`), podés ajustarlo con `overrides` en `sqlc.yaml`.

## 4. Paquete del recurso

Copiá `internal/notes` a `internal/products` y adaptá:

- **`validation.go`**: el struct de entrada (`productInput`) con sus tags JSON, `normalize()` y `validate()`. Devolvé `httpx.Validation(fields)` con un mensaje por campo.
- **`handlers.go`**:
  - el struct de respuesta (`productResponse`) y `toResponse`;
  - un handler por endpoint, con la firma `func(w, r) error`;
  - `pgx.ErrNoRows`, o 0 filas afectadas, se responde con `httpx.NotFound`;
  - los errores inesperados se envuelven con contexto (`fmt.Errorf("crear producto: %w", err)`).
- **`routes.go`**: las rutas con método y patrón, por ejemplo `mux.Handle("GET /products/{id}", httpx.Handle(h.get))`.
- **`handlers_test.go`**: los mismos casos que en notes, usando `testutil.NewDB(t, "products")`.

## 5. Registrar las rutas

En `internal/server/routes.go`:

```go
products.Register(mux, products.NewHandler(queries))
```

## 6. Verificar

```bash
make check
```

Corre gofmt, comprueba que el código generado esté al día, y ejecuta go vet, los tests y govulncheck.

## Con un agente

Como `AGENTS.md` describe este patrón, alcanza con pedir algo como:

> Creá el recurso `products` con los campos `name` (obligatorio, hasta 120 caracteres) y `price` (mayor a 0), siguiendo el patrón de `notes`.
