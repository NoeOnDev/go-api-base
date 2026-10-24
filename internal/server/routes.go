package server

import (
	"log/slog"
	"net/http"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
)

// Deps son las dependencias que necesitan las rutas.
type Deps struct {
	Logger *slog.Logger
}

// NewHandler registra todas las rutas y les aplica los middlewares comunes.
func NewHandler(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", health)

	// Cualquier ruta no registrada responde 404 con el formato de error de la API.
	mux.Handle("/", httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		return httpx.NotFound("la ruta no existe")
	}))

	// Orden: RequestID primero para que el resto ya tenga el logger con request_id;
	// Recover dentro de Logger para que la respuesta 500 de un panic también quede registrada.
	return httpx.Chain(mux,
		httpx.RequestID(d.Logger),
		httpx.Logger,
		httpx.Recover,
	)
}

// health indica que el servidor está vivo.
func health(w http.ResponseWriter, r *http.Request) {
	httpx.Write(w, http.StatusOK, map[string]string{"status": "ok"})
}
