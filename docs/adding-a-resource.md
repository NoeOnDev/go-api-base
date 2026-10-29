# Cómo agregar un recurso

Esta guía usa como ejemplo un recurso `orders` (pedidos de cada usuario). Antes de empezar, elegí qué ejemplo copiar:

| Si los datos… | Copiá | Por ejemplo |
|---|---|---|
| son **de cada usuario** | `internal/notes` | pedidos, tareas, direcciones, favoritos |
| son **compartidos** y los gestiona un admin | `internal/categories` | categorías, sucursales, configuraciones |
| son compartidos y **se relacionan con otra tabla** o necesitan filtros | `internal/products` | productos, artículos, servicios |

## 1. Migración

```bash
make migrate-new name=create_orders
```

Crea `migrations/0000N_create_orders.sql`. Completalo:

```sql
-- +goose Up
CREATE TABLE orders (
    id          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    user_id     bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    status      text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'paid', 'cancelled')),
    total_cents bigint      NOT NULL CHECK (total_cents >= 0),
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX orders_user_id_idx ON orders (user_id, id DESC);

-- +goose Down
DROP TABLE orders;
```

**Buenas prácticas:**
- **El dinero va en centavos** (`bigint`).
- **Toda clave foránea lleva índice**: PostgreSQL no lo crea solo.
- **Para las relaciones, elegí qué pasa al borrar:**
  - `ON DELETE CASCADE`: borra lo dependiente;
  - `ON DELETE RESTRICT`: impide el borrado, como `products` → `categories`.

Aplicala con `make migrate-up`, o reiniciando la API, que migra al arrancar.

## 2. Consultas

Creá `internal/db/queries/orders.sql`. Si los datos son de cada usuario, **todas** las consultas filtran por `user_id`:

```sql
-- name: GetOrder :one
SELECT id, user_id, status, total_cents, created_at, updated_at
FROM orders
WHERE id = $1 AND user_id = $2;
```

El comentario `-- name: Nombre :tipo` define la función que se va a generar:

| Tipo | Devuelve |
|---|---|
| `:one` | una fila; `pgx.ErrNoRows` si no existe |
| `:many` | un slice de filas (vacío si no hay) |
| `:exec` | solo error |
| `:execrows` | cantidad de filas afectadas |

**Patrones útiles**, todos con ejemplos en `products.sql`:
- **Parámetros con nombre:** `sqlc.arg('limit')`.
- **Filtros opcionales:** `(sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))`. sqlc genera un puntero, y `nil` significa "sin filtro".
- **Búsqueda por texto:** `ILIKE '%' || $1 || '%'`, como en `products`, no usa índices: PostgreSQL recorre toda la tabla. Alcanza para unos miles de filas. Si crece, una migración con la extensión `pg_trgm` y un índice que sí sirve para esas búsquedas:

  ```sql
  CREATE EXTENSION IF NOT EXISTS pg_trgm;
  CREATE INDEX products_name_trgm_idx ON products USING gin (name gin_trgm_ops);
  ```
- **Datos de otra tabla:** un `JOIN`, nombrando las columnas con `AS` (`c.name AS category_name`).

## 3. Generar el código

```bash
make sqlc
```

Genera en `internal/store` el struct `Order` y funciones como `GetOrder(ctx, params)`. **No edites esos archivos**: se regeneran.

## 4. Paquete del recurso

Copiá el paquete de ejemplo a `internal/orders` y adaptá cada archivo:

- **`validation.go`:**
  - El struct de entrada con sus tags JSON, `normalize()` y `validate()`.
  - Para las reglas comunes usá `internal/validate`: `Required`, `MaxLen`, `Slug` y `NonNegative`.
  - Juntá los errores en `fields := httpx.Fields{}`, con un mensaje por campo (`fields["title"] = i18n.M("validation.required")`), y devolvé `httpx.Validation(fields)`. Los mensajes son claves de los catálogos de `internal/i18n`, no texto: ver [idiomas](i18n.md).
