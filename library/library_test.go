package library

import (
	"bytes"
	"image/png"
	"regexp"
	"slices"
	"strings"
	"testing"

	catalog "github.com/xena-studios/raptor/eggs"
	"github.com/xena-studios/raptor/internal/eggs"
)

// The house style (README.md#how-eggs-read), so the library reads as one.
var (
	badName     = regexp.MustCompile(`(?i)\b(server|dedicated|generic|egg)\b|[.:!?]$|\s{2,}|^\s|\s$`)
	versionName = regexp.MustCompile(`\b\d+(\.\d+)+\b`)
	envName     = regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`)
	panelWords  = regexp.MustCompile(`(?i)pterodactyl|pelican|featherpanel|https?://|\bpanel\b`)
	emoji       = regexp.MustCompile(`[\x{1F300}-\x{1FAFF}\x{2600}-\x{27BF}]`)
	digest      = regexp.MustCompile(`^ghcr\.io/(xena-studios/yolks|pelican-eggs/[a-z0-9-]+)(:[a-z0-9_.-]+)?@sha256:[0-9a-f]{64}$`)
)

const author = "support@raptorpanel.net"

func TestLibrary(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	builtin, err := catalog.All()
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]string{}
	idPattern := regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*/[a-z0-9]+(-[a-z0-9]+)*$`)
	for _, e := range all {
		t.Run(e.ID, func(t *testing.T) {
			// The folder is the egg's ID, which the Panel matches imports
			// by (internal/panel/orgs checkLibraryID): never a built-in's.
			if !idPattern.MatchString(e.ID) || slices.ContainsFunc(builtin, func(b catalog.Entry) bool { return b.ID == e.ID }) {
				t.Errorf("ID %q: lowercase <category>/<egg>, and not a built-in egg's", e.ID)
			}
			if !e.Own() || e.License != "MIT" {
				t.Errorf("an egg of Raptor's own, MIT: source %+v, license %s", e.Source, e.License)
			}
			if len(e.Credits) == 0 {
				t.Error("credits: what it's based on, and where its icon came from")
			}
			checkIcon(t, e)
			egg, err := eggs.Parse(e.Egg)
			if err != nil {
				t.Fatal(err)
			}
			if egg.Format != eggs.Raptor || !strings.HasSuffix(e.File, ".json") {
				t.Errorf("a %s JSON egg (Raptor's own format), not %s %s", eggs.Raptor, egg.Format, e.File)
			}
			name := egg.Name
			if badName.MatchString(strings.TrimPrefix(name, "Minecraft: ")) || versionName.MatchString(name) || len(name) > 40 || name == "" {
				t.Errorf("name %q doesn't follow the style", name)
			}
			if strings.HasPrefix(e.ID, "minecraft/") && !strings.HasPrefix(name, "Minecraft: ") {
				t.Errorf("name %q: Minecraft software is \"Minecraft: <software>\"", name)
			}
			if other, ok := names[name]; ok {
				t.Errorf("name %q is also %s's", name, other)
			}
			names[name] = e.ID
			if egg.Author != author {
				t.Errorf("author %q, want %s", egg.Author, author)
			}
			checkText(t, "description", egg.Description, 40, 400)
			for _, v := range egg.Variables {
				if !envName.MatchString(v.Env) {
					t.Errorf("variable %s: the environment name is UPPER_SNAKE_CASE", v.Env)
				}
				if v.Name == "" || len(v.Name) > 40 || v.Name != strings.TrimSpace(v.Name) || strings.HasSuffix(v.Name, ".") {
					t.Errorf("variable %s: name %q", v.Env, v.Name)
				}
				if first := []rune(v.Name)[0]; first != []rune(strings.ToUpper(string(first)))[0] {
					t.Errorf("variable %s: name %q starts with a capital", v.Env, v.Name)
				}
				checkText(t, "variable "+v.Env, v.Description, 10, 300)
				if len(v.Rules) == 0 {
					t.Errorf("variable %s has no rules", v.Env)
				}
			}
			for _, img := range egg.Images {
				if !digest.MatchString(img.Ref) {
					t.Errorf("image %s: Raptor's or pelican-eggs' images, pinned by digest", img.Ref)
				}
			}
			if len(e.Arch) == 0 || e.Test.Tier == "" {
				t.Error("arch and a conformance test are required")
			}
		})
	}
}

func checkText(t *testing.T, what, s string, minLen, maxLen int) {
	t.Helper()
	switch {
	case len(s) < minLen || len(s) > maxLen:
		t.Errorf("%s: %d characters, want %d to %d: %q", what, len(s), minLen, maxLen, s)
	case !strings.HasSuffix(s, "."):
		t.Errorf("%s ends with a full stop: %q", what, s)
	case panelWords.MatchString(s) || emoji.MatchString(s) || strings.Contains(s, "\r"):
		t.Errorf("%s: no links, panel names, emoji, or \\r: %q", what, s)
	}
}

func checkIcon(t *testing.T, e catalog.Entry) {
	t.Helper()
	if len(e.Icon) == 0 {
		t.Error("no icon.png")
		return
	}
	if len(e.Icon) > 200<<10 {
		t.Errorf("icon.png is %d KB; keep it under 200", len(e.Icon)>>10)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(e.Icon))
	if err != nil || cfg.Width != 256 || cfg.Height != 256 {
		t.Errorf("icon.png: a 256×256 PNG (%dx%d, %v)", cfg.Width, cfg.Height, err)
	}
}

// Every egg exports to a file the Panel imports, icon and settings in it.
func TestExport(t *testing.T) {
	all, err := All()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range all {
		file, err := e.ImportFile()
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		egg, err := eggs.Parse(file)
		if err != nil {
			t.Fatalf("%s: %v", e.ID, err)
		}
		if egg.Icon == "" || !slices.Equal(egg.Raptor.Arch, e.Arch) {
			t.Errorf("%s: exported without its icon or settings", e.ID)
		}
	}
}
