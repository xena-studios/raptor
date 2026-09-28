//go:build e2e

package conformance

// Behavioral diff against Pterodactyl (docs/EGGS.md#behavioral-diff): the
// same egg, variables, image, and port run on the real Pterodactyl Panel and
// Wings and then on Raptor, and the results are compared: the container's
// environment and settings, and the values the egg's config file rules
// wrote. Every difference must be one listed in expectedDiffs, with its
// reason.
//
// Needs the Pterodactyl stack from scripts/pterodactyl/setup.sh, which
// writes RAPTOR_PTERODACTYL (default /var/lib/raptor-e2e/pterodactyl.json):
//
//	task e2e:pterodactyl

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// diffCase is one egg run on both.
type diffCase struct {
	name  string
	egg   string // file in testdata/, imported into the Panel by setup.sh
	image string
	vars  map[string]string
	mem   int64
	eula  bool
	// expect lists config file values where Raptor deliberately differs
	// from Pterodactyl, with the reason. Each must actually differ: an
	// expected difference that no longer happens fails the test, so the
	// docs can't claim a difference Pterodactyl has since fixed.
	expect map[string]string
	// done is sent as a console command if the server needs a nudge to
	// print its done string (the test egg's shell); empty otherwise.
}

var diffCases = []diffCase{
	{
		name: "parsers", egg: "parsers.ptdl_v2.json", image: "ghcr.io/pterodactyl/yolks:debian", mem: 256,
		vars: map[string]string{"SERVER_NAME": "diff server", "MOTD": `hello "world" & <you>`, "MAX_PLAYERS": "20", "REGION": "us-east"},
		expect: map[string]string{
			"file.config.yml:clusters.east.nodes.a.port": "more than one * in a path matches every level; Pterodactyl expands only the first",
			"file.config.yml:clusters.east.nodes.b.port": "more than one * in a path matches every level; Pterodactyl expands only the first",
			"file.config.yml:clusters.east.nodes.*.port": "Pterodactyl creates a key literally named * for the second wildcard",
			"file.plain.cfg:line 003":                    "Pterodactyl's text parser leaves {{server.build.memory}} (the Panel's rewrite of {{server.build.env.SERVER_MEMORY}}) unresolved; Raptor resolves it",
		},
	},
	{
		name: "paper", egg: "paper.ptdl_v2.json", image: "ghcr.io/pelican-eggs/yolks:java_21", mem: 2048, eula: true,
		vars: map[string]string{"MINECRAFT_VERSION": "1.21.4", "SERVER_JARFILE": "server.jar", "BUILD_NUMBER": "latest"},
	},
}

// expectedDiffs are the differences Raptor has on purpose, keyed by the
// compared field. Anything else fails the test.
var expectedDiffs = map[string]string{
	"labels":       "Raptor labels its containers with its own keys (raptor.*), not Pterodactyl's Service/ContainerType",
	"security_opt": "Raptor adds a seccomp profile that blocks FS_IOC_FSSETXATTR, the quota escape (decision 93)",
	"user":         "each daemon runs servers as its own system user (pterodactyl / raptor); both are non-root",
	"cgroup":       "Raptor places containers in raptor.slice",
	"network":      "each daemon has its own bridge network",
	"dns":          "Raptor uses the host's resolvers through Docker (the provider's DNS, and it works where outside DNS is blocked); Pterodactyl forces 1.1.1.1 and 1.0.0.1",
	"blkio_weight": "a relative weight, equal for every server either way; Raptor keeps servers at the default and gives installs and backups less",
	"cpu_shares":   "a relative weight, equal for every server either way (Docker's default is 1024)",
}

// The test eggs, also imported into the Panel by setup.sh.
//
//go:embed testdata/*.json
var diffEggs embed.FS

type pteroConfig struct {
	PanelURL string         `json:"panel_url"`
	AppKey   string         `json:"application_key"`
	Client   string         `json:"client_key"`
	Node     int            `json:"node"`
	Eggs     map[string]int `json:"eggs"`
}

func TestPterodactylDiff(t *testing.T) {
	b, err := os.ReadFile(envOr("RAPTOR_PTERODACTYL", "/var/lib/raptor-e2e/pterodactyl.json"))
	if err != nil {
		t.Skipf("no Pterodactyl stack (run scripts/pterodactyl/setup.sh): %v", err)
	}
	var pc pteroConfig
	if err := json.Unmarshal(b, &pc); err != nil {
		t.Fatal(err)
	}
	for _, c := range diffCases {
		t.Run(c.name, func(t *testing.T) {
			eggBytes, err := diffEggs.ReadFile("testdata/" + c.egg)
			if err != nil {
				t.Fatal(err)
			}
			egg, err := eggs.Parse(eggBytes)
			if err != nil {
				t.Fatal(err)
			}
			pt := runPterodactyl(t, pc, c, egg)
			rp := runRaptor(t, c, eggBytes, egg, pt)
			compare(t, pt, rp, egg, c.expect)
		})
	}
}

