// Package eggs parses Pterodactyl and Pelican eggs into a single model and
// implements the runtime behavior eggs expect (environment, startup detection,
// stop handling). Pterodactyl Wings' behavior is the spec; see docs/EGGS.md.
package eggs

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/xena-studios/raptor/internal/shared/ordered"
)

// Supported egg format versions (meta.version).
var formats = map[string]bool{
	"PTDL_v1": true,
	"PTDL_v2": true,
	"PLCN_v1": true,
	"PLCN_v2": true,
	"PLCN_v3": true,
}

// Egg is the normalized form of every supported egg format.
type Egg struct {
	Format       string
	Name         string
	Author       string
	Description  string
	Features     []string
	Images       []Image          // in egg order; the first is the default
	Startup      []StartupCommand // in egg order; the first is the default
	Config       Config
	Install      Install
	Variables    []Variable
	FileDenylist []string
	Raptor       Extension // the "x-raptor" block, if any
}

// Image is a selectable Docker image.
type Image struct {
	Name string
	Ref  string
}

// StartupCommand is a named startup command (Pelican eggs can have several).
type StartupCommand struct {
	Name    string
	Command string
}

// Config is the egg's process configuration.
type Config struct {
	Files []ConfigFile
	Done  []Matcher
	// StripANSI removes ANSI escape codes before matching done strings.
	StripANSI bool
	Stop      Stop
}

// ConfigFile describes a file Wings rewrites before every start.
type ConfigFile struct {
	Path   string
	Parser string
	Find   []FindRule // in egg order
}

// FindRule is one replacement in a config file. Key's meaning depends on the
// parser (a property name, a dotted path, a line prefix). Value is a string,
// bool, or number, with placeholders unresolved.
//
// An egg can make a replacement conditional by giving an object instead of a
// value: {"key": {"<if_value>": "<value>"}}. Each entry becomes its own rule
// with IfValue set, in egg order, the way Pterodactyl's Panel flattens them.
// An IfValue starting with "regex:" replaces only the matched part.
type FindRule struct {
	Key     string
	IfValue string
	Value   any
}

// Install is the egg's installation script.
type Install struct {
	Script     string
	Container  string
	Entrypoint string
}

// Variable is a user-configurable egg variable, exposed as an environment variable.
type Variable struct {
	Name         string
	Description  string
	Env          string
	Default      string
	UserViewable bool
	UserEditable bool
	Rules        []string
}

// Extension holds Raptor-specific egg data under "x-raptor".
type Extension struct {
	Arch    []string `yaml:"arch"`
	Install struct {
		Timeout string `yaml:"timeout"`
	} `yaml:"install"`
	// Backup is the egg's own hooks; use Egg.Backup, which falls back to
	// the game's defaults.
	Backup BackupHooks `yaml:"backup"`
	Health struct {
		Type     string `yaml:"type"`
		Protocol string `yaml:"protocol"`
	} `yaml:"health"`
	Players struct {
		// Query is how to ask the game for its player count: "minecraft"
		// (Java edition status) or "source" (A2S_INFO, most Steam games).
		Query string `yaml:"query"`
		// Port is where it answers: empty = the primary port, "+1" an
		// offset from it, or a variable's name (QUERY_PORT).
		Port string `yaml:"port"`
	} `yaml:"players"`
	Certified bool `yaml:"certified"`
	// Addons are software add-ons servers made from the egg get
	// (docs/EGGS.md#add-ons): "plugins" or "mods" to their settings.
	Addons map[string]Addon `yaml:"addons"`
	// Workshop turns on Steam Workshop items for games that download them
	// themselves (docs/EGGS.md#steam-workshop); nil if the egg has none.
	Workshop *Workshop `yaml:"workshop"`
}

// BackupHooks are console commands sent around a backup of a running
// server (docs/EGGS.md#backup-hooks): Pre before the snapshot (save-off,
// save-all flush), Post after it whatever happened (save-on).
type BackupHooks struct {
	Pre  []string `yaml:"pre"`
	Post []string `yaml:"post"`
	// WaitFor is the console line that says the Pre commands are done: a
	// substring, or a regular expression after "regex:", as done lines are.
	WaitFor string `yaml:"wait_for"`
	// Timeout is how long to wait for it ("" = the node's default, a
	// minute). The backup is taken anyway after it, with a warning.
	Timeout string `yaml:"timeout"`
	// set: the egg has an x-raptor.backup block, which is used as it is,
	// even empty, instead of its game's defaults.
	set bool
}

