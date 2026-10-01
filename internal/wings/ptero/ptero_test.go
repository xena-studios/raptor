package ptero

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
)

// fakePterodactyl is the Panel's application API and Pterodactyl Wings.
type fakePterodactyl struct {
	t      *testing.T
	mu     sync.Mutex
	state  map[string]string // server UUID → Wings state
	stops  int               // a stop takes this many polls
	ignore bool              // ignore stop; only kill works
	calls  []string
	panel  *httptest.Server
	wings  *httptest.Server
}

const (
	appKey     = "ptla_test"
	wingsToken = "wings-token"
	nodeUUID   = "node-uuid-1"
)

func newFake(t *testing.T) *fakePterodactyl {
	f := &fakePterodactyl{t: t, state: map[string]string{"srv-uuid-1": "running", "srv-uuid-2": "offline"}}
	f.panel = httptest.NewServer(http.HandlerFunc(f.servePanel))
	f.wings = httptest.NewServer(http.HandlerFunc(f.serveWings))
	t.Cleanup(f.panel.Close)
	t.Cleanup(f.wings.Close)
	return f
}

func (f *fakePterodactyl) record(s string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, s)
}

func page(w http.ResponseWriter, data []any, cur, total int) {
	items := make([]map[string]any, len(data))
	for i, d := range data {
		items[i] = map[string]any{"attributes": d}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": items, "meta": map[string]any{"pagination": map[string]any{"current_page": cur, "total_pages": total}}})
}

func (f *fakePterodactyl) servePanel(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+appKey {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.record(r.Method + " " + r.URL.Path)
	switch {
	case r.URL.Path == "/api/application/nodes":
		p, _ := strconv.Atoi(r.URL.Query().Get("page"))
		if p == 1 {
			page(w, []any{map[string]any{"id": 7, "uuid": "another-node"}}, 1, 2)
		} else {
			page(w, []any{map[string]any{"id": 9, "uuid": nodeUUID}}, 2, 2)
		}
	case r.URL.Path == "/api/application/servers":
		srv := func(id int, uuid, name string, node int) map[string]any {
			val := "26"
			return map[string]any{
				"id": id, "uuid": uuid, "identifier": uuid[:8], "name": name, "node": node, "allocation": 100 + id,
				"nest": 1, "egg": 5,
				"limits":    map[string]any{"memory": 2048, "swap": -1, "disk": 10240, "cpu": 200, "threads": "0-1"},
				"container": map[string]any{"startup_command": "java -jar server.jar", "image": "ghcr.io/pterodactyl/yolks:java_21"},
				"relationships": map[string]any{
					"allocations": map[string]any{"data": []any{
						map[string]any{"attributes": map[string]any{"id": 100 + id, "ip": "0.0.0.0", "port": 25565 + id}},
						map[string]any{"attributes": map[string]any{"id": 200 + id, "ip": "0.0.0.0", "port": 25600 + id}},
					}},
					"variables": map[string]any{"data": []any{
						map[string]any{"attributes": map[string]any{"env_variable": "VERSION", "server_value": "latest"}},
						map[string]any{"attributes": map[string]any{"env_variable": "BUILD", "server_value": &val}},
						map[string]any{"attributes": map[string]any{"env_variable": "UNSET", "server_value": nil}},
					}},
				},
			}
		}
		page(w, []any{srv(1, "srv-uuid-1", "survival", 9), srv(2, "srv-uuid-2", "creative", 9), srv(3, "srv-uuid-3", "elsewhere", 7)}, 1, 1)
	case r.URL.Path == "/api/application/nests/1/eggs/5":
		// A child egg: its config and install script come from egg 4.
		_ = json.NewEncoder(w).Encode(map[string]any{"attributes": map[string]any{
			"id": 5, "name": "Paper", "author": "parker@pterodactyl.io", "description": "A fork of Spigot",
			"docker_images": map[string]string{"Java 21": "ghcr.io/pterodactyl/yolks:java_21"},
			"startup":       "java -Xms128M -jar {{SERVER_JARFILE}}",
			"config":        map[string]any{"files": nil, "startup": nil, "stop": nil, "logs": nil, "file_denylist": []string{"server.jar"}, "extends": 4},
			"script":        map[string]any{"install": nil, "entry": nil, "container": nil, "extends": 4},
			"relationships": map[string]any{"variables": map[string]any{"data": []any{
				map[string]any{"attributes": map[string]any{"name": "Version", "env_variable": "VERSION", "default_value": "latest", "user_viewable": true, "user_editable": true, "rules": "required|string|max:20"}},
			}}},
		}})
	case r.URL.Path == "/api/application/nests/1/eggs/4":
		_ = json.NewEncoder(w).Encode(map[string]any{"attributes": map[string]any{
			"id": 4, "name": "Vanilla",
			"config": map[string]any{
				"files":   map[string]any{"server.properties": map[string]any{"parser": "properties", "find": map[string]any{"server-port": "{{server.build.default.port}}"}}},
				"startup": map[string]any{"done": ")! For help, type "}, "stop": "stop", "logs": map[string]any{},
			},
			"script": map[string]any{"install": "#!/bin/bash\necho installed", "entry": "bash", "container": "ghcr.io/pterodactyl/installers:alpine"},
		}})
	case strings.HasSuffix(r.URL.Path, "/suspend"):
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakePterodactyl) serveWings(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+wingsToken {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	uuid := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/servers/"), "/")[0]
	f.mu.Lock()
	defer f.mu.Unlock()
	if strings.HasSuffix(r.URL.Path, "/power") {
		var b struct {
			Action string `json:"action"`
		}
		_ = json.NewDecoder(r.Body).Decode(&b)
		f.calls = append(f.calls, "power "+b.Action)
		if b.Action == "kill" || !f.ignore {
			f.state[uuid] = "stopping"
			if b.Action == "kill" {
				f.stops = 0
			}
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if f.state[uuid] == "stopping" {
		if f.stops == 0 {
			f.state[uuid] = "offline"
		} else {
			f.stops--
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"state": f.state[uuid]})
}

func (f *fakePterodactyl) client() *Client {
	u := f.wings.Listener.Addr().(*net.TCPAddr)
	c := &Client{PanelURL: f.panel.URL, Key: appKey}
	c.Wings.UUID, c.Wings.Token = nodeUUID, wingsToken
	c.Wings.API.Host, c.Wings.API.Port = "0.0.0.0", u.Port
	c.Wings.System.Data = "/var/lib/pterodactyl/volumes"
	return c
}

func TestServers(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	c := f.client()
	node, err := c.NodeID(ctx)
	if err != nil || node != 9 {
		t.Fatalf("node: %d, %v", node, err)
	}
	servers, err := c.Servers(ctx, node)
	if err != nil {
		t.Fatal(err)
	}
	if len(servers) != 2 {
		t.Fatalf("%d servers on this node: %+v", len(servers), servers)
	}
	s := servers[0]
	if s.Name != "survival" || s.Image != "ghcr.io/pterodactyl/yolks:java_21" || s.Variables["VERSION"] != "latest" ||
		s.Variables["BUILD"] != "26" || s.Limits.Memory != 2048 || s.Limits.CPU != 200 || len(s.Allocations) != 2 ||
		!s.Allocations[0].Primary || s.Allocations[1].Primary || s.Allocations[0].Port != 25566 {
		t.Errorf("server: %+v", s)
	}
	if _, ok := s.Variables["UNSET"]; ok {
		t.Error("a variable without a value was kept")
	}
	if c.Dir(s.UUID) != "/var/lib/pterodactyl/volumes/srv-uuid-1" {
		t.Errorf("dir: %s", c.Dir(s.UUID))
	}
}

// The egg is rebuilt as a PTDL_v2 file Raptor parses, with what it
// inherits from its parent.
func TestEgg(t *testing.T) {
	f := newFake(t)
	b, err := f.client().Egg(context.Background(), 1, 5)
	if err != nil {
		t.Fatal(err)
	}
	e, err := eggs.Parse(b)
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, b)
	}
	if e.Name != "Paper" || e.Install.Script != "#!/bin/bash\necho installed" || e.Install.Container != "ghcr.io/pterodactyl/installers:alpine" ||
		len(e.Config.Files) != 1 || len(e.Config.Done) != 1 || len(e.Variables) != 1 || e.FileDenylist[0] != "server.jar" {
		t.Errorf("egg: %+v", e)
	}
}

func TestStop(t *testing.T) {
	ctx := context.Background()
	f := newFake(t)
	c := f.client()
	f.stops = 2
	if err := c.Stop(ctx, "srv-uuid-1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(ctx, "srv-uuid-2", time.Minute); err != nil { // already offline
		t.Fatal(err)
	}
	// A server that ignores stop is killed.
	f.state["srv-uuid-1"], f.ignore = "running", true
	if err := c.Stop(ctx, "srv-uuid-1", 1500*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if got := strings.Join(f.calls, ","); !strings.Contains(got, "power stop,power stop,power kill") {
		t.Errorf("calls: %s", got)
	}
}

func TestReadWingsConfig(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.yml")
	_ = os.WriteFile(p, []byte(fmt.Sprintf("uuid: %s\ntoken_id: abc\ntoken: %s\nremote: https://panel.example\napi:\n  port: 8443\n  ssl:\n    enabled: true\n", nodeUUID, wingsToken)), 0o600)
	c, err := ReadWingsConfig(p)
	if err != nil || c.Remote != "https://panel.example" || c.API.Port != 8443 || !c.API.SSL.Enabled || c.System.Data != "/var/lib/pterodactyl/volumes" {
		t.Fatalf("%+v, %v", c, err)
	}
	_ = os.WriteFile(p, []byte("remote: x\n"), 0o600)
	if _, err := ReadWingsConfig(p); err == nil {
		t.Error("a config without a node was accepted")
	}
}
