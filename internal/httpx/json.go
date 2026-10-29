package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// MaxBodyBytes es el tamaño máximo aceptado para el cuerpo de una petición (1 MB).
const MaxBodyBytes = 1 << 20

// Decode lee el cuerpo JSON de la petición en dst. Rechaza cuerpos demasiado grandes,
// campos desconocidos y más de un valor JSON, y traduce los errores a mensajes claros
// para el cliente (siempre como *Error).
func Decode(w http.ResponseWriter, r *http.Request, dst any) error {
	r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)

	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}

	// Un segundo Decode debe llegar al final: el cuerpo tiene que contener un solo valor JSON.
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return BadRequest("el cuerpo debe contener un solo objeto JSON")
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
		// encoding/json no exporta un tipo para este error, así que se reconoce por su mensaje.
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return BadRequest("unknown_field", "field", field)
	case errors.As(err, &maxBytesErr):
		return &Error{Status: http.StatusRequestEntityTooLarge, Code: "payload_too_large", Args: []any{"limit", maxBytesErr.Limit}}
	default:
		return BadRequest("unreadable_body")
	}
}

// Write responde con status y v codificado como JSON. Si v es nil, solo envía el status
// (útil para 204 No Content).
func Write(w http.ResponseWriter, status int, v any) {
	if v == nil {
		w.WriteHeader(status)
		return
	}

	// Se codifica antes de enviar nada. Las respuestas son structs propios, así que si no se
	// pueden codificar es un error de programación: el panic llega a Recover, que lo registra
	// con el request_id y responde un 500 correcto (todavía no se envió el status).
	body, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Errorf("httpx.Write: codificar la respuesta: %w", err))
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	// Un error al escribir casi siempre es un cliente que cerró la conexión: no hay nada que hacer.
	_, _ = w.Write(append(body, '\n'))
}
