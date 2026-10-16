package store_test

import (
	"context"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Migration 45: nodes' short IDs become their IDs' last part, and the
// records of their old hostnames and of player subdomains are queued to go.
func TestNodeHostnamesMigration(t *testing.T) {
	db := paneltest.EmptyDB(t)
	ctx := context.Background()
	if err := store.MigrateTo(ctx, db, 44); err != nil {
		t.Fatal(err)
	}
	var org, node string
	if err := db.QueryRow(ctx, "INSERT INTO orgs (name) VALUES ('o') RETURNING id").Scan(&org); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key, dns_ipv4) VALUES ($1, 'box', 'k7m2qx9d', decode(repeat('00', 32), 'hex'), '203.0.113.7') RETURNING id`, org).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO subdomains (org_id, node_id, server_id, name, dns_target, dns_port) VALUES
		($1, $2, 's1', 'smp', 'n-k7m2qx9d.raptornodes.net', 25565),
		($1, $2, 's2', 'gone', '', 0)`, org, node); err != nil {
		t.Fatal(err)
	}
	if err := store.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	var short string
	var v4 *string
	if err := db.QueryRow(ctx, "SELECT short_id, dns_ipv4::text FROM nodes WHERE id = $1", node).Scan(&short, &v4); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(node, "-"+short) || len(short) != 12 || v4 != nil {
		t.Errorf("node %s: short ID %q, DNS %v", node, short, v4)
	}
	rows, err := db.Query(ctx, "SELECT name || ' ' || type FROM dns_retired ORDER BY 1")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		got = append(got, s)
	}
	if want := "_minecraft._tcp.smp SRV, n-k7m2qx9d A, smp CNAME"; strings.Join(got, ", ") != want {
		t.Errorf("retired: %v, want %s", got, want)
	}
}
