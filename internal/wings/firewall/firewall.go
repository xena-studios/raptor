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
	// Host is the node's own firewall, if the owner set one (host.go), and
	// Open what stays open when it blocks incoming connections.
	Host *HostRules
	Open []OpenPort
}

// Why each of Raptor's rules is there. Each rule carries its reason as an
// nftables comment, so the System tab can explain the machine's whole rule
// set (List). They're fixed text: nothing an owner typed goes into the rule
// set.
const (
	whyMetadata    = "Servers and installs can't reach the cloud metadata service, which hands out the machine's credentials"
	whyInstallJump = "Install containers' traffic goes through install_out"
	whyServersJump = "Connections to servers go through your rules (servers_in)"
	whyDNS         = "Install containers may ask the machine's DNS resolvers, even on a private network"
	whyInstallOK   = "Private ranges you let install containers reach"
	whyPrivate     = "Install containers can't reach the machine's own or private networks, only the internet"
	whyInstallBack = "Replies to install containers' own connections get back"
	whyInstallHost = "Install containers can't connect to the machine itself"
	whyHostJump    = "Connections to the machine go through your rules (host_in)"
	whyLocal       = "The machine's own and its containers' networks aren't the outside world: not filtered"
	whyContainers  = "Docker's container networks aren't the outside world: not filtered"
	whyEstablished = "Replies to connections already allowed get back"
	whyICMP        = "Ping and the network's control messages (IPv6 needs them to work)"
	whyDHCP        = "Answers from the network's DHCP server, so the machine keeps its address"
	whyOpen        = "Always open while incoming connections are blocked: "
	whyDropRest    = "Blocking incoming connections: anything not allowed above is dropped"
	whyServerNets  = "Traffic from servers' own networks isn't filtered here"
	whyHostMeta    = "Servers on the host's network can't reach the cloud metadata service either"
	// yoursPrefix marks one of the owner's rules ("yours 3" is the third),
	// whose note is shown from the saved rules.
	yoursPrefix = "yours "
)

// Render returns the nftables script for the rules. Loading it replaces the
// table atomically: the empty "table" line creates it if it's missing so the
// "delete" can't fail.
func (r Rules) Render() string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	// rule writes a rule with why it's there.
	rule := func(why, format string, a ...any) {
		fmt.Fprintf(&b, "\t\t"+format+" comment \"%s\"\n", append(a, comment(why))...)
	}

	v4, v6 := split(metadata)
	w("table inet %s", Table)
	w("delete table inet %s", Table)
	w("table inet %s {", Table)

	w("\tchain forward {")
	w("\t\ttype filter hook forward priority filter - 1; policy accept;")
	rule(whyMetadata, "iifname { %q, %q } ip daddr { %s } drop", r.ServerBridge, r.InstallBridge, join(v4))
	rule(whyMetadata, "iifname { %q, %q } ip6 daddr { %s } drop", r.ServerBridge, r.InstallBridge, join(v6))
	rule(whyInstallJump, "iifname %q jump install_out", r.InstallBridge)
	if r.Host != nil {
		rule(whyServersJump, "oifname %q jump servers_in", r.ServerBridge)
	}
	w("\t}")

	w("\tchain install_out {")
	dns4, dns6 := split(r.DNS)
	if len(dns4) > 0 {
		rule(whyDNS, "ip daddr { %s } meta l4proto { tcp, udp } th dport 53 accept", join(dns4))
	}
	if len(dns6) > 0 {
		rule(whyDNS, "ip6 daddr { %s } meta l4proto { tcp, udp } th dport 53 accept", join(dns6))
	}
	allow4, allow6 := splitPrefixes(r.InstallAllow)
	if len(allow4) > 0 {
		rule(whyInstallOK, "ip daddr { %s } accept", join(allow4))
	}
	if len(allow6) > 0 {
		rule(whyInstallOK, "ip6 daddr { %s } accept", join(allow6))
	}
	rule(whyPrivate, "ip daddr { %s } drop", strings.Join(blockedV4, ", "))
	rule(whyPrivate, "ip6 daddr { %s } drop", strings.Join(blockedV6, ", "))
	w("\t}")

	// Traffic from install containers to the host itself never reaches the
	// forward hook. Nothing on the host is theirs to talk to (DNS goes
	// through Docker's resolver inside the container).
	w("\tchain input {")
	w("\t\ttype filter hook input priority filter - 1; policy accept;")
	rule(whyInstallBack, "iifname %q ct state established,related accept", r.InstallBridge)
	rule(whyInstallHost, "iifname %q drop", r.InstallBridge)
	if r.Host != nil {
		rule(whyHostJump, "jump host_in")
	}
	w("\t}")
	if r.Host != nil {
		r.renderHost(w, rule)
	}

	if r.Cgroup != "" {
		w("\tchain output {")
		w("\t\ttype filter hook output priority filter - 1; policy accept;")
		rule(whyHostMeta, "socket cgroupv2 level %d %q ip daddr { %s } drop", strings.Count(r.Cgroup, "/")+1, r.Cgroup, join(v4))
		rule(whyHostMeta, "socket cgroupv2 level %d %q ip6 daddr { %s } drop", strings.Count(r.Cgroup, "/")+1, r.Cgroup, join(v6))
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

// maxComment is nftables' limit on a comment, less room for multi-byte
// characters it might count differently.
const maxComment = 120

// comment makes text safe inside an nftables comment: the reasons are
// fixed, but an open port's names a server, and nftables has no escapes
// in quoted strings, so a quote or backslash would end it early.
func comment(s string) string {
	var b strings.Builder
	for _, r := range s {
		ok := r == ' ' || strings.ContainsRune(".,:;()'+/_-", r) ||
			(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if ok && b.Len() < maxComment {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}
