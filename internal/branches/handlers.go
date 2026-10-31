// Package branches is the example of business hours: the branches of a business (a store, a clinic,
// a restaurant), each with its weekly hours in its own time zone, and the exceptions of some dates
// (holidays, days with other hours). It answers which are the real hours of some days and whether
// a branch is open now. Reads are public; writes, only for administrators, like categories.
//
// The hours are wall-clock times of the branch's zone (see "Modeling schedules" in docs/time.md);
// schedule.go turns them into instants.
package branches

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/civil"
	"github.com/NoeOnDev/go-api-base/internal/db"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Limits of the ranges of days that can be asked for.
const (
	scheduleDays    = 7  // GET /branches/{id}/schedule without ?to=
	maxScheduleDays = 62 // about two months
)

// Handler groups the branch endpoints.
type Handler struct {
	db *pgxpool.Pool // for the transactions
	q  *store.Queries
}

// NewHandler creates the handler.
func NewHandler(pool *pgxpool.Pool, q *store.Queries) *Handler {
	return &Handler{db: pool, q: q}
}

type branchResponse struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	TimeZone string `json:"time_zone"`
	// Hours are the weekly hours, sorted by day and hour.
	Hours     []WeeklyHours `json:"hours"`
	CreatedAt time.Time     `json:"created_at"`
	UpdatedAt time.Time     `json:"updated_at"`
}

func toResponse(b store.Branch, hours []WeeklyHours) branchResponse {
	if hours == nil {
		hours = []WeeklyHours{}
	}
	return branchResponse{ID: b.ID, Name: b.Name, TimeZone: b.TimeZone, Hours: hours, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt}
}

var (
	errNotFound          = httpx.NotFound("branch_not_found")
	errExceptionNotFound = httpx.NotFound("branch_exception_not_found")
	errInUse             = httpx.Conflict("branch_in_use", "")
)

// list answers GET /branches?limit=20&offset=0, sorted by name, each one with its weekly hours.
func (h *Handler) list(w http.ResponseWriter, r *http.Request) error {
	page, err := httpx.ParsePage(r)
	if err != nil {
		return err
	}
	branches, err := h.q.ListBranches(r.Context(), store.ListBranchesParams{Limit: page.Limit, Offset: page.Offset})
	if err != nil {
		return fmt.Errorf("list branches: %w", err)
	}
	total, err := h.q.CountBranches(r.Context())
	if err != nil {
		return fmt.Errorf("count branches: %w", err)
	}

	ids := make([]int64, len(branches))
	for i, b := range branches {
		ids[i] = b.ID
	}
	hours, err := h.weeklyHours(r.Context(), ids...)
	if err != nil {
		return err
	}

	data := make([]branchResponse, len(branches))
	for i, b := range branches {
		data[i] = toResponse(b, hours[b.ID])
	}
	httpx.Write(w, http.StatusOK, httpx.List[branchResponse]{
		Data: data,
		Meta: httpx.PageMeta{Limit: page.Limit, Offset: page.Offset, Total: total},
	})
	return nil
}

// weeklyHours returns the weekly hours of the branches, by branch.
func (h *Handler) weeklyHours(ctx context.Context, ids ...int64) (map[int64][]WeeklyHours, error) {
	rows, err := h.q.ListBranchHours(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("list the hours of the branches: %w", err)
	}
	hours := make(map[int64][]WeeklyHours, len(ids))
	for _, row := range rows {
		hours[row.BranchID] = append(hours[row.BranchID], WeeklyHours{
			Weekday: time.Weekday(row.Weekday),
			Hours:   Hours{OpensAt: row.OpensAt, ClosesAt: row.ClosesAt},
		})
	}
	return hours, nil
}

// branch finds the branch of the route ({id}).
func (h *Handler) branch(r *http.Request) (store.Branch, error) {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return store.Branch{}, err
	}
	b, err := h.q.GetBranch(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Branch{}, errNotFound
	}
	if err != nil {
		return store.Branch{}, fmt.Errorf("get branch %d: %w", id, err)
	}
	return b, nil
}

// get answers GET /branches/{id}.
func (h *Handler) get(w http.ResponseWriter, r *http.Request) error {
	b, err := h.branch(r)
	if err != nil {
		return err
	}
	hours, err := h.weeklyHours(r.Context(), b.ID)
	if err != nil {
		return err
	}
	httpx.Write(w, http.StatusOK, toResponse(b, hours[b.ID]))
	return nil
}

