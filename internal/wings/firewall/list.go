package firewall

import (
	"regexp"
	"strconv"
	"strings"
)

// The machine's whole rule set, for the System tab (docs/WINGS.md#firewall):
// every rule nftables has, from Raptor, Docker, ufw, or anything else,
// with whose it is, whether Raptor needs it, and what it does.

// Who a rule is from.
const (
	OwnerRaptor   = "raptor"   // Raptor's own (inet raptor)
	OwnerYours    = "yours"    // one of the owner's rules, from the System tab
	OwnerDocker   = "docker"   // Docker's, which servers' networking needs
	OwnerUFW      = "ufw"      // ufw's
	OwnerFail2ban = "fail2ban" // fail2ban's
	OwnerOther    = "other"    // anything else: the provider, a script
)

// MaxListed bounds the list; a machine with more is cut short.
const MaxListed = 1000

// Listed is one rule, or a base chain's drop policy.
type Listed struct {
	// Table is "<family> <name>" ("inet raptor", "ip nat"); Chain the
	// chain's name, and Hook its hook if it's a base chain.
	Table string `json:"table"`
	Chain string `json:"chain"`
	Hook  string `json:"hook,omitempty"`
	// Rule is the rule as nftables prints it, without its counters and
	// comment; for a policy, "policy drop".
	Rule   string `json:"rule"`
	Policy bool   `json:"policy,omitempty"`
	Owner  string `json:"owner"`
	// Needed: Raptor's own rules, and Docker's, which servers' ports and
	// internet access go through. Removing them breaks servers.
	Needed bool   `json:"needed"`
	Why    string `json:"why"`
	// Index is one of the owner's rules' number (from 1).
	Index int `json:"index,omitempty"`
	// A rule that only allows or blocks by protocol, port, and source is
	// also given as one, the way the System tab's rules are (summarize):
	// Action is "allow" or "block", empty for any other rule; Protocol
	// "tcp", "udp", or "both" ("" for any); Ports and Source "" for any.
	Action   string `json:"action,omitempty"`
	Protocol string `json:"protocol,omitempty"`
	Ports    string `json:"ports,omitempty"`
	Source   string `json:"source,omitempty"`
}

var (
	counters   = regexp.MustCompile(`\s*counter packets \d+ bytes \d+`)
	commentRe  = regexp.MustCompile(`\s*comment "([^"]*)"\s*$`)
	quoted     = regexp.MustCompile(`"[^"]*"`)
	hookRe     = regexp.MustCompile(`\bhook (\w+)`)
	policyRe   = regexp.MustCompile(`\bpolicy (\w+);`)
	whitespace = regexp.MustCompile(`\s+`)
	// nftables prints a port matched by ct original proto-dst in hex
	// ("0x1f90 [invalid type]"); it's 8080.
	hexPort = regexp.MustCompile(`0x([0-9a-f]{1,4}) \[invalid type\]`)
)

