// Package server arma el servidor HTTP: rutas, middlewares y timeouts.
package server

import (
	"log/slog"
	"net/http"
	"time"
)

// New crea un http.Server con timeouts. Sin ellos, un cliente lento o malicioso
// puede mantener conexiones abiertas indefinidamente y agotar los recursos del servidor.
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
