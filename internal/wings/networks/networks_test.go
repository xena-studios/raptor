package networks

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// fakeRuntime is Docker's networks in memory, picking subnets from
// 172.30.0.0/16 up.
type fakeRuntime struct {
	nets  map[string]containers.NetworkSpec
	inUse map[string]bool
	next  byte
}

func (f *fakeRuntime) EnsureNetwork(_ context.Context, s containers.NetworkSpec) (containers.Network, error) {
	if cur, ok := f.nets[s.Name]; ok && cur != s && (s.Subnet.IsValid() || !sameButSubnet(cur, s)) {
		if f.inUse[s.Name] {
			return containers.Network{}, containers.ErrNetworkInUse
		}
	}
	if !s.Subnet.IsValid() {
		if cur, ok := f.nets[s.Name]; ok {
			s.Subnet = cur.Subnet
		} else {
			f.next++
			s.Subnet = netip.PrefixFrom(netip.AddrFrom4([4]byte{172, 29 + f.next, 0, 0}), 16)
		}
	}
	f.nets[s.Name] = s
	return containers.Network{Name: s.Name, Bridge: s.Bridge, Subnet: s.Subnet, Gateway: s.Subnet.Addr().Next()}, nil
}

func sameButSubnet(a, b containers.NetworkSpec) bool {
	a.Subnet, b.Subnet = netip.Prefix{}, netip.Prefix{}
	return a == b
}

func (f *fakeRuntime) RemoveNetwork(_ context.Context, name string) error {
	if f.inUse[name] {
		return containers.ErrNetworkInUse
	}
	delete(f.nets, name)
	return nil
}

func (f *fakeRuntime) HostNetworks(context.Context) ([]containers.HostNetwork, error) {
	return nil, nil
}

func setup(t *testing.T) (*Service, *fakeRuntime, *store.DB, *[][]firewall.Network) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	rt := &fakeRuntime{nets: map[string]containers.NetworkSpec{}, inUse: map[string]bool{}}
	var reloads [][]firewall.Network
	s := New(Options{
		Store: db, Runtime: rt,
		Reserved: []netip.Prefix{netip.MustParsePrefix("172.29.0.0/16")},
		Changed: func(_ context.Context, n []firewall.Network) error {
			reloads = append(reloads, n)
			return nil
		},
	})
	return s, rt, db, &reloads
}

func TestSaveAndDelete(t *testing.T) {
	ctx := context.Background()
	s, rt, db, reloads := setup(t)

	a, err := s.Save(ctx, Network{Name: "Community A", Internet: true, HostAccess: true})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.Subnet != "172.30.0.0/16" || a.Gateway != "172.30.0.1" {
		t.Fatalf("created %+v", a)
	}
	if _, ok := rt.nets[DockerName(a.ID)]; !ok {
		t.Fatal("not created on Docker")
	}
	if len(Bridge(a.ID)) > 15 {
		t.Errorf("bridge name %q is too long for Linux", Bridge(a.ID))
	}
	if len(*reloads) != 1 || len((*reloads)[0]) != 1 || (*reloads)[0][0].Bridge != Bridge(a.ID) || !(*reloads)[0][0].HostAccess {
		t.Errorf("firewall reloads %+v", *reloads)
	}

	// Names are unique, whatever their case.
	if _, err := s.Save(ctx, Network{Name: "community a"}); !errors.Is(err, ErrInvalid) {
		t.Errorf("duplicate name: %v", err)
	}
	// Subnets can't overlap another network's or Raptor's own.
	for _, sub := range []string{"172.30.4.0/24", "172.29.0.0/20"} {
		if _, err := s.Save(ctx, Network{Name: "B", Subnet: sub}); !errors.Is(err, ErrInvalid) {
			t.Errorf("subnet %s: %v", sub, err)
		}
	}
	for _, bad := range []Network{
		{Name: ""},
		{Name: strings.Repeat("x", 41)},
		{Name: "x", Subnet: "8.8.8.0/24"},
		{Name: "x", Subnet: "10.0.0.0/8"},
		{Name: "x", Subnet: "fd00::/64"},
		{Name: "x", MTU: 100},
	} {
		if _, err := s.Save(ctx, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}

	b, err := s.Save(ctx, Network{Name: "B", Subnet: "10.210.0.0/24", HostAccess: false})
	if err != nil {
		t.Fatal(err)
	}
	if !rt.nets[DockerName(b.ID)].Internal {
		t.Error("internet access off isn't an internal network")
	}

	// A server on A: A can't be deleted, and its settings that recreate
	// it can't change while it runs.
	if err := db.Write.InsertServer(ctx, store.InsertServerParams{
		ID: "s1", Name: "Lobby", Egg: []byte("{}"), EggHash: "x", Image: "x", Startup: "x", Variables: "{}",
		Limits: "{}", Settings: "{}", DesiredState: "stopped", InstallState: "installed", Networks: `["` + a.ID + `"]`,
	}); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || !slices.Equal(list[0].Servers, []string{"s1"}) || len(list[1].Servers) != 0 {
		t.Fatalf("list %+v", list)
	}
	if err := s.Delete(ctx, a.ID); !errors.Is(err, ErrInUse) {
		t.Errorf("delete with a server on it: %v", err)
	}
	rt.inUse[DockerName(a.ID)] = true
	a.Internet = false
	if _, err := s.Save(ctx, a); !errors.Is(err, ErrInUse) {
		t.Errorf("recreate while running: %v", err)
	}
	// Renaming and host access don't recreate it.
	a.Internet, a.Name, a.HostAccess = true, "Renamed", false
	a.Subnet = ""
	got, err := s.Save(ctx, a)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Renamed" || got.Subnet != "172.30.0.0/16" || got.HostAccess {
		t.Errorf("updated %+v", got)
	}

	if err := s.Delete(ctx, b.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := rt.nets[DockerName(b.ID)]; ok {
		t.Error("still on Docker")
	}
	if err := s.Delete(ctx, b.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted twice: %v", err)
	}
}

func TestResolveAndReconcile(t *testing.T) {
	ctx := context.Background()
	s, rt, db, _ := setup(t)
	a, err := s.Save(ctx, Network{Name: "A", IPv6: true, MTU: 1400})
	if err != nil {
		t.Fatal(err)
	}
	spec := rt.nets[DockerName(a.ID)]
	if !spec.IPv6 || spec.IPv6Subnet.Bits() != 64 || spec.IPv6Subnet.Addr().As16()[0] != 0xfd || spec.MTU != 1400 {
		t.Errorf("spec %+v", spec)
	}
	if a.IPv6Subnet != spec.IPv6Subnet.String() {
		t.Errorf("reported IPv6 range %q, created %s", a.IPv6Subnet, spec.IPv6Subnet)
	}

	// After a restart, with Docker's network gone, Reconcile makes it again.
	rt.nets = map[string]containers.NetworkSpec{}
	s2 := New(Options{Store: db, Runtime: rt})
	if err := s2.Reconcile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := rt.nets[DockerName(a.ID)].Subnet.String(); got != a.Subnet {
		t.Errorf("recreated with %s, want %s", got, a.Subnet)
	}
	nets, err := s2.Resolve(ctx, []string{a.ID})
	if err != nil || len(nets) != 1 || nets[0].Name != DockerName(a.ID) {
		t.Fatalf("resolve: %v %v", nets, err)
	}
	if _, err := s2.Resolve(ctx, []string{"nope"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown network: %v", err)
	}
	if fw := s2.Firewall(ctx); len(fw) != 1 || fw[0].Gateway.String() != a.Gateway {
		t.Errorf("firewall %+v", fw)
	}
}
