package server

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"

	"github.com/NoeOnDev/go-api-base/internal/appointments"
	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/branches"
	"github.com/NoeOnDev/go-api-base/internal/categories"
	"github.com/NoeOnDev/go-api-base/internal/classes"
	"github.com/NoeOnDev/go-api-base/internal/config"
	"github.com/NoeOnDev/go-api-base/internal/files"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/mail"
	"github.com/NoeOnDev/go-api-base/internal/notes"
	"github.com/NoeOnDev/go-api-base/internal/notifications"
	"github.com/NoeOnDev/go-api-base/internal/openapi"
	"github.com/NoeOnDev/go-api-base/internal/products"
	"github.com/NoeOnDev/go-api-base/internal/ratelimit"
	"github.com/NoeOnDev/go-api-base/internal/realtime"
	"github.com/NoeOnDev/go-api-base/internal/secretbox"
	"github.com/NoeOnDev/go-api-base/internal/shifts"
	"github.com/NoeOnDev/go-api-base/internal/storage"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Deps are the dependencies the routes need.
type Deps struct {
	Logger *slog.Logger
	DB     *pgxpool.Pool
	Config config.Config
	// Mailer sends the emails. In main it is a jobs.MailSender (it queues them in River); in the
	// tests, a mail.Recorder.
	Mailer mail.Sender
	// Broker hands out the real-time events (GET /events). If nil, one is created; main passes it
	// to close it on shutdown.
	Broker *realtime.Broker
	// Jobs queues background jobs (it is a jobs.Client; see docs/jobs.md). Pass it to the resources
	// that queue jobs; in the tests it can be nil, or a client from jobs.New without Start. It is
	// declared with the River type so server does not import jobs (jobs uses testutil in its tests).
	Jobs *river.Client[pgx.Tx]
	// Box is the ENCRYPTION_KEY key ring: it encrypts the two-factor secret and protects the
	// recovery codes. main passes the same one the jobs use. If nil (in some tests), two-factor
	// authentication is not available.
	Box *secretbox.Box
	// I18n are the catalogs of the user-facing texts (see docs/i18n.md). If nil, one is built with
	// Config.DefaultLocale (or English, if it is empty).
	I18n *i18n.Catalog
	// Storage keeps the uploaded files (see docs/files.md): the local disk or S3 in main. If nil (in
	// the tests), they are kept in memory.
	Storage storage.Store
}

