// Package eggs parses Pterodactyl and Pelican eggs into a single model and
// implements the runtime behavior eggs expect (environment, startup detection,
// stop handling). Pterodactyl Wings' behavior is the spec; see docs/EGGS.md.
package eggs

import (
	"errors"
	"fmt"
	"path"
	"regexp"
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
	"RPTR_v1": true,
}

// Raptor is the format of Raptor's own eggs: Pelican's layout (an ordered
// docker_images map, startup or startup_commands, an icon in "image") with
// Raptor's settings as top-level fields, where other formats carry them
// under "x-raptor" (docs/EGGS.md#raptor-eggs).
const Raptor = "RPTR_v1"

// IsRaptor reports whether format is one of Raptor's own.
func IsRaptor(format string) bool { return strings.HasPrefix(format, "RPTR_") }

// RaptorKeys are Raptor's settings: top-level fields in Raptor's eggs, and
// the keys of "x-raptor" in other formats.
var RaptorKeys = []string{"arch", "category", "library", "install", "backup", "health", "players", "addons", "workshop", "reforger", "web", "modpack", "rcon", "scan"}

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
	Raptor       Extension // Raptor's settings: top-level in RPTR_v1, else the "x-raptor" block
	// Icon is the egg's picture, as Pelican eggs carry it: a data: URI in
	// "image" (data:image/png;base64,…). In PTDL_v1 eggs "image" was the
	// Docker image instead, so only data: URIs count.
	Icon string
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

// Extension holds Raptor's settings: top-level fields in Raptor's own
// eggs, and the "x-raptor" block in other formats.
type Extension struct {
	Arch []string `yaml:"arch"`
	// Category is where the egg goes in a library ("minecraft", "steam",
	// …) when it's imported without one being chosen.
	Category string `yaml:"category"`
	// Library is the egg's ID in Raptor's egg library ("minecraft/paper"),
	// which an import into the official library matches an egg by.
	Library string `yaml:"library"`
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
	// Reforger turns on Bohemia Workshop mods for Arma Reforger, listed in
	// its JSON config (docs/EGGS.md#arma-reforger-mods); nil if none.
	Reforger *Reforger `yaml:"reforger"`
	// Web is a web dashboard the server serves (txAdmin, Grafana), so the
	// app can link to it (#428); nil if it has none.
	Web *Web `yaml:"web"`
	// Modpack says which variables pick a modpack, so the app can offer a
	// pack picker (#413); nil if the egg has none.
	Modpack *Modpack `yaml:"modpack"`
	// RCON is how Wings reaches the game's remote console (#423), for the
	// Players tab; nil if the egg has none.
	RCON *RCON `yaml:"rcon"`
	// Scan turns on the backdoor scan of the server's scripts (#428); nil
	// if the egg has none.
	Scan *Scan `yaml:"scan"`
}

// RCON is where a game's remote console listens and how to log in. Wings
// connects from the node to the container; the password never leaves it.
type RCON struct {
	// Protocol is "source" (Valve's Source RCON over TCP: Source games,
	// ARK, Palworld, and most others) or "webrcon" (Rust's, a websocket).
	Protocol string `yaml:"protocol"`
	// Port is where it listens: empty = the primary port (Source games
	// take RCON on their game port), "+1" an offset from it, or a
	// variable's name (RCON_PORT).
	Port string `yaml:"port"`
	// PwVar is the variable holding the password (its name, never
	// the password). An empty value means RCON is off.
	PwVar string `yaml:"password"`
	// Enabled is a variable that turns RCON on ("1" or "true"); empty: on
	// whenever there's a password.
	Enabled string `yaml:"enabled"`
	// Players is the game's command set for the Players tab: "source"
	// (status, kickid, banid, say), "ark" (ListPlayers, KickPlayer,
	// BanPlayer, ServerChat), "palworld" (ShowPlayers, KickPlayer,
	// BanPlayer, Broadcast), or "rust" (playerlist, kick, banid, say).
	// Empty: no Players tab.
	Players string `yaml:"players"`
}

