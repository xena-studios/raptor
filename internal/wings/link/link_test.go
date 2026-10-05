package link

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/shared/nodelink/nodelinktest"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const nodeID = "node-1"

// fixture is a Panel (the real hub) and a node (the real link, executor,
// and outbox), connected through a proxy that can break the connection.
type fixture struct {
	t        *testing.T
	hub      *nodes.Hub
	link     *Link
	outbox   *events.Outbox
	proxy    *nodelinktest.Proxy
	panelKey ed25519.PrivateKey
	runs     atomic.Int32
	started  chan struct{} // a slow command started
	release  chan struct{} // let it finish
	announce chan int64
	files    *fakeFiles
	nodeKey  ed25519.PrivateKey
	panelPub ed25519.PublicKey
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	panelPub, panelPriv, _ := ed25519.GenerateKey(nil)
	nodePub, nodePriv, _ := ed25519.GenerateKey(nil)
	f := &fixture{t: t, panelKey: panelPriv, started: make(chan struct{}, 1), release: make(chan struct{}), announce: make(chan int64, 100)}

	x := &command.Executor{DB: db, NodeID: nodeID, PanelKey: panelPub}
	x.Register("server.start", command.Handler{Signed: command.Never, Run: func(context.Context, command.Envelope) (any, error) {
		f.runs.Add(1)
		return map[string]bool{"ok": true}, nil
	}})
	x.Register("backup.create", command.Handler{Signed: command.Never, Run: func(context.Context, command.Envelope) (any, error) {
		f.runs.Add(1)
		f.started <- struct{}{}
		<-f.release
		return map[string]string{"backup": "b1"}, nil
	}})
	x.Register("server.fail", command.Handler{Signed: command.Never, Run: func(context.Context, command.Envelope) (any, error) {
		return nil, errors.New("it broke")
	}})
	f.outbox = events.New(db)

	f.hub = &nodes.Hub{
		PanelKey: panelPriv,
		NodeKey: func(_ context.Context, id string) (ed25519.PublicKey, error) {
			if id != nodeID {
				return nil, nodelink.ErrUnknownNode
			}
			return nodePub, nil
		},
		EventsAvailable: func(_ context.Context, _ string, last int64) { f.announce <- last },
	}
	srv := httptest.NewServer(f.hub)
	t.Cleanup(srv.Close)
	t.Cleanup(f.hub.Close)
	f.proxy = nodelinktest.NewProxy(t, strings.TrimPrefix(srv.URL, "http://"))

	f.files = &fakeFiles{uploads: map[string][]byte{}, sizes: map[string]int64{}, downloads: map[string][]byte{}}
	f.link = New(Config{
		PanelURL: "http://" + f.proxy.Addr, NodeID: nodeID, NodeKey: nodePriv, PanelKey: panelPub,
		Commands: x, Events: f.outbox, MinBackoff: 10 * time.Millisecond, MaxBackoff: 50 * time.Millisecond,
		Transfers: func() Transfers { return f.files },
	})
	f.nodeKey, f.panelPub = nodePriv, panelPub
	done := make(chan struct{})
	go func() { _ = f.link.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return f
}

func (f *fixture) envelope(action string) []byte {
	id, _ := uuid.NewV7()
	e := command.Envelope{CommandID: id.String(), NodeID: nodeID, UserID: "user-1", Action: action, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
	e.Grant = command.Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ExpiresAt: e.ExpiresAt}
	p, err := e.Grant.Payload()
	if err != nil {
		f.t.Fatal(err)
	}
	e.Grant.Signature = ed25519.Sign(f.panelKey, p)
	b, _ := json.Marshal(e)
	return b
}

func (f *fixture) execute(env []byte) (*nodev1.ExecuteResponse, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return f.hub.Execute(ctx, nodeID, env)
}

func TestCommands(t *testing.T) {
	f := newFixture(t)
	env := f.envelope("server.start")
	res, err := f.execute(env)
	if err != nil || res.GetDuplicate() || string(res.GetResult()) != `{"ok":true}` {
		t.Fatalf("execute: %v, %v", res, err)
	}
	res, err = f.execute(env)
	if err != nil || !res.GetDuplicate() || f.runs.Load() != 1 {
		t.Fatalf("retry: %v, %v, runs %d", res, err, f.runs.Load())
	}
	if st := f.link.Status(); st.State != Connected {
		t.Errorf("status: %+v", st)
	}

	// A command that ran and failed is an answer, not an error.
	res, err = f.execute(f.envelope("server.fail"))
	if err != nil || res.GetError() != "it broke" {
		t.Errorf("failed command: %v, %v", res, err)
	}
	// Refusals are errors, and not retried.
	var bad command.Envelope
	_ = json.Unmarshal(f.envelope("server.start"), &bad)
	bad.Grant.Signature[0] ^= 1
	b, _ := json.Marshal(bad)
	if _, err := f.execute(b); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("forged grant: %v", err)
	}
	if _, err := f.execute(f.envelope("server.format")); connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Errorf("unknown action: %v", err)
	}
}

