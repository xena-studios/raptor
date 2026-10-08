package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// ConsoleBatch is some of a server's console output: its history first
// (History set, the first batch even if empty), then new lines.
type ConsoleBatch struct {
	Lines   []string `json:"lines"`
	History bool     `json:"history,omitempty"`
}

var errNodeOffline = connect.NewError(connect.CodeUnavailable, errors.New("the node is offline"))

// Console streams a server's console from a node connected to this
// instance (envelope: a server.console command with its grant), calling out
// for each batch, until the node hangs up, out fails, or ctx ends. A
// watcher that falls behind loses lines: the server never waits for it.
func (h *Hub) Console(ctx context.Context, nodeID string, envelope []byte, out func(ConsoleBatch) error) error {
	c, ok := h.Conn(nodeID)
	if !ok {
		return errNodeOffline
	}
	stream, err := c.Node.Console(ctx, &nodev1.ConsoleRequest{Envelope: envelope})
	if err != nil {
		return err
	}
	defer func() { _ = stream.Close() }()
	for stream.Receive() {
		m := stream.Msg()
		if err := out(ConsoleBatch{Lines: m.GetLines(), History: m.GetHistory()}); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	return stream.Err()
}

// Console streams across instances (docs/ARCHITECTURE.md#realtime-console-and-stats):
// if another instance holds the node, this one asks it, by notification,
// to open the stream and send the batches back as notifications. The
// watcher renews its request every consoleKeepalive; the holder stops a
// stream that isn't renewed within consoleLease, or when told to.
const (
	consoleKeepalive = 10 * time.Second
	consoleLease     = 30 * time.Second
	// A notification's payload is at most 8000 bytes; batches are split to
	// stay under it, and longer single lines are cut.
	notifyMax  = 7000
	lineMaxFwd = 6000
	// A watcher's queue of forwarded batches; past it, batches are dropped.
	forwardQueue = 64
)

// Notification kinds for console streams (the payload after the colon is
// JSON): "con" opens a stream on the holder, "cka" renews it, "cx" closes
// it, "cl" carries a batch to the watcher, "ce" ends it.
type consoleOpen struct {
	Sub      string `json:"sub"`
	Origin   string `json:"origin"`
	Node     string `json:"node"`
	Envelope []byte `json:"env"`
}

type consoleMsg struct {
	Sub string `json:"sub"`
	ConsoleBatch
	// End: the stream is over, cleanly unless Code is set.
	End   bool   `json:"end,omitempty"`
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
}

// watcher is a stream this instance watches on another one.
type watcher struct {
	batches chan ConsoleBatch // dropped when full
	end     chan consoleMsg   // never dropped
}

// Console streams a server's console from wherever its node is connected
// (see Hub.Console).
func (r *Router) Console(ctx context.Context, nodeID string, envelope []byte, out func(ConsoleBatch) error) error {
	if _, ok := r.Hub.Conn(nodeID); ok {
		return r.Hub.Console(ctx, nodeID, envelope, out)
	}
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("bad node ID"))
	}
	holder, err := r.q().NodeHolder(ctx, store.NodeHolderParams{NodeID: pgUUID(id), StaleSecs: staleAfter.Seconds()})
	if err != nil || holder == r.ID {
		return errNodeOffline
	}
	return r.watchRemote(ctx, holder, id, nodeID, envelope, out)
}

func (r *Router) watchRemote(ctx context.Context, holder string, node uuid.UUID, nodeID string, envelope []byte, out func(ConsoleBatch) error) error {
	sub := uuid.NewString()
	w := &watcher{batches: make(chan ConsoleBatch, forwardQueue), end: make(chan consoleMsg, 1)}
	r.mu.Lock()
	r.watchers[sub] = w
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.watchers, sub)
		r.mu.Unlock()
		_ = r.notify(context.WithoutCancel(ctx), holder, "cx", consoleMsg{Sub: sub})
	}()
	if err := r.notify(ctx, holder, "con", consoleOpen{Sub: sub, Origin: r.ID, Node: nodeID, Envelope: envelope}); err != nil {
		return connect.NewError(connect.CodeUnavailable, err)
	}
	renew := time.NewTicker(consoleKeepalive)
	defer renew.Stop()
	check := time.NewTicker(pollEvery)
	defer check.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case b := <-w.batches:
			if err := out(b); err != nil {
				return err
			}
		case m := <-w.end:
			if m.Code == "" {
				return nil // the node ended the stream (the server was deleted)
			}
			var code connect.Code
			if code.UnmarshalText([]byte(m.Code)) != nil {
				code = connect.CodeUnknown
			}
			return connect.NewError(code, errors.New(m.Error))
		case <-renew.C:
			_ = r.notify(ctx, holder, "cka", consoleMsg{Sub: sub})
		case <-check.C:
			// The holder may have gone, or lost the node.
			if h, err := r.q().NodeHolder(ctx, store.NodeHolderParams{NodeID: pgUUID(node), StaleSecs: staleAfter.Seconds()}); err != nil || h != holder {
				return errNodeOffline
			}
		}
	}
}

