//go:build e2e

package conformance

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
	pteroimport "github.com/xena-studios/raptor/internal/wings/ptero"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// Raptor beside the real Pterodactyl on one box, and `raptor import
// pterodactyl` against it (docs/WINGS.md#pterodactyl-import): a server
// running in Pterodactyl isn't touched by Raptor (or a Raptor restart), and
// then moves to Raptor with its files, settings, and port.
//
//	sudo scripts/pterodactyl/setup.sh internal/wings/conformance/testdata/*.json
//	task e2e:conformance RUN=x  (or run the test binary with -test.run TestPterodactylImport)
func TestPterodactylImport(t *testing.T) {
	ctx := context.Background()
	b, err := os.ReadFile(envOr("RAPTOR_PTERODACTYL", "/var/lib/raptor-e2e/pterodactyl.json"))
	if err != nil {
		t.Skipf("no Pterodactyl stack (run scripts/pterodactyl/setup.sh): %v", err)
	}
	var pc pteroConfig
	if err := json.Unmarshal(b, &pc); err != nil {
		t.Fatal(err)
	}
	p := ptero{t: t, pc: pc}
	eggID, ok := pc.Eggs["parsers.ptdl_v2.json"]
	if !ok {
		t.Fatal("setup.sh didn't import parsers.ptdl_v2.json")
	}

	// A server running in Pterodactyl, with a file Raptor must bring along.
	var allocs struct {
		Data []struct {
			Attributes struct {
				ID       int  `json:"id"`
				Port     int  `json:"port"`
				Assigned bool `json:"assigned"`
			} `json:"attributes"`
		} `json:"data"`
	}
	p.call("GET", fmt.Sprintf("/api/application/nodes/%d/allocations?per_page=100", pc.Node), pc.AppKey, nil, &allocs)
	allocID, port := 0, 0
	for _, a := range allocs.Data {
		if !a.Attributes.Assigned {
			allocID, port = a.Attributes.ID, a.Attributes.Port
			break
		}
	}
	eggBytes, err := diffEggs.ReadFile("testdata/parsers.ptdl_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := eggs.Parse(eggBytes)
	if err != nil {
		t.Fatal(err)
	}
	env := map[string]string{}
	for _, v := range parsed.Variables {
		env[v.Env] = v.Default
	}
	var created struct {
		Attributes struct {
			ID         int    `json:"id"`
			UUID       string `json:"uuid"`
			Identifier string `json:"identifier"`
		} `json:"attributes"`
	}
	p.call("POST", "/api/application/servers", pc.AppKey, map[string]any{
		"name": "import-test", "user": 1, "egg": eggID, "docker_image": parsed.DefaultImage(),
		"startup": parsed.DefaultStartup(), "environment": env,
		"limits":         map[string]any{"memory": 128, "swap": 0, "disk": 512, "io": 500, "cpu": 50},
		"feature_limits": map[string]any{"databases": 0, "backups": 0},
		"allocation":     map[string]any{"default": allocID},
	}, &created)
	ps := created.Attributes
	t.Cleanup(func() { p.call("DELETE", fmt.Sprintf("/api/application/servers/%d/force", ps.ID), pc.AppKey, nil, nil) })
	waitFor(t, 10*time.Minute, "Pterodactyl install", func() bool {
		var srv struct {
			Attributes struct {
				Status *string `json:"status"`
			} `json:"attributes"`
		}
		p.call("GET", fmt.Sprintf("/api/application/servers/%d", ps.ID), pc.AppKey, nil, &srv)
		return srv.Attributes.Status == nil
	})
	pdir := filepath.Join("/var/lib/pterodactyl/volumes", ps.UUID)
	if err := os.MkdirAll(filepath.Join(pdir, "world"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeOwned(t, filepath.Join(pdir, "world", "level.dat"), "the world\n", pdir)
	p.call("POST", "/api/client/servers/"+ps.Identifier+"/power", pc.Client, map[string]string{"signal": "start"}, nil)
	startedAt := func() string {
		out, _ := exec.Command("docker", "inspect", "-f", "{{.State.Running}} {{.State.StartedAt}}", ps.UUID).Output()
		return strings.TrimSpace(string(out))
	}
	waitFor(t, 5*time.Minute, "Pterodactyl running", func() bool { return strings.HasPrefix(startedAt(), "true ") })
	before := startedAt()

	// Raptor beside it: its own server runs, it never lists or touches
	// Pterodactyl's, and a Raptor restart (reconcile) leaves it running.
	e := newEnv(t)
	m := e.manager()
	rid, err := m.Create(ctx, server.Config{
		Name: "beside", Egg: eggBytes, Image: parsed.DefaultImage(), Limits: containers.Limits{MemoryMiB: 128}, Settings: server.DefaultSettings(),
		Allocations: []server.Allocation{{IP: "0.0.0.0", Port: freePort(t), Primary: true}},
	}, server.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 5*time.Minute, "Raptor install", func() bool { st, err := m.Status(rid); return err == nil && st.State == server.Offline })
	if err := m.Start(ctx, rid); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 2*time.Minute, "Raptor running", func() bool { st, err := m.Status(rid); return err == nil && st.State == server.Running })
	m.Close()
	m = e.manager()
	t.Cleanup(m.Close)
	if _, ok := m.List()[ps.UUID]; ok {
		t.Fatal("Raptor lists Pterodactyl's server")
	}
	if after := startedAt(); after != before {
		t.Fatalf("Pterodactyl's server was restarted or stopped by Raptor: %q, then %q", before, after)
	}

	// The import, as raptor import pterodactyl does it.
	wc, err := pteroimport.ReadWingsConfig(pteroimport.DefaultConfig)
	if err != nil {
		t.Fatal(err)
	}
	c := &pteroimport.Client{PanelURL: pc.PanelURL, Key: pc.AppKey, Wings: wc}
	node, err := c.NodeID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	servers, err := c.Servers(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	var s pteroimport.Server
	for _, x := range servers {
		if x.UUID == ps.UUID {
			s = x
		}
	}
	if s.UUID == "" || len(s.Allocations) != 1 || s.Allocations[0].Port != port || s.Limits.Memory != 128 {
		t.Fatalf("server from the Panel: %+v", s)
	}
	eggFile, err := c.Egg(ctx, s.Nest, s.EggID)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(ctx, s.UUID, time.Minute); err != nil {
		t.Fatal(err)
	}
	id, res, err := m.ImportFrom(ctx, s.Config(eggFile), c.Dir(s.UUID), nil)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if res.Files < 2 {
		t.Errorf("copied %+v", res)
	}
	dir := filepath.Join(e.volumes, id)
	got, err := os.ReadFile(filepath.Join(dir, "world", "level.dat"))
	if err != nil || string(got) != "the world\n" {
		t.Fatalf("level.dat in Raptor: %q, %v", got, err)
	}
	if fi, err := os.Stat(filepath.Join(dir, "world", "level.dat")); err != nil || fi.Sys().(*syscall.Stat_t).Uid != uid {
		t.Errorf("owner in Raptor: %v, %v", fi, err)
	}
	if _, err := os.Stat(filepath.Join(pdir, "world", "level.dat")); err != nil {
		t.Errorf("the original was touched: %v", err)
	}
	if err := m.Start(ctx, id); err != nil {
		t.Fatalf("start the imported server: %v", err)
	}
	waitFor(t, 2*time.Minute, "imported server running", func() bool { st, err := m.Status(id); return err == nil && st.State == server.Running })
	srv, err := m.Get(ctx, id)
	if err != nil || srv.Primary().Port != port || srv.Limits.MemoryMiB != 128 || srv.EggSource != fmt.Sprintf("pterodactyl:%d/%d", s.Nest, s.EggID) {
		t.Errorf("imported server: %+v, %v", srv, err)
	}
	if err := c.Suspend(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	var after struct {
		Attributes struct {
			Suspended bool `json:"suspended"`
		} `json:"attributes"`
	}
	p.call("GET", fmt.Sprintf("/api/application/servers/%d", s.ID), pc.AppKey, nil, &after)
	if !after.Attributes.Suspended {
		t.Error("not suspended in Pterodactyl")
	}
}
