package firewall

import (
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
)

// Database instances on the owner's networks answer only the servers with
// a database on them (docs/DATABASES.md#isolation): on each network, the
// instance's fixed address drops connections from any other address, and
// its IPv6 address (Docker's, not fixed) refuses them all, so clients fall
// back to IPv4 at once instead of waiting.

// Guard is one instance on one network.
type Guard struct {
	Bridge  string
	Addr    netip.Addr   // its fixed IPv4 address there
	IPv6    netip.Addr   // its IPv6 address there, if the network has IPv6
	Allowed []netip.Addr // servers with a database on it, by their address there
	// External: it has a port on the node (External), and connections
	// Docker translated to it from there pass. Who may make them is
	// decided before the translation.
	External bool
}

// External is an instance's port on the node, and the addresses allowed
// to connect to it (docs/DATABASES.md#external-access). Connections to
// the port from anywhere else, the node's own containers included, are
// dropped before Docker translates them to the instance.
type External struct {
	Port  int
	Allow []netip.Prefix
}

const (
	whyGuard         = "A database instance answers only the servers with a database on it"
	whyGuardV6       = "Database instances are reached over IPv4, where they're guarded"
	whyGuardExternal = "Connections through a database instance's external port, from addresses its allowlist lets in"
	whyExternal      = "A database instance's external port is reachable only from the addresses you allowed"
)

func (r Rules) renderGuards(rule func(why, format string, a ...any)) {
	for _, g := range r.Guards {
		if !g.Addr.IsValid() {
			continue
		}
		if g.External {
			rule(whyGuardExternal, "oifname %q ip daddr %s ct status dnat accept", g.Bridge, g.Addr)
			if g.IPv6.IsValid() {
				rule(whyGuardExternal, "oifname %q ip6 daddr %s ct status dnat accept", g.Bridge, g.IPv6)
			}
		}
		v4, _ := split(g.Allowed)
		if len(v4) > 0 {
			rule(whyGuard, "oifname %q ip daddr %s ip saddr != { %s } drop", g.Bridge, g.Addr, join(v4))
		} else {
			rule(whyGuard, "oifname %q ip daddr %s drop", g.Bridge, g.Addr)
		}
		if g.IPv6.IsValid() {
			rule(whyGuardV6, "oifname %q ip6 daddr %s reject with icmpx type admin-prohibited", g.Bridge, g.IPv6)
		}
	}
}

// renderExternal drops connections to instances' external ports from
// addresses not allowed, in prerouting before Docker's translation, when
// they're for the node itself.
func (r Rules) renderExternal(rule func(why, format string, a ...any)) {
	for _, e := range r.External {
		v4, v6 := splitPrefixes(e.Allow)
		match := fmt.Sprintf("fib daddr type local tcp dport %d", e.Port)
		if len(v4) > 0 {
			rule(whyExternal, "%s ip saddr != { %s } drop", match, join(v4))
		} else {
			rule(whyExternal, "%s meta nfproto ipv4 drop", match)
		}
		if len(v6) > 0 {
			rule(whyExternal, "%s ip6 saddr != { %s } drop", match, join(v6))
		} else {
			rule(whyExternal, "%s meta nfproto ipv6 drop", match)
		}
	}
}

// bridgeFilter is the switch that sends traffic between containers on one
// bridge through the firewall; without it, guards see nothing. Docker
// turns it on; EnsureBridgeFilter makes sure.
const bridgeFilter = "/proc/sys/net/bridge/bridge-nf-call-iptables"

// EnsureBridgeFilter loads br_netfilter and turns bridge filtering on, if
// either isn't already.
func EnsureBridgeFilter(ctx context.Context) error {
	if b, err := os.ReadFile(bridgeFilter); err == nil && strings.TrimSpace(string(b)) == "1" {
		return nil
	}
	if out, err := exec.CommandContext(ctx, "modprobe", "br_netfilter").CombinedOutput(); err != nil {
		return fmt.Errorf("modprobe br_netfilter: %w: %s", err, bytes.TrimSpace(out))
	}
	return os.WriteFile(bridgeFilter, []byte("1\n"), 0o644) //nolint:gosec // a sysctl
}
