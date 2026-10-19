package firewall

import (
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	r := Rules{
		ServerBridge:  "raptor0",
		InstallBridge: "raptor-inst",
		DNS:           []netip.Addr{netip.MustParseAddr("192.168.1.1"), netip.MustParseAddr("fd00::53")},
		InstallAllow: []netip.Prefix{
			netip.MustParsePrefix("10.0.0.0/8"),
			netip.MustParsePrefix("10.1.2.3/32"), // covered by 10/8, dropped
			netip.MustParsePrefix("192.168.1.50/32"),
		},
		Cgroup: "raptor.slice",
	}
	got := r.Render()
	for _, want := range []string{
		"table inet raptor\ndelete table inet raptor\ntable inet raptor {\n",
		`type filter hook forward priority filter - 1; policy accept;`,
		`iifname { "raptor0", "raptor-inst" } ip daddr { 169.254.169.254 } drop`,
		`iifname { "raptor0", "raptor-inst" } ip6 daddr { fd00:ec2::254 } drop`,
		`iifname "raptor-inst" jump install_out`,
		`ip daddr { 192.168.1.1 } meta l4proto { tcp, udp } th dport 53 accept`,
		`ip6 daddr { fd00::53 } meta l4proto { tcp, udp } th dport 53 accept`,
		`ip daddr { 10.0.0.0/8, 192.168.1.50/32 } accept`,
		`ip daddr { 0.0.0.0/8, 10.0.0.0/8, 100.64.0.0/10, 127.0.0.0/8, 169.254.0.0/16, 172.16.0.0/12, 192.0.0.0/24, 192.168.0.0/16, 198.18.0.0/15, 224.0.0.0/3 } drop`,
		`iifname "raptor-inst" drop`,
		`socket cgroupv2 level 1 "raptor.slice" ip daddr { 169.254.169.254 } drop`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rules missing %q:\n%s", want, got)
		}
	}
	// Allow rules must come before the drops in install_out.
	if strings.Index(got, "dport 53 accept") > strings.Index(got, "224.0.0.0/3 } drop") {
		t.Error("DNS accept comes after the private-range drop")
	}
}

func TestRenderMinimal(t *testing.T) {
	got := Rules{ServerBridge: "raptor0", InstallBridge: "raptor-inst"}.Render()
	for _, absent := range []string{"dport 53", "chain output", " accept\n\t\tip daddr { 0.0.0.0/8"} {
		if strings.Contains(got, absent) {
			t.Errorf("rules contain %q:\n%s", absent, got)
		}
	}
}

