package actions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/backdoor"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/store"
)

type scanEggs struct{ scan *eggs.Scan }

func (s scanEggs) Egg(context.Context, string) (*eggs.Egg, error) {
	return &eggs.Egg{Raptor: eggs.Extension{Scan: s.scan}}, nil
}

// files.scan through the executor, as the Panel sends it.
func TestScanCommand(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "server")
	if err := os.MkdirAll(filepath.Join(dir, "resources", "evil"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "resources", "evil", "server.lua"), []byte("local ok = 1\nos.execute('id')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(root, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	svc := files.NewService(files.Options{Servers: fileServers{dir}, Store: db, UID: os.Getuid(), GID: os.Getgid()})
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	send := func(m scanEggs) (json.RawMessage, error) {
		t.Helper()
		x := &command.Executor{DB: db, NodeID: filesNode, PanelKey: pub}
		RegisterScan(x, m, svc)
		id, _ := uuid.NewV7()
		e := command.Envelope{CommandID: id.String(), NodeID: filesNode, UserID: "alice", Action: FilesScan, ServerID: filesServer, ExpiresAt: time.Now().Add(time.Minute).Unix()}
		e.Grant = command.Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ServerID: e.ServerID, ExpiresAt: e.ExpiresAt}
		p, err := e.Grant.Payload()
		if err != nil {
			t.Fatal(err)
		}
		e.Grant.Signature = ed25519.Sign(key, p)
		res, err := x.Execute(ctx, e)
		return res.Value, err
	}

	raw, err := send(scanEggs{&eggs.Scan{Rules: "fivem", Dirs: []string{"resources", "txData"}}})
	if err != nil {
		t.Fatal(err)
	}
	var r backdoor.Report
	if err := json.Unmarshal(raw, &r); err != nil || r.Files != 1 || len(r.Findings) != 1 {
		t.Fatalf("scan = %s, %v", raw, err)
	}
	if f := r.Findings[0]; f.File != "resources/evil/server.lua" || f.Line != 2 || f.Rule != "shell" || f.Severity != backdoor.High {
		t.Errorf("finding %+v", f)
	}
	if _, err := send(scanEggs{}); !errors.Is(err, errNoScan) {
		t.Errorf("a game without a scan: %v", err)
	}
}