// WaitTimeout returns Timeout, or 0 if it's unset.
func (h BackupHooks) WaitTimeout() time.Duration {
	d, _ := time.ParseDuration(h.Timeout)
	return max(d, 0)
}

// CheckBackup checks a backup block's timeout. (Nothing else: eggs stored
// with servers must keep parsing.)
func CheckBackup(h BackupHooks) error {
	if h.Timeout != "" {
		if d, err := time.ParseDuration(h.Timeout); err != nil || d <= 0 {
			return fmt.Errorf("egg: x-raptor.backup.timeout %q: want a duration like 5m", h.Timeout)
		}
	}
	return nil
}

// JavaMinecraftBackup is what a Minecraft Java server is sent around a
// backup: saving off and everything written out first, so the snapshot
// doesn't catch region files mid-write, and saving on again after. 1.13
// and later answer "Saved the game", older versions "Saved the world".
var JavaMinecraftBackup = BackupHooks{
	Pre:     []string{"save-off", "save-all flush"},
	Post:    []string{"save-on"},
	WaitFor: "regex:Saved the (game|world)",
	Timeout: "5m",
}

// Backup returns the hooks to run around a backup: the egg's own
// x-raptor.backup if it has one, else JavaMinecraftBackup for a Minecraft
// Java server (stopped with "stop", started at the "Done (…)! For help,
// type" line, as every Java server egg is), so servers whose egg lacks the
// block (imported ones, or made before the catalog had it) save safely too.
// Proxies and Bedrock don't match.
func (e *Egg) Backup() BackupHooks {
	if e.Raptor.Backup.set {
		return e.Raptor.Backup
	}
	if e.Config.Stop.Command == "stop" && slices.ContainsFunc(e.Config.Done, func(m Matcher) bool {
		return strings.Contains(m.String(), "For help, type")
	}) {
		return JavaMinecraftBackup
	}
	return BackupHooks{}
}

// Workshop says where a game takes Steam Workshop items: the IDs go in a
// variable, and the game (or its install script) downloads them. Raptor
// downloads nothing.
type Workshop struct {
	// AppID is the game whose Workshop the items are from.
	AppID int64 `yaml:"app_id"`
	// Variable holds the IDs: one collection's (Collection), or items',
	// comma-separated.
	Variable   string `yaml:"variable"`
	Collection bool   `yaml:"collection"`
	// Apply is what makes the game pick up a change: "restart" (the
	// default) or "reinstall".
	Apply string `yaml:"apply"`
}

// CheckWorkshop validates x-raptor.workshop against the egg's variables.
func CheckWorkshop(w *Workshop, vars []Variable) error {
	if w == nil {
		return nil
	}
	if w.AppID <= 0 {
		return errors.New("egg: x-raptor.workshop.app_id: want the game's Steam app ID")
	}
	if !slices.ContainsFunc(vars, func(v Variable) bool { return v.Env == w.Variable }) {
		return fmt.Errorf("egg: x-raptor.workshop.variable %q: not one of the egg's variables", w.Variable)
	}
	if w.Apply != "" && w.Apply != "restart" && w.Apply != "reinstall" {
		return fmt.Errorf("egg: x-raptor.workshop.apply %q: want restart or reinstall", w.Apply)
	}
	return nil
}

// Addon is an add-on kind an egg turns on: a plugin installer or a mod
// manager, which installs files from Modrinth into Dir.
type Addon struct {
	// Loaders are what the software runs (Modrinth's names: paper, fabric,
	// ...); add-ons for any of them can be installed.
	Loaders []string `yaml:"loaders"`
	// Dir is where the files go, from the server's directory.
	Dir string `yaml:"dir"`
	// Version is the variable holding the game version (MINECRAFT_VERSION),
	// to offer add-ons for it. Empty, or a value that isn't a version
	// ("latest"): any.
	Version string `yaml:"version"`
}

