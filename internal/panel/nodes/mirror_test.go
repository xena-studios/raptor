package nodes

import (
	"context"
	"crypto/ed25519"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/panel/store"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/link"
	"github.com/xena-studios/raptor/internal/wings/schedule"
	"github.com/xena-studios/raptor/internal/wings/server"
	wstore "github.com/xena-studios/raptor/internal/wings/store"
)

// fakeServers is the node's server manager.
type fakeServers struct {
	mu      sync.Mutex
	servers map[string]*server.Server
	states  map[string]server.State
}

func (f *fakeServers) List() map[string]server.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := map[string]server.State{}
	for id := range f.servers {
		out[id] = f.states[id]
	}
	return out
}

func (f *fakeServers) Get(_ context.Context, id string) (*server.Server, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.servers[id]
	if !ok {
		return nil, server.ErrNotFound
	}
	c := *s
	return &c, nil
}

func (f *fakeServers) Status(id string) (server.Status, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return server.Status{State: f.states[id]}, nil
}

func (f *fakeServers) set(id, name string, version int64, st server.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &server.Server{ID: id, Version: version, DesiredState: "running", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	s.Name = name
	f.servers[id], f.states[id] = s, st
}

// fakeChildren is the node's schedules and backups.
type fakeChildren struct {
	mu        sync.Mutex
	schedules map[string][]*schedule.Schedule
	backups   map[string][]*backup.Backup
}

func (f *fakeChildren) ListSchedules(_ context.Context, sid string) ([]*schedule.Schedule, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.schedules[sid], nil
}

func (f *fakeChildren) ListBackups(_ context.Context, sid string) ([]*backup.Backup, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.backups[sid], nil
}

type listFunc[T any] func(context.Context, string) ([]T, error)

func (l listFunc[T]) List(ctx context.Context, sid string) ([]T, error) { return l(ctx, sid) }

func (f *fakeServers) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.servers, id)
}

