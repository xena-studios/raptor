// Package eggs parses Pterodactyl and Pelican eggs into a single model and
// implements the runtime behavior eggs expect (environment, startup detection,
// stop handling). Pterodactyl Wings' behavior is the spec; see docs/EGGS.md.
package eggs

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
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

// FindRule is one key/value replacement. Value is a string or a nested
// structure (for match/replace_with rules); interpreted by the config parsers.
type FindRule struct {
	Key   string
	Value any
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
	Backup struct {
		Pre     []string `yaml:"pre"`
		Post    []string `yaml:"post"`
		WaitFor string   `yaml:"wait_for"`
	} `yaml:"backup"`
	Health struct {
		Type     string `yaml:"type"`
		Protocol string `yaml:"protocol"`
	} `yaml:"health"`
	Players struct {
		Query string `yaml:"query"`
	} `yaml:"players"`
	Certified bool `yaml:"certified"`
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
	root, err := decode(data)
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

	// Images: PTDL_v1 has a single "image"; later formats have an ordered map.
	if n := get(root, "docker_images"); n != nil && n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			e.Images = append(e.Images, Image{Name: n.Content[i].Value, Ref: n.Content[i+1].Value})
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
	}

	return e, nil
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
				var v any
				if err := find.Content[j+1].Decode(&v); err != nil {
					return nil, fmt.Errorf("egg: config file %q: %w", f.Path, err)
				}
				f.Find = append(f.Find, FindRule{Key: find.Content[j].Value, Value: v})
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
		return strings_(n)
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
		out = append(out, p)
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
	d, err := decode([]byte(n.Value))
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
