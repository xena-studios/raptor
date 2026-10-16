// Package machines keeps the Panel's view of each node's machine
// (docs/PANEL.md#hostname-and-time-zone): it reads the machine's hostname
// and time zone when the node connects (node.system), and keeps the
// hostname following the node's name unless an admin set one of their own.
package machines

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodecmd"
)

// UserID is who the Panel's own commands for this are from, in the node's
// audit log.
const UserID = "panel:machine"

// lockID is the advisory lock one Panel instance holds while it syncs.
const lockID = 0x7261_6d61 // "rama"

// Sender delivers a command to a node wherever it's connected
// (*nodes.Router).
type Sender interface {
	Execute(ctx context.Context, nodeID string, envelope []byte) (*nodev1.ExecuteResponse, error)
}

// Service syncs machines.
type Service struct {
	DB       *pgxpool.Pool
	Nodes    Sender
	PanelKey ed25519.PrivateKey
	Log      *slog.Logger

	kick chan struct{}
}

func (s *Service) init() {
	if s.kick == nil {
		s.kick = make(chan struct{}, 1)
	}
	if s.Log == nil {
		s.Log = slog.New(slog.DiscardHandler)
	}
}

// Init prepares the service before Run and Kick are called concurrently.
func (s *Service) Init() { s.init() }

// Kick asks for a sync now: a node connected, or one was renamed.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Run syncs every interval, and when kicked, until ctx ends. Every
// instance runs it; one at a time does the work.
func (s *Service) Run(ctx context.Context, every time.Duration) {
	s.init()
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-s.kick:
			// Let the connection settle (it's recorded as the node's after
			// the hello), and a burst of kicks become one sync.
			select {
			case <-ctx.Done():
				return
			case <-time.After(2 * time.Second):
			}
		}
		s.tick(ctx)
	}
}

func (s *Service) tick(ctx context.Context) {
	conn, err := s.DB.Acquire(ctx)
	if err != nil {
		return
	}
	defer conn.Release()
	var locked bool
	if err := conn.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", int64(lockID)).Scan(&locked); err != nil || !locked {
		return
	}
	defer func() { _, _ = conn.Exec(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", int64(lockID)) }()
	s.Sync(ctx)
}

// Sync reads the settings of nodes that connected since they were last
// read, and sends each the hostname it should have.
func (s *Service) Sync(ctx context.Context) {
	s.init()
	q := store.New(s.DB)
	rows, err := q.MachinesToSync(ctx)
	if err != nil {
		s.Log.Error("machines: listing nodes failed", "err", err)
		return
	}
	for _, n := range rows {
		node := uuid.UUID(n.ID.Bytes).String()
		hostname := n.Hostname
		if !n.Read {
			st, err := s.read(ctx, node)
			if err != nil {
				if ctx.Err() == nil {
					s.Log.Info("machines: couldn't read a node's settings; trying again later", "node", node, "err", err)
				}
				continue
			}
			if err := q.SetNodeSystem(ctx, store.SetNodeSystemParams{ID: n.ID, Hostname: st.Hostname, Timezone: st.Timezone}); err != nil {
				s.Log.Error("machines: recording a node's settings failed", "node", node, "err", err)
				continue
			}
			if st.Hostname != n.Hostname && n.Hostname != "" {
				n.HostnameTried = "" // changed on the machine: asked again
			}
			hostname = st.Hostname
		}
		want := Want(n.Name, n.ShortID, n.HostnameCustom)
		if hostname == want || n.HostnameTried == want {
			continue
		}
		note, err := s.SetHostname(ctx, n.ID, node, want)
		if err != nil && ctx.Err() == nil {
			s.Log.Info("machines: couldn't send a node its hostname; trying again later", "node", node, "err", err)
		} else if note != "" {
			s.Log.Info("machines: a node's hostname changed with a note", "node", node, "note", note)
		}
	}
}

type state struct {
	Hostname string `json:"hostname"`
	Timezone string `json:"timezone"`
}

func (s *Service) send(ctx context.Context, node, action string, params any) (*nodev1.ExecuteResponse, error) {
	env, err := nodecmd.New(s.PanelKey, node, UserID, action, "", params, time.Minute)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(env)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return s.Nodes.Execute(cctx, node, raw)
}

func (s *Service) read(ctx context.Context, node string) (state, error) {
	var st state
	res, err := s.send(ctx, node, "node.system", nil)
	if err != nil {
		return st, err
	}
	if res.GetError() != "" {
		return st, errors.New(res.GetError())
	}
	if err := json.Unmarshal(res.GetResult(), &st); err != nil {
		return st, err
	}
	st.Hostname, st.Timezone = truncate(st.Hostname, 253), truncate(st.Timezone, 64)
	return st, nil
}

// SetHostname asks the node for a hostname now and records its answer. An
// error is the node not answering; its refusal is recorded (and not asked
// again until the hostname it should have changes) and returned as an
// ErrRefused.
func (s *Service) SetHostname(ctx context.Context, id pgtype.UUID, node, hostname string) (note string, err error) {
	res, err := s.send(ctx, node, "node.hostname", map[string]string{"hostname": hostname})
	if err != nil {
		return "", err
	}
	refused := res.GetError()
	if err := store.New(s.DB).SetNodeHostnameTried(ctx, store.SetNodeHostnameTriedParams{ID: id, Tried: hostname, Error: truncate(refused, 500)}); err != nil {
		return "", err
	}
	if refused != "" {
		return "", ErrRefused{refused}
	}
	var r struct {
		Note string `json:"note"`
	}
	_ = json.Unmarshal(res.GetResult(), &r)
	return r.Note, nil
}

// ErrRefused is the node's reason for not taking a hostname.
type ErrRefused struct{ Reason string }

func (e ErrRefused) Error() string { return e.Reason }

var notHost = regexp.MustCompile(`[^a-z0-9]+`)

// Want is the hostname a node's machine should have: the admin's own, or
// the node's name made into one ("Basement Box #2" is basement-box-2), or
// n-<short id> for a name with nothing to keep.
func Want(name, shortID, custom string) string {
	if custom != "" {
		return custom
	}
	h := strings.Trim(notHost.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(h) > 63 {
		h = strings.TrimRight(h[:63], "-")
	}
	if h == "" {
		return "n-" + shortID
	}
	return h
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