// A real node (link, outbox) and a real Panel (hub, registry, mirror on
// Postgres): the mirror follows the node's servers, and rebuilds after it's
// dropped or falls out of step.
func TestMirror(t *testing.T) {
	r := newRegistry(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	hub := &Hub{PanelKey: r.PanelKey, NodeKey: r.NodeKey}
	defer hub.Close()
	mirror := &Mirror{DB: r.DB, Hub: hub}
	hub.EventsAvailable = func(_ context.Context, id string, _ int64) { mirror.Notify(id) }
	mux := http.NewServeMux()
	mux.Handle("GET "+nodelink.Path, hub)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	org, _ := r.CreateOrg(ctx, "org")
	token, _ := r.CreateJoinToken(ctx, org, "")
	_, nodeKey, _ := ed25519.GenerateKey(nil)
	res, err := r.Enroll(ctx, enrollReq(t, token, nodeKey))
	if err != nil {
		t.Fatal(err)
	}
	nodeID := res.GetNodeId()

	db, err := wstore.Open(ctx, filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	outbox := events.New(db)
	fs := &fakeServers{servers: map[string]*server.Server{}, states: map[string]server.State{}}
	fs.set("s1", "Survival", 1, server.Running)
	kids := &fakeChildren{schedules: map[string][]*schedule.Schedule{}, backups: map[string][]*backup.Backup{}}
	engine := jobs.New(jobs.Options{Store: db, Events: outbox, LogDir: t.TempDir(), Poll: 50 * time.Millisecond})
	engine.Register("files.compress", jobs.Handler{Run: func(context.Context, jobs.Job, io.Writer) (any, error) { return nil, nil }})
	if err := engine.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	l := link.New(link.Config{
		PanelURL: srv.URL, NodeID: nodeID, NodeKey: nodeKey, PanelKey: res.GetPanelKey(),
		Events: outbox, Servers: func() link.Servers { return fs },
		Schedules: func() link.Schedules { return listFunc[*schedule.Schedule](kids.ListSchedules) },
		Backups:   func() link.Backups { return listFunc[*backup.Backup](kids.ListBackups) },
		Jobs:      engine,
	})
	go func() { _ = l.Run(ctx) }()

	type row struct{ name, state string }
	rows := func() map[string]row {
		id, _ := uuid.Parse(nodeID)
		list, err := store.New(r.DB).ListMirrorServers(ctx, pgUUID(id))
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]row{}
		for _, s := range list {
			out[s.ServerID] = row{s.Name, s.State}
		}
		return out
	}
	waitFor := func(what string, ok func(map[string]row) bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			got := rows()
			if ok(got) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: mirror is %v", what, got)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	appendEvent := func(typ, sid string, data map[string]any) {
		if _, err := outbox.Append(ctx, events.Event{Type: typ, ServerID: sid, Data: data}); err != nil {
			t.Fatal(err)
		}
	}

	// The first sync is a snapshot.
	waitFor("snapshot", func(m map[string]row) bool { return m["s1"] == row{"Survival", "running"} })

	// A new server, a rename, a state change, a deletion.
	fs.set("s2", "Creative", 1, server.Installing)
	appendEvent("server.created", "s2", nil)
	waitFor("created", func(m map[string]row) bool { return m["s2"] == row{"Creative", "installing"} })
	fs.set("s1", "Survival 2", 2, server.Running)
	appendEvent("server.updated", "s1", nil)
	waitFor("updated", func(m map[string]row) bool { return m["s1"].name == "Survival 2" })
	appendEvent("server.state", "s2", map[string]any{"state": "running"})
	waitFor("state", func(m map[string]row) bool { return m["s2"].state == "running" })
	// A schedule and a backup on s2, then the backup finishing.
	kids.mu.Lock()
	sc := &schedule.Schedule{ID: "sc1", ServerID: "s2", Version: 1, NextRun: time.Now().Add(time.Hour)}
	sc.Name, sc.Cron, sc.Enabled = "Restart", "0 4 * * *", true
	kids.schedules["s2"] = []*schedule.Schedule{sc}
	kids.backups["s2"] = []*backup.Backup{{ID: "b1", ServerID: "s2", Kind: "manual", Status: "running", CreatedAt: time.Now()}}
	kids.mu.Unlock()
	appendEvent("schedule.created", "s2", nil)
	children := func() (int, string) {
		id, _ := uuid.Parse(nodeID)
		scs, err := store.New(r.DB).ListMirrorSchedules(ctx, store.ListMirrorSchedulesParams{NodeID: pgUUID(id), ServerID: "s2"})
		if err != nil {
			t.Fatal(err)
		}
		bks, err := store.New(r.DB).ListMirrorBackups(ctx, store.ListMirrorBackupsParams{NodeID: pgUUID(id), ServerID: "s2"})
		if err != nil {
			t.Fatal(err)
		}
		status := ""
		if len(bks) == 1 {
			status = bks[0].Status
		}
		return len(scs), status
	}
	waitChildren := func(what string, wantSchedules int, wantBackup string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			n, st := children()
			if n == wantSchedules && st == wantBackup {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s: %d schedules, backup %q", what, n, st)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitChildren("schedule and backup", 1, "running")
	kids.mu.Lock()
	kids.backups["s2"][0].Status = "ok"
	kids.mu.Unlock()
	appendEvent("backup.finished", "s2", nil)
	waitChildren("backup finished", 1, "ok")

	// The node turning SFTP on is a node event: it lands on the node's row.
	appendEvent("node.sftp", "", map[string]any{"enabled": true, "port": 2022, "host_key_fingerprint": "SHA256:hk"})
	deadline := time.Now().Add(10 * time.Second)
	for {
		var on bool
		var port int32
		var hk string
		if err := r.DB.QueryRow(ctx, "SELECT sftp_enabled, sftp_port, sftp_host_key FROM nodes WHERE id = $1", nodeID).Scan(&on, &port, &hk); err != nil {
			t.Fatal(err)
		}
		if on && port == 2022 && hk == "SHA256:hk" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("node sftp: %v %d %q", on, port, hk)
		}
		time.Sleep(50 * time.Millisecond)
	}

	// A job on s2: its status changes are events, and the mirror follows
	// them to the end.
	job, err := engine.Enqueue(ctx, jobs.Spec{Type: "files.compress", ServerID: "s2"})
	if err != nil {
		t.Fatal(err)
	}
	mirroredJob := func() string {
		id, _ := uuid.Parse(nodeID)
		list, err := store.New(r.DB).ListMirrorJobs(ctx, store.ListMirrorJobsParams{NodeID: pgUUID(id), ServerID: "s2"})
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range list {
			if j.JobID == job {
				return j.Type + " " + j.Status
			}
		}
		return ""
	}
	for deadline := time.Now().Add(10 * time.Second); mirroredJob() != "files.compress succeeded"; time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("job: mirrored as %q", mirroredJob())
		}
	}

	// Schedule runs: a run from its start to its end, a skipped run, and a
	// run whose start the Panel never saw; then the schedule deleted.
	runs := func(schedule string) []string {
		id, _ := uuid.Parse(nodeID)
		list, err := store.New(r.DB).ListScheduleRuns(ctx, store.ListScheduleRunsParams{NodeID: pgUUID(id), ServerID: "s2", ScheduleID: schedule, Lim: 10})
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range list {
			out = append(out, x.Status+" "+x.Reason+" "+x.SkipReason+" "+string(x.Steps))
		}
		return out
	}
	waitRuns := func(what, schedule string, want ...string) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !slices.Equal(runs(schedule), want); time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("%s: runs are %q", what, runs(schedule))
			}
		}
	}
	appendEvent("schedule.run.queued", "s2", map[string]any{"schedule_id": "sc1", "job_id": "j1", "reason": "manual"})
	waitRuns("queued", "sc1", "running manual  []")
	appendEvent("schedule.run.skipped", "s2", map[string]any{"schedule_id": "sc1", "reason": "offline", "scheduled_for": time.Now().UnixMilli()})
	appendEvent("schedule.run.finished", "s2", map[string]any{
		"schedule_id": "sc1", "job_id": "j1", "reason": "manual", "ok": false, "error": "step 1",
		"steps": []map[string]any{{"type": "command", "ok": false, "error": "server isn't running"}},
	})
	appendEvent("schedule.run.finished", "s2", map[string]any{"schedule_id": "sc2", "job_id": "j2", "reason": "scheduled", "ok": true, "steps": []map[string]any{{"type": "wait", "ok": true}}})
	waitRuns("finished", "sc1",
		"skipped scheduled offline []",
		`failed manual  [{"ok": false, "type": "command", "error": "server isn't running"}]`)
	waitRuns("finished alone", "sc2", `succeeded scheduled  [{"ok": true, "type": "wait"}]`)
	appendEvent("schedule.deleted", "s2", map[string]any{"schedule_id": "sc1"})
	waitRuns("schedule deleted", "", `succeeded scheduled  [{"ok": true, "type": "wait"}]`)

	fs.remove("s1")
	appendEvent("server.deleted", "s1", nil)
	waitFor("deleted", func(m map[string]row) bool { _, ok := m["s1"]; return !ok && len(m) == 1 })

	// Dropped: rebuilt from a snapshot on the next sync.
	if err := mirror.Reset(ctx, nodeID); err != nil {
		t.Fatal(err)
	}
	fs.set("s3", "Modded", 1, server.Offline)
	mirror.Notify(nodeID)
	waitFor("rebuilt", func(m map[string]row) bool {
		ids := make([]string, 0, len(m))
		for id := range m {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		return slices.Equal(ids, []string{"s2", "s3"})
	})

	// The Panel ahead of the node (its state.db restored from a snapshot):
	// the node says so, and the mirror takes a snapshot.
	id, _ := uuid.Parse(nodeID)
	if err := store.New(r.DB).SetNodeAcked(ctx, store.SetNodeAckedParams{ID: pgUUID(id), LastAckedSeq: 9999}); err != nil {
		t.Fatal(err)
	}
	fs.set("s4", "Late", 1, server.Offline)
	mirror.Notify(nodeID)
	waitFor("resynced", func(m map[string]row) bool { _, ok := m["s4"]; return ok })
	if acked, _ := store.New(r.DB).GetNodeAcked(ctx, pgUUID(id)); acked == 9999 {
		t.Error("acked wasn't reset by the snapshot")
	}
	if mirroredJob() != "files.compress succeeded" {
		t.Errorf("job after the snapshots: %q", mirroredJob())
	}
}