// AddonKinds are the add-on kinds and the loaders each takes. Datapacks
// run on any Java server; Modrinth names their one loader "datapack".
var AddonKinds = map[string][]string{
	"plugins":   {"paper", "spigot", "bukkit", "purpur", "folia", "velocity", "bungeecord", "waterfall", "sponge"},
	"mods":      {"fabric", "forge", "neoforge", "quilt"},
	"datapacks": {"datapack"},
}

// AddonDir is where a kind's files go by default: datapacks in the world
// (server.properties' level-name, "world" unless changed).
var AddonDir = map[string]string{"plugins": "plugins", "mods": "mods", "datapacks": "world/datapacks"}

// AddonExt is a kind's file extension.
var AddonExt = map[string]string{"plugins": ".jar", "mods": ".jar", "datapacks": ".zip"}

// CheckAddons validates x-raptor.addons.
func CheckAddons(addons map[string]Addon) error {
	for kind, a := range addons {
		known, ok := AddonKinds[kind]
		if !ok {
			return fmt.Errorf("egg: x-raptor.addons.%s: want plugins, mods, or datapacks", kind)
		}
		if len(a.Loaders) == 0 {
			return fmt.Errorf("egg: x-raptor.addons.%s: name its loaders", kind)
		}
		for _, l := range a.Loaders {
			if !slices.Contains(known, l) {
				return fmt.Errorf("egg: x-raptor.addons.%s: unknown loader %q", kind, l)
			}
		}
		clean := path.Clean(a.Dir)
		if a.Dir == "" || clean != a.Dir || path.IsAbs(a.Dir) || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
			return fmt.Errorf("egg: x-raptor.addons.%s.dir %q: want a folder inside the server, like plugins", kind, a.Dir)
		}
	}
	return nil
}

// DefaultInstallTimeout is used when the egg doesn't set x-raptor.install.timeout.
const DefaultInstallTimeout = 2 * time.Hour

// InstallTimeout returns how long the install script may run.
func (e *Egg) InstallTimeout() time.Duration {
	if d, err := time.ParseDuration(e.Raptor.Install.Timeout); err == nil && d > 0 {
		return d
	}
	return DefaultInstallTimeout
}

// SupportsArch reports whether the egg declares support for a CPU
// architecture (Go names: "amd64", "arm64"). Eggs that don't declare any
// are assumed to support all; the image manifest is checked separately.
func (e *Egg) SupportsArch(arch string) bool {
	if len(e.Raptor.Arch) == 0 {
		return true
	}
	for _, a := range e.Raptor.Arch {
		if normalizeArch(a) == arch {
			return true
		}
	}
	return false
}

func normalizeArch(a string) string {
	switch strings.ToLower(a) {
	case "x86_64", "x86-64", "amd64":
		return "amd64"
	case "aarch64", "arm64":
		return "arm64"
	}
	return strings.ToLower(a)
}

// DefaultImage returns the egg's default image reference.
func (e *Egg) DefaultImage() string {
	if len(e.Images) == 0 {
		return ""
	}
	return e.Images[0].Ref
}

// DefaultStartup returns the egg's default startup command.
func (e *Egg) DefaultStartup() string {
	if len(e.Startup) == 0 {
		return ""
	}
	return e.Startup[0].Command
}