// The connection dies while a command runs: the command finishes on the
// node anyway, Wings reconnects, and the Panel's retry with the same
// command ID gets its result instead of running it again.
func TestConnectionCutMidCommand(t *testing.T) {
	for _, mode := range []string{"cut", "panel closes"} {
		t.Run(mode, func(t *testing.T) {
			f := newFixture(t)
			if _, err := f.hub.Wait(context.Background(), nodeID); err != nil {
				t.Fatal(err)
			}
			env := f.envelope("backup.create")
			type result struct {
				res *nodev1.ExecuteResponse
				err error
			}
			done := make(chan result, 1)
			go func() {
				res, err := f.execute(env)
				done <- result{res, err}
			}()
			<-f.started
			first, _ := f.hub.Conn(nodeID)
			if mode == "cut" {
				f.proxy.Cut()
			} else {
				_ = first.Session.Close()
			}
			// Back on a new connection while the command still runs: the
			// retry is told it's in progress and keeps trying.
			deadline := time.Now().Add(5 * time.Second)
			for {
				if c, ok := f.hub.Conn(nodeID); ok && c != first {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("Wings didn't reconnect")
				}
				time.Sleep(10 * time.Millisecond)
			}
			time.Sleep(100 * time.Millisecond)
			close(f.release)
			r := <-done
			if r.err != nil || string(r.res.GetResult()) != `{"backup":"b1"}` {
				t.Fatalf("result: %v, %v", r.res, r.err)
			}
			if n := f.runs.Load(); n != 1 {
				t.Errorf("ran %d times", n)
			}
			if st := f.link.Status(); st.Reconnects < 1 {
				t.Errorf("status: %+v", st)
			}
		})
	}
}

