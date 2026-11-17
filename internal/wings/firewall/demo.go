package firewall

import (
	"net/netip"
	"slices"
)

// Demo is the public demo node's egress policy (#449,
// docs/SECURITY-MODEL.md#the-public-demo). Guests' sandboxes run on the
// demo network, and a running one reaches only what games need: the hosts
// in Allow (Modrinth, Hangar, uMod, Mojang and PaperMC, Steam,
// CurseForge's and Thunderstore's CDNs, resolved by Wings), Steam's
// connection managers' ports, and DNS. Everything else is dropped, which
// keeps mining pools, spam, and scans from guests' servers off the
// internet. Players reach the sandboxes' ports as on any node: replies to
// their connections go back out.
//
// Installs run on the install network, where library scripts may reach
// the wider web (their downloads come from GitHub releases and game CDNs
// all over), but with LimitInstalls, each install container's new
// connections and outbound bytes are rate-limited.
type Demo struct {
	// Bridge is the demo network's interface.
	Bridge string
	// Allow are the allowed hosts' addresses.
	Allow []netip.Addr
	// LimitInstalls rate-limits install containers.
	LimitInstalls bool
}

// Steam's servers (connection managers and the master server) are on many
// addresses that change; game servers reach them on these ports.
const demoSteamPorts = "27015-27050"

// Install containers' limits on the demo node: new connections a second
// (and a burst), and outbound bytes a second, per container.
const (
	demoInstallConns = "limit rate over 20/second burst 60 packets"
	demoInstallBytes = "limit rate over 2 mbytes/second burst 8 mbytes"
)

const (
	whyDemoJump    = "The demo's sandboxes go through demo_out"
	whyDemoBack    = "Replies to players connected to a sandbox, and to its allowed connections"
	whyDemoDNS     = "Sandboxes may ask the machine's DNS resolvers"
	whyDemoAllow   = "Hosts games need: Modrinth, Hangar, uMod, Mojang, PaperMC, Steam, CurseForge, Thunderstore"
	whyDemoSteam   = "Steam's connection managers and master server, on their ports"
	whyDemoDrop    = "Anything else from a sandbox is dropped: no mining, spam, or scans from the demo"
	whyDemoConns   = "Demo installs: at most 20 new connections a second per install"
	whyDemoBytes   = "Demo installs: at most 2 MB a second out per install"
	demoAllow4Set  = "demo_allow4"
	demoAllow6Set  = "demo_allow6"
	demoConnsSet   = "demo_install_conns"
	demoBytesSet   = "demo_install_bytes"
	demoSetTimeout = "1m"
)

// renderDemoSets declares the demo's sets, before any chain uses them.
func (r Rules) renderDemoSets(w func(string, ...any)) {
	v4, v6 := split(slices.Compact(slices.SortedFunc(slices.Values(r.Demo.Allow), netip.Addr.Compare)))
	set := func(name, typ string, elems []string) {
		w("\tset %s {", name)
		w("\t\ttype %s;", typ)
		if len(elems) > 0 {
			w("\t\telements = { %s }", join(elems))
		}
		w("\t}")
	}
	set(demoAllow4Set, "ipv4_addr", v4)
	set(demoAllow6Set, "ipv6_addr", v6)
	if r.Demo.LimitInstalls {
		for _, name := range []string{demoConnsSet, demoBytesSet} {
			w("\tset %s {", name)
			w("\t\ttype ipv4_addr; flags dynamic; timeout %s;", demoSetTimeout)
			w("\t}")
		}
	}
}

// renderDemo writes the sandboxes' outbound chain.
func (r Rules) renderDemo(w func(string, ...any), rule func(why, format string, a ...any)) {
	w("\tchain demo_out {")
	rule(whyDemoBack, "ct state established,related accept")
	dns4, dns6 := split(r.DNS)
	if len(dns4) > 0 {
		rule(whyDemoDNS, "ip daddr { %s } meta l4proto { tcp, udp } th dport 53 accept", join(dns4))
	}
	if len(dns6) > 0 {
		rule(whyDemoDNS, "ip6 daddr { %s } meta l4proto { tcp, udp } th dport 53 accept", join(dns6))
	}
	rule(whyDemoAllow, "ip daddr @%s meta l4proto { tcp, udp } th dport { 80, 443 } accept", demoAllow4Set)
	rule(whyDemoAllow, "ip6 daddr @%s meta l4proto { tcp, udp } th dport { 80, 443 } accept", demoAllow6Set)
	rule(whyDemoSteam, "meta l4proto { tcp, udp } th dport %s accept", demoSteamPorts)
	rule(whyDemoDrop, "drop")
	w("\t}")
}

// renderDemoInstalls writes the install limits, first in install_out.
func renderDemoInstalls(rule func(why, format string, a ...any)) {
	rule(whyDemoConns, "ct state new update @%s { ip saddr %s } drop", demoConnsSet, demoInstallConns)
	rule(whyDemoBytes, "update @%s { ip saddr %s } drop", demoBytesSet, demoInstallBytes)
}
