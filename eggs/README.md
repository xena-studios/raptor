# Built-in egg catalog

One directory per egg, with a `raptor.yaml` saying where the egg came from,
its licence, whether it's certified, which CPU architectures it runs on, and
how the conformance suite tests it. See
[docs/EGGS.md](../docs/EGGS.md#built-in-catalog).

Today it holds only Raptor's own test egg, `generic/raptor-test`: not a
game, but the quickest check that a node works. Raptor's own eggs are
versioned with this repository (`source.repo: xena-studios/raptor`, no
commit). Eggs copied from elsewhere are unmodified upstream files at a
pinned commit, with their licence kept beside them.

An `icon.png` beside an egg is its icon, instead of the one in the egg
file, if any.

`go test ./eggs` checks every entry, and `task e2e:conformance RUN=<id>`
runs one.
