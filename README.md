# go-api-base

A complete, production-ready REST API template in Go, for personal, client and company projects. It comes with what almost every API needs already solved: authentication with 2FA, emails, background jobs, real time and security. That way each project starts directly with its own work.

It is complete, but with few dependencies: it favors the standard library and adds only this:
- **PostgreSQL with pgx**
- **migrations with goose**
- **typed queries with sqlc**
- **the River job queue**, also on PostgreSQL
- official packages from the Go team for passwords and request limits

It runs as a single binary, with PostgreSQL as the only service (no Redis or separate queues). It works the same with **one instance or several** behind a load balancer: sessions, per-account limits, the queue and real time are coordinated through PostgreSQL (see [docs/decisions.md](docs/decisions.md#multiple-instances)).

It includes:

- **Authentication** with sessions in an `HttpOnly` cookie, passwords with argon2id and `user`/`admin` roles. It includes a profile, password change, password reset by email, optional email verification, 2FA with an authenticator app and recovery codes, sessions per device (with optional idle expiry), verified email change and account deletion. The first admin is created with a command.
- **Emails** over SMTP (any provider), with editable templates. They go through the queue, so they are retried if they fail. In development, Mailpit or the log is used.
- **Background jobs** with River, on PostgreSQL (no Redis): retries, scheduled and periodic jobs, and queuing inside a transaction. The workers run in the same binary.
- **Real time with SSE:** `GET /events` sends the browser the user's events and those of the topics it listens to (with permission). It also works to stream an AI agent's response.
- **In-app notifications**, stored and sent live. Auth uses them to warn about security changes.
- **Security:** CSRF protection, configurable CORS, attempt limits (per IP, per account in the login and the 2FA, and per recipient in the emails), argon2 with a concurrency cap, `__Host-` cookies in production, `nosniff` and `no-store` headers, and server timeouts.
- **API basics:**
  - a single JSON error format
  - validation and pagination
  - logs with `slog` and a `request_id` per request
  - embedded migrations applied on startup
  - graceful shutdown
- **User-facing text in several languages:** English by default and Mexican Spanish, chosen with `Accept-Language`.
- **Three example resources** to copy:
  - `notes`: each user's data
  - `categories` and `products`: a catalog with relationships, filters and admin permissions
- **Quality:**
  - integration tests against PostgreSQL
  - `staticcheck` and `govulncheck`
  - CI with GitHub Actions

## Requirements

- Go 1.27.3 or newer.
- PostgreSQL, any supported version.
- `make`.
- `gcc`, only to build sqlc the first time (`make sqlc`).

## Quick start

1. **Create the database.** This example uses a PostgreSQL container called `postgres_db`; adapt it to your setup:

   ```bash
   docker exec -i postgres_db psql -U postgres <<'SQL'
   CREATE ROLE go_api_base LOGIN PASSWORD 'change-this-password';
   CREATE DATABASE go_api_base OWNER go_api_base;
   CREATE DATABASE go_api_base_test OWNER go_api_base;
   SQL
   ```

2. **Set up the environment.** Copy the example file, fill in `DATABASE_URL` and `TEST_DATABASE_URL`, and generate the encryption key:

   ```bash
   cp .env.example .env
   sed -i "s|^ENCRYPTION_KEY=.*|ENCRYPTION_KEY=$(make -s key)|" .env
   ```

3. **(Optional) Emails in development.** Start Mailpit, which shows the emails at `http://localhost:8025`, and set `MAIL_DRIVER=smtp`, `SMTP_HOST=localhost` and `SMTP_PORT=1025`:

   ```bash
   docker run -d --name mailpit -p 1025:1025 -p 8025:8025 axllent/mailpit:v1.31.6
   ```

   Without Mailpit, with `MAIL_DRIVER=log`, the emails (and their links) show up in the API's log.

4. **Create the administrator.** The command applies the migrations and asks for the password:

   ```bash
   make create-admin email=admin@example.com name="Admin"
   ```

5. **Start the API:**

   ```bash
   make run
   ```

6. **Try it.** `-c` and `-b` store and send the session cookie:

   ```bash
   curl -c cookies.txt -X POST localhost:8080/auth/register \
     -d '{"name":"Alice","email":"alice@example.com","password":"a-long-password"}'
   curl -b cookies.txt -X POST localhost:8080/notes -d '{"title":"My first note"}'
   curl -b cookies.txt localhost:8080/notes
   curl 'localhost:8080/products?q=café'
   ```

## Endpoints

| Method | Path | Access |
|---|---|---|
| `GET` | `/healthz` | Public. `200` if the API and the database respond, `503` if not |
| `POST` | `/auth/register`, `/auth/login` | Public, with attempt limits |
| `POST` | `/auth/logout` | Public; deletes the session if it exists and always deletes the cookie |
| `GET`, `PUT` | `/auth/me` | With a session: view and edit the profile |
| `PUT` | `/auth/password` | With a session: change the password (asks for the current one and closes the other sessions) |
| `DELETE` | `/auth/me` | With a session: delete the account (asks for the password) |
| `PUT`, `POST` | `/auth/email`, `/auth/email/confirm` | Change the email: requested with the password and confirmed with the link that reaches the new one |
| `GET`, `DELETE`, `POST` | `/auth/sessions`, `/auth/sessions/{id}`, `/auth/sessions/revoke-others` | With a session: see the sessions per device and close them |
| `POST` | `/auth/password/forgot`, `/auth/password/reset` | Public: reset the password by email |
| `POST` | `/auth/email/verify`, `/auth/email/resend` | Verify the email (required with `REQUIRE_EMAIL_VERIFICATION=true`) |
| `POST` | `/auth/two-factor/setup`, `/enable`, `/disable`, `/recovery-codes` | With a session: turn on and manage 2FA |
| `POST` | `/auth/two-factor/challenge` | Second step of the login when the user has 2FA |
| `GET` | `/events?topics=` | With a session: SSE stream with the real-time events (see [docs/realtime.md](docs/realtime.md)) |
| `GET` | `/notifications?unread=` | With a session: the user's notifications and how many are unread |
| `POST` | `/notifications/{id}/read`, `/notifications/read-all` | With a session: mark them as read |
| `GET`, `POST` | `/notes` | With a session; each user sees only their notes |
| `GET`, `PUT`, `DELETE` | `/notes/{id}` | With a session; another user's note responds `404` |
| `GET` | `/categories`, `/categories/{id}` | Public |
| `POST`, `PUT`, `DELETE` | `/categories…` | Admin only |
| `GET` | `/products?category_id=&q=&active=`, `/products/{id}` | Public: visitors only see active products; the admin sees all |
| `POST`, `PUT`, `DELETE` | `/products…` | Admin only |

Lists accept `?limit=` (from 1 to 100, 20 by default) and `?offset=` (up to 10000), and respond `{"data": [...], "meta": {"limit", "offset", "total"}}`. A huge offset makes the query slow, because PostgreSQL reads and discards the previous rows; to go further, paginate by cursor (`WHERE id < $last`).

All errors have the same shape:

```json
{"error": {"code": "validation_failed", "message": "The data sent is not valid.", "fields": {"title": "This field is required."}}}
```

| Status | When |
|---|---|
| `400` | Malformed JSON or invalid ID |
| `401` | No session |
| `403` | No permission, or a request from another origin |
| `404` | The resource does not exist |
| `409` | Repeated value, or the resource is in use |
| `422` | Validation |
| `429` | Too many attempts |
| `500` | Internal error, without details; the cause is in the log with its `request_id` |

Each error also has a specific `code` (`email_taken`, `slug_taken`, `invalid_credentials`…), and `message` comes in the language that `Accept-Language` asks for: English by default, or Mexican Spanish. The full list of codes and how to add a language are in [docs/i18n.md](docs/i18n.md).

## Configuration

Everything is configured with environment variables. [`.env.example`](.env.example) has the full list.

| Variable | Default | What for |
|---|---|---|
| `APP_ENV` | `development` | `production` turns on JSON logs and the `Secure` cookie |
| `HTTP_ADDR` | `:8080` | Server address |
| `DATABASE_URL` | (required) | PostgreSQL connection. The pool has 10 connections unless the URL sets another size (`?pool_max_conns=20`) |
| `TEST_DATABASE_URL` | | Database of the integration tests |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `SHUTDOWN_TIMEOUT` | `10s` | Maximum wait when the server shuts down |
| `SESSION_TTL` | `168h` | Session lifetime |
| `SESSION_IDLE_TIMEOUT` | `0` | Closes a session without activity for that long (for example `30m`, at least `15m`). `0` turns it off |
| `COOKIE_SECURE` | `true` in production | The cookie only travels over HTTPS |
| `CORS_ALLOWED_ORIGINS` | | Frontend origins if it is on another domain |
| `TRUST_PROXY` | `false` | Read the real IP from `X-Forwarded-For` (with a proxy in front). It is only trusted if the connection comes from localhost or a private network |
| `TRUSTED_PROXIES` | | With `TRUST_PROXY=true`, the exact IPs or CIDR ranges of the proxy (for example `127.0.0.1`), instead of every private network. With a CDN in front, like Cloudflare, its ranges too (see "Before production") |
| `FRONTEND_URL` | `http://localhost:5173` | Base of the links in the emails |
| `REQUIRE_EMAIL_VERIFICATION` | `false` | Require a verified email to use the protected routes |
| `MAIL_DRIVER` | `log` | `log` (writes the emails to the log) or `smtp` |
| `MAIL_FROM` | `no-reply@localhost` | Sender |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD` | — / `587` | SMTP server details |
| `APP_NAME` | `My App` | Name shown by the 2FA authenticator app |
| `DEFAULT_LOCALE` | `en` | Language of the user-facing text when the request does not ask for an available one (see [docs/i18n.md](docs/i18n.md)) |
| `ENCRYPTION_KEY` | (required) | Key to encrypt the 2FA secret. Generate it with `make key` (see [docs/encryption.md](docs/encryption.md)) |
| `ENCRYPTION_PREVIOUS_KEYS` | | Previous keys, only during a rotation |
| `JOBS_MAX_WORKERS` | `10` | Background jobs that run at once (emails have their own queue of 4) |
| `GOMEMLIMIT` | (no limit) | Go's own variable, not the template's. With it, the garbage collector frees memory before reaching that value. In production, around 70% of the server's RAM is a good choice (with 1 GB, `700MiB`) |

Values with spaces go in quotes: `APP_NAME="My App"`.

### Before production

What to check in the configuration, whatever the server. If `COOKIE_SECURE` or `MAIL_DRIVER` are wrong in production, or if `FRONTEND_URL` or `MAIL_FROM` still point to `localhost`, the API warns about it in the log on startup.

- **`APP_ENV=production`:** JSON logs and `Secure` cookies with the `__Host-` prefix.
- **HTTPS in front of the API:** the API speaks HTTP, and the proxy or load balancer terminates TLS. Without HTTPS, `Secure` cookies do not travel and nobody can log in.
- **`TRUST_PROXY=true` only if there is a proxy in front, and with the API port closed to the internet.**
  - With the proxy on the same server, make the API listen only there: `HTTP_ADDR=127.0.0.1:8080`, and also `TRUSTED_PROXIES=127.0.0.1`.
  - If your provider has a private network shared between customers, always set the exact IP of the proxy with `TRUSTED_PROXIES`.
  - With a CDN in front of the proxy, add the CDN ranges (for example, [Cloudflare's](https://www.cloudflare.com/ips/)) to `TRUSTED_PROXIES` besides the proxy. The proxy must append its IP to the `X-Forwarded-For` the CDN sends, not replace it. Otherwise, every user going through the same CDN node shares the attempt limits.
- **`MAIL_DRIVER=smtp`** with a real provider, and **`FRONTEND_URL`** with the frontend's domain (the links in the emails point there). With the frontend on another domain, `CORS_ALLOWED_ORIGINS` too.
- **`ENCRYPTION_KEY` stored as a secret and backed up apart from the database:** if it is lost, users have to set up 2FA again (see [docs/encryption.md](docs/encryption.md)).
- **`GOMEMLIMIT`** around 70% of the server's RAM.
- **A direct connection to PostgreSQL**, or PgBouncer in `session` mode. Real time and River listen with `LISTEN`, which does not work with PgBouncer in `transaction` mode.

## Commands

Run `make` to see the full list. The main ones:

| Command | What for |
|---|---|
| `make run` | Starts the API |
| `make check` | gofmt, sqlc up to date, go vet, staticcheck, tests and govulncheck. Run it before each commit |
| `make sqlc` | Regenerates the database code after changing migrations or queries |
| `make migrate-new name=create_x` | Creates a migration |
| `make create-admin email=…` | Creates an administrator |
| `make key` | Generates a key for `ENCRYPTION_KEY` |
| `make rotate-keys` | Finishes a rotation of `ENCRYPTION_KEY` (see [docs/encryption.md](docs/encryption.md)) |
| `make river-migration version=N` | SQL of a new River migration, when updating it (see [docs/jobs.md](docs/jobs.md)) |
| `make build` | Static binary in `bin/api` |

## Structure

```
cmd/api/             application startup and the create-admin, generate-key and rotate-keys commands
internal/config/     environment variables
internal/httpx/      JSON, errors, pagination, middlewares, CORS and per-IP request limits
internal/server/     server, routes and CSRF protection
internal/auth/       users, sessions, roles, profile, password reset and verification (and their email templates)
internal/ratelimit/  attempt limits per account or user, in PostgreSQL
internal/i18n/       user-facing text by language (English and es-MX)
internal/mail/       email sending (log and SMTP) and templates by language
internal/jobs/       background jobs (River): workers, periodic jobs and the email queue
internal/realtime/   real time with SSE: streams, channels and topics
internal/notifications/ notifications stored and sent live
internal/totp/       TOTP codes for 2FA (RFC 6238)
internal/secretbox/  encryption (AES-256-GCM) and HMAC with a keyring of rotatable keys
internal/db/         connection, migrations, PostgreSQL errors and SQL queries
internal/store/      code generated by sqlc (do not edit)
internal/validate/   shared validation rules
internal/notes/      example: each user's data
internal/categories/ example: catalog, public reads and admin writes
internal/products/   example: relationships, filters and search
internal/testutil/   test helpers
migrations/          SQL migrations
docs/                guides and decisions
```

## Starting a new project

**With `gonew`** (if the repository is published):

```bash
go install golang.org/x/tools/cmd/gonew@latest
gonew github.com/NoeOnDev/go-api-base github.com/your-user/my-project
```

`gonew` copies the template and changes the module name and every import.

**Copying the folder** (if the repository is private or not published):

```bash
cp -r go-api-base my-project && cd my-project && rm -rf .git .env bin
go mod edit -module github.com/your-user/my-project
grep -rl 'github.com/NoeOnDev/go-api-base' --include='*.go' --include='go.mod' . | xargs sed -i 's#github.com/NoeOnDev/go-api-base#github.com/your-user/my-project#g'
git init
```

Then:
- **Keep `TEMPLATE_VERSION`:** it says which version of the template the project came from. When you bring the changes of newer versions from the [CHANGELOG](CHANGELOG.md) to the project, update it to the last one you applied. That way you always know where to start reading.
- Keep the authentication.
- Keep the examples that look like your project and delete the rest (their package, queries, migrations and registration in `internal/server/routes.go`).
- If you don't need some auth feature (open registration, 2FA, email change…), [docs/auth.md](docs/auth.md#removing-features) says what to delete.
- If you don't need the job queue or real time, [docs/jobs.md](docs/jobs.md#removing-the-queue) and [docs/realtime.md](docs/realtime.md#removing-real-time) explain how to remove them.
- Add your resources following [docs/adding-a-resource.md](docs/adding-a-resource.md).

## Documentation

- [Authentication](docs/auth.md): how it works, how to use it from React and how to remove the features you don't use.
- [Encryption and `ENCRYPTION_KEY`](docs/encryption.md): what it protects, how to store it and how to rotate it.
- [Languages (i18n)](docs/i18n.md): user-facing text in English and Mexican Spanish, how to add a language and the list of error codes.
- [Background jobs](docs/jobs.md): creating jobs, queuing in a transaction, periodic jobs, retries and how to remove the queue.
- [Real time](docs/realtime.md): SSE events from React, topics with permissions, streams for AI agents, multiple instances and how to remove it.
- [Adding a resource](docs/adding-a-resource.md)
- [Design decisions](docs/decisions.md): why it is built this way, how it works with multiple instances and what it does not include.
- [AGENTS.md](AGENTS.md): conventions for coding agents (Claude Code, Codex, Copilot), with an index of where each topic is.
