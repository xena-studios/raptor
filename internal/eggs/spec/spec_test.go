package spec_test

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	catalog "github.com/xena-studios/raptor/eggs"
	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/eggs/spec"
)

// Reading an egg and writing it back unchanged must not change what it
// does, for every egg in the catalog (Pterodactyl and Pelican, JSON and
// YAML).
func TestRoundTripCatalog(t *testing.T) {
	all, err := catalog.All()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		t.Run(e.ID, func(t *testing.T) {
			file, err := e.ServerEgg()
			if err != nil {
				t.Fatal(err)
			}
			s, err := spec.Read(file)
			if err != nil {
				t.Fatal(err)
			}
			out, err := spec.Write(file, s)
			if err != nil {
				t.Fatal(err)
			}
			before, _ := eggs.Parse(file)
			after, err := eggs.Parse(out)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				diff(t, *before, *after)
			}
			again, err := spec.Write(out, s)
			if err != nil {
				t.Fatal(err)
			}
			if string(again) != string(out) {
				t.Error("a second write changed the file")
			}
		})
	}
}

func TestBlank(t *testing.T) {
	s := &spec.Spec{
		Name:      "Hello",
		Images:    []eggs.Image{{Name: "Debian", Ref: "ghcr.io/pterodactyl/yolks:debian"}},
		Startup:   []eggs.StartupCommand{{Command: "./run.sh {{GREETING}}"}},
		StopType:  "signal",
		StopValue: "SIGINT",
		Done:      []string{"ready", "regex:^Done \\("},
		Install:   eggs.Install{Script: "echo hi", Container: "debian:12", Entrypoint: "bash"},
		Variables: []eggs.Variable{{Name: "Greeting", Env: "GREETING", Default: "hi", UserViewable: true, UserEditable: true, Rules: []string{"required", "string", "max:20"}}},
		Files: []spec.ConfigFile{{Path: "server.properties", Parser: "properties", Find: []spec.Find{
			{Key: "server-port", Value: "{{server.build.default.port}}"},
			{Key: "mode", IfValue: "a", Value: "b"},
		}}},
		Features:     []string{"eula"},
		Arch:         []string{"amd64"},
		PlayersQuery: "minecraft",
		Addons:       map[string]eggs.Addon{"plugins": {Loaders: []string{"paper"}, Dir: "plugins", Version: "GREETING"}},
	}
	out, err := spec.Write(spec.Blank(time.Unix(0, 0)), s)
	if err != nil {
		t.Fatal(err)
	}
	e, err := eggs.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if e.Format != "PTDL_v2" || e.Name != "Hello" || e.DefaultStartup() != "./run.sh {{GREETING}}" ||
		e.Config.Stop.Signal == 0 || len(e.Config.Done) != 2 || e.Install.Script != "echo hi" ||
		len(e.Variables) != 1 || strings.Join(e.Variables[0].Rules, "|") != "required|string|max:20" ||
		len(e.Config.Files) != 1 || len(e.Config.Files[0].Find) != 2 || e.Config.Files[0].Find[1].IfValue != "a" ||
		!e.SupportsArch("amd64") || e.SupportsArch("arm64") || e.Raptor.Players.Query != "minecraft" ||
		e.Raptor.Addons["plugins"].Dir != "plugins" {
		t.Fatalf("unexpected egg: %+v\n%s", e, out)
	}
	back, err := spec.Read(out)
	if err != nil {
		t.Fatal(err)
	}
	if back.StopType != "signal" || back.StopValue != "SIGINT" || back.Done[1] != "regex:^Done \\(" {
		t.Fatalf("read back %+v", back)
	}
}

