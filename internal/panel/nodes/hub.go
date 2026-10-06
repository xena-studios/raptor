// Package nodes is the Panel's side of node connections
// (docs/ARCHITECTURE.md#node-connection): it accepts them, keeps a registry
// of connected nodes, and calls their NodeService, retrying commands with the
// same command ID across reconnects.
package nodes

import (
	"context"
	"crypto/ed25519"
	"errors"
	"log/slog"
	"net/http"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// Hub accepts node connections and routes calls to them. Mount it at
// nodelink.Path.
type Hub struct {
	PanelKey ed25519.PrivateKey
	// NodeKey returns a node's enrolled public key, or an error to refuse it
	// (nodelink.ErrUnknownNode).
	NodeKey func(ctx context.Context, nodeID string) (ed25519.PublicKey, error)
	// EventsAvailable is called when a node says it has events up to
	// lastSeq; the Panel pulls them with Conn.Node.Events.
	EventsAvailable func(ctx context.Context, nodeID string, lastSeq int64)
	// OnConnect and OnDisconnect are called as a node's main connection
	// comes and goes.
	OnConnect func(ctx context.Context, h nodelink.Hello)
	// OnAddress is called with the address a node connected from
	// (ClientIP, with ClientIPHeader).
	OnAddress      func(ctx context.Context, h nodelink.Hello, addr netip.Addr)
	ClientIPHeader string
	OnDisconnect   func(ctx context.Context, nodeID string)
	Log            *slog.Logger
	Keepalive      nodelink.Keepalive // for tests

	once     sync.Once
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.Mutex
	conns    map[string]*Conn
	changed  chan struct{}                     // closed and replaced when a node connects
	pending  map[string]chan *nodelink.Session // transfers being opened, by node/transfer
	draining atomic.Bool
}

// Conn is a connected node.
type Conn struct {
	NodeID  string
	Hello   nodelink.Hello
	Since   time.Time
	Session *nodelink.Session
	Node    nodev1connect.NodeServiceClient
}

func (h *Hub) init() {
	h.once.Do(func() {
		h.ctx, h.cancel = context.WithCancel(context.Background())
		h.conns = map[string]*Conn{}
		h.changed = make(chan struct{})
		if h.Log == nil {
			h.Log = slog.New(slog.DiscardHandler)
		}
	})
}

// ServeHTTP accepts a node connection and serves it until it drops.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.init()
	if h.draining.Load() {
		// This instance is shutting down; the node retries and lands on
		// another one.
		w.Header().Set("Retry-After", "1")
		http.Error(w, "this Panel instance is shutting down", http.StatusServiceUnavailable)
		return
	}
	s, err := nodelink.Accept(h.ctx, w, r, nodelink.AcceptConfig{PanelKey: h.PanelKey, NodeKey: h.NodeKey, Keepalive: h.Keepalive})
	if err != nil {
		h.Log.Info("node connection refused", "remote", r.RemoteAddr, "err", err)
		return
	}
	if id, ok := nodelink.TransferID(s.Hello.Purpose); ok {
		// The transfer's owner closes it.
		if !h.acceptTransfer(s, id) {
			h.Log.Info("unrequested transfer connection", "node", s.Hello.NodeID, "transfer", id)
			_ = s.Close()
		}
		return
	}
	defer func() { _ = s.Close() }()
	c := &Conn{
		NodeID: s.Hello.NodeID, Hello: s.Hello, Since: time.Now(), Session: s,
		Node: nodev1connect.NewNodeServiceClient(s.Client(), nodelink.BaseURL),
	}
	h.register(c)
	defer func() {
		h.unregister(c)
		if h.OnDisconnect != nil {
			h.OnDisconnect(context.WithoutCancel(r.Context()), c.NodeID)
		}
	}()
	h.Log.Info("node connected", "node", c.NodeID, "version", s.Hello.Software)
	if h.OnConnect != nil {
		h.OnConnect(context.WithoutCancel(r.Context()), s.Hello)
	}
	if h.OnAddress != nil {
		h.OnAddress(context.WithoutCancel(r.Context()), s.Hello, ClientIP(r, h.ClientIPHeader))
	}

	mux := http.NewServeMux()
	mux.Handle(nodev1connect.NewPanelServiceHandler(&panelService{h: h}))
	_ = s.Serve(mux)
	h.Log.Info("node disconnected", "node", c.NodeID)
}

