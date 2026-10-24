package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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
		return BadRequest("el cuerpo de la petición está vacío")
	case errors.As(err, &syntaxErr), errors.Is(err, io.ErrUnexpectedEOF):
		return BadRequest("el cuerpo contiene JSON mal formado")
	case errors.As(err, &typeErr):
		if typeErr.Field != "" {
			return BadRequest(fmt.Sprintf("el campo %q tiene un tipo incorrecto", typeErr.Field))
		}
		return BadRequest("el cuerpo tiene un tipo incorrecto")
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		// encoding/json no exporta un tipo para este error, así que se reconoce por su mensaje.
		field := strings.TrimPrefix(err.Error(), "json: unknown field ")
		return BadRequest(fmt.Sprintf("campo desconocido %s", field))
	case errors.As(err, &maxBytesErr):
		return &Error{
			Status:  http.StatusRequestEntityTooLarge,
			Code:    "payload_too_large",
			Message: fmt.Sprintf("el cuerpo no puede superar %d bytes", maxBytesErr.Limit),
		}
	default:
		return BadRequest("no se pudo leer el cuerpo de la petición")
	}
}

// Write responde con status y v codificado como JSON. Si v es nil, solo envía el status
// (útil para 204 No Content).
func Write(w http.ResponseWriter, status int, v any) {
	if v == nil {
		w.WriteHeader(status)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Los encabezados ya se enviaron: solo queda registrar el problema.
		slog.Error("no se pudo escribir la respuesta JSON", "error", err)
	}
}
