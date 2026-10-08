package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"sync"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel/metric"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/panel/telemetry"
)

// Mirror keeps the Panel's copy of each node's servers
// (docs/ARCHITECTURE.md#mirror-sync). Events say what changed; the node is
// then asked for those servers as they are now, so the mirror holds what
// Wings has, never a replay of what it said. A node without a mirror yet,
// or one that fell behind what the node keeps, gets a snapshot.
type Mirror struct {
	DB  *pgxpool.Pool
	Hub *Hub
	Log *slog.Logger

	once    sync.Once
	mu      sync.Mutex
	pending map[string]chan struct{} // a worker per node with work queued
}

// Events per pull.
const eventBatch = 500

// Notify asks for a sync of a node: it's coalesced with one already queued,
// and a node is synced by one worker at a time.
func (m *Mirror) Notify(nodeID string) {
	m.once.Do(func() {
		m.pending = map[string]chan struct{}{}
		if m.Log == nil {
			m.Log = slog.New(slog.DiscardHandler)
		}
	})
	m.mu.Lock()
	ch, running := m.pending[nodeID]
	if !running {
		ch = make(chan struct{}, 1)
		m.pending[nodeID] = ch
	}
	m.mu.Unlock()
	select {
	case ch <- struct{}{}:
	default: // a sync is already queued; it'll see this
	}
	if !running {
		go m.worker(nodeID, ch)
	}
}

// Mirror syncs that failed (raptor.mirror.sync_failures): pages show stale
// data until a later sync works.
var syncFailures, _ = telemetry.Meter.Int64Counter("raptor.mirror.sync_failures", metric.WithDescription("Mirror syncs that failed"))

func (m *Mirror) worker(nodeID string, ch chan struct{}) {
	for {
		select {
		case <-ch:
		case <-time.After(time.Minute):
			m.mu.Lock()
			if len(ch) == 0 {
				delete(m.pending, nodeID)
				m.mu.Unlock()
				return
			}
			m.mu.Unlock()
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		if err := m.Sync(ctx, nodeID); err != nil {
			m.Log.Warn("mirror sync failed", "node", nodeID, "err", err)
			syncFailures.Add(context.Background(), 1)
		}
		cancel()
	}
}

// Sync brings a node's mirror up to date.
func (m *Mirror) Sync(ctx context.Context, nodeID string) error {
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return err
	}
	node := pgUUID(id)
	c, ok := m.Hub.Conn(nodeID)
	if !ok {
		return nil // it syncs when it's back
	}
	q := store.New(m.DB)
	acked, err := q.GetNodeAcked(ctx, node)
	if err != nil {
		return err
	}
	if acked < 0 {
		return m.snapshot(ctx, node, c)
	}
	for {
		res, err := c.Node.Events(ctx, &nodev1.EventsRequest{AfterSeq: acked, Limit: eventBatch})
		if connect.CodeOf(err) == connect.CodeFailedPrecondition {
			m.Log.Info("mirror needs a snapshot", "node", nodeID, "reason", err)
			return m.snapshot(ctx, node, c)
		}
		if err != nil {
			return err
		}
		evs := res.GetEvents()
		if len(evs) == 0 {
			return nil
		}
		// State changes come with the event; anything else about a server
		// means asking for it.
		states := map[string]string{}
		fetch := map[string]bool{}
		// The node's SFTP as of its last node.sftp event in the batch.
		var sftp *sftpState
		for _, e := range evs {
			sid := e.GetServerId()
			if sid == "" {
				if e.GetType() == "node.sftp" {
					var d sftpState
					if json.Unmarshal(e.GetData(), &d) == nil {
						sftp = &d
					}
				}
				continue
			}
			switch e.GetType() {
			case "server.state":
				var d struct {
					State string `json:"state"`
				}
				if json.Unmarshal(e.GetData(), &d) == nil && d.State != "" {
					states[sid] = d.State
				}
			case "server.console.command":
			default:
				fetch[sid] = true
			}
		}
		var servers *nodev1.GetServersResponse
		if len(fetch) > 0 {
			ids := slices.Sorted(maps.Keys(fetch))
			if servers, err = c.Node.GetServers(ctx, &nodev1.GetServersRequest{Ids: ids}); err != nil {
				return err
			}
		}
		last := evs[len(evs)-1].GetSeq()
		err = pgx.BeginFunc(ctx, m.DB, func(tx pgx.Tx) error {
			q := store.New(tx)
			for sid, st := range states {
				if err := q.SetMirrorServerState(ctx, store.SetMirrorServerStateParams{NodeID: node, ServerID: sid, State: st}); err != nil {
					return err
				}
			}
			if sftp != nil {
				if err := q.SetNodeSFTP(ctx, store.SetNodeSFTPParams{ID: node, SftpEnabled: sftp.Enabled, SftpPort: sftp.Port, SftpHostKey: sftp.HostKey}); err != nil {
					return err
				}
			}
			// Fetched after the events, so newer than their states.
			if servers != nil {
				if err := applyServers(ctx, q, node, servers); err != nil {
					return err
				}
			}
			return q.SetNodeAcked(ctx, store.SetNodeAckedParams{ID: node, LastAckedSeq: last})
		})
		if err != nil {
			return err
		}
		acked = last
		if len(evs) < eventBatch {
			return nil
		}
	}
}

// sftpState is a node.sftp event's data.
type sftpState struct {
	Enabled bool   `json:"enabled"`
	Port    int32  `json:"port"`
	HostKey string `json:"host_key_fingerprint"`
}

