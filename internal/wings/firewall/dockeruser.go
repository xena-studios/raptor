package firewall

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os/exec"
	"strings"
)

// Docker blocks traffic between its bridges in its own iptables rules,
// where a drop is final whatever inet raptor accepts: a raw PREROUTING
// rule per container drops packets to it from any other interface, and
// the filter table drops forwarding into a bridge unless it's to a
// published port. For rules between networks, Wings keeps a chain named
// RAPTOR-BETWEEN in both tables, jumped to first from raw PREROUTING and
// from DOCKER-USER (the chain Docker keeps for its users), accepting
// exactly what the rules allow, so Docker stops blocking it.
//
// raw runs before connection tracking, so it also lets replies through
// (from the destination's ports). inet raptor's between chain is the one
// that tracks connections: it lets replies to allowed connections back,
// and drops a new one either way that no rule allows.

// DockerUserChain is Wings' chain, in the raw and filter tables.
const DockerUserChain = "RAPTOR-BETWEEN"

// ErrNoDockerUser is returned when Docker has no DOCKER-USER chain (its
// nftables firewall backend, or Docker's iptables rules turned off).
var ErrNoDockerUser = errors.New("docker's DOCKER-USER chain is missing")

// iptablesScript is the iptables-restore input replacing both chains.
func iptablesScript(between []Between) string {
	var raw, filter strings.Builder
	for _, a := range between {
		from, to := a.From, a.To
		fromNet, toNet := host(from, a.FromSubnet), host(to, a.ToSubnet)
		ports, _ := parsePorts(a.Ports)
		set := make([]string, len(ports))
		for i, p := range ports {
			set[i] = strings.ReplaceAll(p.String(), "-", ":")
		}
		var protos []string
		switch {
		case a.Protocol == TCP || a.Protocol == UDP:
			protos = []string{a.Protocol}
		case len(ports) > 0:
			protos = []string{TCP, UDP}
		default:
			protos = []string{""}
		}
		for _, proto := range protos {
			match := func(dir string) string {
				if proto == "" {
					return ""
				}
				m := " -p " + proto
				if len(set) > 0 {
					m += " -m multiport --" + dir + " " + strings.Join(set, ",")
				}
				return m
			}
			fwd := fmt.Sprintf(" -i %s%s%s%s", a.FromBridge, addr("-s", fromNet), addr("-d", toNet), match("dports"))
			back := fmt.Sprintf(" -i %s%s%s%s", a.ToBridge, addr("-s", toNet), addr("-d", fromNet), match("sports"))
			raw.WriteString("-A " + DockerUserChain + fwd + " -j ACCEPT\n")
			raw.WriteString("-A " + DockerUserChain + back + " -j ACCEPT\n")
			fmt.Fprintf(&filter, "-A %s -i %s -o %s%s%s%s -j ACCEPT\n", DockerUserChain,
				a.FromBridge, a.ToBridge, addr("-s", fromNet), addr("-d", toNet), match("dports"))
		}
	}
	head := ":" + DockerUserChain + " - [0:0]\n-F " + DockerUserChain + "\n"
	return "*raw\n" + head + raw.String() + "COMMIT\n*filter\n" + head + filter.String() + "COMMIT\n"
}

// host is one server's address as a /32, or else its network's range.
func host(a netip.Addr, subnet netip.Prefix) netip.Prefix {
	if a.IsValid() {
		return netip.PrefixFrom(a, 32)
	}
	return subnet
}

func addr(flag string, p netip.Prefix) string {
	if !p.IsValid() {
		return ""
	}
	return " " + flag + " " + p.String()
}

// ApplyDockerUser replaces both RAPTOR-BETWEEN chains with the rules, and
// makes sure they're jumped to.
func ApplyDockerUser(ctx context.Context, between []Between) error {
	if err := iptables(ctx, "-L", "DOCKER-USER", "-n"); err != nil {
		return ErrNoDockerUser
	}
	cmd := exec.CommandContext(ctx, "iptables-restore", "-w", "--noflush")
	cmd.Stdin = strings.NewReader(iptablesScript(between))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("iptables-restore: %w: %s", err, bytes.TrimSpace(out))
	}
	return ensureJumps(ctx)
}

// ensureJumps puts the jumps in place: first in raw PREROUTING, ahead of
// Docker's per-container drops (which Docker appends), and in DOCKER-USER.
func ensureJumps(ctx context.Context) error {
	if !rawJumpFirst(ctx) {
		for iptables(ctx, "-t", "raw", "-D", "PREROUTING", "-j", DockerUserChain) == nil {
		}
		if err := iptables(ctx, "-t", "raw", "-I", "PREROUTING", "1", "-j", DockerUserChain); err != nil {
			return err
		}
	}
	if iptables(ctx, "-C", "DOCKER-USER", "-j", DockerUserChain) != nil {
		if err := iptables(ctx, "-I", "DOCKER-USER", "-j", DockerUserChain); err != nil {
			return err
		}
	}
	return nil
}

// rawJumpFirst reports whether raw PREROUTING's first rule is the jump.
func rawJumpFirst(ctx context.Context) bool {
	out, err := exec.CommandContext(ctx, "iptables", "-w", "-t", "raw", "-S", "PREROUTING").Output()
	if err != nil {
		return false
	}
	for line := range strings.Lines(string(out)) {
		if strings.HasPrefix(line, "-A ") {
			return strings.TrimSpace(line) == "-A PREROUTING -j "+DockerUserChain
		}
	}
	return false
}

// DockerUserPresent reports whether both jumps are in place. Restarting
// Docker or a firewall service can remove them.
func DockerUserPresent(ctx context.Context) bool {
	return rawJumpFirst(ctx) && iptables(ctx, "-C", "DOCKER-USER", "-j", DockerUserChain) == nil
}

func iptables(ctx context.Context, args ...string) error {
	out, err := exec.CommandContext(ctx, "iptables", append([]string{"-w"}, args...)...).CombinedOutput() //nolint:gosec // fixed arguments from this file
	if err != nil {
		return fmt.Errorf("iptables %s: %w: %s", strings.Join(args, " "), err, bytes.TrimSpace(out))
	}
	return nil
}
