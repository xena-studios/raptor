package nodelink

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/hashicorp/yamux"
)

// Timing (docs/ARCHITECTURE.md#node-connection). Cloudflare closes WebSockets
// idle for about 100 seconds, so a ping every 30 keeps them open; a
// connection whose pings go unanswered for 90 seconds is dead.
const (
	PingInterval     = 30 * time.Second
	DeadAfter        = 90 * time.Second
	HandshakeTimeout = 30 * time.Second
)

// maxMessage bounds one WebSocket message; writes are split into messages of
// at most maxWrite, so a large yamux frame never exceeds it.
const (
	maxMessage = 1 << 20
	maxWrite   = 256 << 10
)

// transferWindow is a transfer connection's yamux stream window: big enough
// that a chunk isn't throttled by the round trip (yamux's default, 256 KiB,
// allows about 5 MB/s at 50 ms).
const transferWindow = 16 << 20

// splitConn splits writes into WebSocket messages of at most maxWrite.
type splitConn struct{ net.Conn }

func (c splitConn) Write(b []byte) (int, error) {
	n := 0
	for len(b) > 0 {
		m, err := c.Conn.Write(b[:min(len(b), maxWrite)])
		n += m
		if err != nil {
			return n, err
		}
		b = b[m:]
	}
	return n, nil
}

// BaseURL is the URL Connect clients on a session use. Requests never leave
// the session, so the host is only a label.
const BaseURL = "http://node-connection"

// Session is an established node connection: yamux over the WebSocket.
// Either side opens streams; each stream carries one HTTP/1.1 connection, so
// Connect RPCs run in both directions at once.
type Session struct {
	Hello Hello // what Wings sent: node ID, purpose, versions

	mux    *yamux.Session
	client *http.Client
	cancel context.CancelFunc

	ping, dead time.Duration

	mu       sync.Mutex
	lastPong time.Time
	rtt      time.Duration
}

// Keepalive overrides PingInterval and DeadAfter (zero: the defaults).
type Keepalive struct {
	Ping, Dead time.Duration
}

func yamuxConfig(purpose string) *yamux.Config {
	c := yamux.DefaultConfig()
	if _, ok := TransferID(purpose); ok {
		c.MaxStreamWindowSize = transferWindow
	}
	c.EnableKeepAlive = false // our own ping, with the dead-after rule
	c.ConnectionWriteTimeout = 30 * time.Second
	c.LogOutput = io.Discard
	return c
}

func newSession(ctx context.Context, c *websocket.Conn, h Hello, server bool, ka Keepalive) (*Session, error) {
	ctx, cancel := context.WithCancel(ctx)
	conn := splitConn{websocket.NetConn(ctx, c, websocket.MessageBinary)}
	var mux *yamux.Session
	var err error
	if server {
		mux, err = yamux.Server(conn, yamuxConfig(h.Purpose))
	} else {
		mux, err = yamux.Client(conn, yamuxConfig(h.Purpose))
	}
	if err != nil {
		cancel()
		_ = conn.Close()
		return nil, err
	}
	s := &Session{Hello: h, mux: mux, cancel: cancel, lastPong: time.Now(), ping: PingInterval, dead: DeadAfter}
	if ka.Ping > 0 {
		s.ping = ka.Ping
	}
	if ka.Dead > 0 {
		s.dead = ka.Dead
	}
	s.client = &http.Client{Transport: &http.Transport{
		DialContext:         func(context.Context, string, string) (net.Conn, error) { return mux.Open() },
		MaxIdleConnsPerHost: 32,
		IdleConnTimeout:     90 * time.Second,
		DisableCompression:  true,
	}}
	go s.keepalive()
	go func() {
		<-mux.CloseChan()
		cancel()
		s.client.CloseIdleConnections()
	}()
	return s, nil
}

// keepalive pings every PingInterval and closes the session once no ping
// has been answered for DeadAfter.
func (s *Session) keepalive() {
	t := time.NewTicker(s.ping)
	defer t.Stop()
	for {
		select {
		case <-s.mux.CloseChan():
			return
		case <-t.C:
		}
		if rtt, err := s.pingWithin(s.ping); err == nil {
			s.mu.Lock()
			s.lastPong, s.rtt = time.Now(), rtt
			s.mu.Unlock()
		}
		s.mu.Lock()
		dead := time.Since(s.lastPong) >= s.dead
		s.mu.Unlock()
		if dead {
			_ = s.Close()
			return
		}
	}
}