// PortFor is the RCON port of a server whose primary port is primary
// and whose variables are vars; 0 if it can't be told.
func (r *RCON) PortFor(primary int, vars map[string]string) int {
	port := primary
	switch p := strings.TrimSpace(r.Port); {
	case p == "":
	case strings.HasPrefix(p, "+") || strings.HasPrefix(p, "-"):
		n, err := strconv.Atoi(p)
		if err != nil {
			return 0
		}
		port += n
	default:
		n, err := strconv.Atoi(strings.TrimSpace(vars[p]))
		if err != nil {
			return 0
		}
		port = n
	}
	if port <= 0 || port > 65535 {
		return 0
	}
	return port
}

// PasswordFor is a server's RCON password, or "" when RCON is off: no
// password, or the Enabled variable isn't 1 or true.
func (r *RCON) PasswordFor(vars map[string]string) string {
	if r.Enabled != "" {
		switch strings.ToLower(strings.TrimSpace(vars[r.Enabled])) {
		case "1", "true", "yes", "on":
		default:
			return ""
		}
	}
	return vars[r.PwVar]
}

// RCONProtocols are the RCON protocols Wings speaks.
var RCONProtocols = []string{"source", "webrcon"}

// RCONPlayers are the Players tab's command sets, and the protocol each
// is spoken over.
var RCONPlayers = map[string]string{"source": "source", "ark": "source", "palworld": "source", "rust": "webrcon"}

// CheckRCON validates x-raptor.rcon against the egg's variables.
func CheckRCON(r *RCON, vars []Variable) error {
	if r == nil {
		return nil
	}
	if !slices.Contains(RCONProtocols, r.Protocol) {
		return fmt.Errorf("egg: x-raptor.rcon.protocol %q: want source or webrcon", r.Protocol)
	}
	isVar := func(v string) bool { return slices.ContainsFunc(vars, func(x Variable) bool { return x.Env == v }) }
	if !isVar(r.PwVar) {
		return fmt.Errorf("egg: x-raptor.rcon.password %q: not one of the egg's variables", r.PwVar)
	}
	if r.Enabled != "" && !isVar(r.Enabled) {
		return fmt.Errorf("egg: x-raptor.rcon.enabled %q: not one of the egg's variables", r.Enabled)
	}
	switch p := strings.TrimSpace(r.Port); {
	case p == "":
	case strings.HasPrefix(p, "+") || strings.HasPrefix(p, "-"):
		if _, err := strconv.Atoi(p); err != nil {
			return fmt.Errorf("egg: x-raptor.rcon.port %q: want an offset like +1 or a variable", r.Port)
		}
	case !isVar(p):
		return fmt.Errorf("egg: x-raptor.rcon.port %q: not one of the egg's variables", r.Port)
	}
	if r.Players != "" {
		proto, ok := RCONPlayers[r.Players]
		if !ok {
			return fmt.Errorf("egg: x-raptor.rcon.players %q: want source, ark, palworld, or rust", r.Players)
		}
		if proto != r.Protocol {
			return fmt.Errorf("egg: x-raptor.rcon.players %q goes with protocol %s", r.Players, proto)
		}
	}
	return nil
}

// ScanRules are the rule sets x-raptor.scan can name; Wings has their
// rules (internal/wings/backdoor).
var ScanRules = []string{"fivem"}

// Scan is where a server keeps scripts worth scanning for backdoors, and
// which rules fit them.
type Scan struct {
	// Rules is the rule set: "fivem".
	Rules string `yaml:"rules"`
	// Dirs are the folders scanned, from the server's directory.
	Dirs []string `yaml:"dirs"`
}

// CheckScan validates x-raptor.scan.
func CheckScan(s *Scan) error {
	if s == nil {
		return nil
	}
	if !slices.Contains(ScanRules, s.Rules) {
		return fmt.Errorf("egg: x-raptor.scan.rules %q: want one of %s", s.Rules, strings.Join(ScanRules, ", "))
	}
	if len(s.Dirs) == 0 || len(s.Dirs) > 8 {
		return errors.New("egg: x-raptor.scan.dirs: name 1 to 8 folders")
	}
	for _, d := range s.Dirs {
		if c := path.Clean(d); d == "" || c != d || path.IsAbs(d) || c == "." || c == ".." || strings.HasPrefix(c, "../") {
			return fmt.Errorf("egg: x-raptor.scan.dirs %q: want a folder inside the server's directory, like resources", d)
		}
	}
	return nil
}