func (r *Router) notify(ctx context.Context, instance, kind string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = r.DB.Exec(ctx, "SELECT pg_notify($1, $2)", channel(instance), kind+":"+string(b))
	return err
}

// consoleNotification handles a console notification; false if it isn't one.
func (r *Router) consoleNotification(ctx context.Context, kind, payload string) bool {
	switch kind {
	case "con":
		var o consoleOpen
		if json.Unmarshal([]byte(payload), &o) == nil {
			go r.serveConsole(ctx, o)
		}
	case "cka", "cx":
		var m consoleMsg
		if json.Unmarshal([]byte(payload), &m) != nil {
			return true
		}
		r.mu.Lock()
		s := r.serving[m.Sub]
		r.mu.Unlock()
		if s == nil {
			return true
		}
		if kind == "cx" {
			s.cancel()
		} else {
			s.renew()
		}
	case "cl", "ce":
		var m consoleMsg
		if json.Unmarshal([]byte(payload), &m) != nil {
			return true
		}
		r.mu.Lock()
		w := r.watchers[m.Sub]
		r.mu.Unlock()
		switch {
		case w == nil:
		case kind == "ce":
			select {
			case w.end <- m:
			default:
			}
		default:
			select {
			case w.batches <- m.ConsoleBatch:
			default: // the watcher is behind; it loses this batch
			}
		}
	default:
		return false
	}
	return true
}

// served is a stream this instance runs for a watcher on another instance.
type served struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	until  time.Time
}

func (s *served) renew() {
	s.mu.Lock()
	s.until = time.Now().Add(consoleLease)
	s.mu.Unlock()
}

func (s *served) expired() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return time.Now().After(s.until)
}

// serveConsole runs a stream for a watcher on another instance and sends
// it the batches, until the watcher closes it or stops renewing it.
func (r *Router) serveConsole(ctx context.Context, o consoleOpen) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	s := &served{cancel: cancel}
	s.renew()
	r.mu.Lock()
	r.serving[o.Sub] = s
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		delete(r.serving, o.Sub)
		r.mu.Unlock()
	}()
	go func() {
		t := time.NewTicker(consoleKeepalive)
		defer t.Stop()
		for {
			select {
			case <-sctx.Done():
				return
			case <-t.C:
				if s.expired() {
					cancel()
					return
				}
			}
		}
	}()

	err := r.Hub.Console(sctx, o.Node, o.Envelope, func(b ConsoleBatch) error {
		for _, part := range splitBatch(b) {
			if err := r.notify(sctx, o.Origin, "cl", consoleMsg{Sub: o.Sub, ConsoleBatch: part}); err != nil {
				return err
			}
		}
		return nil
	})
	end := consoleMsg{Sub: o.Sub, End: true}
	if err != nil && sctx.Err() == nil {
		end.Code, end.Error = connect.CodeOf(err).String(), err.Error()
		var ce *connect.Error
		if errors.As(err, &ce) {
			end.Error = ce.Message()
		}
	}
	_ = r.notify(context.WithoutCancel(ctx), o.Origin, "ce", end)
}

// splitBatch splits a batch into parts that fit in a notification, cutting
// lines too long for one. Every part keeps History.
func splitBatch(b ConsoleBatch) []ConsoleBatch {
	var parts []ConsoleBatch
	cur := ConsoleBatch{History: b.History}
	size := 0
	for _, l := range b.Lines {
		n := jsonLen(l)
		for n > lineMaxFwd && len(l) > 0 {
			l = strings.ToValidUTF8(l[:len(l)/2], "")
			n = jsonLen(l + "…")
			if n <= lineMaxFwd {
				l += "…"
			}
		}
		if size+n > notifyMax && len(cur.Lines) > 0 {
			parts = append(parts, cur)
			cur, size = ConsoleBatch{History: b.History}, 0
		}
		cur.Lines = append(cur.Lines, l)
		size += n + 1
	}
	return append(parts, cur)
}

// jsonLen is a string's length as JSON (escapes included).
func jsonLen(s string) int {
	b, _ := json.Marshal(s)
	return len(b)
}
