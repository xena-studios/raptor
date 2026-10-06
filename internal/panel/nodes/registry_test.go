package nodes

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// newRegistry needs a disposable database: PANEL_TEST_DATABASE_URL.
func newRegistry(t *testing.T) *Registry {
	t.Helper()
	url := os.Getenv("PANEL_TEST_DATABASE_URL")
	if url == "" {
		t.Skip("PANEL_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := store.Open(ctx, url)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := store.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, key, _ := ed25519.GenerateKey(nil)
	return &Registry{DB: pool, PanelKey: key}
}

func enrollReq(t *testing.T, token string, key ed25519.PrivateKey) *nodev1.EnrollRequest {
	t.Helper()
	pub := key.Public().(ed25519.PublicKey)
	p, err := nodelink.EnrollPayload(token, pub)
	if err != nil {
		t.Fatal(err)
	}
	return &nodev1.EnrollRequest{
		Token: token, PublicKey: pub, Signature: ed25519.Sign(key, p), Name: "box-1", WingsVersion: "1.2.3",
		Facts: &nodev1.NodeFacts{Os: "Debian GNU/Linux 13", Arch: "amd64", Cpus: 4},
	}
}

func TestEnroll(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()
	org, err := r.CreateOrg(ctx, "test org")
	if err != nil {
		t.Fatal(err)
	}
	token, err := r.CreateJoinToken(ctx, org, "first node")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(token, nodelink.JoinTokenPrefix) {
		t.Fatalf("token %q", token)
	}
	_, key, _ := ed25519.GenerateKey(nil)
	res, err := r.Enroll(ctx, enrollReq(t, token, key))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.GetShortId()) != 8 || !ed25519.PublicKey(res.GetPanelKey()).Equal(r.PanelKey.Public()) {
		t.Errorf("enrolled: %v", res)
	}
	got, err := r.NodeKey(ctx, res.GetNodeId())
	if err != nil || !got.Equal(key.Public()) {
		t.Errorf("node key: %v", err)
	}

	// The same node asking again gets the same answer (its first was lost).
	again, err := r.Enroll(ctx, enrollReq(t, token, key))
	if err != nil || again.GetNodeId() != res.GetNodeId() {
		t.Errorf("repeat: %v, %v", again, err)
	}
	// Anyone else with the used token is refused.
	_, other, _ := ed25519.GenerateKey(nil)
	if _, err := r.Enroll(ctx, enrollReq(t, token, other)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("used token, another key: %v", err)
	}

	// Made-up, expired, and unsigned.
	if _, err := r.Enroll(ctx, enrollReq(t, nodelink.JoinTokenPrefix+"nope", other)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("unknown token: %v", err)
	}
	stale, _ := r.CreateJoinToken(ctx, org, "")
	r.Now = func() time.Time { return time.Now().Add(2 * JoinTokenTTL) }
	if _, err := r.Enroll(ctx, enrollReq(t, stale, other)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("expired token: %v", err)
	}
	r.Now = nil
	fresh, _ := r.CreateJoinToken(ctx, org, "")
	req := enrollReq(t, fresh, other)
	req.Signature[0] ^= 1
	if _, err := r.Enroll(ctx, req); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("bad signature: %v", err)
	}
	// The failed attempt didn't burn the token.
	if _, err := r.Enroll(ctx, enrollReq(t, fresh, other)); err != nil {
		t.Errorf("token after a bad attempt: %v", err)
	}

	// Removed and revoked nodes can't connect.
	if _, err := r.NodeKey(ctx, "0192f0a4-0000-7000-8000-00000000dead"); !errors.Is(err, nodelink.ErrUnknownNode) {
		t.Errorf("unknown node: %v", err)
	}
	if _, err := r.DB.Exec(ctx, "UPDATE nodes SET key_revoked_at = now() WHERE id = $1", res.GetNodeId()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.NodeKey(ctx, res.GetNodeId()); err == nil || !strings.Contains(err.Error(), "revoked") {
		t.Errorf("revoked node: %v", err)
	}
}

func TestShortID(t *testing.T) {
	seen := map[string]bool{}
	for range 1000 {
		s, err := newShortID()
		if err != nil {
			t.Fatal(err)
		}
		if len(s) != 8 || strings.ContainsAny(s, "01ilo") || seen[s] {
			t.Fatalf("short ID %q", s)
		}
		seen[s] = true
	}
}