- **`handlers.go`:**
  - El struct de respuesta y `toResponse`.
  - Un handler por endpoint, con la firma `func(w, r) error`.
  - Con sesión, el usuario es `auth.MustUser(r.Context())`.
  - Traducí los errores:

    | Error | Respuesta |
    |---|---|
    | `pgx.ErrNoRows` o 0 filas afectadas | `httpx.NotFound("order_not_found")` |
    | `db.IsUniqueViolation(err)` | `httpx.Conflict("order_number_taken", "campo")` |
    | `db.IsForeignKeyViolation(err)` al crear o editar | `httpx.Validation` |
    | `db.IsForeignKeyViolation(err)` al borrar | `httpx.Conflict("order_in_use", "")` |
    | cada código nuevo | su mensaje en `internal/i18n/locales/*.json` (`"errors.order_not_found": "…"`) y una fila en la tabla de [idiomas](i18n.md) |
    | cualquier otro | envolverlo con contexto: `fmt.Errorf("crear pedido: %w", err)` |

- **Varios cambios que van juntos, en una transacción.** Si una operación hace más de una escritura y tiene que pasar todo o nada (por ejemplo, crear el pedido y descontar el stock), usá `pgx.BeginFunc` con las consultas de la transacción (`q.WithTx(tx)`). Si la función devuelve un error, se deshace todo:

  ```go
  // El handler necesita el pool (pasáselo al crearlo en routes.go).
  err := pgx.BeginFunc(r.Context(), h.db, func(tx pgx.Tx) error {
  	q := h.q.WithTx(tx)
  	order, err := q.CreateOrder(r.Context(), params)
  	if err != nil {
  		return fmt.Errorf("crear pedido: %w", err)
  	}
  	return q.DecrementStock(r.Context(), store.DecrementStockParams{ProductID: order.ProductID, Quantity: order.Quantity})
  })
  ```

  - **Nada lento adentro:** calculá antes lo que tarda (un hash con `auth.HashPassword`, una llamada a otra API). Mientras la transacción está abierta, ocupa una conexión de la base y puede bloquear filas.
  - **Avisos después del commit:** publicar un evento (`broker.Publish`) o encolar un trabajo fuera de la transacción va después del `BeginFunc`. Para encolar dentro de la misma transacción, usá `InsertTx` (ver [docs/jobs.md](jobs.md)).
  - **Si dos peticiones simultáneas pueden pisarse** (leer, decidir y escribir), bloqueá la fila al empezar con `SELECT … FOR UPDATE`, como hace `issueToken` con `LockUser`.
- **`routes.go`:** las rutas, con la protección que corresponda:

  ```go
  mux.Handle("GET /orders", authn.RequireUser(httpx.Handle(h.list)))
  mux.Handle("PUT /orders/{id}/cancel", authn.RequireUser(httpx.Handle(h.cancel)))
  mux.Handle("GET /admin/orders", authn.RequireAdmin(httpx.Handle(h.listAll)))
  ```

- **`handlers_test.go`:** con `testutil.NewDB`, `testutil.Login`, `testutil.NewAPI`, `testutil.Do` y `testutil.Decode`, como en `internal/products`. Cubrí:
  - el caso feliz;
  - validación (422);
  - no encontrado (404);
  - sin sesión (401);
  - sin permiso (403) o pertenencia (el recurso de otro usuario da 404), según corresponda.

## 5. Registrar las rutas

En `internal/server/routes.go`:

```go
orders.Register(mux, orders.NewHandler(queries), authService)
```

## 6. Verificar

```bash
make check
```

Corre gofmt, comprueba que el código generado esté al día, y ejecuta go vet, los tests y govulncheck.

## Con un agente

Como `AGENTS.md` describe estos patrones, alcanza con pedir algo como:

> Creá el recurso `orders` de cada usuario, con `status` (pending, paid o cancelled) y `total_cents`, siguiendo el patrón de `notes`. Agregá `PUT /orders/{id}/cancel`, que solo cancela pedidos pendientes.