// Parse parses an egg in any supported format (JSON or YAML), preserving the
// order of images, startup commands, and config keys.
func Parse(data []byte) (*Egg, error) {
	root, err := ordered.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("egg: %w", err)
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("egg: not an object")
	}

	e := &Egg{Format: scalar(get(get(root, "meta"), "version"))}
	if !formats[e.Format] {
		return nil, fmt.Errorf("egg: unsupported format %q", e.Format)
	}

	e.Name = scalar(get(root, "name"))
	e.Author = scalar(get(root, "author"))
	e.Description = scalar(get(root, "description"))
	e.Features = strings_(get(root, "features"))
	e.FileDenylist = strings_(get(root, "file_denylist"))

	// Images: later formats have an ordered "docker_images" map. PTDL_v1 has
	// an "images" list or a single "image" (Pterodactyl's importer accepts
	// both), and some exports have "docker_images" as a list.
	if n := get(root, "docker_images"); n != nil && n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			e.Images = append(e.Images, Image{Name: n.Content[i].Value, Ref: n.Content[i+1].Value})
		}
	} else if refs := strings_(n); len(refs) > 0 {
		for _, ref := range refs {
			e.Images = append(e.Images, Image{Name: ref, Ref: ref})
		}
	} else if refs := strings_(get(root, "images")); len(refs) > 0 {
		for _, ref := range refs {
			e.Images = append(e.Images, Image{Name: ref, Ref: ref})
		}
	} else if img := scalar(get(root, "image")); img != "" {
		e.Images = []Image{{Name: img, Ref: img}}
	}
	if len(e.Images) == 0 {
		return nil, errors.New("egg: no docker images")
	}

	// Startup: Pelican has an ordered "startup_commands" map; older eggs a single "startup".
	if n := get(root, "startup_commands"); n != nil && n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			e.Startup = append(e.Startup, StartupCommand{Name: n.Content[i].Value, Command: n.Content[i+1].Value})
		}
	} else if s := scalar(get(root, "startup")); s != "" {
		e.Startup = []StartupCommand{{Name: "Default", Command: s}}
	}
	if len(e.Startup) == 0 {
		return nil, errors.New("egg: no startup command")
	}

	cfg := get(root, "config")
	if e.Config.Files, err = parseFiles(embedded(get(cfg, "files"))); err != nil {
		return nil, err
	}
	startup := embedded(get(cfg, "startup"))
	for _, d := range strings_(get(startup, "done")) {
		e.Config.Done = append(e.Config.Done, NewMatcher(d))
	}
	e.Config.StripANSI = scalar(get(startup, "strip_ansi")) == "true"
	e.Config.Stop = ParseStop(scalar(get(cfg, "stop")))

	inst := get(get(root, "scripts"), "installation")
	e.Install = Install{
		Script:     scalar(get(inst, "script")),
		Container:  scalar(get(inst, "container")),
		Entrypoint: scalar(get(inst, "entrypoint")),
	}

	if vars := get(root, "variables"); vars != nil && vars.Kind == yaml.SequenceNode {
		for _, v := range vars.Content {
			e.Variables = append(e.Variables, Variable{
				Name:         scalar(get(v, "name")),
				Description:  scalar(get(v, "description")),
				Env:          scalar(get(v, "env_variable")),
				Default:      scalar(get(v, "default_value")),
				UserViewable: truthy(get(v, "user_viewable")),
				UserEditable: truthy(get(v, "user_editable")),
				Rules:        parseRules(get(v, "rules")),
			})
		}
	}

	if n := get(root, "x-raptor"); n != nil {
		if err := n.Decode(&e.Raptor); err != nil {
			return nil, fmt.Errorf("egg: x-raptor: %w", err)
		}
		if get(n, "backup") != nil {
			e.Raptor.Backup.set = true
			if err := CheckBackup(e.Raptor.Backup); err != nil {
				return nil, err
			}
		}
		if t := e.Raptor.Install.Timeout; t != "" {
			if d, err := time.ParseDuration(t); err != nil || d <= 0 {
				return nil, fmt.Errorf("egg: x-raptor.install.timeout %q: want a duration like 90m", t)
			}
		}
		for _, a := range e.Raptor.Arch {
			if n := normalizeArch(a); n != "amd64" && n != "arm64" {
				return nil, fmt.Errorf("egg: x-raptor.arch %q: want amd64 or arm64", a)
			}
		}
		switch q := e.Raptor.Players.Query; q {
		case "", "minecraft", "source":
		default:
			return nil, fmt.Errorf("egg: x-raptor.players.query %q: want minecraft or source", q)
		}
		// Kinds this version doesn't know are left out, not refused, so an
		// egg with a newer kind still runs on an older node.
		for kind := range e.Raptor.Addons {
			if _, ok := AddonKinds[kind]; !ok {
				delete(e.Raptor.Addons, kind)
			}
		}
		if err := CheckAddons(e.Raptor.Addons); err != nil {
			return nil, err
		}
		if err := CheckWorkshop(e.Raptor.Workshop, e.Variables); err != nil {
			return nil, err
		}
	}

	return e, nil
}

// scalarValue decodes a replacement value: a string, bool, or number. Anything
// else (lists, deeper objects) is kept as its JSON/YAML text.
func scalarValue(n *yaml.Node) (any, error) {
	if n.Kind != yaml.ScalarNode {
		out, err := yaml.Marshal(n)
		return strings.TrimSpace(string(out)), err
	}
	switch n.ShortTag() {
	case "!!bool", "!!int", "!!float":
		var v any
		if err := n.Decode(&v); err != nil {
			return nil, err
		}
		return v, nil
	case "!!null":
		return "", nil
	}
	return n.Value, nil
}

