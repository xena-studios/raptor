package library

import (
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

// Entry is one egg in the library.
type Entry struct {
	// ID is the directory under library/, e.g. "minecraft/paper": the
	// egg's ID in the Panel's official library, which never changes.
	ID string
	// File is the egg's file name, as upstream named it.
	File string
	// Egg is the egg exactly as upstream published it.
	Egg []byte
	// Icon is the entry's icon.png, if it has one: shown instead of the
	// icon in the egg (docs/EGGS.md#egg-library).
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
	// Internal eggs are Raptor's own tools (the test server): seeded into
	// the official library for staff and the conformance suite, but not
	// listed in orgs' libraries.
	Internal bool `yaml:"internal"`
	// Arch lists the CPU architectures the game itself supports.
	Arch []string `yaml:"arch"`
	// Players says how to ask the game for its player count (the egg's
	// x-raptor.players, which upstream eggs don't have): the Panel adds it
	// to the egg servers are made from (ServerEgg).
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
	// Reforger says where Arma Reforger takes Bohemia Workshop mods (the
	// egg's x-raptor.reforger, docs/EGGS.md#arma-reforger-mods), added
	// like Players.
	Reforger *eggs.Reforger `yaml:"reforger"`
	// Web is a web dashboard the server serves (the egg's x-raptor.web),
	// for the app's link to it.
	Web *eggs.Web `yaml:"web"`
	// Modpack says which variables pick a modpack (the egg's
	// x-raptor.modpack), for the app's pack picker.
	Modpack *eggs.Modpack `yaml:"modpack"`
	// RCON is how to reach the game's remote console (the egg's
	// x-raptor.rcon), for the Players tab (#423).
	RCON *eggs.RCON `yaml:"rcon"`
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

// pausesSaving matches console commands that stop a game saving (save-off,
// Bedrock's save hold), which a backup's post must undo.
var pausesSaving = regexp.MustCompile(`(?i)save-?off|save hold|autosave (off|false|0)|disable|pause`)

func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	v, err := time.ParseDuration(n.Value)
	if err != nil {
		return err
	}
	*d = Duration(v)
	return nil
}

// loadFS loads entries laid out like the library (<category>/<egg>/ with
// a raptor.yaml) from fsys. Every entry is validated.
func loadFS(fsys fs.FS) ([]Entry, error) {
	metas, err := fs.Glob(fsys, "*/*/raptor.yaml")
	if err != nil {
		return nil, err
	}
	var out []Entry
	var errs []error
	for _, m := range metas {
		e, err := load(fsys, path.Dir(m))
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

func load(fsys fs.FS, dir string) (Entry, error) {
	e := Entry{ID: dir}
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
	if e.Own() && (s.Commit != "" || s.Path != "library/"+e.ID+"/"+e.File) {
		return fmt.Errorf("an egg of Raptor's own has no commit, and its path is library/%s/%s", e.ID, e.File)
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
	if err := eggs.CheckAddonVariables(e.Addons, egg.Variables); err != nil {
		return err
	}
	if err := eggs.CheckWeb(e.Web, egg.Variables); err != nil {
		return err
	}
	if err := eggs.CheckModpack(e.Modpack, egg.Variables); err != nil {
		return err
	}
	if err := eggs.CheckWorkshop(e.Workshop, egg.Variables); err != nil {
		return err
	}
	if err := eggs.CheckReforger(e.Reforger); err != nil {
		return err
	}
	if err := eggs.CheckRCON(e.RCON, egg.Variables); err != nil {
		return err
	}
	if b := e.Backup; b != nil {
		if err := eggs.CheckBackup(*b); err != nil {
			return err
		}
		switch {
		case len(b.Pre) == 0:
			// Post without pre undoes nothing. Pre alone is a save before
			// the snapshot (Rust's server.save), for games that can't
			// pause saving; one that pauses it must have a post.
			return errors.New("backup needs pre commands")
		case len(b.Post) == 0 && slices.ContainsFunc(b.Pre, pausesSaving.MatchString):
			return errors.New("backup: pre pauses saving, so post must resume it")
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
// egg with the library's x-raptor block added (the architectures, and how to
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
	ext := raptorBlock(root)
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
			if a.DirVariable != "" {
				set(n, "dir_variable", str(a.DirVariable))
				dirs := &yaml.Node{Kind: yaml.MappingNode}
				for _, v := range slices.Sorted(maps.Keys(a.Dirs)) {
					set(dirs, v, str(a.Dirs[v]))
				}
				set(n, "dirs", dirs)
			}
			if a.Auth != nil {
				auth := &yaml.Node{Kind: yaml.MappingNode}
				set(auth, "username", str(a.Auth.Username))
				set(auth, "token", str(a.Auth.Token))
				set(n, "auth", auth)
			}
			set(addons, kind, n)
		}
		set(ext, "addons", addons)
	}
	if w := e.Workshop; w != nil {
		n := &yaml.Node{Kind: yaml.MappingNode}
		set(n, "app_id", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(w.AppID, 10)})
		if w.Variable != "" {
			set(n, "variable", str(w.Variable))
		}
		if f := w.File; f != nil {
			fn := &yaml.Node{Kind: yaml.MappingNode}
			for _, kv := range [][2]string{{"path", f.Path}, {"format", f.Format}, {"key", f.Key}, {"separator", f.Separator}, {"mods_key", f.ModsKey}} {
				if kv[1] != "" {
					set(fn, kv[0], str(kv[1]))
				}
			}
			if len(f.Overrides) > 0 {
				seq := &yaml.Node{Kind: yaml.SequenceNode}
				for _, o := range f.Overrides {
					seq.Content = append(seq.Content, str(o))
				}
				set(fn, "overrides", seq)
			}
			set(n, "file", fn)
		}
		if w.Collection {
			set(n, "collection", &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
		}
		if w.Apply != "" {
			set(n, "apply", str(w.Apply))
		}
		set(ext, "workshop", n)
	}
	if r := e.Reforger; r != nil {
		n := &yaml.Node{Kind: yaml.MappingNode}
		set(n, "config", str(r.Config))
		set(ext, "reforger", n)
	}
	if w := e.Web; w != nil {
		n := &yaml.Node{Kind: yaml.MappingNode}
		set(n, "name", str(w.Name))
		if w.Port != "" {
			set(n, "port", str(w.Port))
		}
		if w.Enabled != "" {
			set(n, "enabled", str(w.Enabled))
		}
		set(ext, "web", n)
	}
	if m := e.Modpack; m != nil {
		n := &yaml.Node{Kind: yaml.MappingNode}
		set(n, "source", str(m.Source))
		set(n, "project", str(m.Project))
		set(n, "version", str(m.Version))
		if m.GameVersion != "" {
			set(n, "game_version", str(m.GameVersion))
		}
		set(ext, "modpack", n)
	}
	if r := e.RCON; r != nil {
		n := &yaml.Node{Kind: yaml.MappingNode}
		set(n, "protocol", str(r.Protocol))
		if r.Port != "" {
			set(n, "port", str(r.Port))
		}
		set(n, "password", str(r.PwVar))
		if r.Enabled != "" {
			set(n, "enabled", str(r.Enabled))
		}
		if r.Players != "" {
			set(n, "players", str(r.Players))
		}
		set(ext, "rcon", n)
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

// ImportFile is the egg as one file to import into another Panel's egg
// library (task library:export; Raptor's Panel seeds the library itself):
// the server egg with the entry's icon.png in "image", as a data: URI (the
// way Pelican eggs carry their icons, which the Panel reads as the egg's
// icon).
func (e *Entry) ImportFile() ([]byte, error) {
	file, err := e.ServerEgg()
	if err != nil {
		return nil, err
	}
	root, err := ordered.Decode(file)
	if err != nil {
		return nil, err
	}
	// The entry's ID, which the Panel matches an imported egg-library
	// file by (docs/EGGS.md#egg-library).
	set(raptorBlock(root), "library", str(e.ID))
	if len(e.Icon) > 0 {
		set(root, "image", str("data:image/png;base64,"+base64.StdEncoding.EncodeToString(e.Icon)))
	}
	if path.Ext(e.File) == ".json" {
		return ordered.EncodeJSON(root, "    ")
	}
	return yaml.Marshal(root)
}

// raptorBlock is where an egg's Raptor settings go: the top of one of
// Raptor's own eggs (RPTR_v1), else its x-raptor block.
func raptorBlock(root *yaml.Node) *yaml.Node {
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value != "meta" {
			continue
		}
		m := root.Content[i+1]
		for j := 0; j+1 < len(m.Content); j += 2 {
			if m.Content[j].Value == "version" && eggs.IsRaptor(m.Content[j+1].Value) {
				return root
			}
		}
	}
	return mapping(root, "x-raptor")
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
