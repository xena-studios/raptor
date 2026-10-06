// Package install embeds the files `raptor bootstrap` installs on a node:
// the systemd units and Docker's scope drop-in (docs/WINGS.md#systemd-service).
package install

import "embed"

// Systemd holds systemd/raptor-wings.service, systemd/raptor-shutdown.service,
// and systemd/docker-.scope.d/10-raptor.conf.
//
//go:embed systemd
var Systemd embed.FS
