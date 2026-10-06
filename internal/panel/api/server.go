// Package api implements the Panel's HTTP API role.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"connectrpc.com/connect"

	"github.com/xena-studios/raptor/internal/gen/proto/raptor/meta/v1/metav1connect"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
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
	// Router is this instance's place among others (nil: a single
	// instance, as in tests).
	Router *nodes.Router
	// DrainWindow spreads node reconnects on shutdown (default 20 s).
	DrainWindow time.Duration
	// Auth signs people in (nil without a database).
	Auth *auth.Service
	// AppOrigin is the only origin browsers may call the API from
	// (https://app.raptorpanel.net; http://localhost:5173 in development).
	AppOrigin string
}

// Handler returns the API's HTTP handler.
func Handler(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	api := http.NewServeMux()
	path, handler := metav1connect.NewMetaServiceHandler(metaService{})
	api.Handle(path, handler)
	if cfg.Auth != nil {
		// Nothing auth takes is big; passkey answers are a few kilobytes.
		path, handler := panelv1connect.NewAuthServiceHandler(cfg.Auth, connect.WithReadMaxBytes(256<<10))
		api.Handle(path, handler)
	}
	appOrigin := cfg.AppOrigin
	if appOrigin == "" {
		appOrigin = "https://app.raptorpanel.net"
	}
	mux.Handle(apiPrefix+"/", http.StripPrefix(apiPrefix, browserGuard(appOrigin, api)))

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

	// Node connections are hijacked, so Shutdown doesn't wait for them.
	// They're handed to the other instances a few at a time: this one stops
	// accepting nodes, stops claiming the ones it has, and closes them over
	// the drain window.
	if cfg.Hub != nil {
		window := cfg.DrainWindow
		if window == 0 {
			window = 20 * time.Second
		}
		if cfg.Router != nil {
			cfg.Router.Stop(context.Background())
		}
		log.Info("draining node connections", "window", window.String())
		cfg.Hub.Drain(context.Background(), window)
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
