package rollout

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// fakeNodes are nodes that update when told (or don't).
type fakeNodes struct {
	t      *testing.T
	db     *pgxpool.Pool
	mu     sync.Mutex
	sent   []string
	refuse map[string]string // node → error answer
	broken map[string]bool   // never comes back on the new version
	down   bool              // the node can't be reached
}

func (f *fakeNodes) Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.down {
		return nil, errors.New("offline")
	}
	var e nodecmd.Envelope
	if err := json.Unmarshal(envelope, &e); err != nil || e.Action != "node.update" || e.UserID != UserID || len(e.Grant.Signature) == 0 {
		f.t.Errorf("bad command: %s", envelope)
	}
	f.sent = append(f.sent, nodeID)
	if msg, ok := f.refuse[nodeID]; ok {
		return &nodev1.ExecuteResponse{Error: msg}, nil
	}
	if !f.broken[nodeID] {
		var p struct{ Version string }
		_ = json.Unmarshal(e.Params, &p)
		if _, err := f.db.Exec(ctx, "UPDATE nodes SET wings_version = $2 WHERE id = $1", nodeID, p.Version); err != nil {
			f.t.Error(err)
		}
	}
	return &nodev1.ExecuteResponse{}, nil
}

func TestRollout(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	var org string
	if err := db.QueryRow(ctx, "INSERT INTO orgs (name) VALUES ('o') RETURNING id::text").Scan(&org); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "INSERT INTO panel_instances (id) VALUES ('i1')"); err != nil {
		t.Fatal(err)
	}
	for i := range 20 {
		pub := make([]byte, 32)
		_, _ = rand.Read(pub)
		var id string
		if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key, wings_version)
			VALUES ($1, $2, $3, $4, '1.0.0') RETURNING id::text`, org, fmt.Sprint("n", i), fmt.Sprintf("node%04d", i), pub).Scan(&id); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(ctx, "INSERT INTO node_connections (node_id, instance_id) VALUES ($1, 'i1')", id); err != nil {
			t.Fatal(err)
		}
	}
	_, key, _ := ed25519.GenerateKey(nil)
	f := &fakeNodes{t: t, db: db, refuse: map[string]string{}, broken: map[string]bool{}}
	now := time.Now()
	e := &Engine{
		DB: db, Sender: f, PanelKey: key, Now: func() time.Time { return now },
		Stages: []int{5, 25, 100}, Soak: []time.Duration{time.Hour, time.Hour, 0},
	}
	tick := func() {
		t.Helper()
		if _, err := db.Exec(ctx, "UPDATE panel_instances SET seen_at = now()"); err != nil {
			t.Fatal(err)
		}
		if err := e.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rollout := func() store.WingsRollout {
		t.Helper()
		r, err := store.New(db).LatestRollout(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}

	r, err := e.Start(ctx, "1.1.0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Start(ctx, "1.2.0"); err == nil {
		t.Fatal("a second rollout started")
	}
	// The rollout's order decides who's in which stage: 1 node at 5%, 5 at
	// 25%. Of the rest, the last is a development build and the one before
	// it is offline; positions 1 and 2 refuse (automatic updates off), and
	// three later ones never come back on the new version.
	cands, err := store.New(db).RolloutCandidates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	order(r.ID, cands)
	at := func(i int) string { return uuid.UUID(cands[i].ID.Bytes).String() }
	if _, err := db.Exec(ctx, "UPDATE nodes SET wings_version = 'dev' WHERE id = $1", at(19)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, "DELETE FROM node_connections WHERE node_id = $1", at(18)); err != nil {
		t.Fatal(err)
	}
	f.refuse[at(1)], f.refuse[at(2)] = "automatic updates are off on this node", "automatic updates are off on this node"
	f.broken[at(7)], f.broken[at(9)], f.broken[at(11)] = true, true, true

	// 5%: one node.
	tick()
	if len(f.sent) != 1 || f.sent[0] != at(0) {
		t.Fatalf("stage 1 sent %v", f.sent)
	}
	// It updated, but the stage soaks for an hour first.
	tick()
	if r := rollout(); r.Stage != 0 {
		t.Fatalf("advanced before the soak: %+v", r)
	}
	now = now.Add(time.Hour + time.Minute)
	tick()
	if r := rollout(); r.Stage != 1 {
		t.Fatalf("didn't advance: %+v", r)
	}

	// 25%: five in all; two refuse and are skipped, not failed.
	tick()
	if len(f.sent) != 5 {
		t.Fatalf("stage 2 sent %v", f.sent)
	}
	now = now.Add(time.Hour + time.Minute)
	tick()
	if r := rollout(); r.Stage != 2 || r.State != "running" {
		t.Fatalf("stage 3 didn't start: %+v", r)
	}

	// Everyone else but the offline node and the dev build; three of them
	// don't come back on the new version, and the rollout halts.
	tick()
	if len(f.sent) != 18 {
		t.Fatalf("stage 3: %d sent in all, want 18 (not the offline node or the dev build)", len(f.sent))
	}
	now = now.Add(ResolveTimeout + time.Minute)
	tick()
	got := rollout()
	if got.State != "halted" || !strings.Contains(got.Reason, "3 of 13") {
		t.Fatalf("didn't halt: %+v", got)
	}
	rows, _ := store.New(db).RolloutNodes(ctx, r.ID)
	counts := map[string]int{}
	for _, row := range rows {
		counts[row.Status]++
	}
	if counts["failed"] != 3 || counts["skipped"] != 2 || counts["updated"] != 13 {
		t.Errorf("outcomes: %v", counts)
	}
}

// Updates that work all the way through finish the rollout.
func TestRolloutDone(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	var org string
	_ = db.QueryRow(ctx, "INSERT INTO orgs (name) VALUES ('o') RETURNING id::text").Scan(&org)
	_, _ = db.Exec(ctx, "INSERT INTO panel_instances (id) VALUES ('i1')")
	for i := range 3 {
		pub := make([]byte, 32)
		_, _ = rand.Read(pub)
		var id string
		_ = db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key, wings_version)
			VALUES ($1, 'n', $2, $3, '1.0.0') RETURNING id::text`, org, fmt.Sprintf("done%04d", i), pub).Scan(&id)
		_, _ = db.Exec(ctx, "INSERT INTO node_connections (node_id, instance_id) VALUES ($1, 'i1')", id)
	}
	_, key, _ := ed25519.GenerateKey(nil)
	f := &fakeNodes{t: t, db: db, refuse: map[string]string{}, broken: map[string]bool{}}
	e := &Engine{DB: db, Sender: f, PanelKey: key, Stages: []int{5, 25, 100}, Soak: []time.Duration{0, 0, 0}}
	if _, err := e.Start(ctx, "1.1.0"); err != nil {
		t.Fatal(err)
	}
	for range 8 {
		if err := e.Tick(ctx); err != nil {
			t.Fatal(err)
		}
	}
	r, _ := store.New(db).LatestRollout(ctx)
	if r.State != "done" || len(f.sent) != 3 {
		t.Fatalf("rollout: %+v, sent %v", r, f.sent)
	}
}
