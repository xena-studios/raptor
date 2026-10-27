package doctor

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"connectrpc.com/connect"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
)

// shortID is the end of an ID, as raptor ps and the Panel's URLs show it.
func shortID(id string) string {
	if len(id) > 12 {
		return id[len(id)-12:]
	}
	return id
}

// askWings says why a list from Wings can't be checked, or "" if it can.
func askWings(e *Env, ask bool) string {
	switch {
	case e.Status == nil || e.Status.GetServers() == nil:
		return "checked once Wings' runtime is ready"
	case !ask || e.Docker == nil:
		return "can't check"
	}
	return ""
}

func wingsErr(err error) []Result {
	if connect.CodeOf(err) == connect.CodeUnimplemented {
		return skip("the Wings running is older than this command; it's checked once Wings restarts on the new version")
	}
	return skip("couldn't ask Wings: " + err.Error())
}

// checkNetworks checks each of the owner's networks (docs/NETWORKS.md) is
// on Docker with the subnet Wings gave it.
func checkNetworks(ctx context.Context, e *Env) []Result {
	if why := askWings(e, e.Networks != nil); why != "" {
		return skip(why)
	}
	res, err := e.Networks(ctx)
	if err != nil {
		return wingsErr(err)
	}
	if len(res.GetNetworks()) == 0 {
		return skip("none yet")
	}
	out := make([]Result, 0, len(res.GetNetworks()))
	for _, n := range res.GetNetworks() {
		r := checkNetwork(ctx, e, n)
		r[0].ID, r[0].Title = "network-"+shortID(n.GetId()), "Network "+n.GetName()
		out = append(out, r...)
	}
	return out
}

func checkNetwork(ctx context.Context, e *Env, n *localv1.NetworkInfo) []Result {
	subnets, err := e.Docker.NetworkSubnets(ctx, n.GetDockerName())
	if err != nil {
		return skip("couldn't ask Docker: " + err.Error())
	}
	if subnets == nil {
		return fail(n.GetDockerName()+" is missing from Docker",
			"Servers and database instances on it can't start, and can't reach each other by name or address.",
			"systemctl restart raptor-wings: Wings creates its networks when it starts, and servers keep running. If it's still missing, journalctl -u raptor-wings | grep 'network setup failed' says why.")
	}
	want, err := netip.ParsePrefix(n.GetSubnet())
	if err == nil && !slices.Contains(subnets, want) {
		have := make([]string, 0, len(subnets))
		for _, p := range subnets {
			have = append(have, p.String())
		}
		return fail(fmt.Sprintf("%s has %s on Docker, but Raptor gave it %s", n.GetDockerName(), strings.Join(have, ", "), want),
			"Servers' and instances' fixed addresses, and the firewall's rules for the network, are for its subnet, so what's on it may not reach each other or the internet.",
			"Stop the servers and database instances on it, then systemctl restart raptor-wings: Wings recreates a network whose settings differ when nothing is on it.")
	}
	return pass(fmt.Sprintf("%s, %s, %d servers", n.GetDockerName(), n.GetSubnet(), n.GetServers()))
}

// engineNames are the engines as people know them.
var engineNames = map[string]string{
	"mariadb": "MariaDB", "mysql": "MySQL", "postgres": "PostgreSQL", "redis": "Redis", "mongodb": "MongoDB",
}

// checkInstances checks each database instance that should be running is
// running, answers on its socket, and is on each of its networks at its
// address.
func checkInstances(ctx context.Context, e *Env) []Result {
	if why := askWings(e, e.Instances != nil); why != "" {
		return skip(why)
	}
	res, err := e.Instances(ctx)
	if err != nil {
		return wingsErr(err)
	}
	if len(res.GetInstances()) == 0 {
		return skip("none on this node")
	}
	out := make([]Result, 0, len(res.GetInstances()))
	for _, in := range res.GetInstances() {
		r := checkInstance(in)
		r[0].ID, r[0].Title = "db-"+shortID(in.GetId()), "Database instance "+in.GetName()
		out = append(out, r...)
	}
	return out
}

const restartFix = "Restart it on the node's Databases page in the Panel."

func checkInstance(in *localv1.DatabaseInstanceInfo) []Result {
	what := strings.TrimSpace(engineNames[in.GetEngine()] + " " + in.GetVersion())
	unreachable := "Servers can't reach their databases on it."
	switch {
	case in.GetDesiredState() != "running":
		return pass(what + ", stopped")
	case in.GetState() == "failed":
		return fail(what+", failed: "+in.GetError(), unreachable,
			"Start it again on the node's Databases page in the Panel. If it fails again, the error says why (\"out of memory\": give it more memory); journalctl -u raptor-wings | grep "+in.GetId()+" has more.")
	case in.GetState() != "running":
		return warn(what+", "+in.GetState(), unreachable+" (until it answers)",
			"Run raptor doctor again in a minute. If it stays "+in.GetState()+", raptor db ps and journalctl -u raptor-wings say why.")
	case !in.GetChecked():
		return warn(what+", running, but it couldn't be checked: "+in.GetCheckError(), "Wings couldn't ask it whether it answers.",
			"Run raptor doctor again; if this stays, journalctl -u raptor-wings | grep "+in.GetId()+".")
	case !in.GetAnswering():
		return fail(what+", running, but not answering on its socket: "+in.GetCheckError(), unreachable,
			"Wings starts an instance that stops on its own again; one that's up but doesn't answer may be stuck or out of memory. "+restartFix)
	}
	var bad []string
	for _, n := range in.GetNetworks() {
		name := n.GetName()
		if name == "" {
			name = n.GetDockerName()
		}
		switch {
		case !n.GetAttached():
			bad = append(bad, "not on "+name)
		case n.GetAddress() != "" && n.GetActualAddress() != n.GetAddress():
			bad = append(bad, fmt.Sprintf("on %s at %s, not its address %s", name, n.GetActualAddress(), n.GetAddress()))
		}
	}
	if len(bad) > 0 {
		return fail(what+", answering, but "+strings.Join(bad, "; "),
			"Servers on those networks can't reach it, by its address or by its host name ("+in.GetHost()+").",
			restartFix+" Wings puts it on its networks when it starts. If a network itself is missing, see the Networks check.")
	}
	nets := "the server network"
	if n := len(in.GetNetworks()); n > 0 && in.GetNetworks()[0].GetId() != "" {
		nets = fmt.Sprintf("%d networks", n)
		if n == 1 {
			nets = "1 network"
		}
	}
	return pass(fmt.Sprintf("%s, answering, on %s", what, nets))
}
