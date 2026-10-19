package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// Alerts queues the alerts a batch of events raises, in the transaction
	// that applies it (alerts.Service.Ingest); nil: none.
	Alerts func(ctx context.Context, q *store.Queries, node pgtype.UUID, evs []*nodev1.Event) error

	once    sync.Once
	mu      sync.Mutex
	pending map[string]chan struct{} // a worker per node with work queued
	behind  map[string]backlog       // nodes with events not applied yet
}

// backlog is a node's events the mirror hasn't applied: up to head, the
// oldest waiting since since.
type backlog struct {
	head  int64
	since time.Time
}

// lagWarning is how far behind a node's mirror gets before it's logged.
const lagWarning = time.Minute

// Event lag (docs/ARCHITECTURE.md#mirror-sync): how old events are when
// the mirror applies them, and the worst node's backlog now. Not per node,
// so the series don't grow with the nodes; a node that falls behind is
// logged.
var eventLag, _ = telemetry.Meter.Float64Histogram("raptor.mirror.event_lag", metric.WithUnit("s"),
	metric.WithDescription("How old a node's events are when the mirror applies them (the oldest of each batch)"),
	metric.WithExplicitBucketBoundaries(0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 300, 900))

// ObserveMetrics reports the mirror's backlog: how many nodes have events
// it hasn't applied (raptor.mirror.nodes_behind), and how long the worst
// has waited (raptor.mirror.backlog_age).
func (m *Mirror) ObserveMetrics() error {
	behind, err := telemetry.Meter.Int64ObservableGauge("raptor.mirror.nodes_behind",
		metric.WithDescription("Nodes with events the mirror hasn't applied yet"))
	if err != nil {
		return err
	}
	age, err := telemetry.Meter.Float64ObservableGauge("raptor.mirror.backlog_age", metric.WithUnit("s"),
		metric.WithDescription("How long the oldest unapplied node event has waited, across nodes"))
	if err != nil {
		return err
	}
	_, err = telemetry.Meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		n, oldest := m.backlog(time.Now())
		o.ObserveInt64(behind, int64(n))
		o.ObserveFloat64(age, oldest.Seconds())
		return nil
	}, behind, age)
	return err
}

// backlog is how many nodes are behind and the longest wait.
func (m *Mirror) backlog(now time.Time) (int, time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var oldest time.Duration
	for _, b := range m.behind {
		oldest = max(oldest, now.Sub(b.since))
	}
	return len(m.behind), oldest
}

// Available is a node saying it has events up to lastSeq: the mirror is
// behind until it applies them, and a sync is queued.
func (m *Mirror) Available(nodeID string, lastSeq int64) {
	m.init()
	m.mu.Lock()
	b, ok := m.behind[nodeID]
	if !ok {
		b.since = time.Now()
	}
	b.head = max(b.head, lastSeq)
	m.behind[nodeID] = b
	m.mu.Unlock()
	m.Notify(nodeID)
}

// applied records that the mirror has a node's events up to seq.
func (m *Mirror) applied(nodeID string, seq int64) {
	m.init()
	m.mu.Lock()
	if b, ok := m.behind[nodeID]; ok && seq >= b.head {
		delete(m.behind, nodeID)
	}
	m.mu.Unlock()
}

// forget drops a node that isn't connected here: it's synced, and counted,
// where it connects next.
func (m *Mirror) forget(nodeID string) {
	m.init()
	m.mu.Lock()
	delete(m.behind, nodeID)
	m.mu.Unlock()
}

func (m *Mirror) init() {
	m.once.Do(func() {
		m.pending = map[string]chan struct{}{}
		m.behind = map[string]backlog{}
		if m.Log == nil {
			m.Log = slog.New(slog.DiscardHandler)
		}
	})
}

// Events per pull.
const eventBatch = 500

