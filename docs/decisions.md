# Design decisions

Why the template is built this way. If you change a decision, update this file.

## Standard library first

`net/http` (with Go 1.22's method and parameter routing), `encoding/json`, `log/slog` and `testing` cover what an API needs.
Fewer dependencies means fewer updates, fewer possible vulnerabilities and code that can be understood without knowing a framework.

The template comes with a lot already solved (authentication with 2FA, key rotation, job queue, real time), but it builds it on the standard library: what grows is our own code, which can be read and changed, not the list of dependencies. TOTP, encryption, SSE and SMTP sending, for example, add none.

**Dependencies of the binary:**
- `pgx`: PostgreSQL driver.
- `goose`: migrations.
- `river`: the job queue, on the same PostgreSQL (see "Background jobs with River").
- Four packages from the Go team:
  - `golang.org/x/crypto`: argon2id;
  - `golang.org/x/time`: per-IP request limits;
  - `golang.org/x/term`: reading the password in `create-admin` without showing it;
  - `golang.org/x/text`: choosing the language from `Accept-Language` (see `docs/i18n.md`).

## pgx + sqlc instead of an ORM

- You write plain SQL, and sqlc generates typed Go functions from it. SQL errors show up when generating, not in production.
- There are no hidden queries or unexpected N+1 problems.
- pgx is the most complete and fastest PostgreSQL driver for Go.

## Migrations with goose, embedded and applied on startup

- They are versioned `.sql` files, easy to review.
- They go inside the binary (`embed`): deploying is copying a single file.
- The API migrates on startup, with a PostgreSQL advisory lock so two instances do not migrate at once. It is simple, and deploying needs no separate step.
- sqlc reads the same migrations as its schema, so they are the single source of truth.

## Pinned tool versions in the Makefile

sqlc, goose (the CLI), staticcheck and govulncheck run with `go run package@version`, with the version in a `Makefile` variable (`SQLC_VERSION`, etc.):
- **Nothing to install:** the first time they are downloaded, verified against Go's checksum database, and built; after that they are used from the cache.
- **They add no dependencies to the API's `go.mod`.** sqlc alone pulls in hundreds.
- **They work in projects created with `gonew`.** A separate module (for example `tools/go.mod`) does not work: Go excludes subfolders with their own `go.mod` when packaging the module, so `gonew` does not copy them.

To update a tool, change its version in the `Makefile`.

## A single error format

```json
{"error": {"code": "validation_failed", "message": "...", "fields": {"title": "This field is required."}}}
```

The frontend handles every error the same way. Each error has a specific code (`email_taken`, and not a `conflict` shared by nine cases), which is also the key of its message in the catalogs: that way the frontend can tell the cases apart without reading the text, and the text comes in the request's language (see `docs/i18n.md`). 500 errors do not expose details; the real cause is in the log with its `request_id`.

## Handlers that return an error

`func(w, r) error` plus `httpx.Handle` centralizes error handling: each handler ends with `return err` and no code is repeated to respond with errors.

## Responses separate from the database structs

Each resource defines its response struct. Adding a column to the table does not change the API without you deciding it, and no field is ever exposed by accident (for example, a password hash).

## Cookie sessions instead of JWT

- **Safer for a web frontend:** the `HttpOnly` cookie cannot be read from JavaScript, unlike a JWT stored in `localStorage`.
- **They can be revoked instantly** (logout, password change, blocking a user): deleting the row is enough. A JWT stays valid until it expires.
- **The cost is one query per request** (`GetSessionUser`, by primary key). It is negligible at this scale.
- **The database stores the SHA-256 of the token**, not the token: whoever reads the table cannot use the sessions.

JWT makes sense when several services validate sessions without access to the database, or for third-party APIs. Neither is the case for this template.

**In production, the cookie is called `__Host-session`.** The browser only accepts that prefix if the cookie is `Secure`, has `Path=/` and has no `Domain`, so a subdomain (for example, a compromised one) cannot create or overwrite it. In development it is called `session`, because the prefix requires HTTPS.

**Headers:** every response carries `X-Content-Type-Options: nosniff`, and the `/auth` ones, `Cache-Control: no-store`, because some carry secrets (the 2FA one, the recovery codes). The rest of the API does not carry `no-store`, because a public list may want caching; a resource with personal data can add it with `httpx.NoStore`.

## Emails in the background and over SMTP

- **SMTP with the standard library's `net/smtp`**, instead of a provider's SDK: it works with any provider (Resend, Brevo, Amazon SES, Mailgun, your hosting's mail) and with Mailpit in development, and switching providers is changing four variables.
- **They go through the job queue** (`jobs.MailSender`): the response does not wait for the mail server, and if SMTP fails or the API restarts, the email is not lost. It is retried up to 5 times, with a longer wait each time.
- **The queued email is encrypted** with `secretbox`: the reset ones carry a link with a token, and the queue table keeps finished jobs for 24 hours.
- **At most 4 sendings at once** (the `email` queue): each one opens its own SMTP connection, and providers reject many simultaneous connections.
- **HTML and plain text**, embedded in the binary, one folder per language. HTML email is its own world: tables for the layout, inline styles, and Outlook on Windows, which renders with Word's engine. That is why the layout is not written from scratch: it is the "Basic" one of Postmark's transactional templates (MIT), tested in the usual clients. MJML would make it easier to redesign, but it needs Node to compile and CI to check the output; React Email or Maizzle would bring React or Tailwind only for the emails. The plain-text version goes along, for the clients without HTML and because it helps against spam filters.
- **`html/template` for the HTML**, which escapes the user's data. It removes HTML comments, including Outlook's conditional ones (`<!--[if mso]>`), so the only one the layout needs comes from a function. The `<h1>` and `<p>` of each email get their inline styles after rendering, so the emails are written with plain tags.

## One-time tokens by email

Password reset and email verification use the `user_tokens` table:
- **As with sessions**, only the SHA-256 of the token is stored.
- **They expire** after an hour (reset) or 24 hours (verification).
- **Only the last link works:** asking for a new one invalidates the previous one.
- **They cannot be used twice:** they are validated and deleted in a single statement (`DELETE … RETURNING`), so not even two simultaneous requests can reuse them.
- **They are cancelled when what backed them changes:** a new password deletes the pending 2FA challenges and email change (they were obtained with the previous one), and a new email deletes the reset links sent to the old one.

The links point to the **frontend** (`FRONTEND_URL`), not to the API: the screen takes the token from the URL and calls the API. That way the API stays JSON only.

## What the API reveals about registered emails

- **The login and "forgot my password" do not reveal it:** they respond the same whether the email exists or not, the login computes an argon2 hash anyway to take the same time, and the per-account limit also counts emails that do not exist.
- **Registration and email change do:** an email in use responds `409`. Without it, whoever picks the wrong account or was already registered would not know why they cannot go on, and the email change would never be confirmed. It is the usual practice (Laravel does the same on registration), and registration has its per-IP limit, so it is not useful to check many emails quickly.

With that, someone can find out whether an email is registered, although not through the login. What matters is protected: the password cannot be tried without limit, and the login's timing does not tell one email from another.

If a project needs to hide it completely, registration has to always respond the same ("check your email") and email the owner of the address if they already had an account, and the email change has to accept the request and notify on confirmation.

## Optional email verification

It is off by default (`REQUIRE_EMAIL_VERIFICATION=false`), because many private projects do not need it.

When on, it blocks the protected routes with a `403 email_not_verified` instead of preventing the login. That way the user can log in, see the notice and ask for another link without getting stuck.

## 2FA with our own TOTP

- **TOTP (RFC 6238) implemented with the standard library** (about 100 lines in `internal/totp`, tested with the RFC's official vectors), instead of adding a dependency.
- **The frontend draws the QR code** from `otpauth_url`, because generating the image in Go would need another library.
- **The secret is encrypted with `secretbox`:** AES-256-GCM with a key derived from `ENCRYPTION_KEY` through HKDF. That way, a leaked database backup is not enough to generate codes. The user's id goes as associated data: a secret copied to another row cannot be decrypted.
- **The recovery codes are stored as HMACs** with another derived key, over the code and the user's id. They are random, about 50 bits, so a fast hash is enough if it is keyed: without `ENCRYPTION_KEY` they cannot be brute-forced.
- **Reusing a code is rejected** by storing the last step used, with an atomic `UPDATE … WHERE totp_last_step < $2`.
- **Turning it off asks for a code too**, not only the password: otherwise, with a stolen session and the password, an attacker could remove it and log in later without the phone. A recovery code also works, for whoever lost the phone.
- **The two-step login uses a challenge** in `user_tokens` and an `HttpOnly` cookie, not a token in the response body. The frontend handles no secrets, and the challenge expires in 5 minutes.
- **2FA is opt-in for users, and can be required of administrators** with `REQUIRE_ADMIN_TWO_FACTOR=true`. An admin account with only a password is the most valuable one to steal, but each project decides (ASVS asks for it at level 2), so it is off by default. It is checked in a single place, `User.IsAdmin()`, which the session computes: `RequireAdmin`, the routes with `OptionalUser` and any new check go through it. The last-admin protection counts admins by role, so an admin without 2FA still counts.

## Rotatable keys with an identifier

`secretbox` is a keyring: a current key and the previous ones (`ENCRYPTION_PREVIOUS_KEYS`).

- **Each encrypted value carries the identifier of its key** (a fingerprint derived with HKDF), so the API knows which one decrypts it without trying them all. Laravel, with `APP_PREVIOUS_KEYS`, does try one after another.
- **The rotation is gradual:** each secret is encrypted again when its user uses 2FA, and `rotate-keys` finishes the rest.
- **The report says when an old key can be removed**, so it is not done blindly.
- **The recovery codes store the `key_id` of their HMAC**, because without the original code there is no way to know which key computed it.

An external key service (AWS KMS, Vault) was ruled out: it adds another service, cost and complexity. If a project requires it (for example, for a compliance standard), the change is contained: the master keys that today come from `ENCRYPTION_KEY` would come from the service on startup, and `secretbox` stays the same.

## argon2id for passwords

It is the algorithm OWASP recommends. The parameters (19 MiB, 2 iterations, 1 thread) are the minimum they recommend, chosen so it works well even on a 1 vCPU VPS. On a server with more resources they can be raised in `defaultParams` (`internal/auth/password.go`).

They are stored inside each hash (PHC format), so they can be raised later without invalidating the existing passwords. Each one is hashed again with the new parameters the next time its user logs in (the only moment the API has the password): that login computes one more hash, once.

**Common passwords are rejected** with a list embedded in the binary: the 10,000 most common ones of 8 to 128 characters from SecLists' `100k-most-used-passwords-NCSC.txt` (MIT license), built from real breaches. ASVS asks for at least 3,000 (6.2.4). Checking every password against a breach database, like Have I Been Pwned, needs an external call on each registration, so it is left to each project (see "What it does not include").

**At most one hash per core at a time** (a semaphore in `password.go`). Each hash reserves 19 MiB: without a cap, 200 simultaneous logins took the process from 16 MiB to 2.5 GiB on 2 cores, and on a small VPS the system would have killed it. With the cap, the peak stays around 120 MiB and the logins even finish sooner, because the CPU is no longer split among 200 hashes. If the client leaves while waiting for its turn, nothing is computed.

## CSRF protection with the standard library

`http.CrossOriginProtection` (Go 1.25) rejects the requests that modify data when the browser says they come from another site (`Sec-Fetch-Site`, `Origin`). Together with the `SameSite=Lax` cookie, it replaces classic CSRF tokens without the frontend having to do anything.

The origins of `CORS_ALLOWED_ORIGINS` are considered trusted.

## Attempt limits

They all allow a few attempts in a row and then refill one every so often. There are two kinds, depending on where they keep the count:

**Per IP, in memory** (`httpx.RateLimiter`, a token bucket from `golang.org/x/time/rate`): a general one on every route except `/healthz` (200 in a row, then 10 per second, like Laravel's `throttle:api`), and on the auth routes without a session, a lower one per group (login, registration, 2FA, tokens, emails). The general one keeps an expensive route (a repeated `ILIKE` search, any new route) from depending only on the proxy; `/healthz` is left out because a monitor or a load balancer calls it often from the same IP, and it goes inside CORS, so the preflights do not count. They are generous, because there can be many people behind an IP (an office, CGNAT); they mostly work against someone trying many accounts from a single IP. At first there was a single one for everything, of 5 per minute, and it cut off legitimate users. They are in memory because they are a cheap first barrier: they do not add a database write per request, which is what matters against a burst. With several instances each one counts separately, and that is accepted because they are a coarse barrier. An idle key is forgotten only once its limit would have refilled, and each limiter remembers at most 50,000 keys (about 10 MB): when it is full, it forgets any key to make room, at a constant cost. Whoever gets forgotten only gets their attempts back, which, against an attack from that many IPs, the per-IP limit could not stop anyway; the per-account ones, in PostgreSQL, still apply.

**Per account or per user, in PostgreSQL** (`internal/ratelimit`). These are the ones that stop brute force against an account, so they must apply the same on every instance and not reset with a deploy:

- **Per user, on the sensitive actions with a session** (changing the password, 2FA): there the attack comes from someone with a session, and the IP adds nothing.
- **Per account, on the login:** 10 attempts in a row per email and then one per minute, whatever IP they come from. It also counts emails that do not exist, so as not to reveal which are registered.
- **Per user, on the 2FA code:** whoever reaches that step already has the password and can spread their attempts across many IPs. Besides, each challenge allows 5 attempts (counted in the database, with an atomic `UPDATE`); after that the password must be entered again, and the user is warned by email.
- **Per recipient, on the emails:** dozens of emails cannot be sent to one person from many IPs either. In "forgot my password" the limit is silent, so as not to reveal which emails exist, and it does not invalidate the last link sent.

They use the GCRA algorithm, which gives the same result as the token bucket while storing a single date per key: when it has all its attempts again. Each attempt is a single atomic `INSERT … ON CONFLICT DO UPDATE … WHERE`, so two instances spending at once do not step on each other (`TestLimiterConcurrent` tests it). The key is stored as a SHA-256, because it can be an email, and the hourly cleanup deletes those that already refilled. It costs one write per attempt, which goes unnoticed next to the login's argon2. Redis would do it just as well, but it would be one more service.

The per-account limit has a cost: whoever knows an email can block that login **as long as they keep sending attempts**, and one per minute is enough for them. A password reset resets the login and 2FA limits, but that helps once the attack is over: if it continues, the attacker spends the attempts again. It was accepted because, without the limit, an attack spread over hundreds of IPs tries passwords unchecked, and because the block lasts only as long as the attack.

If a project needs to avoid that block, the usual technique is OWASP's *device cookies*: a device where the user already logged in does not count toward the limit.

The IP is read from `X-Forwarded-For` only with `TRUST_PROXY=true`, and even then **only if the connection comes from localhost or a private network**, which is where the proxy is: on the same server (`127.0.0.1`) or on a Docker network (`172.x`). If the API port were open to the internet by mistake, whoever connects directly cannot make up their IP to dodge the limits. It is the same criterion Rails uses by default. `TRUSTED_PROXIES` sets the exact addresses of the proxy (IPs or CIDR ranges), instead of every private network. It is useful for a proxy on another server, and for providers whose private network is shared between customers of the same data center: there "the private network" includes other people.

`X-Forwarded-For` is walked **from right to left, skipping the trusted proxies**: each proxy appends the IP that connected to it, so the first one that is not a proxy is the client's. With a single proxy it is the last value; with two (Cloudflare in front of Caddy), the last one is Cloudflare's, and taking it would make every user of the same node share the limits. What is left of the client can be written by the client, so it is not used. It is the same algorithm as Caddy's `trusted_proxies_strict` option.

The rule has a limit: it trusts any connection from a private network. With Docker and the port published on every interface, in some setups (for example, IPv6 without `ip6tables`) Docker forwards outside connections with `docker-proxy`, and the API sees them arrive from the gateway (`172.17.0.1`), which is private. To the API, that connection is the same as the legitimate proxy's, so the code cannot tell them apart. That is why the API port must not be open to the internet, and it is best to set the exact IP of the proxy with `TRUSTED_PROXIES`.

IPv6 addresses are grouped per `/64` block: a provider assigns a whole `/64` to each customer, so counting per exact address would allow dodging the limit by rotating addresses.

## The template is not a dependency

`gonew` copies the code, and the projects created are not connected to the template: improving it breaks nothing in them. That is why this repository can be reorganized freely, including the migrations, which represent the initial schema.

The template is versioned with tags (`v0.1.0`, …) and a `CHANGELOG.md`, and the `TEMPLATE_VERSION` file holds the current version. Since it is copied with the rest, each project knows which version it came from, and updates it when it brings in newer changes. Bringing an improvement to an existing project is done by hand, looking at the change in the history. When a version changes the schema, the CHANGELOG has the SQL for the project's new migration.

**The same rule applies in each project:** while it is not deployed, the original migrations are edited (adding an `ALTER TABLE` for each field only clutters the history). After the first deploy, each change goes in a new migration, because the production database already applied the previous ones.

## Example resources

Each example shows something different, and together they cover the most common cases:
- `notes`: per-user ownership.
- `categories`: admin writes, a unique value and no deleting while in use.
- `products`: JOIN, filters, search and an invalid FK.
- `branches`: business hours, wall-clock times in the branch's zone turned into instants (see [time.md](time.md#modeling-schedules)).

In a real project you keep the useful ones and delete the rest.

Someone else's resource responds **404 and not 403**, so as not to reveal that it exists.

## Money in cents

`price_cents bigint` instead of `float` (it has rounding errors) or `numeric` (it maps to an awkward type in Go). The frontend divides by 100 to show it.

## Dates in UTC

Every date is a `timestamptz` and travels in UTC: `"created_at": "2026-10-31T02:00:00Z"`. It does not depend on where the server runs:

- **The PostgreSQL session is in UTC** (the `timezone` startup parameter, unless `DATABASE_URL` sets another one), so what depends on it in SQL (`now()::date`, `date_trunc`, the text of a date) gives the same on any server.
- **Go reads the dates in UTC** (`ScanLocation` in the pool, `internal/db/db.go`), whatever the zone of the process. Without it, the same API answered `-06:00` on a machine in Mexico and `Z` in a container.
- **The binary carries the time zone database** (`time/tzdata`), so the zones work in a container without `/usr/share/zoneinfo`. If the system has it, Go uses the system's, which may be more up to date.

**The frontend shows each date in the user's zone**, with `Intl.DateTimeFormat`. The API does not convert it: the same response works for every user, and the browser has to format it in their language anyway. It is what Laravel does since version 7, which serializes the dates in UTC with `Z`.

## A separate, protected test database

The tests use **only** `TEST_DATABASE_URL`, a database different from the development one (`DATABASE_URL`), so they never touch the data you use to test by hand or show demos.

Since each test empties every table, `testutil.NewDB` refuses to run if the test database does not end in `_test` or if it is the same as `DATABASE_URL`. That way, a mistake when copying variables cannot delete the development database, let alone the production one. It is the same convention Rails and Django use.

## Integration tests in series

The packages share the test database and each test empties it when it starts. That is why `make test` uses `go test -p 1`. At the current scale, all the tests take around a second and a half.

## bigint IDs

`bigint GENERATED ALWAYS AS IDENTITY` works on any PostgreSQL version and is efficient.
If you need unguessable IDs, an alternative is UUIDv7: PostgreSQL 18 generates it natively with `uuidv7()`.

## Logs with slog

In development, readable text is used, and in production JSON, which is what log tools process.
Each line of a request carries its `request_id`, which is also returned in the `X-Request-ID` header. That way, an error a user reports can be found in the logs.

`GET /healthz` is logged at `debug` (with `httpx.Quiet`): a monitor calls it every few seconds, and at `info` it would fill the log with identical lines. If the database does not respond, the `503` is logged as an error, like any 5xx.

## Server timeouts

`ReadHeaderTimeout` 5 s, `ReadTimeout` 15 s, `WriteTimeout` 30 s, `IdleTimeout` 60 s.
Without them, a slow client can keep connections open indefinitely.

**A statement is cancelled after 10 seconds** (`statement_timeout`), unless `DATABASE_URL` sets another limit. `WriteTimeout` cuts the response, but not the query behind it: without the limit, a slow query keeps its pool connection after the request is gone, and a few of them leave the API without connections. It is set with `SET` on each new connection (in `AfterConnect`), not as a startup parameter in the URL, because PgBouncer rejects the parameters it does not know. Whatever legitimately takes longer (a report, a job) raises it only for itself, inside its transaction: `SET LOCAL statement_timeout = '60s'`. The migrations use their own connections without the limit, because an index on a large table can take minutes.

SSE streams have no `WriteTimeout` (it would cut them after 30 seconds), but **each write has a 10-second deadline**: a client that stops reading without closing the connection is disconnected, instead of blocking the handler and keeping its slot.

**On shutdown**, the HTTP server and the background jobs stop at the same time, each one with `SHUTDOWN_TIMEOUT` (10 seconds by default). If a request did not finish in time, it is cut. The process manager must wait a bit longer than `SHUTDOWN_TIMEOUT` before killing the process; some wait only 10 seconds by default.

## Panics

`Recover` logs the panic with its stack and the `request_id`, and responds a `500` with the API's error format. If the response had already started (for example, halfway through an SSE stream), it responds nothing: the JSON would be mixed with what was already sent. It cuts the connection with `http.ErrAbortHandler`.

## Missing routes

Any unregistered route responds 404 in JSON. A method not allowed on an existing route (for example `POST /healthz`) also responds 404, because the `/` catch-all route takes priority over `net/http`'s 405. It is an accepted simplification.

## Go version

`go.mod` requires Go 1.27.3, which includes the October 2026 security fixes (`crypto/x509`, `net/http`, `crypto/tls`).
With `GOTOOLCHAIN=auto` (the default), an older Go version downloads the right one automatically.

## Account features

The ones Laravel Jetstream adds over Breeze: sessions per device, changing the email and deleting the account.

- **Ask for the password** in everything that, with a stolen session, would allow keeping the account or doing damage that is hard to undo: changing the email, deleting the account and closing every other session. Closing a single session does not ask for it.
- **The email changes only once it is confirmed from the new address**, with a notice to the current one: that way a typo does not leave the account inaccessible, and the owner finds out if it was not them.
- **The last activity of each session is stored every 5 minutes**, not on each request: that would be a database write per request. It is enough to show "active a while ago" and for the idle expiry.
- **The only admin cannot be deleted**, with the admins' rows locked in the transaction, so two cannot be deleted at once. The same check protects the last active admin from being demoted or suspended by another admin, and a suspended admin does not count.
- **Administrators can suspend, not only delete.** Suspending is undone with one call and keeps the data, so it is the safe choice for "block this user"; deleting asks for the admin's password, like changing a role, because it cannot be undone and, with a stolen session, a new admin would outlast it. Suspending does not ask for it.
- **An invitation creates the user without a password**, instead of keeping invitations in a table of their own: the link is one more single-use token of `user_tokens`, the email is reserved from the start (registering with it answers `409`), and the admins see the invited users in the same list. Accepting sets the password only if there is none yet (`UPDATE … WHERE password_hash IS NULL`), so an old link cannot overwrite the one set with "forgot my password". Inviting another admin asks for the admin's password, with the per-user limit; inviting users does not, so several can be invited in a row.
- **A suspended account answers `403 account_suspended` only after the right password**, so the response does not reveal which emails are registered, nor which are suspended.

## Background jobs with River

[River](https://riverqueue.com) keeps the queue in PostgreSQL. It was chosen over:
- **Our own queue** (with `SELECT … FOR UPDATE SKIP LOCKED`): it looks simple, but retries, stuck jobs, periodic jobs without duplicates and orderly shutdown are hundreds of lines that are hard to test. River solves it and is a small dependency (it uses the same pgx).
- **Redis** (with Asynq, for example): it adds another service to install, back up and monitor. Besides, PostgreSQL allows queuing inside the same transaction as the data, which Redis cannot do.

Decisions:
- **The workers run inside the API process.** A single binary and a single service; they can be split if a project needs it (see `docs/jobs.md`).
- **Its schema comes in as a goose migration** (exported with its CLI), not with River's migrator: goose is still the single source of the schema.
- **Cleaning up expired sessions and tokens is a periodic job**, instead of being done on each login.
- **River UI**, its web interface, **is not included**: it adds another dependency and needs its own authentication.

## Real time with SSE

Server-Sent Events, with the standard library, instead of WebSockets:
- **They cover the usual cases:** notifications, seeing others' changes live (like Jira), state changes and an AI agent's response while it is generated. In all of them, the browser sends little (over REST) and the server notifies.
- **No dependencies or new protocol:** it is plain HTTP. It uses the same session cookie, goes through the same middlewares and through any proxy, and the browser reconnects on its own.
- **WebSockets are needed** when the browser sends a lot and very often (a collaborative drawing). That case also needs to resolve simultaneous edits, which depends on each project, so they are added there.

Decisions:
- **Topics need an authorizer:** a topic without an authorizer is rejected, so nobody can listen to other people's data by making up a name. A rejected topic does not cut the connection: it is reported in the `ready` event, and the user keeps receiving their channel. That way, if they are removed from a project, they do not lose their notifications and the frontend knows which topic they lost.
- **Lost events are not resent.** On reconnecting, the frontend asks for the state over REST: it is simpler and loses nothing, because the source of truth is the database.
- **A stream does not outlive its session:** it is cut at once with the logout or when auth closes the other sessions (password, 2FA), the session is checked on each heartbeat and it lasts at most 15 minutes. Without this, someone with a stolen session would keep receiving events after the owner resets the password.
- **The stream removes the `WriteTimeout` only for its request**, and on shutdown the API closes them all (`RegisterOnShutdown`); otherwise, the shutdown would wait until the timeout.
- **Limits of 10 connections per user and 1000 in total**, because each one is an open socket and goroutine. Each connection takes about 11 KB.
- **Multiple instances with PostgreSQL's `LISTEN/NOTIFY`**, without Redis: each broker delivers its own at once and notifies the others. A message larger than 8000 bytes (the maximum of a `NOTIFY`) is stored in `realtime_messages`, and its id travels. If the `LISTEN` connection is cut, on reconnecting it cuts the streams, because it may have lost events (see `docs/realtime.md`).

## Multiple instances

The API works the same with one instance as with several behind a load balancer, for example to deploy without downtime. What must apply to all of them goes through PostgreSQL, without Redis:

| What | How it is coordinated |
|---|---|
| Sessions | In the `sessions` table: any instance serves any request, without pinning a user to an instance |
| Per-account and per-user limits | `internal/ratelimit`, in the `rate_limits` table |
| Migrations | An advisory lock: a single instance migrates |
| Jobs | River spreads the queue among the instances, and a single one (the leader) queues the periodic ones |
| Real time | The brokers notify each other with `LISTEN/NOTIFY` (see `docs/realtime.md`) |

Kept in each instance, on purpose:
- **The per-IP limits:** they are a coarse, cheap barrier. With N instances, the effective limit is N times higher.
- **The SSE connection limits** (10 per user, 1000 in total) **and the cap on argon2 hashes** (one per core): they protect each process's resources.

`LISTEN` needs a direct connection to PostgreSQL, or PgBouncer in `session` mode. With PgBouncer in `transaction` mode the notifications do not arrive: neither the real-time ones nor the ones River uses (which in that case needs `PollOnly`).

A rule for new code: **nothing that must apply to every instance is kept in memory** (a counter, a cache, an "already done"). It goes in PostgreSQL.

## What it does not include

- Mandatory 2FA for every user (for administrators there is `REQUIRE_ADMIN_TWO_FACTOR`), and passkeys or WebAuthn.
- Login with Google or other providers (OAuth).
- Permissions finer than `user`/`admin`.
- WebSockets (see "Real time with SSE").
- Checking passwords against breaches (Have I Been Pwned): it needs an external call on each registration and password change. The most common ones are already rejected (see "argon2id for passwords").
- A persistent audit log: the security events go to the logs and to the user's notifications. What to keep, and for how long, depends on each project.
- Metrics (Prometheus or OpenTelemetry): they add dependencies, and each project uses its own monitoring.
- An OpenAPI contract: written by hand it falls out of date. The endpoints are in the README and in `docs/auth.md`.
- Deployment files (Dockerfile, systemd units, proxy configuration): each project deploys differently. "Before production" in the README lists what to check, whatever the server.

It is not a to-do list: they are features that depend on each project, and they are added in the project that needs them.
