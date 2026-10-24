// Package httpx reúne las utilidades HTTP comunes a toda la API: lectura y escritura de JSON,
// el formato único de errores y los middlewares.
package httpx

import (
	"errors"
	"fmt"
	"net/http"
)

// Error es un error de la API con su código HTTP. Se serializa siempre como
//
//	{"error": {"code": "...", "message": "...", "fields": {...}}}
//
// para que el frontend maneje todos los errores de la misma forma.
type Error struct {
	Status  int               `json:"-"`
	Code    string            `json:"code"`
	Message string            `json:"message"`
	Fields  map[string]string `json:"fields,omitempty"`

	// Err es la causa interna. Se registra en el log, pero nunca se envía al cliente.
	Err error `json:"-"`
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Err)
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// BadRequest indica que la petición está mal formada (por ejemplo, JSON inválido).
func BadRequest(message string) *Error {
	return &Error{Status: http.StatusBadRequest, Code: "bad_request", Message: message}
}

// NotFound indica que el recurso pedido no existe.
func NotFound(message string) *Error {
	return &Error{Status: http.StatusNotFound, Code: "not_found", Message: message}
}

// Unauthorized indica que la petición necesita una sesión válida.
func Unauthorized(message string) *Error {
	return &Error{Status: http.StatusUnauthorized, Code: "unauthorized", Message: message}
}

// Forbidden indica que hay sesión, pero el usuario no tiene permiso para esta acción.
func Forbidden(message string) *Error {
	return &Error{Status: http.StatusForbidden, Code: "forbidden", Message: message}
}

// Conflict indica que la operación choca con el estado actual (por ejemplo, un valor único repetido).
// field es opcional: si se indica, el mensaje también va en fields para que el frontend lo muestre en el campo.
func Conflict(message, field string) *Error {
	e := &Error{Status: http.StatusConflict, Code: "conflict", Message: message}
	if field != "" {
		e.Fields = map[string]string{field: message}
	}
	return e
}

// TooManyRequests indica que el cliente superó el límite de peticiones.
func TooManyRequests() *Error {
	return &Error{
		Status:  http.StatusTooManyRequests,
		Code:    "too_many_requests",
		Message: "demasiados intentos, esperá un momento y volvé a probar",
	}
}

// Validation indica que los datos son correctos como JSON pero no cumplen las reglas.
// fields asocia cada campo con su mensaje de error.
func Validation(fields map[string]string) *Error {
	return &Error{
		Status:  http.StatusUnprocessableEntity,
		Code:    "validation_failed",
		Message: "los datos enviados no son válidos",
		Fields:  fields,
	}
}

// Internal envuelve un error inesperado. El cliente solo ve un mensaje genérico.
func Internal(err error) *Error {
	return &Error{
		Status:  http.StatusInternalServerError,
		Code:    "internal_error",
		Message: "error interno del servidor",
		Err:     err,
	}
}

// WriteError responde con el formato de error de la API. Cualquier error que no sea *Error
// se trata como interno. Los errores 5xx se registran con la causa real.
func WriteError(w http.ResponseWriter, r *http.Request, err error) {
	var apiErr *Error
	if !errors.As(err, &apiErr) {
		apiErr = Internal(err)
	}

	if apiErr.Status >= http.StatusInternalServerError {
		LoggerFrom(r.Context()).Error("error interno", "error", err)
	}

	Write(w, apiErr.Status, map[string]*Error{"error": apiErr})
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
