# Built-in egg catalog

Unmodified copies of upstream eggs, one directory each, with a `raptor.yaml`
saying where the egg came from (repository, commit, path), its license,
whether it's certified, which CPU architectures the game runs on, and how the
conformance suite tests it. See [docs/EGGS.md](../docs/EGGS.md#built-in-catalog).

All eggs here come from the [pelican-eggs](https://github.com/pelican-eggs)
repositories, under the MIT license (`LICENSE.pelican-eggs`):
Copyright (c) Michael Parker and contributors.

To update an egg, replace the file with the upstream version and update
`source.commit`; `go test ./eggs` checks every entry, and
`task e2e:conformance RUN=<id>` runs it.
