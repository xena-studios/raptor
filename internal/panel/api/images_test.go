package api

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/jackc/pgx/v5"

	panelv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/panel/v1"
	"github.com/xena-studios/raptor/internal/panel/auth"
	"github.com/xena-studios/raptor/internal/panel/images"
	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/orgs"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/staff"
)

func testPNG(t *testing.T, w, h int, shade uint8) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.NRGBA{shade, uint8(x), uint8(y), 255}) //nolint:gosec // test pattern
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// Avatars and org logos: who may set them, how they're served, and how
// staff take one down (docs/PANEL.md#images).
func TestImages(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	mail := &inbox{}
	now := time.Now()
	clock := func() time.Time { return now }
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	authSvc := &auth.Service{DB: db, Mailer: mail, AppURL: appOrigin, Now: clock}
	orgSvc := &orgs.Service{DB: db, Auth: authSvc, Registry: &nodes.Registry{DB: db, PanelKey: key}, Now: clock}
	staffSvc := &staff.Service{DB: db, Auth: authSvc}
	srv := httptest.NewTLSServer(Handler(Config{Auth: authSvc, Orgs: orgSvc, Staff: staffSvc, Images: images.Handler(db), AppOrigin: appOrigin}))
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
	type response struct {
		StatusCode int
		Header     http.Header
		Body       []byte
	}
	get := func(id string, header ...string) response {
		t.Helper()
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+images.Path+id+".png", nil)
		for i := 0; i+1 < len(header); i += 2 {
			req.Header.Set(header[i], header[i+1])
		}
		res, err := srv.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = res.Body.Close() }()
		body, err := io.ReadAll(res.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response{res.StatusCode, res.Header, body}
	}

	alice, aliceID := signUp("alice@example.com")
	bob, bobID := signUp("bob@example.com")
	admin, adminID := signUp("admin@raptor.example")

	// An avatar: normalized, stored, served.
	res, err := alice.auth.SetAvatar(ctx, &panelv1.SetAvatarRequest{Image: testPNG(t, 600, 400, 1)})
	if err != nil {
		t.Fatal(err)
	}
	avatar := res.GetUser().GetAvatar()
	if !images.ValidID(avatar) {
		t.Fatalf("avatar ID %q", avatar)
	}
	if s, _ := alice.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); s.GetUser().GetAvatar() != avatar {
		t.Errorf("the session's avatar: %q", s.GetUser().GetAvatar())
	}
	served := get(avatar)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(served.Body))
	if served.StatusCode != http.StatusOK || err != nil || format != "png" || cfg.Width != 256 || cfg.Height != 256 {
		t.Errorf("served: %d %s %dx%d %v", served.StatusCode, format, cfg.Width, cfg.Height, err)
	}
	for k, want := range map[string]string{
		"Content-Type": "image/png", "X-Content-Type-Options": "nosniff",
		"Content-Security-Policy": "default-src 'none'; sandbox", "Cross-Origin-Resource-Policy": "same-site",
		"Cache-Control": "public, max-age=604800, immutable",
	} {
		if got := served.Header.Get(k); got != want {
			t.Errorf("%s: %q, want %q", k, got, want)
		}
	}
	if r := get(avatar, "If-None-Match", served.Header.Get("ETag")); r.StatusCode != http.StatusNotModified {
		t.Errorf("If-None-Match: %d", r.StatusCode)
	}
	if r := get(avatar, "Origin", "https://evil.example"); r.StatusCode != http.StatusOK {
		t.Errorf("an <img> from anywhere still loads: %d", r.StatusCode)
	}

	// What isn't a picture is refused, and nothing changes.
	for name, data := range map[string][]byte{
		"svg":  []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`),
		"text": []byte("hello"),
	} {
		if _, err := alice.auth.SetAvatar(ctx, &panelv1.SetAvatarRequest{Image: data}); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: %v", name, err)
		}
	}
	if s, _ := alice.auth.GetSession(ctx, &panelv1.GetSessionRequest{}); s.GetUser().GetAvatar() != avatar {
		t.Error("a refused upload changed the avatar")
	}

	// A logo: admins and owners only.
	created, err := alice.orgs.CreateOrg(ctx, &panelv1.CreateOrgRequest{Name: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	org := created.GetOrg().GetId()
	exec(`INSERT INTO org_members (org_id, user_id, role) VALUES ($1::uuid, $2::uuid, 'member')`, org, bobID)
	if _, err := bob.orgs.SetOrgLogo(ctx, &panelv1.SetOrgLogoRequest{OrgId: org, Image: testPNG(t, 64, 64, 2)}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a member setting the logo: %v", err)
	}
	if _, err := admin.orgs.SetOrgLogo(ctx, &panelv1.SetOrgLogoRequest{OrgId: org, Image: testPNG(t, 64, 64, 2)}); connect.CodeOf(err) != connect.CodeNotFound {
		t.Errorf("an outsider setting the logo: %v", err)
	}
	logoRes, err := alice.orgs.SetOrgLogo(ctx, &panelv1.SetOrgLogoRequest{OrgId: org, Image: testPNG(t, 64, 64, 2)})
	if err != nil {
		t.Fatal(err)
	}
	logo := logoRes.GetLogo()
	list, err := bob.orgs.ListOrgs(ctx, &panelv1.ListOrgsRequest{})
	if err != nil || len(list.GetOrgs()) != 1 || list.GetOrgs()[0].GetLogo() != logo {
		t.Errorf("a member sees the logo: %v %v", list, err)
	}
	members, err := bob.orgs.ListMembers(ctx, &panelv1.ListMembersRequest{OrgId: org})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range members.GetMembers() {
		if m.GetUserId() == aliceID && m.GetAvatar() != avatar {
			t.Errorf("alice's avatar in the member list: %q", m.GetAvatar())
		}
	}
	var action string
	if err := db.QueryRow(ctx, `SELECT action FROM audit_log WHERE org_id = $1::uuid ORDER BY at DESC LIMIT 1`, org).Scan(&action); err != nil || action != "org.logo.set" {
		t.Errorf("audit: %q %v", action, err)
	}

	// Members' requests can add pictures but never change one's pixels.
	err = pgx.BeginFunc(ctx, db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE raptor_app"); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `UPDATE images SET png = $2 WHERE id = $1`, logo, []byte("x"))
		return err
	})
	if err == nil {
		t.Error("raptor_app changed a picture's pixels")
	}

	// Staff take a picture down: it's gone everywhere, no longer served,
	// and the same file is refused.
	var out bytes.Buffer
	if err := staff.CLI(ctx, db, &out, []string{"add", "admin@raptor.example", "admin"}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO passkeys (user_id, credential_id, credential, name) VALUES ($1::uuid, 'k', '{}', 'key')`, adminID)
	exec(`UPDATE sessions SET passkey_at = now() WHERE user_id = $1::uuid`, adminID)
	if _, err := alice.staff.TakeDownImage(ctx, &panelv1.TakeDownImageRequest{ImageId: logo, Reason: "abuse"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("a customer taking a picture down: %v", err)
	}
	if _, err := admin.staff.TakeDownImage(ctx, &panelv1.TakeDownImageRequest{ImageId: logo, Reason: "reported as abusive"}); err != nil {
		t.Fatal(err)
	}
	if list, _ := alice.orgs.ListOrgs(ctx, &panelv1.ListOrgsRequest{}); list.GetOrgs()[0].GetLogo() != "" {
		t.Error("the org still has the logo")
	}
	if r := get(logo); r.StatusCode != http.StatusNotFound {
		t.Errorf("a taken-down picture is served: %d", r.StatusCode)
	}
	if _, err := alice.orgs.SetOrgLogo(ctx, &panelv1.SetOrgLogoRequest{OrgId: org, Image: testPNG(t, 64, 64, 2)}); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("uploading a taken-down picture again: %v", err)
	}
	if _, err := admin.staff.TakeDownImage(ctx, &panelv1.TakeDownImageRequest{ImageId: logo, Reason: "again"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Errorf("taking it down twice: %v", err)
	}

	// Removing an avatar, then the daily cleanup: pictures nothing uses go
	// a day after they were last uploaded; ones in use and taken-down ones
	// stay.
	if res, err := alice.auth.SetAvatar(ctx, &panelv1.SetAvatarRequest{}); err != nil || res.GetUser().GetAvatar() != "" {
		t.Fatalf("removing the avatar: %v %v", res, err)
	}
	inUse, err := bob.auth.SetAvatar(ctx, &panelv1.SetAvatarRequest{Image: testPNG(t, 32, 32, 3)})
	if err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE images SET created_at = now() - interval '2 days'`)
	if err := authSvc.Prune(ctx); err != nil {
		t.Fatal(err)
	}
	left := map[string]bool{}
	rows, _ := db.Query(ctx, `SELECT id FROM images`)
	for rows.Next() {
		var id string
		_ = rows.Scan(&id)
		left[id] = true
	}
	if left[avatar] || !left[inUse.GetUser().GetAvatar()] || !left[logo] {
		t.Errorf("after cleanup: %v (unused %v, in use %v, taken down %v)", left, left[avatar], left[inUse.GetUser().GetAvatar()], left[logo])
	}
}