// capture is what a run produced.
type capture struct {
	id        string // server ID, for normalizing
	gateway   string // the server network's gateway ({{config.docker.interface}})
	env       map[string]string
	container map[string]any // normalized container settings
	files     map[string][]byte
}

// --- Pterodactyl ---------------------------------------------------------------

type ptero struct {
	t  *testing.T
	pc pteroConfig
}

func (p ptero) call(method, path, key string, body any, out any) {
	p.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequestWithContext(context.Background(), method, p.pc.PanelURL+path, rd)
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		p.t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		p.t.Fatalf("%s %s: %s: %s", method, path, resp.Status, b)
	}
	if out != nil {
		if err := json.Unmarshal(b, out); err != nil {
			p.t.Fatalf("%s %s: %v: %s", method, path, err, b)
		}
	}
}

func runPterodactyl(t *testing.T, pc pteroConfig, c diffCase, egg *eggs.Egg) capture {
	p := ptero{t: t, pc: pc}
	eggID, ok := pc.Eggs[c.egg]
	if !ok {
		t.Fatalf("egg %s wasn't imported by setup.sh", c.egg)
	}
	// A free allocation on the node.
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
	if allocID == 0 {
		t.Fatal("no free allocation")
	}
	env := map[string]string{}
	for _, v := range egg.Variables {
		env[v.Env] = v.Default
	}
	for k, v := range c.vars {
		env[k] = v
	}
	var created struct {
		Attributes struct {
			ID         int    `json:"id"`
			UUID       string `json:"uuid"`
			Identifier string `json:"identifier"`
		} `json:"attributes"`
	}
	p.call("POST", "/api/application/servers", pc.AppKey, map[string]any{
		"name": "diff-" + c.name, "user": 1, "egg": eggID, "docker_image": c.image,
		"startup": egg.DefaultStartup(), "environment": env,
		"limits":         map[string]any{"memory": c.mem, "swap": 0, "disk": 0, "io": 500, "cpu": 0},
		"feature_limits": map[string]any{"databases": 0, "backups": 0},
		"allocation":     map[string]any{"default": allocID},
	}, &created)
	s := created.Attributes
	t.Cleanup(func() { p.call("DELETE", fmt.Sprintf("/api/application/servers/%d/force", s.ID), pc.AppKey, nil, nil) })
	dir := filepath.Join("/var/lib/pterodactyl/volumes", s.UUID)

	// Installed: the Panel clears "status" when Wings reports the install.
	waitFor(t, 30*time.Minute, "Pterodactyl install", func() bool {
		var srv struct {
			Attributes struct {
				Status *string `json:"status"`
			} `json:"attributes"`
		}
		p.call("GET", fmt.Sprintf("/api/application/servers/%d", s.ID), pc.AppKey, nil, &srv)
		return srv.Attributes.Status == nil
	})
	if c.eula {
		writeOwned(t, filepath.Join(dir, "eula.txt"), "eula=true\n", dir)
	}
	p.call("POST", "/api/client/servers/"+s.Identifier+"/power", pc.Client, map[string]string{"signal": "start"}, nil)
	waitFor(t, 10*time.Minute, "Pterodactyl running", func() bool {
		var res struct {
			Attributes struct {
				State string `json:"current_state"`
			} `json:"attributes"`
		}
		p.call("GET", "/api/client/servers/"+s.Identifier+"/resources", pc.Client, nil, &res)
		return res.Attributes.State == "running"
	})
	cp := inspect(t, s.UUID, s.UUID, port)
	cp.gateway = networkGateway(t, "pterodactyl_nw")
	cp.files = readConfigFiles(t, dir, egg)
	p.call("POST", "/api/client/servers/"+s.Identifier+"/power", pc.Client, map[string]string{"signal": "kill"}, nil)
	// The kill is asynchronous; Raptor needs the port next.
	waitFor(t, time.Minute, "Pterodactyl's server to stop", func() bool {
		out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", s.UUID).Output()
		return err != nil || strings.TrimSpace(string(out)) == "false"
	})
	return cp
}

// --- Raptor --------------------------------------------------------------------

