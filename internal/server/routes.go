package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/notes"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Deps son las dependencias que necesitan las rutas.
type Deps struct {
	Logger *slog.Logger
	DB     *pgxpool.Pool
}

// NewHandler registra todas las rutas y les aplica los middlewares comunes.
func NewHandler(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /healthz", health(d.DB))

	// Recursos de la API. Cada recurso nuevo se registra acá.
	queries := store.New(d.DB)
	notes.Register(mux, notes.NewHandler(queries))

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

// health indica si el servidor está vivo y puede hablar con la base de datos.
// Responde 503 si la base no contesta, para que un monitor o un balanceador lo detecte.
func health(pool *pgxpool.Pool) http.HandlerFunc {
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()

		if err := pool.Ping(ctx); err != nil {
			httpx.LoggerFrom(r.Context()).Warn("health check: la base no responde", "error", err)
			return &httpx.Error{
				Status:  http.StatusServiceUnavailable,
				Code:    "unavailable",
				Message: "la base de datos no responde",
			}
		}

		httpx.Write(w, http.StatusOK, map[string]string{"status": "ok"})
		return nil
	})
}
