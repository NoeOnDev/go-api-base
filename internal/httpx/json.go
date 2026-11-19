package httpx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"
)

// MaxBodyBytes is the largest accepted request body (1 MB).
const MaxBodyBytes = 1 << 20

// Decode reads the JSON body of the request into dst. It rejects bodies that are too large,
// unknown fields, more than one JSON value and the NUL character in a text, and turns the errors
// into clear messages for the client (always as an *Error).
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	if err != nil {
		return decodeError(err)
	}

	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}

	// A second Decode must reach the end: the body must hold a single JSON value.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("multiple_json_values")
	}

	// PostgreSQL does not store the NUL character in a text: without this, it would be a 500.
	if hasEscapedNUL(body) {
		return BadRequest("invalid_characters")
	}

	return nil
}

// hasEscapedNUL reports whether a valid JSON has the NUL character in one of its texts. In JSON it
// can only be written as \u0000 (the byte itself is not valid inside a string), so that escape is
// looked for, skipping the character each backslash escapes: in \\u0000 the escaped one is the
// second backslash, and what follows is plain text.
func hasEscapedNUL(body []byte) bool {
	for i := 0; i+1 < len(body); i++ {
		if body[i] != '\\' {
			continue
		}
		if bytes.HasPrefix(body[i+1:], []byte("u0000")) {
			return true
		}
		i++
	}
	return false
}

// ValidText reports whether s can be stored as text: valid UTF-8 and without the NUL character,
// which PostgreSQL rejects. The body (Decode) and the URL (RejectInvalidText) are already checked;
// use it for a text that comes from somewhere else, like a header.
func ValidText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

func decodeError(err error) error {
	var (
		syntaxErr   *json.SyntaxError
		typeErr     *json.UnmarshalTypeError
		maxBytesErr *http.MaxBytesError
	)

	switch {
	case errors.Is(err, io.EOF):
		return BadRequest("empty_body")
	case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
		return BadRequest("malformed_json")
	case errors.As(err, &typeErr):
		if typeErr.Field != "" {
			return BadRequest("wrong_type", "field", typeErr.Field)
		}
		return BadRequest("wrong_body_type")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json exports no type for this error, so it is recognized by its message.
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return BadRequest("unknown_field", "field", field)
	case errors.As(err, &maxBytesErr):
		return &Error{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Args: []any{"limit", maxBytesErr.Limit}}
	default:
		return BadRequest("unreadable_body")
	}
}

// Write answers with status and v encoded as JSON. If v is nil, it only sends the status
// (useful for 204 No Content).
func Write(w http.ResponseWriter, status int, v any) {
	if v == nil {
		w.WriteHeader(status)
		return
	}

	// It is encoded before sending anything. Responses are our own structs, so failing to encode
	// one is a programming error: the panic reaches Recover, which logs it with the request_id and
	// answers a proper 500 (the status has not been sent yet).
	body, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Errorf("httpx.Write: encode the response: %w", err))
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// A write error is almost always a client that closed the connection: there is nothing to do.
	_, _ = w.Write(append(body, '\n'))
}
