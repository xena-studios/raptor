// Package networks manages the owner's own server networks
// (docs/NETWORKS.md): Docker bridges Wings creates beside the server
// network, each kept apart from the others by Raptor's firewall table, so
// groups of servers on one node can't reach each other.
package networks

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Max is how many networks a node has at most.
const Max = 32

// Network is one of the owner's networks.
type Network struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Subnet is its IPv4 range; empty when saving picks a free one, or
	// keeps the one it has.
	Subnet string `json:"subnet"`
	// Internet is whether its servers can reach the internet. Players
	// reach their ports either way.
	Internet bool `json:"internet"`
	IPv6     bool `json:"ipv6"`
	// MTU is 0 for Docker's default.
	MTU int `json:"mtu"`
	// HostAccess is whether its servers can connect to the machine itself.
	HostAccess bool `json:"host_access"`

	// Reported, never saved: its gateway, IPv6 range, and the servers on
	// it (primary or not).
	Gateway    string   `json:"gateway,omitempty"`
	IPv6Subnet string   `json:"ipv6_subnet,omitempty"`
	Servers    []string `json:"servers"`
	// Addresses are servers' fixed addresses on it, by server ID, once
	// they've started there.
	Addresses map[string]string `json:"addresses"`
	CreatedAt int64             `json:"created_at,omitempty"`
}

// Errors, matched with errors.Is. Their messages are sentences for people.
var (
	ErrInvalid  = errors.New("invalid network")
	ErrNotFound = server.ErrNoNetwork
	// ErrInUse: servers are on the network, or running on it.
	ErrInUse = errors.New("network in use")
)

type invalid struct{ msg string }

func (e invalid) Error() string        { return e.msg }
func (e invalid) Is(target error) bool { return target == ErrInvalid }

type inUse struct{ msg string }

func (e inUse) Error() string        { return e.msg }
func (e inUse) Is(target error) bool { return target == ErrInUse }

// Runtime is what the service needs of the container runtime.
type Runtime interface {
	EnsureNetwork(ctx context.Context, s containers.NetworkSpec) (containers.Network, error)
	RemoveNetwork(ctx context.Context, name string) error
	HostNetworks(ctx context.Context) ([]containers.HostNetwork, error)
}

// Options configure the service.
type Options struct {
	Store   *store.DB
	Runtime Runtime
	Log     *slog.Logger
	// Changed, if set, is called after the networks or the rules between
	// them changed, with the firewall's view of them, to reload it.
	Changed func(ctx context.Context, nets []firewall.Network, between []firewall.Between) error
	// Reserved are ranges a network can't use: the server and install
	// networks'.
	Reserved []netip.Prefix
}

// Service manages the networks.
type Service struct {
	o    Options
	mu   sync.Mutex
	live map[string]containers.Network // by ID, once ensured on Docker
}

// New returns the service. Call Reconcile before servers start.
func New(o Options) *Service {
	if o.Log == nil {
		o.Log = slog.Default()
	}
	return &Service{o: o, live: map[string]containers.Network{}}
}

// DockerName is a network's name on Docker.
func DockerName(id string) string { return "raptor_net_" + suffix(id) }

// Bridge is a network's interface on the host: "rn" and 12 hex digits,
// inside Linux's 15-character limit.
func Bridge(id string) string { return "rn" + suffix(id) }

// suffix is the random end of a version 7 UUID, so networks made in the
// same moment differ.
func suffix(id string) string {
	h := strings.ReplaceAll(id, "-", "")
	if len(h) < 12 {
		sum := sha256.Sum256([]byte(id))
		return fmt.Sprintf("%x", sum[:6])
	}
	return h[len(h)-12:]
}

// ipv6Subnet is a network's ULA /64, the same every time: fd, 40 bits from
// its ID, and subnet 0 (RFC 4193).
func ipv6Subnet(id string) netip.Prefix {
	sum := sha256.Sum256([]byte("raptor-network:" + id))
	var a [16]byte
	a[0] = 0xfd
	copy(a[1:6], sum[:5])
	return netip.PrefixFrom(netip.AddrFrom16(a), 64)
}

func (s *Service) spec(r store.Network) containers.NetworkSpec {
	sp := containers.NetworkSpec{
		Name: DockerName(r.ID), Bridge: Bridge(r.ID), Internal: r.Internal == 1,
		IPv6: r.Ipv6 == 1, MTU: int(r.Mtu), Pinned: true,
	}
	sp.Subnet, _ = netip.ParsePrefix(r.Subnet)
	if sp.IPv6 {
		sp.IPv6Subnet = ipv6Subnet(r.ID)
	}
	return sp
}

// Reconcile creates every network that's missing on Docker, and recreates
// any whose settings differ. One that fails is logged and tried again
// when a server on it starts.
func (s *Service) Reconcile(ctx context.Context) error {
	rows, err := s.o.Store.Read.ListNetworks(ctx)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range rows {
		n, err := s.o.Runtime.EnsureNetwork(ctx, s.spec(r))
		if err != nil {
			s.o.Log.Error("network setup failed", "network", r.Name, "id", r.ID, "err", err)
			continue
		}
		s.live[r.ID] = n
	}
	return nil
}

