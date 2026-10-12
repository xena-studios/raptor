// Package spec reads an egg file into the fields Raptor's egg editor shows,
// and writes edits back into the file (docs/EGGS.md#editing-eggs).
//
// Writing changes only what the editor covers. Keys it doesn't know
// (Pelican's tags, config.logs, a variable's field_type, ...) stay as they
// were, and so does the format's style: Pterodactyl's JSON-encoded config
// strings and "a|b" rules, Pelican's objects and lists of rules. The result
// is always JSON, which both formats accept.
package spec

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/shared/ordered"
)

// Spec is what the editor edits.
type Spec struct {
	Name        string
	Author      string
	Description string
	// Images are the runtimes to choose from; the first is the default.
	Images []eggs.Image
	// Startup commands; the first is the default. Pterodactyl eggs have one.
	Startup []eggs.StartupCommand
	// StopType is "command" (StopValue is written to the console), "signal"
	// (StopValue is SIGINT, SIGTERM, SIGABRT, or SIGKILL), or "native" (the
	// image's own stop signal).
	StopType  string
	StopValue string
	// Done are console lines that mean the server is up: substrings, or
	// regular expressions after "regex:".
	Done      []string
	Install   eggs.Install
	Variables []eggs.Variable
	Files     []ConfigFile
	// Features are the egg's features: "eula" asks for the Minecraft EULA.
	Features     []string
	FileDenylist []string
	// Raptor's x-raptor block.
	Arch         []string
	PlayersQuery string
	PlayersPort  string
}

// ConfigFile is a file Wings edits before every start.
type ConfigFile struct {
	Path   string
	Parser string
	Find   []Find
}

// Find is one replacement. With IfValue set, it only replaces that value.
type Find struct {
	Key     string
	IfValue string
	Value   string
}

// Limits.
const (
	MaxName        = 100
	MaxDescription = 2000
	MaxImages      = 20
	MaxVariables   = 100
	MaxFiles       = 50
	MaxScript      = 256 << 10
)

// Parsers are the config file parsers Wings has.
var Parsers = []string{"file", "properties", "ini", "json", "yaml", "yml", "xml"}

// Signals a stop can send.
var Signals = []string{"SIGINT", "SIGTERM", "SIGABRT", "SIGKILL"}