func TestEvents(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	c, err := f.hub.Wait(ctx, nodeID)
	if err != nil {
		t.Fatal(err)
	}
	if last := <-f.announce; last != 0 { // on connecting
		t.Errorf("first announcement: %d", last)
	}
	for i := range 3 {
		if _, err := f.outbox.Append(ctx, events.Event{Type: "server.state", ServerID: "s1", Data: map[string]any{"i": i}}); err != nil {
			t.Fatal(err)
		}
	}
	// Appends are announced (coalesced) until the last one is.
	for last := int64(0); last != 3; {
		select {
		case last = <-f.announce:
		case <-time.After(5 * time.Second):
			t.Fatalf("not announced (last %d)", last)
		}
	}
	res, err := c.Node.Events(ctx, &nodev1.EventsRequest{AfterSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetLastSeq() != 3 || len(res.GetEvents()) != 2 || res.GetEvents()[0].GetSeq() != 2 || string(res.GetEvents()[0].GetData()) != `{"i":1}` {
		t.Errorf("events: %v", res)
	}
	if acked, _ := f.outbox.Acked(ctx); acked != 1 {
		t.Errorf("acked %d", acked)
	}
	// The Panel ahead of the node (state.db restored from a snapshot).
	if _, err := c.Node.Events(ctx, &nodev1.EventsRequest{AfterSeq: 9}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("Panel ahead: %v", err)
	}
}

func TestBackoff(t *testing.T) {
	l := New(Config{})
	for n := range 20 {
		ceil := min(MinBackoff<<n, MaxBackoff)
		for range 50 {
			if d := l.backoff(n); d <= 0 || d > ceil {
				t.Fatalf("attempt %d: %s (ceiling %s)", n, d, ceil)
			}
		}
	}
}

func TestNodeKey(t *testing.T) {
	p := filepath.Join(t.TempDir(), "raptor", "node.key")
	if k, err := LoadNodeKey(p); k != nil || err != nil {
		t.Fatalf("missing: %v, %v", k, err)
	}
	pub, err := GenerateNodeKey(p)
	if err != nil {
		t.Fatal(err)
	}
	k, err := LoadNodeKey(p)
	if err != nil || !k.Public().(ed25519.PublicKey).Equal(pub) {
		t.Fatalf("load: %v", err)
	}
	if _, err := GenerateNodeKey(p); err == nil {
		t.Error("replaced an existing key")
	}
	_ = os.Chmod(p, 0o644)
	if _, err := LoadNodeKey(p); err == nil {
		t.Error("loaded a world-readable key")
	}
}

// fakeFiles is the file service's transfer side, in memory.
type fakeFiles struct {
	mu        sync.Mutex
	uploads   map[string][]byte
	sizes     map[string]int64
	downloads map[string][]byte
}

func (f *fakeFiles) HasTransfer(_ context.Context, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, up := f.sizes[id]
	_, down := f.downloads[id]
	return up || down
}

func (f *fakeFiles) WriteChunk(_ context.Context, id string, offset int64, r io.Reader) (files.Upload, error) {
	f.mu.Lock()
	got, size := f.uploads[id], f.sizes[id]
	f.mu.Unlock()
	up := files.Upload{ID: id, Size: size, Received: int64(len(got))}
	if offset != up.Received {
		return up, files.ErrOffset
	}
	b, err := io.ReadAll(r)
	if err != nil {
		return up, err // a chunk cut short is dropped, as Wings does
	}
	f.mu.Lock()
	f.uploads[id] = append(got, b...)
	up.Received = int64(len(f.uploads[id]))
	f.mu.Unlock()
	up.Done = up.Received == size
	return up, nil
}

func (f *fakeFiles) ReadChunk(_ context.Context, id string, offset, n int64, w io.Writer) (int64, error) {
	f.mu.Lock()
	b, ok := f.downloads[id]
	f.mu.Unlock()
	if !ok {
		return 0, files.ErrTransferNotFound
	}
	m, err := w.Write(b[offset:min(offset+n, int64(len(b)))])
	return int64(m), err
}

func TestTransfer(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.hub.Wait(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	data := make([]byte, 5<<20)
	_, _ = rand.Read(data)
	f.files.sizes["up-1"] = int64(len(data))
	f.files.downloads["down-1"] = data

	// An upload in chunks; the control connection keeps answering.
	tr, err := f.hub.OpenTransfer(ctx, nodeID, "up-1")
	if err != nil {
		t.Fatal(err)
	}
	const chunk = 2 << 20
	var st nodes.UploadState
	for off := int64(0); off < int64(len(data)); off += chunk {
		end := min(off+chunk, int64(len(data)))
		if st, err = tr.Upload(ctx, off, bytes.NewReader(data[off:end])); err != nil {
			t.Fatal(err)
		}
	}
	if !st.Done || !bytes.Equal(f.files.uploads["up-1"], data) {
		t.Fatalf("upload: %+v", st)
	}
	// A chunk at the wrong offset is refused with where to resume.
	if st, err := tr.Upload(ctx, 1, bytes.NewReader([]byte("x"))); !errors.Is(err, nodes.ErrTransfer) || st.Received != int64(len(data)) {
		t.Errorf("wrong offset: %+v, %v", st, err)
	}
	_ = tr.Close()

	// A download, resumed from an offset on a new connection.
	var out bytes.Buffer
	tr, err = f.hub.OpenTransfer(ctx, nodeID, "down-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Download(ctx, 0, 3<<20, int64(len(data)), &out); err != nil {
		t.Fatal(err)
	}
	_ = tr.Close()
	tr, err = f.hub.OpenTransfer(ctx, nodeID, "down-1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tr.Close() }()
	if _, err := tr.Download(ctx, int64(out.Len()), 3<<20, int64(len(data)), &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Error("download differs")
	}

	// Only transfers that exist get a connection.
	if _, err := f.hub.OpenTransfer(ctx, nodeID, "nope"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("unknown transfer: %v", err)
	}
}

// A transfer connection the Panel didn't ask for is closed: a node can't
// open one on its own.
func TestUnrequestedTransfer(t *testing.T) {
	f := newFixture(t)
	s, err := nodelink.Dial(context.Background(), nodelink.DialConfig{
		URL: "http://" + f.proxy.Addr, NodeID: nodeID, NodeKey: f.nodeKey, PanelKey: f.panelPub, Purpose: nodelink.TransferPurpose("up-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the Panel kept an unrequested transfer connection")
	}
}
