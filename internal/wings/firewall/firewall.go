// Package firewall manages Wings' nftables rules (docs/WINGS.md#firewall).
//
// The rules live in their own table, "inet raptor", with base chains that run
// just before Docker's. Nftables evaluates every table on a hook, so a drop
// here is final, and Docker flushing or rewriting its own rules never touches
// ours. The whole table is replaced atomically on every Apply.
package firewall

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"slices"
	"strings"
)

// Table is the nftables table Wings owns.
const Table = "raptor"

// Metadata endpoints of cloud providers, which can hand out credentials.
var metadata = []netip.Addr{
	netip.MustParseAddr("169.254.169.254"),
	netip.MustParseAddr("fd00:ec2::254"),
}

// Ranges install containers can't reach: the host's own and private
// networks, link-local (including the metadata endpoint), multicast, and
// reserved ranges.
var (
	blockedV4 = []string{
		"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16",
		"172.16.0.0/12", "192.0.0.0/24", "192.168.0.0/16", "198.18.0.0/15", "224.0.0.0/3",
	}
	blockedV6 = []string{"::1/128", "fc00::/7", "fe80::/10", "ff00::/8"}
)

// Rules is the input to the rule set.
type Rules struct {
	ServerBridge  string // interface of the server network
	InstallBridge string // interface of the install network
	// DNS are the host's upstream resolvers. Install containers may reach
	// them on port 53 even when they're in a blocked range (a home router,
	// a provider's private resolver), or name resolution would fail.
	DNS []netip.Addr
	// InstallAllow are private ranges the owner lets install containers reach
	// (e.g. a local package mirror).
	InstallAllow []netip.Prefix
	// Cgroup, if set, is the cgroup (relative to the root) all Raptor
	// containers run in. It's used to block the metadata endpoint for
	// host-networked servers, which don't go through a bridge.
	Cgroup string
}

// Render returns the nftables script for the rules. Loading it replaces the
// table atomically: the empty "table" line creates it if it's missing so the
// "delete" can't fail.
func (r Rules) Render() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }

	v4, v6 := split(metadata)
	w("table inet %s", Table)
	w("delete table inet %s", Table)
	w("table inet %s {", Table)

	w("\tchain forward {")
	w("\t\ttype filter hook forward priority filter - 1; policy accept;")
	w("\t\tiifname { %q, %q } ip daddr { %s } drop", r.ServerBridge, r.InstallBridge, join(v4))
	w("\t\tiifname { %q, %q } ip6 daddr { %s } drop", r.ServerBridge, r.InstallBridge, join(v6))
	w("\t\tiifname %q jump install_out", r.InstallBridge)
	w("\t}")

	w("\tchain install_out {")
	dns4, dns6 := split(r.DNS)
	if len(dns4) > 0 {
		w("\t\tip daddr { %s } meta l4proto { tcp, udp } th dport 53 accept", join(dns4))
	}
	if len(dns6) > 0 {
		w("\t\tip6 daddr { %s } meta l4proto { tcp, udp } th dport 53 accept", join(dns6))
	}
	allow4, allow6 := splitPrefixes(r.InstallAllow)
	if len(allow4) > 0 {
		w("\t\tip daddr { %s } accept", join(allow4))
	}
	if len(allow6) > 0 {
		w("\t\tip6 daddr { %s } accept", join(allow6))
	}
	w("\t\tip daddr { %s } drop", strings.Join(blockedV4, ", "))
	w("\t\tip6 daddr { %s } drop", strings.Join(blockedV6, ", "))
	w("\t}")

	// Traffic from install containers to the host itself never reaches the
	// forward hook. Nothing on the host is theirs to talk to (DNS goes
	// through Docker's resolver inside the container).
	w("\tchain input {")
	w("\t\ttype filter hook input priority filter - 1; policy accept;")
	w("\t\tiifname %q ct state established,related accept", r.InstallBridge)
	w("\t\tiifname %q drop", r.InstallBridge)
	w("\t}")

	if r.Cgroup != "" {
		w("\tchain output {")
		w("\t\ttype filter hook output priority filter - 1; policy accept;")
		w("\t\tsocket cgroupv2 level %d %q ip daddr { %s } drop", strings.Count(r.Cgroup, "/")+1, r.Cgroup, join(v4))
		w("\t\tsocket cgroupv2 level %d %q ip6 daddr { %s } drop", strings.Count(r.Cgroup, "/")+1, r.Cgroup, join(v6))
		w("\t}")
	}

	w("}")
	return b.String()
}

// Apply loads the rules, replacing any previous version of the table.
func Apply(ctx context.Context, r Rules) error {
	cmd := exec.CommandContext(ctx, "nft", "-f", "-")
	cmd.Stdin = strings.NewReader(r.Render())
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// Present reports whether the table is loaded. Something like
// `nft flush ruleset` (the default Debian nftables.service does that on
// restart) removes it, and Wings then reapplies it.
func Present(ctx context.Context) bool {
	return exec.CommandContext(ctx, "nft", "list", "table", "inet", Table).Run() == nil
}

// Remove deletes the table.
func Remove(ctx context.Context) error {
	if !Present(ctx) {
		return nil
	}
	if out, err := exec.CommandContext(ctx, "nft", "delete", "table", "inet", Table).CombinedOutput(); err != nil {
		return fmt.Errorf("nft: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// HostResolvers returns the upstream DNS servers Docker forwards container
// queries to: the nameservers in /etc/resolv.conf, or systemd-resolved's
// upstreams when resolv.conf only points at its local stub. Loopback
// addresses are dropped; containers never reach them directly.
func HostResolvers() []netip.Addr {
	addrs := nameservers("/etc/resolv.conf")
	if len(addrs) == 0 || slices.ContainsFunc(addrs, netip.Addr.IsLoopback) {
		addrs = append(addrs, nameservers("/run/systemd/resolve/resolv.conf")...)
	}
	var out []netip.Addr
	for _, a := range addrs {
		if !a.IsLoopback() && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	return out
}

func nameservers(path string) []netip.Addr {
	f, err := os.Open(path) //nolint:gosec // fixed system paths
	if err != nil {
		return nil
	}
	defer func() { _ = f.Close() }()
	var out []netip.Addr
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" {
			// Strip an IPv6 zone ("fe80::1%eth0"): nft can't match on it.
			if a, err := netip.ParseAddr(fields[1]); err == nil {
				out = append(out, a.WithZone(""))
			}
		}
	}
	return out
}

func split(addrs []netip.Addr) (v4, v6 []string) {
	for _, a := range addrs {
		if a.Unmap().Is4() {
			v4 = append(v4, a.Unmap().String())
		} else {
			v6 = append(v6, a.String())
		}
	}
	return v4, v6
}

// splitPrefixes drops prefixes covered by another one, because nftables
// rejects overlapping intervals in a set.
func splitPrefixes(ps []netip.Prefix) (v4, v6 []string) {
	var kept []netip.Prefix
	for _, p := range ps {
		p = p.Masked()
		covered := slices.ContainsFunc(ps, func(q netip.Prefix) bool {
			q = q.Masked()
			return q.Bits() < p.Bits() && q.Contains(p.Addr())
		})
		if !covered && !slices.Contains(kept, p) {
			kept = append(kept, p)
		}
	}
	for _, p := range kept {
		if p.Addr().Is4() {
			v4 = append(v4, p.String())
		} else {
			v6 = append(v6, p.String())
		}
	}
	return v4, v6
}

func join(s []string) string { return strings.Join(s, ", ") }
