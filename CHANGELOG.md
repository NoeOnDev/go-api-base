# Changelog

Version history of the template: what changed in each version. Each project created with `gonew` is an independent
copy, and its `TEMPLATE_VERSION` file says which version it came from. When a template version is published,
`TEMPLATE_VERSION` is updated in the same commit as its section.

Format: one section per version, with what was **added**, **changed** and **fixed**. The template edits its original
migrations, because they represent the initial schema.

## [v0.13.0] - 2026-11-22

The contract of the API in OpenAPI, which the tests keep true, and what a review of the whole template found after the last four versions.

### Added

- **`internal/openapi/openapi.yaml`** describes every route of the API: its parameters, its body and what it answers with each status. The API serves it at `GET /openapi.yaml`, and the frontend generates its TypeScript types from it (see `docs/openapi.md`).
- **The tests check every response against it** (`internal/testutil/contract.go`): each request of `testutil.Do` and `testutil.Upload` must answer a status its operation lists, with a body of the shape it says; and a request the API accepted must be what the document describes. A second test (`internal/openapi`) requires the document to have exactly the routes of the code. A handler that returns a new field, or a new error, fails its own tests until the document says so.
- **A new dependency, only in the tests:** `kin-openapi`, to read the document and validate against it. It does not reach the binary.
- **Tests for the routes the suite did not call through the whole API:** the list of classes and one class, and the roles (`TestRolesMatchTheDocument`).

### Changed

- **"What it does not include" no longer lists an OpenAPI contract** (`docs/decisions.md`). Generating it from the code, and a page of documentation served by the API, are still left out.
- **Inviting someone with a role of the project asks for the administrator's password** (`POST /admin/users`), as inviting another administrator already did: any role other than `user`. With a stolen session, creating a role with every permission and inviting an account with it left that account behind.
- **`files.AttachShared`**, for a file that belongs to its resource and not to who uploaded it (the image of a product). With `Attach`, the file is deleted with its user's account, wherever it is attached; a shared one stays until the resource releases it. The `files` table has a `shared` column.
- **`make fresh` seeds by itself and asks for `SEED_PASSWORD` first:** without it and without a terminal it stops before deleting anything, instead of leaving the database empty.
- **`make fresh` and `make seed` only run against a database of this machine** (`localhost`, a loopback address or a socket), besides refusing in production: a `.env` that points to another database would have deleted it. `DEV_DATABASE_HOST` allows a development database in a container.
- **`GET /admin/roles/{key}`**, which the `Location` of a new role already pointed to.
- **The bodies of `POST /admin/roles` and `PUT /admin/roles/{key}` are their own:** the first one does not take `password` nor the second one `key`, which were ignored.
- **The cleanup job is tried 3 times, not 25,** and has 10 minutes instead of one: the cleanup of the next hour does the same work, and with a store that kept failing the retries piled up for weeks.
- **`testutil.DoWithHeaders`**, for a request with other headers (`Range`, `If-None-Match`) that is also checked against the contract.
- **Updated:** `golang.org/x/crypto` 0.59.0, `golang.org/x/term` 0.48.0 and the AWS SDK's `config` and `credentials`.

### Fixed

- **Deleting an example left `make check` failing.** The test that compares the routes of the code with `openapi.yaml` read the text of the Go files, comments included, and three comments of the base name a route of an example (`"GET /notes/{id}"`). It reads the code now (`go/parser`).
- **Editing a role deleted its permissions the code did not define at that moment** (after a deploy that went back, or with a resource removed for a while), against what `docs/auth.md` says. Only the ones the code defines are replaced.
- **A note deleted while a file was being attached to it left the file in use forever:** it counted for the quota and could not be deleted. Deleting a note locks it before reading its attachments (`LockNote`), and an attachment to a note that is gone answers `404` and releases the file, instead of `500`.
- **The cleanup could delete a file a resource had just taken.** It listed the files nobody used and then deleted them one by one; a form sent in between got its `201` and lost the file. It marks them first, in one statement, and a marked file cannot be taken.
- **`openapi.yaml` against the code,** in what no test exercised: `name` is required in `PUT /auth/me`; `PUT /auth/password` asks for a verified email and can answer `403`; `DELETE /branches/{id}/exceptions/{date}` and `DELETE /classes/{id}/sessions/{date}` answer `422` for a date that is not one; a download answers `206` to a `Range`. Several operations now name their `409` codes, and the document says that a write from another origin answers `403 cross_origin_rejected`.
- **The contract check missed what it could have caught:** the tests of `internal/auth` with their own helpers and the downloads with headers did not go through it, a query parameter the document did not have passed, and so did a `201` without its `Location`.
- **A disk that fails while an upload is written to its temporary file answered `400 malformed_upload`,** without a line in the log. It is a `500`.
- **An upload whose last step failed** (the client left when the bytes had arrived) counted for the quota for a day. It is released at once. The store also has its own time limit, so a bucket that stops answering does not hold the request.
- **The error of a download carried the headers of the file** (`Content-Disposition`, `ETag`): a browser saved the JSON of a `404` as `photo.png`.
- **The local store left its temporary files** when the process died in the middle of an upload. They go under `.tmp/` now, and the startup deletes the old ones.
- **`SEED_PASSWORD` and `ADMIN_PASSWORD` in quotes kept the quotes** as part of the password, with `make` or an env file of Docker. So did `HTTP_ADDR` in the health check of the container, which also asked `127.0.0.1` when the server listened on one address only.
- **`make seed` after a seed that was cut,** or with the categories deleted, failed on the role that was already there or said everything was there without its account.
- **A reminder of an appointment that already started** was sent when its job ran late.
- **The subcommands:** `-h` printed its usage and exited with an error; `api seed -h` seeded and `api fresh test` recreated the development database (an argument they do not take is now an error); `create-admin` asked for the password before finding a mistake in the configuration; Ctrl+C at the password prompt left the terminal without showing what is typed; a second Ctrl+C while the server shut down was ignored.
- **`docs/deploy.md`:** the systemd unit of "Without Docker" left `/tmp` read-only, so every upload failed (`PrivateTmp`, `StateDirectory`); `docker compose up --build` stopped once `go.mod` asked for a newer Go than the server's copy of the image (the `Dockerfile` sets `GOTOOLCHAIN=auto`); the `rsync` of the frontend deleted `deploy/site/.gitignore`; and how to restore the uploaded files was missing. `deploy/.env.example` gains `S3_ENDPOINT`, `S3_PATH_STYLE`, `API_IMAGE` and `GOMEMLIMIT`.
- **Guides that no longer matched the code:** step 5 of `docs/adding-a-resource.md` did not compile for a copy of `notes`; `docs/decisions.md` said a wrong method answers `404` (it is `405`) and that S3 is removed by deleting one file; the recipe to remove the queue deleted the test of the health check, and the ones to remove the roles, 2FA, the files and the OpenAPI contract left a test, a variable of the deployment or the instructions of `AGENTS.md` behind. `AGENTS.md` says what to do without `SEED_PASSWORD`, and that the database tests fail with `CI` set and no test database.
- **The message of `last_admin`** said "you can't delete" also when demoting or suspending the only administrator.

## [v0.12.0] - 2026-11-21

Roles of the project with their permissions, for the systems where `user` and `admin` are not enough, a reference deployment with Docker Compose, and two things every project ends up writing: example data for development, and lists the client can sort. The schema changes: the `roles` and `role_permissions` tables.

### Added

- **Roles of the project, with permissions** (`internal/auth/permissions.go` and `roles.go`; see "Roles" in `docs/auth.md`): besides `user` and `admin`, an administrator creates roles (`cashier`, `auditor`) and gives each one its permissions, from `/admin/roles`. It is the equivalent of `spatie/laravel-permission`, with one role per user.
  - **A permission is a name the code defines** (`products.manage`). A route asks for it with `authn.RequirePermission(PermManage, …)`, and a handler with `user.Can(PermManage)`. The admin role has every one.
  - **`GET /admin/permissions`, and `GET`, `POST`, `PUT` and `DELETE` on `/admin/roles`.** Changing the permissions of a role asks for the administrator's password and applies from the next request of its users. A role in use is not deleted (`409 role_in_use`), and `user` and `admin` are not changed (`409 built_in_role`).
  - **`/auth/me` and the login carry `permissions`**, for the frontend to show or hide what depends on them.
  - **The schema changes:** the new `roles` and `role_permissions` tables, and `users.role` is a foreign key to `roles` instead of a `CHECK`.
  - **`testutil.CreateRole`**, to log in a user with a role of the project in a test.
- **A reference deployment** (`docs/deploy.md`): a `Dockerfile` (the static binary on an image without a shell, as a user without privileges) and `deploy/compose.yaml`, with PostgreSQL and Caddy, which gets the HTTPS certificate. The Caddyfile serves the API under `/api` and the frontend at `/`, on the same domain, and holds the requests while the API restarts, so a deploy does not answer errors. The guide covers the first deploy, updating, logs, backups and running it without Docker, with systemd. The CI builds the image. It was tried on a laptop before it was written down: the login, an upload, the real-time stream and a restart of the API under load.
- **`api healthcheck`** asks the API's own `/healthz`: it is the health check of the container, whose image has no `curl`.
- **`make seed`** (`cmd/api/seed.go`): fills the development database with an administrator (`admin@example.com`) and a user (`user@example.com`), with the password of `SEED_PASSWORD` or the one asked for in the terminal, plus example data: a catalog, some notes and a branch with its hours. It is the equivalent of Laravel's seeders. Running it again adds nothing, and it refuses to run with `APP_ENV=production`. Each resource adds its own seeder (see "Seed data" in `docs/adding-a-resource.md`).
- **`make fresh` and `make fresh-test`** (`cmd/api/fresh.go`): they recreate the development database from the migrations and seed it, or the test one. It is the equivalent of Laravel's `migrate:fresh --seed`, and what the template's way of working needed: its original migrations are edited, and goose does not apply again one it already applied. They refuse to run in production.
- **Sorting a list with `?sort=`** (`httpx.ParseSort`): the name of a column, with `-` in front for descending. It only lets through the columns the handler names, and the query has one `CASE` per order, so a text of the request never reaches the SQL. `GET /products` takes `?sort=` with `name`, `price` or `created_at` (see "Sorting a list" in `docs/adding-a-resource.md`).