// Resolve implements server.Networks.
func (s *Service) Resolve(ctx context.Context, ids []string) ([]containers.Network, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]containers.Network, 0, len(ids))
	for _, id := range ids {
		if n, ok := s.live[id]; ok {
			out = append(out, n)
			continue
		}
		r, err := s.o.Store.Read.GetNetwork(ctx, id)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, fmt.Errorf("%w: %s", ErrNotFound, id)
			}
			return nil, err
		}
		n, err := s.o.Runtime.EnsureNetwork(ctx, s.spec(r))
		if err != nil {
			return nil, fmt.Errorf("network %s: %w", r.Name, err)
		}
		s.live[id] = n
		out = append(out, n)
	}
	return out, nil
}

// Firewall is the networks and the rules between them for the firewall.
func (s *Service) Firewall(ctx context.Context) ([]firewall.Network, []firewall.Between) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.firewallLocked(ctx), s.betweenLocked(ctx)
}

func (s *Service) firewallLocked(ctx context.Context) []firewall.Network {
	rows, err := s.o.Store.Read.ListNetworks(ctx)
	if err != nil {
		s.o.Log.Error("listing networks for the firewall failed", "err", err)
	}
	var out []firewall.Network
	for _, r := range rows {
		f := firewall.Network{Bridge: Bridge(r.ID), HostAccess: r.HostAccess == 1}
		if n, ok := s.live[r.ID]; ok {
			f.Gateway = n.Gateway
		}
		out = append(out, f)
	}
	return out
}

// List returns the networks, with the servers on each.
func (s *Service) List(ctx context.Context) ([]Network, error) {
	rows, err := s.o.Store.Read.ListNetworks(ctx)
	if err != nil {
		return nil, err
	}
	on, err := s.serversOn(ctx)
	if err != nil {
		return nil, err
	}
	addrs, err := s.addresses(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Network, 0, len(rows))
	for _, r := range rows {
		n := s.report(r, on[r.ID])
		n.Addresses = addrs[r.ID]
		if n.Addresses == nil {
			n.Addresses = map[string]string{}
		}
		out = append(out, n)
	}
	return out, nil
}

func (s *Service) report(r store.Network, servers []string) Network {
	n := Network{
		ID: r.ID, Name: r.Name, Subnet: r.Subnet, Internet: r.Internal == 0, IPv6: r.Ipv6 == 1,
		MTU: int(r.Mtu), HostAccess: r.HostAccess == 1, Servers: servers, CreatedAt: r.CreatedAt,
	}
	if n.Servers == nil {
		n.Servers = []string{}
	}
	n.Addresses = map[string]string{}
	if l, ok := s.live[r.ID]; ok {
		n.Gateway = l.Gateway.String()
	}
	if n.IPv6 {
		n.IPv6Subnet = ipv6Subnet(r.ID).String()
	}
	return n
}

// serversOn maps each network's ID to the servers on it.
func (s *Service) serversOn(ctx context.Context) (map[string][]string, error) {
	rows, err := s.o.Store.Read.ListServerNetworks(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string][]string{}
	for _, r := range rows {
		var ids []string
		if r.Networks != "" {
			if err := json.Unmarshal([]byte(r.Networks), &ids); err != nil {
				return nil, fmt.Errorf("server %s networks: %w", r.ID, err)
			}
		}
		for _, id := range ids {
			out[id] = append(out[id], r.ID)
		}
	}
	return out, nil
}

// Host lists every network on the machine's Docker.
func (s *Service) Host(ctx context.Context) ([]containers.HostNetwork, error) {
	return s.o.Runtime.HostNetworks(ctx)
}

// validate checks a network as saved and normalizes it.
func (s *Service) validate(n *Network) error {
	n.Name = strings.TrimSpace(n.Name)
	if n.Name == "" || utf8.RuneCountInString(n.Name) > 40 {
		return invalid{"a network's name is 1 to 40 characters"}
	}
	if strings.ContainsFunc(n.Name, func(r rune) bool { return r < ' ' || r == 0x7f }) {
		return invalid{"a network's name can't have control characters"}
	}
	if n.Subnet = strings.TrimSpace(n.Subnet); n.Subnet != "" {
		p, err := netip.ParsePrefix(n.Subnet)
		if err != nil || !p.Addr().Is4() {
			return invalid{"the subnet is an IPv4 range like 172.30.0.0/16"}
		}
		if p.Bits() < 16 || p.Bits() > 28 {
			return invalid{"the subnet is between a /16 (65,534 addresses) and a /28 (14)"}
		}
		if !p.Addr().IsPrivate() {
			return invalid{"the subnet is a private range: in 10.0.0.0/8, 172.16.0.0/12, or 192.168.0.0/16"}
		}
		p = p.Masked()
		for _, r := range s.o.Reserved {
			if p.Overlaps(r) {
				return invalid{fmt.Sprintf("the subnet overlaps %s, which Raptor's own networks use", r)}
			}
		}
		n.Subnet = p.String()
	}
	if n.MTU != 0 && (n.MTU < 1280 || n.MTU > 9000) {
		return invalid{"the MTU is between 1280 and 9000, or empty for the default"}
	}
	return nil
}

