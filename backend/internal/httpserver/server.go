package httpserver

import (
	"net/http"

	"evidentia/backend/internal/config"
)

// New builds an *http.Server with every timeout explicitly set from cfg —
// this application must never run a server with unbounded timeouts (see
// master prompt §16). ReadHeaderTimeout reuses ReadTimeout: there is no
// separate SERVER_READ_HEADER_TIMEOUT setting, and bounding the
// header-reading phase to the same duration as the full read is a safe,
// conservative default.
func New(cfg config.ServerConfig, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              cfg.Addr(),
		Handler:           handler,
		ReadTimeout:       cfg.ReadTimeout,
		ReadHeaderTimeout: cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
}