// snapshot replaces a node's mirror with every server it has now.
func (m *Mirror) snapshot(ctx context.Context, node pgtype.UUID, c *Conn) error {
	res, err := c.Node.GetServers(ctx, &nodev1.GetServersRequest{})
	if err != nil {
		return err
	}
	return pgx.BeginFunc(ctx, m.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.DeleteMirrorServers(ctx, node); err != nil {
			return err
		}
		if err := applyServers(ctx, q, node, res); err != nil {
			return err
		}
		// Every event up to last_seq is reflected; resume after it.
		return q.SetNodeAcked(ctx, store.SetNodeAckedParams{ID: node, LastAckedSeq: res.GetLastSeq()})
	})
}

func applyServers(ctx context.Context, q *store.Queries, node pgtype.UUID, res *nodev1.GetServersResponse) error {
	for _, s := range res.GetServers() {
		cfg := s.GetConfig()
		if !json.Valid(cfg) {
			return errors.New("node sent a server config that isn't JSON")
		}
		err := q.UpsertMirrorServer(ctx, store.UpsertMirrorServerParams{
			NodeID: node, ServerID: s.GetId(), Name: s.GetName(), Version: s.GetVersion(), State: s.GetState(),
			DesiredState: s.GetDesiredState(), InstallState: s.GetInstallState(), InstallError: s.GetInstallError(),
			EggName: s.GetEggName(), EggSource: s.GetEggSource(), Config: cfg,
			CreatedAt: millis(s.GetCreatedAt()), UpdatedAt: millis(s.GetUpdatedAt()),
		})
		if err != nil {
			return err
		}
		if err := applyChildren(ctx, q, node, s); err != nil {
			return err
		}
	}
	for _, id := range res.GetMissing() {
		if err := q.DeleteMirrorServer(ctx, store.DeleteMirrorServerParams{NodeID: node, ServerID: id}); err != nil {
			return err
		}
	}
	return nil
}

// applyChildren replaces a server's schedules, backups, and jobs with the node's.
func applyChildren(ctx context.Context, q *store.Queries, node pgtype.UUID, s *nodev1.Server) error {
	sid := s.GetId()
	if err := q.DeleteMirrorSchedules(ctx, store.DeleteMirrorSchedulesParams{NodeID: node, ServerID: sid}); err != nil {
		return err
	}
	for _, sc := range s.GetSchedules() {
		def := sc.GetDefinition()
		if !json.Valid(def) {
			return errors.New("node sent a schedule definition that isn't JSON")
		}
		if err := q.InsertMirrorSchedule(ctx, store.InsertMirrorScheduleParams{
			NodeID: node, ServerID: sid, ScheduleID: sc.GetId(), Name: sc.GetName(), Enabled: sc.GetEnabled(),
			Version: sc.GetVersion(), NextRun: optMillis(sc.GetNextRun()), LastRun: optMillis(sc.GetLastRun()), Definition: def,
		}); err != nil {
			return err
		}
	}
	if err := q.DeleteMirrorBackups(ctx, store.DeleteMirrorBackupsParams{NodeID: node, ServerID: sid}); err != nil {
		return err
	}
	for _, b := range s.GetBackups() {
		if err := q.InsertMirrorBackup(ctx, store.InsertMirrorBackupParams{
			NodeID: node, ServerID: sid, BackupID: b.GetId(), Kind: b.GetKind(), Status: b.GetStatus(), Locked: b.GetLocked(),
			Size: b.GetSize(), Files: b.GetFiles(), DestinationID: b.GetDestinationId(), Error: b.GetError(),
			Warning: b.GetWarning(), CreatedBy: b.GetCreatedBy(), CreatedAt: optMillis(b.GetCreatedAt()),
			FinishedAt: optMillis(b.GetFinishedAt()), ExpiresAt: optMillis(b.GetExpiresAt()),
		}); err != nil {
			return err
		}
	}
	if err := q.DeleteMirrorJobs(ctx, store.DeleteMirrorJobsParams{NodeID: node, ServerID: sid}); err != nil {
		return err
	}
	for _, j := range s.GetJobs() {
		if err := q.InsertMirrorJob(ctx, store.InsertMirrorJobParams{
			NodeID: node, ServerID: sid, JobID: j.GetId(), Type: j.GetType(), Status: j.GetStatus(), Attempts: j.GetAttempts(),
			Error: j.GetError(), CreatedAt: optMillis(j.GetCreatedAt()), StartedAt: optMillis(j.GetStartedAt()),
			FinishedAt: optMillis(j.GetFinishedAt()),
		}); err != nil {
			return err
		}
	}
	return nil
}

func millis(ms int64) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: time.UnixMilli(ms), Valid: true}
}

// optMillis is a nullable time: 0 is unset.
func optMillis(ms int64) pgtype.Timestamptz {
	if ms == 0 {
		return pgtype.Timestamptz{}
	}
	return millis(ms)
}

// Reset drops a node's mirror; the next sync rebuilds it from a snapshot.
func (m *Mirror) Reset(ctx context.Context, nodeID string) error {
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return err
	}
	node := pgUUID(id)
	return pgx.BeginFunc(ctx, m.DB, func(tx pgx.Tx) error {
		q := store.New(tx)
		if err := q.DeleteMirrorServers(ctx, node); err != nil {
			return err
		}
		return q.SetNodeAcked(ctx, store.SetNodeAckedParams{ID: node, LastAckedSeq: -1})
	})
}
