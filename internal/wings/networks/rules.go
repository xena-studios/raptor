package networks

import (
	"context"
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"unicode/utf8"

	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Servers' fixed addresses: Docker hands out the lower half of a network's
// range (IPRange), and each server on it is pinned to an address in the
// upper half the first time it starts there, so a rule naming one server
// matches its address and nothing else's.

// halves splits a network's range: Docker's half, and the pinned half.
func halves(p netip.Prefix) (dynamic, pinned netip.Prefix) {
	p = p.Masked()
	bits := p.Bits() + 1
	dynamic = netip.PrefixFrom(p.Addr(), bits)
	base := binary.BigEndian.Uint32(p.Addr().AsSlice())
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], base+uint32(1)<<(32-bits))
	return dynamic, netip.PrefixFrom(netip.AddrFrom4(b), bits)
}

// freeAddress is the first address in the pinned half not taken, short
// of the range's broadcast address.
func freeAddress(subnet netip.Prefix, taken map[netip.Addr]bool) (netip.Addr, error) {
	_, half := halves(subnet)
	last := lastAddr(subnet)
	for a := half.Addr(); half.Contains(a) && a != last; a = a.Next() {
		if !taken[a] {
			return a, nil
		}
	}
	return netip.Addr{}, invalid{fmt.Sprintf("the network's range %s has no addresses left for more servers", subnet)}
}

func lastAddr(p netip.Prefix) netip.Addr {
	base := binary.BigEndian.Uint32(p.Masked().Addr().AsSlice())
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], base|(uint32(1)<<(32-p.Bits())-1))
	return netip.AddrFrom4(b)
}

// Pin returns a server's fixed address on each of these networks, by
// Docker network name, pinning one where it has none, and forgets the
// ones on networks it's no longer on.
func (s *Service) Pin(ctx context.Context, serverID string, ids []string) (map[string]netip.Addr, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[string]netip.Addr{}
	changed := false
	err := s.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		all, err := q.ListServerAddresses(ctx)
		if err != nil {
			return err
		}
		mine := map[string]netip.Addr{}
		taken := map[string]map[netip.Addr]bool{}
		for _, a := range all {
			ip, err := netip.ParseAddr(a.Ip)
			if err != nil {
				continue
			}
			if taken[a.NetworkID] == nil {
				taken[a.NetworkID] = map[netip.Addr]bool{}
			}
			taken[a.NetworkID][ip] = true
			if a.ServerID == serverID {
				mine[a.NetworkID] = ip
			}
		}
		for id := range mine {
			if !slices.Contains(ids, id) {
				if err := q.DeleteServerAddress(ctx, store.DeleteServerAddressParams{ServerID: serverID, NetworkID: id}); err != nil {
					return err
				}
				changed = true
			}
		}
		for _, id := range ids {
			ip, ok := mine[id]
			if !ok {
				r, err := q.GetNetwork(ctx, id)
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("%w: %s", ErrNotFound, id)
				}
				if err != nil {
					return err
				}
				subnet, err := netip.ParsePrefix(r.Subnet)
				if err != nil {
					return fmt.Errorf("network %s: %w", r.Name, err)
				}
				if ip, err = freeAddress(subnet, taken[id]); err != nil {
					return err
				}
				if err := q.InsertServerAddress(ctx, store.InsertServerAddressParams{ServerID: serverID, NetworkID: id, Ip: ip.String()}); err != nil {
					return err
				}
				changed = true
			}
			out[DockerName(id)] = ip
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if changed {
		s.changed(ctx)
	}
	return out, nil
}

// addresses maps each network's ID to its servers' fixed addresses.
func (s *Service) addresses(ctx context.Context) (map[string]map[string]string, error) {
	rows, err := s.o.Store.Read.ListServerAddresses(ctx)
	if err != nil {
		return nil, err
	}
	out := map[string]map[string]string{}
	for _, r := range rows {
		if out[r.NetworkID] == nil {
			out[r.NetworkID] = map[string]string{}
		}
		out[r.NetworkID][r.ServerID] = r.Ip
	}
	return out, nil
}

// Rule lets connections from a network, or one server on it, to another
// network, or one server on it (docs/NETWORKS.md#rules-between-networks).
type Rule struct {
	From       string `json:"from"` // a network's ID
	FromServer string `json:"from_server,omitempty"`
	To         string `json:"to"`
	ToServer   string `json:"to_server,omitempty"`
	Protocol   string `json:"protocol,omitempty"` // tcp, udp, or both (default)
	// Ports is a comma-separated list of ports and ranges; empty is every
	// port.
	Ports string `json:"ports,omitempty"`
	Note  string `json:"note,omitempty"`
}

