// Package library is Raptor's egg library: Raptor's eggs for the games and
// apps people host, plus its own test server, one folder each
// (<category>/<egg>/ with the egg, a raptor.yaml, and an icon.png). The
// folder is the egg's ID. The Panel embeds the library and seeds it into
// its official egg library on every start (internal/panel/catalog,
// docs/EGGS.md#egg-library); the conformance suite runs the same files
// through Wings.
package library

import "embed"

//go:embed */*/*
var files embed.FS

// All returns every egg in the library, validated, sorted by ID.
func All() ([]Entry, error) { return loadFS(files) }