// create answers POST /branches with {name, time_zone}. The hours are set with PUT .../hours.
func (h *Handler) create(w http.ResponseWriter, r *http.Request) error {
	var in branchInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	b, err := h.q.CreateBranch(r.Context(), store.CreateBranchParams{Name: in.Name, TimeZone: in.TimeZone})
	if err != nil {
		return fmt.Errorf("create branch: %w", err)
	}
	w.Header().Set("Location", fmt.Sprintf("/branches/%d", b.ID))
	httpx.Write(w, http.StatusCreated, toResponse(b, nil))
	return nil
}

// update answers PUT /branches/{id} with {name, time_zone}. Changing the zone keeps the hours of the
// clock: 9:00 is still 9:00, now in the new zone.
func (h *Handler) update(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in branchInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	b, err := h.q.UpdateBranch(r.Context(), store.UpdateBranchParams{ID: id, Name: in.Name, TimeZone: in.TimeZone})
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("update branch %d: %w", id, err)
	}
	hours, err := h.weeklyHours(r.Context(), b.ID)
	if err != nil {
		return err
	}
	httpx.Write(w, http.StatusOK, toResponse(b, hours[b.ID]))
	return nil
}

// delete answers DELETE /branches/{id}. Its hours and exceptions go with it; if something else
// references it (the appointments), it answers 409.
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	rows, err := h.q.DeleteBranch(r.Context(), id)
	if _, ok := db.IsForeignKeyViolation(err); ok {
		return errInUse
	}
	if err != nil {
		return fmt.Errorf("delete branch %d: %w", id, err)
	}
	if rows == 0 {
		return errNotFound
	}
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// replaceHours answers PUT /branches/{id}/hours with {hours: [{weekday, opens_at, closes_at}]}: the
// whole week replaces the previous one, in a transaction. An empty list leaves the branch without
// hours.
func (h *Handler) replaceHours(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	var in hoursInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	weekly, err := in.validate()
	if err != nil {
		return err
	}

	err = pgx.BeginFunc(r.Context(), h.db, func(tx pgx.Tx) error {
		q := h.q.WithTx(tx)
		if _, err := q.LockBranch(r.Context(), id); errors.Is(err, pgx.ErrNoRows) {
			return errNotFound
		} else if err != nil {
			return fmt.Errorf("lock branch %d: %w", id, err)
		}
		if err := q.DeleteBranchHours(r.Context(), id); err != nil {
			return fmt.Errorf("delete the hours of branch %d: %w", id, err)
		}
		for _, wh := range weekly {
			err := q.CreateBranchHours(r.Context(), store.CreateBranchHoursParams{
				BranchID: id, Weekday: int16(wh.Weekday), OpensAt: wh.OpensAt, ClosesAt: wh.ClosesAt,
			})
			if err != nil {
				return fmt.Errorf("create the hours of branch %d: %w", id, err)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return h.get(w, r)
}

// location loads the zone of a branch. It was validated when stored.
func location(b store.Branch) (*time.Location, error) {
	loc, err := civil.LoadLocation(b.TimeZone)
	if err != nil {
		return nil, fmt.Errorf("zone of branch %d: %w", b.ID, err)
	}
	return loc, nil
}

// exceptions returns the exceptions of a branch between two dates.
func (h *Handler) exceptions(ctx context.Context, branchID int64, from, to civil.Date) ([]Exception, error) {
	rows, err := h.q.ListBranchExceptions(ctx, store.ListBranchExceptionsParams{BranchID: branchID, From: from, To: to})
	if err != nil {
		return nil, fmt.Errorf("list the exceptions of branch %d: %w", branchID, err)
	}
	exceptions := make([]Exception, len(rows))
	for i, row := range rows {
		exceptions[i] = toException(row.Date, row.OpensAt, row.ClosesAt, row.Note)
	}
	return exceptions, nil
}

func toException(date civil.Date, opensAt, closesAt civil.Time, note string) Exception {
	e := Exception{Date: date, Closed: opensAt.IsZero(), Note: note}
	if !e.Closed {
		e.Hours = &Hours{OpensAt: opensAt, ClosesAt: closesAt}
	}
	return e
}

// today returns the current date of the branch's clock.
func today(loc *time.Location) civil.Date {
	return civil.DateOf(time.Now().In(loc))
}

// listExceptions answers GET /branches/{id}/exceptions?from=&to=, by default the next two months.
func (h *Handler) listExceptions(w http.ResponseWriter, r *http.Request) error {
	b, err := h.branch(r)
	if err != nil {
		return err
	}
	loc, err := location(b)
	if err != nil {
		return err
	}
	rng, err := httpx.ParseDateRange(r, today(loc), maxScheduleDays, 366)
	if err != nil {
		return err
	}
	exceptions, err := h.exceptions(r.Context(), b.ID, rng.From, rng.To)
	if err != nil {
		return err
	}
	httpx.Write(w, http.StatusOK, map[string][]Exception{"data": exceptions})
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

// putException answers PUT /branches/{id}/exceptions/{date} with {closed: true} or {opens_at,
// closes_at}, and an optional note. It replaces the exception of that date, if there was one.
func (h *Handler) putException(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	date, err := pathDate(r)
	if err != nil {
		return err
	}
	var in exceptionInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err
	}
	in.normalize()
	if err := in.validate(); err != nil {
		return err
	}

	row, err := h.q.UpsertBranchException(r.Context(), store.UpsertBranchExceptionParams{
		BranchID: id, Date: date, OpensAt: in.OpensAt, ClosesAt: in.ClosesAt, Note: in.Note,
	})
	if _, ok := db.IsForeignKeyViolation(err); ok {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("save the exception of branch %d: %w", id, err)
	}
	httpx.Write(w, http.StatusOK, toException(row.Date, row.OpensAt, row.ClosesAt, row.Note))
	return nil
}

// deleteException answers DELETE /branches/{id}/exceptions/{date}: that date goes back to the
// weekly hours.
func (h *Handler) deleteException(w http.ResponseWriter, r *http.Request) error {
	id, err := httpx.PathID(r, "id")
	if err != nil {
		return err
	}
	date, err := pathDate(r)
	if err != nil {
		return err
	}
	rows, err := h.q.DeleteBranchException(r.Context(), store.DeleteBranchExceptionParams{BranchID: id, Date: date})
	if err != nil {
		return fmt.Errorf("delete the exception of branch %d: %w", id, err)
	}
	if rows == 0 {
		return errExceptionNotFound
	}
	httpx.Write(w, http.StatusNoContent, nil)
	return nil
}

// Schedule returns the hours of the branch for each date from from to to, and its zone. The
// appointments use it to know when there is availability.
func (h *Handler) Schedule(ctx context.Context, b store.Branch, from, to civil.Date) ([]Day, *time.Location, error) {
	loc, err := location(b)
	if err != nil {
		return nil, nil, err
	}
	hours, err := h.weeklyHours(ctx, b.ID)
	if err != nil {
		return nil, nil, err
	}
	exceptions, err := h.exceptions(ctx, b.ID, from, to)
	if err != nil {
		return nil, nil, err
	}
	return Days(hours[b.ID], exceptions, from, to, loc), loc, nil
}

// schedule answers GET /branches/{id}/schedule?from=&to=: the real hours of each date, in the local
// time of the branch and in instants. Without from, from today of the branch; without to, a week.
func (h *Handler) schedule(w http.ResponseWriter, r *http.Request) error {
	b, err := h.branch(r)
	if err != nil {
		return err
	}
	loc, err := location(b)
	if err != nil {
		return err
	}
	rng, err := httpx.ParseDateRange(r, today(loc), scheduleDays, maxScheduleDays)
	if err != nil {
		return err
	}
	days, _, err := h.Schedule(r.Context(), b, rng.From, rng.To)
	if err != nil {
		return err
	}
	httpx.Write(w, http.StatusOK, map[string]any{"time_zone": b.TimeZone, "data": days})
	return nil
}

// status answers GET /branches/{id}/status: whether it is open now, until when, and when it opens
// next.
func (h *Handler) status(w http.ResponseWriter, r *http.Request) error {
	b, err := h.branch(r)
	if err != nil {
		return err
	}
	loc, err := location(b)
	if err != nil {
		return err
	}
	now := time.Now()
	d := today(loc)
	hours, err := h.weeklyHours(r.Context(), b.ID)
	if err != nil {
		return err
	}
	exceptions, err := h.exceptions(r.Context(), b.ID, d.AddDays(-1), d.AddDays(statusDays))
	if err != nil {
		return err
	}
	httpx.Write(w, http.StatusOK, StatusAt(now, hours[b.ID], exceptions, loc))
	return nil
}
