// Package api implements the Panel's HTTP API role.
package api

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Config configures the API server.
type Config struct {
	Addr string
}

// Handler returns the API's HTTP handler.
func Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	return mux
}

// Run serves the API until ctx is cancelled, then shuts down gracefully.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

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
