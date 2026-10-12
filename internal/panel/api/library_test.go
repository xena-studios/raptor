package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/xena-studios/raptor/internal/eggs"
	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
)

func helloSpec(name string) *panelv1.EggSpec {
	return &panelv1.EggSpec{
		Name:      name,
		Images:    []*panelv1.EggImage{{Name: "Debian", Ref: "ghcr.io/pterodactyl/yolks:debian"}},
		Startup:   []*panelv1.EggStartup{{Command: "./hello {{GREETING}}"}},
		StopType:  "command",
		StopValue: "stop",
		Done:      []string{"ready"},
		Variables: []*panelv1.EggVariable{{Name: "Greeting", Env: "GREETING", Default: "hi", UserViewable: true, UserEditable: true, Rules: []string{"required", "string"}}},
	}
}

// The egg library: an org's own eggs (made, edited as versions,
// customized from built-in ones, published, archived, deleted) beside
// Raptor's official ones, which only staff admins change.
func TestEggLibrary(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	now := time.Now()
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Now: func() time.Time { return now }}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, Staff: &staff.Service{DB: db, Auth: authSvc}, AppOrigin: appOrigin}))
	defer srv.Close()
	signUp := func(email string) (*browser, string) {
		t.Helper()
		b := newBrowser(t, srv, appOrigin)
		if _, err := b.auth.StartEmailSignIn(ctx, &panelv1.StartEmailSignInRequest{Email: email}); err != nil {
			t.Fatal(err)
		}
		code, _ := mail.last(t)
		res, err := b.auth.FinishEmailSignIn(ctx, &panelv1.FinishEmailSignInRequest{Proof: &panelv1.FinishEmailSignInRequest_Code{Code: &panelv1.EmailCode{Email: email, Code: code}}})
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
	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	carol, _ := signUp("carol@example.com")
	admin, adminID := signUp("admin@raptor.example")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	exec(`INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')`, org, bobID)
	other, err := carol.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Other"})
	if err != nil {
		t.Fatal(err)
	}

	// From scratch: a draft, made by an admin, checked first.
	if _, err := bob.lib.CreateEgg(ctx, &panelv1.CreateEggRequest{OrgId: org, Spec: helloSpec("Hello")}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member creating: %v", err)
	}
	bad := helloSpec("Hello")
	bad.Variables[0].Env = "SERVER_PORT"
	if _, err := alice.lib.CreateEgg(ctx, &panelv1.CreateEggRequest{OrgId: org, Spec: bad}); connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "set by Raptor") {
		t.Errorf("a reserved variable: %v", err)
	}
	if _, err := alice.lib.CreateEgg(ctx, &panelv1.CreateEggRequest{OrgId: org, Spec: helloSpec("Hello"), Category: "nope"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("an unknown category: %v", err)
	}
	made, err := alice.lib.CreateEgg(ctx, &panelv1.CreateEggRequest{OrgId: org, Spec: helloSpec("Hello"), Summary: "Says hi", Category: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	id := made.GetId()
	d, err := bob.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: org, Id: id})
	if err != nil {
		t.Fatal(err)
	}
	if e := d.GetEgg(); e.GetName() != "Hello" || e.GetStatus() != "draft" || e.GetOrigin() != "scratch" || e.GetScope() != "org" ||
		e.GetSummary() != "Says hi" || e.GetVersion() != 1 || e.GetCreatedBy() != "alice@example.com" {
		t.Errorf("made: %v", e)
	}
	if d.GetCanEdit() || d.GetSpec().GetStartup()[0].GetCommand() != "./hello {{GREETING}}" || d.GetFormat() != "pterodactyl" || len(d.GetVersions()) != 1 {
		t.Errorf("details for a member: %v", d)
	}
	if _, err := carol.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: other.GetOrg().GetId(), Id: id}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("another org's egg: %v", err)
	}
	// A draft can't be deployed.
	if _, err := bob.lib.GetEggFile(ctx, &panelv1.GetEggFileRequest{OrgId: org, Id: id}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("deploying a draft: %v", err)
	}

	// Editing: each change is a version; an edit made on an old one fails.
	edit := helloSpec("Hello")
	edit.Done = []string{"regex:^Ready in \\d+s"}
	up, err := alice.lib.UpdateEgg(ctx, &panelv1.UpdateEggRequest{OrgId: org, Id: id, Change: &panelv1.UpdateEggRequest_Spec{Spec: edit}, Summary: "Says hi", Category: "generic", Note: "Ready line", BaseVersion: 1})
	if err != nil || up.GetVersion() != 2 {
		t.Fatalf("edit: %v, %v", up, err)
	}
	if _, err := alice.lib.UpdateEgg(ctx, &panelv1.UpdateEggRequest{OrgId: org, Id: id, Change: &panelv1.UpdateEggRequest_Spec{Spec: helloSpec("Hello")}, BaseVersion: 1}); connect.CodeOf(err) != connect.CodeAborted {
		t.Errorf("a stale edit: %v", err)
	}
	same, err := alice.lib.UpdateEgg(ctx, &panelv1.UpdateEggRequest{OrgId: org, Id: id, Change: &panelv1.UpdateEggRequest_Spec{Spec: edit}, Summary: "Says hi", Category: "generic", BaseVersion: 2})
	if err != nil || same.GetVersion() != 2 {
		t.Errorf("saving nothing new: %v, %v", same, err)
	}
	if _, err := alice.lib.UpdateEgg(ctx, &panelv1.UpdateEggRequest{OrgId: org, Id: id, Change: &panelv1.UpdateEggRequest_File{File: []byte("{}")}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a broken file: %v", err)
	}
	if _, err := bob.lib.UpdateEgg(ctx, &panelv1.UpdateEggRequest{OrgId: org, Id: id, Change: &panelv1.UpdateEggRequest_Spec{Spec: edit}}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member editing: %v", err)
	}

	// Published: deployable, at any version.
	if _, err := alice.lib.SetEggStatus(ctx, &panelv1.SetEggStatusRequest{OrgId: org, Id: id, Status: "published"}); err != nil {
		t.Fatal(err)
	}
	f, err := bob.lib.GetEggFile(ctx, &panelv1.GetEggFileRequest{OrgId: org, Id: id})
	if err != nil || f.GetVersion() != 2 {
		t.Fatalf("file: %v, %v", f, err)
	}
	if e, err := eggs.Parse(f.GetEgg()); err != nil || e.Config.Done[0].String() != "regex:^Ready in \\d+s" {
		t.Errorf("deployed egg: %v, %v", e, err)
	}
	if v1, err := bob.lib.GetEggFile(ctx, &panelv1.GetEggFileRequest{OrgId: org, Id: id, Version: 1}); err != nil || v1.GetVersion() != 1 || bytes.Equal(v1.GetEgg(), f.GetEgg()) {
		t.Errorf("version 1: %v", err)
	}

	// Built-in eggs: listed, deployable, never changed, and customizable.
	list, err := bob.lib.ListEggs(ctx, &panelv1.EggServiceListEggsRequest{OrgId: org})
	if err != nil {
		t.Fatal(err)
	}
	var paper *panelv1.CatalogEgg
	for _, e := range list.GetEggs() {
		if e.GetId() == "minecraft/paper" {
			paper = e
		}
	}
	if paper == nil || paper.GetScope() != "builtin" || !paper.GetCertified() || list.GetEggs()[0].GetId() != id {
		t.Fatalf("list: first %v, paper %v", list.GetEggs()[0], paper)
	}
	if _, err := alice.lib.UpdateEgg(ctx, &panelv1.UpdateEggRequest{OrgId: org, Id: "minecraft/paper", Change: &panelv1.UpdateEggRequest_Spec{Spec: edit}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("editing a built-in egg: %v", err)
	}
	custom, err := alice.lib.CustomizeEgg(ctx, &panelv1.CustomizeEggRequest{OrgId: org, Id: "minecraft/paper"})
	if err != nil {
		t.Fatal(err)
	}
	cd, err := alice.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: org, Id: custom.GetId()})
	if err != nil {
		t.Fatal(err)
	}
	if e := cd.GetEgg(); e.GetName() != "Paper (custom)" || e.GetOrigin() != "fork" || e.GetParentName() != "Paper" || e.GetParentId() != "minecraft/paper" ||
		e.GetStatus() != "draft" || e.GetCategory() != "minecraft" || !cd.GetCanEdit() || cd.GetSpec().GetPlayersQuery() != "minecraft" {
		t.Errorf("customized: %v", cd)
	}

	// Deleting: not while a server uses it; archive instead.
	var node string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'egg12345', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at, egg_source) VALUES ($1, 's1', 'hi', 1, now(), now(), $2)`, node, id)
	if _, err := alice.lib.DeleteEgg(ctx, &panelv1.DeleteEggRequest{OrgId: org, Id: id}); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "1 server") {
		t.Errorf("deleting an egg in use: %v", err)
	}
	if d, _ := bob.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: org, Id: id}); d.GetEgg().GetServers() != 1 {
		t.Errorf("servers: %v", d.GetEgg().GetServers())
	}
	if _, err := alice.lib.SetEggStatus(ctx, &panelv1.SetEggStatusRequest{OrgId: org, Id: id, Status: "archived"}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.lib.DeleteEgg(ctx, &panelv1.DeleteEggRequest{OrgId: org, Id: custom.GetId()}); err != nil {
		t.Fatal(err)
	}
	log, err := alice.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, e := range log.GetEvents() {
		actions = append(actions, e.GetAction())
	}
	if got := strings.Join(actions, " "); !strings.HasPrefix(got, "egg.delete egg.archive egg.customize egg.publish egg.update egg.create") {
		t.Errorf("audit: %s", got)
	}

	// The official library: staff admins, with a fresh passkey.
	if _, err := alice.lib.ListEggs(ctx, &panelv1.EggServiceListEggsRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a customer reading the official library: %v", err)
	}
	var out bytes.Buffer
	if err := staff.CLI(ctx, db, &out, []string{"add", "admin@raptor.example", "admin"}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO passkeys (user_id, credential_id, credential, name) VALUES ($1::uuid, 'k', '{}', 'key')`, adminID)
	exec(`UPDATE sessions SET passkey_at = $2 WHERE user_id = $1`, adminID, now.Add(-time.Hour))
	if _, err := admin.lib.CreateEgg(ctx, &panelv1.CreateEggRequest{Spec: helloSpec("Official hello")}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("an hour-old passkey changing the official library: %v", err)
	}
	exec(`UPDATE sessions SET passkey_at = $2 WHERE user_id = $1`, adminID, now)
	off, err := admin.lib.CreateEgg(ctx, &panelv1.CreateEggRequest{Spec: helloSpec("Official hello"), Category: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	// Drafts are staff's own; published, every org sees it.
	if _, err := bob.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: org, Id: off.GetId()}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an official draft seen by an org: %v", err)
	}
	if _, err := admin.lib.SetEggStatus(ctx, &panelv1.SetEggStatusRequest{Id: off.GetId(), Status: "published"}); err != nil {
		t.Fatal(err)
	}
	od, err := bob.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: org, Id: off.GetId()})
	if err != nil || od.GetEgg().GetScope() != "official" || od.GetCanEdit() {
		t.Errorf("an official egg in an org: %v, %v", od, err)
	}
	if _, err := alice.lib.UpdateEgg(ctx, &panelv1.UpdateEggRequest{OrgId: org, Id: off.GetId(), Change: &panelv1.UpdateEggRequest_Spec{Spec: edit}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("an org editing an official egg: %v", err)
	}
	if _, err := alice.lib.CustomizeEgg(ctx, &panelv1.CustomizeEggRequest{OrgId: org, Id: off.GetId()}); err != nil {
		t.Errorf("customizing an official egg: %v", err)
	}
	staffLog, err := admin.staff.ListStaffAudit(ctx, &panelv1.ListStaffAuditRequest{})
	if err != nil || len(staffLog.GetEntries()) < 2 || staffLog.GetEntries()[0].GetAction() != "egg.publish" {
		t.Errorf("staff log: %v, %v", staffLog, err)
	}

	// The community: alice's org shares a published egg; carol's org finds
	// it, adds it (after reviewing it, in the app), and reports it; staff
	// take it out.
	shared, err := alice.lib.CreateEgg(ctx, &panelv1.CreateEggRequest{OrgId: org, Spec: helloSpec("Shared hello"), Category: "generic"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := alice.lib.ShareEgg(ctx, &panelv1.ShareEggRequest{OrgId: org, Id: shared.GetId(), Shared: true}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("sharing a draft: %v", err)
	}
	if _, err := alice.lib.SetEggStatus(ctx, &panelv1.SetEggStatusRequest{OrgId: org, Id: shared.GetId(), Status: "published"}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.lib.ShareEgg(ctx, &panelv1.ShareEggRequest{OrgId: org, Id: shared.GetId(), Shared: true}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member sharing: %v", err)
	}
	if _, err := alice.lib.ShareEgg(ctx, &panelv1.ShareEggRequest{OrgId: org, Id: shared.GetId(), Shared: true}); err != nil {
		t.Fatal(err)
	}
	carolOrg := other.GetOrg().GetId()
	community, err := carol.lib.ListCommunityEggs(ctx, &panelv1.ListCommunityEggsRequest{OrgId: carolOrg, Query: "hello"})
	if err != nil || len(community.GetEggs()) != 1 || community.GetEggs()[0].GetPublisher() != "Acme" || community.GetEggs()[0].GetScope() != "community" {
		t.Fatalf("community: %v, %v", community, err)
	}
	if _, err := carol.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: carolOrg, Id: shared.GetId()}); err != nil {
		t.Errorf("a shared egg's page: %v", err)
	}
	added, err := carol.lib.CustomizeEgg(ctx, &panelv1.CustomizeEggRequest{OrgId: carolOrg, Id: shared.GetId(), AsIs: true})
	if err != nil {
		t.Fatal(err)
	}
	if f, err := carol.lib.GetEggFile(ctx, &panelv1.GetEggFileRequest{OrgId: carolOrg, Id: added.GetId()}); err != nil || len(f.GetEgg()) == 0 {
		t.Errorf("deploying an added egg: %v", err)
	}
	if again, _ := carol.lib.ListCommunityEggs(ctx, &panelv1.ListCommunityEggsRequest{OrgId: carolOrg}); again.GetEggs()[0].GetCopies() != 1 {
		t.Errorf("copies: %v", again.GetEggs()[0])
	}
	if _, err := carol.lib.UpdateEgg(ctx, &panelv1.UpdateEggRequest{OrgId: carolOrg, Id: shared.GetId(), Change: &panelv1.UpdateEggRequest_Spec{Spec: edit}}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("editing another org's shared egg: %v", err)
	}
	if _, err := alice.lib.ReportEgg(ctx, &panelv1.ReportEggRequest{OrgId: org, Id: shared.GetId(), Reason: "spam"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("reporting your own egg: %v", err)
	}
	if _, err := carol.lib.ReportEgg(ctx, &panelv1.ReportEggRequest{OrgId: carolOrg, Id: shared.GetId(), Reason: "malware", Details: "curl | sh"}); err != nil {
		t.Fatal(err)
	}
	if _, err := carol.lib.ReportEgg(ctx, &panelv1.ReportEggRequest{OrgId: carolOrg, Id: shared.GetId(), Reason: "malware"}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("reporting twice: %v", err)
	}
	if _, err := carol.lib.ListEggReports(ctx, &panelv1.ListEggReportsRequest{}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a customer reading reports: %v", err)
	}
	reports, err := admin.lib.ListEggReports(ctx, &panelv1.ListEggReportsRequest{})
	if err != nil || len(reports.GetReports()) != 1 || reports.GetReports()[0].GetReporterEmail() != "carol@example.com" || reports.GetReports()[0].GetPublisher() != "Acme" {
		t.Fatalf("reports: %v, %v", reports, err)
	}
	if _, err := admin.lib.ModerateEgg(ctx, &panelv1.ModerateEggRequest{Id: shared.GetId(), Action: "remove"}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("removing without a reason: %v", err)
	}
	if _, err := admin.lib.ModerateEgg(ctx, &panelv1.ModerateEggRequest{Id: shared.GetId(), Action: "remove", Reason: "Runs a script from an unknown host"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := carol.lib.ListCommunityEggs(ctx, &panelv1.ListCommunityEggsRequest{OrgId: carolOrg}); len(c.GetEggs()) != 0 {
		t.Errorf("a removed egg is still listed: %v", c)
	}
	if _, err := carol.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: carolOrg, Id: shared.GetId()}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("a removed egg's page: %v", err)
	}
	if staffList, _ := admin.lib.ListCommunityEggs(ctx, &panelv1.ListCommunityEggsRequest{}); len(staffList.GetEggs()) != 1 || !staffList.GetEggs()[0].GetRemoved() {
		t.Errorf("staff's community list: %v", staffList)
	}
	mine, err := alice.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: org, Id: shared.GetId()})
	if err != nil || !mine.GetEgg().GetRemoved() || mine.GetEgg().GetRemovedReason() != "Runs a script from an unknown host" {
		t.Errorf("the org's view of its removed egg: %v, %v", mine, err)
	}
	if _, err := alice.lib.ShareEgg(ctx, &panelv1.ShareEggRequest{OrgId: org, Id: shared.GetId(), Shared: false}); err != nil {
		t.Errorf("unsharing a removed egg: %v", err)
	}
	if _, err := alice.lib.ShareEgg(ctx, &panelv1.ShareEggRequest{OrgId: org, Id: shared.GetId(), Shared: true}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("sharing a removed egg again: %v", err)
	}
	if r, _ := admin.lib.ListEggReports(ctx, &panelv1.ListEggReportsRequest{}); len(r.GetReports()) != 0 {
		t.Errorf("reports left open: %v", r)
	}
	// Unpublishing a shared egg takes it out of the community.
	if _, err := admin.lib.ModerateEgg(ctx, &panelv1.ModerateEggRequest{Id: shared.GetId(), Action: "restore"}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.lib.ShareEgg(ctx, &panelv1.ShareEggRequest{OrgId: org, Id: shared.GetId(), Shared: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.lib.SetEggStatus(ctx, &panelv1.SetEggStatusRequest{OrgId: org, Id: shared.GetId(), Status: "draft"}); err != nil {
		t.Fatal(err)
	}
	if c, _ := carol.lib.ListCommunityEggs(ctx, &panelv1.ListCommunityEggsRequest{OrgId: carolOrg}); len(c.GetEggs()) != 0 {
		t.Errorf("an unpublished egg is still shared: %v", c)
	}
}
