package catalog

import (
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
)

func TestCatalog(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) < 18 {
		t.Fatalf("only %d entries", len(all))
	}
	for _, e := range all {
		egg, err := eggs.Parse(e.Egg)
		if err != nil {
			t.Fatal(err)
		}
		// The image a test picks must be one the egg offers; Wings refuses
		// any other.
		if img := e.Test.Image; img != "" && !slices.ContainsFunc(egg.Images, func(i eggs.Image) bool { return i.Ref == img }) {
			t.Errorf("%s: test image %s isn't one of the egg's images", e.ID, img)
		}
		// Test variables must be the egg's (a typo would silently do nothing)
		// and pass its own rules, as in the Panel.
		for k := range e.Test.Variables {
			if !slices.ContainsFunc(egg.Variables, func(v eggs.Variable) bool { return v.Env == k }) {
				t.Errorf("%s: test variable %s isn't one of the egg's", e.ID, k)
			}
		}
		if _, err := egg.Validate(e.Test.Variables); err != nil && e.Test.Tier != TierManual {
			t.Errorf("%s: variables: %v", e.ID, err)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	good := all[0]
	for name, mutate := range map[string]func(*Entry){
		"short commit":   func(e *Entry) { e.Source.Commit = "abc123" },
		"wrong file":     func(e *Entry) { e.Source.Path = "x/other.json" },
		"no license":     func(e *Entry) { e.License = "" },
		"bad arch":       func(e *Entry) { e.Arch = []string{"x86_64"} },
		"bad tier":       func(e *Entry) { e.Test.Tier = "sometimes" },
		"manual, no why": func(e *Entry) { e.Test.Tier, e.Test.Reason = TierManual, "" },
		"command alone":  func(e *Entry) { e.Test.Command, e.Test.Expect = "list", "" },
		"broken egg":     func(e *Entry) { e.Egg = []byte("{") },
		"bad regexp":     func(e *Entry) { e.Test.Command, e.Test.Expect = "list", "(" },
		"ready alone":    func(e *Entry) { e.Test.Command, e.Test.Expect, e.Test.Ready = "", "", "x" },
	} {
		e := good
		e.Arch = slices.Clone(good.Arch)
		mutate(&e)
		if e.validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Every entry's server egg parses, in its own format, with the catalog's
// architectures and player query, and is otherwise the upstream egg.
func TestServerEgg(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		b, err := e.ServerEgg()
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		got, err := eggs.Parse(b)
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		want, _ := eggs.Parse(e.Egg)
		if !slices.Equal(got.Raptor.Arch, e.Arch) || got.Raptor.Players.Query != e.Players.Query || got.Raptor.Players.Port != e.Players.Port {
			t.Errorf("%s: x-raptor = %+v", e.ID, got.Raptor)
		}
		got.Raptor, want.Raptor = eggs.Extension{}, eggs.Extension{}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: the server egg differs from upstream beyond x-raptor", e.ID)
		}
		if (b[0] == '{') != (e.Egg[0] == '{') {
			t.Errorf("%s: format changed", e.ID)
		}
	}
}

// Minecraft servers get the plugin installer or mod manager their software
// takes, in the egg they're made from.
func TestAddons(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"minecraft/paper": "plugins", "minecraft/velocity": "plugins", "minecraft/fabric": "mods", "minecraft/neoforge": "mods"}
	for _, e := range all {
		kind, ok := want[e.ID]
		if !ok {
			continue
		}
		file, err := e.ServerEgg()
		if err != nil {
			t.Fatal(err)
		}
		egg, err := eggs.Parse(file)
		if err != nil {
			t.Fatal(err)
		}
		if a, ok := egg.Raptor.Addons[kind]; !ok || len(a.Loaders) == 0 || a.Dir != kind {
			t.Errorf("%s: addons %+v", e.ID, egg.Raptor.Addons)
		}
		delete(want, e.ID)
	}
	if len(want) > 0 {
		t.Errorf("missing: %v", want)
	}
}

// Games that download Steam Workshop items themselves get the picker, in
// the egg they're made from.
func TestWorkshop(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]eggs.Workshop{
		"steam/gmod":  {AppID: 4000, Variable: "WORKSHOP_ID", Collection: true},
		"steam/ark":   {AppID: 346110, Variable: "MOD_ID"},
		"steam/squad": {AppID: 393380, Variable: "WORKSHOP_ITEMS", Apply: "reinstall"},
	}
	for _, e := range all {
		file, err := e.ServerEgg()
		if err != nil {
			t.Fatal(err)
		}
		egg, err := eggs.Parse(file)
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		w, ok := want[e.ID]
		switch {
		case !ok && egg.Raptor.Workshop != nil:
			t.Errorf("%s: unexpected workshop %+v", e.ID, egg.Raptor.Workshop)
		case ok && (egg.Raptor.Workshop == nil || *egg.Raptor.Workshop != w):
			t.Errorf("%s: workshop %+v, want %+v", e.ID, egg.Raptor.Workshop, w)
		}
	}
}

// Every Minecraft Java server saves around backups (save-off, save-all
// flush, then save-on), in the egg it's made from; proxies, Bedrock, and
// other games don't.
func TestBackupHooks(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	java := []string{
		"minecraft/fabric", "minecraft/forge", "minecraft/modrinth", "minecraft/neoforge", "minecraft/paper",
		"minecraft/purpur", "minecraft/purpur-geyser", "minecraft/quilt", "minecraft/spongevanilla", "minecraft/vanilla",
	}
	for _, e := range all {
		file, err := e.ServerEgg()
		if err != nil {
			t.Fatal(err)
		}
		egg, err := eggs.Parse(file)
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		got := egg.Raptor.Backup
		if !slices.Contains(java, e.ID) {
			if e.Backup != nil || len(got.Pre) > 0 {
				t.Errorf("%s: unexpected backup hooks %+v", e.ID, got)
			}
			continue
		}
		want := eggs.JavaMinecraftBackup
		if !slices.Equal(got.Pre, want.Pre) || !slices.Equal(got.Post, want.Post) || got.WaitFor != want.WaitFor || got.Timeout != want.Timeout {
			t.Errorf("%s: backup hooks %+v, want %+v", e.ID, got, want)
		}
		// The hooks the egg carries are the ones its servers use.
		if b := egg.Backup(); !slices.Equal(b.Pre, want.Pre) || b.WaitTimeout() != 5*time.Minute {
			t.Errorf("%s: Backup() = %+v", e.ID, b)
		}
	}
}

func TestBackupValidate(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	good := all[0]
	for name, b := range map[string]eggs.BackupHooks{
		"pre alone":  {Pre: []string{"save-off"}, WaitFor: "x"},
		"post alone": {Post: []string{"save-on"}},
		"no wait":    {Pre: []string{"save-off"}, Post: []string{"save-on"}},
		"bad regex":  {Pre: []string{"save-off"}, Post: []string{"save-on"}, WaitFor: "regex:("},
		"bad time":   {Pre: []string{"save-off"}, Post: []string{"save-on"}, WaitFor: "x", Timeout: "later"},
	} {
		e := good
		e.Backup = &b
		if e.validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
