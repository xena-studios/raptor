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