var (
	envName  = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,190}$`)
	reserved = []string{"STARTUP", "SERVER_MEMORY", "SERVER_IP", "SERVER_PORT", "P_SERVER_LOCATION", "P_SERVER_UUID", "P_SERVER_ALLOCATION_LIMIT", "TZ", "HOME", "USER", "PATH"}
)

// Read reads an egg file.
func Read(data []byte) (*Spec, error) {
	e, err := eggs.Parse(data)
	if err != nil {
		return nil, err
	}
	s := &Spec{
		Name: e.Name, Author: e.Author, Description: e.Description, Images: e.Images, Startup: e.Startup,
		Install: e.Install, Variables: e.Variables, Features: e.Features, FileDenylist: e.FileDenylist,
		Arch: e.Raptor.Arch, PlayersQuery: e.Raptor.Players.Query, PlayersPort: e.Raptor.Players.Port,
	}
	switch stop := e.Config.Stop; {
	case stop.Command != "":
		s.StopType, s.StopValue = "command", stop.Command
	case stop.Signal != 0:
		s.StopType, s.StopValue = "signal", signalName(stop.Signal)
	default:
		s.StopType = "native"
	}
	for _, d := range e.Config.Done {
		s.Done = append(s.Done, d.String())
	}
	for _, f := range e.Config.Files {
		cf := ConfigFile{Path: f.Path, Parser: f.Parser}
		for _, r := range f.Find {
			cf.Find = append(cf.Find, Find{Key: r.Key, IfValue: r.IfValue, Value: fmt.Sprint(r.Value)})
		}
		s.Files = append(s.Files, cf)
	}
	return s, nil
}

func signalName(sig syscall.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	case syscall.SIGABRT:
		return "SIGABRT"
	}
	return "SIGKILL"
}

// Blank is the egg "create from scratch" starts from: Pterodactyl's
// current export format.
func Blank(now time.Time) []byte {
	return fmt.Appendf(nil, `{
    "_comment": "Made with Raptor's egg editor",
    "meta": {
        "version": "PTDL_v2",
        "update_url": null
    },
    "exported_at": %q,
    "name": "",
    "author": "",
    "description": "",
    "features": [],
    "docker_images": {},
    "file_denylist": [],
    "startup": "",
    "config": {
        "files": "{}",
        "startup": "{\"done\": []}",
        "logs": "{}",
        "stop": "stop"
    },
    "scripts": {
        "installation": {
            "script": "",
            "container": "ghcr.io/pterodactyl/installers:debian",
            "entrypoint": "bash"
        }
    },
    "variables": []
}
`, now.UTC().Format(time.RFC3339))
}

// invalid is a mistake in what the editor sent, worded for its author.
type invalid string

func (e invalid) Error() string { return string(e) }

// IsInvalid reports whether err is a mistake in the spec (rather than in
// the file underneath).
func IsInvalid(err error) bool {
	var i invalid
	return errors.As(err, &i)
}

func bad(format string, a ...any) error { return invalid(fmt.Sprintf(format, a...)) }

// Validate checks a spec, and tidies it (trims names, drops empty rows).
func (s *Spec) Validate() error {
	s.Name = strings.TrimSpace(s.Name)
	s.Author = strings.TrimSpace(s.Author)
	s.Description = strings.TrimSpace(s.Description)
	switch {
	case s.Name == "":
		return bad("give the egg a name")
	case utf8.RuneCountInString(s.Name) > MaxName:
		return bad("the name is at most %d characters", MaxName)
	case utf8.RuneCountInString(s.Description) > MaxDescription:
		return bad("the description is at most %d characters", MaxDescription)
	case len(s.Author) > 254:
		return bad("the author is at most 254 characters")
	}
	if len(s.Images) == 0 {
		return bad("add at least one runtime image")
	}
	if len(s.Images) > MaxImages {
		return bad("an egg has at most %d runtimes", MaxImages)
	}
	seen := map[string]bool{}
	for i := range s.Images {
		im := &s.Images[i]
		im.Name, im.Ref = strings.TrimSpace(im.Name), strings.TrimSpace(im.Ref)
		if im.Ref == "" || strings.ContainsAny(im.Ref, " \t\n") || len(im.Ref) > 255 {
			return bad("runtime %d needs an image, like ghcr.io/pterodactyl/yolks:java_21", i+1)
		}
		if im.Name == "" {
			im.Name = im.Ref
		}
		if seen[im.Name] {
			return bad("two runtimes are called %q", im.Name)
		}
		seen[im.Name] = true
	}
	var startup []eggs.StartupCommand
	for _, c := range s.Startup {
		c.Name, c.Command = strings.TrimSpace(c.Name), strings.TrimSpace(c.Command)
		if c.Command == "" {
			continue
		}
		if c.Name == "" {
			c.Name = "Default"
		}
		startup = append(startup, c)
	}
	if len(startup) == 0 {
		return bad("set a startup command")
	}
	s.Startup = startup
	switch s.StopType {
	case "command":
		if s.StopValue = strings.TrimSpace(s.StopValue); s.StopValue == "" {
			return bad("say what to type to stop the server, like stop")
		}
		if strings.HasPrefix(s.StopValue, "^") {
			return bad("a stop command can't start with ^; choose a signal instead")
		}
	case "signal":
		if !slices.Contains(Signals, s.StopValue) {
			return bad("the stop signal is SIGINT, SIGTERM, SIGABRT, or SIGKILL")
		}
	case "native":
		s.StopValue = ""
	default:
		return bad("stop the server with a command, a signal, or the image's default")
	}
	s.Done = nonEmpty(s.Done) // spaces count: "Listening on " isn't "Listening on"
	for _, d := range s.Done {
		if p, ok := strings.CutPrefix(d, "regex:"); ok {
			if _, err := regexp.Compile(p); err != nil {
				return bad("the ready line %q isn't a valid regular expression", p)
			}
		}
	}
	if len(s.Install.Script) > MaxScript {
		return bad("the install script is at most %d KiB", MaxScript>>10)
	}
	s.Install.Container = strings.TrimSpace(s.Install.Container)
	s.Install.Entrypoint = strings.TrimSpace(s.Install.Entrypoint)
	if strings.TrimSpace(s.Install.Script) != "" && s.Install.Container == "" {
		return bad("the install script needs an image to run in")
	}
	if len(s.Variables) > MaxVariables {
		return bad("an egg has at most %d variables", MaxVariables)
	}
	envs := map[string]bool{}
	for i := range s.Variables {
		v := &s.Variables[i]
		v.Name, v.Env = strings.TrimSpace(v.Name), strings.TrimSpace(v.Env)
		switch {
		case v.Name == "":
			return bad("variable %d needs a name", i+1)
		case !envName.MatchString(v.Env):
			return bad("%s: the key is letters, digits, and _, like SERVER_NAME", v.Name)
		case slices.Contains(reserved, strings.ToUpper(v.Env)):
			return bad("%s: %s is set by Raptor; choose another key", v.Name, v.Env)
		case envs[v.Env]:
			return bad("two variables use the key %s", v.Env)
		}
		envs[v.Env] = true
		v.Rules = compact(v.Rules)
	}
	if len(s.Files) > MaxFiles {
		return bad("an egg edits at most %d config files", MaxFiles)
	}
	files := map[string]bool{}
	for i := range s.Files {
		f := &s.Files[i]
		f.Path = strings.TrimSpace(f.Path)
		clean := path.Clean(f.Path)
		switch {
		case f.Path == "" || clean != f.Path || path.IsAbs(f.Path) || clean == ".." || strings.HasPrefix(clean, "../"):
			return bad("config file %d needs a path inside the server, like server.properties", i+1)
		case !slices.Contains(Parsers, f.Parser):
			return bad("%s: choose the file's format", f.Path)
		case files[f.Path]:
			return bad("%s is listed twice", f.Path)
		}
		files[f.Path] = true
		var find []Find
		for _, r := range f.Find {
			if strings.TrimSpace(r.Key) != "" { // the file parser matches line prefixes, spaces and all
				find = append(find, r)
			}
		}
		f.Find = find
	}
	s.Features = compact(s.Features)
	s.FileDenylist = compact(s.FileDenylist)
	for _, a := range s.Arch {
		if a != "amd64" && a != "arm64" {
			return bad("the CPUs are amd64 and arm64")
		}
	}
	switch s.PlayersQuery {
	case "", "minecraft", "source":
	default:
		return bad("count players with the Minecraft or Source query")
	}
	if s.PlayersQuery == "" {
		s.PlayersPort = ""
	}
	return nil
}

func nonEmpty(in []string) []string {
	out := []string{}
	for _, v := range in {
		if strings.TrimSpace(v) != "" {
			out = append(out, v)
		}
	}
	return out
}

func compact(in []string) []string {
	out := []string{}
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}

// Write applies a spec to an egg file (base; Blank for a new egg), and
// returns the edited file as JSON. The spec is validated first.
func Write(base []byte, s *Spec) ([]byte, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	root, err := ordered.Decode(base)
	if err != nil {
		return nil, err
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("egg isn't an object")
	}
	ptero := strings.HasPrefix(scalar(get(get(root, "meta"), "version")), "PTDL")

	set(root, "name", str(s.Name))
	set(root, "author", str(s.Author))
	set(root, "description", str(s.Description))
	set(root, "features", strs(s.Features))

	images := &yaml.Node{Kind: yaml.MappingNode}
	for _, im := range s.Images {
		images.Content = append(images.Content, str(im.Name), str(im.Ref))
	}
	set(root, "docker_images", images)
	del(root, "images")
	del(root, "image")
	set(root, "file_denylist", strs(s.FileDenylist))

	if n := get(root, "startup_commands"); n != nil || !ptero {
		cmds := &yaml.Node{Kind: yaml.MappingNode}
		for _, c := range s.Startup {
			cmds.Content = append(cmds.Content, str(c.Name), str(c.Command))
		}
		set(root, "startup_commands", cmds)
		del(root, "startup")
	} else {
		if len(s.Startup) > 1 {
			return nil, bad("Pterodactyl eggs have one startup command")
		}
		set(root, "startup", str(s.Startup[0].Command))
	}

	cfg := mapping(root, "config")
	if err := writeFiles(cfg, s.Files, ptero); err != nil {
		return nil, err
	}
	if err := writeDone(cfg, s.Done, ptero); err != nil {
		return nil, err
	}
	switch s.StopType {
	case "command":
		set(cfg, "stop", str(s.StopValue))
	case "signal":
		set(cfg, "stop", str("^"+s.StopValue))
	default:
		set(cfg, "stop", str(""))
	}

	inst := mapping(mapping(root, "scripts"), "installation")
	set(inst, "script", str(s.Install.Script))
	set(inst, "container", str(s.Install.Container))
	set(inst, "entrypoint", str(s.Install.Entrypoint))

	writeVariables(root, s.Variables, ptero)

	ext := get(root, "x-raptor")
	if ext == nil && (len(s.Arch) > 0 || s.PlayersQuery != "") {
		ext = mapping(root, "x-raptor")
	}
	if ext != nil && ext.Kind == yaml.MappingNode {
		if len(s.Arch) > 0 {
			arch := strs(s.Arch)
			arch.Style = yaml.FlowStyle
			set(ext, "arch", arch)
		} else {
			del(ext, "arch")
		}
		if s.PlayersQuery != "" {
			p := &yaml.Node{Kind: yaml.MappingNode}
			set(p, "query", str(s.PlayersQuery))
			if s.PlayersPort != "" {
				set(p, "port", str(s.PlayersPort))
			}
			set(ext, "players", p)
		} else {
			del(ext, "players")
		}
	}

	out, err := ordered.EncodeJSON(root, "    ")
	if err != nil {
		return nil, err
	}
	if _, err := eggs.Parse(out); err != nil {
		return nil, bad("%s", strings.TrimPrefix(err.Error(), "egg: "))
	}
	return out, nil
}

// embeddedNode reads config.files or config.startup: a JSON string in
// Pterodactyl eggs, an object in Pelican's. It returns the object, and
// whether the egg keeps it as a string.
func embeddedNode(cfg *yaml.Node, key string, ptero bool) (*yaml.Node, bool) {
	n := get(cfg, key)
	switch {
	case n == nil:
		return &yaml.Node{Kind: yaml.MappingNode}, ptero
	case n.Kind == yaml.ScalarNode:
		if d, err := ordered.Decode([]byte(n.Value)); err == nil && d.Kind == yaml.MappingNode {
			return d, true
		}
		return &yaml.Node{Kind: yaml.MappingNode}, true
	case n.Kind == yaml.MappingNode:
		return n, false
	}
	return &yaml.Node{Kind: yaml.MappingNode}, ptero
}

func setEmbedded(cfg *yaml.Node, key string, n *yaml.Node, asString bool) error {
	if !asString {
		set(cfg, key, n)
		return nil
	}
	b, err := ordered.EncodeJSON(n, "    ")
	if err != nil {
		return err
	}
	set(cfg, key, str(strings.TrimSuffix(string(b), "\n")))
	return nil
}

func writeFiles(cfg *yaml.Node, files []ConfigFile, ptero bool) error {
	old, asString := embeddedNode(cfg, "files", ptero)
	out := &yaml.Node{Kind: yaml.MappingNode}
	for _, f := range files {
		before := get(get(old, f.Path), "find")
		spec := get(old, f.Path)
		if spec == nil || spec.Kind != yaml.MappingNode {
			spec = &yaml.Node{Kind: yaml.MappingNode}
		} else {
			spec = clone(spec)
		}
		set(spec, "parser", str(f.Parser))
		find := &yaml.Node{Kind: yaml.MappingNode}
		for _, r := range f.Find {
			value := keep(before, r)
			if r.IfValue == "" {
				set(find, r.Key, value)
				continue
			}
			cond := get(find, r.Key)
			if cond == nil || cond.Kind != yaml.MappingNode {
				cond = &yaml.Node{Kind: yaml.MappingNode}
				set(find, r.Key, cond)
			}
			set(cond, r.IfValue, value)
		}
		set(spec, "find", find)
		out.Content = append(out.Content, str(f.Path), spec)
	}
	return setEmbedded(cfg, "files", out, asString)
}

// keep returns a replacement's value, keeping the egg's own node (a
// number or a boolean, say) when the value didn't change.
func keep(find *yaml.Node, r Find) *yaml.Node {
	n := get(find, r.Key)
	if r.IfValue != "" {
		n = get(n, r.IfValue)
	}
	if n != nil && n.Kind == yaml.ScalarNode && n.Value == r.Value {
		return n
	}
	return str(r.Value)
}

func writeDone(cfg *yaml.Node, done []string, ptero bool) error {
	startup, asString := embeddedNode(cfg, "startup", ptero)
	startup = clone(startup)
	if len(done) == 1 && scalar(get(startup, "done")) != "" {
		set(startup, "done", str(done[0]))
	} else {
		set(startup, "done", strs(done))
	}
	return setEmbedded(cfg, "startup", startup, asString)
}

func writeVariables(root *yaml.Node, vars []eggs.Variable, ptero bool) {
	old := get(root, "variables")
	used := map[*yaml.Node]bool{}
	out := &yaml.Node{Kind: yaml.SequenceNode}
	for _, v := range vars {
		var n *yaml.Node
		if old != nil && old.Kind == yaml.SequenceNode {
			for _, o := range old.Content {
				if !used[o] && scalar(get(o, "env_variable")) == v.Env {
					used[o] = true
					n = clone(o)
					break
				}
			}
		}
		listRules := !ptero
		if n == nil {
			n = &yaml.Node{Kind: yaml.MappingNode}
		} else if r := get(n, "rules"); r != nil {
			listRules = r.Kind == yaml.SequenceNode
		}
		set(n, "name", str(v.Name))
		set(n, "description", str(v.Description))
		set(n, "env_variable", str(v.Env))
		set(n, "default_value", str(v.Default))
		set(n, "user_viewable", boolean(v.UserViewable))
		set(n, "user_editable", boolean(v.UserEditable))
		if listRules {
			set(n, "rules", strs(v.Rules))
		} else {
			set(n, "rules", str(strings.Join(v.Rules, "|")))
		}
		out.Content = append(out.Content, n)
	}
	set(root, "variables", out)
}

func clone(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	c := *n
	c.Content = make([]*yaml.Node, len(n.Content))
	for i, k := range n.Content {
		c.Content[i] = clone(k)
	}
	return &c
}

func str(s string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s} }

func boolean(b bool) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: strconv.FormatBool(b)}
}

func strs(v []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode}
	for _, s := range v {
		n.Content = append(n.Content, str(s))
	}
	return n
}

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

func scalar(n *yaml.Node) string {
	if n == nil || n.Kind != yaml.ScalarNode || n.Tag == "!!null" {
		return ""
	}
	return n.Value
}

func set(n *yaml.Node, key string, v *yaml.Node) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content[i+1] = v
			return
		}
	}
	n.Content = append(n.Content, str(key), v)
}

func del(n *yaml.Node, key string) {
	for i := 0; i+1 < len(n.Content); i += 2 {
		if n.Content[i].Value == key {
			n.Content = append(n.Content[:i], n.Content[i+2:]...)
			return
		}
	}
}

// mapping returns the mapping under key, adding an empty one if it's
// missing (or isn't a mapping).
func mapping(n *yaml.Node, key string) *yaml.Node {
	if m := get(n, key); m != nil && m.Kind == yaml.MappingNode {
		return m
	}
	m := &yaml.Node{Kind: yaml.MappingNode}
	set(n, key, m)
	return m
}
