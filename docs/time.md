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