// Notify asks for a sync of a node: it's coalesced with one already queued,
// and a node is synced by one worker at a time.
func (m *Mirror) Notify(nodeID string) {
	m.init()
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
		m.forget(nodeID)
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
			m.applied(nodeID, acked)
			return nil
		}
		// State changes come with the event; anything else about a server
		// means asking for it.
		states := map[string]string{}
		fetch := map[string]bool{}
		// The node's SFTP as of its last node.sftp event in the batch.
		var sftp *sftpState
		var runs []*nodev1.Event
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
			case "server.console.command", "security.audit", "alert.fired", "alert.resolved":
				// Nothing about the server itself changed.
			case "schedule.run.queued", "schedule.run.skipped", "schedule.run.finished",
				"schedule.deleted", "server.deleted":
				runs = append(runs, e)
				fetch[sid] = true
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
			for _, e := range runs {
				if err := applyRun(ctx, q, node, e); err != nil {
					return err
				}
			}
			// Fetched after the events, so newer than their states.
			if servers != nil {
				if err := applyServers(ctx, q, node, servers); err != nil {
					return err
				}
			}
			if m.Alerts != nil {
				if err := m.Alerts(ctx, q, node, evs); err != nil {
					return fmt.Errorf("alerts: %w", err)
				}
			}
			return q.SetNodeAcked(ctx, store.SetNodeAckedParams{ID: node, LastAckedSeq: last})
		})
		if err != nil {
			return err
		}
		acked = last
		if lag := time.Since(time.UnixMilli(evs[0].GetAt())); evs[0].GetAt() > 0 {
			eventLag.Record(ctx, max(lag, 0).Seconds())
			if lag > lagWarning {
				m.Log.Warn("mirror is behind a node", "node", nodeID, "lag", lag.Round(time.Second), "events", len(evs))
			}
		}
		if len(evs) < eventBatch {
			m.applied(nodeID, acked)
			return nil
		}
	}
}

// keepRuns is how many runs of each schedule the Panel keeps.
const keepRuns = 100

// runEvent is a schedule.run.* event's data.
type runEvent struct {
	ScheduleID   string          `json:"schedule_id"`
	JobID        string          `json:"job_id"`
	Reason       string          `json:"reason"`
	ScheduledFor int64           `json:"scheduled_for"`
	OK           bool            `json:"ok"`
	Steps        json.RawMessage `json:"steps"`
	Error        string          `json:"error"`
}

// applyRun records a schedule's run from its events, and forgets a deleted
// schedule's or server's runs.
func applyRun(ctx context.Context, q *store.Queries, node pgtype.UUID, e *nodev1.Event) error {
	if e.GetType() == "server.deleted" {
		return q.DeleteServerScheduleRuns(ctx, store.DeleteServerScheduleRunsParams{NodeID: node, ServerID: e.GetServerId()})
	}
	// Unreadable data has nothing to record; not worth stopping the mirror for.
	var d runEvent
	if err := json.Unmarshal(e.GetData(), &d); err != nil || d.ScheduleID == "" {
		return nil //nolint:nilerr // see above
	}
	at := millis(e.GetAt())
	switch e.GetType() {
	case "schedule.deleted":
		return q.DeleteScheduleRuns(ctx, store.DeleteScheduleRunsParams{NodeID: node, ScheduleID: d.ScheduleID})
	case "schedule.run.queued":
		if d.JobID == "" {
			return nil
		}
		err := q.StartScheduleRun(ctx, store.StartScheduleRunParams{
			NodeID: node, ServerID: e.GetServerId(), ScheduleID: d.ScheduleID, RunKey: d.JobID,
			Status: "running", Reason: d.Reason, ScheduledFor: optMillis(d.ScheduledFor), StartedAt: at,
		})
		if err != nil {
			return err
		}
	case "schedule.run.skipped":
		// A skipped run's reason is why it was skipped.
		err := q.StartScheduleRun(ctx, store.StartScheduleRunParams{
			NodeID: node, ServerID: e.GetServerId(), ScheduleID: d.ScheduleID, RunKey: fmt.Sprintf("skip:%d", e.GetSeq()),
			Status: "skipped", Reason: "scheduled", SkipReason: d.Reason, ScheduledFor: optMillis(d.ScheduledFor),
			StartedAt: at, FinishedAt: at,
		})
		if err != nil {
			return err
		}
	case "schedule.run.finished":
		if d.JobID == "" {
			return nil
		}
		status := "failed"
		if d.OK {
			status = "succeeded"
		}
		steps := []byte(d.Steps)
		if !json.Valid(steps) || string(steps) == "null" {
			steps = []byte("[]")
		}
		err := q.FinishScheduleRun(ctx, store.FinishScheduleRunParams{
			NodeID: node, ServerID: e.GetServerId(), ScheduleID: d.ScheduleID, RunKey: d.JobID,
			Status: status, Reason: d.Reason, Steps: steps, Error: d.Error, FinishedAt: at,
		})
		if err != nil {
			return err
		}
	}
	return q.TrimScheduleRuns(ctx, store.TrimScheduleRunsParams{NodeID: node, ScheduleID: d.ScheduleID, Keep: keepRuns})
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
	err = pgx.BeginFunc(ctx, m.DB, func(tx pgx.Tx) error {
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
	if err == nil {
		m.applied(c.NodeID, res.GetLastSeq())
	}
	return err
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
			FinishedAt: optMillis(b.GetFinishedAt()), ExpiresAt: optMillis(b.GetExpiresAt()), Databases: nonNil(b.GetDatabases()),
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

// nonNil is a list for a NOT NULL array column.
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
