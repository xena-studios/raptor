package nodes

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/link"
)

// oneUpload is a node with a single upload in progress.
type oneUpload struct {
	mu  sync.Mutex
	got []byte
}

func (u *oneUpload) HasTransfer(_ context.Context, id string) bool { return id == "up1" }

func (u *oneUpload) WriteChunk(_ context.Context, _ string, offset int64, r io.Reader) (files.Upload, error) {
	b, err := io.ReadAll(r)
	u.mu.Lock()
	defer u.mu.Unlock()
	if offset != int64(len(u.got)) {
		return files.Upload{Received: int64(len(u.got))}, files.ErrOffset
	}
	u.got = append(u.got, b...)
	return files.Upload{ID: "up1", Size: 5, Received: int64(len(u.got)), Done: len(u.got) == 5}, err
}

func (u *oneUpload) ReadChunk(context.Context, string, int64, int64, io.Writer) (int64, error) {
	return 0, files.ErrTransferNotFound
}

// Two instances behind one proxy, which sends /i/a and /i/b to each and
// everything else to a. The node is connected to a; a transfer opened
// through b reaches b, by b's route, so its bytes never pass through a.
func TestTransferReachesTheInstanceThatAsked(t *testing.T) {
	r := newRegistry(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, b := newInstance(t, ctx, r), newInstance(t, ctx, r)
	a.hub.Route, b.hub.Route = "/i/a", "/i/b"

	proxyTo := func(in *instance, strip string) http.Handler {
		u, _ := url.Parse(in.srv.URL)
		p := httputil.NewSingleHostReverseProxy(u)
		if strip == "" {
			return p
		}
		return http.StripPrefix(strip, p)
	}
	var viaB sync.Mutex
	hitB := 0
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasPrefix(req.URL.Path, "/i/a/"):
			proxyTo(a, "/i/a").ServeHTTP(w, req)
		case strings.HasPrefix(req.URL.Path, "/i/b/"):
			viaB.Lock()
			hitB++
			viaB.Unlock()
			proxyTo(b, "/i/b").ServeHTTP(w, req)
		default:
			proxyTo(a, "").ServeHTTP(w, req)
		}
	}))
	defer proxy.Close()

	org, _ := r.CreateOrg(ctx, "org")
	token, _ := r.CreateJoinToken(ctx, org, "")
	_, nodeKey, _ := ed25519.GenerateKey(nil)
	res, err := r.Enroll(ctx, enrollReq(t, token, nodeKey))
	if err != nil {
		t.Fatal(err)
	}
	nodeID := res.GetNodeId()
	up := &oneUpload{}
	l := link.New(link.Config{
		PanelURL: proxy.URL, NodeID: nodeID, NodeKey: nodeKey, PanelKey: res.GetPanelKey(),
		Transfers:  func() link.Transfers { return up },
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
	})
	go func() { _ = l.Run(ctx) }()
	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	if _, err := a.hub.Wait(wctx, nodeID); err != nil {
		t.Fatal(err)
	}

	tr, err := b.router.OpenTransfer(wctx, nodeID, "up1")
	if err != nil {
		t.Fatalf("opening a transfer through the other instance: %v", err)
	}
	defer func() { _ = tr.Close() }()
	st, err := tr.Upload(wctx, 0, bytes.NewReader([]byte("hello")))
	if err != nil || !st.Done || string(up.got) != "hello" {
		t.Fatalf("upload through b: %+v, %v (node has %q)", st, err, up.got)
	}
	viaB.Lock()
	defer viaB.Unlock()
	if hitB == 0 {
		t.Error("the transfer connection didn't come through b's route")
	}
}