// NewHandler registers every route and applies the common middlewares.
func NewHandler(d Deps) http.Handler {
	mux := http.NewServeMux()

	catalog := d.I18n
	if catalog == nil {
		var err error
		if catalog, err = i18n.New(cmp.Or(d.Config.DefaultLocale, i18n.BaseLocale)); err != nil {
			panic(fmt.Sprintf("server: %v", err)) // config.Load already validates DEFAULT_LOCALE
		}
	}

	// The contract of the API, for the frontend's types and for whoever integrates with it (see
	// docs/openapi.md).
	mux.Handle("GET /openapi.yaml", openapi.Handler())

	// A monitor checks it every few seconds: it is logged at debug, unless the database does not answer.
	mux.Handle("GET /healthz", httpx.Quiet(health(d.DB)))

	queries := store.New(d.DB)

	broker := d.Broker
	if broker == nil {
		broker = realtime.NewBroker()
	}

	// In-app notifications: they are stored and sent live through GET /events.
	notificationsService := notifications.NewService(queries, broker, catalog)

	authService := auth.NewService(d.DB, auth.Options{
		SessionTTL:            d.Config.SessionTTL,
		SessionIdleTimeout:    d.Config.SessionIdleTimeout,
		CookieSecure:          d.Config.CookieSecure,
		Mailer:                d.Mailer,
		FrontendURL:           d.Config.FrontendURL,
		RequireVerifiedEmail:  d.Config.RequireEmailVerification,
		RequireAdminTwoFactor: d.Config.RequireAdminTwoFactor,
		Box:                   d.Box,
		Issuer:                d.Config.AppName,
		TrustedProxies:        d.Config.TrustedProxies,
		Notifier:              notificationsService,
		I18n:                  catalog,
		TimeZone:              d.Config.TimeZone,
		SessionWatcher:        broker,
	})
	// Limits of the auth routes: "in a row" is how many requests are allowed at once, and then one
	// is refilled every the given time. Tune them here for each project.
	//
	// The per-IP ones are generous because many people can share an IP (an office, a university,
	// mobile data). Brute force is stopped mainly by the per-account limit (login), the
	// per-challenge and per-user ones (two-factor) and the per-recipient one (emails), all in auth.
	//
	// The per-IP ones live in memory: a first cheap barrier, and with several instances each one
	// counts on its own. The per-account and per-user ones are in PostgreSQL (internal/ratelimit).
	byIP := func(every time.Duration, burst int) httpx.Middleware {
		return httpx.NewRateLimiter(every, burst).MiddlewareByIP(d.Config.TrustedProxies)
	}
	auth.Register(mux, authService, auth.Limits{
		Login:     byIP(3*time.Second, 20),  // 20 in a row, then 1 every 3 s
		Register:  byIP(10*time.Second, 10), // 10 in a row, then 1 every 10 s
		TwoFactor: byIP(3*time.Second, 20),  // 20 in a row, then 1 every 3 s
		Tokens:    byIP(6*time.Second, 10),  // 10 in a row, then 1 every 6 s
		Emails:    byIP(time.Minute, 10),    // 10 in a row, then 1 per minute
		// Per user: 5 in a row, then 1 every 12 s. It is in the database, like the other per-user and
		// per-account limits, so it holds for every API instance.
		Account: ratelimit.New(queries, "account", 12*time.Second, 5),
	})

	// General per-IP limit, on every route except /healthz (a monitor or a load balancer calls it
	// often from the same IP). It is a first barrier, like the auth ones: an expensive search
	// repeated, or any new route, does not depend only on the proxy. It is generous because many
	// people can share an IP; the auth routes add their own, lower ones.
	general := byIP(100*time.Millisecond, 200) // 200 in a row, then 10 per second
	limitAll := func(next http.Handler) http.Handler {
		limited := general(next)
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" {
				next.ServeHTTP(w, r)
				return
			}
			limited.ServeHTTP(w, r)
		})
	}

	// Real time: an SSE stream with the events of the user and of the topics they ask for. The
	// browser opens it again on its own every few minutes, so it does not count as activity of the
	// session: a forgotten tab does not keep it alive (SESSION_IDLE_TIMEOUT).
	mux.Handle("GET /events", authService.RequireUserWithoutActivity(broker.Handler(authService)))

	notifications.Register(mux, notificationsService, authService)

	// Uploaded files. The limits are per user, and they are tuned here for each project: how many
	// uploads in a row (30, then 1 every 10 s), and how much all the files of one user can take.
	fileStore := d.Storage
	if fileStore == nil {
		fileStore = storage.NewMemory()
	}
	filesService := files.NewService(d.DB, queries, fileStore, files.Options{UserQuota: 1 << 30}) // 1 GB
	uploads := ratelimit.New(queries, "upload", 10*time.Second, 30)
	files.Register(mux, filesService, authService, uploads)

	// API resources. Each new resource is registered here.
	notes.Register(mux, notes.NewHandler(d.DB, queries, broker, filesService), authService, uploads)
	categories.Register(mux, categories.NewHandler(queries), authService)
	products.Register(mux, products.NewHandler(queries), authService)
	branchesHandler := branches.NewHandler(d.DB, queries)
	branches.Register(mux, branchesHandler, authService)
	appointments.Register(mux, appointments.NewHandler(d.DB, queries, d.Jobs, branchesHandler), authService)
	shifts.Register(mux, shifts.NewHandler(queries, d.Config.TimeZone), authService)
	classes.Register(mux, classes.NewHandler(queries, d.Config.TimeZone), authService)

	// Any unregistered route answers 404 with the API error format, or 405 with the Allow header if
	// the path exists with another method (a DELETE where only POST is registered).
	mux.Handle("/", httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		if allowed := allowedMethods(mux, r); len(allowed) > 0 {
			w.Header().Set("Allow", strings.Join(allowed, ", "))
			return &httpx.Error{Status: http.StatusMethodNotAllowed, Code: "method_not_allowed"}
		}
		return httpx.NotFound("route_not_found")
	}))

	// Order:
	//   - RequestID first, so everything else already has the logger with the request_id.
	//   - The language (i18n) before anything that can answer an error, so the message comes out in
	//     the language of the request.
	//   - SecurityHeaders outside everything that can answer, so errors carry them too. HSTS goes
	//     only in production with Secure cookies, which is when there is HTTPS in front.
	//   - Recover inside Logger, so the 500 response of a panic is logged too.
	//   - CORS before the CSRF protection, to answer the preflights of the allowed origins.
	//   - The general limit inside CORS, so the preflights do not count.
	//   - RejectInvalidText last: its 400 counts for the limit and carries the CORS headers.
	return httpx.Chain(csrfProtection(mux, d.Config.CORSAllowedOrigins),
		httpx.RequestID(d.Logger),
		catalog.Middleware,
		httpx.SecurityHeaders(d.Config.IsProduction() && d.Config.CookieSecure),
		httpx.Logger,
		httpx.Recover,
		httpx.CORS(d.Config.CORSAllowedOrigins),
		limitAll,
		httpx.RejectInvalidText,
	)
}

