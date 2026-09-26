package eggs

import (
	"fmt"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"
)

func load(t *testing.T, name string) *Egg {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	e, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestParsePaperBothFormats(t *testing.T) {
	for _, name := range []string{"paper.ptdl_v2.json", "paper.plcn_v3.yaml"} {
		t.Run(name, func(t *testing.T) {
			e := load(t, name)
			if e.Name != "Paper" {
				t.Errorf("name = %q", e.Name)
			}
			if got := e.DefaultImage(); got != "ghcr.io/pelican-eggs/yolks:java_25" && got != "ghcr.io/pelican-eggs/yolks:java_21" {
				t.Errorf("default image = %q (order not preserved?)", got)
			}
			if e.DefaultStartup() == "" {
				t.Error("no startup command")
			}
			if !e.Config.IsDone("[12:00:00 INFO]: Done (3.2s)! For help, type \"help\"") {
				t.Error("done line not detected")
			}
			if e.Config.Stop.Command != "stop" {
				t.Errorf("stop = %+v", e.Config.Stop)
			}
			if e.Install.Container != "ghcr.io/pelican-eggs/installers:alpine" || e.Install.Entrypoint != "ash" || e.Install.Script == "" {
				t.Errorf("install = %+v", e.Install)
			}
			if len(e.Config.Files) != 1 || e.Config.Files[0].Path != "server.properties" || e.Config.Files[0].Parser != "properties" {
				t.Fatalf("files = %+v", e.Config.Files)
			}
			if keys := findKeys(e.Config.Files[0]); !slices.Equal(keys, []string{"server-ip", "server-port", "query.port"}) {
				t.Errorf("find keys = %v (order not preserved?)", keys)
			}
			var jar *Variable
			for i := range e.Variables {
				if e.Variables[i].Env == "SERVER_JARFILE" {
					jar = &e.Variables[i]
				}
			}
			if jar == nil || jar.Default != "server.jar" || !jar.UserEditable {
				t.Fatalf("SERVER_JARFILE = %+v", jar)
			}
			if !slices.Contains(jar.Rules, "required") {
				t.Errorf("rules = %v", jar.Rules)
			}
			if !slices.Contains(e.Features, "eula") {
				t.Errorf("features = %v", e.Features)
			}
		})
	}
}

func TestParseNodeAndRust(t *testing.T) {
	node := load(t, "nodejs.ptdl_v2.json")
	if node.Config.Stop.Signal != syscall.SIGINT {
		t.Errorf("node stop = %+v, want SIGINT", node.Config.Stop)
	}
	if len(node.Config.Done) != 2 {
		t.Errorf("node done = %v", node.Config.Done)
	}

	rust := load(t, "rust.ptdl_v2.json")
	if rust.DefaultImage() != "ghcr.io/pelican-eggs/games:rust" {
		t.Errorf("rust image = %q", rust.DefaultImage())
	}
	if len(rust.Config.Files) == 0 || rust.Config.Files[0].Parser != "file" {
		t.Errorf("rust files = %+v", rust.Config.Files)
	}
}

func findKeys(f ConfigFile) []string {
	var keys []string
	for _, r := range f.Find {
		keys = append(keys, r.Key)
	}
	return keys
}

func TestParseRejectsUnknownFormat(t *testing.T) {
	if _, err := Parse([]byte(`{"meta":{"version":"PTDL_v9"},"docker_images":{"a":"b"},"startup":"x"}`)); err == nil {
		t.Fatal("expected error")
	}
	if _, err := Parse([]byte(`not: [valid`)); err == nil {
		t.Fatal("expected error")
	}
}

func TestParsePTDLv1(t *testing.T) {
	e, err := Parse([]byte(`{
		"meta": {"version": "PTDL_v1"},
		"name": "Old",
		"image": "quay.io/example:latest",
		"startup": "./run",
		"config": {"files": "{}", "startup": "{\"done\": \"ready\", \"userInteraction\": []}", "logs": "{}", "stop": "^^C"},
		"scripts": {"installation": {"script": "echo hi", "container": "alpine", "entrypoint": "ash"}},
		"variables": [{"name": "X", "env_variable": "X", "default_value": 5, "user_viewable": 1, "user_editable": 0, "rules": "required|integer"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.DefaultImage() != "quay.io/example:latest" {
		t.Errorf("image = %q", e.DefaultImage())
	}
	if e.Config.Stop.Signal != syscall.SIGKILL {
		t.Errorf("^^C should be SIGKILL, got %+v", e.Config.Stop)
	}
	v := e.Variables[0]
	if v.Default != "5" || !v.UserViewable || v.UserEditable {
		t.Errorf("variable = %+v", v)
	}
}

// Pterodactyl's own source-engine eggs (Garry's Mod, TF2, …) are PTDL_v1
// with an "images" list; older exports have "docker_images" as a list.
func TestParseImageLists(t *testing.T) {
	for name, images := range map[string]string{
		"images":        `"images": ["ghcr.io/pterodactyl/games:source", "b"]`,
		"docker_images": `"docker_images": ["ghcr.io/pterodactyl/games:source", "b"]`,
	} {
		e, err := Parse([]byte(`{"meta": {"version": "PTDL_v1"}, ` + images + `, "startup": "./srcds_run"}`))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(e.Images) != 2 || e.DefaultImage() != "ghcr.io/pterodactyl/games:source" {
			t.Errorf("%s: images = %+v", name, e.Images)
		}
	}
}

// A conditional find value becomes one rule per condition, in egg order.
func TestParseConditionalFind(t *testing.T) {
	e, err := Parse([]byte(`{
		"meta": {"version": "PTDL_v2"}, "docker_images": {"a": "a"}, "startup": "x",
		"config": {"files": "{\"config.yml\": {\"parser\": \"yaml\", \"find\": {\"listeners[0].host\": \"0.0.0.0:{{server.build.default.port}}\", \"servers.*.address\": {\"regex:^(127\\\\.0\\\\.0\\\\.1|localhost)(:\\\\d{1,5})?$\": \"{{config.docker.interface}}$2\", \"127.0.0.1\": \"{{config.docker.interface}}\"}, \"online\": true}}}"}
	}`))
	if err != nil {
		t.Fatal(err)
	}
	got := e.Config.Files[0].Find
	want := []FindRule{
		{Key: "listeners[0].host", Value: "0.0.0.0:{{server.build.default.port}}"},
		{Key: "servers.*.address", IfValue: `regex:^(127\.0\.0\.1|localhost)(:\d{1,5})?$`, Value: "{{config.docker.interface}}$2"},
		{Key: "servers.*.address", IfValue: "127.0.0.1", Value: "{{config.docker.interface}}"},
		{Key: "online", Value: true},
	}
	if len(got) != len(want) {
		t.Fatalf("find = %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("rule %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestParseXRaptor(t *testing.T) {
	base := `{"meta": {"version": "PTDL_v2"}, "docker_images": {"a": "a"}, "startup": "x", "x-raptor": %s}`
	e, err := Parse(fmt.Appendf(nil, base, `{"arch": ["x86_64"], "install": {"timeout": "3h"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if e.InstallTimeout() != 3*time.Hour || !e.SupportsArch("amd64") || e.SupportsArch("arm64") {
		t.Errorf("timeout %s, arch %v", e.InstallTimeout(), e.Raptor.Arch)
	}
	for _, bad := range []string{`{"install": {"timeout": "soon"}}`, `{"install": {"timeout": "-1h"}}`, `{"arch": ["sparc"]}`} {
		if _, err := Parse(fmt.Appendf(nil, base, bad)); err == nil {
			t.Errorf("%s: expected an error", bad)
		}
	}
	plain, _ := Parse(fmt.Appendf(nil, base, `{}`))
	if plain.InstallTimeout() != DefaultInstallTimeout || !plain.SupportsArch("arm64") {
		t.Error("defaults wrong")
	}
}

func TestParseRulesKeepsRegexPipes(t *testing.T) {
	e, err := Parse([]byte(`{"meta":{"version":"PTDL_v2"},"docker_images":{"a":"b"},"startup":"x",
		"variables":[{"env_variable":"V","rules":"required|regex:/^(a|b)$/i|max:5"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"required", "regex:/^(a|b)$/i", "max:5"}
	if got := e.Variables[0].Rules; !slices.Equal(got, want) {
		t.Fatalf("rules = %q, want %q", got, want)
	}
}

func TestParseStop(t *testing.T) {
	cases := map[string]Stop{
		"":         {},
		"stop":     {Command: "stop"},
		"^C":       {Signal: syscall.SIGINT},
		"^SIGINT":  {Signal: syscall.SIGINT},
		"^SIGTERM": {Signal: syscall.SIGTERM},
		"^^C":      {Signal: syscall.SIGKILL},
		"^X":       {Signal: syscall.SIGKILL},
	}
	for in, want := range cases {
		if got := ParseStop(in); got != want {
			t.Errorf("ParseStop(%q) = %+v, want %+v", in, got, want)
		}
	}
}

func TestMatcher(t *testing.T) {
	c := Config{Done: []Matcher{NewMatcher("regex:^Server started on port \\d+$"), NewMatcher("Ready")}}
	for line, want := range map[string]bool{
		"Server started on port 3000": true,
		"Bot Ready!":                  true,
		"starting...":                 false,
	} {
		if got := c.IsDone(line); got != want {
			t.Errorf("IsDone(%q) = %v", line, got)
		}
	}
	c = Config{Done: []Matcher{NewMatcher("Done")}, StripANSI: true}
	if !c.IsDone("\x1b[32mDo\x1b[0mne") {
		t.Error("ANSI codes not stripped")
	}
}

func TestEnvironment(t *testing.T) {
	env := Runtime{
		ServerID: "id", Startup: "java -jar {{SERVER_JARFILE}}", MemoryMiB: 2048,
		IP: "0.0.0.0", Port: 25565, Timezone: "UTC", Location: "node1",
		Variables: map[string]string{"server_jarfile": "server.jar", "SERVER_PORT": "1", "TZ": "evil"},
	}.Environment()

	want := []string{
		"TZ=UTC",
		"STARTUP=java -jar {{SERVER_JARFILE}}",
		"SERVER_MEMORY=2048",
		"SERVER_IP=0.0.0.0",
		"SERVER_PORT=25565",
		"P_SERVER_UUID=id",
		"P_SERVER_LOCATION=node1",
		"P_SERVER_ALLOCATION_LIMIT=0",
		"SERVER_JARFILE=server.jar",
	}
	if !slices.Equal(env, want) {
		t.Fatalf("env =\n%q\nwant\n%q", env, want)
	}
}
