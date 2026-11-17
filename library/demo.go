package library

import (
	"embed"
	"fmt"
	"io/fs"
	"path"
	"sort"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/shared/demo"
)

// codeFiles is the library without its icons: the egg files and their
// raptor.yaml, which is all Wings needs to know the library's eggs. A
// binary that uses only this (Wings) doesn't carry the icons.
//
//go:embed */*/*.json */*/*.yaml
var codeFiles embed.FS

// DemoEgg is a library egg as the public demo sees it (#449,
// docs/PANEL.md#sandboxes).
type DemoEgg struct {
	ID string
	// Fingerprint is the egg's eggs.CodeFingerprint: what it runs.
	Fingerprint string
	// Fits is whether it fits a sandbox (demo.Fits); Why says why not.
	Fits bool
	Why  string
	Arch []string
	// Test is how the conformance suite runs it, which is how a sandbox
	// is made from it: its variables, image, and EULA.
	Test Test
}

// DemoEggs returns the library's eggs for the demo, internal ones left
// out, sorted by ID. It reads only codeFiles, so Wings can call it.
func DemoEggs() ([]DemoEgg, error) { return demoEggs(codeFiles) }

func demoEggs(fsys fs.FS) ([]DemoEgg, error) {
	metas, err := fs.Glob(fsys, "*/*/raptor.yaml")
	if err != nil {
		return nil, err
	}
	out := make([]DemoEgg, 0, len(metas))
	for _, m := range metas {
		e, err := load(fsys, path.Dir(m))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path.Dir(m), err)
		}
		if e.Internal {
			continue
		}
		file, err := e.ServerEgg()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.ID, err)
		}
		parsed, err := eggs.Parse(file)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.ID, err)
		}
		d := DemoEgg{ID: e.ID, Fingerprint: eggs.CodeFingerprint(parsed), Arch: e.Arch, Test: e.Test}
		d.Fits, d.Why = demo.Fits(string(e.Test.Tier), e.Test.MemoryMiB)
		out = append(out, d)
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}
