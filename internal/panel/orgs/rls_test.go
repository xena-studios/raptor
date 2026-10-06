package orgs

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/xena-studios/raptor/internal/panel/paneltest"
)

// The policies on their own, with no checks in Go: what a handler that
// forgot to check membership would see.
func TestRowLevelSecurity(t *testing.T) {
	db := paneltest.NewDB(t)
	ctx := context.Background()
	var alice, bob, carol, acme, globex, node string
	err := db.QueryRow(ctx, `
		WITH a AS (INSERT INTO users (email) VALUES ('alice@example.com') RETURNING id),
		     b AS (INSERT INTO users (email) VALUES ('bob@example.com') RETURNING id),
		     c AS (INSERT INTO users (email) VALUES ('carol@example.com') RETURNING id),
		     o1 AS (INSERT INTO orgs (name) VALUES ('Acme') RETURNING id),
		     o2 AS (INSERT INTO orgs (name) VALUES ('Globex') RETURNING id)
		SELECT a.id, b.id, c.id, o1.id, o2.id FROM a, b, c, o1, o2`).Scan(&alice, &bob, &carol, &acme, &globex)
	if err != nil {
		t.Fatal(err)
	}
	// alice owns Acme with bob as a member; carol owns Globex, which has a node.
	for _, m := range [][3]string{{acme, alice, "owner"}, {acme, bob, "member"}, {globex, carol, "owner"}} {
		if _, err := db.Exec(ctx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, $3)", m[0], m[1], m[2]); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box', 'abcd1234', decode(repeat('00', 32), 'hex')) RETURNING id`, globex).Scan(&node); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO m_servers (node_id, server_id, name, version, created_at, updated_at) VALUES ($1, 's1', 'mc', 1, now(), now())`, node); err != nil {
		t.Fatal(err)
	}

	for _, o := range []string{acme, globex} {
		if _, err := db.Exec(ctx, "INSERT INTO audit_log (org_id, actor, action) VALUES ($1, 'user', 'org.create')", o); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(ctx, "INSERT INTO audit_log (user_id, actor, action) VALUES ($1, 'user', 'signin')", carol); err != nil {
		t.Fatal(err)
	}

	// as runs fn as raptor_app for user ("" for nobody), rolled back after.
	as := func(user string, fn func(tx pgx.Tx)) {
		t.Helper()
		tx, err := db.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE raptor_app"); err != nil {
			t.Fatal(err)
		}
		if user != "" {
			if _, err := tx.Exec(ctx, "SELECT set_config('raptor.user_id', $1, true)", user); err != nil {
				t.Fatal(err)
			}
		}
		fn(tx)
	}
	count := func(tx pgx.Tx, query string, args ...any) int {
		t.Helper()
		var n int
		if err := tx.QueryRow(ctx, query, args...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return n
	}
	// exec runs a statement in a savepoint and returns the rows it changed,
	// or -1 if it was refused.
	exec := func(tx pgx.Tx, query string, args ...any) int64 {
		t.Helper()
		sp, err := tx.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		tag, err := sp.Exec(ctx, query, args...)
		if err != nil {
			_ = sp.Rollback(ctx)
			return -1
		}
		_ = sp.Commit(ctx)
		return tag.RowsAffected()
	}

	as("", func(tx pgx.Tx) {
		for _, table := range []string{"orgs", "org_members", "users", "nodes"} {
			if n := count(tx, "SELECT count(*) FROM "+table); n != 0 {
				t.Errorf("nobody sees %d rows of %s", n, table)
			}
		}
	})
	as(alice, func(tx pgx.Tx) {
		if n := count(tx, "SELECT count(*) FROM orgs"); n != 1 {
			t.Errorf("alice sees %d orgs", n)
		}
		if n := count(tx, "SELECT count(*) FROM org_members WHERE org_id = $1", globex); n != 0 {
			t.Errorf("alice sees Globex's members")
		}
		if n := count(tx, "SELECT count(*) FROM users"); n != 2 {
			t.Errorf("alice sees %d users, want herself and bob", n)
		}
		if n := count(tx, "SELECT count(*) FROM nodes"); n != 0 {
			t.Errorf("alice sees Globex's node")
		}
		if n := count(tx, "SELECT count(*) FROM m_servers"); n != 0 {
			t.Errorf("alice sees Globex's servers")
		}
		if n := exec(tx, "UPDATE orgs SET name = 'pwned' WHERE id = $1", globex); n > 0 {
			t.Error("alice renamed Globex")
		}
		if n := exec(tx, "DELETE FROM org_members WHERE org_id = $1", globex); n > 0 {
			t.Error("alice removed Globex's members")
		}
		// Joining someone else's org isn't possible as raptor_app at all.
		if n := exec(tx, "INSERT INTO org_members (org_id, user_id, role) VALUES ($1, $2, 'owner')", globex, alice); n >= 0 {
			t.Error("alice joined Globex")
		}
		if n := exec(tx, "INSERT INTO org_invitations (org_id, email, role, token_hash, expires_at) VALUES ($1, 'x@example.com', 'owner', 'x', now())", globex); n >= 0 {
			t.Error("alice invited someone to Globex")
		}
		if n := exec(tx, "SELECT 1 FROM join_tokens"); n > 0 {
			t.Errorf("alice sees join tokens")
		}
		// The audit log: her org's, not Globex's or carol's account events,
		// and nothing can be changed or written for another org.
		if n := count(tx, "SELECT count(*) FROM audit_log"); n != 1 {
			t.Errorf("alice sees %d audit events, want Acme's one", n)
		}
		if n := exec(tx, "INSERT INTO audit_log (org_id, actor, actor_id, action) VALUES ($1, 'user', $2, 'x')", globex, alice); n >= 0 {
			t.Error("alice wrote to Globex's log")
		}
		if n := exec(tx, "INSERT INTO audit_log (org_id, actor, actor_id, action) VALUES ($1, 'user', $2, 'x')", acme, bob); n >= 0 {
			t.Error("alice wrote an event as bob")
		}
		if n := exec(tx, "UPDATE audit_log SET action = 'nothing'"); n >= 0 {
			t.Error("alice rewrote the log")
		}
		if n := exec(tx, "DELETE FROM audit_log"); n >= 0 {
			t.Error("alice deleted from the log")
		}
	})
	// A member can't do what owners and admins do, even in their own org.
	var acmeNode string
	if err := db.QueryRow(ctx, `INSERT INTO nodes (org_id, name, short_id, public_key) VALUES ($1, 'box2', 'efgh5678', decode(repeat('00', 32), 'hex')) RETURNING id`, acme).Scan(&acmeNode); err != nil {
		t.Fatal(err)
	}
	as(bob, func(tx pgx.Tx) {
		if n := exec(tx, "UPDATE orgs SET name = 'mine' WHERE id = $1", acme); n > 0 {
			t.Error("member renamed the org")
		}
		// Server access: a member can't grant it, least of all to themselves.
		if n := exec(tx, "INSERT INTO server_grants (org_id, user_id, node_id, server_id, permissions) VALUES ($1, $2, $3, 's1', '{power}')", acme, bob, acmeNode); n >= 0 {
			t.Error("member granted themselves access")
		}
		if n := exec(tx, "UPDATE org_members SET role = 'owner' WHERE user_id = $1", bob); n > 0 {
			t.Error("member made themselves owner")
		}
		if n := exec(tx, "DELETE FROM org_members WHERE user_id = $1", alice); n > 0 {
			t.Error("member removed the owner")
		}
		if n := exec(tx, "INSERT INTO org_invitations (org_id, email, role, token_hash, expires_at) VALUES ($1, 'x@example.com', 'member', 'x', now())", acme); n >= 0 {
			t.Error("member invited someone")
		}
		if n := count(tx, "SELECT count(*) FROM audit_log"); n != 0 {
			t.Errorf("a member sees %d audit events", n)
		}
		// Leaving is allowed.
		if n := exec(tx, "DELETE FROM org_members WHERE user_id = $1", bob); n != 1 {
			t.Errorf("member leaving: %d", n)
		}
	})
	// The owner can.
	as(alice, func(tx pgx.Tx) {
		if n := exec(tx, "INSERT INTO server_grants (org_id, user_id, node_id, server_id, permissions) VALUES ($1, $2, $3, 's1', '{power}')", acme, bob, acmeNode); n != 1 {
			t.Errorf("owner granting access: %d", n)
		}
		if n := exec(tx, "INSERT INTO server_grants (org_id, user_id, node_id, server_id, permissions) VALUES ($1, $2, $3, 's1', '{root}')", acme, bob, acmeNode); n >= 0 {
			t.Error("an unknown permission was stored")
		}
		if n := exec(tx, "UPDATE orgs SET name = 'Acme 2' WHERE id = $1", acme); n != 1 {
			t.Errorf("owner renaming: %d", n)
		}
		if n := exec(tx, "UPDATE org_members SET role = 'admin' WHERE user_id = $1 AND org_id = $2", bob, acme); n != 1 {
			t.Errorf("owner changing a role: %d", n)
		}
	})
	// Setting raptor.user_id doesn't help without it being the transaction's
	// user: the owner role (the Panel's own work) isn't limited.
	var n int
	if err := db.QueryRow(ctx, "SELECT count(*) FROM orgs").Scan(&n); err != nil || n != 2 {
		t.Errorf("the Panel itself sees %d orgs: %v", n, err)
	}
}
