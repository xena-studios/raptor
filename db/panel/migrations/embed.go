// Package migrations embeds the Panel's Postgres migrations.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