// MaxRules bounds the rules between a node's networks.
const MaxRules = 100

const rulesKey = "networks.rules"

// Rules returns the rules between networks.
func (s *Service) Rules(ctx context.Context) ([]Rule, error) {
	return s.rules(ctx, s.o.Store.Read)
}

func (s *Service) rules(ctx context.Context, q *store.Queries) ([]Rule, error) {
	b, err := q.GetKV(ctx, rulesKey)
	if errors.Is(err, sql.ErrNoRows) {
		return []Rule{}, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Rule
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("rules between networks: %w", err)
	}
	if out == nil {
		out = []Rule{}
	}
	return out, nil
}

// SetRules checks the rules between networks, saves them, and reloads the
// firewall.
func (s *Service) SetRules(ctx context.Context, rules []Rule) error {
	if len(rules) > MaxRules {
		return invalid{fmt.Sprintf("at most %d rules between networks", MaxRules)}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.o.Store.Read.ListNetworks(ctx)
	if err != nil {
		return err
	}
	names := map[string]string{}
	for _, r := range rows {
		names[r.ID] = r.Name
	}
	on, err := s.serversOn(ctx)
	if err != nil {
		return err
	}
	for i := range rules {
		r := &rules[i]
		bad := func(format string, a ...any) error {
			return invalid{fmt.Sprintf("rule %d: %s", i+1, fmt.Sprintf(format, a...))}
		}
		if names[r.From] == "" || names[r.To] == "" {
			return bad("pick a network to allow from and one to allow to")
		}
		if r.From == r.To {
			return bad("servers on one network already reach each other")
		}
		if r.FromServer != "" && !slices.Contains(on[r.From], r.FromServer) {
			return bad("that server isn't on %s", names[r.From])
		}
		if r.ToServer != "" && !slices.Contains(on[r.To], r.ToServer) {
			return bad("that server isn't on %s", names[r.To])
		}
		if r.Protocol, r.Ports, err = firewall.CheckPorts(r.Protocol, r.Ports); err != nil {
			return bad("%v", err)
		}
		if utf8.RuneCountInString(r.Note) > 100 {
			return bad("the note is at most 100 characters")
		}
		if slices.Contains(rules[:i], *r) {
			return bad("it's listed twice")
		}
	}
	b, err := json.Marshal(rules)
	if err != nil {
		return err
	}
	if err := s.o.Store.Write.SetKV(ctx, store.SetKVParams{Key: rulesKey, Value: b}); err != nil {
		return err
	}
	s.changed(ctx)
	return nil
}

// dropRulesFor removes the rules naming a network, when it's deleted.
func (s *Service) dropRulesFor(ctx context.Context, id string) error {
	rules, err := s.rules(ctx, s.o.Store.Read)
	if err != nil {
		return err
	}
	kept := slices.DeleteFunc(rules, func(r Rule) bool { return r.From == id || r.To == id })
	b, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	return s.o.Store.Write.SetKV(ctx, store.SetKVParams{Key: rulesKey, Value: b})
}

// betweenLocked is the rules as the firewall needs them. A rule naming a
// server that has no address on that network yet (it hasn't started
// there) lets nothing through until it has.
func (s *Service) betweenLocked(ctx context.Context) []firewall.Between {
	rules, err := s.rules(ctx, s.o.Store.Read)
	if err != nil {
		s.o.Log.Error("reading the rules between networks failed", "err", err)
		return nil
	}
	addrs, err := s.addresses(ctx)
	if err != nil {
		s.o.Log.Error("reading servers' addresses failed", "err", err)
		return nil
	}
	subnets := map[string]netip.Prefix{}
	if rows, err := s.o.Store.Read.ListNetworks(ctx); err == nil {
		for _, n := range rows {
			subnets[n.ID], _ = netip.ParsePrefix(n.Subnet)
		}
	}
	var out []firewall.Between
	for _, r := range rules {
		if !subnets[r.From].IsValid() || !subnets[r.To].IsValid() {
			continue
		}
		b := firewall.Between{
			FromBridge: Bridge(r.From), FromSubnet: subnets[r.From], ToBridge: Bridge(r.To), ToSubnet: subnets[r.To],
			Protocol: r.Protocol, Ports: r.Ports,
		}
		ok := true
		for _, side := range []struct {
			server, network string
			addr            *netip.Addr
		}{{r.FromServer, r.From, &b.From}, {r.ToServer, r.To, &b.To}} {
			if side.server == "" {
				continue
			}
			a, err := netip.ParseAddr(addrs[side.network][side.server])
			if err != nil {
				ok = false
				break
			}
			*side.addr = a
		}
		if ok {
			out = append(out, b)
		}
	}
	return out
}
