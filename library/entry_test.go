package library

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
)

// Every egg's conformance test is one a user could set up in the Panel.
func TestTests(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(all, func(e Entry) bool { return e.ID == "generic/raptor-test" && e.Own() && len(e.Icon) > 0 }) {
		t.Fatal("Raptor's test egg isn't in the library")
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

// test is the library's test egg, and upstream the same egg as if it were
// a copy of someone else's.
func test(t *testing.T) (own, upstream Entry) {
	t.Helper()
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(all, func(e Entry) bool { return e.ID == "generic/raptor-test" })
	if i < 0 {
		t.Fatal("no test egg")
	}
	own = all[i]
	upstream = own
	upstream.Source.Repo, upstream.Source.Commit, upstream.Source.Path = "someone/eggs", strings.Repeat("a", 40), "x/"+own.File
	if err := upstream.validate(); err != nil {
		t.Fatalf("an upstream copy: %v", err)
	}
	return own, upstream
}

func TestValidateRejects(t *testing.T) {
	own, upstream := test(t)
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
		for kind, good := range map[string]Entry{"own": own, "upstream": upstream} {
			e := good
			e.Arch = slices.Clone(good.Arch)
			mutate(&e)
			if e.validate() == nil {
				t.Errorf("%s, %s: accepted", kind, name)
			}
		}
	}
	// Raptor's own eggs have no commit to pin, and live where they say.
	e := own
	e.Source.Commit = strings.Repeat("a", 40)
	if e.validate() == nil {
		t.Error("an own egg with a commit: accepted")
	}
	e = own
	e.Source.Path = "elsewhere/" + own.File
	if e.validate() == nil {
		t.Error("an own egg elsewhere: accepted")
	}
	if own.SourceURL() != "https://github.com/xena-studios/raptor/blob/main/library/generic/raptor-test/egg-raptor-test.json" {
		t.Errorf("own source URL %s", own.SourceURL())
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

// What raptor.yaml says about add-ons, the Steam Workshop, and backups is
// in the egg servers are made from.
func TestRaptorSettings(t *testing.T) {
	_, e := test(t)
	e.Players.Query = "minecraft"
	e.Addons = map[string]eggs.Addon{"plugins": {Loaders: []string{"paper"}, Dir: "plugins"}}
	e.Workshop = &eggs.Workshop{AppID: 4000, Variable: "MESSAGE", Collection: true}
	b := eggs.JavaMinecraftBackup
	e.Backup = &b
	e.RCON = &eggs.RCON{Protocol: "source", PwVar: "MESSAGE", Port: "+1", Players: "source"}
	if err := e.validate(); err != nil {
		t.Fatal(err)
	}
	file, err := e.ServerEgg()
	if err != nil {
		t.Fatal(err)
	}
	egg, err := eggs.Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	if a, ok := egg.Raptor.Addons["plugins"]; !ok || a.Dir != "plugins" || !slices.Equal(a.Loaders, []string{"paper"}) {
		t.Errorf("addons %+v", egg.Raptor.Addons)
	}
	if w := egg.Raptor.Workshop; w == nil || *w != *e.Workshop {
		t.Errorf("workshop %+v", w)
	}
	got := egg.Backup()
	if !slices.Equal(got.Pre, b.Pre) || !slices.Equal(got.Post, b.Post) || got.WaitFor != b.WaitFor || got.WaitTimeout() != 5*time.Minute {
		t.Errorf("backup %+v", got)
	}
	if egg.Raptor.Players.Query != "minecraft" {
		t.Errorf("players %+v", egg.Raptor.Players)
	}
	if r := egg.Raptor.RCON; r == nil || *r != *e.RCON {
		t.Errorf("rcon %+v", r)
	}
	// An RCON password variable the egg doesn't have is refused.
	e.RCON = &eggs.RCON{Protocol: "source", PwVar: "NOPE"}
	if e.validate() == nil {
		t.Error("an rcon variable the egg doesn't have: accepted")
	}
	e.RCON = nil
	// A workshop variable the egg doesn't have is refused.
	e.Workshop = &eggs.Workshop{AppID: 4000, Variable: "NOPE"}
	if e.validate() == nil {
		t.Error("a workshop variable the egg doesn't have: accepted")
	}
}

func TestBackupValidate(t *testing.T) {
	good, _ := test(t)
	for name, b := range map[string]eggs.BackupHooks{
		"pause alone": {Pre: []string{"save-off"}, WaitFor: "x"},
		"post alone":  {Post: []string{"save-on"}},
		"no wait":     {Pre: []string{"save-off"}, Post: []string{"save-on"}},
		"bad regex":   {Pre: []string{"save-off"}, Post: []string{"save-on"}, WaitFor: "regex:("},
		"bad time":    {Pre: []string{"save-off"}, Post: []string{"save-on"}, WaitFor: "x", Timeout: "later"},
	} {
		e := good
		e.Backup = &b
		if e.validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A save with nothing to undo (Rust's server.save).
	e := good
	e.Backup = &eggs.BackupHooks{Pre: []string{"server.save"}, WaitFor: "Saved"}
	if err := e.validate(); err != nil {
		t.Errorf("a save alone: %v", err)
	}
}

// The file to import carries the icon, which the Panel reads as the egg's.
func TestImportFile(t *testing.T) {
	own, _ := test(t)
	file, err := own.ImportFile()
	if err != nil {
		t.Fatal(err)
	}
	egg, err := eggs.Parse(file)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(egg.Icon, "data:image/png;base64,") || len(egg.Icon) < len(own.Icon) {
		t.Errorf("icon %.40q", egg.Icon)
	}
	if !slices.Equal(egg.Raptor.Arch, own.Arch) {
		t.Errorf("x-raptor %+v", egg.Raptor)
	}
}
