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
// n-<short-id>.<Domain>, pointing there (docs/ARCHITECTURE.md#node-dns).
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

// Sync writes the node's records if DNS doesn't have its current addresses
// (public ones only; a node connecting from a private address keeps its old
// records until it connects from a public one again).
func (a *Addresses) Sync(ctx context.Context, nodeID string) error {
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
	name := "n-" + n.ShortID + "." + a.Domain
	v4, v6 := n.DnsIpv4, n.DnsIpv6
	changed := false
	for _, r := range []struct {
		typ  string
		want *netip.Addr
		have **netip.Addr
	}{
		{"A", n.PublicIpv4, &v4},
		{"AAAA", n.PublicIpv6, &v6},
	} {
		if r.want == nil || !public(*r.want) || (*r.have != nil && **r.have == *r.want) {
			continue
		}
		if err := a.DNS.Set(ctx, name, r.typ, *r.want); err != nil {
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
