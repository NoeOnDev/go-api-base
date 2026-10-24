package server

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/categories"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/notes"
	"github.com/NoeOnDev/go-api-base/internal/products"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Deps son las dependencias que necesitan las rutas.
type Deps struct {
	Logger *slog.Logger
	DB     *pgxpool.Pool
	Config config.Config
	// Mailer envía los correos. En main es un mail.Queue (envío en segundo plano);
	// en los tests, un mail.Recorder.
	Mailer mail.Sender
}

// NewHandler registra todas las rutas y les aplica los middlewares comunes.
func NewHandler(d Deps) http.Handler {
	mux := http.NewServeMux()

	mux.Handle("GET /healthz", health(d.DB))

	queries := store.New(d.DB)

	// Sin ENCRYPTION_KEY (por ejemplo, en algunos tests) el 2FA queda no disponible.
	// En la aplicación real config.Load exige la clave, así que siempre está.
	var box *secretbox.Box
	if len(d.Config.EncryptionKey) > 0 {
		b, err := secretbox.New(d.Config.EncryptionKey, d.Config.EncryptionPreviousKeys...)
		if err != nil {
			panic(fmt.Sprintf("ENCRYPTION_KEY inválida: %v", err)) // config.Load ya la validó
		}
		box = b
	}

	authService := auth.NewService(queries, auth.Options{
		SessionTTL:           d.Config.SessionTTL,
		CookieSecure:         d.Config.CookieSecure,
		Mailer:               d.Mailer,
		FrontendURL:          d.Config.FrontendURL,
		RequireVerifiedEmail: d.Config.RequireEmailVerification,
		Box:                  box,
		Issuer:               d.Config.AppName,
		TrustProxy:           d.Config.TrustProxy,
	})
	// Intentos: 5 seguidos por IP y luego uno cada 12 segundos (5 por minuto).
	// Correos: 3 seguidos por IP y luego uno cada 5 minutos.
	attempts := httpx.NewRateLimiter(12*time.Second, 5, d.Config.TrustProxy)
	emails := httpx.NewRateLimiter(5*time.Minute, 3, d.Config.TrustProxy)
	auth.Register(mux, authService, auth.Limits{Attempts: attempts.Middleware, Emails: emails.Middleware})

	// Recursos de la API. Cada recurso nuevo se registra acá.
	notes.Register(mux, notes.NewHandler(queries), authService)
	categories.Register(mux, categories.NewHandler(queries), authService)
	products.Register(mux, products.NewHandler(queries), authService)

	// Cualquier ruta no registrada responde 404 con el formato de error de la API.
	mux.Handle("/", httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		return httpx.NotFound("la ruta no existe")
	}))

	// Orden:
	//   - RequestID primero, para que el resto ya tenga el logger con request_id.
	//   - SecurityHeaders afuera de todo lo que puede responder, así también lo llevan los errores.
	//   - Recover dentro de Logger, para que la respuesta 500 de un panic también quede registrada.
	//   - CORS antes de la protección CSRF, para responder los preflight de los orígenes permitidos.
	return httpx.Chain(csrfProtection(mux, d.Config.CORSAllowedOrigins),
		httpx.RequestID(d.Logger),
		httpx.SecurityHeaders,
		httpx.Logger,
		httpx.Recover,
		httpx.CORS(d.Config.CORSAllowedOrigins),
	)
}

// csrfProtection rechaza las peticiones que modifican datos (POST, PUT, DELETE…) enviadas
// por el navegador desde otro sitio. Usa los encabezados Sec-Fetch-Site y Origin, que el
// navegador completa y una página maliciosa no puede falsificar. Complementa a la cookie
// SameSite=Lax. Los orígenes de CORS se consideran confiables.
func csrfProtection(next http.Handler, trustedOrigins []string) http.Handler {
	cop := http.NewCrossOriginProtection()
	for _, origin := range trustedOrigins {
		// config ya validó el formato de cada origen.
		_ = cop.AddTrustedOrigin(origin)
	}
	cop.SetDenyHandler(httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		httpx.LoggerFrom(r.Context()).Warn("petición de otro origen rechazada",
			"origin", r.Header.Get("Origin"), "sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
		return httpx.Forbidden("petición de otro origen rechazada")
	}))
	return cop.Handler(next)
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
