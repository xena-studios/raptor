package actions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/store"
	"github.com/xena-studios/raptor/internal/wings/update"
)

type fakeUpdater struct{ applied []string }

func (f *fakeUpdater) Apply(_ context.Context, version, actor string) (update.Plan, error) {
	f.applied = append(f.applied, version+" by "+actor)
	return update.Plan{Current: "1.4.0", Target: version, Available: true}, nil
}

// node.update as the Panel's rollouts send it: only newer, signed releases,
// and only where the owner allows it.
func TestNodeUpdate(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	run := func(p UpdatePolicy, version string) (*fakeUpdater, error) {
		t.Helper()
		f := &fakeUpdater{}
		x := &command.Executor{DB: db, NodeID: "node-1", PanelKey: pub}
		RegisterUpdates(x, f, p)
		id, _ := uuid.NewV7()
		raw, _ := json.Marshal(UpdateParams{Version: version})
		e := command.Envelope{CommandID: id.String(), NodeID: "node-1", UserID: "panel:rollout", Action: NodeUpdate, Params: raw, ExpiresAt: time.Now().Add(time.Minute).Unix()}
		e.Grant = command.Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ExpiresAt: e.ExpiresAt}
		pl, _ := e.Grant.Payload()
		e.Grant.Signature = ed25519.Sign(key, pl)
		_, err := x.Execute(ctx, e)
		return f, err
	}
	on := UpdatePolicy{Automatic: true, Current: "1.4.0"}
	if f, err := run(on, "1.5.0"); err != nil || len(f.applied) != 1 || f.applied[0] != "1.5.0 by panel" {
		t.Fatalf("newer: %v, %v", f.applied, err)
	}
	for name, tt := range map[string]struct {
		p       UpdatePolicy
		version string
		want    string
	}{
		"older":     {on, "1.3.9", "isn't newer"},
		"same":      {on, "1.4.0", "isn't newer"},
		"garbage":   {on, "latest", "isn't a version"},
		"off":       {UpdatePolicy{Current: "1.4.0"}, "1.5.0", "automatic updates are off"},
		"pinned":    {UpdatePolicy{Automatic: true, Pin: "1.4.0", Current: "1.4.0"}, "1.5.0", "pinned"},
		"dev build": {UpdatePolicy{Automatic: true, Current: "dev"}, "1.5.0", "development build"},
	} {
		f, err := run(tt.p, tt.version)
		if err == nil || !strings.Contains(err.Error(), tt.want) || len(f.applied) != 0 {
			t.Errorf("%s: %v (applied %v)", name, err, f.applied)
		}
	}
}
