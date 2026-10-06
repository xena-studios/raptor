package nodes

import (
	"context"
	"crypto/ed25519"
	"net/http"
	"net/netip"
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
	name := "n-" + res.GetShortId() + ".raptornodes.net"
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
	seen("203.0.113.7")
	seen("203.0.113.7") // unchanged: nothing written
	seen("2001:db8:1::5")
	seen("192.168.1.20") // private: the public record stays
	seen("100.70.0.1")   // CGNAT: same
	seen("203.0.113.9")
	want := []string{name + " A 203.0.113.7", name + " AAAA 2001:db8:1::5", name + " A 203.0.113.9"}
	if len(f.sets) != len(want) {
		t.Fatalf("writes: %v", f.sets)
	}
	for i := range want {
		if f.sets[i] != want[i] {
			t.Fatalf("writes: %v, want %v", f.sets, want)
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