func TestNameservers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "resolv.conf")
	if err := os.WriteFile(path, []byte("# comment\nnameserver 192.168.5.2\nnameserver fe80::1%eth0\nsearch lan\nnameserver bogus\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := nameservers(path)
	want := []netip.Addr{netip.MustParseAddr("192.168.5.2"), netip.MustParseAddr("fe80::1")}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}

func TestRenderNetworks(t *testing.T) {
	r := Rules{
		ServerBridge: "raptor0", InstallBridge: "raptor-inst", ServerGateway: netip.MustParseAddr("172.29.0.1"),
		Networks: []Network{
			{Bridge: "rn1", Gateway: netip.MustParseAddr("172.30.0.1"), HostAccess: true},
			{Bridge: "rn2", Gateway: netip.MustParseAddr("172.31.0.1")},
		},
		Host: &HostRules{},
	}
	got := r.Render()
	for _, want := range []string{
		`iifname { "raptor0", "rn1", "rn2", "raptor-inst" } ip daddr { 169.254.169.254 } drop`,
		`iifname "raptor0" oifname { "rn1", "rn2" } jump between`,
		`iifname "rn1" oifname { "raptor0", "rn2" } jump between`,
		`iifname "rn2" oifname { "raptor0", "rn1" } jump between`,
		"chain between {\n\t\tct state established,related accept",
		"ct status dnat return",
		`type filter hook prerouting priority dstnat - 1; policy accept;`,
		`ip daddr 172.29.0.1 iifname != { "lo", "raptor0" } drop`,
		`ip daddr 172.30.0.1 iifname != { "lo", "rn1" } drop`,
		`ip daddr 172.31.0.1 iifname != { "lo", "rn2" } drop`,
		`iifname "rn2" ct state established,related accept`,
		`iifname "rn2" drop`,
		`oifname { "raptor0", "rn1", "rn2" } jump servers_in`,
		`iifname { "lo", "raptor0", "rn1", "rn2", "raptor-inst" } accept`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rules missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, `iifname "rn1" drop`) {
		t.Error("a network with host access is cut off from the machine")
	}
	// Networks are kept apart before the owner's rules see the traffic.
	if strings.Index(got, "jump between") > strings.Index(got, "jump servers_in") {
		t.Error("between comes after servers_in")
	}
}

func TestRenderBetween(t *testing.T) {
	r := Rules{
		ServerBridge: "raptor0", InstallBridge: "raptor-inst",
		Networks: []Network{{Bridge: "rn1"}, {Bridge: "rn2"}},
		Between: []Between{
			{FromBridge: "rn1", ToBridge: "rn2", To: netip.MustParseAddr("172.31.128.1"), Protocol: TCP, Ports: "25565"},
			{FromBridge: "rn2", From: netip.MustParseAddr("172.30.128.5"), ToBridge: "rn1", Protocol: Both},
			{FromBridge: "rn1", ToBridge: "rn2", Protocol: UDP},
		},
	}
	got := r.Render()
	for _, want := range []string{
		"chain between {\n\t\tct state established,related accept",
		`iifname "rn1" oifname "rn2" meta nfproto ipv4 ip daddr 172.31.128.1 meta l4proto tcp th dport { 25565 } accept`,
		`iifname "rn2" oifname "rn1" meta nfproto ipv4 ip saddr 172.30.128.5 accept`,
		`iifname "rn1" oifname "rn2" meta nfproto ipv4 meta l4proto udp accept`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rules missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "172.31.128.1") > strings.Index(got, "\t\tdrop comment \"Servers on one") {
		t.Error("a rule comes after the drop")
	}
}

func TestCheckPorts(t *testing.T) {
	if p, ports, err := CheckPorts("", "25565, 19132"); err != nil || p != Both || ports != "25565,19132" {
		t.Errorf("got %q %q %v", p, ports, err)
	}
	for _, bad := range [][2]string{{"icmp", ""}, {"tcp", "0"}, {"tcp", "10-5"}} {
		if _, _, err := CheckPorts(bad[0], bad[1]); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

func TestIptablesScript(t *testing.T) {
	got := iptablesScript([]Between{
		{FromBridge: "rn1", FromSubnet: netip.MustParsePrefix("172.30.0.0/16"), ToBridge: "rn2", To: netip.MustParseAddr("172.31.128.1"), Protocol: TCP, Ports: "25565,27015-27020"},
		{FromBridge: "rn2", From: netip.MustParseAddr("172.31.128.5"), ToBridge: "rn1", ToSubnet: netip.MustParsePrefix("172.30.0.0/16")},
	})
	want := "*raw\n:RAPTOR-BETWEEN - [0:0]\n-F RAPTOR-BETWEEN\n" +
		"-A RAPTOR-BETWEEN -i rn1 -s 172.30.0.0/16 -d 172.31.128.1/32 -p tcp -m multiport --dports 25565,27015:27020 -j ACCEPT\n" +
		"-A RAPTOR-BETWEEN -i rn2 -s 172.31.128.1/32 -d 172.30.0.0/16 -p tcp -m multiport --sports 25565,27015:27020 -j ACCEPT\n" +
		"-A RAPTOR-BETWEEN -i rn2 -s 172.31.128.5/32 -d 172.30.0.0/16 -j ACCEPT\n" +
		"-A RAPTOR-BETWEEN -i rn1 -s 172.30.0.0/16 -d 172.31.128.5/32 -j ACCEPT\n" +
		"COMMIT\n*filter\n:RAPTOR-BETWEEN - [0:0]\n-F RAPTOR-BETWEEN\n" +
		"-A RAPTOR-BETWEEN -i rn1 -o rn2 -s 172.30.0.0/16 -d 172.31.128.1/32 -p tcp -m multiport --dports 25565,27015:27020 -j ACCEPT\n" +
		"-A RAPTOR-BETWEEN -i rn2 -o rn1 -s 172.31.128.5/32 -d 172.30.0.0/16 -j ACCEPT\n" +
		"COMMIT\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestRenderGuards(t *testing.T) {
	r := Rules{
		ServerBridge: "raptor0", InstallBridge: "raptor-inst",
		Networks: []Network{{Bridge: "rn1"}},
		Guards: []Guard{
			{
				Bridge: "rn1", Addr: netip.MustParseAddr("172.30.128.9"), IPv6: netip.MustParseAddr("fd00::9"),
				Allowed: []netip.Addr{netip.MustParseAddr("172.30.128.1"), netip.MustParseAddr("172.30.128.2")},
			},
			{Bridge: "rn1", Addr: netip.MustParseAddr("172.30.128.10")},
		},
	}
	got := r.Render()
	for _, want := range []string{
		`oifname "rn1" ip daddr 172.30.128.9 ip saddr != { 172.30.128.1, 172.30.128.2 } drop`,
		`oifname "rn1" ip6 daddr fd00::9 reject with icmpx type admin-prohibited`,
		`oifname "rn1" ip daddr 172.30.128.10 drop`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("rules missing %q:\n%s", want, got)
		}
	}
	if strings.Index(got, "172.30.128.10 drop") > strings.Index(got, "jump between") {
		t.Error("guards come after the jumps between networks")
	}
}
