package docker

import (
	"bufio"
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/xena-studios/raptor/internal/wings/containers"
)

// Bridge interface names. Fixed, so firewall rules can match them before
// Docker has created them.
const (
	ServerBridge  = "raptor0"
	InstallBridge = "raptor-inst"
)

// ensureNetwork returns the named network, creating it if needed. An existing
// network Wings didn't create is never touched. subnet may be invalid (zero),
// in which case the first free candidate is used.
func (c *Client) ensureNetwork(ctx context.Context, name, bridge string, subnet netip.Prefix, icc bool, avoid []netip.Prefix) (containers.Network, error) {
	res, err := c.api.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	switch {
	case err == nil:
		n := res.Network
		if n.Labels[LabelManaged] != "true" {
			return containers.Network{}, fmt.Errorf("network %s exists but wasn't created by Wings; remove or rename it", name)
		}
		if subnet.IsValid() && (len(n.IPAM.Config) == 0 || n.IPAM.Config[0].Subnet != subnet) {
			return containers.Network{}, fmt.Errorf("network %s exists with a different subnet than the configured %s", name, subnet)
		}
		return toNetwork(n.Network, bridge)
	case !cerrdefs.IsNotFound(err):
		return containers.Network{}, fmt.Errorf("inspect network %s: %w", name, err)
	}

	used, err := c.usedPrefixes(ctx)
	if err != nil {
		return containers.Network{}, err
	}
	used = append(used, avoid...)
	if subnet.IsValid() {
		if p, ok := overlaps(subnet, used); ok {
			return containers.Network{}, fmt.Errorf("subnet %s for network %s overlaps %s, which is already in use on this host", subnet, name, p)
		}
	} else if subnet, err = pickSubnet(used); err != nil {
		return containers.Network{}, fmt.Errorf("network %s: %w", name, err)
	}

	gw := subnet.Addr().Next()
	ipv4, ipv6 := true, false
	opts := map[string]string{
		"com.docker.network.bridge.name":       bridge,
		"com.docker.network.bridge.enable_icc": fmt.Sprint(icc),
	}
	if _, err := c.api.NetworkCreate(ctx, name, client.NetworkCreateOptions{
		Driver:     "bridge",
		EnableIPv4: &ipv4,
		EnableIPv6: &ipv6,
		IPAM:       &network.IPAM{Driver: "default", Config: []network.IPAMConfig{{Subnet: subnet, Gateway: gw}}},
		Options:    opts,
		Labels:     map[string]string{LabelManaged: "true"},
	}); err != nil {
		return containers.Network{}, fmt.Errorf("create network %s: %w", name, err)
	}
	return containers.Network{Name: name, Bridge: bridge, Subnet: subnet, Gateway: gw}, nil
}

func toNetwork(n network.Network, bridge string) (containers.Network, error) {
	if b := n.Options["com.docker.network.bridge.name"]; b != "" {
		bridge = b
	}
	for _, cfg := range n.IPAM.Config {
		if cfg.Subnet.Addr().Is4() {
			gw := cfg.Gateway
			if !gw.IsValid() {
				gw = cfg.Subnet.Addr().Next()
			}
			return containers.Network{Name: n.Name, Bridge: bridge, Subnet: cfg.Subnet, Gateway: gw}, nil
		}
	}
	return containers.Network{}, fmt.Errorf("network %s has no IPv4 subnet", n.Name)
}

// usedPrefixes lists IPv4 ranges already in use on the host: interface
// addresses, routes, and other Docker networks.
func (c *Client) usedPrefixes(ctx context.Context) ([]netip.Prefix, error) {
	var used []netip.Prefix
	nets, err := c.api.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	for _, n := range nets.Items {
		for _, cfg := range n.IPAM.Config {
			if cfg.Subnet.IsValid() {
				used = append(used, cfg.Subnet)
			}
		}
	}
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil, fmt.Errorf("interface addresses: %w", err)
	}
	for _, a := range addrs {
		if p, err := netip.ParsePrefix(a.String()); err == nil && p.Addr().Is4() && !p.Addr().IsLoopback() {
			used = append(used, p.Masked())
		}
	}
	routes, err := hostRoutes("/proc/net/route")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	return append(used, routes...), nil
}

