package api

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1/panelv1connect"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// newBrowserUA is newBrowser with its own User-Agent.
func newBrowserUA(t *testing.T, srv *httptest.Server, ua string) *browser {
	jar, _ := cookiejar.New(nil)
	c := *srv.Client()
	c.Jar = jar
	headers := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			req.Header().Set("Origin", appOrigin)
			req.Header().Set("User-Agent", ua)
			return next(ctx, req)
		}
	})
	return &browser{
		http: &c,
		auth: panelv1connect.NewAuthServiceClient(&c, srv.URL+"/api", connect.WithInterceptors(headers)),
		orgs: panelv1connect.NewOrgServiceClient(&c, srv.URL+"/api", connect.WithInterceptors(headers)),
		cmds: panelv1connect.NewCommandServiceClient(&c, srv.URL+"/api", connect.WithInterceptors(headers)),
	}
}

func TestAuditLog(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, DataKey: key}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: &orgs.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin}))
	defer srv.Close()

	signIn := func(b *browser, email, code string) error {
		t.Helper()
		if code == "" {
			if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email}); err != nil {
				t.Fatal(err)
			}
			code, _ = mail.last(t)
		}
		_, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{
			Code: &panelv1.EmailCode{Email: email, Code: code},
		}})
		return err
	}
	actions := func(events []*panelv1.AuditEvent) string {
		var out []string
		for _, e := range events {
			out = append(out, e.GetAction())
		}
		return strings.Join(out, " ")
	}
	meta := func(e *panelv1.AuditEvent) map[string]any {
		var m map[string]any
		_ = json.Unmarshal([]byte(e.GetMetadataJson()), &m)
		return m
	}
	mails := func() int {
		mail.mu.Lock()
		defer mail.mu.Unlock()
		return len(mail.mail)
	}

	laptop := newBrowserUA(t, srv, "Firefox on Linux")
	if err := signIn(laptop, "alice@example.com", ""); err != nil {
		t.Fatal(err)
	}
	// A wrong code for her account shows up in her activity.
	if _, err := laptop.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: "alice@example.com"}); err != nil {
		t.Fatal(err)
	}
	right, _ := mail.last(t)
	wrong := "000000"
	if right == wrong {
		wrong = "111111"
	}
	if err := signIn(newBrowserUA(t, srv, "Firefox on Linux"), "alice@example.com", wrong); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("wrong code: %v", err)
	}
	// The same browser again: no email.
	before := mails()
	if err := signIn(newBrowserUA(t, srv, "Firefox on Linux"), "alice@example.com", right); err != nil {
		t.Fatal(err)
	}
	if mails() != before {
		t.Errorf("a new-device email for a browser the account has used")
	}
	// A new one: an email saying where from.
	phone := newBrowserUA(t, srv, "Safari on iPhone")
	if err := signIn(phone, "alice@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if m := mail.lastMail(t); !strings.Contains(m, "hasn't used before") || !strings.Contains(m, "Safari on iPhone") {
		t.Errorf("new-device email: %q", m)
	}
	if _, err := phone.auth.SignOut(ctx, &panelv1.SignOutRequest{}); err != nil {
		t.Fatal(err)
	}

	act, err := laptop.auth.ListActivity(ctx, &panelv1.ListActivityRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(act.GetEvents()); got != "session.signout signin signin signin.failed signin" {
		t.Errorf("activity: %s", got)
	}
	newest := act.GetEvents()[1]
	if newest.GetUserAgent() != "Safari on iPhone" || newest.GetIp() == "" || meta(newest)["new_device"] != true || meta(newest)["method"] != "email" {
		t.Errorf("signin event: %v", newest)
	}
	// Someone else's activity isn't in it.
	bob := newBrowserUA(t, srv, "Chrome")
	if err := signIn(bob, "bob@example.com", ""); err != nil {
		t.Fatal(err)
	}
	if act, _ := bob.auth.ListActivity(ctx, &panelv1.ListActivityRequest{}); actions(act.GetEvents()) != "signin" {
		t.Errorf("bob's activity: %s", actions(act.GetEvents()))
	}

	// The org's log: who did what, for admins and owners.
	created, err := laptop.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	if _, err := laptop.orgs.InviteMember(ctx, &panelv1.InviteMemberRequest{OrgId: org, Email: "bob@example.com", Role: panelv1.Role_ROLE_MEMBER}); err != nil {
		t.Fatal(err)
	}
	token := inviteRe.FindStringSubmatch(mail.lastMail(t))[1]
	if _, err := bob.orgs.AcceptInvitation(ctx, &panelv1.AcceptInvitationRequest{Token: token}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member reading the log: %v", err)
	}
	if _, err := newBrowserUA(t, srv, "x").orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org}); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("signed out: %v", err)
	}
	bobID := ""
	members, _ := laptop.orgs.ListMembers(ctx, &panelv1.ListMembersRequest{OrgId: org})
	for _, m := range members.GetMembers() {
		if m.GetEmail() == "bob@example.com" {
			bobID = m.GetUserId()
		}
	}
	if _, err := laptop.orgs.SetMemberRole(ctx, &panelv1.SetMemberRoleRequest{OrgId: org, UserId: bobID, Role: panelv1.Role_ROLE_ADMIN}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.orgs.RemoveMember(ctx, &panelv1.RemoveMemberRequest{OrgId: org, UserId: bobID}); err != nil {
		t.Fatal(err)
	}
	log, err := laptop.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org})
	if err != nil {
		t.Fatal(err)
	}
	if got := actions(log.GetEvents()); got != "member.leave member.role invitation.accept invitation.create org.create" {
		t.Errorf("org log: %s", got)
	}
	if e := log.GetEvents()[1]; e.GetActorEmail() != "alice@example.com" || e.GetTarget() != bobID || meta(e)["role"] != "admin" {
		t.Errorf("role event: %v", e)
	}
	// Bob left, so alice no longer shares an org with him: his address
	// isn't shown on his past events.
	if e := log.GetEvents()[0]; e.GetActorEmail() != "" {
		t.Errorf("a former member's address: %v", e)
	}
	// Pages of 50.
	for i := range 55 {
		if _, err := laptop.orgs.RenameOrg(ctx, &panelv1.RenameOrgRequest{OrgId: org, Name: "Acme " + string(rune('a'+i%26))}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := laptop.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org})
	if err != nil || len(page.GetEvents()) != auth.PageSize || page.GetNextPageToken() == "" {
		t.Fatalf("first page: %d, %v", len(page.GetEvents()), err)
	}
	page2, err := laptop.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org, PageToken: page.GetNextPageToken()})
	if err != nil || len(page2.GetEvents()) != 60-auth.PageSize || page2.GetNextPageToken() != "" {
		t.Fatalf("second page: %d, %q, %v", len(page2.GetEvents()), page2.GetNextPageToken(), err)
	}
	if page2.GetEvents()[len(page2.GetEvents())-1].GetAction() != "org.create" {
		t.Errorf("last page doesn't end at the start")
	}

	// Filters.
	account := func(f *panelv1.ActivityFilter) string {
		t.Helper()
		act, err := laptop.auth.ListActivity(ctx, &panelv1.ListActivityRequest{Filter: f})
		if err != nil {
			t.Fatal(err)
		}
		return actions(act.GetEvents())
	}
	inOrg := func(f *panelv1.ActivityFilter) string {
		t.Helper()
		log, err := laptop.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org, Filter: f})
		if err != nil {
			t.Fatal(err)
		}
		return actions(log.GetEvents())
	}
	future := timestamppb.New(time.Now().Add(time.Hour))
	past := timestamppb.New(time.Now().Add(-time.Hour))
	for _, c := range []struct {
		name string
		got  string
		want string
	}{
		{"failed", account(&panelv1.ActivityFilter{Failed: true}), "signin.failed"},
		{"kind", account(&panelv1.ActivityFilter{Actions: []string{"session.%"}}), "session.signout"},
		{"words", account(&panelv1.ActivityFilter{Query: "IPHONE"}), "session.signout signin"},
		{"wildcards are literal", account(&panelv1.ActivityFilter{Query: "%"}), ""},
		{"range", account(&panelv1.ActivityFilter{Ip: "127.0.0.0/8", Failed: true}), "signin.failed"},
		{"another range", account(&panelv1.ActivityFilter{Ip: "203.0.113.0/24"}), ""},
		{"from", account(&panelv1.ActivityFilter{From: future}), ""},
		{"to", account(&panelv1.ActivityFilter{To: past}), ""},
		{"from and to", account(&panelv1.ActivityFilter{From: past, To: future, Failed: true}), "signin.failed"},
		{"org kind", inOrg(&panelv1.ActivityFilter{Actions: []string{"member.%", "org.create"}}), "member.leave member.role org.create"},
		{"org person", inOrg(&panelv1.ActivityFilter{ActorId: bobID}), "member.leave invitation.accept"},
		{"org words", inOrg(&panelv1.ActivityFilter{Query: "Acme z"}), "org.rename org.rename"},
	} {
		if c.got != c.want {
			t.Errorf("%s: %q, want %q", c.name, c.got, c.want)
		}
	}
	for _, f := range []*panelv1.ActivityFilter{
		{Ip: "nope"},
		{Actions: []string{"x'; drop"}},
		{Query: strings.Repeat("a", 101)},
		{From: future, To: past},
	} {
		if _, err := laptop.auth.ListActivity(ctx, &panelv1.ListActivityRequest{Filter: f}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("filter %v: %v", f, err)
		}
	}
	// A node's: nothing here was done to a node.
	if log, err := laptop.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org, NodeId: uuid.NewString()}); err != nil || len(log.GetEvents()) != 0 {
		t.Errorf("a node's activity: %v, %v", actions(log.GetEvents()), err)
	}
	if _, err := laptop.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org, ServerId: bobID}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a server without its node: %v", err)
	}

	if err := authSvc.Prune(ctx); err != nil {
		t.Error(err)
	}
}
