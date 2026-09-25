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
