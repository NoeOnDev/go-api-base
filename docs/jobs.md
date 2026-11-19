# Background jobs

The template uses [River](https://riverqueue.com), a job queue stored in **PostgreSQL**: no Redis needed. It is the equivalent of Laravel's queues (`queue:work`, `schedule:run`):

- **Jobs survive a restart.** If the API crashes or restarts, the pending ones are still in the database.
- **They are retried if they fail**, with a longer wait each time.
- **They can be scheduled** for later, or repeated every so often.
- **They can be queued inside a transaction**, together with the data: both are stored or neither.

**The workers run inside the same process as the API.** Laravel needs another process (`queue:work`), because each PHP request lives and dies on its own; in Go, the API is already a process that is always running. That way it is still a single binary and a single service to deploy.

## What already uses the queue

| Job | Queue | What it does |
|---|---|---|
| `send_email` | `email` | Sends the emails. Up to 5 attempts, with the content encrypted (see below) |
| `cleanup` | `default` | Every hour it deletes the expired sessions and tokens, the notifications read more than 90 days ago, the keys of the attempt limits that already refilled and the large real-time messages |

There are two queues, each one with its own worker limit, so a slow job in one does not hold back the other:

- **`default`:** `JOBS_MAX_WORKERS` jobs at once (10 by default).
- **`email`:** 4 at once, because each sending opens an SMTP connection and providers reject many simultaneous ones.

The workers use the same connection pool as the API, which has 10 connections if `DATABASE_URL` does not set another size (one per core, on a server with more than 10; pgx alone would use only 4 on a small one). If you raise `JOBS_MAX_WORKERS`, raise the pool too (`?pool_max_conns=20`).

Each instance also keeps two connections outside the pool, to listen (`LISTEN`): River's and the one of real time. Count them in PostgreSQL's limit: with three instances and a pool of 10, up to 36 connections.

River maintains its own tables through that pool too (it deletes the finished jobs, and once a day rebuilds its indexes), with the same limit of 10 seconds per statement. That is plenty while the queue is small, and the completed jobs are deleted after a day. With millions of jobs waiting, raise it (`?statement_timeout=`).

## Creating a job

A job has two parts: the **arguments**, stored in the database as JSON, and the **worker**, which runs it.

**A job that a resource queues lives in the resource's package**, like `internal/appointments/reminder.go`, and `internal/jobs` only registers it. Inside `internal/jobs` it would be an import cycle in the tests: the resource would import `jobs` to queue it, and the tests of `jobs` import `testutil`, which imports every resource. Only the jobs of the base itself (`send_email`, `cleanup`) are in `internal/jobs`.

```go
// internal/reports/job.go
package reports

// ReportArgs is the job of generating a user's monthly report.
type ReportArgs struct {
	UserID int64  `json:"user_id"`
	Month  string `json:"month"` // "2026-10"
}

// Kind is the job's name in the database. Do not change it after deploying:
// the pending jobs with the old name would not find their worker.
func (ReportArgs) Kind() string { return "monthly_report" }

// ReportWorker generates the report. internal/jobs registers it (see registerWorkers).
type ReportWorker struct {
	river.WorkerDefaults[ReportArgs]
	q *store.Queries
}

func NewReportWorker(q *store.Queries) *ReportWorker { return &ReportWorker{q: q} }

func (w *ReportWorker) Work(ctx context.Context, job *river.Job[ReportArgs]) error {
	// Returning an error retries the job later.
	return generateReport(ctx, w.q, job.Args.UserID, job.Args.Month)
}
```

Then register it in `registerWorkers` (`internal/jobs/workers.go`), like the routes in `server/routes.go`:

```go
if err := river.AddWorkerSafely(workers, reports.NewReportWorker(deps.Queries)); err != nil {
	return err
}
```

If the worker needs something else (the real-time broker, an HTTP client), add it to `jobs.Deps` and pass it from `cmd/api/main.go`.

## Queuing

From a handler, with the client of `server.Deps.Jobs` (a `*river.Client[pgx.Tx]`: pass it to the resource when registering it in `routes.go`, like `appointments.NewHandler`):

```go
if _, err := h.jobs.Insert(r.Context(), ReportArgs{UserID: user.ID, Month: "2026-10"}, nil); err != nil {
	return fmt.Errorf("queue report: %w", err)
}
```

**Inside a transaction**, so the job exists only if the data was stored:

```go
tx, err := h.db.Begin(r.Context())
if err != nil {
	return err
}
defer tx.Rollback(r.Context())

order, err := h.q.WithTx(tx).CreateOrder(r.Context(), params)
if err != nil {
	return err
}
// If something fails afterwards and there is a Rollback, the job is not queued either.
if _, err := h.jobs.InsertTx(r.Context(), tx, OrderConfirmationArgs{OrderID: order.ID}, nil); err != nil {
	return err
}
return tx.Commit(r.Context())
```

The job does not run until the transaction commits, so the worker always finds the order.

## Job options

They are set with an `InsertOpts` method on the arguments (always applies), or when queuing (only that time):

```go
func (ReportArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       river.QueueDefault,
		MaxAttempts: 10,                        // 25 by default
		ScheduledAt: time.Now().Add(time.Hour), // run it later
		// Only one per user and month, even if it is queued twice:
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	}
}
```

Inside the worker:

- **`return err`:** it is retried later. River waits longer between attempts each time: seconds at first, hours at the end.
- **`return river.JobCancel(err)`:** it is not retried, because it would be useless (for example, the user no longer exists).
- **`return river.JobSnooze(30 * time.Second)`:** it is tried again in 30 seconds, without counting as a failure (for example, an external API asks to wait).

A failed attempt is logged as a warning ("job failed; it will be retried") and the last one as an error ("job failed on its last attempt; it is discarded"), with the job's `kind` and id (`errorLogger` in `internal/jobs/jobs.go`). River does not log a job cancelled with `JobCancel`, except at debug: if losing it matters, log it in the worker before returning, like `sendEmailWorker` does.

## Sensitive data in the arguments

The arguments are stored in the `river_job` table unencrypted, and River keeps finished jobs for 24 hours. **If they carry a secret or personal data, encrypt the sensitive part** with `secretbox`, as `SendEmailArgs` does with the whole email: a password reset email carries a link with a token, and unencrypted, whoever reads the table could use it. Even better is to store only an id and look up the rest in the worker.

## Periodic jobs

The equivalent of Laravel's scheduler. They are added in `periodicJobs` (`internal/jobs/workers.go`):

```go
// Every hour, like the cleanup.
river.NewPeriodicJob(
	river.PeriodicInterval(time.Hour),
	func() (river.JobArgs, *river.InsertOpts) { return CleanupArgs{}, nil },
	&river.PeriodicJobOpts{ID: "cleanup", RunOnStart: true},
),
// Every day at 8:00 of APP_TIME_ZONE: ->dailyAt('08:00')->timezone(…) in Laravel.
river.NewPeriodicJob(
	DailyAt(8, 0, deps.TimeZone),
	func() (river.JobArgs, *river.InsertOpts) { return DailyDigestArgs{}, nil },
	&river.PeriodicJobOpts{ID: "daily_digest"},
),
```

- With several instances, only one queues them: the one River elects as leader.
- `RunOnStart` also queues it on startup. To avoid repeating it if the API restarts often, make it unique per period (`UniqueOpts.ByPeriod`), like `CleanupArgs`. A job at a fixed hour usually does not want it.
- **A run at a fixed hour can be skipped.** The schedule lives in the memory of the leader (River only stores it in its paid version): if no instance is running at that minute, because of a deploy or a crash, the new leader waits for the next time, tomorrow or next week. A job that must not be skipped does not trust that it ran: it looks at what is pending ("the digests not sent yet today"), with `RunOnStart` and unique per period (`UniqueOpts.ByPeriod`) so that starting up runs it if it is due, and only once.
- **At an hour of the clock**, `DailyAt(hour, minute, loc)` and `WeeklyAt(weekday, hour, minute, loc)` (`internal/jobs/schedule.go`). They follow the clock of `loc`, not a fixed interval: after a daylight saving time change, 8:00 is still 8:00. `deps.TimeZone` is `APP_TIME_ZONE`; a job for a place with its own zone passes that one. `river.PeriodicInterval(24*time.Hour)` would drift one hour at each change.
- **At 8:00 of each user**, everyone in their own zone, is not one periodic job per user: a job every 15 minutes picks the users for whom it is already 8:00 and who have not received the notice today, and stores that they did. Every 15 minutes, and not every hour, because some zones are 30 or 45 minutes off (India, Newfoundland, Nepal). See [time.md](time.md).

## On shutdown

The API stops taking requests and, at the same time, the workers have up to `SHUTDOWN_TIMEOUT` to finish what they are doing. Stopping both at once is safe: if a request that is still finishing queues a job, it stays in the database and runs on the next start.

- **Pending jobs, or those waiting for a retry,** stay in the database and are picked up on startup.
- **One cut halfway** is not lost either: River retries it when it has been "running" for an hour. That is why a worker must be able to run twice without breaking anything: it records that the work was done and checks it first, like the appointment reminder with `reminded_at` (`internal/appointments/reminder.go`).

## Inspecting the queue

```sql
-- What is pending, what is being retried and what failed for good
SELECT id, kind, state, attempt, max_attempts, scheduled_at, errors[array_length(errors, 1)] AS last_error
FROM river_job
WHERE state NOT IN ('completed', 'cancelled')
ORDER BY id DESC;
```

River has a web interface, [River UI](https://riverqueue.com/docs/river-ui), similar to Horizon. It is not in the template because it adds another dependency and needs its own authentication.

## Tests

A worker is tested with `rivertest`, without starting the queue (see `internal/jobs/email_test.go`):

```go
worker := rivertest.NewWorker(t, riverpgxv5.New(pool), &river.Config{}, reports.NewReportWorker(store.New(pool)))
res, err := worker.Work(t.Context(), t, tx, reports.ReportArgs{UserID: 1, Month: "2026-10"}, nil)
```

To check that a handler queued a job, give the test API a client without workers with `testutil.WithJobs`, and use `rivertest.RequireInserted` (see `internal/appointments/handlers_test.go`):

```go
client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{}) // it queues, and runs nothing
api := testutil.NewAPI(pool, testutil.WithJobs(client))
// … the request that queues the job …
job := rivertest.RequireInserted(t.Context(), t, riverpgxv5.New(pool), &reports.ReportArgs{}, nil)
```

## Updating River

1. Update `github.com/riverqueue/river` in `go.mod` and `RIVER_VERSION` in the `Makefile`, to the same version.
2. Check its [CHANGELOG](https://github.com/riverqueue/river/blob/master/CHANGELOG.md). If the version brings a new migration, create a goose migration with its SQL:

   ```bash
   make migrate-new name=river_vN
   make river-migration version=N
   ```

   The second command prints the Up and Down sections, ready to paste into the new migration.

## Running the workers in another process

If a project has heavy jobs that must not compete with the API for the CPU, they can run separately:

1. A `worker` subcommand in `cmd/api` that builds the client with `jobs.New`, calls `Start` and waits for the shutdown signal, without an HTTP server.
2. In the API, build the client without `Queues` and without calling `Start`: it only queues.

It is not built in, because a single process is enough for most projects and it is simpler.

## Removing the queue

If a project does not need it:

1. **The appointments example** queues its reminders here: remove it first (see "Removing the examples" in [time.md](time.md#removing-the-examples)), or send its reminders another way.
2. **Then, the hourly cleanup**, which today is a queue job. Move the body of `cleanupWorker.Work` (`internal/jobs/cleanup.go`), with its constants, to a `cleanup(ctx, q, idle, logger) error` function in `cmd/api/cleanup.go`. Next to it, write `cleanupEvery`, which calls it on startup and then every hour with a `time.Ticker`, until `ctx` is cancelled. In `run`, start it with `go cleanupEvery(ctx, store.New(pool), cfg.SessionIdleTimeout, logger)`. With several instances, each one cleans up on its own, and that is fine: they are deletions that do not clash. The tests of `cleanup_test.go` use River: adapt them to call `cleanup` directly, or delete them.
3. Delete `internal/jobs` and the `00008_create_river.sql` migration. In the `Makefile`, remove `RIVER_VERSION`, `RIVER_CLI` and the `river-migration` target (and its row in `AGENTS.md` and the README).
4. In `cmd/api/main.go`, remove the queue client and use the `mail.Sender` directly (`sender`) as `Mailer`: the emails are sent inside the request, without retries. `POST /auth/password/forgot` then waits for the mail server only when the email exists, so its response time tells registered emails apart: if that matters to the project, send that email from a goroutine (with `context.WithoutCancel(r.Context())`). `shutdown` is left only with the HTTP server: remove its jobs parameter and the `stopper` interface. Delete `main_test.go`, which tests the shutdown of the jobs.
5. In `internal/server/routes.go`, remove the `Jobs` field of `Deps`, and in `internal/testutil/http.go`, `WithJobs`, which sets it. In `cmd/api/keys.go`, remove the part about queued emails (`jobs.QueuedEmailKeys` and `AddQueuedEmails`).
6. In `internal/config`, remove `JobsMaxWorkers`, its cases in `config_test.go` and `positiveInt`, which is left unused. Also remove `JOBS_MAX_WORKERS` from `.env.example` and the README.
7. `make sqlc` (River's schema also generates types in `internal/store`), `go mod tidy` and `make check`.

This recipe was tested on a copy of the project, with a test database created only with the remaining migrations.
