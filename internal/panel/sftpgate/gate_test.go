package sftpgate

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"sync"
	"testing"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// fakeNode answers node.sftp like Wings: the port it opened, or none.
type fakeNode struct {
	mu   sync.Mutex
	sent []bool
}

func (f *fakeNode) Execute(_ context.Context, _ string, raw []byte) (*nodev1.ExecuteResponse, error) {
	var env nodecmd.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	var p struct {
		Enabled bool `json:"enabled"`
	}
	_ = json.Unmarshal(env.Params, &p)
	f.mu.Lock()
	f.sent = append(f.sent, p.Enabled)
	f.mu.Unlock()
	port := 0
	if p.Enabled {
		port = 2022
	}
	res, _ := json.Marshal(map[string]any{"enabled": p.Enabled, "port": port, "host_key_fingerprint": "SHA256:hk"})
	return &nodev1.ExecuteResponse{Result: res}, nil
}

func TestGate(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	_, key, _ := ed25519.GenerateKey(nil)
	node := &fakeNode{}
	g := &Gate{DB: db, Sender: node, PanelKey: key}

	var org, nodeID, user string
	if err := db.QueryRow(ctx, "INSERT INTO orgs (name) VALUES ('org') RETURNING id").Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'gate1234', decode(repeat('00', 32), 'hex')) RETURNING id`, org).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, "INSERT INTO users (email) VALUES ('a@example.com') RETURNING id").Scan(&user); err != nil {
		t.Fatal(err)
	}
	state := func() (bool, int32) {
		var on bool
		var port int32
		if err := db.QueryRow(ctx, "SELECT sftp_enabled, sftp_port FROM nodes WHERE id = $1", nodeID).Scan(&on, &port); err != nil {
			t.Fatal(err)
		}
		return on, port
	}
	sent := func() []bool {
		node.mu.Lock()
		defer node.mu.Unlock()
		return append([]bool(nil), node.sent...)
	}

	// Nobody needs it: closed, and nothing is sent.
	if err := g.Sync(ctx, nodeID); err != nil || len(sent()) != 0 {
		t.Fatalf("idle: %v, sent %v", err, sent())
	}
	// A password: the port opens, and the answer is recorded.
	if _, err := db.Exec(ctx, `INSERT INTO sftp_passwords (user_id, node_id, server_id, username, secret_hash, expires_at) VALUES ($1, $2, 's1', 't-aaaaaaaaaa', '\x00', now() + interval '1 hour')`, user, nodeID); err != nil {
		t.Fatal(err)
	}
	if err := g.Sync(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	if on, port := state(); !on || port != 2022 {
		t.Errorf("after a password: %v %d", on, port)
	}
	// In step: ticking sends nothing more.
	g.Tick(ctx)
	if s := sent(); len(s) != 1 || !s[0] {
		t.Errorf("sent %v, want one open", s)
	}
	// It runs out: the next tick closes the port.
	if _, err := db.Exec(ctx, "UPDATE sftp_passwords SET expires_at = now() - interval '1 second'"); err != nil {
		t.Fatal(err)
	}
	g.Tick(ctx)
	if on, _ := state(); on {
		t.Error("the port stayed open after the password ran out")
	}
	// A password on a node where SFTP isn't allowed opens nothing.
	if _, err := db.Exec(ctx, "UPDATE sftp_passwords SET expires_at = now() + interval '1 hour'; UPDATE nodes SET sftp_allowed = false"); err != nil {
		t.Fatal(err)
	}
	g.Tick(ctx)
	if s := sent(); len(s) != 2 || s[1] {
		t.Errorf("sent %v, want open then close only", s)
	}
}
