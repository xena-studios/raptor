package firewall

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func sampleHost() Rules {
	return Rules{
		ServerBridge: "raptor0", InstallBridge: "raptor-inst",
		Host: &HostRules{BlockIncoming: true, Rules: []HostRule{
			{Action: Block, Source: "203.0.113.7"},
			{Action: Allow, Protocol: TCP, Ports: "22", Source: "198.51.100.0/24", Note: "home"},
			{Action: Block, Protocol: UDP, Ports: "27015-27020, 25565", Source: "2001:db8::/32"},
			{Action: Allow, Ports: "8080"},
		}},
		Open: []OpenPort{{Protocol: TCP, Port: 22, Why: "SSH"}, {Protocol: Both, Port: 2022, Why: "SFTP"}},
	}
}

func TestRenderHost(t *testing.T) {
	r := sampleHost()
	if err := r.Host.Validate(); err != nil {
		t.Fatal(err)
	}
	got := r.Render()
	if p := os.Getenv("RAPTOR_DUMP_NFT"); p != "" {
		_ = os.WriteFile(p, []byte(got), 0o600)
	}
	for _, want := range []string{
		`oifname "raptor0" jump servers_in`,
		"\t\tjump host_in comment",
		`iifname { "lo", "raptor0", "raptor-inst" } accept`,
		`ip saddr 203.0.113.7/32 drop`,
		`ip saddr 198.51.100.0/24 meta l4proto tcp th dport { 22 } accept`,
		`ip6 saddr 2001:db8::/32 meta l4proto udp th dport { 25565, 27015-27020 } drop`,
		`meta l4proto { tcp, udp } th dport { 8080 } accept`,
		`ip6 saddr 2001:db8::/32 meta l4proto udp ct original proto-dst { 25565, 27015-27020 } drop`,
		`meta l4proto tcp th dport 22 accept`,
		`meta l4proto { tcp, udp } th dport 2022 accept`,
		"\t\tdrop comment \"Blocking incoming connections: anything not allowed above is dropped\"\n\t}",
		`accept comment "yours 2"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "home") {
		t.Error("a note went into the rule set")
	}
	hostIn := got[strings.Index(got, "chain host_in"):]
	// Allow rules come before block rules, and the opened ports after both.
	if strings.Index(hostIn, "dport { 8080 } accept") > strings.Index(hostIn, "203.0.113.7/32 drop") ||
		strings.Index(hostIn, "th dport 22 accept") < strings.Index(hostIn, "2001:db8::/32") {
		t.Errorf("order:\n%s", hostIn)
	}

	// Without blocking incoming, nothing is dropped by default.
	r.Host.BlockIncoming = false
	if got := r.Render(); strings.Contains(got, "\t\tdrop comment") || strings.Contains(got, "th dport 2022") {
		t.Errorf("default drop without blocking incoming:\n%s", got)
	}
	// No host rules: no host chains.
	if got := (Rules{ServerBridge: "raptor0", InstallBridge: "raptor-inst"}).Render(); strings.Contains(got, "host_in") {
		t.Error("host chains without host rules")
	}
}

// An open port's reason names a server, whose name is anyone's: it can't
// end the comment, or add rules.
func TestRenderComment(t *testing.T) {
	r := sampleHost()
	r.Open = append(r.Open, OpenPort{Protocol: Both, Port: 25565, Why: `smp" accept; drop \ {` + strings.Repeat("x", 200)})
	got := r.Render()
	line := got[strings.Index(got, "th dport 25565 accept"):]
	line = line[:strings.Index(line, "\n")]
	if strings.Count(line, `"`) != 2 || strings.Contains(line, `\`) || len(line) > 200 {
		t.Errorf("unsafe comment: %s", line)
	}
}

func TestValidateHost(t *testing.T) {
	for _, r := range []HostRule{
		{Action: "reject", Ports: "22"},
		{Action: Allow, Ports: "0"},
		{Action: Allow, Ports: "70000"},
		{Action: Allow, Ports: "30-20"},
		{Action: Allow, Ports: "20-30,25"},
		{Action: Allow, Ports: "22; drop"},
		{Action: Block, Source: "evil\n}"},
		{Action: Allow, Protocol: "icmp", Ports: "1"},
		{Action: Allow},
		{Action: Block},
		{Action: Allow, Ports: "1", Note: strings.Repeat("x", 101)},
	} {
		h := HostRules{Rules: []HostRule{r}}
		if err := h.Validate(); !errors.Is(err, ErrInvalidRule) {
			t.Errorf("%+v: %v", r, err)
		}
	}
	h := HostRules{Rules: []HostRule{{Action: Allow, Ports: " 22 ", Source: "::ffff:10.0.0.1"}}}
	if err := h.Validate(); err != nil || h.Rules[0].Source != "10.0.0.1/32" || h.Rules[0].Protocol != Both || h.Rules[0].Ports != "22" {
		t.Errorf("normalized: %+v, %v", h.Rules[0], err)
	}
	if !h.Rules[0].Covers(TCP, 22) || h.Rules[0].Covers(TCP, 23) {
		t.Error("covers")
	}
}