// Modpack is where a modpack egg takes its pack: the variables holding
// the pack, its version, and the game version, which the egg's install
// script reads. Raptor downloads nothing; the picker fills them.
type Modpack struct {
	// Source is where packs are found: "modrinth".
	Source string `yaml:"source"`
	// Project holds the pack (its slug or ID), Version its version (an ID,
	// or "latest"), GameVersion the game version (optional).
	Project     string `yaml:"project"`
	Version     string `yaml:"version"`
	GameVersion string `yaml:"game_version"`
}

// CheckModpack validates x-raptor.modpack against the egg's variables.
func CheckModpack(m *Modpack, vars []Variable) error {
	if m == nil {
		return nil
	}
	if m.Source != "modrinth" && m.Source != "ftb" && m.Source != "technic" {
		return fmt.Errorf("egg: x-raptor.modpack.source %q: want modrinth, ftb, or technic", m.Source)
	}
	for key, v := range map[string]string{"project": m.Project, "version": m.Version, "game_version": m.GameVersion} {
		if v == "" && key == "game_version" {
			continue
		}
		if !slices.ContainsFunc(vars, func(x Variable) bool { return x.Env == v }) {
			return fmt.Errorf("egg: x-raptor.modpack.%s %q: not one of the egg's variables", key, v)
		}
	}
	return nil
}

// Web is a web page a server serves on one of its ports.
type Web struct {
	// Name is what it's called ("txAdmin", "Grafana").
	Name string `yaml:"name"`
	// Port is the variable holding its port; empty: the primary port. An
	// empty value means it's off.
	Port string `yaml:"port"`
	// Enabled is a variable that turns it on ("1" or "true"); empty: on.
	Enabled string `yaml:"enabled"`
}