func TestValidate(t *testing.T) {
	good := func() *spec.Spec {
		return &spec.Spec{
			Name: "x", Images: []eggs.Image{{Ref: "debian"}}, Startup: []eggs.StartupCommand{{Command: "run"}},
			StopType: "command", StopValue: "stop",
		}
	}
	if err := good().Validate(); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*spec.Spec){
		"no name":      func(s *spec.Spec) { s.Name = " " },
		"no image":     func(s *spec.Spec) { s.Images = nil },
		"no startup":   func(s *spec.Spec) { s.Startup = []eggs.StartupCommand{{Command: " "}} },
		"bad signal":   func(s *spec.Spec) { s.StopType, s.StopValue = "signal", "SIGHUP" },
		"caret stop":   func(s *spec.Spec) { s.StopValue = "^C" },
		"bad regex":    func(s *spec.Spec) { s.Done = []string{"regex:("} },
		"bad env":      func(s *spec.Spec) { s.Variables = []eggs.Variable{{Name: "a", Env: "A-B"}} },
		"reserved env": func(s *spec.Spec) { s.Variables = []eggs.Variable{{Name: "a", Env: "SERVER_PORT"}} },
		"dup env": func(s *spec.Spec) {
			s.Variables = []eggs.Variable{{Name: "a", Env: "A"}, {Name: "b", Env: "A"}}
		},
		"escaping file": func(s *spec.Spec) { s.Files = []spec.ConfigFile{{Path: "../x", Parser: "file"}} },
		"absolute file": func(s *spec.Spec) { s.Files = []spec.ConfigFile{{Path: "/etc/x", Parser: "file"}} },
		"bad parser":    func(s *spec.Spec) { s.Files = []spec.ConfigFile{{Path: "x", Parser: "toml"}} },
		"bad arch":      func(s *spec.Spec) { s.Arch = []string{"riscv"} },
		"bad addon":     func(s *spec.Spec) { s.Addons = map[string]eggs.Addon{"shaders": {Loaders: []string{"iris"}, Dir: "x"}} },
		"addon outside": func(s *spec.Spec) {
			s.Addons = map[string]eggs.Addon{"mods": {Loaders: []string{"fabric"}, Dir: "../mods"}}
		},
		"script, no container": func(s *spec.Spec) {
			s.Install = eggs.Install{Script: "echo"}
		},
	} {
		s := good()
		change(s)
		if err := s.Validate(); err == nil || !spec.IsInvalid(err) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

// diff reports which of the egg's fields differ.
func diff(t *testing.T, a, b eggs.Egg) {
	t.Helper()
	norm := func(e *eggs.Egg) []string {
		if e.Features == nil {
			e.Features = []string{}
		}
		if e.FileDenylist == nil {
			e.FileDenylist = []string{}
		}
		var done []string
		for _, d := range e.Config.Done {
			done = append(done, d.String())
		}
		e.Config.Done = nil
		return done
	}
	if da, db := norm(&a), norm(&b); !reflect.DeepEqual(da, db) {
		t.Errorf("done changed: %q, %q", da, db)
	}
	va, vb := reflect.ValueOf(a), reflect.ValueOf(b)
	for i := range va.NumField() {
		if !reflect.DeepEqual(va.Field(i).Interface(), vb.Field(i).Interface()) {
			t.Errorf("%s changed:\n%#v\n%#v", va.Type().Field(i).Name, va.Field(i).Interface(), vb.Field(i).Interface())
		}
	}
}

// A re-imported upstream file keeps the Raptor settings of the egg it
// replaces; a file with its own, or replacing one without any, is kept as
// it is.
func TestKeepRaptor(t *testing.T) {
	upstream, err := os.ReadFile("../testdata/paper.ptdl_v2.json")
	if err != nil {
		t.Fatal(err)
	}
	s, err := spec.Read(upstream)
	if err != nil {
		t.Fatal(err)
	}
	s.Arch = []string{"arm64"}
	s.PlayersQuery = "minecraft"
	prev, err := spec.Write(upstream, s)
	if err != nil {
		t.Fatal(err)
	}
	out, err := spec.KeepRaptor(upstream, prev)
	if err != nil {
		t.Fatal(err)
	}
	e, err := eggs.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(e.Raptor.Arch, []string{"arm64"}) || e.Raptor.Players.Query != "minecraft" {
		t.Errorf("kept: %+v", e.Raptor)
	}
	if same, err := spec.KeepRaptor(prev, upstream); err != nil || string(same) != string(prev) {
		t.Errorf("a file with its own settings changed: %v", err)
	}
	if same, err := spec.KeepRaptor(upstream, upstream); err != nil || string(same) != string(upstream) {
		t.Errorf("nothing to keep changed the file: %v", err)
	}
}