func runRaptor(t *testing.T, c diffCase, eggBytes []byte, egg *eggs.Egg, pt capture) capture {
	e := newEnv(t)
	// Same timezone and location as Pterodactyl, so the environments compare.
	e.opts.Timezone = pt.env["TZ"]
	e.opts.Location = pt.env["P_SERVER_LOCATION"]
	m := e.manager()
	t.Cleanup(m.Close)
	port, _ := strconv.Atoi(pt.env["SERVER_PORT"])
	id, err := m.Create(context.Background(), server.Config{
		Name: "diff-" + c.name, Egg: eggBytes, Image: c.image, Variables: c.vars,
		Limits: containers.Limits{MemoryMiB: c.mem}, Settings: server.DefaultSettings(),
		Allocations: []server.Allocation{{IP: "0.0.0.0", Port: port, Primary: true}},
	}, server.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, 30*time.Minute, "Raptor install", func() bool {
		st, err := m.Status(id)
		return err == nil && st.State == server.Offline
	})
	dir := filepath.Join(e.volumes, id)
	if c.eula {
		writeOwned(t, filepath.Join(dir, "eula.txt"), "eula=true\n", dir)
	}
	if err := m.Start(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Minute, "Raptor running", func() bool {
		st, err := m.Status(id)
		return err == nil && st.State == server.Running
	})
	cp := inspect(t, "raptor-"+id, id, port)
	cp.gateway = e.opts.DockerInterface
	cp.files = readConfigFiles(t, dir, egg)
	_ = m.Kill(context.Background(), id)
	return cp
}

// --- Capture and compare -----------------------------------------------------------

// inspect reads a container's settings, normalized for comparison.
func inspect(t *testing.T, name, id string, port int) capture {
	t.Helper()
	out, err := exec.Command("docker", "inspect", name).Output()
	if err != nil {
		t.Fatalf("docker inspect %s: %v", name, err)
	}
	var in []struct {
		Config struct {
			Env        []string
			Cmd        []string
			Entrypoint []string
			User       string
			Hostname   string
			WorkingDir string
			Tty        bool
			OpenStdin  bool
			StdinOnce  bool
			Labels     map[string]string
		}
		HostConfig struct {
			Tmpfs             map[string]string
			CapDrop           []string
			CapAdd            []string
			SecurityOpt       []string
			ReadonlyRootfs    bool
			Memory            int64
			MemorySwap        int64
			MemoryReservation int64
			PidsLimit         *int64
			Dns               []string
			LogConfig         struct{ Type string }
			CgroupParent      string
			NetworkMode       string
			Privileged        bool
			Init              *bool
			OomKillDisable    *bool
			CPUQuota          int64 `json:"CpuQuota"`
			CPUShares         int64 `json:"CpuShares"`
			BlkioWeight       uint16
			ExtraHosts        []string
			PortBindings      map[string][]struct{ HostIP, HostPort string }
		}
		Mounts []struct {
			Destination string
			RW          bool
		}
	}
	if err := json.Unmarshal(out, &in); err != nil || len(in) != 1 {
		t.Fatalf("docker inspect %s: %v", name, err)
	}
	c, h := in[0].Config, in[0].HostConfig
	env := map[string]string{}
	for _, kv := range c.Env {
		k, v, _ := strings.Cut(kv, "=")
		env[k] = strings.ReplaceAll(v, id, "<id>")
	}
	var mounts []string
	for _, m := range in[0].Mounts {
		mounts = append(mounts, fmt.Sprintf("%s rw=%v", m.Destination, m.RW))
	}
	sort.Strings(mounts)
	var ports []string
	for p, bs := range h.PortBindings {
		for _, b := range bs {
			ports = append(ports, fmt.Sprintf("%s:%s->%s", b.HostIP, b.HostPort, p))
		}
	}
	sort.Strings(ports)
	caps := slices.Clone(h.CapDrop)
	for i, cp := range caps {
		caps[i] = strings.TrimPrefix(cp, "CAP_")
	}
	sort.Strings(caps)
	pids := int64(0)
	if h.PidsLimit != nil {
		pids = *h.PidsLimit
	}
	return capture{id: id, env: env, container: map[string]any{
		"cmd": c.Cmd, "entrypoint": c.Entrypoint, "user": c.User, "hostname": strings.ReplaceAll(c.Hostname, id, "<id>"),
		"working_dir": c.WorkingDir, "tty": c.Tty, "open_stdin": c.OpenStdin, "stdin_once": c.StdinOnce,
		"labels": c.Labels, "tmpfs": h.Tmpfs, "cap_drop": caps, "cap_add": h.CapAdd, "security_opt": h.SecurityOpt,
		"readonly_rootfs": h.ReadonlyRootfs, "memory": h.Memory, "memory_swap": h.MemorySwap,
		"memory_reservation": h.MemoryReservation, "pids_limit": pids, "dns": h.Dns, "log_driver": h.LogConfig.Type,
		"cgroup": h.CgroupParent, "network": h.NetworkMode, "privileged": h.Privileged,
		"cpu_quota": h.CPUQuota, "cpu_shares": h.CPUShares, "blkio_weight": h.BlkioWeight,
		"extra_hosts": h.ExtraHosts, "mounts": mounts, "ports": ports,
	}}
}

