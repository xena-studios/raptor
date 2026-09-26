package eggs

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzParse(f *testing.F) {
	files, _ := filepath.Glob("testdata/*.json")
	yamls, _ := filepath.Glob("testdata/*.yaml")
	for _, p := range append(files, yamls...) {
		if b, err := os.ReadFile(p); err == nil {
			f.Add(b)
		}
	}
	f.Add([]byte(`{"meta":{"version":"PTDL_v2"},"docker_images":["a"],"startup":"x","config":{"files":"{\"a\":{\"parser\":\"file\",\"find\":{\"k\":{\"v\":\"w\"}}}}"}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		e, err := Parse(data)
		if err != nil {
			return
		}
		_, _ = e.Validate(nil)
		_ = e.Lint()
		_ = e.InstallTimeout()
	})
}

func FuzzValidate(f *testing.F) {
	f.Add("required|string|max:20", "Paper")
	f.Add("nullable|integer|between:1,65535", "25565")
	f.Add(`required|regex:/^([\w\d._-]+)(\.jar)$/`, "server.jar")
	f.Add(`in:"a,b",c`, "a,b")
	f.Add("digits_between:17,18", "123456789012345678")
	f.Fuzz(func(t *testing.T, rules, value string) {
		v := Variable{Env: "X", Rules: parseRules(scalarNode(rules))}
		_ = v.check(value)
	})
}

func FuzzPHPRegex(f *testing.F) {
	for _, s := range []string{`/^a+$/i`, `(abc)`, `{x}`, `#a#u`, `/a/A`, ``} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, p string) {
		if re, err := phpRegex(p); err == nil {
			_ = re.MatchString("test")
		}
	})
}

func FuzzResolve(f *testing.F) {
	f.Add("{{server.build.default.port}} {{env.X}} {{config.docker.interface}} {{ server.environment.X }}")
	f.Fuzz(func(t *testing.T, s string) {
		v := Values{Port: 1, Env: map[string]string{"X": "{{env.X}}"}, DockerInterface: "172.29.0.1"}
		out := v.Resolve(s)
		// Resolution is a single pass: values containing placeholders are
		// never expanded again.
		if s == "{{env.X}}" && out != "{{env.X}}" {
			t.Fatalf("resolved recursively: %q", out)
		}
	})
}
