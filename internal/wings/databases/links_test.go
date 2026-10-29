package databases

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/networks"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// linkFixture is a node with networks A and B, an instance on A, servers
// a1 and a2 on A and b1 on B, and a database made for a1.
func linkFixture(t *testing.T) (*Service, *store.DB) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s, err := New(Options{Store: db, KeyPath: filepath.Join(dir, "databases.key")})
	if err != nil {
		t.Fatal(err)
	}
	q := db.Write
	for _, n := range []struct{ id, subnet string }{{"na", "172.27.0.0/16"}, {"nb", "172.28.0.0/16"}} {
		if err := q.InsertNetwork(ctx, store.InsertNetworkParams{ID: n.id, Name: n.id, Subnet: n.subnet, CreatedAt: 1, UpdatedAt: 1}); err != nil {
			t.Fatal(err)
		}
	}
	for _, sv := range []struct{ id, net, ip string }{
		{"0199a3b2-0000-7000-8000-0000000000a1", "na", "172.27.0.11"},
		{"0199a3b2-0000-7000-8000-0000000000a2", "na", "172.27.0.12"},
		{"0199a3b2-0000-7000-8000-0000000000b1", "nb", "172.28.0.11"},
	} {
		if err := q.InsertServer(ctx, store.InsertServerParams{
			ID: sv.id, Name: sv.id, Egg: []byte("{}"), Variables: "{}", Limits: "{}", Settings: "{}",
			DesiredState: "stopped", InstallState: "installed", Networks: `["` + sv.net + `"]`,
		}); err != nil {
			t.Fatal(err)
		}
		if err := q.InsertServerAddress(ctx, store.InsertServerAddressParams{ServerID: sv.id, NetworkID: sv.net, Ip: sv.ip}); err != nil {
			t.Fatal(err)
		}
	}
	if err := q.InsertInstance(ctx, store.InsertInstanceParams{
		ID: "i1", Name: "MariaDB", Engine: MariaDB, Version: "11.8", Image: "img", RootPassword: []byte{1},
		Networks: `["na"]`, Limits: "{}", Settings: "{}", DesiredState: "running", CreatedAt: 1, UpdatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertInstanceAddress(ctx, store.InsertInstanceAddressParams{InstanceID: "i1", NetworkID: "na", Ip: "172.27.128.2"}); err != nil {
		t.Fatal(err)
	}
	sealed, err := s.sealer.seal("secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := q.InsertDatabase(ctx, store.InsertDatabaseParams{
		ID: "d1", InstanceID: "i1", Label: "lp", DbName: "s0000000000a1_lp", Username: "s0000000000a1_lp", Password: sealed, CreatedAt: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := q.InsertDatabaseLink(ctx, store.InsertDatabaseLinkParams{DatabaseID: "d1", ServerID: a1, CreatedAt: 1}); err != nil {
		t.Fatal(err)
	}
	return s, db
}

const (
	a1 = "0199a3b2-0000-7000-8000-0000000000a1"
	a2 = "0199a3b2-0000-7000-8000-0000000000a2"
	b1 = "0199a3b2-0000-7000-8000-0000000000b1"
)

// Every server on an instance's network reaches it, with a database there
// or not; a server on another network doesn't.
func TestGuardsWholeNetwork(t *testing.T) {
	s, _ := linkFixture(t)
	guards := s.Guards(context.Background())
	if len(guards) != 1 || guards[0].Bridge != networks.Bridge("na") || guards[0].Addr != netip.MustParseAddr("172.27.128.2") {
		t.Fatalf("guards: %+v", guards)
	}
	got := guards[0].Allowed
	want := []netip.Addr{netip.MustParseAddr("172.27.0.11"), netip.MustParseAddr("172.27.0.12")}
	slices.SortFunc(got, func(a, b netip.Addr) int { return a.Compare(b) })
	if !slices.Equal(got, want) {
		t.Errorf("allowed %v, want %v (and never B's 172.28.0.11)", got, want)
	}
}

// Linking: a server on the instance's network can share a database, one on
// another network can't, and two servers' databases can't share a label;
// a server that's deleted, or unlinked, leaves the database.
func TestLinks(t *testing.T) {
	ctx := context.Background()
	s, _ := linkFixture(t)

	if err := s.Link(ctx, "d1", b1); err == nil || !strings.Contains(err.Error(), "isn't on any network") {
		t.Errorf("linking a server on another network: %v", err)
	}
	if err := s.Link(ctx, "d1", a2); err != nil {
		t.Fatal(err)
	}
	if err := s.Link(ctx, "d1", a2); err != nil {
		t.Errorf("linking again: %v", err)
	}
	all, err := s.Databases(ctx, "")
	if err != nil || len(all) != 1 || len(all[0].Links) != 2 || all[0].Links[0].ServerID != a1 || all[0].DBName != "s0000000000a1_lp" || all[0].Username != all[0].DBName {
		t.Fatalf("databases: %+v, %v", all, err)
	}
	mine, err := s.ServerDatabases(ctx, a2)
	if err != nil || len(mine) != 1 || mine[0].ServerID != a2 {
		t.Fatalf("a2's databases: %+v, %v", mine, err)
	}
	env, err := s.Env(ctx, a2)
	if err != nil || !slices.Contains(env, "DB_PASSWORD=secret") || !slices.Contains(env, "DB_NAME=s0000000000a1_lp") {
		t.Errorf("a2's variables: %v, %v", env, err)
	}
	// The server-side commands reach only linked databases, and won't
	// delete one another server uses.
	if _, err := s.Password(ctx, b1, "d1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("an unlinked server's password: %v", err)
	}
	if p, err := s.Password(ctx, a2, "d1"); err != nil || p != "secret" {
		t.Errorf("a linked server's password: %q, %v", p, err)
	}
	if err := s.DeleteDatabase(ctx, a2, "d1"); !errors.Is(err, ErrShared) {
		t.Errorf("deleting a shared database from a server: %v", err)
	}
	if p, err := s.PasswordByID(ctx, "d1"); err != nil || p != "secret" {
		t.Errorf("the admin's password: %q, %v", p, err)
	}

	s.ServerDeleted(ctx, a1)
	if mine, _ := s.ServerDatabases(ctx, a1); len(mine) != 0 {
		t.Errorf("a deleted server's databases: %+v", mine)
	}
	if all, _ := s.Databases(ctx, "i1"); len(all) != 1 || len(all[0].Links) != 1 || all[0].ServerID != a2 {
		t.Errorf("after deleting a1: %+v", all)
	}
	if err := s.Unlink(ctx, "d1", a2); err != nil {
		t.Fatal(err)
	}
	if err := s.Unlink(ctx, "d1", a2); !errors.Is(err, ErrNotFound) {
		t.Errorf("unlinking twice: %v", err)
	}
	if all, _ := s.Databases(ctx, "i1"); len(all) != 1 || len(all[0].Links) != 0 || all[0].ServerID != "" {
		t.Errorf("a database no server uses: %+v", all)
	}
	if _, err := s.Databases(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("another instance's databases: %v", err)
	}
}

// An instance's own databases are named their label, and can't take a
// name Raptor keeps for servers' databases or users.
func TestInstanceDatabaseNames(t *testing.T) {
	ctx := context.Background()
	s, _ := linkFixture(t)
	for _, label := range []string{"Bad Name", "s0000000000a1_lp", "lp_ro", "stats_vw"} {
		if _, _, err := s.CreateInstanceDatabase(ctx, "i1", label); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", label, err)
		}
	}
	if _, _, err := s.CreateInstanceDatabase(ctx, "i1", "lp"); err == nil || !strings.Contains(err.Error(), "already has a database called lp") {
		t.Errorf("a label the instance has: %v", err)
	}
	server := store.Database{Label: "lp", DbName: "s0000000000a1_lp"}
	if roName(server) != "r0000000000a1_lp" || viewerName(server) != "v0000000000a1_lp" {
		t.Errorf("a server's database's users: %s, %s", roName(server), viewerName(server))
	}
	own := store.Database{Label: "stats", DbName: "stats"}
	if roName(own) != "stats_ro" || viewerName(own) != "stats_vw" {
		t.Errorf("an instance's own database's users: %s, %s", roName(own), viewerName(own))
	}
}