// hostRoutes parses the kernel's IPv4 routing table, skipping default routes.
func hostRoutes(path string) ([]netip.Prefix, error) {
	f, err := os.Open(path) //nolint:gosec // fixed /proc path
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var out []netip.Prefix
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 8 {
			continue
		}
		dst, err1 := hexAddr(fields[1])
		mask, err2 := hexAddr(fields[7])
		if err1 != nil || err2 != nil {
			continue
		}
		bits, _ := net.IPMask(mask.AsSlice()).Size()
		if bits == 0 {
			continue
		}
		out = append(out, netip.PrefixFrom(dst, bits).Masked())
	}
	return out, sc.Err()
}

// hexAddr decodes /proc/net/route's little-endian hex IPv4 addresses.
func hexAddr(s string) (netip.Addr, error) {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 4 {
		return netip.Addr{}, fmt.Errorf("bad address %q", s)
	}
	var a [4]byte
	binary.BigEndian.PutUint32(a[:], binary.LittleEndian.Uint32(b))
	return netip.AddrFrom4(a), nil
}

// subnetCandidates are tried in order. 172.29–31 come first: they're at the
// end of Docker's default pools (so Docker is unlikely to have used them)
// and rarely used by home or provider networks.
func subnetCandidates() []netip.Prefix {
	var out []netip.Prefix
	for _, b := range []byte{29, 30, 31, 28, 27, 26, 25, 24} {
		out = append(out, netip.PrefixFrom(netip.AddrFrom4([4]byte{172, b, 0, 0}), 16))
	}
	for b := 200; b < 256; b++ {
		out = append(out, netip.PrefixFrom(netip.AddrFrom4([4]byte{10, byte(b), 0, 0}), 16))
	}
	return out
}

func pickSubnet(used []netip.Prefix) (netip.Prefix, error) {
	for _, c := range subnetCandidates() {
		if _, ok := overlaps(c, used); !ok {
			return c, nil
		}
	}
	return netip.Prefix{}, errors.New("no free subnet; set docker.subnet in the config")
}

func overlaps(p netip.Prefix, used []netip.Prefix) (netip.Prefix, bool) {
	for _, u := range used {
		if p.Overlaps(u) {
			return u, true
		}
	}
	return netip.Prefix{}, false
}

// EnsureNetwork implements containers.Runtime.
func (c *Client) EnsureNetwork(ctx context.Context, s containers.NetworkSpec) (containers.Network, error) {
	res, err := c.api.NetworkInspect(ctx, s.Name, client.NetworkInspectOptions{})
	switch {
	case err == nil:
		n := res.Network
		if n.Labels[LabelManaged] != "true" {
			return containers.Network{}, fmt.Errorf("network %s exists but wasn't created by Wings; remove or rename it", s.Name)
		}
		if matches(n.Network, s) {
			return toNetwork(n.Network, s.Bridge)
		}
		if len(n.Containers) > 0 {
			return containers.Network{}, fmt.Errorf("%w: %d containers are connected to %s", containers.ErrNetworkInUse, len(n.Containers), s.Name)
		}
		if _, err := c.api.NetworkRemove(ctx, s.Name, client.NetworkRemoveOptions{}); err != nil {
			return containers.Network{}, fmt.Errorf("remove network %s to change it: %w", s.Name, err)
		}
	case !cerrdefs.IsNotFound(err):
		return containers.Network{}, fmt.Errorf("inspect network %s: %w", s.Name, err)
	}

	used, err := c.usedPrefixes(ctx)
	if err != nil {
		return containers.Network{}, err
	}
	subnet := s.Subnet
	if subnet.IsValid() {
		if p, ok := overlaps(subnet, used); ok {
			return containers.Network{}, fmt.Errorf("%w: %s overlaps %s, which is already in use on this machine", ErrSubnetTaken, subnet, p)
		}
	} else if subnet, err = pickSubnet(used); err != nil {
		return containers.Network{}, fmt.Errorf("network %s: %w", s.Name, err)
	}
	gw := subnet.Addr().Next()
	ipv4 := true
	ipam := []network.IPAMConfig{{Subnet: subnet, Gateway: gw}}
	if s.IPv6 {
		ipam = append(ipam, network.IPAMConfig{Subnet: s.IPv6Subnet})
	}
	opts := map[string]string{
		"com.docker.network.bridge.name":       s.Bridge,
		"com.docker.network.bridge.enable_icc": "true",
	}
	if s.MTU > 0 {
		opts[mtuOption] = strconv.Itoa(s.MTU)
	}
	if _, err := c.api.NetworkCreate(ctx, s.Name, client.NetworkCreateOptions{
		Driver:     "bridge",
		EnableIPv4: &ipv4,
		EnableIPv6: &s.IPv6,
		Internal:   s.Internal,
		IPAM:       &network.IPAM{Driver: "default", Config: ipam},
		Options:    opts,
		Labels:     map[string]string{LabelManaged: "true", LabelNetwork: "true"},
	}); err != nil {
		return containers.Network{}, fmt.Errorf("create network %s: %w", s.Name, err)
	}
	return containers.Network{Name: s.Name, Bridge: s.Bridge, Subnet: subnet, Gateway: gw}, nil
}

