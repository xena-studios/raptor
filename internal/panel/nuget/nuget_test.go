package nuget

import (
	"encoding/json"
	"testing"
)

func TestAuthors(t *testing.T) {
	for in, want := range map[string]int{`"Alice, Bob"`: 1, `["Alice","Bob"]`: 2} {
		var a Authors
		if err := json.Unmarshal([]byte(in), &a); err != nil || len(a) != want {
			t.Errorf("%s: %v, %v", in, a, err)
		}
	}
}

func TestDependencies(t *testing.T) {
	var v Version
	if err := json.Unmarshal([]byte(`{"version":"1.0.0-beta.1","dependencyGroups":[
		{"dependencies":[{"id":"OpenMod.Unturned"},{"id":"Pustalorc.PlayerInfoLib"},{"id":"System.Text.Json"}]},
		{"dependencies":[{"id":"pustalorc.playerinfolib"},{"id":"Dapper"},{"id":"../x"}]}]}`), &v); err != nil {
		t.Fatal(err)
	}
	deps := v.Dependencies()
	if len(deps) != 2 || deps[0].ID != "Pustalorc.PlayerInfoLib" || deps[1].ID != "Dapper" {
		t.Errorf("dependencies: %v", deps)
	}
	if !v.Prerelease() {
		t.Error("a pre-release")
	}
}

func TestIcon(t *testing.T) {
	if Icon(Package{IconURL: "https://example.com/i.png"}) != "" {
		t.Error("an icon from anywhere")
	}
	if u := DefaultAPI + "/v3-flatcontainer/a/1.0.0/icon"; Icon(Package{IconURL: u}) != u {
		t.Error("NuGet's own icon")
	}
}
