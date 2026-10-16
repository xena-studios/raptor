package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/commands"
	"github.com/xena-studios/raptor/internal/panel/live"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
)

// Support access through the Panel: staff ask, owners are emailed, an
// owner's grant must match the request, staff act only within what was
// granted (the grant names the request), and ending it stops them.
func TestSupportRequests(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	node := &fakeNode{t: t, panelKey: pub}
	consoles := &fakeConsoles{t: t, panelKey: pub, push: make(chan string, 1)}
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	staffSvc := &staff.Service{DB: db, Auth: authSvc}
	srv := httptest.NewTLSServer(Handler(Config{
		Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}},
		Commands: &commands.Service{Auth: authSvc, Sender: node, Consoles: consoles, PanelKey: key}, Staff: staffSvc, AppOrigin: appOrigin,
	}))
	defer srv.Close()
	signUp := func(email string) (*browser, string) {
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
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	owner, _ := signUp("owner@example.com")
	sup, supID := signUp("support@raptor.example")
	other, otherID := signUp("other@raptor.example")
	created, _ := owner.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	org := created.GetOrg().GetId()
	var nodeID string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key) VALUES ($1, 'box', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	for _, s := range []struct{ email, id string }{{"support@raptor.example", supID}, {"other@raptor.example", otherID}} {
		if err := staff.CLI(ctx, db, &out, []string{"add", s.email, "support"}); err != nil {
			t.Fatal(err)
		}
		exec(`INSERT INTO passkeys (user_id, credential_id, credential, name) VALUES ($1::uuid, $2, '{}', 'key')`, s.id, []byte(s.id))
		exec(`UPDATE sessions SET passkey_at = now() WHERE user_id = $1`, s.id)
	}

	// Asking: the owner hears about it.
	req, err := sup.staff.RequestSupportAccess(ctx, &panelv1.RequestSupportAccessRequest{
		NodeId: nodeID, Level: 1, Minutes: 60, Reason: "won't start after update", Ticket: "T-42",
	})
	if err != nil {
		t.Fatal(err)
	}
	mail.mu.Lock()
	body := mail.mail[len(mail.mail)-1]
	mail.mu.Unlock()
	if !strings.HasPrefix(body, "owner@example.com") || !strings.Contains(body, "won't start after update") || !strings.Contains(body, "your passkey") {
		t.Errorf("the owner's email: %s", body)
	}
	list, err := owner.orgs.ListNodeSupportRequests(ctx, &panelv1.ListNodeSupportRequestsRequest{OrgId: org, NodeId: nodeID})
	if err != nil || len(list.GetRequests()) != 1 || list.GetRequests()[0].GetStatus() != "pending" {
		t.Fatalf("the owner's list: %v, %v", list, err)
	}
	staffRun := func(b *browser, action, request string) error {
		_, err := b.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, Action: action, ParamsJson: `{}`, Support: request})
		return err
	}
	if err := staffRun(sup, "node.health", req.GetId()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("before it's approved: %v", err)
	}

	// The owner's grant has to match what was asked.
	grant := func(level int, d time.Duration, staffID string) error {
		p, _ := json.Marshal(map[string]any{
			"id": req.GetId(), "staff_id": staffID, "staff_email": "support@raptor.example", "level": level,
			"reason": "won't start after update", "ticket": "T-42", "expires_at": time.Now().Add(d).UnixMilli(),
		})
		_, err := owner.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, Action: "support.grant", ParamsJson: string(p)})
		return err
	}
	if err := grant(2, time.Hour, supID); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("more than asked: %v", err)
	}
	if err := grant(1, 3*time.Hour, supID); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("longer than asked: %v", err)
	}
	if err := grant(1, time.Hour, otherID); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("someone else: %v", err)
	}
	if err := grant(1, 30*time.Minute, supID); err != nil {
		t.Fatal(err)
	}

	// Under it: diagnostics, named, as the staff member; nothing more.
	if err := staffRun(sup, "node.health", req.GetId()); err != nil {
		t.Fatalf("diagnostics: %v", err)
	}
	if e := node.last(); e.Grant.Support != req.GetId() || e.UserID != supID {
		t.Errorf("the command sent: %+v", e.Grant)
	}
	if err := staffRun(sup, "server.stop", req.GetId()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("stopping at level 1: %v", err)
	}
	if err := staffRun(sup, "support.grant", req.GetId()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("granting itself more: %v", err)
	}
	if err := staffRun(other, "node.health", req.GetId()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("another staff member on it: %v", err)
	}
	if err := staffRun(owner, "node.health", req.GetId()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a customer using it: %v", err)
	}

	// Ended by the owner: over.
	if _, err := owner.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, Action: "support.revoke", ParamsJson: `{"all":true}`}); err != nil {
		t.Fatal(err)
	}
	if err := staffRun(sup, "node.health", req.GetId()); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("after it's ended: %v", err)
	}
	all, _ := sup.staff.ListSupportRequests(ctx, &panelv1.ListSupportRequestsRequest{})
	if len(all.GetRequests()) != 1 || all.GetRequests()[0].GetStatus() != "revoked" || !all.GetRequests()[0].GetMine() {
		t.Errorf("staff's list: %v", all)
	}

	// Denying.
	r2, _ := sup.staff.RequestSupportAccess(ctx, &panelv1.RequestSupportAccessRequest{NodeId: nodeID, Level: 2, Minutes: 15, Reason: "x", Ticket: "T-43"})
	if _, err := owner.orgs.DenySupportRequest(ctx, &panelv1.DenySupportRequestRequest{OrgId: org, RequestId: r2.GetId()}); err != nil {
		t.Fatal(err)
	}
	if _, err := owner.orgs.DenySupportRequest(ctx, &panelv1.DenySupportRequestRequest{OrgId: org, RequestId: r2.GetId()}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("denying twice: %v", err)
	}
	// Level 2: a server's console too, over the live socket, under the
	// request; the request that was ended can't open one.
	r3, err := sup.staff.RequestSupportAccess(ctx, &panelv1.RequestSupportAccessRequest{NodeId: nodeID, Level: 2, Minutes: 30, Reason: "lag", Ticket: "T-44"})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := json.Marshal(map[string]any{
		"id": r3.GetId(), "staff_id": supID, "staff_email": "support@raptor.example", "level": 2,
		"reason": "lag", "ticket": "T-44", "expires_at": time.Now().Add(30 * time.Minute).UnixMilli(),
	})
	if _, err := owner.cmds.Execute(ctx, &panelv1.ExecuteRequest{NodeId: nodeID, Action: "support.grant", ParamsJson: string(p)}); err != nil {
		t.Fatal(err)
	}
	ws, resp, err := websocket.Dial(ctx, "wss"+strings.TrimPrefix(srv.URL, "https")+"/api/live", &websocket.DialOptions{
		HTTPClient: sup.http, HTTPHeader: http.Header{"Origin": {appOrigin}},
	})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ws.CloseNow() }()
	watch := func(id, request string) live.Out {
		t.Helper()
		if err := wsjson.Write(ctx, ws, live.In{Op: "console", ID: id, Node: nodeID, Server: "s1", Support: request}); err != nil {
			t.Fatal(err)
		}
		rctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		var m live.Out
		if err := wsjson.Read(rctx, ws, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	if m := watch("old", req.GetId()); m.Type != "ended" || m.Error == "" {
		t.Errorf("a console under the ended request: %+v", m)
	}
	if m := watch("s", r3.GetId()); m.Type != "lines" || m.Lines[0] != "history" {
		t.Fatalf("a console under level 2: %+v", m)
	}
	consoles.mu.Lock()
	opened := consoles.opened[len(consoles.opened)-1]
	consoles.mu.Unlock()
	if opened.Grant.Support != r3.GetId() || opened.UserID != supID {
		t.Errorf("the console's grant: %+v, user %s", opened.Grant, opened.UserID)
	}

	// The org's log has it, as staff.
	var n int
	_ = db.QueryRow(ctx, "SELECT count(*) FROM audit_log WHERE actor = 'staff' AND (action = 'support.request' OR action = 'command')").Scan(&n)
	if n < 3 {
		t.Errorf("staff entries in the org's log: %d", n)
	}
}