// pingWithin pings, giving up after d (yamux's own ping waits as long as
// its write timeout).
func (s *Session) pingWithin(d time.Duration) (time.Duration, error) {
	type result struct {
		rtt time.Duration
		err error
	}
	ch := make(chan result, 1)
	go func() {
		rtt, err := s.mux.Ping()
		ch <- result{rtt, err}
	}()
	select {
	case r := <-ch:
		return r.rtt, r.err
	case <-time.After(d):
		return 0, errors.New("ping timed out")
	case <-s.mux.CloseChan():
		return 0, errors.New("session closed")
	}
}

// RTT is the last ping's round trip.
func (s *Session) RTT() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rtt
}

// Ping measures a round trip now.
func (s *Session) Ping() (time.Duration, error) { return s.mux.Ping() }

// Client is an HTTP client whose requests go to the other side, for Connect
// clients with BaseURL.
func (s *Session) Client() *http.Client { return s.client }

// Serve answers the other side's requests with h until the session closes.
// h's request contexts carry the session (FromContext).
func (s *Session) Serve(h http.Handler) error {
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 30 * time.Second,
		ConnContext:       func(ctx context.Context, _ net.Conn) context.Context { return context.WithValue(ctx, sessionKey{}, s) },
	}
	go func() {
		<-s.mux.CloseChan()
		_ = srv.Close()
	}()
	err := srv.Serve(s.mux)
	if s.mux.IsClosed() {
		return nil
	}
	return err
}

// Done is closed when the session ends.
func (s *Session) Done() <-chan struct{} { return s.mux.CloseChan() }

// Close ends the session and its WebSocket.
func (s *Session) Close() error {
	err := s.mux.Close()
	s.cancel()
	return err
}

type sessionKey struct{}

// FromContext returns the session a request arrived on.
func FromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionKey{}).(*Session)
	return s
}

// DialConfig is Wings' side of a connection.
type DialConfig struct {
	// URL is the Panel's URL (https://api.raptorpanel.net); the connection
	// goes to wss://<host>/nodes/connect.
	URL      string
	NodeID   string
	NodeKey  ed25519.PrivateKey
	PanelKey ed25519.PublicKey
	Purpose  string
	Software string
	// HTTPClient makes the WebSocket request (default: http.DefaultClient,
	// which honors HTTPS_PROXY).
	HTTPClient *http.Client
	Keepalive  Keepalive
	Now        func() time.Time
}

// RefusedError is the Panel turning the node away after the handshake
// started (unknown or revoked node, unsupported version, ...).
type RefusedError struct {
	Reason string
	Retry  time.Duration // the Panel's requested wait, if any
}

func (e *RefusedError) Error() string { return "the Panel refused the connection: " + e.Reason }

// WebSocketURL is the node connection's URL for a Panel URL.
func WebSocketURL(panel string) (string, error) {
	u, err := url.Parse(panel)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "wss", "ws":
	default:
		return "", fmt.Errorf("panel URL %q: want https://", panel)
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + Path
	return u.String(), nil
}

