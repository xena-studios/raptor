package firewall

import (
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// The node's own firewall (docs/WINGS.md#node-firewall): rules the owner
// sets from the node's System tab, for the machine itself and its game
// servers. Allow rules come first, so they can make exceptions to block
// rules; block rules next; then, with BlockIncoming, everything else new
// to the machine is dropped, except what Raptor and the owner need open.

// Rule actions and protocols.
const (
	Allow = "allow"
	Block = "block"

	TCP  = "tcp"
	UDP  = "udp"
	Both = "both"
)

// MaxHostRules bounds the rule list.
const MaxHostRules = 100

// HostRule is one rule. Note is for people and never goes into the
// rule set.
type HostRule struct {
	Action   string `json:"action"`
	Protocol string `json:"protocol,omitempty"` // tcp, udp, or both (default)
	// Ports is a comma-separated list of ports and ranges ("22",
	// "25565,27015-27020"); empty is every port and protocol.
	Ports string `json:"ports,omitempty"`
	// Source is an address or a CIDR range; empty is anywhere.
	Source string `json:"source,omitempty"`
	Note   string `json:"note,omitempty"`
}

// HostRules is the node's firewall.
type HostRules struct {
	// BlockIncoming drops new connections to the machine itself that no
	// rule allows. Game servers' ports stay open: their allocations are
	// what opens them.
	BlockIncoming bool       `json:"block_incoming"`
	Rules         []HostRule `json:"rules"`
}

// OpenPort is a port the machine keeps open when it blocks incoming
// connections: SSH, Raptor's SFTP, and servers on the host's network.
type OpenPort struct {
	Protocol string `json:"protocol"` // tcp, udp, or both
	Port     int    `json:"port"`
	Why      string `json:"why"`
}

// ErrInvalidRule matches every rule validation error (errors.Is). Their
// messages are sentences for people, without it.
var ErrInvalidRule = errors.New("invalid firewall rule")

type invalidRule struct{ msg string }

func (e invalidRule) Error() string        { return e.msg }
func (e invalidRule) Is(target error) bool { return target == ErrInvalidRule }

func ruleErr(i int, format string, a ...any) error {
	return invalidRule{fmt.Sprintf("rule %d: %s", i+1, fmt.Sprintf(format, a...))}
}

// Validate checks the rules and normalizes them (protocols, sources).
func (h *HostRules) Validate() error {
	if len(h.Rules) > MaxHostRules {
		return invalidRule{fmt.Sprintf("at most %d rules", MaxHostRules)}
	}
	for i := range h.Rules {
		r := &h.Rules[i]
		if r.Action != Allow && r.Action != Block {
			return ruleErr(i, "allow or block")
		}
		if r.Protocol == "" {
			r.Protocol = Both
		}
		if r.Protocol != TCP && r.Protocol != UDP && r.Protocol != Both {
			return ruleErr(i, "the protocol is TCP, UDP, or both")
		}
		r.Ports = strings.ReplaceAll(r.Ports, " ", "")
		if _, err := parsePorts(r.Ports); err != nil {
			return ruleErr(i, "%v", err)
		}
		if r.Source != "" {
			p, err := parseSource(r.Source)
			if err != nil {
				return ruleErr(i, "the source is an address or a range like 203.0.113.0/24")
			}
			r.Source = p.String()
		}
		if r.Source == "" && r.Ports == "" {
			if r.Action == Allow {
				return ruleErr(i, "allowing every port from anywhere is no rule: turn off blocking incoming connections instead")
			}
			return ruleErr(i, "blocking every port from anywhere would cut the machine off: turn on blocking incoming connections instead")
		}
		if len(r.Note) > 100 {
			return ruleErr(i, "the note is at most 100 characters")
		}
	}
	return nil
}

func parseSource(s string) (netip.Prefix, error) {
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return p.Masked(), nil
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap().WithZone("")
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// portRange is a port or an inclusive range.
type portRange struct{ lo, hi int }

func (p portRange) String() string {
	if p.lo == p.hi {
		return strconv.Itoa(p.lo)
	}
	return fmt.Sprintf("%d-%d", p.lo, p.hi)
}

func (p portRange) contains(port int) bool { return port >= p.lo && port <= p.hi }

func parsePorts(s string) ([]portRange, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, ",")
	if len(parts) > 20 {
		return nil, errors.New("at most 20 ports or ranges per rule")
	}
	var out []portRange
	for _, part := range parts {
		lo, hi, isRange := strings.Cut(part, "-")
		a, err := strconv.Atoi(lo)
		if err != nil || a < 1 || a > 65535 {
			return nil, fmt.Errorf("%q isn't a port (1 to 65535)", part)
		}
		b := a
		if isRange {
			if b, err = strconv.Atoi(hi); err != nil || b < a || b > 65535 {
				return nil, fmt.Errorf("%q isn't a range of ports", part)
			}
		}
		out = append(out, portRange{a, b})
	}
	// nftables refuses overlapping intervals in a set.
	slices.SortFunc(out, func(x, y portRange) int { return x.lo - y.lo })
	for i := 1; i < len(out); i++ {
		if out[i].lo <= out[i-1].hi {
			return nil, fmt.Errorf("%s and %s overlap", out[i-1], out[i])
		}
	}
	return out, nil
}

// Covers reports whether a rule matches every connection to port over
// protocol (from its source).
func (r HostRule) Covers(protocol string, port int) bool {
	if r.Protocol != Both && r.Protocol != protocol {
		return false
	}
	ports, _ := parsePorts(r.Ports)
	return len(ports) == 0 || slices.ContainsFunc(ports, func(p portRange) bool { return p.contains(port) })
}

// match is a rule's match expression; dport is how to match the
// destination port ("th dport" on the machine, "ct original proto-dst" for
// servers, whose packets are already translated to the container's port).
func (r HostRule) match(dport string) string {
	var parts []string
	if r.Source != "" {
		p, _ := parseSource(r.Source)
		if p.Addr().Is4() {
			parts = append(parts, "ip saddr "+p.String())
		} else {
			parts = append(parts, "ip6 saddr "+p.String())
		}
	}
	ports, _ := parsePorts(r.Ports)
	if len(ports) > 0 {
		proto := "{ tcp, udp }"
		if r.Protocol != Both {
			proto = r.Protocol
		}
		set := make([]string, len(ports))
		for i, p := range ports {
			set[i] = p.String()
		}
		parts = append(parts, "meta l4proto "+proto, dport+" { "+join(set)+" }")
	}
	return strings.Join(parts, " ")
}

func verdict(action string) string {
	if action == Allow {
		return "accept"
	}
	return "drop"
}

// renderHost writes the host rules' chains: host_in, jumped to from input,
// and servers_in, from forward for traffic to the server network. The
// owner's rules are commented with their number, never their note.
func (r Rules) renderHost(w func(string, ...any), rule func(why, format string, a ...any)) {
	h := r.Host
	w("\tchain host_in {")
	// Loopback and container networks aren't the outside world.
	rule(whyLocal, "iifname { \"lo\", %q, %q } accept", r.ServerBridge, r.InstallBridge)
	for _, pat := range []string{"docker*", "br-*", "veth*"} {
		rule(whyContainers, "iifname %q accept", pat)
	}
	for _, action := range []string{Allow, Block} {
		for i, hr := range h.Rules {
			if hr.Action == action {
				rule(yoursPrefix+strconv.Itoa(i+1), "%s %s", hr.match("th dport"), verdict(action))
			}
		}
	}
	if h.BlockIncoming {
		rule(whyEstablished, "ct state established,related accept")
		rule(whyICMP, "meta l4proto { icmp, ipv6-icmp } accept")
		rule(whyDHCP, "udp sport { 67, 547 } udp dport { 68, 546 } accept")
		for _, p := range r.Open {
			proto := "{ tcp, udp }"
			if p.Protocol != Both {
				proto = p.Protocol
			}
			rule(whyOpen+p.Why, "meta l4proto %s th dport %d accept", proto, p.Port)
		}
		rule(whyDropRest, "drop")
	}
	w("\t}")
	w("\tchain servers_in {")
	rule(whyServerNets, "iifname { %q, %q } accept", r.ServerBridge, r.InstallBridge)
	for _, action := range []string{Allow, Block} {
		for i, hr := range h.Rules {
			if hr.Action == action {
				rule(yoursPrefix+strconv.Itoa(i+1), "%s %s", hr.match("ct original proto-dst"), verdict(action))
			}
		}
	}
	w("\t}")
}
