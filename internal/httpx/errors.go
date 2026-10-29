// Package httpx reúne las utilidades HTTP comunes a toda la API: lectura y escritura de JSON,
// el formato único de errores y los middlewares.
package httpx

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/i18n"
)

// Error es un error de la API con su código HTTP. Se responde siempre como
//
//	{"error": {"code": "...", "message": "...", "fields": {...}}}
//
// para que el frontend maneje todos los errores de la misma forma. Code identifica el error y es
// también la clave de su mensaje en los catálogos de internal/i18n ("errors.<code>"): el mensaje
// se traduce al escribir la respuesta, en el idioma de la petición. Ver docs/i18n.md.
type Error struct {
	Status int
	Code   string
	// Args completan el mensaje: pares clave, valor, como en slog ("field", "title").
	Args []any
	// Fields asocia cada campo con su mensaje (en los errores de validación).
	Fields Fields

	// Err es la causa interna. Se registra en el log, pero nunca se envía al cliente.
	Err error
}

// Fields son los mensajes por campo de un error de validación.
type Fields map[string]i18n.Message

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

// Message es el mensaje del error en el idioma de t.
func (e *Error) Message(t i18n.Translator) string { return t.T("errors."+e.Code, e.Args...) }

// FieldMessages son los mensajes por campo en el idioma de t.
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

// BadRequest indica que la petición está mal formada (por ejemplo, JSON inválido).
// args completan el mensaje (ver Error.Args).
func BadRequest(code string, args ...any) *Error {
	return &Error{Status: http.StatusBadRequest, Code: code, Args: args}
}

// NotFound indica que el recurso pedido no existe, por ejemplo NotFound("note_not_found").
func NotFound(code string) *Error {
	return &Error{Status: http.StatusNotFound, Code: code}
}

// Unauthorized indica que la petición necesita una sesión válida (o que las credenciales no lo son).
func Unauthorized(code string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: code}
}

// Forbidden indica que hay sesión, pero el usuario no tiene permiso para esta acción.
func Forbidden(code string) *Error {
	return &Error{Status: http.StatusForbidden, Code: code}
}

// Conflict indica que la operación choca con el estado actual, por ejemplo Conflict("email_taken",
// "email"). field es opcional: si se indica, el mensaje también va en fields para que el frontend
// lo muestre en el campo.
func Conflict(code, field string) *Error {
	e := &Error{Status: http.StatusConflict, Code: code}
	if field != "" {
		e.Fields = Fields{field: i18n.M("errors." + code)}
	}
	return e
}

// TooManyRequests indica que el cliente superó el límite de peticiones.
func TooManyRequests() *Error {
	return &Error{Status: http.StatusTooManyRequests, Code: "too_many_requests"}
}

// Validation indica que los datos son correctos como JSON pero no cumplen las reglas.
// fields asocia cada campo con su mensaje, por ejemplo {"title": i18n.M("validation.required")}.
func Validation(fields Fields) *Error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Fields: fields}
}

// Internal envuelve un error inesperado. El cliente solo ve un mensaje genérico.
func Internal(err error) *Error {
	return &Error{Status: http.StatusInternalServerError, Code: "internal_error", Err: err}
}

// errorBody es un error como lo recibe el cliente.
type errorBody struct {
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`
}

// WriteError responde con el formato de error de la API, con los mensajes en el idioma de la
// petición. Cualquier error que no sea *Error se trata como interno. Los errores 5xx se
// registran con la causa real.
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

// writeBody escribe el error, traducido al idioma de la petición, sin registrarlo.
func writeBody(w http.ResponseWriter, r *http.Request, e *Error) {
	t := i18n.FromContext(r.Context())
	Write(w, e.Status, map[string]errorBody{"error": {
		Code:    e.Code,
		Message: e.Message(t),
		Fields:  e.FieldMessages(t),
	}})
}

// HandlerFunc es un handler que devuelve un error en lugar de escribirlo.
// Así cada handler termina con un simple "return err" y el formato de error queda centralizado.
type HandlerFunc func(w http.ResponseWriter, r *http.Request) error

// Handle convierte un HandlerFunc en un http.HandlerFunc que responde los errores con WriteError.
func Handle(fn HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := fn(w, r); err != nil {
			WriteError(w, r, err)
		}
	}
}
