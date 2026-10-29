// Package server builds the HTTP server: routes, middlewares and timeouts.
package server

import (
	"log/slog"
	"net/http"
	"time"
)

// New creates an http.Server with timeouts. Without them, a slow or malicious client can keep
// connections open forever and use up the server's resources.
func New(addr string, handler http.Handler, logger *slog.Logger) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}
}
