// Package migrations embeds the Wings SQLite migrations.
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
