// Package live is the web app's realtime connection: one WebSocket per
// browser tab, carrying every console the tab watches
// (docs/ARCHITECTURE.md#realtime-console-and-stats).
package live

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/coder/websocket"

	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/nodes"
)

// Path is where the web app connects, under the API's prefix.
const Path = "/live"

// Limits.
const (
	maxSubscriptions = 20
	maxMessage       = 4 << 10 // what the browser sends is small
	outQueue         = 256     // messages waiting for a slow browser; past it, lines are dropped
	sessionCheck     = time.Minute
	writeTimeout     = 10 * time.Second
)

// Handler serves the live WebSocket.
type Handler struct {
	Auth     *auth.Service
	Commands *commands.Service
	// AppOrigin is the only origin allowed to connect.
	AppOrigin string
	// Recheck is how often a console stream is reopened to check access
	// again (commands.ConsoleRecheck).
	Recheck time.Duration
}

// In is a message from the browser. Op "console" watches a server's
// console under the browser's id for it; "close" stops watching.
type In struct {
	Op     string `json:"op"`
	ID     string `json:"id"`
	Node   string `json:"node,omitempty"`
	Server string `json:"server,omitempty"`
}

// Out is a message to the browser. Type "lines" carries console lines
// (Reset: a new stream starts with the console's history, so clear what's
// shown); "ended" says a watch is over, with Retry if trying again may work
// (the node is offline or reconnecting).
type Out struct {
	ID    string   `json:"id"`
	Type  string   `json:"type"`
	Lines []string `json:"lines,omitempty"`
	Reset bool     `json:"reset,omitempty"`
	Error string   `json:"error,omitempty"`
	Retry bool     `json:"retry,omitempty"`
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := auth.WithRequest(r.Context(), w, r)
	if _, err := h.Auth.Current(ctx); err != nil {
		http.Error(w, "sign in first", http.StatusUnauthorized)
		return
	}
	var patterns []string
	if u, err := url.Parse(h.AppOrigin); err == nil && u.Host != "" {
		patterns = []string{u.Host}
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: patterns})
	if err != nil {
		return
	}
	c.SetReadLimit(maxMessage)
	s := &socket{h: h, c: c, out: make(chan Out, outQueue), subs: map[string]context.CancelFunc{}}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go s.write(ctx, cancel)
	go s.checkSession(ctx, cancel)
	s.read(ctx)
	s.mu.Lock()
	for _, stop := range s.subs {
		stop()
	}
	s.mu.Unlock()
	_ = c.Close(websocket.StatusNormalClosure, "")
}

type socket struct {
	h    *Handler
	c    *websocket.Conn
	out  chan Out
	mu   sync.Mutex
	subs map[string]context.CancelFunc
}

func (s *socket) read(ctx context.Context) {
	for {
		_, b, err := s.c.Read(ctx)
		if err != nil {
			return
		}
		var m In
		if json.Unmarshal(b, &m) != nil || m.ID == "" || len(m.ID) > 64 {
			continue
		}
		switch m.Op {
		case "console":
			s.watch(ctx, m)
		case "close":
			s.mu.Lock()
			if stop := s.subs[m.ID]; stop != nil {
				stop()
				delete(s.subs, m.ID)
			}
			s.mu.Unlock()
		}
	}
}

// write sends queued messages; a browser that can't keep up for
// writeTimeout is disconnected.
func (s *socket) write(ctx context.Context, cancel context.CancelFunc) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-s.out:
			b, err := json.Marshal(m)
			if err != nil {
				continue
			}
			wctx, wcancel := context.WithTimeout(ctx, writeTimeout)
			err = s.c.Write(wctx, websocket.MessageText, b)
			wcancel()
			if err != nil {
				cancel()
				return
			}
		}
	}
}

// send queues a message. Lines are dropped when the queue is full (a slow
// browser loses lines; the server never waits); anything else waits.
func (s *socket) send(ctx context.Context, m Out) {
	if m.Type == "lines" && !m.Reset {
		select {
		case s.out <- m:
		default:
		}
		return
	}
	select {
	case s.out <- m:
	case <-ctx.Done():
	}
}

// checkSession closes the socket once its session ends (signed out,
// revoked, expired).
func (s *socket) checkSession(ctx context.Context, cancel context.CancelFunc) {
	t := time.NewTicker(sessionCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.h.Auth.Current(ctx); err != nil && ctx.Err() == nil {
				_ = s.c.Close(websocket.StatusPolicyViolation, "signed out")
				cancel()
				return
			}
		}
	}
}

func (s *socket) watch(ctx context.Context, m In) {
	s.mu.Lock()
	if s.subs[m.ID] != nil || len(s.subs) >= maxSubscriptions {
		s.mu.Unlock()
		s.send(ctx, Out{ID: m.ID, Type: "ended", Error: "too many consoles open in this tab"})
		return
	}
	wctx, stop := context.WithCancel(ctx)
	s.subs[m.ID] = stop
	s.mu.Unlock()
	go func() {
		defer func() {
			s.mu.Lock()
			if s.subs[m.ID] != nil {
				delete(s.subs, m.ID)
			}
			s.mu.Unlock()
			stop()
		}()
		s.console(wctx, m)
	}()
}

// console streams one console, reopening it every Recheck so access is
// checked again; each opening starts with the history (Reset).
func (s *socket) console(ctx context.Context, m In) {
	recheck := s.h.Recheck
	if recheck <= 0 {
		recheck = commands.ConsoleRecheck
	}
	for {
		sctx, cancel := context.WithTimeout(ctx, recheck)
		first := true
		err := s.h.Commands.Console(sctx, m.Node, m.Server, func(b nodes.ConsoleBatch) error {
			s.send(ctx, Out{ID: m.ID, Type: "lines", Lines: b.Lines, Reset: first && b.History})
			first = false
			return nil
		})
		timedOut := sctx.Err() != nil && ctx.Err() == nil
		cancel()
		switch {
		case ctx.Err() != nil:
			return // the browser stopped watching
		case timedOut:
			continue
		case err == nil:
			s.send(ctx, Out{ID: m.ID, Type: "ended"}) // the server was deleted
		default:
			code := connect.CodeOf(err)
			msg := err.Error()
			var ce *connect.Error
			if errors.As(err, &ce) {
				msg = ce.Message()
			}
			s.send(ctx, Out{ID: m.ID, Type: "ended", Error: msg, Retry: code == connect.CodeUnavailable || code == connect.CodeUnknown})
		}
		return
	}
}