// register makes c the node's connection; a node has one, so an older one
// (a reconnect that beat the old connection's timeout) is closed.
func (h *Hub) register(c *Conn) {
	h.mu.Lock()
	old := h.conns[c.NodeID]
	h.conns[c.NodeID] = c
	close(h.changed)
	h.changed = make(chan struct{})
	h.mu.Unlock()
	if old != nil {
		_ = old.Session.Close()
	}
}

func (h *Hub) unregister(c *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[c.NodeID] == c {
		delete(h.conns, c.NodeID)
	}
}

// Conn returns a node's connection, if it's connected.
func (h *Hub) Conn(nodeID string) (*Conn, bool) {
	h.init()
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.conns[nodeID]
	return c, ok
}

// Connected lists the connected nodes.
func (h *Hub) Connected() []string {
	h.init()
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, 0, len(h.conns))
	for id := range h.conns {
		out = append(out, id)
	}
	return out
}

// Wait returns the node's connection, waiting for it to connect until ctx
// ends.
func (h *Hub) Wait(ctx context.Context, nodeID string) (*Conn, error) {
	h.init()
	for {
		h.mu.Lock()
		c, ok := h.conns[nodeID]
		changed := h.changed
		h.mu.Unlock()
		if ok {
			return c, nil
		}
		select {
		case <-changed:
		case <-ctx.Done():
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("node is offline"))
		}
	}
}

// Disconnect drops a node's connection, if it has one (its key was
// replaced or revoked). A node with a valid key reconnects.
func (h *Hub) Disconnect(nodeID string) {
	if c, ok := h.Conn(nodeID); ok {
		_ = c.Session.Close()
	}
}

// Drain hands this instance's nodes to the others before it stops: it refuses
// new node connections, then closes the ones it has spread evenly over
// window, so the nodes reconnect (to another instance) a few at a time
// instead of all at once (docs/RELIABILITY.md#deploys).
func (h *Hub) Drain(ctx context.Context, window time.Duration) {
	h.init()
	h.draining.Store(true)
	h.mu.Lock()
	conns := make([]*Conn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	if len(conns) == 0 {
		return
	}
	gap := window / time.Duration(len(conns))
	for i, c := range conns {
		if i > 0 {
			select {
			case <-time.After(gap):
			case <-ctx.Done():
			}
		}
		_ = c.Session.Close()
	}
}

// Close drops every node connection and refuses new ones.
func (h *Hub) Close() {
	h.init()
	h.cancel()
	h.mu.Lock()
	conns := make([]*Conn, 0, len(h.conns))
	for _, c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.Unlock()
	for _, c := range conns {
		_ = c.Session.Close()
	}
}

// Execute sends a command (its envelope as JSON) and returns the node's
// answer. Wings runs a command ID at most once, so it's retried, on the
// next connection if the node drops, until it answers or ctx ends. A
// command that ran and failed is a response with Error set, not an error.
func (h *Hub) Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error) {
	wait := 100 * time.Millisecond
	for {
		c, err := h.Wait(ctx, nodeID)
		if err != nil {
			return nil, err
		}
		res, err := c.Node.Execute(ctx, &nodev1.ExecuteRequest{Envelope: envelope})
		if err == nil || !retryable(err) || ctx.Err() != nil {
			return res, err
		}
		h.Log.Debug("retrying command", "node", nodeID, "err", err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, err
		}
		wait = min(wait*2, 2*time.Second)
	}
}

// retryable: the call didn't get an answer from Wings (the connection
// dropped), or the command is still running from an earlier attempt.
func retryable(err error) bool {
	if !connect.IsWireError(err) {
		return true
	}
	c := connect.CodeOf(err)
	return c == connect.CodeAborted || c == connect.CodeUnavailable
}

// panelService is PanelService: what nodes call.
type panelService struct {
	nodev1connect.UnimplementedPanelServiceHandler
	h *Hub
}

func (p *panelService) EventsAvailable(ctx context.Context, req *nodev1.EventsAvailableRequest) (*nodev1.EventsAvailableResponse, error) {
	s := nodelink.FromContext(ctx)
	if s == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("no session"))
	}
	if p.h.EventsAvailable != nil {
		p.h.EventsAvailable(ctx, s.Hello.NodeID, req.GetLastSeq())
	}
	return &nodev1.EventsAvailableResponse{}, nil
}
