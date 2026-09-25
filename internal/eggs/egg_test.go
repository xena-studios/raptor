package eggs

import (
	"os"
	"slices"
	"syscall"
	"testing"
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
