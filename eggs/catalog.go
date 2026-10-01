// Package catalog is Raptor's built-in egg catalog: unmodified copies of
// upstream eggs, one directory each, with a raptor.yaml describing where the
// egg came from, whether it's certified, and how the conformance suite tests
// it (docs/EGGS.md#built-in-catalog).
package catalog

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/xena-studios/raptor/internal/eggs"
)

//go:embed */*/*
var files embed.FS

// Entry is one egg in the catalog.
type Entry struct {
	// ID is the directory under eggs/, e.g. "minecraft/paper".
	ID string
	// File is the egg's file name, as upstream named it.
	File string
	// Egg is the egg exactly as upstream published it.
	Egg []byte
	Meta
}

// Meta is an entry's raptor.yaml.
type Meta struct {
	Source struct {
		Repo   string `yaml:"repo"`   // GitHub owner/name
		Commit string `yaml:"commit"` // the egg is copied from this commit
		Path   string `yaml:"path"`
	} `yaml:"source"`
	License   string `yaml:"license"` // SPDX ID of the upstream repository's license
	Certified bool   `yaml:"certified"`
	// Arch lists the CPU architectures the game itself supports.
	Arch []string `yaml:"arch"`
	// Players says how to ask the game for its player count (the egg's
	// x-raptor.players, which upstream eggs don't have): the Panel adds it
	// to the egg when it creates a server from the catalog.
	Players struct {
		Query string `yaml:"query"` // minecraft or source
		Port  string `yaml:"port"`  // "" = the primary port, "+1", or a variable's name
	} `yaml:"players"`
	Test Test `yaml:"test"`
}

// Tier says when the conformance suite runs an egg.
type Tier string

// Tiers.
const (
	// TierFast eggs run in CI whenever eggs or the code that runs them change.
	TierFast Tier = "fast"
	// TierSlow eggs (big SteamCMD downloads) run weekly and on demand.
	TierSlow Tier = "slow"
	// TierManual eggs can't run on standard CI runners (disk, accounts) and
	// are run by hand.
	TierManual Tier = "manual"
)

// Test is how the conformance suite runs an egg, set the way a user would
// set up the server in the Panel.
type Test struct {
	Tier Tier `yaml:"tier"`
	// Why a manual egg can't run in CI.
	Reason string `yaml:"reason"`
	// Image overrides the egg's default image (the user's image choice,
	// e.g. the Java version current Minecraft needs).
	Image     string            `yaml:"image"`
	MemoryMiB int64             `yaml:"memory_mib"`
	Variables map[string]string `yaml:"variables"`
	// EULA writes eula.txt, as accepting the Minecraft EULA in the Panel does.
	EULA bool `yaml:"eula"`
	// Files are written into the server directory after the install, like a
	// user's uploads (a bot's code).
	Files map[string]string `yaml:"files"`
	// DoneTimeout bounds the time from start to the egg's done string.
	DoneTimeout Duration `yaml:"done_timeout"`
	// Ready, if set, is a regular expression for a console line to wait for
	// before sending Command, for eggs whose done string comes before the
	// server accepts commands.
	Ready string `yaml:"ready"`
	// Command is sent to the running server's console; Expect is a regular
	// expression a console line must match within a minute. Empty for
	// servers without a console (voice servers).
	Command string `yaml:"command"`
	Expect  string `yaml:"expect"`
	// Config maps files to text the egg's config parsers must have written
	// before the start; {{port}} is the server's port.
	Config map[string]string `yaml:"config"`
}

// Duration is a time.Duration written as "5m".
type Duration time.Duration

// UnmarshalYAML implements yaml.Unmarshaler.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// All returns every entry, sorted by ID. Every entry is validated.
func All() ([]Entry, error) {
	metas, err := fs.Glob(files, "*/*/raptor.yaml")
	if err != nil {
		return nil, err
	}
	var out []Entry
	var errs []error
	for _, m := range metas {
		e, err := load(path.Dir(m))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path.Dir(m), err))
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, errors.Join(errs...)
}

func load(dir string) (Entry, error) {
	e := Entry{ID: dir}
	b, err := files.ReadFile(dir + "/raptor.yaml")
	if err != nil {
		return e, err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&e.Meta); err != nil {
		return e, fmt.Errorf("raptor.yaml: %w", err)
	}
	ents, err := files.ReadDir(dir)
	if err != nil {
		return e, err
	}
	for _, f := range ents {
		if f.Name() != "raptor.yaml" && f.Name() != "README.md" {
			if e.File != "" {
				return e, fmt.Errorf("more than one egg file (%s, %s)", e.File, f.Name())
			}
			e.File = f.Name()
		}
	}
	if e.File == "" {
		return e, errors.New("no egg file")
	}
	if e.Egg, err = files.ReadFile(dir + "/" + e.File); err != nil {
		return e, err
	}
	return e, e.validate()
}

func (e *Entry) validate() error {
	if _, err := eggs.Parse(e.Egg); err != nil {
		return err
	}
	s := e.Source
	switch {
	case s.Repo == "" || len(s.Commit) != 40 || s.Path == "":
		return errors.New("source needs repo, a full commit hash, and path")
	case path.Base(s.Path) != e.File:
		return fmt.Errorf("egg file %s doesn't match the source path %s", e.File, s.Path)
	case e.License == "":
		return errors.New("license is required")
	case len(e.Arch) == 0:
		return errors.New("arch is required")
	}
	switch e.Players.Query {
	case "", "minecraft", "source":
	default:
		return fmt.Errorf("players.query %q: want minecraft or source", e.Players.Query)
	}
	for _, a := range e.Arch {
		if a != "amd64" && a != "arm64" {
			return fmt.Errorf("unknown arch %q", a)
		}
	}
	t := e.Test
	switch {
	case !slices.Contains([]Tier{TierFast, TierSlow, TierManual}, t.Tier):
		return fmt.Errorf("test.tier must be fast, slow, or manual, not %q", t.Tier)
	case t.Tier == TierManual && t.Reason == "":
		return errors.New("manual eggs need test.reason")
	case t.MemoryMiB < 64:
		return errors.New("test.memory_mib must be at least 64")
	case t.DoneTimeout <= 0:
		return errors.New("test.done_timeout is required")
	case (t.Command == "") != (t.Expect == ""):
		return errors.New("test.command and test.expect go together")
	case t.Ready != "" && t.Command == "":
		return errors.New("test.ready only applies with test.command")
	}
	for _, re := range []string{t.Ready, t.Expect} {
		if _, err := regexp.Compile(re); err != nil {
			return err
		}
	}
	return nil
}

// Supports reports whether the game runs on a CPU architecture.
func (e *Entry) Supports(arch string) bool { return slices.Contains(e.Arch, arch) }
