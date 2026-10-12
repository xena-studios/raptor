package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

const importedEgg = `{
  "meta": {"version": "PTDL_v2"},
  "name": "Imported Game",
  "author": "someone@example.com",
  "description": "From the internet",
  "docker_images": {"Main": "someone/game:latest"},
  "startup": "./game --port {{SERVER_PORT}}",
  "config": {"files": "{}", "startup": "{\"done\": \"Ready\"}", "logs": "{}", "stop": "^C"},
  "scripts": {"installation": {"script": "#!/bin/bash\necho hi", "container": "debian:bookworm-slim", "entrypoint": "bash"}},
  "variables": [{"name": "Max players", "env_variable": "MAX_PLAYERS", "default_value": "10", "user_viewable": true, "user_editable": true, "rules": "required|integer"}]
}`

// Importing an egg: an admin previews it (from a URL or a file), imports
// it, and anyone in the org can then use it.
func TestImportEgg(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}

	// The egg's "internet": https://example.com, answered by a local
	// server (httptest's certificate is for example.com).
	eggSrv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/egg.json" {
			_, _ = w.Write([]byte(importedEgg))
			return
		}
		http.NotFound(w, r)
	}))
	defer eggSrv.Close()
	tr := eggSrv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, eggSrv.Listener.Addr().String())
	}

	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}, EggClient: &http.Client{Transport: tr}}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, AppOrigin: appOrigin}))
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
	alice, _ := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	carol, _ := signUp("carol@example.com")
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	if _, err := db.Exec(ctx, `INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'member')`, org, bobID); err != nil {
		t.Fatal(err)
	}

	// From a GitHub-style link that isn't GitHub: read as given.
	p, err := alice.lib.ReviewEggFile(ctx, &panelv1.ReviewEggFileRequest{OrgId: org, Source: &panelv1.ReviewEggFileRequest_Url{Url: "https://example.com/egg.json"}})
	if err != nil {
		t.Fatal(err)
	}
	r := p.GetReview()
	if r.GetName() != "Imported Game" || r.GetInstallContainer() != "debian:bookworm-slim" || !strings.Contains(r.GetInstallScript(), "echo hi") ||
		r.GetStartup()[0] != "./game --port {{SERVER_PORT}}" || len(r.GetVariables()) != 1 || r.GetDuplicate() || p.GetSourceUrl() != "https://example.com/egg.json" {
		t.Errorf("preview: %v", p)
	}
	if len(r.GetWarnings()) == 0 || r.GetWarnings()[0].GetKind() != "registry" || !strings.Contains(r.GetWarnings()[0].GetText(), "someone/game:latest") {
		t.Errorf("warnings: %v", r.GetWarnings())
	}
	for url, what := range map[string]string{
		"https://127.0.0.1/egg.json":   "a private address",
		"http://example.com/egg.json":  "plain http",
		"https://example.com/missing":  "a 404",
		"https://example.com:8443/egg": "another port",
	} {
		if _, err := alice.lib.ReviewEggFile(ctx, &panelv1.ReviewEggFileRequest{OrgId: org, Source: &panelv1.ReviewEggFileRequest_Url{Url: url}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", what, err)
		}
	}
	file, err := alice.lib.ReviewEggFile(ctx, &panelv1.ReviewEggFileRequest{OrgId: org, Source: &panelv1.ReviewEggFileRequest_File{File: []byte(importedEgg)}})
	if err != nil || file.GetSha256() != p.GetSha256() || file.GetSourceUrl() != "" {
		t.Errorf("preview from a file: %v, %v", file, err)
	}
	if _, err := alice.lib.ReviewEggFile(ctx, &panelv1.ReviewEggFileRequest{OrgId: org, Source: &panelv1.ReviewEggFileRequest_File{File: []byte("hello")}}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("not an egg: %v", err)
	}
	if _, err := bob.lib.ReviewEggFile(ctx, &panelv1.ReviewEggFileRequest{OrgId: org, Source: &panelv1.ReviewEggFileRequest_File{File: []byte(importedEgg)}}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member previewing: %v", err)
	}

	// Importing: admins only, once.
	if _, err := bob.lib.ImportEggFile(ctx, &panelv1.ImportEggFileRequest{OrgId: org, Egg: p.GetEgg()}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member importing: %v", err)
	}
	imp, err := alice.lib.ImportEggFile(ctx, &panelv1.ImportEggFileRequest{OrgId: org, Egg: p.GetEgg(), SourceUrl: p.GetSourceUrl()})
	if err != nil {
		t.Fatal(err)
	}
	id := imp.GetId()
	if _, err := alice.lib.ImportEggFile(ctx, &panelv1.ImportEggFileRequest{OrgId: org, Egg: p.GetEgg()}); connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Errorf("imported twice: %v", err)
	}
	if again, err := alice.lib.ReviewEggFile(ctx, &panelv1.ReviewEggFileRequest{OrgId: org, Source: &panelv1.ReviewEggFileRequest_File{File: []byte(importedEgg)}}); err != nil || !again.GetReview().GetDuplicate() {
		t.Errorf("preview of an imported egg: %v, %v", again, err)
	}
	if _, err := alice.lib.ImportEggFile(ctx, &panelv1.ImportEggFileRequest{OrgId: org, Egg: []byte("{}")}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("importing a non-egg: %v", err)
	}

	// Members see and use it; outsiders can't.
	d, err := bob.lib.GetEggDetails(ctx, &panelv1.GetEggDetailsRequest{OrgId: org, Id: id})
	if e := d.GetEgg(); err != nil || e.GetScope() != "org" || e.GetCategory() != "other" || e.GetStatus() != "published" ||
		e.GetCertified() || e.GetSourceUrl() != "https://example.com/egg.json" || e.GetCreatedBy() != "alice@example.com" ||
		e.GetVariables()[0].GetEnv() != "MAX_PLAYERS" {
		t.Errorf("member's view: %v, %v", d, err)
	}
	got, err := bob.lib.GetEggFile(ctx, &panelv1.GetEggFileRequest{OrgId: org, Id: id})
	if err != nil || !bytes.Equal(got.GetEgg(), []byte(importedEgg)) {
		t.Errorf("member's file: %v", err)
	}
	if _, err := carol.lib.GetEggFile(ctx, &panelv1.GetEggFileRequest{OrgId: org, Id: id}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("outsider's file: %v", err)
	}
	if _, err := bob.lib.DeleteEgg(ctx, &panelv1.DeleteEggRequest{OrgId: org, Id: id}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("member deleting: %v", err)
	}

	log, err := alice.orgs.ListAuditLog(ctx, &panelv1.ListAuditLogRequest{OrgId: org})
	if err != nil || log.GetEvents()[0].GetAction() != "egg.import" || !strings.Contains(log.GetEvents()[0].GetMetadataJson(), "example.com/egg.json") {
		t.Errorf("audit: %v, %v", log, err)
	}

	// Servers made before the library name the egg "org:<id>".
	if got, err := bob.lib.GetEggFile(ctx, &panelv1.GetEggFileRequest{OrgId: org, Id: "org:" + id}); err != nil || !bytes.Equal(got.GetEgg(), []byte(importedEgg)) {
		t.Errorf("file by the old ID: %v", err)
	}

	if _, err := alice.lib.DeleteEgg(ctx, &panelv1.DeleteEggRequest{OrgId: org, Id: id}); err != nil {
		t.Fatal(err)
	}
	if _, err := bob.lib.GetEggFile(ctx, &panelv1.GetEggFileRequest{OrgId: org, Id: id}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("deleted egg: %v", err)
	}
}