// Parse reads `nft list ruleset` output. host is the owner's saved rules,
// for their notes (nil if none). Truncated is whether there were more than
// MaxListed.
func Parse(out string, host *HostRules) (list []Listed, truncated bool) {
	depth := 0
	var table, chain, hook string
	inChain := false
	for line := range strings.Lines(out) {
		t := strings.TrimSpace(line)
		bare := quoted.ReplaceAllString(t, `""`)
		opens := strings.Count(bare, "{")
		switch {
		case depth == 0 && strings.HasPrefix(t, "table ") && strings.HasSuffix(t, "{"):
			f := strings.Fields(t)
			if len(f) >= 3 {
				table = f[1] + " " + f[2]
			}
		case depth == 1 && strings.HasPrefix(t, "chain ") && strings.HasSuffix(t, "{"):
			f := strings.Fields(t)
			if len(f) >= 2 {
				chain, hook, inChain = f[1], "", true
			}
		case depth == 2 && inChain && t != "" && t != "}":
			if strings.HasPrefix(t, "type ") {
				if m := hookRe.FindStringSubmatch(t); m != nil {
					hook = m[1]
				}
				if m := policyRe.FindStringSubmatch(t); m != nil && m[1] == "drop" {
					l := Listed{Table: table, Chain: chain, Hook: hook, Rule: "policy drop", Policy: true}
					explain(&l, "", host)
					list = append(list, l)
				}
				break
			}
			if len(list) >= MaxListed {
				truncated = true
				break
			}
			rule := counters.ReplaceAllString(t, "")
			rule = hexPort.ReplaceAllStringFunc(rule, func(h string) string {
				n, _ := strconv.ParseUint(hexPort.FindStringSubmatch(h)[1], 16, 16)
				return strconv.FormatUint(n, 10)
			})
			why := ""
			if m := commentRe.FindStringSubmatch(rule); m != nil {
				why = m[1]
				rule = rule[:len(rule)-len(m[0])]
			}
			l := Listed{Table: table, Chain: chain, Hook: hook, Rule: whitespace.ReplaceAllString(strings.TrimSpace(rule), " ")}
			explain(&l, why, host)
			summarize(&l)
			list = append(list, l)
		}
		depth += opens - strings.Count(bare, "}")
		if depth <= 1 {
			inChain = false
		}
		if depth <= 0 {
			depth, table = 0, ""
		}
	}
	return list, truncated
}

// explain says whose a rule is and what it does; comment is its nftables
// comment.
func explain(l *Listed, comment string, host *HostRules) {
	name := strings.ToUpper(l.Chain)
	rule := strings.ToLower(l.Rule)
	switch {
	case l.Table == "inet "+Table:
		if n, ok := strings.CutPrefix(comment, yoursPrefix); ok {
			l.Owner = OwnerYours
			l.Index, _ = strconv.Atoi(n)
			l.Why = "Your rule, from the list above"
			if host != nil && l.Index >= 1 && l.Index <= len(host.Rules) && host.Rules[l.Index-1].Note != "" {
				l.Why += ": " + host.Rules[l.Index-1].Note
			}
			if l.Chain == "servers_in" {
				l.Why += " (for servers)"
			} else {
				l.Why += " (for the machine)"
			}
			return
		}
		l.Owner, l.Needed = OwnerRaptor, true
		l.Why = comment
		if l.Why == "" {
			l.Why = "Raptor's: sends traffic to the chain that filters it"
		}
	case strings.HasPrefix(l.Chain, "ufw") || strings.HasPrefix(l.Chain, "ufw6"):
		l.Owner = OwnerUFW
		l.Why = "ufw's: change it with the ufw command."
	case strings.HasPrefix(l.Chain, "f2b-") || strings.Contains(rule, "f2b-") || strings.HasPrefix(l.Table, "inet f2b"):
		l.Owner = OwnerFail2ban
		l.Why = "fail2ban's: it blocks addresses that keep failing to log in."
	case strings.HasPrefix(name, "DOCKER") || strings.Contains(rule, "docker") || strings.Contains(rule, "br-") ||
		strings.Contains(rule, `"raptor0"`) || strings.Contains(rule, `"raptor-inst"`) ||
		(strings.HasSuffix(l.Table, " nat") && strings.Contains(rule, "masquerade")):
		l.Owner, l.Needed = OwnerDocker, true
		l.Why = dockerWhy(name, l.Table, rule)
	default:
		l.Owner = OwnerOther
		l.Why = "Not from Raptor or Docker: your provider, a script, or another program set it up. Raptor leaves it alone."
		switch {
		case l.Policy && l.Hook == "input":
			l.Why = "Anything this chain doesn't accept is dropped, whatever Raptor's rules allow: it can cut off SSH and SFTP. Not from Raptor or Docker."
		case l.Policy && l.Hook == "forward":
			l.Why = "Anything this chain doesn't accept is dropped, whatever Raptor's rules allow: it can stop players reaching servers. Not from Raptor or Docker."
		case l.Policy:
			l.Why = "Anything this chain doesn't accept is dropped, whatever Raptor's rules allow. Not from Raptor or Docker."
		}
	}
}