// ErrSubnetTaken is returned when a network's subnet overlaps one already
// in use on the machine.
var ErrSubnetTaken = errors.New("subnet in use")

// LabelNetwork marks one of the owner's networks, as opposed to the server
// and install networks.
const LabelNetwork = "raptor.wings.network"

const mtuOption = "com.docker.network.driver.mtu"

// matches reports whether an existing network has a spec's settings.
func matches(n network.Network, s containers.NetworkSpec) bool {
	var v4, v6 netip.Prefix
	for _, cfg := range n.IPAM.Config {
		if cfg.Subnet.Addr().Is4() {
			v4 = cfg.Subnet
		} else {
			v6 = cfg.Subnet
		}
	}
	mtu := 0
	if v := n.Options[mtuOption]; v != "" {
		mtu, _ = strconv.Atoi(v)
	}
	return (!s.Subnet.IsValid() || v4 == s.Subnet) &&
		n.Internal == s.Internal && n.EnableIPv6 == s.IPv6 && (!s.IPv6 || v6 == s.IPv6Subnet) &&
		mtu == s.MTU && n.Options["com.docker.network.bridge.name"] == s.Bridge
}

// RemoveNetwork implements containers.Runtime.
func (c *Client) RemoveNetwork(ctx context.Context, name string) error {
	res, err := c.api.NetworkInspect(ctx, name, client.NetworkInspectOptions{})
	if cerrdefs.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect network %s: %w", name, err)
	}
	if res.Network.Labels[LabelManaged] != "true" {
		return fmt.Errorf("network %s wasn't created by Wings", name)
	}
	if n := len(res.Network.Containers); n > 0 {
		return fmt.Errorf("%w: %d containers are connected to %s", containers.ErrNetworkInUse, n, name)
	}
	if _, err := c.api.NetworkRemove(ctx, name, client.NetworkRemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove network %s: %w", name, err)
	}
	return nil
}

// HostNetworks implements containers.Runtime.
func (c *Client) HostNetworks(ctx context.Context) ([]containers.HostNetwork, error) {
	list, err := c.api.NetworkList(ctx, client.NetworkListOptions{})
	if err != nil {
		return nil, fmt.Errorf("list networks: %w", err)
	}
	out := make([]containers.HostNetwork, 0, len(list.Items))
	for _, n := range list.Items {
		h := containers.HostNetwork{Name: n.Name, Driver: n.Driver, Internal: n.Internal, Raptor: n.Labels[LabelManaged] == "true"}
		for _, cfg := range n.IPAM.Config {
			if cfg.Subnet.IsValid() {
				h.Subnets = append(h.Subnets, cfg.Subnet)
			}
		}
		// The list doesn't say what's connected; inspecting does.
		if res, err := c.api.NetworkInspect(ctx, n.ID, client.NetworkInspectOptions{}); err == nil {
			h.Containers = len(res.Network.Containers)
		}
		out = append(out, h)
	}
	return out, nil
}
