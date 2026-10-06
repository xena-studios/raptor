// Package api implements the Panel's HTTP API role.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/xena-studios/raptor/internal/gen/proto/raptor/meta/v1/metav1connect"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// apiPrefix is where Connect services are mounted. The web app calls /api/<service>/<method>.
const apiPrefix = "/api"

// Config configures the API server.
type Config struct {
	Addr string
	// Nodes and Hub serve enrollment and node connections; without them
	// (no database configured) those routes don't exist.
	Nodes *nodes.Registry
	Hub   *nodes.Hub
}

// Handler returns the API's HTTP handler.
func Handler(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	path, handler := metav1connect.NewMetaServiceHandler(metaService{})
	mux.Handle(apiPrefix+path, http.StripPrefix(apiPrefix, handler))

	if cfg.Nodes != nil {
		path, handler := nodev1connect.NewEnrollmentServiceHandler(cfg.Nodes)
		mux.Handle(apiPrefix+path, http.StripPrefix(apiPrefix, handler))
	}
	if cfg.Hub != nil {
		mux.Handle("GET "+nodelink.Path, cfg.Hub)
	}
	return mux
}

// Run serves the API until ctx is cancelled, then shuts down gracefully.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           Handler(cfg),
		ReadHeaderTimeout: 10 * time.Second,
		Protocols:         new(http.Protocols),
	}
	// HTTP/1.1 for browsers, unencrypted HTTP/2 for gRPC clients behind the TLS proxy.
	srv.Protocols.SetHTTP1(true)
	srv.Protocols.SetUnencryptedHTTP2(true)

	errc := make(chan error, 1)
	go func() {
		log.Info("api listening", "addr", cfg.Addr)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	// Node connections are hijacked, so Shutdown doesn't wait for them; they
	// reconnect to another instance with jitter.
	if cfg.Hub != nil {
		cfg.Hub.Close()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	if err := <-errc; !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