func dockerWhy(chain, table, rule string) string {
	nat := strings.HasSuffix(table, " nat")
	switch {
	case chain == "DOCKER" && nat && strings.Contains(rule, "dnat"):
		return "Docker's: sends players' connections on a server's port to its container."
	case chain == "DOCKER" && nat:
		return "Docker's: connections from containers' own networks aren't redirected."
	case chain == "DOCKER":
		return "Docker's: lets connections in to servers' ports."
	case strings.HasPrefix(chain, "DOCKER-ISOLATION"):
		return "Docker's: keeps its container networks from reaching each other."
	case chain == "DOCKER-USER":
		return "Docker's place for your own container rules (Docker never changes it)."
	case strings.Contains(rule, "masquerade"):
		return "Docker's: lets containers reach the internet through the machine's address."
	case strings.HasPrefix(chain, "DOCKER"):
		return "Docker's: forwards traffic to and from containers."
	}
	return "Docker's: hands container traffic to its own chains."
}

// groups are nftables' anonymous sets ("{ 80, 443 }"), made one word.
var groups = regexp.MustCompile(`\{\s*([^{}]*?)\s*\}`)

// summarize fills in a rule's Action, Protocol, Ports, and Source if all
// it does is allow or block by those, like `ufw status` lists them. Any
// other match (an interface, a destination, connection state) or verdict
// (a jump, a NAT) leaves it as nftables prints it.
func summarize(l *Listed) {
	if l.Policy {
		return
	}
	rule := groups.ReplaceAllStringFunc(l.Rule, func(g string) string {
		return strings.ReplaceAll(groups.FindStringSubmatch(g)[1], " ", "")
	})
	f := strings.Fields(rule)
	var action, proto, ports, source string
	setProto := func(p string) bool {
		switch p {
		case "tcp", "udp":
		case "tcp,udp", "udp,tcp":
			p = Both
		default:
			return false
		}
		if proto != "" && proto != p {
			return false
		}
		proto = p
		return true
	}
	for i := 0; i < len(f); i++ {
		next := func() (string, bool) {
			if i+1 >= len(f) {
				return "", false
			}
			i++
			return f[i], true
		}
		switch w := f[i]; {
		case action != "":
			// Only "reject with ..." may follow a verdict.
			if action != "reject" || w != "with" {
				return
			}
			i = len(f)
		case w == "accept":
			action = Allow
		case w == "drop":
			action = Block
		case w == "reject":
			action = "reject"
		case (w == "ip" || w == "ip6") && i+1 < len(f) && f[i+1] == "saddr":
			i++
			v, ok := next()
			if !ok || source != "" || strings.HasPrefix(v, "!=") {
				return
			}
			source = v
		case w == "meta" && i+1 < len(f) && f[i+1] == "l4proto",
			w == "ip" && i+1 < len(f) && f[i+1] == "protocol",
			w == "ip6" && i+1 < len(f) && f[i+1] == "nexthdr":
			i++
			v, ok := next()
			if !ok || !setProto(v) {
				return
			}
		case (w == "tcp" || w == "udp" || w == "th") && i+1 < len(f) && f[i+1] == "dport",
			w == "ct" && i+2 < len(f) && f[i+1] == "original" && f[i+2] == "proto-dst":
			if w == "ct" {
				i++
			} else if w != "th" && !setProto(w) {
				return
			}
			i++
			v, ok := next()
			if !ok || ports != "" || strings.HasPrefix(v, "!=") || strings.HasPrefix(v, "@") {
				return
			}
			ports = strings.ReplaceAll(v, ",", ", ")
		default:
			return
		}
	}
	if action == "" {
		return
	}
	if action == "reject" {
		action = Block
	}
	l.Action, l.Protocol, l.Ports, l.Source = action, proto, ports, strings.ReplaceAll(source, ",", ", ")
}
