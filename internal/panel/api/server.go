// Package api implements the Panel's HTTP API role.
package api

import (
	"cmp"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"
	"connectrpc.com/otelconnect"

	"github.com/xena-studios/raptor/internal/gen/proto/raptor/meta/v1/metav1connect"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/catalog"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/live"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/transfers"
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
	// Draining, once set, makes /healthz answer 503, so the proxy stops
	// sending this instance new requests and node connections while it hands
	// its nodes to the others (Run sets it at shutdown).
	Draining *atomic.Bool
	// Auth signs people in (nil without a database).
	Auth *auth.Service
	// Orgs manages orgs, members, and invitations (nil without a database).
	Orgs *orgs.Service
	// Commands sends users' commands to their nodes (nil without a
	// database).
	Commands *commands.Service
	// Support takes nodes' support bundles (nil: not configured).
	Support http.Handler
	// Transfers opens file transfer connections to nodes (*nodes.Router),
	// for uploads and downloads in the web file manager (nil: none).
	Transfers transfers.Opener
	// ConsoleRecheck is how often live console streams check access again
	// (default commands.ConsoleRecheck; shorter in tests).
	ConsoleRecheck time.Duration
	// AppOrigin is the only origin browsers may call the API from
	// (https://app.raptorpanel.net; http://localhost:5173 in development).
	AppOrigin string
}

// Handler returns the API's HTTP handler.
func Handler(cfg Config) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		if cfg.Draining != nil && cfg.Draining.Load() {
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	// Every RPC's latency and result (rpc.server.duration with the code),
	// through the global providers: nothing is sent unless telemetry is on.
	var traced []connect.HandlerOption
	if ic, err := otelconnect.NewInterceptor(); err == nil {
		traced = append(traced, connect.WithInterceptors(ic))
	}
	opts := func(o ...connect.HandlerOption) []connect.HandlerOption { return append(slices.Clone(traced), o...) }

	api := http.NewServeMux()
	path, handler := metav1connect.NewMetaServiceHandler(metaService{}, opts()...)
	api.Handle(path, handler)
	if cfg.Auth != nil {
		// Nothing auth takes is big; passkey answers are a few kilobytes.
		path, handler := panelv1connect.NewAuthServiceHandler(cfg.Auth, opts(connect.WithReadMaxBytes(256<<10))...)
		api.Handle(path, handler)
		// Outside /api: browsers arrive here from the provider's site, by
		// navigation, without an Origin to check.
		mux.Handle("GET /oauth/{provider}/callback", cfg.Auth.OAuthCallback())
		path, handler = panelv1connect.NewCatalogServiceHandler(&catalog.Service{Auth: cfg.Auth}, opts(connect.WithReadMaxBytes(4<<10))...)
		api.Handle(path, handler)
	}
	if cfg.Commands != nil {
		path, handler := panelv1connect.NewCommandServiceHandler(cfg.Commands, opts(connect.WithReadMaxBytes(7<<20))...)
		api.Handle(path, handler)
	}
	if cfg.Commands != nil && cfg.Auth != nil && cfg.Transfers != nil {
		(&transfers.Handler{Auth: cfg.Auth, Commands: cfg.Commands, Opener: cfg.Transfers}).Register(api)
	}
	if cfg.Commands != nil && cfg.Auth != nil {
		api.Handle("GET "+live.Path, &live.Handler{Auth: cfg.Auth, Commands: cfg.Commands, AppOrigin: cmp.Or(cfg.AppOrigin, "https://app.raptorpanel.net"), Recheck: cfg.ConsoleRecheck})
	}
	if cfg.Orgs != nil {
		path, handler := panelv1connect.NewOrgServiceHandler(cfg.Orgs, opts(connect.WithReadMaxBytes(64<<10))...)
		api.Handle(path, handler)
	}
	appOrigin := cfg.AppOrigin
	if appOrigin == "" {
		appOrigin = "https://app.raptorpanel.net"
	}
	mux.Handle(apiPrefix+"/", http.StripPrefix(apiPrefix, browserGuard(appOrigin, api)))

	if cfg.Nodes != nil {
		path, handler := nodev1connect.NewEnrollmentServiceHandler(cfg.Nodes, opts()...)
		mux.Handle(apiPrefix+path, http.StripPrefix(apiPrefix, handler))
	}
	if cfg.Hub != nil {
		mux.Handle("GET "+nodelink.Path, cfg.Hub)
		header := cfg.Hub.ClientIPHeader
		mux.HandleFunc("GET "+nodelink.AddressPath, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			_, _ = io.WriteString(w, nodes.ClientIP(r, header).String()+"\n") //nolint:gosec // a parsed address, as text/plain
		})
	}
	if cfg.Support != nil {
		// From raptor doctor, not browsers: no Origin check or cookies.
		mux.Handle("POST "+nodelink.BundlePath, cfg.Support)
	}
	return mux
}

// Run serves the API until ctx is cancelled, then shuts down gracefully.
func Run(ctx context.Context, cfg Config, log *slog.Logger) error {
	if cfg.Draining == nil {
		cfg.Draining = new(atomic.Bool)
	}
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
	cfg.Draining.Store(true)

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
