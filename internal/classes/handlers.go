// Package classes is the example of repeating classes or events: "Yoga on Mondays and Wednesdays at
// 18:00, from November 3 to December 19". The class is a rule; its sessions are computed for the
// days asked for, not stored one by one. Only the changes to one session are stored: canceled, or
// moved to another hour or day. Reads are public; writes, only for administrators.
//
// The hour is of the clock of the class's zone (see "Modeling schedules" in docs/time.md):
// schedule.go keeps it across daylight saving time changes.
package classes

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Limits of the ranges of days.
const (
	sessionDays    = 30 // GET /classes/{id}/sessions without ?to=
	maxSessionDays = 92 // about three months
	agendaDays     = 7  // GET /classes/sessions without ?to=
	maxAgendaDays  = 31
)

// Handler groups the class endpoints.
type Handler struct {
	q *store.Queries
	// appZone is APP_TIME_ZONE: the "today" of the agenda of every class.
	appZone *time.Location
}

// NewHandler creates the handler. appZone is the application's zone (nil: UTC).
func NewHandler(q *store.Queries, appZone *time.Location) *Handler {
	if appZone == nil {
		appZone = time.UTC
	}
	return &Handler{q: q, appZone: appZone}
}

type classResponse struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	TimeZone        string     `json:"time_zone"`
	Weekdays        []int16    `json:"weekdays"`
	StartTime       civil.Time `json:"start_time"`
	DurationMinutes int32      `json:"duration_minutes"`
	StartsOn        civil.Date `json:"starts_on"`
	EndsOn          civil.Date `json:"ends_on"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}

func toResponse(c store.Class) classResponse {
	return classResponse{
		ID: c.ID, Name: c.Name, TimeZone: c.TimeZone, Weekdays: c.Weekdays, StartTime: c.StartTime,
		DurationMinutes: c.DurationMinutes, StartsOn: c.StartsOn, EndsOn: c.EndsOn, CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
}

// classSession is a session in the agenda of every class.
type classSession struct {
	ClassID   int64  `json:"class_id"`
	ClassName string `json:"class_name"`
	TimeZone  string `json:"time_zone"`
	Session
}

var (
	errNotFound       = httpx.NotFound("class_not_found")
	errChangeNotFound = httpx.NotFound("class_change_not_found")
)

// list answers GET /classes?limit=20&offset=0, sorted by name.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	classes, err := h.q.ListClasses(r.Context(), store.ListClassesParams{Limit: page.Limit, Offset: page.Offset})
	if err != nil {
		return fmt.Errorf("list classes: %w", err)
	}
	total, err := h.q.CountClasses(r.Context())
	if err != nil {
		return fmt.Errorf("count classes: %w", err)
	}
	data := make([]classResponse, len(classes))
	for i, c := range classes {
		data[i] = toResponse(c)
	}
	httpx.Write(w, http.StatusOK, httpx.List[classResponse]{
		Data: data,
		Meta: httpx.PageMeta{Limit: page.Limit, Offset: page.Offset, Total: total},
	})
	return nil
}

// class finds the class of the route ({id}) and loads its zone.
func (h *Handler) class(r *http.Request) (store.Class, *time.Location, error) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return store.Class{}, nil, err
	}
	c, err := h.q.GetClass(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Class{}, nil, errNotFound
	}
	if err != nil {
		return store.Class{}, nil, fmt.Errorf("get class %d: %w", id, err)
	}
	loc, err := civil.LoadLocation(c.TimeZone)
	if err != nil {
		return store.Class{}, nil, fmt.Errorf("zone of class %d: %w", c.ID, err)
	}
	return c, loc, nil
}

// get answers GET /classes/{id}.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	c, _, err := h.class(r)
	if err != nil {
		return err
	}
	httpx.Write(w, http.StatusOK, toResponse(c))
	return nil
}

// create answers POST /classes with {name, time_zone, weekdays, start_time, duration_minutes,
// starts_on, ends_on?}.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var in classInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}
	c, err := h.q.CreateClass(r.Context(), store.CreateClassParams{
		Name: in.Name, TimeZone: in.TimeZone, Weekdays: in.weekdays(), StartTime: in.StartTime,
		DurationMinutes: int32(in.DurationMinutes), StartsOn: in.StartsOn, EndsOn: in.EndsOn,
	})
	if err != nil {
		return fmt.Errorf("create class: %w", err)
	}
	w.Header().Set("Location", fmt.Sprintf("/classes/%d", c.ID))
	httpx.Write(w, http.StatusCreated, toResponse(c))
	return nil
}

// update answers PUT /classes/{id}, with the same body. The changes to sessions that are no longer
// in the rule stay stored, but do not apply.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in classInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}
	c, err := h.q.UpdateClass(r.Context(), store.UpdateClassParams{
		ID: id, Name: in.Name, TimeZone: in.TimeZone, Weekdays: in.weekdays(), StartTime: in.StartTime,
		DurationMinutes: int32(in.DurationMinutes), StartsOn: in.StartsOn, EndsOn: in.EndsOn,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("update class %d: %w", id, err)
	}
	httpx.Write(w, http.StatusOK, toResponse(c))
	return nil
}

// delete answers DELETE /classes/{id}, with its changes.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	rows, err := h.q.DeleteClass(r.Context(), id)
	if err != nil {
		return fmt.Errorf("delete class %d: %w", id, err)
	}
	if rows == 0 {
		return errNotFound
	}
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// changes returns the changes of the classes that touch the days from from to to, by class.
func (h *Handler) changes(ctx context.Context, ids []int64, from, to civil.Date) (map[int64][]Change, error) {
	rows, err := h.q.ListSessionChanges(ctx, store.ListSessionChangesParams{ClassIds: ids, From: from, To: to})
	if err != nil {
		return nil, fmt.Errorf("list the session changes: %w", err)
	}
	changes := make(map[int64][]Change, len(ids))
	for _, row := range rows {
		changes[row.ClassID] = append(changes[row.ClassID], Change{
			Date: row.Date, Canceled: row.Canceled, LocalStartsAt: row.LocalStartsAt, Note: row.Note,
		})
	}
	return changes, nil
}

func ruleOf(c store.Class) Rule {
	return rule(c.Weekdays, c.StartTime, c.DurationMinutes, c.StartsOn, c.EndsOn)
}

// sessions answers GET /classes/{id}/sessions?from=&to=: the sessions of those days of the class's
// clock (from today, 30 days, by default), canceled ones included and moved ones at their new hour.
func (h *Handler) sessions(w http.ResponseWriter, r *http.Request) error {
	c, loc, err := h.class(r)
	if err != nil {
		return err
	}
	rng, err := httpx.ParseDateRange(r, civil.DateOf(time.Now().In(loc)), sessionDays, maxSessionDays)
	if err != nil {
		return err
	}
	changes, err := h.changes(r.Context(), []int64{c.ID}, rng.From, rng.To)
	if err != nil {
		return err
	}
	httpx.Write(w, http.StatusOK, map[string]any{
		"time_zone": c.TimeZone,
		"data":      Sessions(ruleOf(c), changes[c.ID], rng.From, rng.To, loc),
	})
	return nil
}

// agenda answers GET /classes/sessions?from=&to=: the sessions of every class in those days (each
// one on the clock of its class), sorted by when they start. Without from, today of APP_TIME_ZONE.
func (h *Handler) agenda(w http.ResponseWriter, r *http.Request) error {
	rng, err := httpx.ParseDateRange(r, civil.DateOf(time.Now().In(h.appZone)), agendaDays, maxAgendaDays)
	if err != nil {
		return err
	}
	classes, err := h.q.ListClassesBetween(r.Context(), store.ListClassesBetweenParams{From: rng.From, To: rng.To})
	if err != nil {
		return fmt.Errorf("list classes: %w", err)
	}
	ids := make([]int64, len(classes))
	for i, c := range classes {
		ids[i] = c.ID
	}
	changes, err := h.changes(r.Context(), ids, rng.From, rng.To)
	if err != nil {
		return err
	}

	data := []classSession{}
	for _, c := range classes {
		loc, err := civil.LoadLocation(c.TimeZone)
		if err != nil {
			return fmt.Errorf("zone of class %d: %w", c.ID, err)
		}
		for _, s := range Sessions(ruleOf(c), changes[c.ID], rng.From, rng.To, loc) {
			data = append(data, classSession{ClassID: c.ID, ClassName: c.Name, TimeZone: c.TimeZone, Session: s})
		}
	}
	slices.SortFunc(data, func(a, b classSession) int { return a.StartsAt.Compare(b.StartsAt) })
	httpx.Write(w, http.StatusOK, map[string][]classSession{"data": data})
	return nil
}

// pathDate reads the {date} of the route (2006-01-02).
func pathDate(r *http.Request) (civil.Date, error) {
	d, err := civil.ParseDate(r.PathValue("date"))
	if err != nil {
		return civil.Date{}, httpx.Validation(httpx.Fields{"date": i18n.M("validation.date")})
	}
	return d, nil
}

// putChange answers PUT /classes/{id}/sessions/{date} with {canceled: true} or {starts_at}, and an
// optional note: it cancels or moves the session of that date, which must be one of the rule.
func (h *Handler) putChange(w http.ResponseWriter, r *http.Request) error {
	c, loc, err := h.class(r)
	if err != nil {
		return err
	}
	date, err := pathDate(r)
	if err != nil {
		return err
	}
	var in changeInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}
	if !ruleOf(c).isSession(date) {
		return httpx.Validation(httpx.Fields{"date": i18n.M("validation.not_a_session")})
	}

	row, err := h.q.UpsertSessionChange(r.Context(), store.UpsertSessionChangeParams{
		ClassID: c.ID, Date: date, Canceled: in.Canceled, LocalStartsAt: in.StartsAt, Note: in.Note,
	})
	if _, ok := db.IsForeignKeyViolation(err); ok {
		return errNotFound // deleted meanwhile
	}
	if err != nil {
		return fmt.Errorf("save the change of class %d: %w", c.ID, err)
	}
	// The session as it is now.
	change := Change{Date: row.Date, Canceled: row.Canceled, LocalStartsAt: row.LocalStartsAt, Note: row.Note}
	day := row.Date
	if !row.LocalStartsAt.IsZero() {
		day = row.LocalStartsAt.Date()
	}
	sessions := Sessions(ruleOf(c), []Change{change}, day, day, loc)
	httpx.Write(w, http.StatusOK, sessions[0])
	return nil
}

// deleteChange answers DELETE /classes/{id}/sessions/{date}: the session goes back to the rule.
func (h *Handler) deleteChange(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	date, err := pathDate(r)
	if err != nil {
		return err
	}
	rows, err := h.q.DeleteSessionChange(r.Context(), store.DeleteSessionChangeParams{ClassID: id, Date: date})
	if err != nil {
		return fmt.Errorf("delete the change of class %d: %w", id, err)
	}
	if rows == 0 {
		return errChangeNotFound
	}
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}
