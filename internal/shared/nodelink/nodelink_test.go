package nodelink

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/shared/nodelink/nodelinktest"
)

type keys struct {
	panelPub  ed25519.PublicKey
	panelPriv ed25519.PrivateKey
	nodePub   ed25519.PublicKey
	nodePriv  ed25519.PrivateKey
}

func newKeys(t *testing.T) keys {
	var k keys
	var err error
	if k.panelPub, k.panelPriv, err = ed25519.GenerateKey(nil); err != nil {
		t.Fatal(err)
	}
	if k.nodePub, k.nodePriv, err = ed25519.GenerateKey(nil); err != nil {
		t.Fatal(err)
	}
	return k
}

func TestHandshake(t *testing.T) {
	k := newKeys(t)
	now := time.Now()
	h, err := NewHello("node-1", PurposeControl, "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Validate(); err != nil {
		t.Fatal(err)
	}
	c, err := NewChallenge(h, k.panelPriv, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyChallenge(h, c, k.panelPub, now); err != nil {
		t.Fatal(err)
	}
	p, err := NewProof(h, c, k.nodePriv, now)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyProof(h, c, p, k.nodePub, now); err != nil {
		t.Fatal(err)
	}

	// Another Panel's key, or a Panel signature presented as the node's.
	other := newKeys(t)
	if err := VerifyChallenge(h, c, other.panelPub, now); !errors.Is(err, ErrBadSignature) {
		t.Errorf("another Panel's challenge: %v", err)
	}
	if err := VerifyProof(h, c, Proof{Time: c.Time, Signature: c.Signature}, k.panelPub, now); !errors.Is(err, ErrBadSignature) {
		t.Errorf("the Panel's signature as the node's proof: %v", err)
	}
	// A proof from one connection doesn't work on another (fresh nonces).
	h2, _ := NewHello("node-1", PurposeControl, "1.0.0")
	c2, _ := NewChallenge(h2, k.panelPriv, now)
	if err := VerifyProof(h2, c2, p, k.nodePub, now); !errors.Is(err, ErrBadSignature) {
		t.Errorf("replayed proof: %v", err)
	}
	// Nor for another purpose or node.
	for _, mod := range []func(*Hello){
		func(h *Hello) { h.Purpose = TransferPurpose("x") },
		func(h *Hello) { h.NodeID = "node-2" },
	} {
		h3 := h
		mod(&h3)
		if err := VerifyProof(h3, c, p, k.nodePub, now); !errors.Is(err, ErrBadSignature) {
			t.Errorf("proof for %+v: %v", h3, err)
		}
	}
	// Clocks too far apart.
	if err := VerifyProof(h, c, p, k.nodePub, now.Add(MaxSkew+time.Minute)); !errors.Is(err, ErrClockSkew) {
		t.Errorf("skew: %v", err)
	}
}

func TestHelloValidate(t *testing.T) {
	h, _ := NewHello("node-1", PurposeControl, "")
	for name, mod := range map[string]func(*Hello){
		"no node":        func(h *Hello) { h.NodeID = "" },
		"short nonce":    func(h *Hello) { h.Nonce = h.Nonce[:8] },
		"purpose":        func(h *Hello) { h.Purpose = "shell" },
		"empty transfer": func(h *Hello) { h.Purpose = TransferPurpose("") },
	} {
		h2 := h
		mod(&h2)
		if h2.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if id, ok := TransferID(TransferPurpose("abc")); !ok || id != "abc" {
		t.Errorf("transfer ID: %q %v", id, ok)
	}
}

func TestWebSocketURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://api.raptorpanel.net":  "wss://api.raptorpanel.net/nodes/connect",
		"https://api.raptorpanel.net/": "wss://api.raptorpanel.net/nodes/connect",
		"http://127.0.0.1:8080":        "ws://127.0.0.1:8080/nodes/connect",
	} {
		if got, err := WebSocketURL(in); err != nil || got != want {
			t.Errorf("%s: %s, %v", in, got, err)
		}
	}
	if _, err := WebSocketURL("ftp://x"); err == nil {
		t.Error("ftp accepted")
	}
}