// readConfigFiles reads the files the egg's config rules edit.
func readConfigFiles(t *testing.T, dir string, egg *eggs.Egg) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	for _, f := range egg.Config.Files {
		b, err := os.ReadFile(filepath.Join(dir, f.Path)) //nolint:gosec // test server directory
		if err != nil {
			t.Fatalf("%s: %v", f.Path, err)
		}
		out[f.Path] = b
	}
	return out
}

// compare reports every difference not in expectedDiffs.
func compare(t *testing.T, pt, rp capture, egg *eggs.Egg, caseExpect map[string]string) {
	t.Helper()
	var unexpected, expected []string
	hit := map[string]bool{}
	diff := func(field string, a, b any) {
		if fmt.Sprint(a) == fmt.Sprint(b) {
			return
		}
		line := fmt.Sprintf("%s:\n    pterodactyl: %s\n    raptor:      %s", field, clip(a), clip(b))
		if why, ok := caseExpect[field]; ok {
			hit[field] = true
			expected = append(expected, line+"\n    why: "+why)
			return
		}
		if why, ok := expectedDiffs[field]; ok {
			expected = append(expected, line+"\n    why: "+why)
			return
		}
		unexpected = append(unexpected, line)
	}
	keys := map[string]bool{}
	for k := range pt.env {
		keys[k] = true
	}
	for k := range rp.env {
		keys[k] = true
	}
	for _, k := range sortedKeys(keys) {
		a, aok := pt.env[k]
		b, bok := rp.env[k]
		if !aok {
			a = "(unset)"
		}
		if !bok {
			b = "(unset)"
		}
		diff("env."+k, a, b)
	}
	ck := map[string]bool{}
	for k := range pt.container {
		ck[k] = true
	}
	for _, k := range sortedKeys(ck) {
		diff(k, pt.container[k], rp.container[k])
	}
	for _, f := range egg.Config.Files {
		ptFile := bytes.ReplaceAll(pt.files[f.Path], []byte(pt.gateway), []byte("<docker-interface>"))
		rpFile := bytes.ReplaceAll(rp.files[f.Path], []byte(rp.gateway), []byte("<docker-interface>"))
		before := len(unexpected)
		a, err := configValues(f.Parser, ptFile)
		if err != nil {
			t.Errorf("%s from Pterodactyl doesn't parse: %v", f.Path, err)
			continue
		}
		b, err := configValues(f.Parser, rpFile)
		if err != nil {
			t.Errorf("%s from Raptor doesn't parse: %v", f.Path, err)
			continue
		}
		vk := map[string]bool{}
		for k := range a {
			vk[k] = true
		}
		for k := range b {
			vk[k] = true
		}
		for _, k := range sortedKeys(vk) {
			av, aok := a[k]
			bv, bok := b[k]
			if !aok {
				av = "(missing)"
			}
			if !bok {
				bv = "(missing)"
			}
			diff("file."+f.Path+":"+k, av, bv)
		}
		if len(unexpected) > before {
			t.Logf("%s from Pterodactyl:\n%s\n%s from Raptor:\n%s", f.Path, ptFile, f.Path, rpFile)
		}
	}
	for _, e := range expected {
		t.Logf("expected difference, %s", e)
	}
	for field, why := range caseExpect {
		if !hit[field] {
			t.Errorf("expected difference didn't happen: %s (%s). Pterodactyl may have changed; update the docs and this test", field, why)
		}
	}
	if len(unexpected) > 0 {
		t.Errorf("%d unexpected difference(s) from Pterodactyl:\n  %s", len(unexpected), strings.Join(unexpected, "\n  "))
	}
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func waitFor(t *testing.T, timeout time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %s", what, timeout)
		}
		time.Sleep(2 * time.Second)
	}
}

// writeOwned writes a file owned like the directory it's in.
func writeOwned(t *testing.T, path, content, dir string) {
	t.Helper()
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil { //nolint:gosec // server file
		t.Fatal(err)
	}
	if st, ok := sysStat(fi); ok {
		_ = os.Chown(path, int(st.Uid), int(st.Gid))
	}
}

func networkGateway(t *testing.T, name string) string {
	t.Helper()
	out, err := exec.Command("docker", "network", "inspect", "-f", "{{range .IPAM.Config}}{{.Gateway}} {{end}}", name).Output()
	if err != nil {
		t.Fatalf("docker network inspect %s: %v", name, err)
	}
	for _, gw := range strings.Fields(string(out)) {
		if !strings.Contains(gw, ":") { // IPv4, as {{config.docker.interface}} is
			return gw
		}
	}
	t.Fatalf("network %s has no IPv4 gateway", name)
	return ""
}

// clip shortens a value for the report (a seccomp profile is ~10 KB).
func clip(v any) string {
	s := fmt.Sprint(v)
	if len(s) > 300 {
		return s[:300] + "…"
	}
	return s
}