// Save creates a network (with no ID) or changes one. Changing its subnet,
// internet access, IPv6, or MTU recreates it on Docker, which is refused
// while any server on it runs.
func (s *Service) Save(ctx context.Context, n Network) (Network, error) {
	if err := s.validate(&n); err != nil {
		return Network{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.o.Store.Read.ListNetworks(ctx)
	if err != nil {
		return Network{}, err
	}
	var cur *store.Network
	for i, r := range rows {
		if r.ID == n.ID {
			cur = &rows[i]
		} else if strings.EqualFold(r.Name, n.Name) {
			return Network{}, invalid{fmt.Sprintf("there's already a network called %s", r.Name)}
		} else if n.Subnet != "" {
			if p, err := netip.ParsePrefix(r.Subnet); err == nil && p.Overlaps(netip.MustParsePrefix(n.Subnet)) {
				return Network{}, invalid{fmt.Sprintf("the subnet overlaps %s's (%s)", r.Name, r.Subnet)}
			}
		}
	}
	if n.ID != "" && cur == nil {
		return Network{}, fmt.Errorf("%w: %s", ErrNotFound, n.ID)
	}
	if cur == nil && len(rows) >= Max {
		return Network{}, invalid{fmt.Sprintf("a node has at most %d networks", Max)}
	}

	now := time.Now().Unix()
	row := store.Network{
		ID: n.ID, Name: n.Name, Subnet: n.Subnet, Internal: boolInt(!n.Internet), Ipv6: boolInt(n.IPv6),
		Mtu: int64(n.MTU), HostAccess: boolInt(n.HostAccess), CreatedAt: now, UpdatedAt: now,
	}
	if cur == nil {
		id, err := uuid.NewV7()
		if err != nil {
			return Network{}, err
		}
		row.ID = id.String()
	} else {
		row.CreatedAt = cur.CreatedAt
		if row.Subnet == "" {
			row.Subnet = cur.Subnet
		}
	}

	// Docker first: it picks the subnet, and refuses what it can't do.
	live, err := s.o.Runtime.EnsureNetwork(ctx, s.spec(row))
	if errors.Is(err, containers.ErrNetworkInUse) {
		return Network{}, inUse{"stop the servers on this network first: changing its subnet, internet access, IPv6, or MTU recreates it"}
	}
	if err != nil {
		return Network{}, err
	}
	row.Subnet = live.Subnet.String()
	err = s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if cur == nil {
			return q.InsertNetwork(ctx, store.InsertNetworkParams(row))
		}
		// Its servers' fixed addresses were in the old range: they get
		// new ones at their next start.
		if row.Subnet != cur.Subnet {
			if err := q.DeleteNetworkAddresses(ctx, row.ID); err != nil {
				return err
			}
		}
		return q.UpdateNetwork(ctx, store.UpdateNetworkParams{
			Name: row.Name, Subnet: row.Subnet, Internal: row.Internal, Ipv6: row.Ipv6,
			Mtu: row.Mtu, HostAccess: row.HostAccess, UpdatedAt: row.UpdatedAt, ID: row.ID,
		})
	})
	if err != nil {
		if cur == nil {
			_ = s.o.Runtime.RemoveNetwork(context.WithoutCancel(ctx), DockerName(row.ID))
		}
		return Network{}, err
	}
	s.live[row.ID] = live
	s.changed(ctx)

	on, err := s.serversOn(ctx)
	if err != nil {
		return Network{}, err
	}
	return s.report(row, on[row.ID]), nil
}

// Delete removes a network no server is on.
func (s *Service) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, err := s.o.Store.Read.GetNetwork(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrNotFound, id)
	}
	if err != nil {
		return err
	}
	on, err := s.serversOn(ctx)
	if err != nil {
		return err
	}
	if n := len(on[id]); n > 0 {
		return inUse{fmt.Sprintf("%s has %d %s on it; move them to another network first", r.Name, n, plural(n, "server", "servers"))}
	}
	if err := s.o.Runtime.RemoveNetwork(ctx, DockerName(id)); err != nil {
		if errors.Is(err, containers.ErrNetworkInUse) {
			return inUse{"containers Raptor doesn't manage are connected to this network on Docker; disconnect them first"}
		}
		return err
	}
	if err := s.o.Store.Write.DeleteNetwork(ctx, id); err != nil {
		return err
	}
	if err := s.dropRulesFor(ctx, id); err != nil {
		s.o.Log.Error("removing the deleted network's rules failed", "network", r.Name, "err", err)
	}
	delete(s.live, id)
	s.changed(ctx)
	return nil
}

// changed reloads the firewall; called with mu held.
func (s *Service) changed(ctx context.Context) {
	if s.o.Changed == nil {
		return
	}
	if err := s.o.Changed(ctx, s.firewallLocked(ctx), s.betweenLocked(ctx)); err != nil {
		s.o.Log.Error("reloading the firewall after a network change failed", "err", err)
	}
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

var _ server.Networks = (*Service)(nil)