### Changed

- **The writes of `categories` and `products` ask for a permission** (`categories.manage`, `products.manage`) instead of an administrator, and the inactive products are seen by whoever manages them: they are the example of `RequirePermission` and `user.Can`. An administrator can do the same as before.
- **`make seed` also creates a role and an account with it** (`catalog_manager`, `manager@example.com`), to see what a permission does.
- **"What it does not include" no longer lists permissions finer than `user`/`admin`** (`docs/auth.md`, `docs/decisions.md`), nor the deployment files. Several roles per user, Kubernetes and a pipeline that deploys are still left out.
- **`docs/auth.md` and `AGENTS.md` say who types the password of each account** ("Roles"): `make seed` and `SEED_PASSWORD` for the throwaway accounts of development, which a coding agent may use; the terminal for a real administrator, which the person creates themselves; and `ADMIN_PASSWORD` only for a deploy script. The commands did not change.
- **"Quick start" in the README uses `make seed`** instead of `make create-admin`, which stays for production and for an administrator with your own email.

## [v0.11.0] - 2026-11-21

File uploads: on the local disk or in S3, with the permission check on every download, limits per user and a cleanup of the files nobody uses. The notes example shows how a resource uses them. The schema changes: the new `files` and `note_attachments` tables.

### Added

- **File uploads** (`internal/files`, see `docs/files.md`): `POST /files` receives a `multipart/form-data` file, `GET /files/{id}` serves it to its owner or an administrator, and `DELETE /files/{id}` deletes one that nothing uses. A resource uses a file with three calls: `Receive` in its upload route, `files.Attach` in the transaction that links it and `files.Release` in the one that deletes it; `Serve` sends a file after the resource's own permission check.
- **Where the files are kept** (`internal/storage`): `STORAGE_DRIVER=local` (the `STORAGE_DIR` directory, for development and a single server) or `s3`, for Amazon S3 and the compatible services (Cloudflare R2, MinIO), with the `S3_*` variables. On AWS, without keys: the SDK takes the role of the machine. The bucket stays private.
- **A new dependency, the official AWS SDK** (`aws-sdk-go-v2`), only for the S3 store: signing the requests by hand would have been security code to maintain against a service that changes. A project that does not use S3 removes it with a short recipe (see "Removing file uploads" in `docs/files.md`).
- **The `files` table** records whose each file is, its name, its type and its state. Its row is created before the upload and deleted after the bytes, so a failure in the middle never leaves an object in the store that the database does not know about.
- **The hourly cleanup deletes the files nobody needs** (`files.Purge`): the released ones, those of a deleted account, and the ones uploaded more than a day ago that no resource took.
- **What keeps an uploaded file from being dangerous:** its type is the one found in its bytes, not the one the request says; only the images are shown in the browser and everything else is downloaded, with `Content-Security-Policy: sandbox`; the bytes are stored under a random key, never under the name sent; and each user has a limit of uploads in a row (30) and of total space (1 GB), in PostgreSQL.
- **Attachments in the notes example** (`internal/notes/attachments.go`, the `note_attachments` table): `POST`, `GET` and `DELETE` on `/notes/{id}/attachments`. Deleting a note releases its files.
- **`testutil.Upload`, `testutil.WithStorage`**, and `testutil.PNG` and `testutil.PDF`, for the tests of uploads. `TestS3Real` tries a real bucket when `S3_TEST_BUCKET` is set, like the SMTP test with Mailpit; `docs/files.md` says how to run it against AWS Emulator, AWS's own local emulator.

### Changed

- **`notes.NewHandler` and `notes.Register` take more arguments** (the pool and the files service; the limit of uploads): the example now has transactions and an upload route.
- **An upload has its own time limit** (5 minutes), instead of the server's `ReadTimeout` and `WriteTimeout`, which are made for JSON requests.

## [v0.10.0] - 2026-11-19

Fixes from a review of v0.9.1. `make check` passes again with Go 1.27.4, a project created from the template can regenerate `internal/store`, and four inputs that answered `500` or a wrong value are fixed. An open tab no longer keeps an idle session alive, a queued email is not lost in the middle of a key rotation, and the jobs that fail are seen in the log. The recipes to remove what a project does not use (auth features, the queue, real time, the examples) were followed again on copies of the project, and what did not work as written is fixed. The schema changes: the appointments get `reminded_at`, and the notifications and the time entries an index each.

### Added

- **`authn.RequireUserWithoutActivity`**: like `RequireUser`, but the request does not count as activity of the session. For what the browser calls on its own (see "Idle expiry" in `docs/auth.md`).
- **The jobs that fail are logged** (`errorLogger` in `internal/jobs`): a failed attempt as a warning and the last one, after which River discards the job, as an error. River only logged them at info.
- **A warning at startup with `TRUST_PROXY=false` in production**: behind a proxy, every client shares the per-IP limits and the sessions record the proxy's IP. The checklist of the README says so too.

### Changed

