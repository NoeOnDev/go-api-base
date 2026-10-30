# AGENTS.md

Instructions for coding agents (Claude Code, Codex, Copilot) working on this project.

## What it is

> **This repository is a template**, not a dependency: `gonew` copies the code and the projects created from it are not
> connected to it. That is why it can be reorganized freely here (including the migrations, which are the initial schema).
> Every change to the template goes in `CHANGELOG.md`, with the SQL to bring schema changes to a project that is
> already deployed.
>
> **Migrations in a project created from the template:** until the first deploy, a new or removed field is changed
> **in the original migration** (and the local database is recreated). After the first deploy, always with a new
> migration.

`go-api-base` is a complete, production-ready REST API template in Go, for personal, client and company projects.
It runs as a single binary with PostgreSQL, with one instance or several (see "Multiple instances" in
`docs/decisions.md`).
It favors the standard library and adds only the essentials: PostgreSQL (pgx), migrations (goose), typed queries (sqlc),
the job queue (River) and packages from the Go team (`golang.org/x/crypto`, `x/time`, `x/term`, `x/text`).

It comes with complete authentication (cookie sessions, `user`/`admin` roles, profile, password change and reset,
optional email verification, 2FA with TOTP, sessions per device, email change and account deletion; see `docs/auth.md`),
email sending, background jobs with River (`docs/jobs.md`), real time with SSE and notifications (`docs/realtime.md`),
user-facing text in several languages (`docs/i18n.md`), and three **example resources** that serve as models:

| Resource | What it shows |
|---|---|
| `internal/notes` | Protected CRUD with **ownership**: each user sees only their own |
| `internal/categories` | Public reads, admin-only writes, unique value (409), no deleting while in use (409) |
| `internal/products` | Relationship with another table (JOIN), filters and search, missing FK (422), data visible by role |

## Where to look

The documentation is in `docs/` and the `README.md`, and it answers almost everything a project usually asks.
**Before saying that something is missing, does not exist or is not documented, look for it:** in this table, with
`grep -rni '<topic>' docs/ README.md` and in the code comments. If it exists, cite the file and the section. The
headings of each guide are listed with `grep -n '^#' docs/*.md`.

