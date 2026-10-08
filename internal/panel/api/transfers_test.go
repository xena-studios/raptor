package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/link"
	wstore "github.com/xena-studios/raptor/internal/wings/store"
)

// nodeFiles is a node's side of one upload (up1) and one download (dl1) on
// server s1.
type nodeFiles struct {
	mu   sync.Mutex
	got  []byte
	file []byte
}

func (n *nodeFiles) HasTransfer(_ context.Context, id string) bool { return id == "up1" || id == "dl1" }

func (n *nodeFiles) WriteChunk(_ context.Context, _ string, offset int64, r io.Reader) (files.Upload, error) {
	b, err := io.ReadAll(r)
	n.mu.Lock()
	defer n.mu.Unlock()
	up := files.Upload{ID: "up1", Size: 5, Received: int64(len(n.got)), MaxChunk: 64 << 20}
	if offset != up.Received {
		return up, files.ErrOffset
	}
	if err != nil {
		return up, err
	}
	n.got = append(n.got, b...)
	up.Received = int64(len(n.got))
	up.Done = up.Received == up.Size
	return up, nil
}

func (n *nodeFiles) ReadChunk(_ context.Context, _ string, offset, size int64, w io.Writer) (int64, error) {
	end := min(offset+size, int64(len(n.file)))
	c, err := w.Write(n.file[offset:end])
	return int64(c), err
}

func (n *nodeFiles) status() files.Upload {
	n.mu.Lock()
	defer n.mu.Unlock()
	return files.Upload{ID: "up1", Size: 5, Received: int64(len(n.got)), Done: len(n.got) == 5, MaxChunk: 64 << 20}
}

// Uploads and downloads through the Panel to a real node link: chunks go
// on a transfer connection, an upload resumes from what the node has, a
// download arrives whole in several chunks, and both need the user's
// access to the server's files.
func TestFileTransfers(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mail := &inbox{}
	panelPub, panelKey, _ := ed25519.GenerateKey(rand.Reader)
	nodePub, nodeKey, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}

	signUp := func(srv *httptest.Server, email string) (*browser, string) {
		t.Helper()
		b := newBrowser(t, srv, appOrigin)
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		res, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
			Code: &panelv1.EmailCode{Email: email, Code: code},
		}})
		if err != nil {
			t.Fatal(err)
		}
		return b, res.GetUser().GetId()
	}

	var nodeID string
	hub := &nodes.Hub{
		PanelKey: panelKey,
		NodeKey: func(_ context.Context, id string) (ed25519.PublicKey, error) {
			if id != nodeID {
				return nil, nodelink.ErrUnknownNode
			}
			return nodePub, nil
		},
	}
	defer hub.Close()
	cmds := &commands.Service{Auth: authSvc, Sender: hub, PanelKey: panelKey}
	mux := http.NewServeMux()
	mux.Handle("GET "+nodelink.Path, hub)
	mux.Handle("/", Handler(Config{Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, Commands: cmds, Transfers: hub, AppOrigin: appOrigin}))
	srv := httptest.NewTLSServer(mux)
	defer srv.Close()

	alice, _ := signUp(srv, "alice@example.com")
	bob, bobID := signUp(srv, "bob@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'xfer1234', $2) RETURNING id`, org, []byte(nodePub)).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at) VALUES ($1, 's1', 'mc', 1, now(), now())`, nodeID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')", org, bobID); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.orgs.SetServerAccess(ctx, &panelv1.SetServerAccessRequest{OrgId: org, NodeId: nodeID, ServerId: "s1", UserId: bobID, Permissions: []string{"files.read"}}); err != nil {
		t.Fatal(err)
	}

	// The node: a real link and executor, with the file service faked.
	nf := &nodeFiles{file: bytes.Repeat([]byte("0123456789"), 10)}
	wdb, err := wstore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wdb.Close() }()
	x := &command.Executor{DB: wdb, NodeID: nodeID, PanelKey: panelPub}
	readOnly := func(run func(command.Envelope) (any, error)) command.Handler {
		return command.Handler{Signed: command.Never, ReadOnly: true, Run: func(_ context.Context, e command.Envelope) (any, error) {
			if e.ServerID != "s1" {
				return nil, files.ErrTransferNotFound
			}
			return run(e)
		}}
	}
	x.Register("files.upload.status", readOnly(func(e command.Envelope) (any, error) {
		var p struct {
			ID string `json:"upload_id"`
		}
		_ = json.Unmarshal(e.Params, &p)
		if p.ID != "up1" {
			return nil, files.ErrTransferNotFound
		}
		return nf.status(), nil
	}))
	x.Register("files.download", readOnly(func(command.Envelope) (any, error) {
		return map[string]any{"download_id": "dl1", "size": len(nf.file), "max_chunk": 30}, nil
	}))
	l := link.New(link.Config{
		PanelURL: srv.URL, NodeID: nodeID, NodeKey: nodeKey, PanelKey: panelPub, Commands: x,
		Transfers: func() link.Transfers { return nf }, HTTPClient: srv.Client(),
		MinBackoff: 10 * time.Millisecond, MaxBackoff: 100 * time.Millisecond,
	})
	go func() { _ = l.Run(ctx) }()
	wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
	defer wcancel()
	if _, err := hub.Wait(wctx, nodeID); err != nil {
		t.Fatal(err)
	}

	put := func(b *browser, upload string, offset int, body string) (int, files.Upload) {
		t.Helper()
		u := fmt.Sprintf("%s/api/files/upload?node=%s&server=s1&upload=%s&offset=%d", srv.URL, nodeID, upload, offset)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPut, u, strings.NewReader(body))
		req.Header.Set("Origin", appOrigin)
		resp, err := b.http.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		var st files.Upload
		_ = json.NewDecoder(resp.Body).Decode(&st)
		return resp.StatusCode, st
	}

	// Alice (an owner) uploads in two chunks; a chunk at the wrong offset
	// gets the state to resume from.
	if code, st := put(alice, "up1", 0, "hel"); code != http.StatusOK || st.Received != 3 {
		t.Fatalf("first chunk: %d %+v", code, st)
	}
	if code, st := put(alice, "up1", 0, "hel"); code != http.StatusConflict || st.Received != 3 {
		t.Fatalf("a repeated chunk: %d %+v", code, st)
	}
	if code, st := put(alice, "up1", 3, "lo"); code != http.StatusOK || !st.Done || string(nf.got) != "hello" {
		t.Fatalf("last chunk: %d %+v, node has %q", code, st, nf.got)
	}
	// Bob may read the files but not write them; an upload that isn't this
	// server's is unknown.
	if code, _ := put(bob, "up1", 5, "x"); code != http.StatusForbidden {
		t.Errorf("a member without files.write: %d", code)
	}
	if code, _ := put(alice, "up9", 0, "x"); code != http.StatusBadRequest && code != http.StatusNotFound {
		t.Errorf("an unknown upload: %d", code)
	}

	get := func(b *browser) (*http.Response, []byte) {
		t.Helper()
		resp, err := b.http.Get(fmt.Sprintf("%s/api/files/download?node=%s&server=s1&path=%s", srv.URL, nodeID, "logs/latest.log"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		return resp, body
	}
	resp, body := get(bob)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(body, nf.file) {
		t.Fatalf("download: %d, %d bytes", resp.StatusCode, len(body))
	}
	if cd := resp.Header.Get("Content-Disposition"); cd != `attachment; filename=latest.log` {
		t.Errorf("Content-Disposition %q", cd)
	}
	if resp, _ := get(newBrowser(t, srv, appOrigin)); resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("signed out: %d", resp.StatusCode)
	}
}