- **pgx 5.13, River 0.51.2 and goose 3.30.1** (`go.mod` and the `Makefile`). River brings no new migration since 0.49 (its last one is still 008, already in `00008_create_river.sql`); its 0.51.1 stops logging errors when it shuts down in order and makes its listener notice a connection that went silent.
- **Go 1.27.4** (`go.mod`): `govulncheck` reports GO-2026-6859 (`net/http`, `MaxBytesReader`) on 1.27.3, so `make check` and CI failed.
- **A text PostgreSQL cannot store answers `400 invalid_characters`** instead of `500`: the NUL character in a body (`httpx.Decode`) or in a cursor, and NUL or invalid UTF-8 in the path or the query (the new `httpx.RejectInvalidText`, on every route). It happened without a session too: `POST /auth/login` with `\u0000` in the email, `GET /products?q=%00`. `httpx.ValidText` checks a text that comes from somewhere else. The code is in both catalogs and in the table of `docs/i18n.md`.
- **`GET /branches/{id}/appointments` goes by pages** (`?limit=&offset=`, with `meta`): the booked appointments of a day are few, but nothing limits the canceled ones.
- **The responses with a person's data in the examples carry `Cache-Control: no-store`** (`httpx.NoStore`): the notes, the appointments and the agenda, and the shifts and time entries. `AGENTS.md` asked for it and only the notifications did it; `docs/adding-a-resource.md` shows it in its routes.
- **`civil.LoadLocation` rejects the files of the server's zone directory that are not zones** (`localtime`, `posixrules`, `Factory`, and the `posix/` and `right/` copies): `validate.TimeZone` accepted them, and a name stored from one would not load on another server.
- **`make test-race`**, the tests with the race detector, which CI now runs after `make check`. The broker and the rate limiters share state between goroutines, and nothing checked it. It found one race, in a test: `TestTimeZoneUTC` changed `time.Local` while the pool read the clock from its own goroutines, and failed about once in 150 runs. The test no longer changes it.
- **`PUT /auth/me` goes back to the default with an empty value:** `"locale": ""` and `"time_zone": ""` set them to `null` (the default language, the application's zone). Before, once chosen they could only be changed for another.
- **`405 method_not_allowed`, with the `Allow` header,** when the path exists with another method. Before it was `404 route_not_found`, which said that the route did not exist.
- **Changing the password also cancels a reset link asked for before** (`savePassword`), like it already did with the pending email change.
- **With `REQUIRE_EMAIL_VERIFICATION`, `OptionalUser` treats a user who has not verified their email as anonymous**, in line with `RequireUser`, which does not let them in.
- **`/healthz` asks the database at most once per second:** it has no rate limit, and each call took a connection of the pool.
- **`realtime.Stream.Close`**, to call with `defer` after `Open`: it gives the end of the response a deadline of its own (see "Fixed").
- **`Broker.Publish` encodes the event once, when publishing**, instead of once per connection. One that cannot be encoded is not published, and is logged as an error.
- **The hourly cleanup runs every deletion even if one fails**, and returns the errors together: a step that kept failing left the ones after it without running. The notifications also get the index that its deletion uses (`notifications_read_at_idx`): without it, it read the whole table every hour, and the unread ones are never deleted. `rate_limits.tat` stays without one on purpose, and its migration says why: the table only holds the keys of the last hours, and the index would be updated on every attempt.
- **The appointments example has a limit per person and a horizon** (`maxUpcoming` and `maxDaysAhead` in `internal/appointments`): at most 5 booked appointments that have not started (`409 too_many_appointments`), counted inside the transaction with the user's row locked, and none beyond 90 days (`422`, in the booking and in the availability). Before, one account could book every free slot of a branch, for any date.
- **`branches.Merge`** joins the openings that touch or overlap. `StatusAt` already did it; the slots of the appointments use it now.
- **The tests of the base no longer use the example resources**, so `notes`, `categories` and `products` can be deleted as the README says: the tests of `internal/auth` use routes of their own (`protected` in `helpers_test.go`, with `RequireUser`, `RequireAdmin` and `OptionalUser`), those of `internal/server` use `/auth/me`, and `TestTranslateDatesAndTimes` (`internal/i18n`) a text of its own instead of the appointment reminder.
- **What the invitations and the user management share is in `account.go`** (`roles`, `oneOf`, `AdminUser`), and in the tests, in `helpers_test.go`: removing one of the two no longer breaks the other. `TestTimeZoneInInvitations` and `TestLockedNoticeInTheUserZone` moved to the tests of their feature, and `admin_two_factor_test.go` is now `two_factor_admin_test.go`, so the `two_factor*_test.go` of the recipe includes it.
- **`make check` warns when `TEST_DATABASE_URL` is not set**, like `make test`: without it the integration tests are skipped and everything looked fine.
- **A password reset no longer resets the user's 2FA attempt limit** (`internal/auth`): whoever controls the inbox could reset the password again and again and get 10 more tries at the code each time, about three times what the limit allows, exactly when 2FA is the last barrier. The login limit is still reset. If someone with the old password used up the 2FA attempts, one is back within 6 minutes.
- **`civil.ParseDate` accepts the years 0001 to 9998**, in a body and in the URL (see "In a request body" in `docs/time.md`).

### Fixed

- **"Starting a new project" left `sqlc.yaml` with the template's module** (README): `gonew` and the copy recipe only changed the Go files and `go.mod`, so the new project failed `make check` at the sqlc step and `make sqlc` generated code that did not build. Both recipes now change `sqlc.yaml` too.
- **`PUT /classes/{id}/sessions/{date}` answered with another session** when the session was moved onto a day that has its own (`internal/classes`): the change was stored, but the response was the session of that day, with `moved: false`.
- **`GET /branches/{id}/schedule?from=9999-12-31` answered `500`** (a panic when encoding an instant of the year 10000), like any date at the edge of the calendar in the examples. Now it is `422` on the date.
- **An open tab kept an idle session alive**, against what `docs/auth.md` said: `GET /events` is opened again by the browser every 13 to 17 minutes, and each time counted as activity, so `SESSION_IDLE_TIMEOUT` never arrived. The route now uses `RequireUserWithoutActivity`.
- **`GET /events?topics=` with a very long list** took seconds of CPU looking for repeats before answering `400 too_many_topics` (`internal/realtime`). It is rejected at the first topic over the maximum.
- **A queued email was lost in a key rotation with several instances**: the instance not restarted yet cancelled the emails encrypted with the new key, with nothing in the log above debug. An email whose key the instance does not have is retried now, and one that cannot be decrypted (damaged) is cancelled with an error in the log. `docs/encryption.md` explains it.
- **The slots of the appointments (`internal/appointments`):** on the night the clocks go back, the hour that happens twice was listed twice and its second time could not be booked (now only the first has slots); two openings that overlap repeated their slots, and two that touch at midnight did not offer the appointment that crosses it.
- **The overlap searches read all the history** (`ListBookedRanges`, behind the public availability, and `ListShifts`): they compared `starts_at` and `ends_at` one by one, which no index serves. They use `&&` on the range now, like their `EXCLUDE` constraint, and its index. The time entries of some days (`ListTimeEntries`) had the same problem with the history of the person: they have an index on their range now (`time_entries_range_idx`).
- **Reassigning a shift left the previous person's time entries pointing at it** (`internal/shifts`): they saw a `shift_id` that answered them `404`. `PUT /shifts/{id}` with another `user_id` detaches them, in the same statement.
- **A branch or a user deleted in the middle of a request answered `500`** in `POST /appointments`, `POST /shifts` and `PUT /shifts/{id}`: now `422` on `branch_id` or `user_id`, like `products` does with its category.
- **The appointment reminder was sent twice if its job ran twice** (a retry, or a job River rescues): its comment said that reading the appointment again made a second run harmless, and it did not. The new `appointments.reminded_at` column records that it was sent, and the worker checks it first. It notifies and then records, so a process that dies between the two still repeats it: for a reminder that is better than not getting it.
- **`POST /auth/email/verify` used up the link even if marking the email failed:** the two steps go in a transaction now, like in the other links.
- **`POST /admin/users/{id}/invitation` answered `429` without `Retry-After`**, and sent a new link to a suspended account, which could not accept it: now `409 user_suspended`, without an email.
- **`docs/decisions.md` said that "forgot my password" does not reveal which emails exist:** its response does not, but its time is not evened out. It says so now, and how to even it out.
- **An email was sent again if the goodbye to the mail server failed** (`internal/mail`): the server had already accepted it, but `Send` returned the error of the `QUIT` and the worker retried, up to five times.
- **A stream ended with the connection cut** instead of a clean end (`internal/realtime`): each write left a deadline of 10 seconds behind, already past when the handler returned, so the server could not write the end of the response. The browser reconnected anyway, but it and the proxy logged an incomplete response, and a `fetch` reading a streamed answer got an error instead of the end.
- **A stream whose session had just been closed could still send the next event** (half of the times, if it was busy at that moment): for example, the "your password was changed" notice to the session that had been stolen. A closed connection gets no more events.
- **One event that could not be encoded closed every connection of its channel**, with nothing in the log.
- **A number in the text of a notification was written like `1.234567e+06`** (`internal/notifications`): the data was decoded as `float64`. No text of the template had one yet.
- **The jobs were not stopped if the server could not start** (the port in use): one cut halfway was retried an hour later.
- **A long subject went in a single line of the email**, of any length; now it is folded at its spaces (`internal/mail`).
- **`ErrTooManyConnections` had its message in Spanish.**
- **The documentation of the queue and of real time said less than what happens:** a job at a fixed hour is skipped if no instance is running at that minute; each instance keeps two connections outside the pool, to listen; River's maintenance has the limit of 10 seconds per statement too; `Publish` waits for one statement with several instances; and any role of the database can listen to the `realtime` channel (`docs/jobs.md`, `docs/realtime.md`). The comment of `DailyAt` said that an hour that does not exist "runs when the clock jumps": it runs as much later as the jump lasts.
- **"Removing features" in `docs/auth.md`:** removing the invitations, the user management or 2FA did not compile (their tests and helpers depended on each other); the 2FA row did not say that `REQUIRE_ADMIN_TWO_FACTOR` must stay off, nor the registration one that `failingMailer` is left unused; and no recipe said that `make check` names the catalog keys left unused. The user management row lists its routes one by one.
- **"Removing the queue" in `docs/jobs.md`** left `testutil.WithJobs` without the field it sets, and did not say that, with the emails sent inside the request, the response time of "forgot my password" tells registered emails apart.
- **"Removing real time" in `docs/realtime.md`**, in its two recipes, did not list the catalog keys to delete.
- **"Creating a job" in `docs/jobs.md`** put the job in `internal/jobs` and queued it from a resource, which is an import cycle in the tests. A job that a resource queues lives in the resource's package, like the appointment reminder; and a queued job is tested with `testutil.WithJobs`, as `AGENTS.md` said.
- **`docs/encryption.md` said the key protects only 2FA:** it also encrypts the emails waiting in the queue, and what a leak of the database and the key gives says so.
- **Smaller things the documentation had wrong or missing:** the routes of the "Tokens" and "Account" limits and the `/accept-invitation` screen (`docs/auth.md`); what `last_admin` and `too_many_emails` answer to (`docs/i18n.md`); the fourth security notification, `email_changed` (`docs/realtime.md`); the `413` and `503` statuses, the size of the pool on a server with many cores, `ADMIN_PASSWORD`, that `make` cuts a value of `.env` at a `#`, and in "Starting a new project", how to delete `notes`, `categories` and `products`, and that the databases, `AGENTS.md` and `CHANGELOG.md` of the new project are still the template's (README); three more points in "Before production" (`https://` in the URLs, open registration, the wait of the process manager); and two comments that cited a section of `docs/decisions.md` by its old name.
- **The 2FA address was cut by an `APP_NAME` with `&`, `+` or `:`** (`totp.URI`): with "R&D Portal" the authenticator app read the issuer "R". Every reserved character is encoded now, in the name and in the email.

## [v0.9.1] - 2026-10-31

Fixes from a review of v0.9.0. Reassigning a shift no longer moves it to another person's zone, two foreign keys of the examples get their index, and the appointment reminder says the day instead of "tomorrow", with dates and hours written in the reader's format inside the texts. The schema changes: two new indexes in the original migrations `00013` and `00014`.

### Added

- **Dates and hours inside a text:** `{name:date}` and `{name:time}` write a value that comes as text (`"2026-11-05"`, `"14:30"`) in the reader's format, and `i18n.Translator.FormatTime` writes an hour with the new `format.time` of the catalogs (see "Dates and times" in `docs/i18n.md`).
- **`httpx.PathDate(r, "date")`** reads a date of the route (`{date}`) and answers `422` on its name, like `httpx.PathID` with the IDs. `branches` and `classes` used their own copy.

### Changed

- **The appointment reminder says the day:** "Reminder: you have an appointment at Clinic on Nov 5, 2026 at 10:00 AM", instead of "Tomorrow…". The text of a notification is built when it is read, so "tomorrow" was wrong if it was read another day. Its data carries `date` besides `time`.
- **`docs/time.md` says that changing a branch's hours does not touch the booked appointments**, and what a real project can do.
- **"In the URL" in `docs/time.md`** shows `httpx.ParseDateRange`, which every example uses for `?from=&to=`, instead of reading each date by hand.

### Fixed

- **`PUT /shifts/{id}` with another `user_id` reassigns the shift** (`internal/shifts`). Before, the shift stayed with its person but took the zone of the other one, so its instants moved. If it overlaps a shift of the new person, `409 shift_overlap`.
- **`docs/i18n.md` still said that the dates of the emails use a fixed format in UTC** ("Limits"), which v0.9.0 changed.
- **Two foreign keys of the examples had no index**, against the rule of `docs/adding-a-resource.md`: `appointments.branch_id` (the index of the constraint only has the booked appointments; now `appointments_branch_id_idx`, which also serves a branch's agenda) and `time_entries.shift_id` (`time_entries_shift_id_idx`). The original migrations `00013` and `00014` change.
- **A user's time zone that does not load** is logged as a warning in `internal/shifts` too, like in `internal/auth`, before using the application's.

## [v0.9.0] - 2026-10-31

A base for dates, time zones and schedules (see `docs/time.md`). The dates of the JSON always travel in UTC, whatever the zone of the server, and the frontend shows them in the user's zone. Each user can have a time zone, and the application has one for the rest (`APP_TIME_ZONE`), for the dates the API writes for people and for their days. `internal/civil` adds dates and times without a zone, with the daylight saving time rule of Temporal and java.time, and the periodic jobs can run at an hour of the clock. Four new examples model the usual schedules: business hours, appointments, shifts and repeating classes. The schema changes: `users.time_zone`, and the tables of the examples (`00012` to `00015`, with the `btree_gist` extension).

### Added

- **Dates and times without a zone** (`internal/civil`, see `docs/time.md`): `civil.Date` (`"2026-10-31"`), `civil.Time` (`"09:30"`) and `civil.DateTime` (`"2026-11-05T10:00"`), with JSON, the `date`, `time` and `timestamp` columns (sqlc generates them, and their zero value is `NULL`), and the conversion to instants in a zone (`In`, `Date.Start`, `DayRange`) with the daylight saving time rule of Temporal and java.time. `civil.LoadLocation` loads IANA zones.
- **The time zone of each user and of the application** (see `docs/time.md`): the new column `users.time_zone` (an IANA name, `null` for the application's), accepted as `time_zone` by `POST /auth/register`, `PUT /auth/me`, `POST /admin/users` and `POST /auth/invitation/accept`, and returned with the user; the variable `APP_TIME_ZONE` (`UTC` by default); and `auth.User.Location()`, the user's zone for their days.
- **Dates written for people:** `i18n.Translator.FormatDateTime` and `FormatDate`, with the Go layouts `format.datetime` and `format.date` of each catalog.
- **Periodic jobs at an hour of the clock** (see "Periodic jobs" in `docs/jobs.md`): `jobs.DailyAt(hour, minute, loc)` and `jobs.WeeklyAt(weekday, hour, minute, loc)`, which keep the hour after a daylight saving time change. `jobs.Deps.TimeZone` is `APP_TIME_ZONE`.
- **A guide to dates, time zones and schedules**, `docs/time.md`: the three kinds of time values, what the frontend does, the cases of a SaaS in several countries and of a system for a single country, SQL recipes to filter and group by someone's days, and how to model business hours, appointments, shifts and repeating classes.
- **Business hours example** (`internal/branches`, see "Example: business hours" in `docs/time.md`): branches with their zone, weekly hours (split shifts and ranges that cross midnight), exceptions per date, the real hours of some days in local time and instants, and whether a branch is open now. New codes `branch_not_found`, `branch_in_use` and `branch_exception_not_found`, and the validation keys `validation.overlapping_hours`, `validation.same_as_start`, `validation.closed_without_hours`, `validation.too_many_items`, `validation.before_start` and `validation.range_too_long`.
- **Appointments example** (`internal/appointments`, see "Example: appointments" in `docs/time.md`): the free slots of a day within the business hours of a branch, booking (`POST /appointments`) with the hour of the branch's clock, an `EXCLUDE` constraint against double bookings (`409 slot_taken`, with `db.IsExclusionViolation`), the user's appointments by days of their zone, canceling, the admin's agenda of a day, and a reminder the day before (a job queued in the booking's transaction that sends a notification). The migration installs the `btree_gist` extension. New codes `appointment_not_found` and `slot_taken`; `jobs.Deps.Notifier` and `testutil.WithJobs` are new.
- **Shifts example** (`internal/shifts`, see "Example: shifts" in `docs/time.md`): shifts assigned by an administrator with the hours of the clock of the place (also overnight, at most 24 hours), an `EXCLUDE` constraint so a person has no two overlapping shifts (`409 shift_overlap`), clocking in and out with one open entry per person, and the minutes worked per day of the worker's clock. New codes `shift_not_found`, `shift_overlap`, `already_clocked_in` and `not_clocked_in`.
- **Repeating classes example** (`internal/classes`, see "Example: repeating classes" in `docs/time.md`): a class is a rule (weekdays, an hour of the clock of its zone, the first and the last day) whose sessions are computed for the days asked for, keeping the hour across daylight saving time changes; only the changes to one session (canceled, or moved to another hour or day) are stored. The sessions of a class, and the agenda of every class. New codes `class_not_found` and `class_change_not_found`.
- **`httpx.ParseDateRange`** reads `?from=&to=` as a range of days, with a default and a maximum length.
- **Validation rules** `validate.Civil`, `validate.RequiredCivil` and `validate.TimeZone`, with the keys `validation.date`, `validation.time`, `validation.datetime` and `validation.time_zone`. A date in a wrong format arrives invalid instead of failing when decoding, so the validation answers 422 on its field.

### Changed

- **The "sign-in attempt blocked" email shows the time in the user's zone and language** (`31/10/2026 00:56 (America/Mexico_City)` in es-MX), instead of in UTC.
- **Dates always in UTC** (see "Dates in UTC" in `docs/decisions.md`): the pool opens the PostgreSQL session in UTC (unless `DATABASE_URL` sets `timezone`) and reads every `timestamptz` in UTC, so the JSON carries them with `Z` whatever the zone of the server. Before, a server with `TZ=America/Mexico_City` answered `-06:00`.
- **The binary carries the time zone database** (`time/tzdata`), so the zones work in a container without `/usr/share/zoneinfo`.
- **The recipes to remove the queue and real time** (`docs/jobs.md`, `docs/realtime.md`) start by removing the appointments example, which uses both; "Removing the examples" in `docs/time.md` has the recipe of each schedule example.

### Fixed

- **Text left in Spanish:** a comment and the error `TRUSTED_PROXIES requires TRUST_PROXY=true` in `internal/config`, and a test literal.

## [v0.8.0] - 2026-10-30

Administrators can manage users (list, change the role, suspend, delete) and invite them, so a project can work without open registration. Emails go out in HTML, with Postmark's Basic layout, besides plain text. Notifications are paginated by cursor, with helpers for any list. The schema changes: `users.suspended_at`, `users.password_hash` accepts `NULL`, and `user_tokens` has the `invitation` purpose.

### Added

- **User management for administrators** (`internal/auth/admin_users.go`, see "Managing users" in `docs/auth.md`): `GET /admin/users` with search, role and status filters; `GET /admin/users/{id}`; `PUT /admin/users/{id}/role`; `POST /admin/users/{id}/suspend` and `/unsuspend`; and `DELETE /admin/users/{id}`. Changing the role and deleting ask for the admin's password. An admin cannot act on their own account (`409 own_account`), and the last active admin cannot be demoted, suspended or deleted (`409 last_admin`).
- **Suspended accounts:** the new `users.suspended_at` column. A suspended account cannot log in (`403 account_suspended`, only after the right password), its sessions, streams and pending links are cut, its session cookie stops working even if one is left, and "forgot my password" sends it nothing.
- **New codes:** `user_not_found`, `account_suspended`, `own_account`, and the validation key `validation.one_of`.
- **Invitations** (`internal/auth/invitations.go`, see "Invitations" in `docs/auth.md`): `POST /admin/users` creates the user without a password and sends a link valid for 7 days; `POST /auth/invitation/accept` sets the password, verifies the email and starts the session; `POST /admin/users/{id}/invitation` sends a new link. Inviting another admin asks for the admin's password. The invited users appear in the admins' list with `status: "invited"`, cannot log in, and do not count as admins. `users.password_hash` accepts `NULL`, `user_tokens` has the `invitation` purpose, the code `invitation_accepted` is new, and the emails show long durations in days (`duration.days`).
- **Pagination by cursor** (`internal/httpx/cursor.go`): `ParseCursorPage` (`?limit=` and `?after=`), `DecodeCursor`, `EncodeCursor` and `NewCursorList`, with the response `{"data": [...], "meta": {"limit", "next"}}`. The cursor is opaque (JSON in base64url), and an invalid one answers `422` with `validation.invalid_cursor`. "Lists by page or by cursor" in `docs/adding-a-resource.md` explains when to use each one.
- **HTML emails** (see "Emails" in `docs/auth.md`): every email goes out as `multipart/alternative`, with its plain-text version and its HTML one. Each email has `<name>.txt` (with the subject) and `<name>.html` (`preheader` and `content`) per language. The HTML goes inside a shared layout, `internal/mail/layout/`: the "Basic" layout of Postmark's transactional templates (MIT, with its license next to it), with the app name on top, a button that works in every client, a footer and dark mode. `mail.Templates` checks that every email has both versions in every language, and the layout texts come from the catalogs (`email.button_help`).

### Changed

- **The last-admin check counts only active admins** (not suspended), and is shared by `DELETE /auth/me` and the admins' actions (`checkNotLastAdmin` and `deleteUser`, in `internal/auth/account.go`).
- **`GET /notifications` is paginated by cursor**, like a "load more": `?after=` with the `meta.next` of the previous page, instead of `?offset=`, and without `total` (`unread` stays). Every page is just as fast, and a notification that arrives meanwhile does not shift the pages.
- **`checkToken` and `lookupLocale`** moved to the shared files of `internal/auth` (`tokens.go` and `account.go`), because invitations use them too.
- **`db.EscapeLike`** escapes the `ILIKE` wildcards for every package; `internal/products` used its own copy.
- **`mail.NewTemplates` takes the brand** (`mail.Brand`, the app name and its URL), and **`Render` takes the email's name without extension** (`"password_reset"`) and returns its subject, text and HTML (`mail.Rendered`). `mail.Message` has an `HTML` field. The `make check` key scan also reads the HTML templates.

### Fixed

- **Text left in Spanish** that the earlier scans missed because it has no accents: a log line (`panic recuperado` is now `panic recovered`), a few internal errors, a comment and several test literals.

## [v0.7.0] - 2026-10-29

Security and robustness: common passwords are rejected, statements have a time limit, HSTS goes out in production, every route has a per-IP limit, turning off 2FA asks for a code, and administrators can be required to use 2FA.

### Added

- **Common passwords are rejected** (ASVS 6.2.4): registration, password change and reset reject the 10,000 most common passwords of 8 to 128 characters, ignoring letter case, with `422` and `validation.common_password` on `password`. The list comes from SecLists (`100k-most-used-passwords-NCSC.txt`, MIT license) and is embedded in the binary: `internal/auth/passwords/common.txt`, with its license next to it.
- **A statement is cancelled after 10 seconds** (`statement_timeout`), so a slow query does not keep its pool connection after the request gave up. `db.Open` sets it on each new connection with `SET` (it works with PgBouncer), unless `DATABASE_URL` sets another limit (`?statement_timeout=30000`, or `0` to turn it off). Whatever needs longer raises it in its own transaction with `SET LOCAL statement_timeout`. The migrations open their own connections without the limit.
- **HSTS:** in production with `COOKIE_SECURE=true`, every response carries `Strict-Transport-Security: max-age=31536000`, so the browser only talks to the API over HTTPS. It does not include subdomains; the proxy can add it for the whole domain (see "Before production" in the README).
- **Mandatory 2FA for administrators, optional:** with `REQUIRE_ADMIN_TWO_FACTOR=true` (new, `false` by default), an admin without 2FA gets `403 admin_two_factor_required` on the admin routes, and `User.IsAdmin()` is `false` for them, so the routes with `OptionalUser` do not show them the admin-only data either. The account routes keep working, so they can turn it on.
- **A general per-IP limit on every route** except `/healthz`: 200 in a row, then 10 per second (like Laravel's `throttle:api`), adjustable in `internal/server/routes.go`. An expensive route, like a repeated search, or any new route, no longer depends only on the proxy. It goes inside CORS, so the preflights do not count; the auth routes keep their own, lower limits.
- **The per-IP limiters remember at most 50,000 keys** (`maxVisitors` in `internal/httpx/ratelimit.go`, about 10 MB each): when full, they forget any key to make room. Before, the map only shrank with the cleanup every minute.

### Changed

- **Turning off 2FA asks for a second factor:** `POST /auth/two-factor/disable` takes `{password, code}` or `{password, recovery_code}`, and responds `409 two_factor_not_enabled` if it is not on. With only the password, whoever had a stolen session and the password could turn it off and log in later without the phone. The wrong codes count toward the per-user 2FA limit of the login.
- **`User.IsAdmin()` means "can act as an administrator":** the admin role and, with `REQUIRE_ADMIN_TWO_FACTOR`, 2FA. Check admin permissions with it, never by comparing `Role`.
- **The `internal/notes` tests use `testutil.NewAPI`, `Do` and `Decode`**, like the other resources and as `AGENTS.md` asks. `notes` is the model for each user's data, so whoever copies it now copies that style.
- **`httpx.SecurityHeaders` takes whether to send HSTS:** `httpx.SecurityHeaders(d.Config.IsProduction() && d.Config.CookieSecure)` in `internal/server/routes.go`.
- **"What it does not include" lists more of what the template leaves out on purpose** (`docs/decisions.md` and `docs/auth.md`): checking passwords against breaches (Have I Been Pwned), a persistent audit log, metrics, an OpenAPI contract and deployment files, each with its reason. That way an agent does not propose them as if they were missing.

### Fixed

- **Text left in Spanish after v0.6.0:** the log line when migrating (`esquema actualizado` is now `schema up to date`), a test error message and a few test literals.

## [v0.6.0] - 2026-10-29

The API works with multiple instances, user-facing text comes in English or Mexican Spanish depending on the request's language, each error has a specific code, everything internal and the documentation are in English, and the documentation has an index for agents. The schema changes: `users.locale`, and the `rate_limits` and `realtime_messages` tables.

### Added

- **User-facing text in several languages** (see `docs/i18n.md`): English by default and Mexican Spanish, with "tú".
  - **`internal/i18n`:** one JSON catalog per language (`locales/en.json`, `locales/es-MX.json`), embedded. The available languages are the files that exist, and all of them must have the keys of `en.json`. Adding a language is adding files.
  - **Each request's language comes from `Accept-Language`**, with `golang.org/x/text/language` (it was already an indirect dependency and becomes a direct one). A language without its own catalog falls back to the closest one (`es-AR` or `es-ES` get `es-MX`); if there is none, `DEFAULT_LOCALE` (new, `en` by default). The responses carry `Vary: Accept-Language`.
  - **What is translated:** the error and validation messages, the success messages, the emails (with their subject) and the notifications. Logs, internal errors and command output are not translated.
  - **Each user has a language** (`locale`, new in `users` and in the API's user). Registration stores the one the browser asked for, `PUT /auth/me` changes it, and the emails and the live event of the notifications reach the user in that language, not in that of whoever made the change.
  - **Emails by language:** `internal/auth/emails/<language>/*.txt`, each one with its subject in `{{define "subject"}}`. `mail.Templates` loads them and requires the same files in every language.
  - **Notifications do not store their text:** the kind and its data are stored, and `message` is built on read, in the reader's language.
  - **`testutil.DoWithLanguage`**, to test the responses in another language.
  - **`make check` requires the code and the catalogs to match:** every code or key in use exists in the catalogs, no unused keys are left, and each error code is in the table of `docs/i18n.md`.
- **Multiple instances of the API.** What had to apply to all of them and lived in memory now goes through PostgreSQL (see "Multiple instances" in `docs/decisions.md`):
  - **`internal/ratelimit`:** attempt limits stored in the `rate_limits` table, with the GCRA algorithm (one date per key and a single atomic statement per attempt). The login (per account), 2FA (per user), the emails (per recipient) and the account actions use them. They now apply to every instance and are not reset on a deploy. The per-IP limits stay in memory.
  - **The real-time broker spreads the events among the instances** with `LISTEN/NOTIFY` (`Broker.Listen`, called by `main.go`), and the session closings too: a logout on one instance cuts the streams of that session on the others at once. A message larger than 8000 bytes is stored in the `realtime_messages` table, and its id travels. If the `LISTEN` connection is cut, it reconnects and cuts the open streams, so the browser asks for the state.
- The hourly cleanup also deletes the keys of the limits that already refilled and the old messages of `realtime_messages`.
- **"Where to look" in `AGENTS.md`:** a table of each topic with its guide and section, and the rule of searching the documentation before saying that something is missing. It also asks to update the guide, the "Removing…" recipes and the index in the same commit as the change.
- **`docs/links_test.go`:** `make check` checks that the documentation links (including sections, `#anchor`) and the mentions of `docs/*.md` in the code point to what exists.

### Changed

- **Each error has a specific code**, which is also the key of its message. Before, `conflict` had nine meanings, `unauthorized` three and `forbidden` two, and the frontend could only tell them apart by reading the text. Now they are, for example, `email_taken`, `slug_taken`, `two_factor_already_enabled`, `invalid_credentials`, `two_factor_challenge_expired`, `cross_origin_rejected`, `note_not_found` or `malformed_json`. The full list is in `docs/i18n.md`. The HTTP status does not change.
- **The `httpx` helpers take the code and not the text:** `httpx.Conflict("email_taken", "email")`, `httpx.NotFound("note_not_found")`, `httpx.BadRequest("unknown_field", "field", name)`. `httpx.Validation` takes `httpx.Fields` with `i18n.Message`, and the rules of `internal/validate` return `i18n.Message`. `httpx.Error` no longer has `Message`: it is translated when the response is written.
- **The es-MX messages use "tú" and not "vos"**, and the validation ones are gender-neutral ("Este campo es obligatorio.").
- **Everything internal is in English:** comments, internal errors, logs, the output of `create-admin`, `generate-key` and `rotate-keys`, the tests and their example data, the SQL comments (and with them those of `internal/store`), the `Makefile`, `.env.example`, `sqlc.yaml` and CI. What the end user sees stays in the catalogs.
  - If you filter the logs by text, the messages change (for example, `servidor escuchando` becomes `server listening`), and so do the attributes of the hourly cleanup: `sesiones`, `notificaciones`, `limites` and `mensajes_tiempo_real` become `sessions`, `notifications`, `rate_limits` and `realtime_messages`.
  - `make create-admin` without `name` creates the user "Administrator".
- **The documentation is in English:** `README.md`, `AGENTS.md`, `docs/*.md` and this CHANGELOG, with its whole history. The section titles change, and with them their anchors: for example, `docs/auth.md#quitar-funciones` becomes `docs/auth.md#removing-features`, and "Antes de producción" in the README becomes "Before production". If your project links to them, update the links.
- The 2FA date in the locked-account email moves to an unambiguous format: `2006-01-02 15:04 UTC`.
- `auth.Limits.Account` is a `*ratelimit.Limiter` (`ratelimit.New(queries, "account", 12*time.Second, 5)`) instead of an `*httpx.RateLimiter`.
- **A direct connection to PostgreSQL is needed**, or PgBouncer in `session` mode: `LISTEN` does not work with PgBouncer in `transaction` mode.
- `sqlc.yaml` generates `interval` as `time.Duration`.
- **In `internal/auth`, each account feature is in its own files, and what several share, in common files.** That way one can be removed without breaking the others:
  - `checkPassword` and `savePassword` (formerly in `profile.go`) move to the new `account.go`, together with `passwordInput`, which was in `two_factor.go`.
  - `deleteOtherSessions` and `sessionsEnded` move to `session.go`.
  - What belongs to a single feature goes to its file: `endOtherSessions` and `currentSessionID` to `sessions.go`, `checkToken` to `password_reset.go`, and the 2FA challenge cookie to `two_factor_login.go`.
  - `RotateKeys` leaves the 2FA part in `rotateTwoFactorKeys` (`two_factor.go`), and `box()` moves to `keys.go`.
  - The tests follow the same split: the common helpers go to `helpers_test.go`, and each feature's tests to its file. `transactions_test.go` and `email_limits_test.go` were split up, and `key_rotation_test.go` is renamed `two_factor_keys_test.go`.
  - `testutil.EncryptionKey` moves from `twofactor.go` to `http.go`.
- `checkToken` no longer uses the 2FA challenge's maximum attempts: the tokens that arrive by email spend no attempts.
- **`make sqlc` deletes `internal/store` before generating**, because sqlc does not delete the code of a query that no longer exists. `make check` fails if such a file is left.
- The tests of the hourly cleanup are split per table (`TestCleanupDeletesOldNotifications`, `TestCleanupDeletesOldRealtimeMessages`), so removing real time is deleting two tests.

### Fixed

- `docs/auth.md` said that expired sessions and tokens are deleted when new ones are created. Since v0.4.0 the hourly job deletes them.
- **"Removing features" in `docs/auth.md` did not work for the profile, email verification and 2FA:** the code stopped compiling. Now each recipe says everything that changes, and each one was tested separately: it compiles, passes `go vet` and staticcheck, and says which tests of other features must be adjusted.
- **"Removing real time" and "Removing the queue" did not pass `make check`.**
  - The first one was missing `testutil`, the cleanup constants and the tests of other packages that use streams or the `notifications` table.
  - The second one was missing the `Jobs` field of `server.Deps`, `rotate-keys`, `JOBS_MAX_WORKERS`, `main_test.go` and the `Makefile`. Besides, its step for the hourly cleanup was vague: now it moves it to `cmd/api/cleanup.go` with a `time.Ticker`.
  - Both of them, and the one to remove only the notifications, were tested on a copy, with a database created only with the remaining migrations.
- The README did not link auth's "Removing features" from "Starting a new project", and an agent reading the README or `AGENTS.md` had no way to find out it existed.

## [v0.5.5] - 2026-10-25

Improvements from a third review, and documentation without deployment instructions. The schema changes: `users.email` must be lowercase.

### Added

- **`TEMPLATE_VERSION`:** says which version of the template the project came from. It is copied with the rest; when you bring changes from newer versions, update it to the last one you applied.
- **`httpx.Quiet`:** the log line of a route that is called very often goes at `debug`, except for 5xx.
- **staticcheck in `make check`** (and on its own with `make lint`), with a pinned version like the other tools.

### Changed

- **`users.email` requires lowercase in the database** (`CHECK (email = lower(email))`). The application already stored them that way, but an `INSERT` by hand could create `Alice@x.com` next to `alice@x.com` and skip the `UNIQUE`.
- **`GET /healthz` is logged at `debug`** if it responds fine: a monitor no longer fills the log. A `503` is still logged as an error.
- **The documentation no longer explains how to deploy** with Caddy, Nginx, Docker or systemd. What is left are the environment variables, the production warnings and the API's rules, told in general.
- `var recorder Recorder`, unused in `mail_test.go`, becomes the check that each sender satisfies `mail.Sender`.

## [v0.5.4] - 2026-10-25

Documentation changes: how the template presents itself. No code or schema changes.

### Changed

- **The template no longer presents itself as "lightweight" or for small projects:** the README and `AGENTS.md` describe it as a complete, production-ready base for personal, client and company projects. It is still few dependencies, a single binary and one instance; the README says so in the introduction.
- `docs/decisions.md`: the list of the binary's dependencies includes River, and "Standard library first" explains that what grows is our own code, not the dependencies. The external key service and the argon2 parameters are no longer justified by "a small VPS".

## [v0.5.3] - 2026-10-25

Fixes from another review. No schema changes: no update SQL is needed (queries change, so `make sqlc` must be run).

### Fixed

- **Turning on 2FA could leave active a secret the user never scanned.** `enable` validated the code with the pending secret and then activated whatever secret there was. If between the two steps another request asked for a new QR code (`setup`, which only needs a session), that secret was left active, and whoever made the `setup` got it in the response. Now `EnableTOTP` requires the secret to be the verified one; if it changed, it responds `409`. So that a key rotation between `setup` and `enable` does not break the comparison, `enable` no longer encrypts the pending secret again: the login or `rotate-keys` moves it to the current key.
- **Logging in left the previous session of the same browser alive.** With a login without a logout or an account switch, the new cookie replaced the old one, but the row stayed until it expired and showed up in `GET /auth/sessions` as another device. Now `StartSession` (login, registration and the second step of 2FA) closes the session of the request's cookie and cuts its streams. The sessions of other devices are not touched.

### Added

- **Production warnings for `FRONTEND_URL` and `MAIL_FROM`:** if they are still on `localhost`, the API warns about it in the log on startup, as it already did with `COOKIE_SECURE` and `MAIL_DRIVER`. With `FRONTEND_URL` on localhost, the links in the emails do not work; with `MAIL_FROM`, providers reject the emails.
- **Password rehash on login:** if the argon2 parameters change, each password is hashed again with the new ones the next time its user logs in. Before, they kept the old parameters forever.

### Changed

- `server.Deps.Box`: the `ENCRYPTION_KEY` keyring is passed in `Deps`, instead of `NewHandler` building its own from `Config`. `main` passes the same one the jobs use; without it, 2FA is not available. `testutil.NewAPI` builds it on its own.
- `EnableTOTP` takes the verified secret (`store.EnableTOTPParams.TOTPSecret`).
- The shutdown comments in `cmd/api/main.go` describe the current code, and some that told how it used to be are now in the present tense.

## [v0.5.2] - 2026-10-25

Fixes from another review of the whole project. No schema changes: no update SQL is needed (a query changes, so `make sqlc` must be run).

### Fixed

- **With two proxies, the client's IP was the first proxy's.** With a CDN in front of the proxy (Cloudflare → Caddy → API), `httpx.ClientIP` took the last value of `X-Forwarded-For`, which is Cloudflare's IP: every user of the same node shared the per-IP limits and got `429`, and the sessions showed that IP. Now it is walked from right to left skipping the proxies of `TRUSTED_PROXIES`, like Caddy's `trusted_proxies_strict`. For Cloudflare, add its ranges to `TRUSTED_PROXIES` and set `trusted_proxies` in Caddy (see "Before production" in the README). With a single proxy, nothing changes.
- **Turning 2FA on or off could be left halfway.** If storing the recovery codes or closing the other sessions failed, 2FA was left on without codes and with the other sessions open, and the retry responded `409`. Now the steps go in a transaction.
- **`GET /auth/sessions` showed sessions expired by inactivity** until the periodic cleanup deleted them. Now the list has only the ones that still work.

### Added

- **`make rotate-keys` counts the queued emails** encrypted with previous keys. Before, the report could say that a key could be removed while emails encrypted with it were still queued, and they were cancelled when it was removed; it had to be checked by hand with a `SELECT`. Now, while there are some, the key is not listed as removable and the command ends with an error. `jobs.QueuedEmailKeys` and `auth.KeyReport.AddQueuedEmails` do it.
- `docs/decisions.md`: what the API reveals about registered emails (the login and the password reset do not; registration and the email change do, with `409`) and how to hide it completely if a project needs it.
- `SMTP_TEST_ADDR` documented in `AGENTS.md` and `.env.example`: with Mailpit (`localhost:1025`), the tests also test real SMTP sending.

### Changed

- **`?offset=` accepts at most 10000** (`httpx.MaxOffset`); a larger one responds `422`. A huge offset forced PostgreSQL to read and discard all the previous rows. To go further, paginate by cursor (see the README).
- `github.com/riverqueue/river/rivertype` becomes a direct dependency in `go.mod`: it is the same version that was already there as indirect.

## [v0.5.1] - 2026-10-25

Fixes from a review of the whole project. No schema changes: no update SQL is needed.

### Fixed

- **Turning on 2FA did not ask for the password.** Someone with a stolen session could turn it on with their own app: since `enable` closes the other sessions and a password reset does not turn off 2FA, the owner was locked out until an admin turned it off by hand. Now `POST /auth/two-factor/enable` asks for `{code, password}`, like turning it off.
- **Confirming a new email did not cancel the reset links sent to the previous one.** They kept working for up to an hour: if the user changed the email because they had lost the old mailbox, whoever had it could reset the password and keep the account. Now they are deleted on confirmation.
- **Changing or resetting the password did not cancel a pending email change.** Whoever knew the previous password could request the change to a mailbox of theirs and confirm it after the owner changed the password, to keep the email and, from there, the account. Now `savePassword` deletes that link too.

### Changed

- **`POST /auth/two-factor/enable` takes `{code, password}`.** The frontend has to ask for the password on the activation screen.
- **The PostgreSQL pool has 10 connections by default**, instead of pgx's 4 (or one per core), if `DATABASE_URL` does not set `pool_max_conns`. River takes a fixed connection for LISTEN and shares the rest with the API.
- `testutil.EnableTwoFactor(t, api, cookie, password)` takes the user's password. For one created with `testutil.Login` it is `testutil.Password`.

## [v0.5.0] - 2026-10-24

Account features: the ones Laravel Jetstream adds over Breeze.

### Added

- **Sessions per device:** each session stores the browser, the IP and its last activity (at most every 5 minutes).
  - `GET /auth/sessions` lists them and marks the current one;
  - `DELETE /auth/sessions/{id}` closes one and cuts its streams;
  - `POST /auth/sessions/revoke-others` with the password closes all the others.
- **Idle expiry (`SESSION_IDLE_TIMEOUT`)**, optional: a session without activity for that long stops working. An open SSE stream does not count as activity, and the periodic cleanup deletes the idle ones.
- **Changing the email:** `PUT /auth/email` with the password sends a link to the new email and a notice to the current one; `POST /auth/email/confirm` changes it and leaves it verified. `email_changed` notification.
- **Deleting the account:** `DELETE /auth/me` with the password deletes the user and all their data, cuts their streams and sends them a last email. It does not allow deleting the only admin.
- Email templates `email_change.txt`, `email_change_notice.txt` and `account_deleted.txt`.

### Changed

- **`auth.Service.StartSession(w, r, userID)`** takes the request, to store the browser and the IP.
- **Edited migrations:** `00002_create_sessions.sql` (`id`, `user_agent`, `ip`, `last_seen_at`) and `00006_create_user_tokens.sql` (`email` and the `email_change` purpose). Since the template has no derived projects yet, there is no update SQL: the database is recreated.
- "What it does not include" no longer lists changing the email.

## [v0.4.6] - 2026-10-24

Fixes from another review of the whole project.

### Fixed

- **The per-IP limit of `/auth` was a single one for every route** (5 in a row and one every 12 s): behind a shared IP (an office, a university, CGNAT), legitimate users got `429`, and a login with 2FA spent two attempts. Now each group has its own limit (login, registration, 2FA, tokens and emails), more generous, and the actions with a session (changing the password, 2FA) are limited per user. The values are together in `internal/server/routes.go`.
- **`Recover` appended the error JSON to a response that had already started**, for example halfway through an SSE stream. Now it logs the panic and cuts the connection.

### Added

- **`TRUSTED_PROXIES`:** the exact IPs or CIDR ranges of the proxy. Without it, localhost and every private network are still trusted. Some providers have private networks shared between customers.
- `docs/adding-a-resource.md`: `pg_trgm` for `ILIKE` searches when the table grows.

### Changed

- `httpx.NewRateLimiter(every, burst)` no longer takes the proxies: the middleware chooses the key (`MiddlewareByIP(proxies)` or `MiddlewareBy(key)`).
- `httpx.ClientIP(r, proxies)` and `auth.Options.TrustedProxies` take the list of trusted proxies, instead of a boolean.
- `auth.Limits` has a field per group of routes.

## [v0.4.5] - 2026-10-24

Fixes from a new review of the whole project.

### Fixed

- **An SSE client that stopped reading blocked its stream.** `Open` removed the `WriteTimeout` and set no other deadline: the write blocked with the TCP buffers full, the handler saw neither the logout nor the shutdown, and the connection kept counting toward the limits. Now each write has a 10-second deadline.
- **If the shutdown ran over time, River did not stop.** `run` returned before stopping the jobs, and an interrupted one was retried only an hour later. Now the HTTP server and River stop in parallel, each one with the full timeout, and the remaining requests are cut.
- **Resetting the password was not atomic:** the link was spent before storing the password. Now consuming the token, storing the password, marking the email and closing the sessions go in a transaction. The same when changing it from the profile.
- **Two simultaneous link requests left two valid links.** `issueToken` now goes in a transaction that locks the user's row.
- **If the verification email could not be queued, registration responded `500`** even though the account already existed (and a retry gave `409`). Now it responds `201`, and the user can ask for another link.
- **`httpx.Write` sent the status before encoding the JSON:** if the encoding failed, the client got a `200` with a truncated body and the error was logged without the `request_id`. Now it encodes first and, if that fails, `Recover` responds `500` and logs it with the `request_id`.

### Changed

- **`auth.NewService` takes the pool** (`*pgxpool.Pool`) instead of the queries, to be able to open transactions.
- **Without `TEST_DATABASE_URL`, the integration tests fail in CI** (`CI=true`) instead of being skipped silently, and `make test` warns about it at the start.
- `docs/adding-a-resource.md` shows the transaction pattern with `pgx.BeginFunc`, and `docs/decisions.md` explains the shutdown with Docker and systemd.

## [v0.4.4] - 2026-10-24

Documentation only.

### Fixed

- **The `handleClosed` example in `docs/realtime.md`** showed the verify-your-email notice (and did not retry) to any unverified user, even if verification was not required and the cut was due to a `429` or a `503`. Now it asks `/notifications`, which goes through the same checks as `/events`: the `403 email_not_verified` only shows up when verification is required.

## [v0.4.3] - 2026-10-24

Documentation only.

### Fixed

- **The frontend example of `docs/realtime.md`:**
  - the connection is built in a `connect()` function, so a retry creates the new `EventSource` with its listeners (before, `onerror` called a `connect` that did not exist);
  - with an unverified email it does not retry forever: `GET /events` responds `403 email_not_verified`, and the example detects it with `/auth/me`. It also explains that the connection is opened only when the email is verified.

## [v0.4.2] - 2026-10-24

Adjustments from the review of v0.4.1.

### Changed

- **A topic without permission no longer rejects the whole `GET /events` connection with `403`.** It is reported in the `ready` event (`{"topics": […], "rejected": […]}`) and the user keeps receiving their channel and the other topics. Before, someone who was removed from a project also lost the channel of their notifications, and the frontend could not know which topic they had lost.
- **A stream's life varies at random between 13 and 17 minutes**, instead of a fixed 15, so that after a restart not every tab renews at once.

### Fixed

- **The `onerror` example in `docs/realtime.md`** sent to the login on any closed connection, even if it was due to too many connections. Now it asks `GET /auth/me`: with `401` it goes to the login; otherwise, it retries later.

## [v0.4.1] - 2026-10-24

Fixes from the review of v0.4.0.

### Fixed

- **A `GET /events` stream outlived its session.** The session was checked only on connecting, and the heartbeat kept the connection alive: someone with a stolen session kept receiving events after the owner reset the password, including the `password_changed` notice. Now:
  - it is cut at once with the logout of that session, and when auth closes the user's other sessions (password change or reset, 2FA), before the security notice;
  - the session is checked on each heartbeat (every 25 seconds);
  - it lasts at most 15 minutes: on reconnecting, the session and the topic permissions are checked again.

### Added

- `auth.Options.SessionWatcher` (the broker implements it), `auth.Service.SessionKey` and `CheckSession`.
- `broker.DisconnectUser`, to take away someone's access to a topic instantly.
- The periodic cleanup deletes the notifications read more than 90 days ago.
- `docs/encryption.md`: before removing a previous key, confirm that no emails are left in the queue.

### Changed

- `realtime.Broker.Handler` takes the `realtime.Sessions` interface (in the template, the auth service).

## [v0.4.0] - 2026-10-24

Background jobs, real time and notifications: what Laravel's queues, scheduler, Reverb and notifications solve.

### Added

- **Background jobs with River** (`internal/jobs`, see `docs/jobs.md`), on PostgreSQL and without Redis:
  - jobs survive a restart and are retried if they fail;
  - scheduled and periodic jobs (the scheduler), unique per period;
  - queuing inside a transaction (`InsertTx`);
  - the workers run in the same binary and shut down in order, after the HTTP server;
  - River's schema comes in as a goose migration; `make river-migration` to update it;
  - `JOBS_MAX_WORKERS`.
- **Periodic `cleanup` job:** every hour it deletes the expired sessions and tokens.
- **Real time with SSE** (`internal/realtime`, see `docs/realtime.md`):
  - `GET /events` with the user's channel and topics with authorization (`?topics=project:42`);
  - `realtime.Stream` to stream long responses, like an AI agent's;
  - it removes the `WriteTimeout` only for the stream, sends heartbeats and closes when the API shuts down;
  - limits of 10 connections per user and 1000 in total; each one takes about 11 KB.
- **Notifications** (`internal/notifications`): `GET /notifications` (with `?unread` and the counter), `POST /notifications/{id}/read` and `POST /notifications/read-all`. They arrive live through `GET /events`.
- **Security notices:** auth notifies when the password is changed and when 2FA is turned on or off (`auth.Options.Notifier`, optional).
- **`notes` publishes its changes live** (`note.created`, `note.updated`, `note.deleted`), as an example.
- **testutil:** `NewServer`, `OpenEvents`, `WithBroker`; and `auth.WithUser` to test protected handlers.

### Changed

- **Emails go through the queue:** they are stored encrypted (they carry links with tokens), at most 4 are sent at once and they are retried up to 5 times. An SMTP outage or an API restart no longer loses emails.
- Cleaning up expired sessions and tokens is no longer done on each login.
- sqlc generates `jsonb` columns as `json.RawMessage`.

### Removed

- `mail.Queue`, replaced by `jobs.MailSender`.

## [v0.3.8] - 2026-10-24

Last details of the final review.

### Added

- **A warning on startup** if `COOKIE_SECURE=false` in production: the cookies travel without HTTPS and without the `__Host-` prefix. It is not an error, because an internal system may use HTTP on purpose. The production warnings are gathered in `config.Config.Warnings`, next to the `MAIL_DRIVER=log` one.

### Fixed

- **The login email had no length limit.** Since it is the key of the per-account limit, each attempt could take up to 1 MB of memory. Now the same maximum as in registration is validated (254 characters).
- **The email queue had no cap on simultaneous sendings:** each email opened its own SMTP connection. Now at most 4 are sent at once.

## [v0.3.7] - 2026-10-24

Documentation only.

### Changed

- **`TRUST_PROXY` with Docker** (README, `.env.example` and `docs/decisions.md`): do not publish the API port, or publish it only on localhost (`-p 127.0.0.1:8080:8080`). In some setups, Docker forwards outside connections from its private gateway, and the API would trust a made-up `X-Forwarded-For` again.

## [v0.3.6] - 2026-10-24

`TRUST_PROXY` protection and a configuration checklist for production.

### Added

- **"Before production"** in the README: what to check in the configuration, whatever the server.

### Fixed

- **With `TRUST_PROXY=true`, the IP could be made up:** if the API port was open to the internet, whoever connected directly could send a fake `X-Forwarded-For` and dodge the per-IP limits. Now the header is only trusted if the connection comes from localhost or a private network, which is where the proxy is.
- `X-Forwarded-For` is read from every header, not just the first one, and the last value is taken, which is the one the proxy appends. If it is not a valid IP, it is ignored.

## [v0.3.5] - 2026-10-24

Minor adjustments from the review of v0.3.4, and the template's scope.

### Added

- **`GOMEMLIMIT`** documented in the README and `.env.example`: it is Go's own variable. In a test with 200 simultaneous logins and 12 cores, `GOMEMLIMIT=300MiB` lowered the memory peak from 590 MiB to 317 MiB, with logins 10% slower.

### Changed

- **`testutil.Login` accepts the same options as `NewAPI`**, so the cookie matches when a test uses `CookieSecure`.
- **"What it does not include"** (`docs/decisions.md`) is no longer presented as a to-do list: OAuth and the rest are added in the project that needs them. The template does not include a deployment guide, because it depends on each server.

### Fixed

- **The documentation of the per-account limit** said that a password reset was the owner's way out. That holds once the attack is over; if it continues, the login of that account stays blocked while it lasts.

## [v0.3.4] - 2026-10-24

Adjustments for production on a small VPS, from a review with the API running.

### Added

- **Per-account login attempt limit:** 10 in a row per email and then one per minute, whatever IP they come from (`429` with `Retry-After`). It also counts emails that do not exist, so as not to reveal which are registered. A password reset resets it.
- **Headers:** `X-Content-Type-Options: nosniff` on every response (`httpx.SecurityHeaders`) and `Cache-Control: no-store` on all of `/auth` (`httpx.NoStore`).
- **`__Host-` cookies in production:** with `COOKIE_SECURE=true` they are called `__Host-session` and `__Host-two_factor_challenge`, so a subdomain cannot create or overwrite them.

### Changed

- `auth.HashPassword` and `auth.VerifyPassword` take a `context.Context`.

### Fixed

- **Simultaneous logins could exhaust the memory:** each argon2 hash reserves 19 MiB and there was no cap. With 200 simultaneous logins on 2 cores, the process went from 16 MiB to 2.5 GiB. Now at most one hash runs per core: the peak stays around 120 MiB, and the logins finish sooner (2.4 s against 3.3 s).

## [v0.3.3] - 2026-10-24

Two 2FA details around the password change.

### Fixed

- **Changing or resetting the password deletes the pending 2FA challenges.** Before, whoever had passed the first step of the login with the previous password could finish it with a code during the challenge's 5 minutes.
- **Resetting the password resets the user's 2FA attempt limit.** Before, if someone with the previous password had used it up, the owner had to wait up to an hour to log in with the new one.

### Added

- `httpx.RateLimiter.Reset`, to forget a key.

## [v0.3.2] - 2026-10-24

Security fixes from a review of 2FA, email sending and encryption.

### Added

- **A maximum of 5 attempts per 2FA challenge:** once used up, the challenge is deleted and it responds `401 two_factor_locked`; the password must be entered again.
- **Per-user 2FA attempt limit:** 10 in a row and then one every 6 minutes, no matter how many IPs they come from (`429` with `Retry-After`).
- **Warning email** when a challenge is locked: whoever reached that step knows the password. It includes the date and the IP. Template in `internal/auth/emails/two_factor_locked.txt`.
- **Per-recipient email limit:** "Forgot my password", resending the verification and the notice allow 3 emails in a row per user and then one every 20 minutes.
  - `forgot` still responds the same and **does not invalidate the last link sent**.
  - `resend` responds `429`.
- **`auth.Options.TrustProxy`**, to record the real IP in the notice.
- **testutil:** `DoFromIP`, to simulate requests from several IPs.

### Changed

- **The 2FA secret is encrypted bound to its user** (AES-GCM associated data): copied to another row, it cannot be decrypted. `secretbox.Encrypt` and `Decrypt` take an `aad` parameter.
- **The HMAC of the recovery codes includes the user's id.**
- Reading the encrypted format of v0.3.0 (without a key identifier) was removed.
- **Migrations rule** in `AGENTS.md` and `docs/decisions.md`: in a project, until the first deploy the original migrations are edited; after it, always a new one.

### Fixed

- **Brute force of the 2FA code from many IPs:** before, a wrong code did not spend the challenge and there was only a per-IP limit.
- **Unlimited emails to the same person** using several IPs, which also cancelled their previous link.
- **The limiter forgot an idle key after 10 minutes** even if it took longer to refill (the email one takes 15), and gave it back all its attempts. Now it remembers it until it refills.

## [v0.3.1] - 2026-10-24

Rotation of `ENCRYPTION_KEY` without affecting users, and better error handling.

### Added

- **`ENCRYPTION_PREVIOUS_KEYS`:** previous keys that are still accepted to read what was encrypted before a rotation.
- **Key identifier:** each encrypted value carries it (`52bf1807:…`), so it is known which key decrypts it and whether it must be encrypted again.
- **Gradual re-encryption:** when 2FA is used, a secret encrypted with an old key moves to the current one.
- **`make rotate-keys` (`./api rotate-keys`):** encrypts again everything that is left and reports:
  - the secrets that cannot be decrypted;
  - the users with recovery codes of old keys;
  - the keys that can already be removed.

  It ends with an error if the rotation is not complete.
- **`make key` (`./api generate-key`):** generates a new key, without `openssl`.
- **`docs/encryption.md`:** what the key protects, how to store it, the rotation step by step, what to do if it is lost and the comparison with Laravel's `APP_KEY`.

### Changed

- The recovery codes store the `key_id` of their HMAC and are verified with every key of the keyring.
- `secretbox.New` takes the current key and the previous ones. There are typed errors: `ErrUnknownKey` and `ErrDecrypt`.

### Fixed

- A 2FA secret that cannot be decrypted no longer responds `500`: it responds `503 two_factor_unavailable` with a clear message, and the log records the cause and the key.

## [v0.3.0] - 2026-10-24

Opt-in 2FA with an authenticator app, comparable to what Laravel Fortify adds over Breeze.

### Added

- **2FA with TOTP (RFC 6238):** it works with Google Authenticator, Authy, 1Password, etc.
  - `POST /auth/two-factor/setup` returns the secret and the `otpauth://` URL for the QR code.
  - `POST /auth/two-factor/enable` turns it on with a valid code and hands out 10 recovery codes.
  - `POST /auth/two-factor/disable` and `POST /auth/two-factor/recovery-codes` ask for the password.
- **Two-step login:** with 2FA, `POST /auth/login` responds `202 {two_factor_required: true}` and a challenge cookie that expires in 5 minutes. `POST /auth/two-factor/challenge` accepts the app's code or a recovery code.
- **2FA protections:**
  - a code already used is rejected;
  - each recovery code works once;
  - turning 2FA on or off closes the other sessions;
  - a password reset does not skip 2FA.
- **`internal/totp` package**, with the standard library and tested with the RFC's vectors.
- **`internal/secretbox` package:** AES-256-GCM and HMAC-SHA256 with keys derived through HKDF.
- **Configuration:** `ENCRYPTION_KEY` (required) and `APP_NAME`.
- **testutil:** `EncryptionKey`, `EnableTwoFactor` and `TwoFactor.Code`.

### Changed

- `POST /auth/login` can respond `202` when the user has 2FA.
- `auth.User` includes `two_factor_enabled`.
- The configuration strips the quotes around a value, so a `.env` with `APP_NAME="My App"` works with make, the shell and systemd.
- sqlc writes the TOTP initialism in uppercase in the generated names.

## [v0.2.0] - 2026-10-24

Complete authentication for public projects and SaaS, comparable to Laravel Breeze.

### Added

- **Profile:** `PUT /auth/me` to change the name.
- **Password change:** `PUT /auth/password` asks for the current one, has an attempt limit and closes the other sessions (the current one stays active).
- **Password reset by email:**
  - `POST /auth/password/forgot` always responds the same and sends a single-use link that expires in 1 hour.
  - `POST /auth/password/reset` changes the password and closes every session.
- **Optional email verification** (`REQUIRE_EMAIL_VERIFICATION`): a link on registration that expires in 24 hours, `POST /auth/email/verify` and `POST /auth/email/resend`. While the email is not verified, the protected routes respond `403 email_not_verified`.
- **`internal/mail` package:**
  - sending over SMTP (TLS or STARTTLS);
  - log for development;
  - sending in the background, which waits for the pending ones on shutdown;
  - `Recorder` for the tests.
- **Configuration:** `FRONTEND_URL`, `REQUIRE_EMAIL_VERIFICATION`, `MAIL_DRIVER`, `MAIL_FROM` and `SMTP_*`.
- **Editable email templates** in `internal/auth/emails/`.
- **`user_tokens` table** for single-use tokens (stored as SHA-256) and the `users.email_verified_at` column.
- **testutil:** `NewAPI` with options (`WithMailer`, `WithConfig`) and `TokenFromEmail`.
- **Documentation:** the new flows, the screens the frontend needs, Mailpit for development and how to remove each feature.

### Changed

- `auth.Register` takes `auth.Limits` (attempts and emails) instead of a single middleware.
- `auth.User` includes `email_verified_at`.
- The admins created with `create-admin` have their email verified.

## [v0.1.0] - 2026-10-23

First published version.

### Added

- **API basics:**
  - `net/http` with method routing, server timeouts and graceful shutdown on SIGINT/SIGTERM.
  - Configuration from environment variables, validated on startup.
  - Logs with `slog` (text in development, JSON in production), with `request_id` and `user_id` on each line of the request.
  - A single JSON error format, strict JSON reading (1 MB limit and no unknown fields) and `{data, meta}` pagination.
  - Request ID, logging and panic recovery middlewares. `/healthz` with a database ping.
- **Database:**
  - PostgreSQL with `pgx`.
  - Typed queries with `sqlc`.
  - `goose` migrations embedded in the binary and applied on startup, with a lock so two instances do not migrate at once.
  - Translation of PostgreSQL errors: a repeated unique value or an invalid FK are answered as 409 or 422.
- **Authentication:**
  - Registration, login, logout and `/auth/me`, with sessions in PostgreSQL and an `HttpOnly`, `SameSite=Lax` cookie. Only the SHA-256 of the token is stored in the database.
  - Passwords with argon2id (OWASP parameters). The login does not reveal which emails exist.
  - `user` and `admin` roles, with the `RequireUser`, `RequireAdmin` and `OptionalUser` middlewares.
  - `create-admin` subcommand (`make create-admin`).
- **Security:**
  - CSRF protection with `http.CrossOriginProtection`.
  - Configurable CORS.
  - A limit of 5 attempts per minute on login and registration, per IP or per `/64` block on IPv6.
- **Example resources:**
  - `notes`: each user's data, where someone else's resource responds 404.
  - `categories`: public reads, admin writes, unique slug and it cannot be deleted if it has products.
  - `products`: JOIN with the category, filters and search, price in cents, and inactive products visible only to the admin.
- **Tests:**
  - Integration tests against a separate test database, which refuses to run if it does not end in `_test` or if it is the app's.
  - `NewDB`, `Login`, `NewAPI`, `Do` and `Decode` helpers.
- **Tools:**
  - `Makefile` with `run`, `build`, `check` (gofmt, sqlc up to date, vet, tests and govulncheck), migrations and `create-admin`.
  - Tools with a pinned version through `go run package@version`.
  - CI on GitHub Actions with PostgreSQL 18.
- **Documentation:** `README`, `AGENTS.md` (with the gopls MCP instructions), and in `docs/` the authentication guide, the resources guide and the design decisions.
