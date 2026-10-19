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
}

const (
	whyGuard   = "A database instance answers only the servers with a database on it"
	whyGuardV6 = "Database instances are reached over IPv4, where they're guarded"
)

func (r Rules) renderGuards(rule func(why, format string, a ...any)) {
	for _, g := range r.Guards {
		if !g.Addr.IsValid() {
			continue
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
