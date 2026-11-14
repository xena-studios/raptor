// Package catalog is Raptor's built-in egg catalog: Raptor's own eggs and
// unmodified copies of upstream ones, one directory each, with a raptor.yaml
// describing where the egg came from, whether it's certified, and how the
// conformance suite tests it (docs/EGGS.md#built-in-catalog).
package catalog

import (
	"embed"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/shared/ordered"
)

//go:embed */*/*
var files embed.FS

// Entry is one egg in the catalog.
type Entry struct {
	// ID is the directory under eggs/ (or library/), e.g. "minecraft/paper".
	ID string
	// Root is the repository folder the entry is in: "eggs" (the catalog)
	// or "library" (eggs imported by hand, docs/EGGS.md#egg-library).
	Root string
	// File is the egg's file name, as upstream named it.
	File string
	// Egg is the egg exactly as upstream published it.
	Egg []byte
	// Icon is the entry's icon.png, if it has one: shown instead of the
	// icon in the egg (docs/EGGS.md#built-in-catalog).
	Icon []byte
	Meta
}

// Meta is an entry's raptor.yaml.
type Meta struct {
	Source struct {
		Repo   string `yaml:"repo"`   // GitHub owner/name
		Commit string `yaml:"commit"` // the egg is copied from this commit
		Path   string `yaml:"path"`
	} `yaml:"source"`
	License string `yaml:"license"` // SPDX ID of the upstream repository's license
	// Credits are what an egg of Raptor's own is based on, with each one's
	// licence ("https://github.com/pelican-eggs/minecraft (MIT)").
	Credits   []string `yaml:"credits"`
	Certified bool     `yaml:"certified"`
	// Arch lists the CPU architectures the game itself supports.
	Arch []string `yaml:"arch"`
	// Players says how to ask the game for its player count (the egg's
	// x-raptor.players, which upstream eggs don't have): the Panel adds it
	// to the egg when it creates a server from the catalog.
	Players struct {
		Query string `yaml:"query"` // minecraft or source
		Port  string `yaml:"port"`  // "" = the primary port, "+1", or a variable's name
	} `yaml:"players"`
	// Addons are the software add-ons servers get (the egg's
	// x-raptor.addons, docs/EGGS.md#add-ons), added like Players.
	Addons map[string]eggs.Addon `yaml:"addons"`
	// Workshop says where the game takes Steam Workshop items (the egg's
	// x-raptor.workshop, docs/EGGS.md#steam-workshop), added like Players.
	Workshop *eggs.Workshop `yaml:"workshop"`
	// Backup is what's sent to the game's console around a backup (the
	// egg's x-raptor.backup, docs/EGGS.md#backup-hooks), added like
	// Players: save-off and save-all flush, then save-on, for Minecraft.
	Backup *eggs.BackupHooks `yaml:"backup"`
	Test   Test              `yaml:"test"`
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
func All() ([]Entry, error) { return LoadFS(files, "eggs") }

// LoadFS loads entries laid out like the catalog (<category>/<egg>/ with
// a raptor.yaml) from fsys, which is the repository's root folder (eggs
// or library), for Raptor's own eggs' paths. Every entry is validated.
func LoadFS(fsys fs.FS, root string) ([]Entry, error) {
	metas, err := fs.Glob(fsys, "*/*/raptor.yaml")
	if err != nil {
		return nil, err
	}
	var out []Entry
	var errs []error
	for _, m := range metas {
		e, err := load(fsys, root, path.Dir(m))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", path.Dir(m), err))
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, errors.Join(errs...)
}

// OwnRepo is this repository: eggs from it are Raptor's own, versioned
// with the code, not copies of an upstream file at a commit.
const OwnRepo = "xena-studios/raptor"

func (e Entry) root() string {
	if e.Root == "" {
		return "eggs"
	}
	return e.Root
}

// Category is the folder the egg is in, which is its category in a Panel's
// egg library ("minecraft", "steam", …).
func (e Entry) Category() string {
	category, _, _ := strings.Cut(e.ID, "/")
	return category
}

// Own reports whether the egg is Raptor's own.
func (e Entry) Own() bool { return e.Source.Repo == OwnRepo }

// SourceURL is where the egg comes from: upstream's file at the copied
// commit, or, for Raptor's own, the file on main.
func (e Entry) SourceURL() string {
	if e.Own() {
		return "https://github.com/" + OwnRepo + "/blob/main/" + e.Source.Path
	}
	return fmt.Sprintf("https://github.com/%s/blob/%s/%s", e.Source.Repo, e.Source.Commit, e.Source.Path)
}

// iconFile is an entry's own icon, used instead of the one in the egg.
const iconFile = "icon.png"

func load(fsys fs.FS, root, dir string) (Entry, error) {
	e := Entry{ID: dir, Root: root}
	b, err := fs.ReadFile(fsys, dir+"/raptor.yaml")
	if err != nil {
		return e, err
	}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&e.Meta); err != nil {
		return e, fmt.Errorf("raptor.yaml: %w", err)
	}
	ents, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return e, err
	}
	for _, f := range ents {
		if f.Name() == iconFile {
			if e.Icon, err = fs.ReadFile(fsys, dir+"/"+iconFile); err != nil {
				return e, err
			}
			continue
		}
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
	if e.Egg, err = fs.ReadFile(fsys, dir+"/"+e.File); err != nil {
		return e, err
	}
	return e, e.validate()
}

func (e *Entry) validate() error {
	egg, err := eggs.Parse(e.Egg)
	if err != nil {
		return err
	}
	s := e.Source
	if e.Own() && (s.Commit != "" || s.Path != e.root()+"/"+e.ID+"/"+e.File) {
		return fmt.Errorf("an egg of Raptor's own has no commit, and its path is %s/%s/%s", e.root(), e.ID, e.File)
	}
	switch {
	case !e.Own() && (s.Repo == "" || len(s.Commit) != 40 || s.Path == ""):
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
	if err := eggs.CheckAddons(e.Addons); err != nil {
		return err
	}
	if err := eggs.CheckWorkshop(e.Workshop, egg.Variables); err != nil {
		return err
	}
	if b := e.Backup; b != nil {
		if err := eggs.CheckBackup(*b); err != nil {
			return err
		}
		switch {
		case len(b.Pre) == 0 || len(b.Post) == 0:
			// Pre without post leaves the game not saving; post without
			// pre undoes nothing.
			return errors.New("backup needs both pre and post commands")
		case b.WaitFor == "":
			return errors.New("backup.wait_for is required: the line that says the save is done")
		}
		if p, ok := strings.CutPrefix(b.WaitFor, "regex:"); ok {
			if _, err := regexp.Compile(p); err != nil {
				return fmt.Errorf("backup.wait_for: %w", err)
			}
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

// ServerEgg is the egg a server created from this entry gets: the upstream
// egg with the catalog's x-raptor block added (the architectures, and how to
// ask for the player count), which upstream eggs don't have. Key order and
// the file's format (JSON or YAML) are kept.
func (e *Entry) ServerEgg() ([]byte, error) {
	root, err := ordered.Decode(e.Egg)
	if err != nil {
		return nil, err
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("egg isn't an object")
	}
	ext := mapping(root, "x-raptor")
	arch := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
	for _, a := range e.Arch {
		arch.Content = append(arch.Content, str(a))
	}
	set(ext, "arch", arch)
	set(ext, "category", str(e.Category()))
	if e.Players.Query != "" {
		p := &yaml.Node{Kind: yaml.MappingNode}
		set(p, "query", str(e.Players.Query))
		if e.Players.Port != "" {
			set(p, "port", str(e.Players.Port))
		}
		set(ext, "players", p)
	}
	if len(e.Addons) > 0 {
		addons := &yaml.Node{Kind: yaml.MappingNode}
		for _, kind := range slices.Sorted(maps.Keys(e.Addons)) {
			a := e.Addons[kind]
			n := &yaml.Node{Kind: yaml.MappingNode}
			loaders := &yaml.Node{Kind: yaml.SequenceNode, Style: yaml.FlowStyle}
			for _, l := range a.Loaders {
				loaders.Content = append(loaders.Content, str(l))
			}
			set(n, "loaders", loaders)
			set(n, "dir", str(a.Dir))
			if a.Version != "" {
				set(n, "version", str(a.Version))
			}
			set(addons, kind, n)
		}
		set(ext, "addons", addons)
	}
	if w := e.Workshop; w != nil {
		n := &yaml.Node{Kind: yaml.MappingNode}
		set(n, "app_id", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(w.AppID, 10)})
		set(n, "variable", str(w.Variable))
		if w.Collection {
			set(n, "collection", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
		}
		if w.Apply != "" {
			set(n, "apply", str(w.Apply))
		}
		set(ext, "workshop", n)
	}
	if b := e.Backup; b != nil {
		n := &yaml.Node{Kind: yaml.MappingNode}
		for _, l := range []struct {
			key  string
			cmds []string
		}{{"pre", b.Pre}, {"post", b.Post}} {
			seq := &yaml.Node{Kind: yaml.SequenceNode}
			for _, c := range l.cmds {
				seq.Content = append(seq.Content, str(c))
			}
			set(n, l.key, seq)
		}
		set(n, "wait_for", str(b.WaitFor))
		if b.Timeout != "" {
			set(n, "timeout", str(b.Timeout))
		}
		set(ext, "backup", n)
	}
	if path.Ext(e.File) == ".json" {
		return ordered.EncodeJSON(root, "    ")
	}
	return yaml.Marshal(root)
}

// ImportFile is the egg as one file to import into a Panel's egg library:
// the server egg with the entry's icon.png in "image", as a data: URI (the
// way Pelican eggs carry their icons, which the Panel reads as the egg's
// icon).
func (e *Entry) ImportFile() ([]byte, error) {
	file, err := e.ServerEgg()
	if err != nil || len(e.Icon) == 0 {
		return file, err
	}
	root, err := ordered.Decode(file)
	if err != nil {
		return nil, err
	}
	set(root, "image", str("data:image/png;base64,"+base64.StdEncoding.EncodeToString(e.Icon)))
	if path.Ext(e.File) == ".json" {
		return ordered.EncodeJSON(root, "    ")
	}
	return yaml.Marshal(root)
}

func str(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

// mapping returns the mapping under key, adding an empty one if it's
// missing (or isn't a mapping).
func mapping(n *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key && n.Content[i+1].Kind == yaml.MappingNode {
			return n.Content[i+1]
		}
	}
	m := &yaml.Node{Kind: yaml.MappingNode}
	set(n, key, m)
	return m
}

// set sets key in a mapping, replacing its value if it's there.
func set(n *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = v
			return
		}
	}
	n.Content = append(n.Content, str(key), v)
}
