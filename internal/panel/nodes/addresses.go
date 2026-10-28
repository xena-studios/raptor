package nodes

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/panel/dns"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// Addresses records where each node connects from and keeps its hostname,
// <short-id>.<Domain>, pointing there (docs/ARCHITECTURE.md#node-dns).
type Addresses struct {
	Store  *Registry
	DNS    dns.Provider // nil: addresses are recorded, no DNS is written
	Domain string       // e.g. raptornodes.net
	Log    *slog.Logger
}

// ClientIP is the address a request came from. With header set (e.g.
// "CF-Connecting-IP"), it's taken from that header, which must only be
// trusted when the origin accepts nothing but the proxy that sets it
// (docs/SECURITY-MODEL.md#enrollment-and-identity).
func ClientIP(r *http.Request, header string) netip.Addr {
	if header != "" {
		if a, err := netip.ParseAddr(strings.TrimSpace(r.Header.Get(header))); err == nil {
			return a.Unmap()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

// cgnat is 100.64.0.0/10, shared address space no one outside can reach.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// public reports whether an address can be reached from the internet, so
// it's worth a DNS record.
func public(a netip.Addr) bool {
	return a.IsValid() && a.IsGlobalUnicast() && !a.IsPrivate() && !cgnat.Contains(a)
}

// Seen records that a node connected from addr and updates its hostname if
// the address changed. Call it when a node connects.
func (a *Addresses) Seen(ctx context.Context, h nodelink.Hello, addr netip.Addr) {
	if !addr.IsValid() {
		return
	}
	id, err := uuid.Parse(h.NodeID)
	if err != nil {
		return
	}
	q := a.Store.q()
	if addr.Is4() {
		err = q.SetNodeIPv4(ctx, store.SetNodeIPv4Params{ID: pgUUID(id), PublicIpv4: &addr})
	} else {
		err = q.SetNodeIPv6(ctx, store.SetNodeIPv6Params{ID: pgUUID(id), PublicIpv6: &addr})
	}
	if err != nil {
		a.log().Error("recording a node's address", "node", h.NodeID, "err", err)
		return
	}
	go a.syncWithRetry(context.WithoutCancel(ctx), h.NodeID)
}

func (a *Addresses) log() *slog.Logger {
	if a.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return a.Log
}

func (a *Addresses) syncWithRetry(ctx context.Context, nodeID string) {
	wait := 5 * time.Second
	for attempt := range 6 {
		err := a.Sync(ctx, nodeID)
		if err == nil {
			return
		}
		a.log().Warn("updating a node's DNS failed", "node", nodeID, "attempt", attempt+1, "err", err)
		time.Sleep(wait)
		wait *= 2
	}
}

// Records is what a node's A and AAAA records should hold (nil: none),
// from the addresses the Panel last saw it connect from and what its
// records hold now (docs/ARCHITECTURE.md#node-dns):
//   - A: its IPv4 address, once that's public; a node seen from a private
//     or CGNAT address keeps its last public record.
//   - AAAA: none while there's an A record, since game ports are published
//     on IPv4 only and a player's client preferring IPv6 would be sent
//     where no game listens. A node with no public IPv4 (IPv6-only) gets
//     its IPv6 address, so the name resolves at all.
func Records(seen4, seen6, dns4, dns6 *netip.Addr) (a, aaaa *netip.Addr) {
	a = dns4
	if seen4 != nil && public(*seen4) {
		a = seen4
	}
	if a != nil {
		return a, nil
	}
	aaaa = dns6
	if seen6 != nil && public(*seen6) {
		aaaa = seen6
	}
	return nil, aaaa
}

func sameAddr(x, y *netip.Addr) bool {
	if x == nil || y == nil {
		return x == y
	}
	return *x == *y
}

// Sync makes the node's records hold what Records says, if they don't.
func (a *Addresses) Sync(ctx context.Context, nodeID string) error {
	if a.DNS == nil || a.Domain == "" {
		return nil
	}
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return err
	}
	// One sync of a node at a time, on any instance: a node's connections
	// (its control connection and an address connection in each family)
	// each start one, and two reading the records before either wrote
	// could leave an AAAA record beside the A.
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	conn, err := a.Store.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock(hashtextextended('node-dns:' || $1::text, 0))", nodeID); err != nil {
		return err
	}
	defer func() {
		uctx := context.WithoutCancel(ctx)
		if _, err := conn.Exec(uctx, "SELECT pg_advisory_unlock(hashtextextended('node-dns:' || $1::text, 0))", nodeID); err != nil {
			_ = conn.Conn().Close(uctx) // a lock never released would block the node's syncs for good
		}
	}()
	q := store.New(conn)
	n, err := q.NodeDNS(ctx, pgUUID(id))
	if err != nil {
		return err
	}
	name := Hostname(n.ShortID, a.Domain)
	want4, want6 := Records(n.PublicIpv4, n.PublicIpv6, n.DnsIpv4, n.DnsIpv6)
	v4, v6 := n.DnsIpv4, n.DnsIpv6
	changed := false
	for _, r := range []struct {
		typ  string
		want *netip.Addr
		have **netip.Addr
	}{
		{"A", want4, &v4},
		{"AAAA", want6, &v6},
	} {
		if sameAddr(r.want, *r.have) {
			continue
		}
		var to netip.Addr // invalid: removes the record
		if r.want != nil {
			to = *r.want
		}
		if err := a.DNS.Set(ctx, name, r.typ, to); err != nil {
			return err
		}
		*r.have = r.want
		changed = true
	}
	if !changed {
		return nil
	}
	if err := q.SetNodeDNS(ctx, store.SetNodeDNSParams{ID: pgUUID(id), DnsIpv4: v4, DnsIpv6: v6}); err != nil {
		return errors.Join(errors.New("DNS updated but not recorded"), err)
	}
	a.log().Info("node DNS updated", "node", nodeID, "name", name)
	return nil
}

// Forget removes a removed node's DNS records, so its name stops pointing
// at the machine (names are never reused).
func (a *Addresses) Forget(ctx context.Context, nodeID string) error {
	if a.DNS == nil || a.Domain == "" {
		return nil
	}
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return err
	}
	q := a.Store.q()
	n, err := q.NodeDNS(ctx, pgUUID(id))
	if err != nil {
		return err
	}
	name := Hostname(n.ShortID, a.Domain)
	for _, typ := range []string{"A", "AAAA"} {
		if err := a.DNS.Set(ctx, name, typ, netip.Addr{}); err != nil {
			return err
		}
	}
	return q.SetNodeDNS(ctx, store.SetNodeDNSParams{ID: pgUUID(id)})
}