// panel serves node connections; each session answers /who with the node ID
// and calls back the node's /echo.
func panel(t *testing.T, k keys, sessions chan<- *Session) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := Accept(context.Background(), w, r, AcceptConfig{
			PanelKey: k.panelPriv,
			NodeKey: func(_ context.Context, id string) (ed25519.PublicKey, error) {
				if id != "node-1" {
					return nil, ErrUnknownNode
				}
				return k.nodePub, nil
			},
		})
		if err != nil {
			return
		}
		sessions <- s
		_ = s.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, FromContext(r.Context()).Hello.NodeID)
		}))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestSession(t *testing.T) {
	k := newKeys(t)
	sessions := make(chan *Session, 1)
	srv := panel(t, k, sessions)
	ctx := context.Background()
	s, err := Dial(ctx, DialConfig{URL: srv.URL, NodeID: "node-1", NodeKey: k.nodePriv, PanelKey: k.panelPub, Purpose: PurposeControl})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	go func() {
		_ = s.Serve(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			_, _ = w.Write(b)
		}))
	}()
	ps := <-sessions

	// Wings → Panel.
	resp, err := s.Client().Get(BaseURL + "/who")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(b) != "node-1" {
		t.Errorf("who: %q", b)
	}
	// Panel → Wings, many at once.
	errc := make(chan error, 20)
	for i := range 20 {
		go func() {
			msg := strings.Repeat("x", i*10_000)
			resp, err := ps.Client().Post(BaseURL+"/echo", "text/plain", strings.NewReader(msg))
			if err != nil {
				errc <- err
				return
			}
			defer func() { _ = resp.Body.Close() }()
			b, _ := io.ReadAll(resp.Body)
			if string(b) != msg {
				errc <- errors.New("echo mismatch")
				return
			}
			errc <- nil
		}()
	}
	for range 20 {
		if err := <-errc; err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Ping(); err != nil {
		t.Fatal(err)
	}

	// Closing one side ends both.
	_ = ps.Close()
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("Wings' session didn't notice the Panel closing it")
	}
}

func TestDialRefused(t *testing.T) {
	k := newKeys(t)
	srv := panel(t, k, make(chan *Session, 1))
	ctx := context.Background()

	// A node the Panel doesn't know.
	_, err := Dial(ctx, DialConfig{URL: srv.URL, NodeID: "node-9", NodeKey: k.nodePriv, PanelKey: k.panelPub, Purpose: PurposeControl})
	var refused *RefusedError
	if !errors.As(err, &refused) || !strings.Contains(refused.Reason, "unknown") {
		t.Errorf("unknown node: %v", err)
	}
	// The wrong node key.
	other := newKeys(t)
	_, err = Dial(ctx, DialConfig{URL: srv.URL, NodeID: "node-1", NodeKey: other.nodePriv, PanelKey: k.panelPub, Purpose: PurposeControl})
	if !errors.As(err, &refused) {
		t.Errorf("wrong node key: %v", err)
	}
	// A Panel that isn't the pinned one: Wings hangs up before proving itself.
	_, err = Dial(ctx, DialConfig{URL: srv.URL, NodeID: "node-1", NodeKey: k.nodePriv, PanelKey: other.panelPub, Purpose: PurposeControl})
	if !errors.Is(err, ErrBadSignature) {
		t.Errorf("impostor Panel: %v", err)
	}
}

// A connection that silently stops passing bytes is closed on both sides
// once pings go unanswered for the dead-after time.
func TestDeadConnection(t *testing.T) {
	k := newKeys(t)
	ka := Keepalive{Ping: 50 * time.Millisecond, Dead: 300 * time.Millisecond}
	sessions := make(chan *Session, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s, err := Accept(context.Background(), w, r, AcceptConfig{
			PanelKey: k.panelPriv, Keepalive: ka,
			NodeKey: func(context.Context, string) (ed25519.PublicKey, error) { return k.nodePub, nil },
		})
		if err != nil {
			return
		}
		sessions <- s
		<-s.Done()
	}))
	defer srv.Close()
	p := nodelinktest.NewProxy(t, strings.TrimPrefix(srv.URL, "http://"))
	s, err := Dial(context.Background(), DialConfig{
		URL: "http://" + p.Addr, NodeID: "node-1", NodeKey: k.nodePriv, PanelKey: k.panelPub, Purpose: PurposeControl, Keepalive: ka,
	})
	if err != nil {
		t.Fatal(err)
	}
	ps := <-sessions
	time.Sleep(200 * time.Millisecond) // pings keep it up
	select {
	case <-s.Done():
		t.Fatal("closed while healthy")
	default:
	}
	if s.RTT() == 0 {
		t.Error("no ping answered")
	}
	p.Freeze()
	for name, x := range map[string]*Session{"Wings": s, "Panel": ps} {
		select {
		case <-x.Done():
		case <-time.After(3 * time.Second):
			t.Errorf("%s didn't notice the dead connection", name)
		}
	}
}