// Dial connects to the Panel and completes the handshake. The session lives
// until it's closed or ctx is cancelled.
func Dial(ctx context.Context, cfg DialConfig) (*Session, error) {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	u, err := WebSocketURL(cfg.URL)
	if err != nil {
		return nil, err
	}
	hello, err := NewHello(cfg.NodeID, cfg.Purpose, cfg.Software)
	if err != nil {
		return nil, err
	}
	hctx, cancel := context.WithTimeout(ctx, HandshakeTimeout)
	defer cancel()
	c, resp, err := websocket.Dial(hctx, u, &websocket.DialOptions{ //nolint:bodyclose // the library owns the body
		HTTPClient: cfg.HTTPClient,
		HTTPHeader: http.Header{"User-Agent": {"raptor-wings/" + cfg.Software}},
	})
	if err != nil {
		if resp != nil {
			return nil, fmt.Errorf("connecting to %s: %s: %w", u, resp.Status, err)
		}
		return nil, fmt.Errorf("connecting to %s: %w", u, err)
	}
	c.SetReadLimit(maxMessage)
	fail := func(err error) (*Session, error) {
		_ = c.Close(websocket.StatusPolicyViolation, "handshake failed")
		return nil, err
	}
	if err := wsjson.Write(hctx, c, hello); err != nil {
		return fail(err)
	}
	var ch Challenge
	if err := wsjson.Read(hctx, c, &ch); err != nil {
		return fail(fmt.Errorf("handshake: %w", err))
	}
	if ch.Error != "" {
		return fail(&RefusedError{Reason: ch.Error, Retry: time.Duration(ch.Retry) * time.Second})
	}
	if err := VerifyChallenge(hello, ch, cfg.PanelKey, now()); err != nil {
		return fail(err)
	}
	proof, err := NewProof(hello, ch, cfg.NodeKey, now())
	if err != nil {
		return fail(err)
	}
	if err := wsjson.Write(hctx, c, proof); err != nil {
		return fail(err)
	}
	var w Welcome
	if err := wsjson.Read(hctx, c, &w); err != nil {
		return fail(fmt.Errorf("handshake: %w", err))
	}
	if w.Error != "" {
		return fail(&RefusedError{Reason: w.Error, Retry: time.Duration(w.Retry) * time.Second})
	}
	return newSession(ctx, c, hello, false, cfg.Keepalive)
}

// AcceptConfig is the Panel's side of a connection.
type AcceptConfig struct {
	PanelKey ed25519.PrivateKey
	// NodeKey returns a node's enrolled public key. An error refuses the
	// node with that message (e.g. unknown or revoked).
	NodeKey func(ctx context.Context, nodeID string) (ed25519.PublicKey, error)
	// MinVersion is the oldest protocol version accepted (default Version).
	MinVersion int
	Keepalive  Keepalive
	Now        func() time.Time
}

// ErrUnknownNode is what NodeKey returns for a node the Panel doesn't know.
var ErrUnknownNode = errors.New("unknown or removed node")

// Accept upgrades a request to a node connection and completes the
// handshake. The session lives until it's closed or ctx is cancelled (not
// the request's context: the handler may return).
func Accept(ctx context.Context, w http.ResponseWriter, r *http.Request, cfg AcceptConfig) (*Session, error) {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	minVersion := cfg.MinVersion
	if minVersion == 0 {
		minVersion = Version
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{CompressionMode: websocket.CompressionDisabled})
	if err != nil {
		return nil, err
	}
	c.SetReadLimit(maxMessage)
	hctx, cancel := context.WithTimeout(ctx, HandshakeTimeout)
	defer cancel()
	refuse := func(reason string, err error) (*Session, error) {
		_ = wsjson.Write(hctx, c, Welcome{Error: reason})
		_ = c.Close(websocket.StatusPolicyViolation, "refused")
		return nil, err
	}
	var hello Hello
	if err := wsjson.Read(hctx, c, &hello); err != nil {
		_ = c.Close(websocket.StatusProtocolError, "bad handshake")
		return nil, fmt.Errorf("handshake: %w", err)
	}
	if err := hello.Validate(); err != nil {
		return refuse(err.Error(), err)
	}
	if hello.Version < minVersion {
		err := fmt.Errorf("protocol version %d is too old (%d or newer): update Wings", hello.Version, minVersion)
		return refuse(err.Error(), err)
	}
	key, err := cfg.NodeKey(hctx, hello.NodeID)
	if err != nil {
		return refuse(err.Error(), err)
	}
	ch, err := NewChallenge(hello, cfg.PanelKey, now())
	if err != nil {
		return refuse("internal error", err)
	}
	if err := wsjson.Write(hctx, c, ch); err != nil {
		return nil, err
	}
	var proof Proof
	if err := wsjson.Read(hctx, c, &proof); err != nil {
		_ = c.Close(websocket.StatusProtocolError, "bad handshake")
		return nil, fmt.Errorf("handshake: %w", err)
	}
	if err := VerifyProof(hello, ch, proof, key, now()); err != nil {
		return refuse(err.Error(), err)
	}
	if err := wsjson.Write(hctx, c, Welcome{}); err != nil {
		return nil, err
	}
	return newSession(ctx, c, hello, true, cfg.Keepalive)
}
