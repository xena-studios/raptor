// Package link keeps Wings connected to the Panel
// (docs/ARCHITECTURE.md#node-connection): it dials the node connection,
// serves the Panel's calls on it (commands, events), and reconnects with
// jittered backoff whenever it drops. Nothing on the node depends on it:
// servers, schedules, and backups run the same while it's down.
package link

import (
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"sync"
	"time"

	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/events"
)

// Reconnect backoff: full jitter between 0 and min(MaxBackoff, MinBackoff·2^n).
// It resets once a connection has stayed up for stableAfter, so a Panel that
// accepts and immediately drops the node doesn't get hammered.
const (
	MinBackoff  = time.Second
	MaxBackoff  = time.Minute
	stableAfter = time.Minute
)

// Config is what the link needs.
type Config struct {
	PanelURL string
	NodeID   string
	NodeKey  ed25519.PrivateKey
	PanelKey ed25519.PublicKey
	Software string // Wings' version

	Commands *command.Executor
	// CommandsReady reports whether commands can run yet (Wings registers
	// them once the container runtime is up). Until then, Execute answers
	// UNAVAILABLE and the Panel retries.
	CommandsReady func() bool
	Events        *events.Outbox
	// OnConnected is called each time the connection comes up.
	OnConnected func()
	// Transfers returns the file service once it's ready (nil before).
	Transfers func() Transfers
	// Servers, Schedules, and Backups return the services once they're
	// ready (nil before).
	Servers   func() Servers
	Schedules func() Schedules
	Backups   func() Backups
	Log       *slog.Logger

	// For tests.
	HTTPClient             *http.Client
	MinBackoff, MaxBackoff time.Duration
	Keepalive              nodelink.Keepalive
}

// State is the connection's state.
type State string

// States.
const (
	Connecting   State = "connecting"
	Connected    State = "connected"
	Disconnected State = "disconnected" // waiting to retry
)

// Status is what `raptor status` and doctor show.
type Status struct {
	State      State
	Since      time.Time
	LastError  string
	Reconnects int // connections made after the first
	RTT        time.Duration
}

// Link is the node connection, kept up by Run.
type Link struct {
	cfg Config
	log *slog.Logger

	mu       sync.Mutex
	status   Status
	session  *nodelink.Session
	sessions int
}

// New returns a link; Run connects it.
func New(cfg Config) *Link {
	if cfg.MinBackoff == 0 {
		cfg.MinBackoff = MinBackoff
	}
	if cfg.MaxBackoff == 0 {
		cfg.MaxBackoff = MaxBackoff
	}
	log := cfg.Log
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Link{cfg: cfg, log: log.With("component", "link"), status: Status{State: Disconnected, Since: time.Now()}}
}

// Status returns the connection's state.
func (l *Link) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := l.status
	if l.session != nil {
		st.RTT = l.session.RTT()
	}
	return st
}

func (l *Link) setState(s State, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.status.State != s {
		l.status.State, l.status.Since = s, time.Now()
	}
	if err != nil {
		l.status.LastError = err.Error()
	}
}

// Run keeps the connection up until ctx is cancelled.
func (l *Link) Run(ctx context.Context) error {
	attempt := 0
	for {
		l.setState(Connecting, nil)
		started := time.Now()
		err := l.connect(ctx)
		if ctx.Err() != nil {
			l.setState(Disconnected, nil)
			return nil //nolint:nilerr // shutting down
		}
		if time.Since(started) >= stableAfter {
			attempt = 0
		}
		l.setState(Disconnected, err)
		wait := l.backoff(attempt)
		var refused *nodelink.RefusedError
		if errors.As(err, &refused) && refused.Retry > wait {
			wait = refused.Retry
		}
		attempt++
		if err != nil {
			l.log.Warn("node connection failed", "err", err, "retry_in", wait.Round(time.Millisecond))
		} else {
			l.log.Info("node connection closed", "retry_in", wait.Round(time.Millisecond))
		}
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			l.setState(Disconnected, nil)
			return nil
		}
	}
}

// backoff is the wait before attempt n+1: full jitter, so thousands of nodes
// dropped at once (a Panel deploy, Cloudflare maintenance) spread out.
func (l *Link) backoff(n int) time.Duration {
	ceil := l.cfg.MinBackoff << min(n, 16)
	if ceil > l.cfg.MaxBackoff || ceil <= 0 {
		ceil = l.cfg.MaxBackoff
	}
	return time.Duration(rand.Int64N(int64(ceil)) + 1) //nolint:gosec // jitter, not a secret
}

// connect makes one connection and serves it until it drops. It returns nil
// for a connection that worked and then closed.
func (l *Link) connect(ctx context.Context) error {
	s, err := nodelink.Dial(ctx, nodelink.DialConfig{
		URL: l.cfg.PanelURL, NodeID: l.cfg.NodeID, NodeKey: l.cfg.NodeKey, PanelKey: l.cfg.PanelKey,
		Purpose: nodelink.PurposeControl, Software: l.cfg.Software, HTTPClient: l.cfg.HTTPClient, Keepalive: l.cfg.Keepalive,
	})
	if err != nil {
		return err
	}
	l.mu.Lock()
	l.session = s
	if l.sessions > 0 {
		l.status.Reconnects++
	}
	l.sessions++
	l.status.LastError = ""
	l.mu.Unlock()
	l.setState(Connected, nil)
	l.log.Info("connected to the Panel")
	if l.cfg.OnConnected != nil {
		l.cfg.OnConnected()
	}
	defer func() {
		_ = s.Close()
		l.mu.Lock()
		l.session = nil
		l.mu.Unlock()
	}()

	mux := http.NewServeMux()
	mux.Handle(nodev1connect.NewNodeServiceHandler(&service{l: l}))
	go func() { _ = s.Serve(mux) }()

	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if l.cfg.Events != nil {
		panel := nodev1connect.NewPanelServiceClient(s.Client(), nodelink.BaseURL)
		go l.announceEvents(sctx, s, panel)
	}
	select {
	case <-s.Done():
	case <-ctx.Done():
	}
	return nil
}

// Panel returns a client for calling the Panel over the current connection,
// or nil while disconnected.
func (l *Link) Panel() nodev1connect.PanelServiceClient {
	l.mu.Lock()
	s := l.session
	l.mu.Unlock()
	if s == nil {
		return nil
	}
	return nodev1connect.NewPanelServiceClient(s.Client(), nodelink.BaseURL)
}