// ForgetWithRetry is Forget, retried for a while if Cloudflare fails.
func (a *Addresses) ForgetWithRetry(ctx context.Context, nodeID string) {
	wait := time.Second
	for range 8 {
		err := a.Forget(ctx, nodeID)
		if err == nil {
			return
		}
		a.log().Warn("removing a removed node's DNS records failed; retrying", "node", nodeID, "err", err)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return
		}
		wait *= 2
	}
	a.log().Error("gave up removing a removed node's DNS records", "node", nodeID)
}

// Retire deletes the records that should go (dns_retired: nodes' old
// n-<id> hostnames, player subdomains), every so often until there are
// none. Each is forgotten once it's deleted.
func (a *Addresses) Retire(ctx context.Context, every time.Duration) {
	if a.DNS == nil || a.Domain == "" {
		return
	}
	for {
		if err := a.retire(ctx); err != nil {
			a.log().Warn("removing retired DNS records failed; retrying", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (a *Addresses) retire(ctx context.Context) error {
	q := a.Store.q()
	rows, err := q.RetiredDNS(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		name := r.Name + "." + a.Domain
		if err := a.DNS.Remove(ctx, name, r.Type); err != nil {
			return err
		}
		if err := q.ForgetRetiredDNS(ctx, store.ForgetRetiredDNSParams(r)); err != nil {
			return err
		}
		a.log().Info("retired DNS record removed", "name", name, "type", r.Type)
	}
	return nil
}
