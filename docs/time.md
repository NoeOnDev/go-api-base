# Dates, time zones and schedules

Almost every bug with dates comes from treating three different things as one. The template keeps them apart:

| Kind | Examples | Column | Go | JSON |
|---|---|---|---|---|
| **Instant**: something that happened (or will) at an exact moment | `created_at`, `expires_at`, `sent_at` | `timestamptz` | `time.Time` | `"2026-10-31T02:00:00Z"` |
| **Calendar date**: a day, without an hour | a birthday, the day of an invoice, a holiday | `date` | `civil.Date` | `"2026-10-31"` |
| **Wall-clock time**: what the clock shows in a place | "opens at 9:00", "appointment on Nov 5 at 10:00 in Bogotá" | `time` or `timestamp`, plus a zone column | `civil.Time`, `civil.DateTime` | `"09:00"`, `"2026-11-05T10:00"` |

- **An instant travels in UTC** and the frontend shows it in the user's zone (see "Dates in UTC" in [decisions.md](decisions.md#dates-in-utc)).
- **A calendar date is never converted to a zone.** A birthday converted from UTC shows up one day early in America.
- **A wall-clock time needs its zone** (`America/Bogota`) to become an instant. The zone is stored next to it.

## The zone of the application and of each user

- **`APP_TIME_ZONE`** (`UTC` by default) is the zone of the users without their own. A system used in a single place (a business in Panama) sets the business's: `APP_TIME_ZONE=America/Panama`.
- **`users.time_zone`** is each user's, an IANA name, or `null` for the application's. It comes in `POST /auth/register`, `PUT /auth/me`, `POST /admin/users` and `POST /auth/invitation/accept`, and `GET /auth/me` returns it (see [auth.md](auth.md#profile-and-password-change)). There is no header for the zone, so the frontend sends the browser's.
- **In a handler**, `auth.MustUser(ctx).Location()` is the user's zone (their own, or the application's).
- **In an email**, the date goes in the user's zone and language: `tr.FormatDateTime(t.In(loc))`, like the "sign-in attempt blocked" notice (see "Dates and times" in [i18n.md](i18n.md#dates-and-times)).

## Dates and times without a zone: `internal/civil`

`civil.Date`, `civil.Time` (precise to the minute) and `civil.DateTime` are the types of the last two kinds:

```go
d := civil.NewDate(2026, time.November, 5)        // or civil.ParseDate("2026-11-05")
at := d.At(civil.NewTime(10, 0))                 // 2026-11-05T10:00
starts := at.In(bogota)                          // the instant: 2026-11-05T15:00:00Z
day := civil.DateOf(createdAt.In(userLocation))  // the day of an instant for someone
start, end := civil.DayRange(from, to, location) // the instants of some days, to filter
```

- **The zero value is "none":** it goes to JSON as `null` and to PostgreSQL as `NULL`, so a nullable column uses the same type, without a pointer. `civil.NewTime(0, 0)` is midnight, which is not the zero value.
- **sqlc generates them** for `date`, `time` and `timestamp` columns (see `sqlc.yaml`).
- **When the clocks change**, `In` follows the rule of JavaScript's Temporal and Java's java.time: an hour that does not exist (the spring jump) moves forward as long as the jump lasts, and an hour that happens twice (the autumn) is the first time. `Date.Start` is the first instant of a day, even on the days when the clocks jump at midnight.
- **Time zones** are IANA names (`America/Mexico_City`), loaded with `civil.LoadLocation`, which rejects `""` and `"Local"`. They are validated with `validate.TimeZone`.

### In a request body

A field with a wrong format (`"starts_on": "31/10/2026"`) does not fail when decoding: `encoding/json` does not say which field the error of a type belongs to. The value arrives invalid, and the resource's validation rejects it with the message of its type, like any other rule:

```go
type eventInput struct {
	Name     string         `json:"name"`
	StartsAt civil.DateTime `json:"starts_at"`
	EndsOn   civil.Date     `json:"ends_on"` // optional
	TimeZone string         `json:"time_zone"`
}

func (in eventInput) validate() error {
	fields := httpx.Fields{}
	if msg := validate.RequiredCivil(in.StartsAt); !msg.IsZero() {
		fields["starts_at"] = msg // validation.required or validation.datetime
	}
	if msg := validate.Civil(in.EndsOn); !msg.IsZero() {
		fields["ends_on"] = msg // only if it came in another format
	}
	if msg := validate.TimeZone(in.TimeZone); !msg.IsZero() {
		fields["time_zone"] = msg
	}
	// …
}
```

Storing an invalid value fails (`civil: cannot store an invalid date; validate it first`), so a missing check shows up in the tests.

### In the URL

Filters like `?from=2026-10-01&to=2026-10-31` are read with `civil.ParseDate`, answering `validation.date` on the parameter:

```go
from, err := civil.ParseDate(r.URL.Query().Get("from"))
if err != nil {
	return httpx.Validation(httpx.Fields{"from": i18n.M("validation.date")})
}
```

## In the frontend

**Instants** (`created_at`, `starts_at`) come in UTC. The frontend shows them in the user's zone, in their language:

```js
const timeZone = user.time_zone ?? undefined // undefined: the browser's
new Intl.DateTimeFormat('es-MX', { dateStyle: 'medium', timeStyle: 'short', timeZone }).format(new Date(note.created_at))
// "30 oct 2026, 20:00" for a user in Mexico City; "30 oct 2026, 23:00" in Buenos Aires
```

- **Which zone:** the profile's (`user.time_zone`) if it has one, so the screen matches the emails; otherwise the browser's. In a system used in a single place, the business's, fixed in the frontend (`timeZone: 'America/Panama'`), so someone who travels still sees the business's hours.
- **Send the browser's zone** when registering and when accepting an invitation: `Intl.DateTimeFormat().resolvedOptions().timeZone`. If the user changes country, the profile can offer to update it when the browser's no longer matches.
- **A wall-clock time with its zone** (an appointment: `local_starts_at` and `time_zone`) is shown as it comes, with the zone of the place: "10:00 (Bogotá time)". To show it in the viewer's zone, use the instant, `starts_at`.
- **A date input** (`<input type="date">`) gives `"2026-10-31"`: send it as is, as a `civil.Date`. Never with `new Date("2026-10-31").toISOString()`, which turns it into midnight UTC, the 30th in America.
- **A date and time input** (`<input type="datetime-local">`) gives `"2026-11-05T10:00"`, without a zone: send it as is, with the zone it refers to (`time_zone`), and let the API turn it into the instant. Converting it in the browser uses the browser's zone, which may not be the place's.

## Two scenarios

**A SaaS with users in several countries** (Argentina, Mexico, Colombia, the United States): `APP_TIME_ZONE=UTC`, and each user has their zone, sent by the frontend. A note created at `2026-10-31T02:00:00Z` is the same JSON for everyone, and each one sees it on their clock:

| User | Zone | Sees |
|---|---|---|
| Buenos Aires | UTC−3 | Oct 30, 23:00 |
| New York | UTC−4 until Nov 1, then UTC−5 | Oct 30, 22:00 |
| Bogotá | UTC−5 | Oct 30, 21:00 |
| Mexico City | UTC−6 | Oct 30, 20:00 |

In UTC it is already the 31st, but for all of them it is still the 30th. That is why "today" is always someone's day: `civil.DateOf(time.Now().In(user.Location()))`.

**A system for a single country** (Panama, Mexico): the dates are stored the same way, in UTC. `APP_TIME_ZONE=America/Panama` is the zone of the users without their own, of the emails and of the days of the reports. "A single country" is not always a single zone: Mexico has four (Tijuana, with daylight saving time; Sonora; the center; Quintana Roo), and so do the United States and others. If the users are spread out, they keep each their own, with the business's as the default.

**Daylight saving time** exists in the United States, Canada, Chile, Paraguay, Cuba, Europe and the Mexican border, not in the rest of Latin America (Mexico dropped it in 2022). A business in New York that opens at 9:00 opens at 13:00 UTC in the summer and at 14:00 UTC in the winter: that is why a schedule is stored as wall-clock time with its zone, never in UTC.

## SQL recipes

**Filter by someone's days**, with the instants of `civil.DayRange`: the query compares the column directly, so it uses its index.

```go
start, end := civil.DayRange(from, to, auth.MustUser(ctx).Location())
```

```sql
-- name: ListOrdersBetween :many
SELECT … FROM orders
WHERE user_id = $1 AND created_at >= sqlc.arg('start') AND created_at < sqlc.arg('end')
ORDER BY created_at DESC;
```

**Group by someone's day:** `AT TIME ZONE` turns the instant into that zone's wall clock. Combine it with the range, so the index still does the filtering:

```sql
-- name: OrdersPerDay :many
SELECT (created_at AT TIME ZONE sqlc.arg('time_zone')::text)::date AS day, count(*) AS orders
FROM orders
WHERE created_at >= sqlc.arg('start') AND created_at < sqlc.arg('end')
GROUP BY day
ORDER BY day;
```

The `day` column is a `date`, so sqlc generates it as a `civil.Date`.

**Do not use** `created_at::date` or `date_trunc('day', created_at)`: they cut the day in the session's zone, which is UTC.

## Modeling schedules

Every schedule is wall-clock time of a place, with that place's zone. The instants are computed from it, not the other way around.

| Kind | How it is stored | Example |
|---|---|---|
| **Business hours** | The weekday, the opening and closing hours (`time`) and the place's zone. The exceptions (holidays, special days) per `date` | "Monday to Friday, 9:00 to 18:00 in Bogotá" |
| **Appointments, bookings** | The local date and hour (`timestamp`), the zone, and the instants (`timestamptz`) computed from them, to search and to avoid overlaps | "Nov 5 at 10:00 in Bogotá, 30 minutes" |
| **Shifts** | Like an appointment, with its start and its end. A shift that crosses midnight ends the next day | "Monday 22:00 to Tuesday 06:00" |
| **Repeating classes or events** | The rule (weekdays, hour, duration, first and last day) with its zone. The dates are computed, not stored one by one; only the changes to one session (cancelled, moved) are stored | "Yoga on Mondays and Wednesdays at 18:00" |

Each kind has a working example, with its tests:

- **Business hours:** `internal/branches` (see [below](#example-business-hours-branches)).
- **Appointments:** `internal/appointments` (see [below](#example-appointments-appointments)).
- **Shifts:** `internal/shifts` (see [below](#example-shifts-shifts)).

**Why an appointment keeps its local hour and zone, besides the instant:** the rules of the zones change. When Mexico dropped daylight saving time in 2022, the appointments stored only as instants were left one hour off. With the local hour and the zone, the instant can be computed again.

**When the rules of a zone change**, update the zone database (see below) and recompute the future instants of the affected zone:

```sql
UPDATE appointments
SET starts_at = local_starts_at AT TIME ZONE time_zone,
    ends_at = (local_starts_at AT TIME ZONE time_zone) + (ends_at - starts_at)
WHERE time_zone = 'America/Mexico_City' AND starts_at > now();
```

PostgreSQL has its own zone database: both must be up to date. It also resolves the hour that happens twice (the autumn) as the second one, while `civil.DateTime.In` takes the first: if an appointment can fall on that hour, recompute it from Go, with `In`.

## Example: business hours (`branches`)

The branches of a business, each with its zone (`time_zone`) and its hours of the clock. Reads are public; writes, only for administrators.

| Route | What it does |
|---|---|
| `GET /branches`, `GET /branches/{id}` | The branches with their weekly hours |
| `POST /branches`, `PUT /branches/{id}` `{name, time_zone}`, `DELETE /branches/{id}` | Admin. Changing the zone keeps the hours of the clock. A branch with appointments answers `409 branch_in_use` |
| `PUT /branches/{id}/hours` `{hours: [{weekday, opens_at, closes_at}]}` | Admin: replaces the whole week. `weekday` goes from 0 (Sunday) to 6. A day can have several ranges (a split shift); a range whose `closes_at` is before its `opens_at` ends the next day (a bar from 18:00 to 02:00). Ranges that overlap answer `422` |
| `PUT /branches/{id}/exceptions/{date}` `{closed: true}` or `{opens_at, closes_at}`, with an optional `note` | Admin: a holiday or a day with other hours, which replaces the weekly hours of that date. `DELETE` brings the weekly hours back |
| `GET /branches/{id}/exceptions?from=&to=` | The exceptions (by default, the next 62 days) |
| `GET /branches/{id}/schedule?from=&to=` | The real hours of each date, in the local hour and in instants (by default, a week from today in the branch; up to 62 days) |
| `GET /branches/{id}/status` | `{open, closes_at, next_opens_at}` now |

```json
// GET /branches/1/schedule?from=2026-11-02&to=2026-11-03
{"time_zone": "America/Bogota", "data": [
  {"date": "2026-11-02", "exception": true, "note": "All Saints", "openings": []},
  {"date": "2026-11-03", "exception": false, "note": "", "openings": [
    {"opens_at": "09:00", "closes_at": "13:00", "starts_at": "2026-11-03T14:00:00Z", "ends_at": "2026-11-03T18:00:00Z"}
  ]}
]}
```

The logic is in `internal/branches/schedule.go`, without the database: `Days` turns the weekly hours and the exceptions into the openings of some dates, and `StatusAt` says whether the branch is open at an instant (two ranges that touch, like 18:00-00:00 and 00:00-02:00, are one opening). Its tests cover the split shift, the ranges that cross midnight and the days of the daylight saving time change: in New York, 9:00 is 13:00 UTC in the summer and 14:00 UTC in the winter, and a night from 22:00 to 06:00 lasts 7 or 9 real hours.

## Example: appointments (`appointments`)

A user books a time at a branch, within its business hours. Each user sees their appointments; an administrator, all of them.

| Route | What it does |
|---|---|
| `GET /branches/{id}/availability?date=&duration=` | Public: the free slots of a day (today of the branch by default), every 15 minutes, for an appointment of `duration` minutes (30 by default, from 15 to 240) |
| `POST /appointments` `{branch_id, starts_at, duration_minutes?}` | With a session. `starts_at` is the hour of the branch's clock, as the availability gave it (`"2026-11-05T10:00"`), and must be one of its slots: outside the hours, in the past or at 10:05 answers `422`. If someone else took it first, `409 slot_taken` |
| `GET /appointments?from=&to=` | The user's appointments that start on those days **of the user's zone** (from today, 30 days, by default) |
| `GET /appointments/{id}`, `POST /appointments/{id}/cancel` | The owner or an admin; someone else's answers `404`. Canceling frees the slot |
| `GET /branches/{id}/appointments?date=` | Admin: the agenda of a day of the branch |

```json
// POST /appointments → 201
{"id": 1, "branch_id": 1, "branch_name": "Clinic", "user_id": 2,
 "local_starts_at": "2026-11-05T10:00", "time_zone": "America/Bogota",
 "starts_at": "2026-11-05T15:00:00Z", "ends_at": "2026-11-05T15:30:00Z",
 "duration_minutes": 30, "status": "booked", "created_at": "2026-10-31T02:00:00Z"}
```

- **No double bookings:** the table has an `EXCLUDE USING gist (branch_id WITH =, tstzrange(starts_at, ends_at) WITH &&) WHERE (status = 'booked')` constraint. Two requests for the same time, even at the same instant on different instances of the API, cannot both succeed: PostgreSQL stops the second one and the API answers `409` (`db.IsExclusionViolation`). It needs the `btree_gist` extension, which comes with PostgreSQL; the migration installs it. A canceled appointment does not count.
- **One agenda per branch.** For one per person (a clinic with several doctors), add a `staff_id` and put it in the constraint: `(staff_id WITH =, tstzrange(…) WITH &&)`.
- **The reminder:** booking queues, in the same transaction, a job for 24 hours before (`ReminderArgs`, with `InsertTx`). Its worker checks that the appointment is still booked at the same time, and sends a notification: "Tomorrow you have an appointment at Clinic at 10:00", with the hour of the branch's clock. An appointment in less than 24 hours has no reminder.
- **The slots** are computed in `internal/appointments/slots.go`, without the database: every 15 real minutes from the start of each opening, so on the day of a daylight saving time change none falls on the hour that does not exist. It depends on `internal/branches`.

## Example: shifts (`shifts`)

An administrator assigns shifts; each user sees theirs, clocks in and out, and sees the time worked per day.

| Route | What it does |
|---|---|
| `POST /shifts` `{user_id, starts_at, ends_at, time_zone?, note?}`, `PUT /shifts/{id}`, `DELETE /shifts/{id}` | Admin. The hours are of the clock of the place: `"2026-11-02T22:00"` to `"2026-11-03T06:00"` is a night shift. The zone is the shift's, or the worker's if it is missing. At most 24 hours. If the person already has a shift at that time, `409 shift_overlap` |
| `GET /shifts?user_id=&from=&to=`, `GET /shifts/{id}` | The shifts that overlap those days of the zone of whoever asks (from today, a week, by default). The admin sees everyone's, or those of `user_id`; a user, only theirs |
| `POST /time-entries/clock-in`, `POST /time-entries/clock-out` | Clocking in and out, now. The entry belongs to the shift going on (or starting within the hour). Clocking in twice answers `409 already_clocked_in`; clocking out without an open entry, `409 not_clocked_in` |
| `GET /time-entries?user_id=&from=&to=` | The entries of those days of the worker's clock |
| `GET /time-entries/summary?user_id=&from=&to=` | The minutes worked on each day of the worker's clock |

```json
// POST /shifts {"user_id": 2, "starts_at": "2026-03-07T22:00", "ends_at": "2026-03-08T06:00"}, María in New York
{"id": 1, "user_id": 2, "time_zone": "America/New_York",
 "local_starts_at": "2026-03-07T22:00", "local_ends_at": "2026-03-08T06:00",
 "starts_at": "2026-03-08T03:00:00Z", "ends_at": "2026-03-08T10:00:00Z", "duration_minutes": 420, …}
```

- **The night of a change lasts one hour less or more:** that night of March, from 22:00 to 06:00 there are 7 real hours, and `duration_minutes` says so. The pay of a night shift is computed from the instants, not from the hours of the clock.
- **The time worked is split by days of the worker's clock** (`SplitByDay`, in `internal/shifts/schedule.go`): an entry from 22:00 to 06:00 counts 2 hours on the first day and 6 on the second, and an open one counts until now. Each day lasts what it really lasts: 23 or 25 hours when the clocks change.
- **One open entry per person:** a partial unique index (`ON time_entries (user_id) WHERE clock_out IS NULL`).

## Keeping the zone database up to date

The zones change a few times a year (a country drops daylight saving time, or moves it). Go uses the system's database (`/usr/share/zoneinfo`, updated with the `tzdata` package of the distribution) and, without it, the copy inside the binary (`time/tzdata`), which comes with the version of Go it was built with. PostgreSQL uses its own, which comes with its minor versions. In practice: keep the system, PostgreSQL and Go up to date.

## Removing the examples

The schedule examples are independent from the rest of the template, except that `appointments` uses `branches`, the queue and the notifications. Remove the ones you do not use:

**`appointments`:**
1. Delete `internal/appointments`, the `00013_create_appointments.sql` migration and the `appointments.sql` query.
2. In `internal/server/routes.go`, remove `appointments.Register`. In `internal/jobs/workers.go`, remove the registration of `appointments.NewReminderWorker`; if no other job notifies, also the `Notifier` field of `Deps` and its interface, and the `Notifier` line in `cmd/api/main.go`.
3. Remove the keys `errors.appointment_not_found`, `errors.slot_taken`, `validation.slot_unavailable`, `validation.unknown_branch`, `validation.duration_minutes` and `notifications.appointment_reminder` from the catalogs, and the two codes from the table of [i18n.md](i18n.md#error-codes).

**`shifts`:**
1. Delete `internal/shifts`, the `00014_create_shifts.sql` migration and the `shifts.sql` query.
2. In `internal/server/routes.go`, remove `shifts.Register`.
3. Remove the keys `errors.shift_not_found`, `errors.shift_overlap`, `errors.already_clocked_in`, `errors.not_clocked_in`, `validation.unknown_user` and `validation.max_hours` from the catalogs, and the four codes from the table of [i18n.md](i18n.md#error-codes).

**`branches`** (after `appointments`):
1. Delete `internal/branches`, the `00012_create_branches.sql` migration and the `branches.sql` query.
2. In `internal/server/routes.go`, remove `branches.Register` and the `branchesHandler`.
3. Remove the keys `errors.branch_not_found`, `errors.branch_in_use`, `errors.branch_exception_not_found`, `validation.overlapping_hours` and `validation.closed_without_hours` from the catalogs, and the three codes from the table of [i18n.md](i18n.md#error-codes).

Then `make sqlc` and `make check`, which says if a catalog key is left unused.

