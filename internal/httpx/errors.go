// Package httpx has the HTTP utilities shared by the whole API: reading and writing JSON, the
// single error format and the middlewares.
package httpx

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Error is an API error with its HTTP status. It is always answered as
//
//	{"error": {"code": "...", "message": "...", "fields": {...}}}
//
// so the frontend handles every error the same way. Code identifies the error and is also the
// key of its message in the internal/i18n catalogs ("errors.<code>"): the message is translated
// when the response is written, in the language of the request. See docs/i18n.md.
type Error struct {
	Status int
	Code   string
	// Args fill in the message: key/value pairs, as in slog ("field", "title").
	Args []any
	// Fields maps each field to its message (in validation errors).
	Fields Fields

	// Err is the internal cause. It is logged, but never sent to the client.
	Err error
}

// Fields are the per-field messages of a validation error.
type Fields map[string]i18n.Message

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

// Message is the error message in the language of t.
func (e *Error) Message(t i18n.Translator) string { return t.T("errors."+e.Code, e.Args...) }

// FieldMessages are the per-field messages in the language of t.
func (e *Error) FieldMessages(t i18n.Translator) map[string]string {
	if len(e.Fields) == 0 {
		return nil
	}
	fields := make(map[string]string, len(e.Fields))
	for name, m := range e.Fields {
		fields[name] = t.Message(m)
	}
	return fields
}

// BadRequest means the request is malformed (for example, invalid JSON).
// args fill in the message (see Error.Args).
func BadRequest(code string, args ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: code, Args: args}
}

// NotFound means the requested resource does not exist, for example NotFound("note_not_found").
func NotFound(code string) *Error {
	return &Error{Status: http.StatusNotFound, Code: code}
}

// Unauthorized means the request needs a valid session (or that the credentials are not valid).
func Unauthorized(code string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: code}
}

// Forbidden means there is a session, but the user is not allowed to do this.
func Forbidden(code string) *Error {
	return &Error{Status: http.StatusForbidden, Code: code}
}

// Conflict means the operation clashes with the current state, for example
// Conflict("email_taken", "email"). field is optional: when given, the message also goes in
// fields so the frontend shows it next to the field.
func Conflict(code, field string) *Error {
	e := &Error{Status: http.StatusConflict, Code: code}
	if field != "" {
		e.Fields = Fields{field: i18n.M("errors." + code)}
	}
	return e
}

// TooManyRequests means the client exceeded the rate limit.
func TooManyRequests() *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "too_many_requests"}
}

// Validation means the data is valid JSON but breaks the rules.
// fields maps each field to its message, for example {"title": i18n.M("validation.required")}.
func Validation(fields Fields) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Fields: fields}
}

// Internal wraps an unexpected error. The client only sees a generic message.
func Internal(err error) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: "internal_error", Err: err}
}

// errorBody is an error as the client receives it.
type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// WriteError answers with the API error format, with the messages in the language of the
// request. Any error that is not an *Error is treated as internal. 5xx errors are logged with
// their real cause.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		apiErr = Internal(err)
	}

	if apiErr.Status >= http.StatusInternalServerError {
		LoggerFrom(r.Context()).Error("error interno", "error", err)
	}
	writeBody(w, r, apiErr)
}

// writeBody writes the error, translated to the language of the request, without logging it.
func writeBody(w http.ResponseWriter, r *http.Request, e *Error) {
	t := i18n.FromContext(r.Context())
	Write(w, e.Status, map[string]errorBody{"error": {
		Code:    e.Code,
		Message: e.Message(t),
		Fields:  e.FieldMessages(t),
	}})
}

// HandlerFunc is a handler that returns an error instead of writing it.
// That way every handler ends with a plain "return err" and the error format stays in one place.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handle turns a HandlerFunc into an http.HandlerFunc that answers errors with WriteError.
func Handle(fn HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			WriteError(w, r, err)
		}
	}
}
