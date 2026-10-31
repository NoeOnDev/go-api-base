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

**Why an appointment keeps its local hour and zone, besides the instant:** the rules of the zones change. When Mexico dropped daylight saving time in 2022, the appointments stored only as instants were left one hour off. With the local hour and the zone, the instant can be computed again.

**When the rules of a zone change**, update the zone database (see below) and recompute the future instants of the affected zone:

```sql
UPDATE appointments
SET starts_at = local_starts_at AT TIME ZONE time_zone,
    ends_at = (local_starts_at AT TIME ZONE time_zone) + (ends_at - starts_at)
WHERE time_zone = 'America/Mexico_City' AND starts_at > now();
```

PostgreSQL has its own zone database: both must be up to date. It also resolves the hour that happens twice (the autumn) as the second one, while `civil.DateTime.In` takes the first: if an appointment can fall on that hour, recompute it from Go, with `In`.

## Keeping the zone database up to date

The zones change a few times a year (a country drops daylight saving time, or moves it). Go uses the system's database (`/usr/share/zoneinfo`, updated with the `tzdata` package of the distribution) and, without it, the copy inside the binary (`time/tzdata`), which comes with the version of Go it was built with. PostgreSQL uses its own, which comes with its minor versions. In practice: keep the system, PostgreSQL and Go up to date.
