package machines

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"sync"
	"testing"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// fakeNode answers node.system and node.hostname like Wings.
type fakeNode struct {
	mu       sync.Mutex
	hostname string
	refuse   string
	asked    []string
}

func (f *fakeNode) Execute(_ context.Context, _ string, raw []byte) (*nodev1.ExecuteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var env nodecmd.Envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, err
	}
	switch env.Action {
	case "node.system":
		b, _ := json.Marshal(map[string]string{"hostname": f.hostname, "timezone": "Europe/Paris"})
		return &nodev1.ExecuteResponse{Result: b}, nil
	case "node.hostname":
		var p struct{ Hostname string }
		_ = json.Unmarshal(env.Params, &p)
		f.asked = append(f.asked, p.Hostname)
		if f.refuse != "" {
			return &nodev1.ExecuteResponse{Error: f.refuse}, nil
		}
		f.hostname = p.Hostname
		return &nodev1.ExecuteResponse{Result: []byte(`{"note":""}`)}, nil
	}
	return &nodev1.ExecuteResponse{Error: "unknown"}, nil
}

func TestSync(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	var org, node string
	if err := db.QueryRow(ctx, "INSERT INTO orgs (name) VALUES ('acme') RETURNING id").Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, public_key, last_seen_at) VALUES ($1, 'Basement Box', decode(repeat('00', 32), 'hex'), now()) RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO panel_instances (id) VALUES ('test')")
	exec("INSERT INTO node_connections (node_id, instance_id) VALUES ($1, 'test')", node)
	_, key, _ := ed25519.GenerateKey(rand.Reader)
	f := &fakeNode{hostname: "debian"}
	s := &Service{DB: db, Nodes: f, PanelKey: key}
	got := func() (hostname, tz, tried string) {
		t.Helper()
		if err := db.QueryRow(ctx, "SELECT hostname, timezone, hostname_tried FROM nodes WHERE id = $1", node).Scan(&hostname, &tz, &tried); err != nil {
			t.Fatal(err)
		}
		return
	}

	// Connected: read, then renamed to follow the node's name.
	s.Sync(ctx)
	if h, tz, _ := got(); h != "basement-box" || tz != "Europe/Paris" || f.hostname != "basement-box" {
		t.Fatalf("after the first sync: %q %q (machine %q)", h, tz, f.hostname)
	}
	s.Sync(ctx)
	if len(f.asked) != 1 {
		t.Fatalf("asked again: %v", f.asked)
	}

	// Renaming the node renames the machine.
	exec("UPDATE nodes SET name = 'Game Box 2' WHERE id = $1", node)
	s.Sync(ctx)
	if f.hostname != "game-box-2" {
		t.Fatalf("after renaming: %q", f.hostname)
	}

	// An admin's own hostname stays, whatever the node's name.
	exec("UPDATE nodes SET hostname_custom = 'mine.lan', hostname_tried = '' WHERE id = $1", node)
	exec("UPDATE nodes SET name = 'Other' WHERE id = $1", node)
	s.Sync(ctx)
	if f.hostname != "mine.lan" {
		t.Fatalf("custom: %q", f.hostname)
	}

	// A refusal isn't asked again until what it should be changes.
	exec("UPDATE nodes SET hostname_custom = '', hostname_tried = '' WHERE id = $1", node)
	f.refuse = "no hostnamed"
	s.Sync(ctx)
	s.Sync(ctx)
	if _, _, tried := got(); tried != "other" || len(f.asked) != 4 {
		t.Fatalf("refused: tried %q, asked %v", tried, f.asked)
	}

	// Changed on the machine by hand: read on the next connection, and put
	// back.
	f.refuse = ""
	f.hostname = "by-hand"
	exec("UPDATE nodes SET system_read_at = NULL, hostname = 'other', hostname_tried = 'other' WHERE id = $1", node)
	s.Sync(ctx)
	if f.hostname != "other" {
		t.Fatalf("after a change by hand: %q (asked %v)", f.hostname, f.asked)
	}
}
