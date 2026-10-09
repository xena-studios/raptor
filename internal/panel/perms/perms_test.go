package perms

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every action Wings registers has a rule here, so none is accidentally
// unreachable or (worse) assumed safe.
func TestEveryWingsActionHasARule(t *testing.T) {
	re := regexp.MustCompile(`=\s*"((?:server|files|backup|schedule|node|keys)\.[a-z_.]+)"`)
	found := 0
	for _, f := range []string{"../../wings/actions/actions.go", "../../wings/actions/update.go", "../../wings/command/keys.go", "../../wings/command/pairing.go"} {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			found++
			if _, ok := For(m[1]); !ok {
				t.Errorf("Wings action %q has no permission rule", m[1])
			}
		}
	}
	if found < 30 {
		t.Fatalf("found only %d Wings actions; did the files move?", found)
	}
}

func TestRules(t *testing.T) {
	if p, ok := For("server.start"); !ok || p != Power {
		t.Errorf("server.start: %q %v", p, ok)
	}
	if !AdminOnly("server.delete") || AdminOnly("server.start") || AdminOnly("nope") {
		t.Error("admin-only rules")
	}
	if _, ok := For("server.everything"); ok {
		t.Error("unknown action allowed")
	}
	for _, p := range All {
		if !Valid(p) || strings.ContainsAny(p, " ,") {
			t.Errorf("permission %q", p)
		}
	}
	// Any grant at all lets a member see a server's graphs; none doesn't,
	// and view can't be granted.
	if p, _ := For("server.metrics"); !Allows([]string{FilesRead}, p) || Allows(nil, p) || Valid(View) {
		t.Error("server.metrics: any access, and only that")
	}
}
