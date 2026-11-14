// Package library is Raptor's egg library: eggs for every game and app
// people host, kept in this repository in the catalog's layout
// (<category>/<egg>/ with the egg, a raptor.yaml, and an icon.png) and
// imported into a Panel's official library by hand, never built into the
// Panel (docs/EGGS.md#egg-library). Only tests and tools import this
// package, so the eggs never end up in Raptor's binaries.
package library

import (
	"embed"

	catalog "github.com/xena-studios/raptor/eggs"
)

//go:embed *
var files embed.FS

// All returns every egg in the library, validated, sorted by ID.
func All() ([]catalog.Entry, error) { return catalog.LoadFS(files, "library") }
