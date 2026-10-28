package nodes

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

type fakeDNS struct {
	mu   sync.Mutex
	sets []string
}

func (f *fakeDNS) Set(_ context.Context, name, typ string, addr netip.Addr) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = append(f.sets, name+" "+typ+" "+addr.String())
	return nil
}

func (f *fakeDNS) Remove(_ context.Context, name, typ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sets = append(f.sets, name+" "+typ+" removed")
	return nil
}

func TestAddresses(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()
	org, _ := r.CreateOrg(ctx, "org")
	token, _ := r.CreateJoinToken(ctx, org, "")
	_, key, _ := ed25519.GenerateKey(nil)
	res, err := r.Enroll(ctx, enrollReq(t, token, key))
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeDNS{}
	a := &Addresses{Store: r, DNS: f, Domain: "raptornodes.net"}
	hello := nodelink.Hello{NodeID: res.GetNodeId()}
	name := res.GetShortId() + ".raptornodes.net"
	if !strings.HasSuffix(res.GetNodeId(), "-"+res.GetShortId()) || len(res.GetShortId()) != 12 || res.GetHostname() != name {
		t.Fatalf("short ID %q, hostname %q, for node %s", res.GetShortId(), res.GetHostname(), res.GetNodeId())
	}
	seen := func(addr string) {
		t.Helper()
		ip := netip.MustParseAddr(addr)
		id, _ := uuid.Parse(hello.NodeID)
		q := r.q()
		if ip.Is4() {
			_ = q.SetNodeIPv4(ctx, store.SetNodeIPv4Params{ID: pgUUID(id), PublicIpv4: &ip})
		} else {
			_ = q.SetNodeIPv6(ctx, store.SetNodeIPv6Params{ID: pgUUID(id), PublicIpv6: &ip})
		}
		if err := a.Sync(ctx, hello.NodeID); err != nil {
			t.Fatal(err)
		}
	}
	check := func(want ...string) {
		t.Helper()
		if got, w := strings.Join(f.sets, ", "), strings.Join(want, ", "); got != w {
			t.Fatalf("writes: %s, want %s", got, w)
		}
		f.sets = nil
	}
	// Seen over IPv6 only (as a dual-stack node's control connection was,
	// before address connections): AAAA, so the name resolves at all.
	seen("2001:db8:1::5")
	seen("2001:db8:1::5") // unchanged: nothing written
	check(name + " AAAA 2001:db8:1::5")
	// Its IPv4 address: the A record, then the AAAA goes, since game
	// ports listen on IPv4 only.
	seen("203.0.113.7")
	check(name+" A 203.0.113.7", name+" AAAA invalid IP")
	seen("2001:db8:1::6") // a new IPv6 address: still no AAAA
	seen("192.168.1.20")  // private: the public record stays
	seen("100.70.0.1")    // CGNAT: same
	check()
	seen("203.0.113.9")
	check(name + " A 203.0.113.9")

	// A removed node's records go (an invalid address removes them).
	f.sets = nil
	if err := a.Forget(ctx, hello.NodeID); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(f.sets, ", "); got != name+" A invalid IP, "+name+" AAAA invalid IP" {
		t.Errorf("forgetting: %s", got)
	}
}

func TestRecords(t *testing.T) {
	addr := func(s string) *netip.Addr {
		if s == "" {
			return nil
		}
		a := netip.MustParseAddr(s)
		return &a
	}
	str := func(a *netip.Addr) string {
		if a == nil {
			return ""
		}
		return a.String()
	}
	for _, c := range []struct {
		name                     string
		seen4, seen6, dns4, dns6 string
		wantA, wantAAAA          string
	}{
		{"nothing seen", "", "", "", "", "", ""},
		{"IPv4 only", "203.0.113.7", "", "", "", "203.0.113.7", ""},
		{"dual stack: A only", "203.0.113.7", "2001:db8::5", "", "", "203.0.113.7", ""},
		{"dual stack drops an old AAAA", "203.0.113.7", "2001:db8::5", "", "2001:db8::5", "203.0.113.7", ""},
		{"IPv4 moved", "203.0.113.9", "", "203.0.113.7", "", "203.0.113.9", ""},
		{"IPv6 only: AAAA", "", "2001:db8::5", "", "", "", "2001:db8::5"},
		{"IPv6 moved", "", "2001:db8::6", "", "2001:db8::5", "", "2001:db8::6"},
		{"CGNAT IPv4 with IPv6: AAAA", "100.70.0.1", "2001:db8::5", "", "", "", "2001:db8::5"},
		{"private IPv4 keeps the old A", "192.168.1.20", "2001:db8::5", "203.0.113.7", "", "203.0.113.7", ""},
		{"private IPv6 keeps the old AAAA", "", "fd00::1", "", "2001:db8::5", "", "2001:db8::5"},
		{"link-local IPv6 isn't published", "", "fe80::1", "", "", "", ""},
	} {
		a, aaaa := Records(addr(c.seen4), addr(c.seen6), addr(c.dns4), addr(c.dns6))
		if str(a) != c.wantA || str(aaaa) != c.wantAAAA {
			t.Errorf("%s: A %q, AAAA %q; want %q, %q", c.name, str(a), str(aaaa), c.wantA, c.wantAAAA)
		}
	}
}

func TestClientIP(t *testing.T) {
	r, _ := http.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = "172.68.1.1:443"
	r.Header.Set("CF-Connecting-IP", "203.0.113.7")
	if got := ClientIP(r, ""); got.String() != "172.68.1.1" {
		t.Errorf("without trusting the header: %s", got)
	}
	if got := ClientIP(r, "CF-Connecting-IP"); got.String() != "203.0.113.7" {
		t.Errorf("trusting the header: %s", got)
	}
	r.Header.Set("CF-Connecting-IP", "nonsense")
	if got := ClientIP(r, "CF-Connecting-IP"); got.String() != "172.68.1.1" {
		t.Errorf("bad header: %s", got)
	}
}

func TestRetire(t *testing.T) {
	r := newRegistry(t)
	ctx := context.Background()
	if _, err := r.DB.Exec(ctx, `INSERT INTO dns_retired (name, type) VALUES ('n-k7m2qx9d', 'A'), ('smp', 'CNAME'), ('_minecraft._tcp.smp', 'SRV')`); err != nil {
		t.Fatal(err)
	}
	f := &fakeDNS{}
	a := &Addresses{Store: r, DNS: f, Domain: "raptornodes.net"}
	if err := a.retire(ctx); err != nil {
		t.Fatal(err)
	}
	want := "_minecraft._tcp.smp.raptornodes.net SRV removed, n-k7m2qx9d.raptornodes.net A removed, smp.raptornodes.net CNAME removed"
	if got := strings.Join(f.sets, ", "); got != want {
		t.Errorf("removed: %s, want %s", got, want)
	}
	var left int
	if err := r.DB.QueryRow(ctx, "SELECT count(*) FROM dns_retired").Scan(&left); err != nil || left != 0 {
		t.Errorf("%d left, %v", left, err)
	}
}