// CheckWeb validates x-raptor.web against the egg's variables.
func CheckWeb(w *Web, vars []Variable) error {
	if w == nil {
		return nil
	}
	if strings.TrimSpace(w.Name) == "" || len(w.Name) > 40 {
		return errors.New("egg: x-raptor.web.name: name the page, like txAdmin")
	}
	for key, v := range map[string]string{"port": w.Port, "enabled": w.Enabled} {
		if v != "" && !slices.ContainsFunc(vars, func(x Variable) bool { return x.Env == v }) {
			return fmt.Errorf("egg: x-raptor.web.%s %q: not one of the egg's variables", key, v)
		}
	}
	return nil
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
// variable, or in a config file (File), and the game (or its install
// script) downloads them. Raptor downloads nothing.
type Workshop struct {
	// AppID is the game whose Workshop the items are from.
	AppID int64 `yaml:"app_id"`
	// Variable holds the IDs: one collection's (Collection), or items',
	// comma-separated. Empty when File holds them.
	Variable   string `yaml:"variable"`
	Collection bool   `yaml:"collection"`
	// File is the config file that lists the items, for games that read
	// them from one (#408); nil when Variable holds them.
	File *WorkshopFile `yaml:"file"`
	// Apply is what makes the game pick up a change: "restart" (the
	// default) or "reinstall".
	Apply string `yaml:"apply"`
}

// WorkshopFile is a config file that lists a game's Workshop items, edited
// in place through files.read and files.write (docs/EGGS.md#steam-workshop).
type WorkshopFile struct {
	// Path is the file, from the server's folder. {{VAR}} is the value of
	// one of the egg's variables (Project Zomboid's server name).
	Path string `yaml:"path"`
	// Format is how the file lists them: one of WorkshopFormats.
	Format string `yaml:"format"`
	// Key is the list's key: an ini line's (WorkshopItems), or a JSON
	// file's top-level key (File_IDs).
	Key string `yaml:"key"`
	// Separator splits an ini list (";" when empty).
	Separator string `yaml:"separator"`
	// ModsKey is an ini line with the game's own mod IDs, which aren't the
	// items' IDs (Project Zomboid's Mods).
	ModsKey string `yaml:"mods_key"`
	// Overrides are the dst format's modoverrides.lua files, one per shard,
	// where each item is turned on.
	Overrides []string `yaml:"overrides"`
}

// WorkshopFormats are the file formats x-raptor.workshop.file takes: an
// ini line (key=a;b), a JSON file's top-level array of numbers, and Don't
// Starve Together's dedicated_server_mods_setup.lua plus modoverrides.lua.
var WorkshopFormats = []string{"ini", "json", "dst"}

var workshopPathVar = regexp.MustCompile(`\{\{([A-Za-z0-9_]+)\}\}`)

// CheckWorkshop validates x-raptor.workshop against the egg's variables.
func CheckWorkshop(w *Workshop, vars []Variable) error {
	if w == nil {
		return nil
	}
	if w.AppID <= 0 {
		return errors.New("egg: x-raptor.workshop.app_id: want the game's Steam app ID")
	}
	has := func(env string) bool { return slices.ContainsFunc(vars, func(v Variable) bool { return v.Env == env }) }
	switch {
	case w.File != nil && w.Variable != "":
		return errors.New("egg: x-raptor.workshop: want variable or file, not both")
	case w.File != nil:
		if err := checkWorkshopFile(w.File, has); err != nil {
			return err
		}
		if w.Collection {
			return errors.New("egg: x-raptor.workshop.collection: only with a variable")
		}
	case !has(w.Variable):
		return fmt.Errorf("egg: x-raptor.workshop.variable %q: not one of the egg's variables", w.Variable)
	}
	if w.Apply != "" && w.Apply != "restart" && w.Apply != "reinstall" {
		return fmt.Errorf("egg: x-raptor.workshop.apply %q: want restart or reinstall", w.Apply)
	}
	return nil
}

func checkWorkshopFile(f *WorkshopFile, has func(string) bool) error {
	paths := append([]string{f.Path}, f.Overrides...)
	for _, p := range paths {
		if p == "" || strings.HasPrefix(p, "/") || strings.Contains(p, "\\") || path.Clean(p) != p || p == ".." || strings.HasPrefix(p, "../") {
			return fmt.Errorf("egg: x-raptor.workshop.file: path %q: want a clean path in the server's folder", p)
		}
		for _, m := range workshopPathVar.FindAllStringSubmatch(p, -1) {
			if !has(m[1]) {
				return fmt.Errorf("egg: x-raptor.workshop.file: path %q: %s is not one of the egg's variables", p, m[1])
			}
		}
	}
	switch f.Format {
	case "ini":
		if f.Key == "" {
			return errors.New("egg: x-raptor.workshop.file.key: want the ini line's key")
		}
	case "json":
		if f.Key == "" {
			return errors.New("egg: x-raptor.workshop.file.key: want the JSON file's key")
		}
	case "dst":
		if len(f.Overrides) == 0 {
			return errors.New("egg: x-raptor.workshop.file.overrides: want each shard's modoverrides.lua")
		}
	default:
		return fmt.Errorf("egg: x-raptor.workshop.file.format %q: want one of %s", f.Format, strings.Join(WorkshopFormats, ", "))
	}
	if f.Format != "ini" && (f.Separator != "" || f.ModsKey != "") {
		return errors.New("egg: x-raptor.workshop.file: separator and mods_key are only for ini")
	}
	if f.Format != "dst" && len(f.Overrides) > 0 {
		return errors.New("egg: x-raptor.workshop.file.overrides: only for dst")
	}
	if len([]rune(f.Separator)) > 1 {
		return fmt.Errorf("egg: x-raptor.workshop.file.separator %q: want one character", f.Separator)
	}
	return nil
}

// Reforger says where Arma Reforger takes Bohemia Workshop mods: the
// game.mods list of its JSON config, which the game downloads from at
// start. Raptor downloads nothing; it edits the list.
type Reforger struct {
	// Config is the server's JSON config, relative to its folder.
	Config string `yaml:"config"`
}

// CheckReforger validates x-raptor.reforger.
func CheckReforger(r *Reforger) error {
	if r == nil {
		return nil
	}
	c := r.Config
	if c == "" || strings.HasPrefix(c, "/") || slices.Contains(strings.Split(c, "/"), "..") || !strings.HasSuffix(c, ".json") {
		return fmt.Errorf("egg: x-raptor.reforger.config %q: want a .json file in the server's folder", c)
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
	// DirVariable and Dirs pick the folder by a variable's value, for
	// software that keeps add-ons in different places (Rust's FRAMEWORK:
	// Oxide's oxide/plugins, Carbon's carbon/plugins). A value Dirs
	// doesn't name uses Dir.
	DirVariable string            `yaml:"dir_variable"`
	Dirs        map[string]string `yaml:"dirs"`
	// Auth names the variables holding an account's username and token,
	// for sources whose downloads need one (Factorio's mod portal). Wings
	// adds them to the download itself, so they never travel in a command.
	Auth *AddonAuth `yaml:"auth"`
}

// AddonAuth is where an add-on source's account is: variable names.
type AddonAuth struct {
	Username string `yaml:"username"`
	Token    string `yaml:"token"`
}

// DirFor is the folder for a server with these variables.
func (a Addon) DirFor(vars map[string]string) string {
	if d, ok := a.Dirs[vars[a.DirVariable]]; ok && a.DirVariable != "" {
		return d
	}
	return a.Dir
}

// AddonKinds are the add-on kinds and the loaders each takes. Datapacks
// run on any Java server; Modrinth names their one loader "datapack".
// umod is Rust's plugins from uMod, for Oxide and Carbon. thunderstore is
// BepInEx mods from Thunderstore, whose loaders are its communities (the
// game each mod is for).
var AddonKinds = map[string][]string{
	"plugins":   {"paper", "spigot", "bukkit", "purpur", "folia", "velocity", "bungeecord", "waterfall", "sponge"},
	"mods":      {"fabric", "forge", "neoforge", "quilt"},
	"datapacks": {"datapack"},
	"umod":      {"oxide", "carbon"},
	"factorio":  {"factorio"},
	// Thunderstore's communities that have dedicated servers Raptor runs.
	"thunderstore": {"valheim", "v-rising"},
}

// AddonDir is where a kind's files go by default: datapacks in the world
// (server.properties' level-name, "world" unless changed).
var AddonDir = map[string]string{"plugins": "plugins", "mods": "mods", "datapacks": "world/datapacks", "umod": "oxide/plugins", "factorio": "mods", "thunderstore": "BepInEx/plugins"}

// AddonExt is a kind's file extension: uMod's plugins are C# source.
// Thunderstore's packages are downloaded as zips and unpacked, one folder
// each.
var AddonExt = map[string]string{"plugins": ".jar", "mods": ".jar", "datapacks": ".zip", "umod": ".cs", "factorio": ".zip", "thunderstore": ".zip"}

// CheckAddons validates x-raptor.addons.
func CheckAddons(addons map[string]Addon) error {
	for kind, a := range addons {
		known, ok := AddonKinds[kind]
		if !ok {
			return fmt.Errorf("egg: x-raptor.addons.%s: want plugins, mods, datapacks, umod, factorio, or thunderstore", kind)
		}
		if len(a.Loaders) == 0 {
			return fmt.Errorf("egg: x-raptor.addons.%s: name its loaders", kind)
		}
		for _, l := range a.Loaders {
			if !slices.Contains(known, l) {
				return fmt.Errorf("egg: x-raptor.addons.%s: unknown loader %q", kind, l)
			}
		}
		if !insideDir(a.Dir) {
			return fmt.Errorf("egg: x-raptor.addons.%s.dir %q: want a folder inside the server, like plugins", kind, a.Dir)
		}
		if (a.DirVariable == "") != (len(a.Dirs) == 0) {
			return fmt.Errorf("egg: x-raptor.addons.%s: dir_variable and dirs go together", kind)
		}
		for v, d := range a.Dirs {
			if !insideDir(d) {
				return fmt.Errorf("egg: x-raptor.addons.%s.dirs.%s %q: want a folder inside the server", kind, v, d)
			}
		}
	}
	return nil
}

// insideDir is a clean relative folder inside the server.
func insideDir(d string) bool {
	clean := path.Clean(d)
	return d != "" && clean == d && !path.IsAbs(d) && clean != "." && clean != ".." && !strings.HasPrefix(clean, "../")
}

// CheckAddonVariables checks that the variables add-ons name are the egg's.
func CheckAddonVariables(addons map[string]Addon, vars []Variable) error {
	for kind, a := range addons {
		if a.DirVariable != "" && !slices.ContainsFunc(vars, func(v Variable) bool { return v.Env == a.DirVariable }) {
			return fmt.Errorf("egg: x-raptor.addons.%s.dir_variable %q: not one of the egg's variables", kind, a.DirVariable)
		}
		if a.Auth != nil {
			for key, name := range map[string]string{"username": a.Auth.Username, "token": a.Auth.Token} {
				if !slices.ContainsFunc(vars, func(v Variable) bool { return v.Env == name }) {
					return fmt.Errorf("egg: x-raptor.addons.%s.auth.%s %q: not one of the egg's variables", kind, key, name)
				}
			}
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

// Supported reports whether data looks like an egg in a supported format
// (an object whose meta.version Parse knows), so a Parse error is about
// the egg itself rather than the file not being one.
func Supported(data []byte) bool {
	root, err := ordered.Decode(data)
	return err == nil && root.Kind == yaml.MappingNode && formats[scalar(get(get(root, "meta"), "version"))]
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
	} else if img := scalar(get(root, "image")); img != "" && !strings.HasPrefix(img, "data:") {
		e.Images = []Image{{Name: img, Ref: img}}
	}
	if img := scalar(get(root, "image")); strings.HasPrefix(img, "data:image/") {
		e.Icon = img
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

	n := get(root, "x-raptor")
	if IsRaptor(e.Format) {
		n = root
	}
	if IsRaptor(e.Format) && get(root, "x-raptor") != nil {
		return nil, errors.New("egg: Raptor's eggs have their settings at the top level, not under x-raptor")
	}
	if err := e.parseRaptor(n); err != nil {
		if IsRaptor(e.Format) {
			// Raptor's eggs have the settings at the top.
			msg := strings.ReplaceAll(err.Error(), "x-raptor: ", "")
			return nil, errors.New(strings.ReplaceAll(msg, "x-raptor.", ""))
		}
		return nil, err
	}
	return e, nil
}

// parseRaptor reads and checks Raptor's settings from n, if there are any.
func (e *Egg) parseRaptor(n *yaml.Node) error {
	if n != nil {
		var ext Extension
		if err := n.Decode(&ext); err != nil {
			return fmt.Errorf("egg: x-raptor: %w", err)
		}
		e.Raptor = ext
		if get(n, "backup") != nil {
			e.Raptor.Backup.set = true
			if err := CheckBackup(e.Raptor.Backup); err != nil {
				return err
			}
		}
		if t := e.Raptor.Install.Timeout; t != "" {
			if d, err := time.ParseDuration(t); err != nil || d <= 0 {
				return fmt.Errorf("egg: x-raptor.install.timeout %q: want a duration like 90m", t)
			}
		}
		for _, a := range e.Raptor.Arch {
			if n := normalizeArch(a); n != "amd64" && n != "arm64" {
				return fmt.Errorf("egg: x-raptor.arch %q: want amd64 or arm64", a)
			}
		}
		switch q := e.Raptor.Players.Query; q {
		case "", "minecraft", "source":
		default:
			return fmt.Errorf("egg: x-raptor.players.query %q: want minecraft or source", q)
		}
		// Kinds this version doesn't know are left out, not refused, so an
		// egg with a newer kind still runs on an older node.
		for kind := range e.Raptor.Addons {
			if _, ok := AddonKinds[kind]; !ok {
				delete(e.Raptor.Addons, kind)
			}
		}
		if err := CheckAddons(e.Raptor.Addons); err != nil {
			return err
		}
		if err := CheckAddonVariables(e.Raptor.Addons, e.Variables); err != nil {
			return err
		}
		if err := CheckWorkshop(e.Raptor.Workshop, e.Variables); err != nil {
			return err
		}
		if err := CheckReforger(e.Raptor.Reforger); err != nil {
			return err
		}
		if err := CheckWeb(e.Raptor.Web, e.Variables); err != nil {
			return err
		}
		if err := CheckModpack(e.Raptor.Modpack, e.Variables); err != nil {
			return err
		}
		// A protocol or command set this version doesn't know is left out,
		// not refused (as add-on kinds are), so a newer egg still runs.
		if r := e.Raptor.RCON; r != nil {
			if _, ok := RCONPlayers[r.Players]; r.Players != "" && !ok {
				r.Players = ""
			}
			if !slices.Contains(RCONProtocols, r.Protocol) {
				e.Raptor.RCON = nil
			}
		}
		if err := CheckRCON(e.Raptor.RCON, e.Variables); err != nil {
			return err
		}
		if err := CheckScan(e.Raptor.Scan); err != nil {
			return err
		}
	}

	return nil
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
