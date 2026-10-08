package nodes

import (
	"context"
	"crypto/ed25519"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// newRegistry is a Registry on a database of the test's own.
func newRegistry(t *testing.T) *Registry {
	t.Helper()
	_, key, _ := ed25519.GenerateKey(nil)
	return &Registry{DB: paneltest.NewDB(t), PanelKey: key}
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
	if len(res.GetShortId()) != 8 || !ed25519.PublicKey(res.GetPanelKey()).Equal(r.PanelKey.Public()) ||
		res.GetHostname() != "n-"+res.GetShortId()+".raptornodes.net" {
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

func TestRelink(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()
	org, _ := r.CreateOrg(ctx, "org")
	token, _ := r.CreateJoinToken(ctx, org, "")
	_, oldKey, _ := ed25519.GenerateKey(nil)
	res, err := r.Enroll(ctx, enrollReq(t, token, oldKey))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.DB.Exec(ctx, "UPDATE nodes SET key_revoked_at = now(), deleted_at = now() WHERE id = $1", res.GetNodeId()); err != nil {
		t.Fatal(err)
	}
	var changed []string
	r.KeyChanged = func(id string) { changed = append(changed, id) }

	// Another org's token can't take the node.
	other, _ := r.CreateOrg(ctx, "other org")
	foreign, _ := r.CreateJoinToken(ctx, other, "")
	_, newKey, _ := ed25519.GenerateKey(nil)
	req := enrollReq(t, foreign, newKey)
	req.NodeId = res.GetNodeId()
	if _, err := r.Enroll(ctx, req); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("another org's token: %v", err)
	}

	// Its own org's token re-links it: same ID and hostname, the new key,
	// no longer revoked or removed.
	fresh, _ := r.CreateJoinToken(ctx, org, "")
	req = enrollReq(t, fresh, newKey)
	req.NodeId = res.GetNodeId()
	again, err := r.Enroll(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if again.GetNodeId() != res.GetNodeId() || again.GetShortId() != res.GetShortId() {
		t.Errorf("relinked as %v, was %v", again, res)
	}
	key, err := r.NodeKey(ctx, res.GetNodeId())
	if err != nil || !key.Equal(newKey.Public()) {
		t.Errorf("key after relink: %v", err)
	}
	if len(changed) != 1 || changed[0] != res.GetNodeId() {
		t.Errorf("connections with the old key weren't dropped: %v", changed)
	}
	// Repeating it (a lost answer) works; the same token for another node doesn't.
	if _, err := r.Enroll(ctx, req); err != nil {
		t.Errorf("repeat: %v", err)
	}
	other2, _ := r.CreateJoinToken(ctx, org, "")
	n2, _ := r.Enroll(ctx, enrollReq(t, other2, oldKey))
	req.NodeId = n2.GetNodeId()
	if _, err := r.Enroll(ctx, req); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("used token on another node: %v", err)
	}
}

// A node back on a new Wings version gets a fresh mirror snapshot, so what
// the new version reports about servers shows up without waiting for each
// server to change; reconnecting on the same version keeps the mirror.
func TestNewVersionResnapshots(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()
	org, _ := r.CreateOrg(ctx, "org")
	token, _ := r.CreateJoinToken(ctx, org, "")
	_, key, _ := ed25519.GenerateKey(nil)
	res, err := r.Enroll(ctx, enrollReq(t, token, key))
	if err != nil {
		t.Fatal(err)
	}
	id := res.GetNodeId()
	acked := func() int64 {
		var n int64
		if err := r.DB.QueryRow(ctx, "SELECT last_acked_seq FROM nodes WHERE id = $1", id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	connect := func(version string) {
		if err := r.Connected(ctx, nodelink.Hello{NodeID: id, Software: version}); err != nil {
			t.Fatal(err)
		}
	}
	connect("v0.1.0")
	if _, err := r.DB.Exec(ctx, "UPDATE nodes SET last_acked_seq = 42 WHERE id = $1", id); err != nil {
		t.Fatal(err)
	}
	connect("v0.1.0")
	if n := acked(); n != 42 {
		t.Errorf("same version: acked %d, want 42", n)
	}
	connect("v0.2.0")
	if n := acked(); n != -1 {
		t.Errorf("new version: acked %d, want -1 (a snapshot)", n)
	}
}

// Removing a node refuses its key, hides it, and drops its mirror and
// grants; relinking with a join token brings it back.
func TestRemove(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()
	org, _ := r.CreateOrg(ctx, "org")
	token, _ := r.CreateJoinToken(ctx, org, "")
	_, key, _ := ed25519.GenerateKey(nil)
	res, err := r.Enroll(ctx, enrollReq(t, token, key))
	if err != nil {
		t.Fatal(err)
	}
	id := res.GetNodeId()
	if _, err := r.DB.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at) VALUES ($1, 's1', 'mc', 1, now(), now())`, id); err != nil {
		t.Fatal(err)
	}
	if ok, err := r.Remove(ctx, id); err != nil || !ok {
		t.Fatalf("remove: %v, %v", ok, err)
	}
	if ok, err := r.Remove(ctx, id); err != nil || ok {
		t.Errorf("removing it again: %v, %v", ok, err)
	}
	if _, err := r.NodeKey(ctx, id); err == nil {
		t.Error("a removed node's key is still accepted")
	}
	var mirrored int
	_ = r.DB.QueryRow(ctx, "SELECT count(*) FROM m_servers WHERE node_id = $1", id).Scan(&mirrored)
	if mirrored != 0 {
		t.Errorf("%d mirrored servers left", mirrored)
	}
}
