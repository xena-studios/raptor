package firewall

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	r := sampleHost()
	if err := r.Host.Validate(); err != nil {
		t.Fatal(err)
	}
	out := `table ip nat {
	chain DOCKER {
		iifname "raptor0" counter packets 0 bytes 0 return
		iifname != "raptor0" meta l4proto tcp tcp dport 25565 counter packets 12 bytes 640 dnat to 172.18.0.2:25565
	}
	chain POSTROUTING {
		type nat hook postrouting priority srcnat; policy accept;
		ip saddr 172.18.0.0/16 oifname != "raptor0" counter packets 3 bytes 180 masquerade
	}
}
table ip filter {
	set blocked {
		type ipv4_addr
		elements = { 192.0.2.1,
			     192.0.2.2 }
	}
	chain INPUT {
		type filter hook input priority filter; policy drop;
		tcp dport 22 accept comment "from the provider's image"
	}
	chain ufw-user-input {
		tcp dport 80 accept
	}
	chain f2b-sshd {
		ip saddr 198.51.100.9 reject
	}
	chain hex {
		meta l4proto tcp ct original proto-dst 0x1f90 [invalid type] accept
	}
}
` + strings.ReplaceAll(r.Render(), "table inet raptor\ndelete table inet raptor\n", "")
	list, truncated := Parse(out, r.Host)
	if truncated {
		t.Fatal("truncated")
	}
	find := func(sub string) Listed {
		t.Helper()
		for _, l := range list {
			if strings.Contains(l.Rule, sub) {
				return l
			}
		}
		t.Fatalf("no rule with %q in %+v", sub, list)
		return Listed{}
	}
	if l := find("dnat to 172.18.0.2:25565"); l.Owner != OwnerDocker || !l.Needed || l.Table != "ip nat" || l.Chain != "DOCKER" || strings.Contains(l.Rule, "counter") || !strings.Contains(l.Why, "player") {
		t.Errorf("docker dnat: %+v", l)
	}
	if l := find("masquerade"); l.Owner != OwnerDocker || l.Hook != "postrouting" {
		t.Errorf("masquerade: %+v", l)
	}
	if l := find("policy drop"); !l.Policy || l.Owner != OwnerOther || l.Chain != "INPUT" || !strings.Contains(l.Why, "SSH") {
		t.Errorf("policy: %+v", l)
	}
	if l := find("proto-dst 8080 accept"); l.Rule != "meta l4proto tcp ct original proto-dst 8080 accept" {
		t.Errorf("hex port: %+v", l)
	}
	if l := find("tcp dport 22 accept"); l.Owner != OwnerOther || l.Rule != "tcp dport 22 accept" {
		t.Errorf("other: %+v", l)
	}
	if l := find("tcp dport 80"); l.Owner != OwnerUFW {
		t.Errorf("ufw: %+v", l)
	}
	if l := find("198.51.100.9 reject"); l.Owner != OwnerFail2ban {
		t.Errorf("fail2ban: %+v", l)
	}
	if l := find("169.254.169.254"); l.Owner != OwnerRaptor || !l.Needed || !strings.Contains(l.Why, "metadata") || strings.Contains(l.Rule, "comment") {
		t.Errorf("raptor: %+v", l)
	}
	if l := find("198.51.100.0/24 meta l4proto tcp th dport"); l.Owner != OwnerYours || l.Index != 2 || l.Needed || !strings.Contains(l.Why, "home") {
		t.Errorf("yours: %+v", l)
	}
	for _, l := range list {
		if strings.Contains(l.Rule, "192.0.2.") || strings.Contains(l.Rule, "type ") {
			t.Errorf("a set or chain type listed as a rule: %+v", l)
		}
	}
}
