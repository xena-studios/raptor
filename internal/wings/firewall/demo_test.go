package firewall

import (
	"net/netip"
	"strings"
	"testing"
)

// The demo node's sandboxes go through demo_out, which allows replies,
// DNS, the allowed hosts on web ports, and Steam's, and drops the rest;
// installs are rate-limited. A node without Demo has none of it.
func TestRenderDemo(t *testing.T) {
	r := Rules{
		ServerBridge: "raptor0", InstallBridge: "raptor-inst",
		DNS: []netip.Addr{netip.MustParseAddr("9.9.9.9")},
		Demo: &Demo{
			Bridge:        "rn0123456789ab",
			Allow:         []netip.Addr{netip.MustParseAddr("104.18.1.1"), netip.MustParseAddr("2606:4700::1"), netip.MustParseAddr("104.18.1.1")},
			LimitInstalls: true,
		},
	}
	got := r.Render()
	for _, want := range []string{
		"set demo_allow4 {\n\t\ttype ipv4_addr;\n\t\telements = { 104.18.1.1 }\n\t}",
		"set demo_allow6 {\n\t\ttype ipv6_addr;\n\t\telements = { 2606:4700::1 }\n\t}",
		"set demo_install_conns {\n\t\ttype ipv4_addr; flags dynamic; timeout 1m;",
		`iifname "rn0123456789ab" jump demo_out`,
		"chain demo_out {\n\t\tct state established,related accept",
		`ip daddr { 9.9.9.9 } meta l4proto { tcp, udp } th dport 53 accept`,
		`ip daddr @demo_allow4 meta l4proto { tcp, udp } th dport { 80, 443 } accept`,
		`meta l4proto { tcp, udp } th dport 27015-27050 accept`,
		"\t\tdrop comment",
		"chain install_out {\n\t\tct state new update @demo_install_conns { ip saddr limit rate over 20/second burst 60 packets } drop",
		`update @demo_install_bytes { ip saddr limit rate over 2 mbytes/second burst 8 mbytes } drop`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rules missing %q:\n%s", want, got)
		}
	}
	// The sets come before the chains that use them, and the jump comes
	// before the install network's.
	if strings.Index(got, "set demo_allow4") > strings.Index(got, "chain forward") ||
		strings.Index(got, "jump demo_out") > strings.Index(got, "jump install_out") {
		t.Errorf("order:\n%s", got)
	}

	r.Demo = nil
	if plain := r.Render(); strings.Contains(plain, "demo") {
		t.Errorf("a normal node has demo rules:\n%s", plain)
	}
}