func parseFiles(n *yaml.Node) ([]ConfigFile, error) {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil, nil
	}
	var files []ConfigFile
	for i := 0; i+1 < len(n.Content); i += 2 {
		spec := n.Content[i+1]
		f := ConfigFile{Path: n.Content[i].Value, Parser: scalar(get(spec, "parser"))}
		if find := get(spec, "find"); find != nil && find.Kind == yaml.MappingNode {
			for j := 0; j+1 < len(find.Content); j += 2 {
				key, val := find.Content[j].Value, find.Content[j+1]
				if val.Kind == yaml.MappingNode {
					for k := 0; k+1 < len(val.Content); k += 2 {
						v, err := scalarValue(val.Content[k+1])
						if err != nil {
							return nil, fmt.Errorf("egg: config file %q, key %q: %w", f.Path, key, err)
						}
						f.Find = append(f.Find, FindRule{Key: key, IfValue: val.Content[k].Value, Value: v})
					}
					continue
				}
				v, err := scalarValue(val)
				if err != nil {
					return nil, fmt.Errorf("egg: config file %q, key %q: %w", f.Path, key, err)
				}
				f.Find = append(f.Find, FindRule{Key: key, Value: v})
			}
		}
		files = append(files, f)
	}
	return files, nil
}

// parseRules accepts Laravel-style rules as "a|b|c" or a list. Pipes inside a
// regex rule (regex:/a|b/) are kept together.
func parseRules(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.SequenceNode {
		var out []string
		for _, r := range strings_(n) {
			if r = strings.TrimSpace(r); r != "" {
				out = append(out, r)
			}
		}
		return out
	}
	if n.Value == "" {
		return nil
	}
	var out []string
	parts := strings.Split(n.Value, "|")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		if strings.HasPrefix(p, "regex:") || strings.HasPrefix(p, "not_regex:") {
			for !regexClosed(p) && i+1 < len(parts) {
				i++
				p += "|" + parts[i]
			}
		}
		if p = strings.TrimSpace(p); p != "" { // "required|string|" has an empty last rule
			out = append(out, p)
		}
	}
	return out
}

// regexClosed reports whether a regex rule's pattern (e.g. "regex:/a|b/i") is
// complete: it has an opening delimiter and a matching closing one, optionally
// followed by flags.
func regexClosed(rule string) bool {
	pat := rule[strings.IndexByte(rule, ':')+1:]
	if len(pat) < 2 {
		return true
	}
	delim := pat[0]
	end := strings.LastIndexByte(pat, delim)
	if end <= 0 {
		return false
	}
	return strings.Trim(pat[end+1:], "imsxuADSUXJ") == ""
}

// get returns the value node for key in a mapping node, or nil.
func get(n *yaml.Node, key string) *yaml.Node {
	if n == nil || n.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			return n.Content[i+1]
		}
	}
	return nil
}

// embedded handles PTDL eggs, which store config.files/startup/logs as
// JSON-encoded strings, and Pelican eggs, which store them as objects.
func embedded(n *yaml.Node) *yaml.Node {
	if n == nil || n.Kind != yaml.ScalarNode {
		return n
	}
	d, err := ordered.Decode([]byte(n.Value))
	if err != nil {
		return nil
	}
	return d
}

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

// strings_ returns a scalar as a one-element list, or a sequence's scalars.
func strings_(n *yaml.Node) []string {
	switch {
	case n == nil:
		return nil
	case n.Kind == yaml.ScalarNode && n.Tag != "!!null" && n.Value != "":
		return []string{n.Value}
	case n.Kind == yaml.SequenceNode:
		out := make([]string, 0, len(n.Content))
		for _, c := range n.Content {
			if s := scalar(c); s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// truthy accepts booleans and PTDL_v1's 0/1 integers.
func truthy(n *yaml.Node) bool {
	s := scalar(n)
	if b, err := strconv.ParseBool(s); err == nil {
		return b
	}
	i, err := strconv.Atoi(s)
	return err == nil && i != 0
}
