package eggs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
)

// CodeFingerprint identifies what an egg runs, and nothing else: its
// images, startup commands, install script (with its container and
// entrypoint), the rules its variables are checked against, the config
// files Wings rewrites, and how it stops. Two eggs with the same
// fingerprint run the same code, whatever their names, descriptions, icons,
// or x-raptor settings say.
//
// The public demo's node uses it (docs/SECURITY-MODEL.md#the-demo-key): it
// creates sandboxes under the Panel's demo key only from eggs whose
// fingerprint is one of the library's, which Wings computes from the
// library built into it, so the Panel can't slip in an egg of its own.
// It's "" (matching nothing) for an egg it can't encode.
func CodeFingerprint(e *Egg) string {
	type variable struct {
		Env   string   `json:"env"`
		Rules []string `json:"rules"`
	}
	type rule struct {
		Key     string `json:"key"`
		IfValue string `json:"if_value"`
		Value   any    `json:"value"`
	}
	type file struct {
		Path   string `json:"path"`
		Parser string `json:"parser"`
		Find   []rule `json:"find"`
	}
	var c struct {
		Images     []string   `json:"images"`
		Startup    []string   `json:"startup"`
		Script     string     `json:"script"`
		Container  string     `json:"container"`
		Entrypoint string     `json:"entrypoint"`
		Variables  []variable `json:"variables"`
		Files      []file     `json:"files"`
		Stop       string     `json:"stop"`
		Signal     int        `json:"signal"`
		Features   []string   `json:"features"`
	}
	for _, i := range e.Images {
		c.Images = append(c.Images, i.Ref)
	}
	slices.Sort(c.Images)
	for _, s := range e.Startup {
		c.Startup = append(c.Startup, s.Command)
	}
	c.Script, c.Container, c.Entrypoint = e.Install.Script, e.Install.Container, e.Install.Entrypoint
	for _, v := range e.Variables {
		c.Variables = append(c.Variables, variable{Env: v.Env, Rules: v.Rules})
	}
	slices.SortFunc(c.Variables, func(a, b variable) int { return strings.Compare(a.Env, b.Env) })
	for _, f := range e.Config.Files {
		out := file{Path: f.Path, Parser: f.Parser}
		for _, r := range f.Find {
			out.Find = append(out.Find, rule(r))
		}
		c.Files = append(c.Files, out)
	}
	c.Stop, c.Signal = e.Config.Stop.Command, int(e.Config.Stop.Signal)
	c.Features = slices.Sorted(slices.Values(e.Features))
	b, err := json.Marshal(c)
	if err != nil {
		return "" // matches no egg
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
