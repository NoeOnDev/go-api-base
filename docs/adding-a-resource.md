# Adding a resource

This guide uses an `orders` resource (each user's orders) as the example. Before starting, choose which example to copy:

| If the data… | Copy | For example |
|---|---|---|
| belongs **to each user** | `internal/notes` | orders, tasks, addresses, favorites |
| is **shared** and managed by an admin | `internal/categories` | categories, branches, settings |
| is shared and **relates to another table** or needs filters | `internal/products` | products, items, services |

## 1. Migration

```bash
make migrate-new name=create_orders
```

It creates `migrations/0000N_create_orders.sql`. Fill it in:

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

**Good practices:**
- **Money goes in cents** (`bigint`).
- **Every foreign key gets an index**: PostgreSQL does not create it on its own.
- **For relationships, choose what happens on delete:**
  - `ON DELETE CASCADE`: deletes the dependent rows;
  - `ON DELETE RESTRICT`: prevents the deletion, like `products` → `categories`.

Apply it with `make migrate-up`, or by restarting the API, which migrates on startup.

## 2. Queries

Create `internal/db/queries/orders.sql`. If the data belongs to each user, **every** query filters by `user_id`:

```sql
-- name: GetOrder :one
SELECT id, user_id, status, total_cents, created_at, updated_at
FROM orders
WHERE id = $1 AND user_id = $2;
```

The `-- name: Name :kind` comment defines the function that will be generated:

| Kind | Returns |
|---|---|
| `:one` | one row; `pgx.ErrNoRows` if it does not exist |
| `:many` | a slice of rows (empty if there are none) |
| `:exec` | only an error |
| `:execrows` | the number of affected rows |

**Useful patterns**, all with examples in `products.sql`:
- **Named parameters:** `sqlc.arg('limit')`.
- **Optional filters:** `(sqlc.narg('status')::text IS NULL OR status = sqlc.narg('status'))`. sqlc generates a pointer, and `nil` means "no filter".
- **Text search:** `ILIKE '%' || $1 || '%'`, as in `products`, does not use indexes: PostgreSQL scans the whole table. It is enough for a few thousand rows. If it grows, a migration with the `pg_trgm` extension and an index that does work for those searches:

  ```sql
  CREATE EXTENSION IF NOT EXISTS pg_trgm;
  CREATE INDEX products_name_trgm_idx ON products USING gin (name gin_trgm_ops);
  ```
- **Data from another table:** a `JOIN`, naming the columns with `AS` (`c.name AS category_name`).

## 3. Generate the code

```bash
make sqlc
```

It generates the `Order` struct and functions like `GetOrder(ctx, params)` in `internal/store`. **Do not edit those files**: they are regenerated.

## 4. The resource package

Copy the example package to `internal/orders` and adapt each file:

- **`validation.go`:**
  - The input struct with its JSON tags, `normalize()` and `validate()`.
  - For the common rules use `internal/validate`: `Required`, `MaxLen`, `Slug` and `NonNegative`.
  - Gather the errors in `fields := httpx.Fields{}`, with one message per field (`fields["title"] = i18n.M("validation.required")`), and return `httpx.Validation(fields)`. The messages are keys of the `internal/i18n` catalogs, not text: see [languages](i18n.md).
- **`handlers.go`:**
  - The response struct and `toResponse`.
  - One handler per endpoint, with the signature `func(w, r) error`.
  - With a session, the user is `auth.MustUser(r.Context())`.
  - Translate the errors:

    | Error | Response |
    |---|---|
    | `pgx.ErrNoRows` or 0 affected rows | `httpx.NotFound("order_not_found")` |
    | `db.IsUniqueViolation(err)` | `httpx.Conflict("order_number_taken", "field")` |
    | `db.IsForeignKeyViolation(err)` when creating or editing | `httpx.Validation` |
    | `db.IsForeignKeyViolation(err)` when deleting | `httpx.Conflict("order_in_use", "")` |
    | each new code | its message in `internal/i18n/locales/*.json` (`"errors.order_not_found": "…"`) and a row in the table of [languages](i18n.md) |
    | anything else | wrap it with context: `fmt.Errorf("create order: %w", err)` |

- **Several changes that belong together, in a transaction.** If an operation makes more than one write and everything or nothing must happen (for example, creating the order and decrementing the stock), use `pgx.BeginFunc` with the transaction's queries (`q.WithTx(tx)`). If the function returns an error, everything is rolled back:

  ```go
  // The handler needs the pool (pass it when creating it in routes.go).
  err := pgx.BeginFunc(r.Context(), h.db, func(tx pgx.Tx) error {
  	q := h.q.WithTx(tx)
  	order, err := q.CreateOrder(r.Context(), params)
  	if err != nil {
  		return fmt.Errorf("create order: %w", err)
  	}
  	return q.DecrementStock(r.Context(), store.DecrementStockParams{ProductID: order.ProductID, Quantity: order.Quantity})
  })
  ```

  - **Nothing slow inside:** compute what takes time beforehand (a hash with `auth.HashPassword`, a call to another API). While the transaction is open, it takes a database connection and can lock rows.
  - **Notices after the commit:** publishing an event (`broker.Publish`) or queuing a job outside the transaction goes after the `BeginFunc`. To queue inside the same transaction, use `InsertTx` (see [docs/jobs.md](jobs.md)).
  - **If two simultaneous requests can step on each other** (read, decide and write), lock the row at the start with `SELECT … FOR UPDATE`, as `issueToken` does with `LockUser`.
- **`routes.go`:** the routes, with the right protection:

  ```go
  mux.Handle("GET /orders", authn.RequireUser(httpx.Handle(h.list)))
  mux.Handle("PUT /orders/{id}/cancel", authn.RequireUser(httpx.Handle(h.cancel)))
  mux.Handle("GET /admin/orders", authn.RequireAdmin(httpx.Handle(h.listAll)))
  ```

- **`handlers_test.go`:** with `testutil.NewDB`, `testutil.Login`, `testutil.NewAPI`, `testutil.Do` and `testutil.Decode`, as in `internal/products`. Cover:
  - the happy path;
  - validation (422);
  - not found (404);
  - no session (401);
  - no permission (403) or ownership (another user's resource gives 404), as applicable.

## 5. Register the routes

In `internal/server/routes.go`:

```go
orders.Register(mux, orders.NewHandler(queries), authService)
```

## 6. Check

```bash
make check
```

It runs gofmt, checks that the generated code is up to date, and runs go vet, staticcheck, the tests and govulncheck.

## With an agent

Since `AGENTS.md` describes these patterns, it is enough to ask for something like:

> Create the per-user `orders` resource, with `status` (pending, paid or cancelled) and `total_cents`, following the `notes` pattern. Add `PUT /orders/{id}/cancel`, which only cancels pending orders.
