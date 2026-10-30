package httpx

import (
	"net/http"
	"strconv"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Default pagination values for the lists.
const (
	DefaultLimit = 20
	MaxLimit     = 100
	// MaxOffset is the highest accepted offset. PostgreSQL reads and discards every row before the
	// offset, so a huge one makes the query slow. To go further than this (an export, a long
	// infinite scroll), paginate by cursor (ParseCursorPage).
	MaxOffset = 10_000
)

// Page is the pagination of a list (?limit=20&offset=40).
type Page struct {
	Limit  int32
	Offset int32
}

// PageMeta comes with every list so the frontend can build the pagination.
type PageMeta struct {
	Limit  int32 `json:"limit"`
	Offset int32 `json:"offset"`
	Total  int64 `json:"total"`
}

// List is the response of a list: {"data": [...], "meta": {...}}.
type List[T any] struct {
	Data []T      `json:"data"`
	Meta PageMeta `json:"meta"`
}

// ParsePage reads limit and offset from the URL, with default values.
// If they are invalid, it returns a validation error with the matching field.
func ParsePage(r *http.Request) (Page, error) {
	page := Page{Limit: DefaultLimit}
	fields := Fields{}

	if limit, msg := parseLimit(r); !msg.IsZero() {
		fields["limit"] = msg
	} else if limit > 0 {
		page.Limit = limit
	}

	if v := r.URL.Query().Get("offset"); v != "" {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil || n < 0 || n > MaxOffset {
			fields["offset"] = i18n.M("validation.integer_range", "min", 0, "max", MaxOffset)
		}
		page.Offset = int32(n)
	}

	if len(fields) > 0 {
		return Page{}, Validation(fields)
	}
	return page, nil
}

// parseLimit reads ?limit= (from 1 to MaxLimit). Without it, 0 and no message.
func parseLimit(r *http.Request) (int32, i18n.Message) {
	v := r.URL.Query().Get("limit")
	if v == "" {
		return 0, i18n.Message{}
	}
	n, err := strconv.ParseInt(v, 10, 32)
	if err != nil || n < 1 || n > MaxLimit {
		return 0, i18n.M("validation.integer_range", "min", 1, "max", MaxLimit)
	}
	return int32(n), i18n.Message{}
}

// PathID reads a numeric ID from the path, for example {id} in "GET /notes/{id}".
func PathID(r *http.Request, name string) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil || id < 1 {
		return 0, BadRequest("invalid_path_id", "name", name)
	}
	return id, nil
}
