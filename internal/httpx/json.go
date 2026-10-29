package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxBodyBytes is the largest accepted request body (1 MB).
const MaxBodyBytes = 1 << 20

// Decode reads the JSON body of the request into dst. It rejects bodies that are too large,
// unknown fields and more than one JSON value, and turns the errors into clear messages for the
// client (always as an *Error).
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}

	// A second Decode must reach the end: the body must hold a single JSON value.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("multiple_json_values")
	}

	return nil
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
