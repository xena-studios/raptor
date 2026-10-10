package backupkeys

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"testing"

	"github.com/google/uuid"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// node answers backup.key as Wings does, in the mode it's in.
type node struct{ mode string }

func (n *node) Execute(_ context.Context, _ string, raw []byte) (*nodev1.ExecuteResponse, error) {
	var env nodecmd.Envelope
	_ = json.Unmarshal(raw, &env)
	res := map[string]string{"mode": n.mode}
	if n.mode == "panel" {
		res["key"], res["fingerprint"] = "the-node-key", "abc123"
	}
	b, _ := json.Marshal(res)
	if env.Action != "backup.key" || env.Signature != nil {
		return &nodev1.ExecuteResponse{Error: "unexpected"}, nil
	}
	return &nodev1.ExecuteResponse{Result: b}, nil
}

func TestKeeper(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	_, pk, _ := ed25519.GenerateKey(rand.Reader)
	dk := make([]byte, 32)
	_, _ = rand.Read(dk)
	n := &node{mode: "panel"}
	k := &Keeper{DB: db, Sender: n, PanelKey: pk, DataKey: dk}
	org, nid := uuid.New(), uuid.New()

	if mode, err := k.Sync(ctx, org, nid); err != nil || mode != "panel" {
		t.Fatalf("sync: %q, %v", mode, err)
	}
	if st, err := k.Info(ctx, nid); err != nil || st == nil || st.Fingerprint != "abc123" {
		t.Fatalf("info: %+v, %v", st, err)
	}
	// Sealed: the row doesn't hold the key in the clear.
	r, _ := store.New(db).GetBackupKey(ctx, pgID(nid))
	if string(r.Sealed) == "the-node-key" || len(r.Sealed) < 30 {
		t.Fatalf("stored: %q", r.Sealed)
	}
	if key, err := k.Key(ctx, org, nid); err != nil || key != "the-node-key" {
		t.Fatalf("key: %q, %v", key, err)
	}
	// Another org can't have it, and a sealed key moved to another node's
	// row doesn't open.
	if _, err := k.Key(ctx, uuid.New(), nid); err == nil {
		t.Fatal("another org opened it")
	}
	other := uuid.New()
	if err := store.New(db).UpsertBackupKey(ctx, store.UpsertBackupKeyParams{NodeID: pgID(other), OrgID: pgID(org), Sealed: r.Sealed, Fingerprint: "x"}); err != nil {
		t.Fatal(err)
	}
	if _, err := k.Key(ctx, org, other); err == nil {
		t.Fatal("a copied sealed key opened for another node")
	}
	// Without the data key, nothing opens.
	if _, err := (&Keeper{DB: db}).Key(ctx, org, nid); err == nil {
		t.Fatal("opened without the data key")
	}

	// The owner takes it back: the copy goes.
	n.mode = "owner"
	if mode, err := k.Sync(ctx, org, nid); err != nil || mode != "owner" {
		t.Fatalf("sync in owner mode: %q, %v", mode, err)
	}
	if st, _ := k.Info(ctx, nid); st != nil {
		t.Fatal("still kept in owner mode")
	}
	if (&Keeper{}).Enabled() || !k.Enabled() {
		t.Fatal("enabled")
	}
}
