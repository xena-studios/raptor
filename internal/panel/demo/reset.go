// Package demo resets the public demo every hour (#449,
// docs/PANEL.md#the-public-demo): it removes the demo org's guests, with
// their sessions, and empties its showcase servers' trash.
//
// It doesn't restore the showcase servers from their known-good backups
// yet: a restore is passkey-signed, and the Panel holds no key a node
// trusts for it. That needs Phase 2's demo key, trusted by the demo node
// only (DECISIONS #375); until then staff restore a broken showcase server
// by hand (docs/DEPLOY.md#public-demo).
package demo

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// UserID is who the reset's commands are from.
const UserID = "panel:demo"

// Every is how often the demo resets.
const Every = time.Hour

const lockID = 0x7261_7074_6465_6d6f // "raptdemo"

// Sender delivers a command to a node wherever it's connected
// (*nodes.Router).
type Sender interface {
	Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error)
}

// Reset is the hourly reset.
type Reset struct {
	DB       *pgxpool.Pool
	Sender   Sender
	PanelKey ed25519.PrivateKey
	Log      *slog.Logger
	Now      func() time.Time
}

func (r *Reset) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Reset) log() *slog.Logger {
	if r.Log != nil {
		return r.Log
	}
	return slog.Default()
}

// Run checks every interval for a demo org due a reset, until ctx ends.
// Every Panel instance runs it; one at a time does the work.
func (r *Reset) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		if err := r.Tick(ctx); err != nil && ctx.Err() == nil {
			r.log().Warn("resetting the demo failed", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick resets each demo org whose last reset is an hour old or more. One
// Panel instance at a time.
func (r *Reset) Tick(ctx context.Context) error {
	conn, err := r.DB.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(lockID)).Scan(&locked); err != nil || !locked {
		return err
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(lockID)) }()
	orgs, err := store.New(r.DB).DemoOrgs(ctx)
	if err != nil {
		return err
	}
	for _, o := range orgs {
		if o.DemoResetAt.Valid && r.now().Sub(o.DemoResetAt.Time) < Every {
			continue
		}
		res, err := r.ResetOrg(ctx, o.ID)
		if err != nil {
			return err
		}
		r.log().Info("demo reset", "org", uuid.UUID(o.ID.Bytes), "guests", res.Guests, "trash_emptied", res.TrashEmptied,
			"trash_failed", res.TrashFailed, "restores_skipped", res.RestoresSkipped)
	}
	return nil
}

// Result is what a reset did.
type Result struct {
	// Guests removed, with their sessions.
	Guests int64
	// Showcase servers whose trash was emptied, and those whose node
	// didn't answer or refused.
	TrashEmptied, TrashFailed int
	// Showcase servers with a known-good backup that wasn't restored: a
	// restore needs Phase 2's demo key (DECISIONS #375).
	RestoresSkipped int
}

// ResetOrg resets one demo org now: its guests go, and its showcase
// servers' trash is emptied. A node that's offline is skipped until the
// next reset.
func (r *Reset) ResetOrg(ctx context.Context, org pgtype.UUID) (Result, error) {
	var res Result
	q := store.New(r.DB)
	n, err := q.DeleteOrgGuests(ctx, org)
	if err != nil {
		return res, err
	}
	res.Guests = n
	servers, err := q.DemoServers(ctx, org)
	if err != nil {
		return res, err
	}
	for _, s := range servers {
		if s.KnownGoodBackup != "" {
			res.RestoresSkipped++
		}
		if err := r.send(ctx, uuid.UUID(s.NodeID.Bytes).String(), "files.trash.empty", s.ServerID); err != nil {
			res.TrashFailed++
			r.log().Warn("emptying a demo server's trash failed", "node", uuid.UUID(s.NodeID.Bytes), "server", s.ServerID, "err", err)
			continue
		}
		res.TrashEmptied++
	}
	return res, q.SetDemoReset(ctx, store.SetDemoResetParams{ID: org, DemoResetAt: pgtype.Timestamptz{Time: r.now(), Valid: true}})
}

// send runs an unsigned command on a showcase server as the Panel.
func (r *Reset) send(ctx context.Context, node, action, server string) error {
	env, err := nodecmd.New(r.PanelKey, node, UserID, action, server, nil, time.Minute)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	res, err := r.Sender.Execute(cctx, node, raw)
	if err != nil {
		return err
	}
	if res.GetError() != "" {
		return errors.New(res.GetError())
	}
	return nil
}
