//go:build e2e

package docker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/firewall"
)

// probeScript checks each URL from inside a container, as checkScript does.
func probeScript(urls map[string]string) string {
	keys := make([]string, 0, len(urls))
	for k := range urls {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(`
check() {
  out=$(wget -q -T 4 -O /dev/null "$2" 2>&1)
  if [ $? -eq 0 ] || echo "$out" | grep -q "server returned error"; then
    echo "RESULT $1 reachable"
  else
    echo "RESULT $1 blocked"
  fi
}
`)
	for _, k := range keys {
		b.WriteString("check " + k + " " + urls[k] + "\n")
	}
	b.WriteString("echo \"RESULT done done\"\n")
	return b.String()
}

// owned creates one of the owner's networks for a test.
func owned(t *testing.T, dc *Client, internal bool) containers.Network {
	t.Helper()
	sfx := randomSuffix(t)[:8]
	spec := containers.NetworkSpec{Name: "raptor_net_e2e" + sfx, Bridge: "rne2e" + sfx, Internal: internal}
	n, err := dc.EnsureNetwork(context.Background(), spec)
	if err != nil {
		t.Fatalf("ensure network: %v", err)
	}
	// Registered before any container on it, so it's removed after them.
	t.Cleanup(func() {
		if err := dc.RemoveNetwork(context.Background(), spec.Name); err != nil {
			t.Errorf("remove network: %v", err)
		}
	})
	again, err := dc.EnsureNetwork(context.Background(), spec)
	if err != nil || again != n {
		t.Fatalf("ensure again: %+v %v, want %+v", again, err, n)
	}
	return n
}

func TestRuntimeNetworks(t *testing.T) {
	ctx := context.Background()
	dc := newRuntime(t)
	a := owned(t, dc, false)
	b := owned(t, dc, false)
	c := owned(t, dc, true)

	rules := firewall.Rules{
		ServerBridge: dc.nets.Server.Bridge, InstallBridge: dc.nets.Install.Bridge,
		ServerGateway: dc.nets.Server.Gateway, DNS: firewall.HostResolvers(),
		Networks: []firewall.Network{
			{Bridge: a.Bridge, Gateway: a.Gateway, HostAccess: true},
			{Bridge: b.Bridge, Gateway: b.Gateway},
			{Bridge: c.Bridge, Gateway: c.Gateway, HostAccess: true},
		},
	}
	if dc.nets.CgroupParent != "" {
		rules.Cgroup = dc.nets.CgroupParent
	}
	if err := firewall.Apply(ctx, rules); err != nil {
		t.Fatalf("firewall: %v", err)
	}

	// A web server on the host.
	hostLn, err := net.Listen("tcp", "0.0.0.0:0") //nolint:noctx // test listener
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })} //nolint:gosec // test server
	go func() { _ = srv.Serve(hostLn) }()
	t.Cleanup(func() { _ = srv.Close() })

	// The target, on network A: one port published to the world, one on
	// 127.0.0.1 (A's gateway).
	public, private := freePort(t), freePort(t)
	target := startServer(t, dc, containers.ServerSpec{
		Networks: []containers.Network{a},
		Aliases:  []string{"target.raptor"},
		Ports:    []containers.Port{{Port: public}, {IP: "127.0.0.1", Port: private}},
	})
	for _, p := range []int{public, private} {
		if _, err := target.console.Send(0, "httpd -f -p "+strconv.Itoa(p)+" -h /home/container &"); err != nil {
			t.Fatal(err)
		}
	}
	targetIP := endpointIP(t, dc, target.id, a.Name)
	waitHTTP(t, net.JoinHostPort(targetIP, strconv.Itoa(public)))
	bound := portHosts(t, dc, target.id)
	if got := bound[strconv.Itoa(private)+"/tcp"]; got != a.Gateway.String() {
		t.Errorf("127.0.0.1 allocation bound to %q, want network A's gateway %s", got, a.Gateway)
	}

	host := hostIP(t)
	urls := map[string]string{
		"internet":    "http://deb.debian.org/debian/",
		"host":        "http://" + host + ":" + strconv.Itoa(hostLn.Addr().(*net.TCPAddr).Port) + "/",
		"direct":      "http://" + targetIP + ":" + strconv.Itoa(public) + "/",
		"alias":       "http://target.raptor:" + strconv.Itoa(public) + "/",
		"published":   "http://" + host + ":" + strconv.Itoa(public) + "/",
		"gateway":     "http://" + a.Gateway.String() + ":" + strconv.Itoa(private) + "/",
		"default_gw":  "http://" + dc.nets.Server.Gateway.String() + ":" + strconv.Itoa(hostLn.Addr().(*net.TCPAddr).Port) + "/",
		"metadata":    "http://169.254.169.254/",
	}
	script := probeScript(urls)
	run := func(who string, spec containers.ServerSpec, want map[string]string) {
		t.Helper()
		peer := startServer(t, dc, spec)
		writeFile(t, filepath.Join(peer.dir, "check.sh"), script)
		res, err := peer.console.Send(2*time.Minute, "sh /home/container/check.sh")
		if err != nil {
			t.Fatalf("%s: %v", who, err)
		}
		expect(t, who, res, want)
	}

	// On A: everything of A's.
	run("same network", containers.ServerSpec{Networks: []containers.Network{a}}, map[string]string{
		"internet": "reachable", "host": "reachable", "direct": "reachable", "alias": "reachable",
		"published": "reachable", "gateway": "reachable", "metadata": "blocked",
	})
	// On B, which can't reach the machine: only what's published.
	run("other network", containers.ServerSpec{Networks: []containers.Network{b}}, map[string]string{
		"internet": "reachable", "host": "blocked", "direct": "blocked", "alias": "blocked",
		"published": "reachable", "gateway": "blocked", "default_gw": "blocked",
	})
	// On the server network: the same.
	run("server network", containers.ServerSpec{}, map[string]string{
		"internet": "reachable", "host": "reachable", "direct": "blocked", "alias": "blocked",
		"published": "reachable", "gateway": "blocked",
	})
	// A proxy on B and A reaches A's servers.
	run("both networks", containers.ServerSpec{Networks: []containers.Network{b, a}}, map[string]string{
		"internet": "reachable", "direct": "reachable", "alias": "reachable", "gateway": "reachable",
	})
	// No internet on an internal network.
	run("internal network", containers.ServerSpec{Networks: []containers.Network{c}}, map[string]string{
		"internet": "blocked", "direct": "blocked",
	})

	// A network with a running container on it isn't changed or removed.
	spec := containers.NetworkSpec{Name: a.Name, Bridge: a.Bridge, Subnet: a.Subnet, MTU: 1400}
	if _, err := dc.EnsureNetwork(ctx, spec); !errors.Is(err, containers.ErrNetworkInUse) {
		t.Errorf("changed while in use: %v", err)
	}
	if err := dc.RemoveNetwork(ctx, a.Name); !errors.Is(err, containers.ErrNetworkInUse) {
		t.Errorf("removed while in use: %v", err)
	}
	// A subnet that's taken is refused.
	taken := containers.NetworkSpec{Name: "raptor_net_e2etaken", Bridge: "rne2etaken", Subnet: netip.PrefixFrom(a.Subnet.Addr(), 24)}
	if _, err := dc.EnsureNetwork(ctx, taken); !errors.Is(err, ErrSubnetTaken) {
		t.Errorf("overlapping subnet: %v", err)
	}

	list, err := dc.HostNetworks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, n := range list {
		if n.Name == a.Name {
			found = n.Raptor && n.Containers >= 1
		}
	}
	if !found {
		t.Errorf("network A isn't listed as Raptor's with its containers: %+v", list)
	}
}

func endpointIP(t *testing.T, dc *Client, id, network string) string {
	t.Helper()
	insp, err := dc.api.ContainerInspect(context.Background(), id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ep := insp.Container.NetworkSettings.Networks[network]
	if ep == nil {
		t.Fatalf("container isn't on %s", network)
	}
	return ep.IPAddress.String()
}

func portHosts(t *testing.T, dc *Client, id string) map[string]string {
	t.Helper()
	insp, err := dc.api.ContainerInspect(context.Background(), id, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for port, bs := range insp.Container.HostConfig.PortBindings {
		for _, b := range bs {
			out[port.String()] = b.HostIP.String()
		}
	}
	return out
}
