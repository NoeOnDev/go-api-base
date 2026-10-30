package httpx

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Pagination by cursor (?limit=20&after=…), for "load more" lists and long ones. Instead of
// skipping rows with OFFSET, each page starts after the last item of the previous one
// (WHERE id < $after), so it is just as fast on page 1 as on page 1000, and an item inserted
// meanwhile does not shift the rest. It has no total and no page numbers: counting is exactly
// what it avoids. For tables with pages and a total, use ParsePage. See docs/adding-a-resource.md.

// maxCursorLen is the longest accepted cursor: enough for a few sort keys.
const maxCursorLen = 200

// CursorPage is the pagination of a list by cursor.
type CursorPage struct {
	Limit int32
	// After is the cursor of the previous page (its meta.next), or "" for the first one. Read it
	// with DecodeCursor.
	After string
}

// CursorMeta comes with every list by cursor. Next is the cursor of the next page, to send as
// ?after=; null on the last one.
type CursorMeta struct {
	Limit int32   `json:"limit"`
	Next  *string `json:"next"`
}

// CursorList is the response of a list by cursor: {"data": [...], "meta": {"limit", "next"}}.
type CursorList[T any] struct {
	Data []T        `json:"data"`
	Meta CursorMeta `json:"meta"`
}

// ParseCursorPage reads limit and after from the URL, with the same limit rules as ParsePage.
func ParseCursorPage(r *http.Request) (CursorPage, error) {
	page := CursorPage{Limit: DefaultLimit, After: r.URL.Query().Get("after")}
	fields := Fields{}
	if limit, msg := parseLimit(r); !msg.IsZero() {
		fields["limit"] = msg
	} else if limit > 0 {
		page.Limit = limit
	}
	if len(page.After) > maxCursorLen {
		fields["after"] = i18n.M("validation.invalid_cursor")
	}
	if len(fields) > 0 {
		return CursorPage{}, Validation(fields)
	}
	return page, nil
}

// EncodeCursor turns the sort key of the last item into an opaque cursor: JSON in base64url.
// The frontend sends it back as is; that way the key can change (more columns) without changing
// the API.
func EncodeCursor(key any) string {
	b, err := json.Marshal(key)
	if err != nil {
		panic("httpx.EncodeCursor: " + err.Error()) // the key is a struct of the code: a code error
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCursor reads a cursor of EncodeCursor into T. An empty cursor (the first page) is nil.
// A made-up or damaged one is a validation error on after.
func DecodeCursor[T any](cursor string) (*T, error) {
	if cursor == "" {
		return nil, nil
	}
	invalid := Validation(Fields{"after": i18n.M("validation.invalid_cursor")})
	b, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return nil, invalid
	}
	var key T
	if err := json.Unmarshal(b, &key); err != nil {
		return nil, invalid
	}
	return &key, nil
}

// NewCursorList builds the response of a page. The query must ask for limit+1 rows: if one is
// left over, there is a next page, whose cursor is the key of the last item shown.
func NewCursorList[T any](items []T, limit int32, key func(T) any) CursorList[T] {
	list := CursorList[T]{Data: items, Meta: CursorMeta{Limit: limit}}
	if len(items) > int(limit) {
		list.Data = items[:limit]
		next := EncodeCursor(key(list.Data[limit-1]))
		list.Meta.Next = &next
	}
	return list
}
