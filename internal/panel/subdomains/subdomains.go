// Package subdomains gives servers player-facing names on the node domain
// (docs/PANEL.md#subdomains): smp.raptornodes.net is a CNAME to the
// server's node (n-<short-id>.raptornodes.net), plus an SRV record with
// the server's port for Minecraft, so players type the name alone.
//
// Records follow the database, not the requests that change it: Sync
// compares what each subdomain should point at (its node, its server's
// port) with what it last wrote, and writes only the difference. A server
// or node that's gone releases its subdomain the same way.
package subdomains

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/xena-studios/raptor/internal/panel/nodes"
	"github.com/xena-studios/raptor/internal/panel/store"
)

// Records writes the zone's records (dns.Cloudflare).
type Records interface {
	// SetCNAME makes name an alias of target; "" removes it.
	SetCNAME(ctx context.Context, name, target string) error
	// SetSRV points name's SRV record at target's port; 0 removes it.
	SetSRV(ctx context.Context, name string, port int, target string) error
}

// Name rules.
var (
	nameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$`)

	// reserved are names Raptor keeps: its own services, mail and DNS
	// conventions, and names that would look like Raptor's.
	reserved = []string{
		"abuse", "admin", "api", "app", "assets", "auth", "autoconfig", "autodiscover", "billing", "blog",
		"cdn", "dashboard", "dev", "dns", "docs", "email", "ftp", "get", "help", "hostmaster", "imap",
		"login", "mail", "mx", "node", "nodes", "ns", "ns1", "ns2", "ns3", "ns4", "oauth", "panel", "pop",
		"pop3", "postmaster", "raptor", "raptornodes", "raptorpanel", "root", "security", "sftp", "smtp",
		"ssh", "staff", "staging", "static", "status", "support", "test", "verify", "webmail", "wings", "www",
	}
)

// Errors.
var (
	ErrName     = errors.New("a subdomain is 3 to 32 lowercase letters, digits, and hyphens, starting and ending with a letter or digit")
	ErrReserved = errors.New("that name is reserved")
	ErrTaken    = errors.New("that name is taken")
)

// CheckName says whether name can be a subdomain at all: the right shape,
// not reserved, not a node hostname (n-), and not punycode or doubled
// hyphens, which can pass for other names.
func CheckName(name string) error {
	switch {
	case !nameRE.MatchString(name):
		return ErrName
	case strings.Contains(name, "--"):
		return ErrName
	case strings.HasPrefix(name, "n-"), slices.Contains(reserved, name):
		return ErrReserved
	}
	return nil
}

// srvName is the record Minecraft's Java client asks for first.
func srvName(fqdn string) string { return "_minecraft._tcp." + fqdn }

// lockID is the advisory lock a sync holds, so one Panel instance writes
// at a time.
const lockID = 0x7261_7074_7375_6264 // "raptsubd"

// Syncer keeps subdomains' records in step with the database.
type Syncer struct {
	DB *pgxpool.Pool
	// DNS writes records (nil: names are kept, nothing is written).
	DNS Records
	// Domain is the node domain (raptornodes.net).
	Domain string
	Log    *slog.Logger
}

func (s *Syncer) log() *slog.Logger {
	if s.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return s.Log
}

// serverConfig is what Sync reads of a mirrored server's config.
type serverConfig struct {
	Allocations []struct {
		Port    int  `json:"port"`
		Primary bool `json:"primary"`
	} `json:"allocations"`
	Egg *struct {
		Players string `json:"players"`
	} `json:"egg"`
}

// srvPort is the port for a server's SRV record: its primary port, for
// Minecraft Java servers only; 0 for none.
func srvPort(raw []byte) int {
	var c serverConfig
	if json.Unmarshal(raw, &c) != nil || c.Egg == nil || c.Egg.Players != "minecraft" || len(c.Allocations) == 0 {
		return 0
	}
	port := c.Allocations[0].Port
	for _, a := range c.Allocations {
		if a.Primary {
			port = a.Port
		}
	}
	return port
}

// Run syncs every interval until ctx ends.
func (s *Syncer) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := s.Sync(ctx); err != nil && ctx.Err() == nil {
			s.log().Warn("subdomain sync failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Sync writes every subdomain's records that differ from what it should
// have, and releases subdomains whose server or node is gone. One Panel
// instance at a time; the others skip.
func (s *Syncer) Sync(ctx context.Context) error {
	if s.DNS == nil || s.Domain == "" {
		return nil
	}
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(lockID)).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(lockID)) }()

	q := store.New(s.DB)
	rows, err := q.SubdomainsToSync(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, r := range rows {
		target, port := "", 0
		active := !r.ReleasedAt.Valid
		if active && (r.NodeDeletedAt.Valid || !r.ServerExists) {
			if err := q.ReleaseSubdomainGone(ctx, r.ID); err != nil {
				return err
			}
			s.log().Info("subdomain released: its server or node is gone", "name", r.Name)
			active = false
		}
		if active {
			target, port = nodes.Hostname(r.ShortID, s.Domain), srvPort(r.ServerConfig)
		}
		if target == r.DnsTarget && int32(port) == r.DnsPort { //nolint:gosec // ports fit
			continue
		}
		fqdn := r.Name + "." + s.Domain
		err := s.DNS.SetCNAME(ctx, fqdn, target)
		if err == nil {
			err = s.DNS.SetSRV(ctx, srvName(fqdn), port, target)
		}
		msg := ""
		if err != nil {
			// Keep what was recorded, so the next sync tries again.
			target, port, msg = r.DnsTarget, int(r.DnsPort), err.Error()
			errs = append(errs, err)
			s.log().Warn("writing a subdomain's records failed", "name", r.Name, "err", err)
		} else {
			s.log().Info("subdomain records written", "name", r.Name, "target", target, "srv_port", port)
		}
		if err := q.SetSubdomainDNS(ctx, store.SetSubdomainDNSParams{ID: r.ID, DnsTarget: target, DnsPort: int32(port), DnsError: msg}); err != nil { //nolint:gosec // ports fit
			return err
		}
	}
	return errors.Join(errs...)
}
