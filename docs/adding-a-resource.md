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
- **A moment is a `timestamptz`**; a day without an hour, a `date`; and an hour of the wall clock, a `time` or a
  `timestamp` with its zone in another column. See [time.md](time.md).
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

### Lists by page or by cursor

| | By page (`offset`) | By cursor (`after`) |
|---|---|---|
| For | Tables with page numbers and a total: an admin panel, a catalog | A "load more" or infinite scroll, a feed, a long list or an export |
| Request | `?limit=20&offset=40` | `?limit=20&after=<meta.next>` |
| Response | `meta: {limit, offset, total}` | `meta: {limit, next}`, `next` is `null` on the last page |
| Cost | Grows with the offset (PostgreSQL reads and discards the previous rows), up to `MaxOffset`; plus a `count(*)` | The same on every page: it starts after the last row, with the index |
| Example | `internal/notes`, `categories`, `products`, `/admin/users` | `internal/notifications` |

**By cursor**, the query filters after the last row shown and asks for one more than the page, to know whether there is a next one:

```sql
-- name: ListOrders :many
SELECT id, status, total_cents, created_at
FROM orders
WHERE user_id = sqlc.arg('user_id')
  AND (sqlc.narg('after')::bigint IS NULL OR id < sqlc.narg('after'))
ORDER BY id DESC
LIMIT sqlc.arg('limit');
```

```go
type orderCursor struct {
	ID int64 `json:"id"`
}

page, err := httpx.ParseCursorPage(r) // ?limit= and ?after=
if err != nil {
	return err
}
cursor, err := httpx.DecodeCursor[orderCursor](page.After) // nil on the first page; 422 if it is not valid
if err != nil {
	return err
}
var after *int64
if cursor != nil {
	after = &cursor.ID
}
rows, err := h.q.ListOrders(r.Context(), store.ListOrdersParams{UserID: user.ID, After: after, Limit: page.Limit + 1})
// … turn rows into []orderResponse …
httpx.Write(w, http.StatusOK, httpx.NewCursorList(data, page.Limit, func(o orderResponse) any {
	return orderCursor{ID: o.ID}
}))
```

The cursor is opaque for the frontend (JSON in base64url): to sort by another column, add it to the struct and to the query (`WHERE (created_at, id) < ($1, $2)`), and the frontend does not change. Sort always by something unique (the `id`, or the column plus the `id`), so no row is repeated or skipped between pages.

### Sorting a list

A list the client can sort takes `?sort=` with the name of a column, and `-` in front for descending: `?sort=price`, `?sort=-price`. The handler reads it with `httpx.ParseSort`, which only lets through the columns it is given (another value answers `422` on `sort`):

```go
// The newest first, unless ?sort= asks for another order.
sort, err := httpx.ParseSort(r, "-created_at", "name", "price", "created_at")
```

The SQL of a sqlc query is fixed, so the query has one `CASE` per order it accepts. Each one applies only when `sort` names it; the others are `NULL` for every row and do not sort:

```sql
ORDER BY
  CASE WHEN sqlc.arg('sort')::text = 'name' THEN name END,
  CASE WHEN sqlc.arg('sort')::text = '-name' THEN name END DESC,
  CASE WHEN sqlc.arg('sort')::text = 'price' THEN price_cents END,
  CASE WHEN sqlc.arg('sort')::text = '-price' THEN price_cents END DESC,
  id DESC
LIMIT sqlc.arg('limit') OFFSET sqlc.arg('offset');
```

- **Always end with the `id`:** it breaks the ties, so two rows with the same price do not swap places between one page and the next.
- **Only the columns a user has a reason to sort by.** Each one is two lines of the query, and a column without an index sorts the whole result on each request: fine for a catalog of thousands of rows, not for a table of millions.
- **Never build the `ORDER BY` from the text of the request:** that is how an SQL injection gets in. Here the text only chooses among the cases written in the query.
- It is for lists by page. A list by cursor has a single order, the one its cursor encodes.

`internal/products` does it (`ListProducts`).

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

    The three `db.Is…Violation` helpers return the name of the constraint and whether the error is that one: `if _, ok := db.IsUniqueViolation(err); ok { … }`.

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
- **Files of the resource** (an image, attachments): see "Using a file in a resource" in [files.md](files.md#using-a-file-in-a-resource). `internal/notes` has them: when you copy it for a resource without files, delete `attachments.go` and its test, its three routes, the `files` field of the handler and the release of the attachments in `delete`.
- **`routes.go`:** the routes, with the right protection:

  ```go
  mux.Handle("GET /orders", httpx.NoStore(authn.RequireUser(httpx.Handle(h.list))))
  mux.Handle("PUT /orders/{id}/cancel", httpx.NoStore(authn.RequireUser(httpx.Handle(h.cancel))))
  mux.Handle("GET /admin/orders", httpx.NoStore(authn.RequireAdmin(httpx.Handle(h.listAll))))
  mux.Handle("POST /orders/{id}/refund", httpx.NoStore(authn.RequirePermission(PermRefund, httpx.Handle(h.refund))))
  ```

  `PermRefund` is a constant of the package (`const PermRefund = "orders.refund"`): asking for it in a route is all it takes for an administrator to give it to a role (see "Roles" in [auth.md](auth.md#roles)). Use a permission for what someone other than an administrator could do, and `RequireAdmin` for the rest.

  `httpx.NoStore` (`Cache-Control: no-store`) goes on the routes with data of a person, like these orders, so neither the browser nor a proxy keeps the response (see `internal/notes/routes.go`). Data that is the same for everyone, like the categories, does not need it.

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

## 6. Seed data (optional)

If the frontend needs data to start working (a catalog, the options of a selector), add a seeder to `cmd/api/seed.go` and its line to `seeders`:

```go
func seedOrders(ctx context.Context, s seedDeps) (string, error) {
	if n, err := s.q.CountAllOrders(ctx); err != nil || n > 0 {
		return "already there", err // running the seed again adds nothing
	}
	// … create them with s.q, for the user of the seed (seedUserEmail) if they have an owner
	return "5 orders", nil
}
```

`make seed` runs every seeder in order, after the users. It is the equivalent of Laravel's seeders: development data, which a migration must not carry. `TestSeed` (`cmd/api/seed_test.go`) counts what the seed creates: add the new table to its `want`.

## 7. Check

```bash
make check
```

It runs gofmt, checks that the generated code is up to date, and runs go vet, staticcheck, the tests and govulncheck.

## With an agent

Since `AGENTS.md` describes these patterns, it is enough to ask for something like:

> Create the per-user `orders` resource, with `status` (pending, paid or cancelled) and `total_cents`, following the `notes` pattern. Add `PUT /orders/{id}/cancel`, which only cancels pending orders.
