package httpx

import (
	"net/http/httptest"
	"testing"
)

type testCursor struct {
	ID int64 `json:"id"`
}

func TestCursorRoundTrip(t *testing.T) {
	got, err := DecodeCursor[testCursor](EncodeCursor(testCursor{ID: 42}))
	if err != nil || got == nil || got.ID != 42 {
		t.Errorf("DecodeCursor = %+v, %v", got, err)
	}
	if got, err := DecodeCursor[testCursor](""); got != nil || err != nil {
		t.Errorf("the first page has no cursor: %+v, %v", got, err)
	}
	for _, bad := range []string{"not base64!", EncodeCursor("a string, not the struct")} {
		if _, err := DecodeCursor[testCursor](bad); err == nil {
			t.Errorf("%q should be an invalid cursor", bad)
		}
	}
}

func TestNewCursorList(t *testing.T) {
	key := func(n int) any { return testCursor{ID: int64(n)} }

	// limit+1 rows: there is a next page, after the last one shown.
	list := NewCursorList([]int{9, 8, 7}, 2, key)
	if len(list.Data) != 2 || list.Meta.Next == nil {
		t.Fatalf("list = %+v", list)
	}
	if next, _ := DecodeCursor[testCursor](*list.Meta.Next); next.ID != 8 {
		t.Errorf("next = %+v, want the last one shown (8)", next)
	}

	// The last page.
	if list := NewCursorList([]int{1}, 2, key); len(list.Data) != 1 || list.Meta.Next != nil {
		t.Errorf("last page = %+v", list)
	}
}

func TestParseCursorPage(t *testing.T) {
	page, err := ParseCursorPage(httptest.NewRequest("GET", "/x?limit=5&after=abc", nil))
	if err != nil || page.Limit != 5 || page.After != "abc" {
		t.Errorf("page = %+v, %v", page, err)
	}
	if page, err := ParseCursorPage(httptest.NewRequest("GET", "/x", nil)); err != nil || page.Limit != DefaultLimit {
		t.Errorf("default page = %+v, %v", page, err)
	}
	if _, err := ParseCursorPage(httptest.NewRequest("GET", "/x?limit=500", nil)); err == nil {
		t.Error("a limit over MaxLimit should fail")
	}
}
