package server

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/categories"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/notes"
	"github.com/NoeOnDev/go-api-base/internal/notifications"
	"github.com/NoeOnDev/go-api-base/internal/products"
	"github.com/NoeOnDev/go-api-base/internal/ratelimit"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Deps son las dependencias que necesitan las rutas.
type Deps struct {
	Logger *slog.Logger
	DB     *pgxpool.Pool
	Config config.Config
	// Mailer envía los correos. En main es un jobs.MailSender (los encola en River);
	// en los tests, un mail.Recorder.
	Mailer mail.Sender
	// Broker reparte los eventos en tiempo real (GET /events). Si es nil se crea uno;
	// main lo pasa para cerrarlo al apagar.
	Broker *realtime.Broker
	// Jobs encola trabajos en segundo plano (es un jobs.Client; ver docs/jobs.md). Pasalo a los
	// recursos que encolen; en los tests puede ser nil, o un cliente de jobs.New sin Start.
	// Se declara con el tipo de River para que server no importe jobs (jobs usa testutil en sus tests).
	Jobs *river.Client[pgx.Tx]
	// Box es el llavero de ENCRYPTION_KEY: cifra el secreto del 2FA y protege los códigos de
	// recuperación. main pasa el mismo que usan los jobs. Si es nil (en algunos tests), el 2FA
	// no está disponible.
	Box *secretbox.Box
	// I18n son los catálogos de los textos al usuario (ver docs/i18n.md). Si es nil, se arma uno
	// con Config.DefaultLocale (o inglés, si está vacío).
	I18n *i18n.Catalog
}

// NewHandler registra todas las rutas y les aplica los middlewares comunes.
func NewHandler(d Deps) http.Handler {
	mux := http.NewServeMux()

	catalog := d.I18n
	if catalog == nil {
		var err error
		if catalog, err = i18n.New(cmp.Or(d.Config.DefaultLocale, i18n.BaseLocale)); err != nil {
			panic(fmt.Sprintf("server: %v", err)) // config.Load ya valida DEFAULT_LOCALE
		}
	}

	// Un monitor lo consulta cada pocos segundos: en el log va en debug, salvo que la base no responda.
	mux.Handle("GET /healthz", httpx.Quiet(health(d.DB)))

	queries := store.New(d.DB)

	broker := d.Broker
	if broker == nil {
		broker = realtime.NewBroker()
	}

	// Notificaciones dentro del sistema: se guardan y se envían en vivo por GET /events.
	notificationsService := notifications.NewService(queries, broker, catalog)

	authService := auth.NewService(d.DB, auth.Options{
		SessionTTL:           d.Config.SessionTTL,
		SessionIdleTimeout:   d.Config.SessionIdleTimeout,
		CookieSecure:         d.Config.CookieSecure,
		Mailer:               d.Mailer,
		FrontendURL:          d.Config.FrontendURL,
		RequireVerifiedEmail: d.Config.RequireEmailVerification,
		Box:                  d.Box,
		Issuer:               d.Config.AppName,
		TrustedProxies:       d.Config.TrustedProxies,
		Notifier:             notificationsService,
		I18n:                 catalog,
		SessionWatcher:       broker,
	})
	// Límites de las rutas de auth: "seguidos" es cuántas peticiones se permiten de golpe, y
	// después se recarga una cada el tiempo indicado. Ajustalos acá según el proyecto.
	//
	// Los de IP son generosos porque muchas personas pueden salir por la misma IP (una oficina,
	// una universidad, los datos móviles). La fuerza bruta la frenan sobre todo los límites por
	// cuenta (login), por desafío y usuario (2FA) y por destinatario (correos), que están en auth.
	//
	// Los de IP viven en memoria: son una primera barrera barata, y con varias instancias cada una
	// cuenta por separado. Los de cuenta y usuario están en PostgreSQL (internal/ratelimit).
	byIP := func(every time.Duration, burst int) httpx.Middleware {
		return httpx.NewRateLimiter(every, burst).MiddlewareByIP(d.Config.TrustedProxies)
	}
	auth.Register(mux, authService, auth.Limits{
		Login:     byIP(3*time.Second, 20),  // 20 seguidos, luego 1 cada 3 s
		Register:  byIP(10*time.Second, 10), // 10 seguidos, luego 1 cada 10 s
		TwoFactor: byIP(3*time.Second, 20),  // 20 seguidos, luego 1 cada 3 s
		Tokens:    byIP(6*time.Second, 10),  // 10 seguidos, luego 1 cada 6 s
		Emails:    byIP(time.Minute, 10),    // 10 seguidos, luego 1 por minuto
		// Por usuario: 5 seguidos, luego 1 cada 12 s. Está en la base, como los demás límites por
		// usuario o por cuenta, así vale para todas las instancias de la API.
		Account: ratelimit.New(queries, "account", 12*time.Second, 5),
	})

	// Tiempo real: un stream SSE con los eventos del usuario y de los temas que pida.
	mux.Handle("GET /events", authService.RequireUser(broker.Handler(authService)))

	notifications.Register(mux, notificationsService, authService)

	// Recursos de la API. Cada recurso nuevo se registra acá.
	notes.Register(mux, notes.NewHandler(queries, broker), authService)
	categories.Register(mux, categories.NewHandler(queries), authService)
	products.Register(mux, products.NewHandler(queries), authService)

	// Cualquier ruta no registrada responde 404 con el formato de error de la API.
	mux.Handle("/", httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		return httpx.NotFound("route_not_found")
	}))

	// Orden:
	//   - RequestID primero, para que el resto ya tenga el logger con request_id.
	//   - El idioma (i18n) antes de todo lo que puede responder un error, para que el mensaje salga
	//     en el idioma de la petición.
	//   - SecurityHeaders afuera de todo lo que puede responder, así también lo llevan los errores.
	//   - Recover dentro de Logger, para que la respuesta 500 de un panic también quede registrada.
	//   - CORS antes de la protección CSRF, para responder los preflight de los orígenes permitidos.
	return httpx.Chain(csrfProtection(mux, d.Config.CORSAllowedOrigins),
		httpx.RequestID(d.Logger),
		catalog.Middleware,
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
		return httpx.Forbidden("cross_origin_rejected")
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
			return &httpx.Error{Status: http.StatusServiceUnavailable, Code: "unavailable"}
		}

		httpx.Write(w, http.StatusOK, map[string]string{"status": "ok"})
		return nil
	})
}