| Topic | Where |
|---|---|
| Adding a resource, step by step | [docs/adding-a-resource.md](docs/adding-a-resource.md) |
| Cookie sessions, auth endpoints and roles | [docs/auth.md](docs/auth.md#how-sessions-work), [roles](docs/auth.md#roles) |
| Protecting a new route (session, admin, public route) | [docs/auth.md](docs/auth.md#protecting-new-routes) |
| Managing users as an administrator: list, change the role, suspend, delete | [docs/auth.md](docs/auth.md#managing-users) |
| 2FA, password reset, verifying and changing the email, sessions per device, deleting the account | One section each in [docs/auth.md](docs/auth.md#2fa-two-factor-authentication) |
| Emails: variables, templates, retries and Mailpit | [docs/auth.md](docs/auth.md#emails) |
| Attempt limits: values and where to change them | [docs/auth.md](docs/auth.md#built-in-security) |
| Attempt limits: why in memory or in PostgreSQL | [docs/decisions.md](docs/decisions.md#attempt-limits) |
| React frontend, on the same domain or another one | [docs/auth.md](docs/auth.md#from-the-frontend-react), [docs/realtime.md](docs/realtime.md#from-the-frontend-react) |
| Background and periodic jobs | [docs/jobs.md](docs/jobs.md) |
| Real time: publishing events and topics with permissions | [docs/realtime.md](docs/realtime.md#publishing-events) |
| Streaming an AI agent's response | [docs/realtime.md](docs/realtime.md#streaming-an-ai-agents-response) |
| Multiple instances of the API | [docs/decisions.md](docs/decisions.md#multiple-instances) |
| Encryption, `ENCRYPTION_KEY` and its rotation | [docs/encryption.md](docs/encryption.md) |
| Languages: user-facing text, catalogs, adding a language or a message, and the table of error codes | [docs/i18n.md](docs/i18n.md) |
| Environment variables | [README.md](README.md#configuration) |
| What to check before production | [README.md](README.md#before-production) |
| Starting a project from the template | [README.md](README.md#starting-a-new-project) |
| **Removing** unused auth features (open registration, 2FA, email change…) | [docs/auth.md](docs/auth.md#removing-features) |
| **Removing** the job queue | [docs/jobs.md](docs/jobs.md#removing-the-queue) |
| **Removing** real time or only the notifications | [docs/realtime.md](docs/realtime.md#removing-real-time) |
| Why it is built this way (each design decision) | [docs/decisions.md](docs/decisions.md) |
| What the template leaves out on purpose: check it before proposing a new feature | [docs/decisions.md](docs/decisions.md#what-it-does-not-include), [docs/auth.md](docs/auth.md#what-it-does-not-include) |
| What changed in each version, and the SQL to bring it to a project already deployed | [CHANGELOG.md](CHANGELOG.md) |

**When you change behavior, update the documentation in the same commit:** the guide that describes it, the
"Removing…" recipes if routes, files or tables change, this table if you add a guide or a section worth finding, and
`CHANGELOG.md`. `make check` checks that the documentation links and the mentions of `docs/*.md` point to files and
sections that exist (`docs/links_test.go`).

## Commands

| Command | What for |
|---|---|
| `make run` | Starts the API (applies the migrations on startup) |
| `make check` | **Always run it before committing**: gofmt, sqlc diff, go vet, staticcheck, go test and govulncheck |
| `make test` | Tests (`go test -p 1`; the database ones use `TEST_DATABASE_URL`, and are skipped without it) |
| `make sqlc` | Regenerate `internal/store` after changing migrations or queries |
| `make migrate-new name=create_x` | Create a new migration |
| `make migrate-up` / `migrate-down` / `migrate-status` | Manage migrations in development |
| `make create-admin email=… name=…` | Create an administrator (password from `ADMIN_PASSWORD` or the terminal) |
| `make key` / `make rotate-keys` | Generate an `ENCRYPTION_KEY` / finish a rotation (see `docs/encryption.md`) |
| `make river-migration version=N` | SQL of a new River migration, when updating it (see `docs/jobs.md`) |
| `make build` | Static binary in `bin/api` |

`make` loads `.env` automatically. The tools (sqlc, goose, staticcheck, govulncheck) run with `go run package@version`,
with the versions pinned at the top of the `Makefile`; there is no need to install them. **Do not create subfolders with
their own `go.mod`**: `gonew` does not copy them.

## Structure

```
cmd/api/              startup (main.go) and the create-admin (admin.go), generate-key and rotate-keys (keys.go) subcommands
internal/config/      configuration from environment variables
internal/httpx/       JSON, errors, pagination, middlewares, CORS, per-IP request limits, client IP
internal/server/      http.Server with timeouts, routes, CSRF and middleware order (routes.go)
internal/auth/        users, passwords (argon2id), sessions, RequireUser/RequireAdmin and one file per feature
                      (profile, password reset, verification, 2FA…; see "Removing features" in docs/auth.md). What
                      several share, in account.go, session.go and tokens.go; email templates in emails/<language>/*.txt
internal/ratelimit/   attempt limits per account or user, stored in PostgreSQL (shared by every instance)
internal/i18n/        user-facing text by language: catalogs locales/*.json (en, es-MX), Accept-Language, translator
internal/mail/        email sending: LogSender, SMTPSender, Recorder (tests) and templates by language (Templates)
internal/jobs/        background jobs with River: client, workers (workers.go), periodic jobs, MailSender
internal/realtime/    real time with SSE: Stream, Broker (channels and topics), GET /events and LISTEN/NOTIFY between instances
internal/notifications/ notifications stored and sent live; auth uses them for the security notices
internal/totp/        TOTP codes for 2FA (RFC 6238), standard library only
internal/secretbox/   keyring: AES-256-GCM encryption and HMAC with the current key and the previous ones (rotation)
internal/db/          pgx pool, migrations, PostgreSQL errors and SQL queries (queries/*.sql)
internal/store/       GENERATED by sqlc: do not edit by hand
internal/validate/    shared validation rules (required, length, slug, non-negative)
internal/<resource>/  one package per resource: handlers.go, validation.go, routes.go, handlers_test.go
internal/testutil/    helpers for integration tests (NewDB, Login, NewAPI, Do, Decode)
migrations/           goose SQL migrations, embedded in the binary
docs/                 guides and design decisions (see "Where to look"); links_test.go checks their links
```

## Conventions

**General**
- Idiomatic, simple Go. Prefer the standard library; **do not add dependencies without asking**.
- Everything internal in English: code names, comments, internal errors, logs, tests, example data, command output and
  documentation.
- **No user-facing text in the code:** errors, validation, success messages, emails and notifications come from the
  catalogs of `internal/i18n` (English by default and es-MX with "tú"), in the request's language. See `docs/i18n.md`.
- Errors wrapped with context: `fmt.Errorf("create note: %w", err)`. Do not use `panic` for normal errors.
- Pass `r.Context()` to everything that talks to the database.

**HTTP**
- Handlers have the signature `func(w, r) error` and are registered with `httpx.Handle`. On an error, just `return err`.
- Read JSON with `httpx.Decode` and respond with `httpx.Write`. Never write JSON by hand.
- Client errors, with a **specific code** that is also the key of its message (`errors.<code>` in the catalogs):
  `httpx.BadRequest("malformed_json")`, `NotFound("order_not_found")`, `Unauthorized(…)`, `Forbidden(…)`,
  `Conflict("invoice_already_paid", "field")` and `Validation(httpx.Fields{"title": i18n.M("validation.required")})`.
  A new code goes in every catalog and in the table of `docs/i18n.md` (`make check` requires it).
  Any other error is answered as a 500 without exposing details.
- The rules of `internal/validate` return `i18n.Message` (empty if the value is valid: `!msg.IsZero()`).
- Pagination with `httpx.ParsePage` and the `httpx.List[T]` response (`{data, meta}`). Route IDs with `httpx.PathID(r, "id")`.
- Each resource defines its own response struct; do not return the `internal/store` structs directly.
- Logs with `httpx.LoggerFrom(r.Context())` (it already includes `request_id` and, with a session, `user_id`). To add a
  value to every line of the request, including the final one, use `httpx.AddLogAttrs(ctx, "key", value)`. Do not log
  passwords or tokens. A route that is called very often (a health check, metrics) is wrapped with `httpx.Quiet`: its
  line goes at `debug`, except for 5xx.

**Authentication and permissions**
- Protect routes in the resource's `routes.go`: `authn.RequireUser(...)` (any user), `authn.RequireAdmin(...)`
  or `authn.OptionalUser(...)` (a public route that shows more to some users; in the handler, `auth.UserFrom`).
- In a protected handler, the user is `auth.MustUser(r.Context())`. Check admin permissions with `user.IsAdmin()`, never by
  comparing `Role`: with `REQUIRE_ADMIN_TWO_FACTOR=true`, an admin without 2FA is not one yet.
- **Each user's data:** the table references `users (id)` with `ON DELETE CASCADE` (or `SET NULL` if it must stay
  without an owner); with `RESTRICT`, deleting the account (`DELETE /auth/me`) fails.
- **Ownership:** if the data belongs to each user, the table has `user_id` and **every** query filters by it
  (`WHERE id = $1 AND user_id = $2`). Someone else's resource responds 404, not 403. See `internal/notes`.
- Do not touch the CSRF or CORS protection of `internal/server/routes.go` without asking.
- `RequireUser` also requires a verified email if `REQUIRE_EMAIL_VERIFICATION=true` (403 `email_not_verified`).

**Attempt limits and multiple instances** (see "Attempt limits" and "Multiple instances" in `docs/decisions.md`)
- Per IP, in memory: `httpx.NewRateLimiter(every, burst).MiddlewareByIP(proxies)`. Each instance counts separately.
  Every route already has a general one (200 in a row, then 10 per second, in `internal/server/routes.go`); a route that
  needs a lower one adds its own.
- Per account or per user, in PostgreSQL: `ratelimit.New(queries, "name", every, burst)`, with `Allow(ctx, key)` or
  `Middleware(key)`. It applies to every instance: what stops brute force against an account always goes here.
- Nothing that must apply to every instance (a counter, a cache, an "already done") is kept in memory: it goes in
  PostgreSQL.

**Sensitive data**
- Every secret that must be stored and read later (like the 2FA secret or an external API token) is encrypted with
  `secretbox.Box.Encrypt(value, aad)`. `aad` binds the value to its row, for example `"users.totp_secret:<id>"`
  (see `totpAAD` in `internal/auth/two_factor.go`); `Decrypt` needs the same one. What is only compared (like the
  recovery codes) is stored with `Box.MAC` over the value and its owner's id, verified with `Box.MACs` (every key),
  and `Box.KeyID()` is stored too.
- If you add a new encrypted value, add it to `auth.RotateKeys` (or its equivalent) so the rotation includes it.
- Never store secrets in plain text or write them to the log.
- Passwords are hashed only with `auth.HashPassword(ctx, …)` and compared with `auth.VerifyPassword(ctx, …)`: they
  limit how many argon2 hashes run at once. Do not call `argon2` directly.
- Responses with personal data or secrets carry `Cache-Control: no-store`: wrap the route with `httpx.NoStore`
  (all of `/auth` already has it).

**Emails**
- Send them with a `mail.Sender` (in `auth`, with `s.sendEmail` and a template from `internal/auth/emails`). Never block
  the response waiting for the sending: in production the sender is a `jobs.MailSender`, which queues them with retries.
- The links in the emails point to the frontend (`FRONTEND_URL`). Tokens that travel by email are single-use, expire
  and are stored hashed (see `internal/auth/tokens.go`).

**Background jobs** (see `docs/jobs.md`)
- Each job is an arguments struct with `Kind()` and a worker with `river.WorkerDefaults`. Register the worker in
  `registerWorkers` and periodic jobs in `periodicJobs` (`internal/jobs/workers.go`). Never change a deployed `Kind`.
- If the job depends on data stored in the same request, queue it with `InsertTx` inside the transaction.
- The arguments are stored unencrypted: prefer ids, and encrypt any secret or personal data with `secretbox` (like
  `SendEmailArgs`).
- A worker can run twice (retries, rescue of interrupted jobs): it must tolerate that.
- `internal/store` has types for River's tables (`RiverJob`…): do not use them; talk to River through its client.

**Real time** (see `docs/realtime.md`)
- Publish with `broker.PublishToUser` (the user's data) or `broker.Publish` on a topic (shared data), **after**
  saving (and after the `Commit`). For a notification that is stored, `notifications.Service.Notify`.
- What is published reaches the connections of every instance (`LISTEN/NOTIFY`, in `internal/realtime/instances.go`),
  without the publisher doing anything different. An event larger than 8 KB travels through the `realtime_messages` table.
- Each new topic kind needs `broker.Authorize("kind", …)` with the permission check; without it, it is rejected.
  When you take away someone's access, call `broker.DisconnectUser` so they stop listening at once.
- To stream a long response (an AI agent), `realtime.Open` and `stream.Send`. After `Open`, errors go as an event,
  not as JSON.

**Database**
- All SQL lives in `internal/db/queries/*.sql`. After changing it, run `make sqlc`.
- **Never edit `internal/store`**: it is regenerated.
- **Before the first deploy**, change the original migrations instead of adding `ALTER TABLE` (and recreate the local
  database). **After it**, never modify a migration that was already applied: create a new one with `make migrate-new`.
- Every migration must have its `-- +goose Down` section.
- A missing row arrives as `pgx.ErrNoRows` (`:one`) or 0 affected rows (`:execrows`): respond `httpx.NotFound`.
- Several writes that belong together go in a transaction with `pgx.BeginFunc` and `q.WithTx(tx)` (see
  `docs/adding-a-resource.md`). Nothing slow inside (argon2, external calls); the notices (events, `SessionWatcher`)
  after the commit. If two simultaneous requests can step on each other, lock the row with `SELECT … FOR UPDATE`
  (like `LockUser`).
- Repeated unique value: `db.IsUniqueViolation(err)` → `httpx.Conflict`. Missing FK or one in use:
  `db.IsForeignKeyViolation(err)` → `httpx.Validation` (when creating/editing) or `httpx.Conflict` (when deleting).
- Optional filters in sqlc: `sqlc.narg('x')::type IS NULL OR column = sqlc.narg('x')`. Escape `%` and `_` in `ILIKE`
  searches. `ILIKE '%…%'` does not use indexes: if the table grows, `pg_trgm` (see `docs/adding-a-resource.md`).
- Money in cents (`bigint`), never `float`.
- A statement is cancelled after 10 seconds (`statement_timeout`, see "Server timeouts" in `docs/decisions.md`). A query or
  job that legitimately takes longer raises it only for itself: `SET LOCAL statement_timeout = '60s'` inside its transaction.

**Tests**
- Integration tests with `testutil.NewDB(t)` (empties every table) and `testutil.Login(t, pool, email, role)` for the
  session. They only use `TEST_DATABASE_URL`, which must end in `_test` and differ from `DATABASE_URL`. **Do not remove
  that protection.**
- Requests with `testutil.NewAPI`, `testutil.Do` and `testutil.Decode`, as in `internal/products/handlers_test.go`.
  To read the sent emails: `testutil.WithMailer(&mail.Recorder{})` and `testutil.TokenFromEmail`. To change the
  configuration: `testutil.WithConfig(func(c *config.Config){ … })`. To ask for another language:
  `testutil.DoWithLanguage` (without it, the responses are in English). For 2FA:
  `testutil.EnableTwoFactor(t, api, cookie, testutil.Password)` and `TwoFactor.Code`.
- The auth routes have per-IP limits (see `internal/server/routes.go`; the lowest is 10 in a row) and the account
  actions, 5 per user. With `testutil.Do` every request comes from the same IP; for more, or to simulate several IPs,
  use `testutil.DoFromIP`.
- If the test's API changes the cookie configuration (`WithConfig` with `CookieSecure`), pass the same options to
  `testutil.Login` so the cookie matches.
- Real-time events: `testutil.NewServer` (a real HTTP server) and `testutil.OpenEvents`, which returns a reader with
  `Next`, `NextComment` and `Nothing`. To publish from the test, `testutil.WithBroker`. See `internal/notes/events_test.go`.
- Workers: `rivertest.NewWorker` and `rivertest.RequireInserted`. See `internal/jobs/email_test.go`.
- Real sending over SMTP (`TestSMTPSender`) only runs with `SMTP_TEST_ADDR`, for example `SMTP_TEST_ADDR=localhost:1025`
  with Mailpit (the emails show up at `http://localhost:8025`). Without it, it is skipped; it does not run in CI.
- Tests that use the database do not use `t.Parallel()`, and the packages run in series (`-p 1`, already in `make test`).
- Always cover: the happy path, validation (422), not found (404), no session (401) and, if it applies, no permission
  (403) or ownership.

## Adding a resource

Follow `docs/adding-a-resource.md`: migration → queries → `make sqlc` → resource package
(handlers, validation, routes, tests) → register in `internal/server/routes.go` → `make check`.
Use `internal/notes` as the model if the data belongs to each user, or `internal/categories`/`products` if it is shared.

## Notes for the gopls MCP

Start the session at the project root (where `go.mod` is), so `go_workspace` detects the module.
The instructions below are the official gopls ones (`gopls mcp -instructions`).

---

# The gopls MCP server

These instructions describe how to efficiently work in the Go programming language using the gopls MCP server. You can load this file directly into a session where the gopls MCP server is connected.

## Detecting a Go workspace

At the start of every session, you MUST use the `go_workspace` tool to learn about the Go workspace. ONLY if you are in a Go workspace, you MUST run `go_vulncheck` immediately afterwards to identify any existing security risks. The rest of these instructions apply whenever that tool indicates that the user is in a Go workspace.

## Go programming workflows

These guidelines MUST be followed whenever working in a Go workspace. There are two workflows described below: the 'Read Workflow' must be followed when the user asks a question about a Go workspace. The 'Edit Workflow' must be followed when the user edits a Go workspace.

You may re-do parts of each workflow as necessary to recover from errors. However, you must not skip any steps.

### Read workflow

The goal of the read workflow is to understand the codebase.

1. **Understand the workspace layout**: Start by using `go_workspace` to understand the overall structure of the workspace, such as whether it's a module, a workspace, or a GOPATH project.

2. **Find relevant symbols**: If you're looking for a specific type, function, or variable, use `go_search`. This is a fuzzy search that will help you locate symbols even if you don't know the exact name or location.
   EXAMPLE: search for the 'Server' type: `go_search({"query":"server"})`

3. **Understand a file and its intra-package dependencies**: When you have a file path and want to understand its contents and how it connects to other files *in the same package*, use `go_file_context`. This tool will show you a summary of the declarations from other files in the same package that are used by the current file. `go_file_context` MUST be used immediately after reading any Go file for the first time, and MAY be re-used if dependencies have changed.
   EXAMPLE: to understand `server.go`'s dependencies on other files in its package: `go_file_context({"file":"/path/to/server.go"})`

4. **Understand a package's public API**: When you need to understand what a package provides to external code (i.e., its public API), use `go_package_api`. This is especially useful for understanding third-party dependencies or other packages in the same monorepo.
   EXAMPLE: to see the API of the `storage` package: `go_package_api({"packagePaths":["example.com/internal/storage"]})`

### Editing workflow

The editing workflow is iterative. You should cycle through these steps until the task is complete.

1. **Read first**: Before making any edits, follow the Read Workflow to understand the user's request and the relevant code.

2. **Find references**: Before modifying the definition of any symbol, use the `go_symbol_references` tool to find all references to that identifier. This is critical for understanding the impact of your change. Read the files containing references to evaluate if any further edits are required.
   EXAMPLE: `go_symbol_references({"file":"/path/to/server.go","symbol":"Server.Run"})`

3. **Make edits**: Make the required edits, including edits to references you identified in the previous step. Don't proceed to the next step until all planned edits are complete.

4. **Check for errors**: After every code modification, you MUST call the `go_diagnostics` tool. Pass the paths of the files you have edited. This tool will report any build or analysis errors.
   EXAMPLE: `go_diagnostics({"files":["/path/to/server.go"]})`

5. **Fix errors**: If `go_diagnostics` reports any errors, fix them. The tool may provide suggested quick fixes in the form of diffs. You should review these diffs and apply them if they are correct. Once you've applied a fix, re-run `go_diagnostics` to confirm that the issue is resolved. It is OK to ignore 'hint' or 'info' diagnostics if they are not relevant to the current task. Note that Go diagnostic messages may contain a summary of the source code, which may not match its exact text.

6. **Check for vulnerabilities**: If your edits involved adding or updating dependencies in the go.mod file, you MUST run a vulnerability check on the entire workspace. This ensures that the new dependencies do not introduce any security risks. This step should be performed after all build errors are resolved. EXAMPLE: `go_vulncheck({"pattern":"./..."})`

7. **Run tests**: Once `go_diagnostics` reports no errors (and ONLY once there are no errors), run the tests for the packages you have changed. You can do this with `go test [packagePath...]`. Don't run `go test ./...` unless the user explicitly requests it, as doing so may slow down the iteration loop.
