package eggs

import "testing"

func TestCheckModpack(t *testing.T) {
	vars := []Variable{{Env: "PROJECT_ID"}, {Env: "VERSION_ID"}, {Env: "MINECRAFT_VERSION"}}
	ok := &Modpack{Source: "modrinth", Project: "PROJECT_ID", Version: "VERSION_ID", GameVersion: "MINECRAFT_VERSION"}
	if err := CheckModpack(ok, vars); err != nil {
		t.Error(err)
	}
	if err := CheckModpack(&Modpack{Source: "modrinth", Project: "PROJECT_ID", Version: "VERSION_ID"}, vars); err != nil {
		t.Errorf("without a game version: %v", err)
	}
	for name, m := range map[string]*Modpack{
		"another source":      {Source: "curseforge", Project: "PROJECT_ID", Version: "VERSION_ID"},
		"an unknown variable": {Source: "modrinth", Project: "PACK", Version: "VERSION_ID"},
		"no version":          {Source: "modrinth", Project: "PROJECT_ID"},
	} {
		if err := CheckModpack(m, vars); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := CheckModpack(nil, vars); err != nil {
		t.Error(err)
	}
}
