package api

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/mclogs"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// Sharing a log hides IP addresses before mclo.gs sees it, gives back
// mclo.gs' link, and is in the audit log; someone without access to the
// server can't.
func TestShareLog(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	var sent string
	mc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sent = r.FormValue("content")
		_, _ = w.Write([]byte(`{"success":true,"id":"abc","url":"https://mclo.gs/abc"}`))
	}))
	defer mc.Close()
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	orgSvc := &orgs.Service{
		DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key},
		Logs: &mclogs.Client{Base: mc.URL, UserAgent: "test"},
	}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, AppOrigin: appOrigin}))
	defer srv.Close()
	b := newBrowser(t, srv, appOrigin)
	if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	code, _ := mail.last(t)
	if _, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{Code: &panelv1.EmailCode{Email: "alice@example.com", Code: code}}}); err != nil {
		t.Fatal(err)
	}
	created, _ := b.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	org := created.GetOrg().GetId()
	var node string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at, config) VALUES ($1, 's1', 'mc', 1, now(), now(), '{}')`, node); err != nil {
		t.Fatal(err)
	}

	res, err := b.orgs.ShareLog(ctx, &panelv1.ShareLogRequest{
		OrgId: org, NodeId: node, ServerId: "s1", Name: "console",
		Content: "Steve[/203.0.113.7:51234] logged in\nListening on 0.0.0.0:25565",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.GetUrl() != "https://mclo.gs/abc" {
		t.Errorf("url: %q", res.GetUrl())
	}
	if strings.Contains(sent, "203.0.113.7") || !strings.Contains(sent, "0.0.0.0:25565") {
		t.Errorf("sent to mclo.gs: %q", sent)
	}
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'server.log.share'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("audit log: %d, %v", n, err)
	}

	if _, err := b.orgs.ShareLog(ctx, &panelv1.ShareLogRequest{OrgId: org, NodeId: node, ServerId: "s1", Name: "console", Content: "  "}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("empty: %v", err)
	}
	if _, err := b.orgs.ShareLog(ctx, &panelv1.ShareLogRequest{OrgId: org, NodeId: node, ServerId: "nope", Name: "console", Content: "x"}); err == nil {
		t.Error("another server's log was shared")
	}
	other := newBrowser(t, srv, appOrigin)
	if _, err := other.orgs.ShareLog(ctx, &panelv1.ShareLogRequest{OrgId: org, NodeId: node, ServerId: "s1", Name: "console", Content: "x"}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("signed out: %v", err)
	}
}
