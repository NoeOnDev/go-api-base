# go-api-base

A complete, production-ready REST API template in Go, for personal, client and company projects. It comes with what almost every API needs already solved: authentication with 2FA, emails, background jobs, real time and security. That way each project starts directly with its own work.

It is complete, but with few dependencies: it favors the standard library and adds only this:
- **PostgreSQL with pgx**
- **migrations with goose**
- **typed queries with sqlc**
- **the River job queue**, also on PostgreSQL
- official packages from the Go team for passwords and request limits
- `kin-openapi`, only in the tests, to check the responses against the contract of the API
- the official AWS SDK, only for keeping the uploaded files in S3 (a project that does not use S3 removes it)

It runs as a single binary, with PostgreSQL as the only service (no Redis or separate queues). It works the same with **one instance or several** behind a load balancer: sessions, per-account limits, the queue and real time are coordinated through PostgreSQL (see [docs/decisions.md](docs/decisions.md#multiple-instances)).

It includes:

- **Authentication** with sessions in an `HttpOnly` cookie, passwords with argon2id and roles: `user`, `admin` and the ones each project creates, with their permissions. It includes a profile, password change, password reset by email, optional email verification, 2FA with an authenticator app and recovery codes, sessions per device (with optional idle expiry), verified email change and account deletion. The first admin is created with a command.
- **Emails** over SMTP (any provider), with editable templates. They go through the queue, so they are retried if they fail. In development, Mailpit or the log is used.
- **Background jobs** with River, on PostgreSQL (no Redis): retries, scheduled and periodic jobs, and queuing inside a transaction. The workers run in the same binary.
- **Real time with SSE:** `GET /events` sends the browser the user's events and those of the topics it listens to (with permission). It also works to stream an AI agent's response.
- **In-app notifications**, stored and sent live. Auth uses them to warn about security changes.
- **File uploads** on the local disk or in S3 (or a compatible service), served through the API with the permission check, with limits per user and a cleanup of the files nobody uses (see [docs/files.md](docs/files.md)).
- **Security:** CSRF protection, configurable CORS, attempt limits (per IP, per account in the login and the 2FA, and per recipient in the emails), argon2 with a concurrency cap, `__Host-` cookies in production, `nosniff` and `no-store` headers, and server timeouts.
- **API basics:**
  - a single JSON error format
  - validation and pagination
  - logs with `slog` and a `request_id` per request
  - embedded migrations applied on startup
  - graceful shutdown
- **User-facing text in several languages:** English by default and Mexican Spanish, chosen with `Accept-Language`.
- **Example resources** to copy:
  - `notes`: each user's data, with attached files
  - `categories` and `products`: a catalog with relationships, filters, sorting and writes that ask for a permission
  - `branches`: business hours in the branch's time zone, with holidays (see [docs/time.md](docs/time.md))
  - `appointments`: bookings within those hours, without double bookings, and a reminder the day before
  - `shifts`: work shifts, also overnight, clocking in and out, and the minutes worked per day
  - `classes`: classes that repeat some days of the week, with a session canceled or moved
- **The contract of the API in OpenAPI** (`GET /openapi.yaml`), to generate the frontend's types. The tests check every response against it, so it does not go out of date (see [docs/openapi.md](docs/openapi.md)).
- **A reference deployment** for a single server: a `Dockerfile`, and Docker Compose with PostgreSQL and Caddy for HTTPS (see [docs/deploy.md](docs/deploy.md)).
- **Quality:**
  - integration tests against PostgreSQL
  - `staticcheck` and `govulncheck`
  - CI with GitHub Actions

## Requirements

- Go 1.27.4 or newer.
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

4. **Fill the database.** The command applies the migrations, asks for a password and creates an administrator (`admin@example.com`), a user (`user@example.com`) and an account with a role of its own (`manager@example.com`, who manages the catalog) with it, plus example data: a catalog, some notes and a branch. It is for development, and running it again adds nothing:

   ```bash
   make seed
   ```

   For only an administrator, with your own email (what production uses): `make create-admin email=admin@example.com name="Admin"`.

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
   curl 'localhost:8080/products?sort=-price'
   ```

## Endpoints

| Method | Path | Access |
|---|---|---|
| `GET` | `/healthz` | Public. `200` if the API and the database respond, `503` if not |
| `GET` | `/openapi.yaml` | Public. The contract of the API, in OpenAPI 3 (see [docs/openapi.md](docs/openapi.md)) |
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
| `GET`, `PUT`, `POST`, `DELETE` | `/admin/users`, `/admin/users/{id}`, `/role`, `/suspend`, `/unsuspend` | Administrators: list, change the role, suspend and delete users (see "Managing users" in [docs/auth.md](docs/auth.md#managing-users)) |
| `GET`, `POST`, `PUT`, `DELETE` | `/admin/roles`, `/admin/roles/{key}`, `/admin/permissions` | Administrators: the roles of the project and the permissions of each one (see "Roles" in [docs/auth.md](docs/auth.md#roles)) |
| `POST` | `/admin/users`, `/admin/users/{id}/invitation`, `/auth/invitation/accept` | Invitations: an administrator creates the account and the person chooses their password (see "Invitations" in [docs/auth.md](docs/auth.md#invitations)) |
| `GET` | `/events?topics=` | With a session: SSE stream with the real-time events (see [docs/realtime.md](docs/realtime.md)) |
| `GET` | `/notifications?unread=` | With a session: the user's notifications and how many are unread |
| `POST` | `/notifications/{id}/read`, `/notifications/read-all` | With a session: mark them as read |
| `POST`, `GET`, `DELETE` | `/files`, `/files/{id}` | With a session: upload a file, and see or delete one's own (see [docs/files.md](docs/files.md)) |
| `GET`, `POST` | `/notes` | With a session; each user sees only their notes |
| `GET`, `PUT`, `DELETE` | `/notes/{id}` | With a session; another user's note responds `404` |
| `GET`, `POST`, `DELETE` | `/notes/{id}/attachments`, `/notes/{id}/attachments/{file_id}` | With a session: the files attached to a note |
| `GET` | `/categories`, `/categories/{id}` | Public |
| `POST`, `PUT`, `DELETE` | `/categories…` | With the `categories.manage` permission (the admin has every one) |
| `GET` | `/products?category_id=&q=&active=&sort=`, `/products/{id}` | Public: visitors only see active products; whoever manages them sees all |
| `POST`, `PUT`, `DELETE` | `/products…` | With the `products.manage` permission |
| `GET` | `/branches`, `/branches/{id}`, `/schedule?from=&to=`, `/status`, `/exceptions` | Public: branches with their weekly hours, the real hours of some days, whether it is open now, and the holidays |
| `POST`, `PUT`, `DELETE` | `/branches…`, `/branches/{id}/hours`, `/branches/{id}/exceptions/{date}` | Admin only |
| `GET` | `/branches/{id}/availability?date=&duration=` | Public: the free slots of a day |
| `GET`, `POST` | `/appointments?from=&to=`, `/appointments/{id}`, `/appointments/{id}/cancel` | With a session; each user sees their appointments (the admin, all) |
| `GET` | `/branches/{id}/appointments?date=` | Admin only: the agenda of a day |
| `GET` | `/shifts?user_id=&from=&to=`, `/shifts/{id}` | With a session; each user sees their shifts (the admin, all) |
| `POST`, `PUT`, `DELETE` | `/shifts…` | Admin only: assign shifts |
| `POST`, `GET` | `/time-entries/clock-in`, `/clock-out`, `/time-entries?from=&to=`, `/time-entries/summary` | With a session: clock in and out, and the minutes worked per day |
| `GET` | `/classes`, `/classes/{id}`, `/classes/{id}/sessions?from=&to=`, `/classes/sessions?from=&to=` | Public: the classes and their sessions, or those of every class |
| `POST`, `PUT`, `DELETE` | `/classes…`, `/classes/{id}/sessions/{date}` | Admin only: the classes, and canceling or moving one session |

Lists accept `?limit=` (from 1 to 100, 20 by default) and `?offset=` (up to 10000), and respond `{"data": [...], "meta": {"limit", "offset", "total"}}`. A huge offset makes the query slow, because PostgreSQL reads and discards the previous rows. A list that can be sorted takes `?sort=` with one of its columns, and `-` in front for descending (`/products?sort=-price`); another value answers `422`.

`/notifications` is paginated **by cursor**, like a "load more": it responds `{"data": [...], "meta": {"limit", "next"}}`, and the next page is `?after=` with the value of `next` (`null` on the last one). There is no total, and every page is just as fast. See when to use each one in [docs/adding-a-resource.md](docs/adding-a-resource.md#lists-by-page-or-by-cursor).

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
| `405` | The route exists, but not with that method |
| `409` | Repeated value, or the resource is in use |
| `413` | The JSON body is larger than 1 MB (a file that is too large answers `422` on `file`) |
| `422` | Validation |
| `429` | Too many attempts |
| `500` | Internal error, without details; the cause is in the log with its `request_id` |
| `503` | The database does not answer (`/healthz`), the API is shutting down or 2FA cannot be used |

Each error also has a specific `code` (`email_taken`, `slug_taken`, `invalid_credentials`…), and `message` comes in the language that `Accept-Language` asks for: English by default, or Mexican Spanish. The full list of codes and how to add a language are in [docs/i18n.md](docs/i18n.md).

## Configuration

Everything is configured with environment variables. [`.env.example`](.env.example) has the full list.

| Variable | Default | What for |
|---|---|---|
| `APP_ENV` | `development` | `production` turns on JSON logs and the `Secure` cookie |
| `HTTP_ADDR` | `:8080` | Server address |
| `DATABASE_URL` | (required) | PostgreSQL connection. The pool has 10 connections (one per core, on a server with more than 10) unless the URL sets another size (`?pool_max_conns=20`), and a statement is cancelled after 10 seconds unless the URL sets another limit (`?statement_timeout=30000`, in milliseconds; `0` turns it off) |
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
| `REQUIRE_ADMIN_TWO_FACTOR` | `false` | Require administrators to turn on 2FA to use the admin routes (see "Roles" in [docs/auth.md](docs/auth.md#roles)) |
| `MAIL_DRIVER` | `log` | `log` (writes the emails to the log) or `smtp` |
| `MAIL_FROM` | `no-reply@localhost` | Sender |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD` | — / `587` | SMTP server details |
| `STORAGE_DRIVER` | `local` | Where the uploaded files are kept: `local` (the `STORAGE_DIR` directory, `storage` by default) or `s3` (see [docs/files.md](docs/files.md#configuration)) |
| `S3_BUCKET`, `S3_REGION`, `S3_ENDPOINT`, `S3_PATH_STYLE`, `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` | | With `STORAGE_DRIVER=s3`: the bucket and its region, the address of a compatible service (R2, MinIO), and fixed credentials if the machine has no role |
| `APP_NAME` | `My App` | Name of the application: the emails carry it on top, and the 2FA authenticator app shows it |
| `DEFAULT_LOCALE` | `en` | Language of the user-facing text when the request does not ask for an available one (see [docs/i18n.md](docs/i18n.md)) |
| `APP_TIME_ZONE` | `UTC` | Time zone of the users without their own, for the dates the API writes for people (emails) and their days (reports, filters). An IANA name like `America/Mexico_City`. The JSON always carries UTC (see [docs/time.md](docs/time.md)) |
| `ENCRYPTION_KEY` | (required) | Key to encrypt the 2FA secret and the emails waiting in the queue. Generate it with `make key` (see [docs/encryption.md](docs/encryption.md)) |
| `ENCRYPTION_PREVIOUS_KEYS` | | Previous keys, only during a rotation |
| `JOBS_MAX_WORKERS` | `10` | Background jobs that run at once (emails have their own queue of 4) |
| `GOMEMLIMIT` | (no limit) | Go's own variable, not the template's. With it, the garbage collector frees memory before reaching that value. In production, around 70% of the server's RAM is a good choice (with 1 GB, `700MiB`) |

Values with spaces go in quotes: `APP_NAME="My App"`. `make` reads `.env` and cuts a value at the first `#`, and it interprets `$`: avoid both characters in the values (for example, in a password).

`ADMIN_PASSWORD` is not part of `.env`: `make create-admin` reads it from the environment if it is set (to create the administrator from a script), and asks for the password in the terminal if it is not. `make seed` does the same with `SEED_PASSWORD`, which can go in the `.env` of a development machine so the seed runs without asking.

### Before production

What to check in the configuration, whatever the server. [docs/deploy.md](docs/deploy.md) has a deployment with Docker Compose that already sets several of these points. If `COOKIE_SECURE`, `TRUST_PROXY` or `MAIL_DRIVER` are wrong in production, or if `FRONTEND_URL` or `MAIL_FROM` still point to `localhost`, the API warns about it in the log on startup.

- **`APP_ENV=production`:** JSON logs and `Secure` cookies with the `__Host-` prefix.
- **HTTPS in front of the API:** the API speaks HTTP, and the proxy or load balancer terminates TLS. Without HTTPS, `Secure` cookies do not travel and nobody can log in.
- **HSTS:** in production with `COOKIE_SECURE=true` the API sends `Strict-Transport-Security: max-age=31536000` on its own responses. The frontend's domain needs it too: set it in the proxy, and add `includeSubDomains` only if every subdomain has HTTPS.
- **`TRUST_PROXY=true` with a proxy in front (the usual way to have HTTPS), and with the API port closed to the internet.** Without it, every request comes from the proxy's IP: all the clients share the per-IP limits (the login allows one attempt every 3 seconds for everyone) and the sessions record the proxy's IP. The API warns at startup. Leave it `false` only if nothing is in front.
  - With the proxy on the same server, make the API listen only there: `HTTP_ADDR=127.0.0.1:8080`, and also `TRUSTED_PROXIES=127.0.0.1`.
  - If your provider has a private network shared between customers, always set the exact IP of the proxy with `TRUSTED_PROXIES`.
  - With a CDN in front of the proxy, add the CDN ranges (for example, [Cloudflare's](https://www.cloudflare.com/ips/)) to `TRUSTED_PROXIES` besides the proxy. The proxy must append its IP to the `X-Forwarded-For` the CDN sends, not replace it. Otherwise, every user going through the same CDN node shares the attempt limits.
- **`MAIL_DRIVER=smtp`** with a real provider, and **`FRONTEND_URL`** with the frontend's domain (the links in the emails point there). With the frontend on another domain, `CORS_ALLOWED_ORIGINS` too. Both with `https://`: the API accepts `http://` without warning, and the links of the emails would travel in the clear.
- **`APP_NAME`** with the application's name: it is on top of every email and in the authenticator app.
- **`STORAGE_DRIVER=s3`** with a private bucket, unless the API runs on a single server whose disk is backed up: with `local`, the uploaded files are in a directory of that server, and several instances would each have their own (see [docs/files.md](docs/files.md#configuration)).
- **Open registration:** anyone can create an account with `POST /auth/register`. If the project is by invitation only, remove it (see [docs/auth.md](docs/auth.md#removing-features)).
- **`REQUIRE_ADMIN_TWO_FACTOR=true`**, unless the project has a reason not to: an administrator account with only a password is the most valuable one to steal.
- **`ENCRYPTION_KEY` stored as a secret and backed up apart from the database:** if it is lost, users have to set up 2FA again (see [docs/encryption.md](docs/encryption.md)).
- **`APP_TIME_ZONE`** with the business's zone if the system is used in a single place (`America/Panama`). For users in several zones, leave `UTC` and have the frontend send each user's (see [docs/time.md](docs/time.md)).
- **`GOMEMLIMIT`** around 70% of the server's RAM.
- **The process manager waits longer than `SHUTDOWN_TIMEOUT`** before killing the process (10 seconds by default): `TimeoutStopSec` in systemd, `stop_grace_period` in Docker Compose. Otherwise, the requests and jobs still running are cut halfway.
- **A direct connection to PostgreSQL**, or PgBouncer in `session` mode. Real time and River listen with `LISTEN`, which does not work with PgBouncer in `transaction` mode.

## Commands

Run `make` to see the full list. The main ones:

| Command | What for |
|---|---|
| `make run` | Starts the API |
| `make check` | gofmt, sqlc up to date, go vet, staticcheck, tests and govulncheck. Run it before each commit |
| `make test-race` | The tests with the race detector (CI runs it on every push) |
| `make sqlc` | Regenerates the database code after changing migrations or queries |
| `make migrate-new name=create_x` | Creates a migration |
| `make seed` | Fills the development database with accounts and example data |
| `make fresh`, `make fresh-test` | Recreates the development database from the migrations and seeds it, or the test one: what an edited migration needs |
| `make create-admin email=…` | Creates an administrator |
| `make key` | Generates a key for `ENCRYPTION_KEY` |
| `make rotate-keys` | Finishes a rotation of `ENCRYPTION_KEY` (see [docs/encryption.md](docs/encryption.md)) |
| `make river-migration version=N` | SQL of a new River migration, when updating it (see [docs/jobs.md](docs/jobs.md)) |
| `make build` | Static binary in `bin/api` |

## Structure

```
cmd/api/             application startup and the create-admin, seed, fresh, generate-key and rotate-keys commands
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
internal/openapi/     the contract of the API (openapi.yaml), served at GET /openapi.yaml
internal/storage/    where the uploaded files are kept: the local disk, S3 or memory (tests)
internal/files/      uploaded files: upload, download with permissions, and cleanup
internal/totp/       TOTP codes for 2FA (RFC 6238)
internal/secretbox/  encryption (AES-256-GCM) and HMAC with a keyring of rotatable keys
internal/db/         connection, migrations, PostgreSQL errors and SQL queries
internal/store/      code generated by sqlc (do not edit)
internal/validate/   shared validation rules
internal/civil/      dates and times without a time zone, and their conversion to instants
internal/notes/      example: each user's data, with attached files
internal/categories/ example: catalog, public reads and admin writes
internal/products/   example: relationships, filters and search
internal/branches/   example: business hours in the branch's zone, with holidays
internal/appointments/ example: bookings within the business hours, with a reminder
internal/shifts/     example: work shifts, clocking in and out, minutes worked per day
internal/classes/    example: classes that repeat, with sessions canceled or moved
internal/testutil/   test helpers
migrations/          SQL migrations
deploy/              reference deployment: Docker Compose, the Caddy configuration and its variables
docs/                guides and decisions
```

## Starting a new project

**With `gonew`** (if the repository is published):

```bash
go install golang.org/x/tools/cmd/gonew@latest
gonew github.com/NoeOnDev/go-api-base github.com/your-user/my-project
cd my-project
sed -i 's#github.com/NoeOnDev/go-api-base#github.com/your-user/my-project#g' sqlc.yaml
```

`gonew` copies the template and changes the module name and every import of the Go files. `sqlc.yaml` names the module too (the `internal/civil` types of the date columns) and `gonew` does not touch it, hence the `sed`: without it, `make check` says `internal/store` is out of date and `make sqlc` generates code that does not build.

**Copying the folder** (if the repository is private or not published):

```bash
cp -r go-api-base my-project && cd my-project && rm -rf .git .env bin
go mod edit -module github.com/your-user/my-project
grep -rl 'github.com/NoeOnDev/go-api-base' --include='*.go' --include='go.mod' --include='sqlc.yaml' . | xargs sed -i 's#github.com/NoeOnDev/go-api-base#github.com/your-user/my-project#g'
git init
```

Then:
- **Keep `TEMPLATE_VERSION`:** it says which version of the template the project came from (see the [CHANGELOG](CHANGELOG.md)).
- Keep the authentication.
- Keep the examples that look like your project and delete the rest. For `notes`, `categories` and `products`: their package, their query (`internal/db/queries`), their migration, their registration in `internal/server/routes.go` and their seeder in `cmd/api/seed.go` (with `notes`, also `00017_create_note_attachments.sql`, the table of its attached files; `seedCatalog` is the seeder of `categories` and `products`, with their role and its account; `TestSeed` in `seed_test.go` has a line per table in `want`, where `users` and `roles` go back to 2); then `make sqlc` and `make check`, which names the catalog keys left unused (delete them from every `internal/i18n/locales/*.json`, and the error codes from the table of [docs/i18n.md](docs/i18n.md#error-codes)) and the routes left in `internal/openapi/openapi.yaml` without code (delete their operations and their schemas). `products` references `categories`: delete it first, or both. The schedule ones have their recipe in [docs/time.md](docs/time.md#removing-the-examples).
- If you don't need some auth feature (open registration, 2FA, email change…), [docs/auth.md](docs/auth.md#removing-features) says what to delete.
- If you don't need the job queue or real time, [docs/jobs.md](docs/jobs.md#removing-the-queue) and [docs/realtime.md](docs/realtime.md#removing-real-time) explain how to remove them.
- If you don't need file uploads, or only S3, [docs/files.md](docs/files.md#removing-file-uploads) says what to delete.
- Add your resources following [docs/adding-a-resource.md](docs/adding-a-resource.md).
- **Rename the databases** in `.env` (`go_api_base` and `go_api_base_test` in `.env.example`): two projects with the same names would share them, and the tests empty theirs.
- **`AGENTS.md` and `CHANGELOG.md` are the template's.** In `AGENTS.md`, replace the note of "What it is" that says the repository is a template, and that its original migrations can be edited, with what the project is; start `CHANGELOG.md` over.

## Documentation

- [Authentication](docs/auth.md): how it works, how to use it from React and how to remove the features you don't use.
- [Encryption and `ENCRYPTION_KEY`](docs/encryption.md): what it protects, how to store it and how to rotate it.
- [Languages (i18n)](docs/i18n.md): user-facing text in English and Mexican Spanish, how to add a language and the list of error codes.
- [Dates, time zones and schedules](docs/time.md): instants in UTC, dates and times without a zone, and how to model schedules.
- [Background jobs](docs/jobs.md): creating jobs, queuing in a transaction, periodic jobs, retries and how to remove the queue.
- [Files](docs/files.md): uploads on the local disk or in S3, using a file in a resource, security and how to remove them.
- [Real time](docs/realtime.md): SSE events from React, topics with permissions, streams for AI agents, multiple instances and how to remove it.
- [Adding a resource](docs/adding-a-resource.md)
- [The contract of the API (OpenAPI)](docs/openapi.md): the frontend's types, how the tests keep the document true, and what to change with the API.
- [Deploying](docs/deploy.md): the image, Docker Compose with PostgreSQL and Caddy, updating, backups, and running it without Docker.
- [Design decisions](docs/decisions.md): why it is built this way, how it works with multiple instances and what it does not include.
- [AGENTS.md](AGENTS.md): conventions for coding agents (Claude Code, Codex, Copilot), with an index of where each topic is.
