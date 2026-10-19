package firewall

import (
	"errors"
	"net/netip"
	"strings"
)

// Rules between the owner's networks (docs/NETWORKS.md#rules-between-networks):
// each lets connections from one network, or one server's address on it,
// to another network, or one server, on a protocol and ports. There are no
// block rules: everything between networks that no rule allows is dropped.

// Between is one rule between networks, as the rule set needs it.
type Between struct {
	FromBridge string
	FromSubnet netip.Prefix // the network's range, for Docker's rules
	From       netip.Addr   // zero: anything on the network
	ToBridge   string
	ToSubnet   netip.Prefix
	To         netip.Addr // zero: anything on the network
	Protocol   string     // tcp, udp, or both
	Ports      string     // as in HostRule; empty is every port
}

// CheckPorts checks a rule's protocol and ports, the way the node's
// firewall rules have them, and returns them normalized.
func CheckPorts(protocol, ports string) (string, string, error) {
	if protocol == "" {
		protocol = Both
	}
	if protocol != TCP && protocol != UDP && protocol != Both {
		return "", "", errors.New("the protocol is TCP, UDP, or both")
	}
	ports = strings.ReplaceAll(ports, " ", "")
	if _, err := parsePorts(ports); err != nil {
		return "", "", err
	}
	return protocol, ports, nil
}

// match is the rule's match expression, after its interfaces.
func (a Between) match() string {
	var parts []string
	if a.From.IsValid() {
		parts = append(parts, "ip saddr "+a.From.String())
	}
	if a.To.IsValid() {
		parts = append(parts, "ip daddr "+a.To.String())
	}
	ports, _ := parsePorts(a.Ports)
	switch {
	case len(ports) > 0:
		proto := "{ tcp, udp }"
		if a.Protocol != Both && a.Protocol != "" {
			proto = a.Protocol
		}
		set := make([]string, len(ports))
		for i, p := range ports {
			set[i] = p.String()
		}
		parts = append(parts, "meta l4proto "+proto, "th dport { "+join(set)+" }")
	case a.Protocol == TCP || a.Protocol == UDP:
		parts = append(parts, "meta l4proto "+a.Protocol)
	}
	return strings.Join(parts, " ")
}

// renderBetween writes the between chain: replies, published ports, the
// owner's rules, then the drop.
func (r Rules) renderBetween(w func(string, ...any), rule func(why, format string, a ...any)) {
	w("\tchain between {")
	rule(whyBetweenBack, "ct state established,related accept")
	rule(whyPublished, "ct status dnat return")
	for _, a := range r.Between {
		m := a.match()
		if m != "" {
			m = " " + m
		}
		// IPv4 only, like servers' fixed addresses and DOCKER-USER.
		rule(whyAllowed, "iifname %q oifname %q meta nfproto ipv4%s accept", a.FromBridge, a.ToBridge, m)
	}
	rule(whyBetween, "drop")
	w("\t}")
}
