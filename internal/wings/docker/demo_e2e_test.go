//go:build e2e

package docker

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/firewall"
)

// The public demo's egress policy (#449) loads into nftables, and a
// container on the demo network reaches an allowed host but not another
// one, while the same rules without Demo leave it alone.
func TestDemoEgress(t *testing.T) {
	ctx := context.Background()
	// Both hosts must be reachable from here, or there's nothing to test.
	for _, u := range []string{"http://api.modrinth.com/", "http://example.com/"} {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		req, _ := http.NewRequestWithContext(cctx, http.MethodGet, u, nil)
		res, err := http.DefaultClient.Do(req)
		cancel()
		if err != nil {
			t.Skipf("no internet from the test machine (%s): %v", u, err)
		}
		_ = res.Body.Close()
	}
	var allow []netip.Addr
	for _, h := range []string{"api.modrinth.com"} {
		addrs, err := net.DefaultResolver.LookupNetIP(ctx, "ip4", h)
		if err != nil {
			t.Skipf("%s doesn't resolve: %v", h, err)
		}
		allow = append(allow, addrs...)
	}

	dc := newRuntime(t)
	demo := owned(t, dc, false)
	rules := firewall.Rules{
		ServerBridge: dc.nets.Server.Bridge, InstallBridge: dc.nets.Install.Bridge,
		ServerGateway: dc.nets.Server.Gateway, DNS: firewall.HostResolvers(),
		Networks: []firewall.Network{{Bridge: demo.Bridge, Gateway: demo.Gateway}},
		Demo:     &firewall.Demo{Bridge: demo.Bridge, Allow: allow, LimitInstalls: true},
	}
	if err := firewall.Apply(ctx, rules); err != nil {
		t.Fatalf("the demo's rules don't load: %v\n%s", err, rules.Render())
	}
	t.Cleanup(func() {
		rules.Demo = nil
		_ = firewall.Apply(context.Background(), rules)
	})

	sandbox := startServer(t, dc, containers.ServerSpec{Networks: []containers.Network{demo}})
	urls := map[string]string{"allowed": "http://api.modrinth.com/", "other": "http://example.com/"}
	writeFile(t, filepath.Join(sandbox.dir, "check.sh"), probeScript(urls))
	res, err := sandbox.console.Send(time.Minute, "sh /home/container/check.sh")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "sandbox", res, map[string]string{"allowed": "reachable", "other": "blocked"})

	// Without the demo's rules, the same network reaches both.
	rules.Demo = nil
	if err := firewall.Apply(ctx, rules); err != nil {
		t.Fatal(err)
	}
	res, err = sandbox.console.Send(time.Minute, "sh /home/container/check.sh")
	if err != nil {
		t.Fatal(err)
	}
	expect(t, "normal network", res, map[string]string{"allowed": "reachable", "other": "reachable"})
}