// csrfProtection rejects the requests that modify data (POST, PUT, DELETE…) sent by the browser
// from another site. It uses the Sec-Fetch-Site and Origin headers, which the browser fills in
// and a malicious page cannot forge. It complements the SameSite=Lax cookie. The CORS origins
// are trusted.
func csrfProtection(next http.Handler, trustedOrigins []string) http.Handler {
	cop := http.NewCrossOriginProtection()
	for _, origin := range trustedOrigins {
		// config already validated the format of each origin.
		_ = cop.AddTrustedOrigin(origin)
	}
	cop.SetDenyHandler(httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		httpx.LoggerFrom(r.Context()).Warn("cross-origin request rejected",
			"origin", r.Header.Get("Origin"), "sec_fetch_site", r.Header.Get("Sec-Fetch-Site"))
		return httpx.Forbidden("cross_origin_rejected")
	}))
	return cop.Handler(next)
}

// allowedMethods returns the methods the path of r is registered with, when r came with another one
// (and so fell on the "/" route). The mux does not say it once there is a "/" route, so it is asked
// method by method.
func allowedMethods(mux *http.ServeMux, r *http.Request) []string {
	var allowed []string
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		probe := r.Clone(r.Context())
		probe.Method = method
		if _, pattern := mux.Handler(probe); pattern != "/" && pattern != "" {
			allowed = append(allowed, method)
		}
	}
	return allowed
}

// healthEvery is how often /healthz really asks the database. It has no rate limit (a monitor or a
// load balancer calls it often), so without this each call would take a connection of the pool:
// between checks it answers what the last one found.
const healthEvery = time.Second

// health reports whether the server is alive and can talk to the database.
// It answers 503 if the database does not answer, so a monitor or a load balancer notices.
func health(pool *pgxpool.Pool) http.HandlerFunc {
	var (
		mu      sync.Mutex
		checked time.Time
		lastErr error
	)
	// check pings the database at most every healthEvery. The calls that arrive meanwhile wait for
	// the one in progress and share its answer.
	check := func(ctx context.Context) error {
		mu.Lock()
		defer mu.Unlock()
		if time.Since(checked) >= healthEvery {
			// Without the cancel of the request: the answer is for the next calls too.
			ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			lastErr = pool.Ping(ctx)
			checked = time.Now()
		}
		return lastErr
	}
	return httpx.Handle(func(w http.ResponseWriter, r *http.Request) error {
		if err := check(r.Context()); err != nil {
			httpx.LoggerFrom(r.Context()).Warn("health check: the database does not answer", "error", err)
			return &httpx.Error{Status: http.StatusServiceUnavailable, Code: "unavailable"}
		}

		httpx.Write(w, http.StatusOK, map[string]string{"status": "ok"})
		return nil
	})
}
