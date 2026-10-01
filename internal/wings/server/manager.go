package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Errors returned by power actions and installs.
var (
	ErrNotFound     = errors.New("server not found")
	ErrInstalling   = errors.New("server is installing")
	ErrNotInstalled = errors.New("server isn't installed; reinstall it or enable skip install")
	ErrRunning      = errors.New("server is running; stop it first")
	ErrDiskLimit    = errors.New("server is over its disk limit; free space or raise the limit")
	ErrClosed       = errors.New("server manager is shut down")
)

// Options configure a Manager.
type Options struct {
	Runtime containers.Runtime
	Store   *store.DB
	Log     *slog.Logger
	// Storage is the server data volume and its quotas.
	Storage *storage.Volume

	VolumesDir string // server directories: <VolumesDir>/<id>
	// MachineIDDir holds each server's /etc/machine-id (<dir>/<id>), written
	// before every start. "" = no machine-id.
	MachineIDDir string
	TmpDir       string // install scripts
	LogDir       string // install logs: <LogDir>/install/<id>.log

	// UID and GID the server containers run as (the raptor user).
	UID, GID int
	// Timezone (TZ) and Location (P_SERVER_LOCATION) for every server.
	Timezone, Location string
	// DockerInterface is the server network's gateway, for
	// {{config.docker.interface}} in config files.
	DockerInterface string
	// ReservedPorts are Wings' own ports, never allocatable.
	ReservedPorts []int
	// Jobs runs installs (register before Jobs.Start). Its "install" class
	// limit is the number of parallel installs.
	Jobs *jobs.Engine
	// Events is the outbox every server event is recorded in.
	Events *events.Outbox
	// StartStagger spaces out starts after a reboot (default 3s).
	StartStagger time.Duration

	// OOMKills, if set, returns the kernel's OOM kill count for all server
	// containers (host.OOMKills on raptor.slice). Docker doesn't reliably
	// report OOM kills, so a SIGKILL exit Wings didn't cause is attributed to
	// the OOM killer when this count went up.
	OOMKills func() (int64, error)

	// Deleted, if set, is called after a server was deleted (its local
	// backups are deleted with it).
	Deleted func(ctx context.Context, id string)
	// JobBackup, if set, backs up a stopped server inside one of its jobs
	// (kind BackupSafety before a wipe, BackupFinal before a deletion) and
	// returns the backup's ID ("" for an empty directory). Without it,
	// wiping and final backups are refused.
	JobBackup func(ctx context.Context, id, jobID, kind string, log io.Writer) (string, error)
	// DiskCheck, if set, returns an error while the host disk is too low
	// for installs (host.DiskGuard.Err).
	DiskCheck func() error

	// Crash policy overrides, for tests.
	CrashWindow time.Duration
	CrashDelays []time.Duration
}

// Manager owns every server on the node.
type Manager struct {
	o      Options
	log    *slog.Logger
	events bus

	oomMu   sync.Mutex
	oomSeen int64 // OOM kills already attributed to an exit

	mu      sync.Mutex
	servers map[string]*instance
	ctx     context.Context // cancelled by Close
	cancel  context.CancelFunc
	wg      sync.WaitGroup
}

// New creates a Manager. Call Reconcile once before using it.
func New(o Options) *Manager {
	if o.StartStagger == 0 {
		o.StartStagger = 3 * time.Second
	}
	if o.CrashWindow == 0 {
		o.CrashWindow = crashWindow
	}
	if o.CrashDelays == nil {
		o.CrashDelays = crashDelays
	}
	if o.Log == nil {
		o.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	m := &Manager{
		o:       o,
		log:     o.Log,
		servers: map[string]*instance{},
		ctx:     ctx,
		cancel:  cancel,
	}
	if o.OOMKills != nil {
		m.oomSeen, _ = o.OOMKills()
	}
	if o.Storage != nil && o.Storage.Soft {
		m.goTracked(m.softLimitLoop)
	}
	o.Jobs.Register(JobInstall, jobs.Handler{
		Class:       "install",
		ServerLock:  true,
		Resumable:   true, // an install runs its script over the files again
		MaxAttempts: 3,
		Run:         m.installJob,
	})
	o.Jobs.Register(JobDelete, jobs.Handler{
		Class:       "backup",
		ServerLock:  true,
		Resumable:   true, // the final backup is found again, or retried
		MaxAttempts: 3,
		Run:         m.deleteJob,
	})
	return m
}

// diskCheck returns an error while the host disk is too low for installs.
func (m *Manager) diskCheck() error {
	if m.o.DiskCheck == nil {
		return nil
	}
	return m.o.DiskCheck()
}

// JobInstall is the install/reinstall job type.
const JobInstall = "server.install"

type installPayload struct {
	StartAfter bool `json:"start_after,omitempty"`
	// Wipe: take a safety backup and remove every file before the script
	// runs ("wipe and reinstall").
	Wipe bool `json:"wipe,omitempty"`
}

// InstallOptions change a reinstall.
type InstallOptions struct {
	// Wipe removes every file before the script runs, after a safety
	// backup.
	Wipe bool
}

// oomKilled reports whether an unexplained SIGKILL was the OOM killer: the
// kernel's count went up since the last exit it explained. Each kill is
// attributed to one exit.
func (m *Manager) oomKilled() bool {
	if m.o.OOMKills == nil {
		return false
	}
	m.oomMu.Lock()
	defer m.oomMu.Unlock()
	n, err := m.o.OOMKills()
	if err != nil || n <= m.oomSeen {
		return false
	}
	m.oomSeen++
	return true
}

// Events subscribes to server events. Call the returned function to stop.
func (m *Manager) Events() (<-chan Event, func()) { return m.events.subscribe(256) }

// publish records an event in the outbox and tells in-process subscribers.
func (m *Manager) publish(typ, id string, version int64, data map[string]any) {
	e := Event{Type: typ, ServerID: id, Version: version, Time: time.Now(), Data: data}
	if _, err := m.o.Events.Append(context.WithoutCancel(m.ctx), outboxEvent(e)); err != nil {
		m.log.Error("recording event failed", "server", id, "event", typ, "err", err)
	}
	m.emit(e)
}

// publishTx records an event inside a transaction; call emit after commit.
func publishTx(ctx context.Context, q *store.Queries, e Event) error {
	_, err := events.AppendTx(ctx, q, outboxEvent(e))
	return err
}

func outboxEvent(e Event) events.Event {
	return events.Event{Type: e.Type, ServerID: e.ServerID, Version: e.Version, At: e.Time, Data: e.Data}
}

// emit logs an event and tells in-process subscribers (after it's recorded).
func (m *Manager) emit(e Event) {
	m.o.Events.Wake()
	typ, id, data := e.Type, e.ServerID, e.Data
	attrs := []any{"server", id, "event", typ}
	for k, v := range data {
		if k != "console" {
			attrs = append(attrs, k, v)
		}
	}
	m.log.Info("server event", attrs...)
	m.events.publish(e)
}

func newEvent(typ, id string, version int64, data map[string]any) Event {
	return Event{Type: typ, ServerID: id, Version: version, Time: time.Now(), Data: data}
}

// goTracked runs fn in a goroutine Close waits for. After Close it does
// nothing (checked under m.mu, which Close holds while cancelling).
func (m *Manager) goTracked(fn func()) {
	m.mu.Lock()
	if m.ctx.Err() != nil {
		m.mu.Unlock()
		return
	}
	m.wg.Add(1)
	m.mu.Unlock()
	go func() {
		defer m.wg.Done()
		fn()
	}()
}

// Close stops the job engine (running installs resume on the next start) and
// detaches from every server without stopping any: containers keep running
// and the next Wings reattaches to them (docs/SERVERS.md).
func (m *Manager) Close() {
	m.o.Jobs.Close()
	m.mu.Lock()
	m.cancel()
	for _, i := range m.servers {
		i.detach()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func (m *Manager) instance(id string) (*instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ctx.Err() != nil {
		return nil, ErrClosed
	}
	i, ok := m.servers[id]
	if !ok {
		return nil, ErrNotFound
	}
	return i, nil
}

// Get returns a server's stored configuration.
func (m *Manager) Get(ctx context.Context, id string) (*Server, error) {
	row, err := m.o.Store.Read.GetServer(ctx, id)
	if err != nil {
		return nil, ErrNotFound
	}
	allocs, err := m.o.Store.Read.ListServerAllocations(ctx, id)
	if err != nil {
		return nil, err
	}
	return fromRow(row, allocs)
}

// Status is a server's live status.
type Status struct {
	State   State
	Console *Console
}

// Status returns a server's live state and console.
func (m *Manager) Status(id string) (Status, error) {
	i, err := m.instance(id)
	if err != nil {
		return Status{}, err
	}
	return Status{State: i.getState(), Console: i.console}, nil
}

// List returns every server's ID and live state.
func (m *Manager) List() map[string]State {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]State, len(m.servers))
	for id, i := range m.servers {
		out[id] = i.getState()
	}
	return out
}

// CreateOptions control what happens after a server is created.
type CreateOptions struct {
	StartAfterInstall bool
	// Import, if set, fills the new server's directory instead of the
	// egg's install script (a server moved from another panel): the server
	// is created installed. If it fails, the server is removed again.
	Import func(ctx context.Context, dir string) error
	// InTx, if set, runs in the transaction that stores the server, so what
	// it adds (the default backup schedule) exists with it or not at all.
	InTx func(ctx context.Context, q *store.Queries, id string) error
}

// Create stores a new server and starts its install in the background.
func (m *Manager) Create(ctx context.Context, cfg Config, opts CreateOptions) (string, error) {
	if _, err := cfg.validate(m.o.ReservedPorts); err != nil {
		return "", err
	}
	if err := m.diskCheck(); err != nil {
		return "", err
	}
	id, err := uuid.NewV7()
	if err != nil {
		return "", err
	}
	sid := id.String()
	if err := m.checkHostPorts(cfg.Allocations); err != nil {
		return "", err
	}
	created := newEvent(EventCreated, sid, 1, nil)
	// The server, its event, and its install job exist together or not at all.
	err = m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := checkConflicts(ctx, q, sid, cfg.Allocations); err != nil {
			return err
		}
		if err := q.InsertServer(ctx, store.InsertServerParams{
			ID: sid, Name: cfg.Name, Egg: cfg.Egg, EggSource: cfg.EggSource, EggHash: eggHash(cfg.Egg),
			Image: cfg.Image, Startup: cfg.Startup,
			Variables: mustJSON(cfg.Variables), Limits: mustJSON(cfg.Limits), Settings: mustJSON(cfg.Settings),
			HostNetwork: boolInt(cfg.HostNetwork), DesiredState: "stopped", InstallState: installPending,
		}); err != nil {
			return err
		}
		if err := insertAllocations(ctx, q, sid, cfg.Allocations); err != nil {
			return err
		}
		if err := assignQuotaProject(ctx, q, sid); err != nil {
			return err
		}
		if err := publishTx(ctx, q, created); err != nil {
			return err
		}
		if opts.InTx != nil {
			if err := opts.InTx(ctx, q, sid); err != nil {
				return err
			}
		}
		if opts.Import != nil {
			return nil
		}
		_, err := m.o.Jobs.EnqueueTx(ctx, q, jobs.Spec{Type: JobInstall, ServerID: sid, Payload: installPayload{StartAfter: opts.StartAfterInstall}})
		return err
	})
	if err != nil {
		return "", err
	}

	i := m.newInstance(sid, Installing)
	m.mu.Lock()
	m.servers[sid] = i
	m.mu.Unlock()
	m.emit(created)
	if opts.Import != nil {
		if err := m.importFiles(ctx, i, opts.Import); err != nil {
			if derr := m.Delete(context.WithoutCancel(ctx), sid); derr != nil {
				m.log.Error("removing a server whose import failed", "server", sid, "err", derr)
			}
			return "", fmt.Errorf("import: %w", err)
		}
		return sid, nil
	}
	m.o.Jobs.Wake()
	return sid, nil
}

// importFiles prepares an imported server's storage, fills it, and marks
// it installed. The server stays Installing meanwhile, so nothing else
// touches its files.
func (m *Manager) importFiles(ctx context.Context, i *instance, fill func(ctx context.Context, dir string) error) error {
	i.power.Lock()
	defer i.power.Unlock()
	srv, err := m.Get(ctx, i.id)
	if err != nil {
		return err
	}
	if err := m.prepareStorage(ctx, srv); err != nil {
		return fmt.Errorf("prepare storage: %w", err)
	}
	dir, err := m.serverDir(i.id)
	if err != nil {
		return err
	}
	i.console.Notice("importing files")
	if err := fill(ctx, dir); err != nil {
		return err
	}
	if err := m.o.Store.Write.SetInstallState(ctx, store.SetInstallStateParams{InstallState: installInstalled, ID: i.id}); err != nil {
		return err
	}
	i.setState(Offline)
	i.console.Notice("imported")
	m.publish(EventInstallDone, i.id, srv.Version, map[string]any{"imported": true})
	return nil
}

// Update changes a server's configuration. It's applied on the next start.
// Replacing the egg (a different egg file) is allowed here; it's the
// explicit "update egg" action (docs/SERVERS.md).
func (m *Manager) Update(ctx context.Context, id string, cfg Config) error {
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	if _, err := cfg.validate(m.o.ReservedPorts); err != nil {
		return err
	}
	i.power.Lock()
	defer i.power.Unlock()
	old, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	// Ports the server doesn't hold yet must be free on the host.
	var added []Allocation
	for _, a := range cfg.Allocations {
		if !slices.ContainsFunc(old.Allocations, func(b Allocation) bool { return b.IP == a.IP && b.Port == a.Port }) {
			added = append(added, a)
		}
	}
	if err := m.checkHostPorts(added); err != nil {
		return err
	}
	var updated Event
	err = m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := checkConflicts(ctx, q, id, cfg.Allocations); err != nil {
			return err
		}
		if eggHash(cfg.Egg) != old.EggHash {
			if err := q.ReplaceEgg(ctx, store.ReplaceEggParams{Egg: cfg.Egg, EggSource: cfg.EggSource, EggHash: eggHash(cfg.Egg), Image: cfg.Image, Startup: cfg.Startup, ID: id}); err != nil {
				return err
			}
		}
		if err := q.UpdateServerConfig(ctx, store.UpdateServerConfigParams{
			Name: cfg.Name, Image: cfg.Image, Startup: cfg.Startup,
			Variables: mustJSON(cfg.Variables), Limits: mustJSON(cfg.Limits), Settings: mustJSON(cfg.Settings),
			HostNetwork: boolInt(cfg.HostNetwork), ID: id,
		}); err != nil {
			return err
		}
		if err := q.DeleteServerAllocations(ctx, id); err != nil {
			return err
		}
		if err := insertAllocations(ctx, q, id, cfg.Allocations); err != nil {
			return err
		}
		row, err := q.GetServer(ctx, id)
		if err != nil {
			return err
		}
		updated = newEvent(EventUpdated, id, row.Version, map[string]any{"restart_needed": i.isUp()})
		return publishTx(ctx, q, updated)
	})
	if err != nil {
		return err
	}
	// A new disk limit applies at once, even while the server runs.
	if cfg.Limits.DiskMiB != old.Limits.DiskMiB && old.QuotaProject != 0 {
		if err := m.o.Storage.SetLimit(old.QuotaProject, cfg.Limits.DiskMiB<<20); err != nil {
			m.log.Warn("applying the new disk limit failed; it's applied on the next start", "server", id, "err", err)
		}
	}
	m.emit(updated)
	return nil
}

func insertAllocations(ctx context.Context, q *store.Queries, id string, allocs []Allocation) error {
	for _, a := range allocs {
		if err := q.InsertAllocation(ctx, store.InsertAllocationParams{ServerID: id, Ip: a.IP, Port: int64(a.Port), IsPrimary: boolInt(a.Primary)}); err != nil {
			return fmt.Errorf("allocation %s:%d: %w", a.IP, a.Port, err)
		}
	}
	return nil
}

// checkConflicts rejects allocations another server holds (0.0.0.0 overlaps
// every address on the same port).
func checkConflicts(ctx context.Context, q *store.Queries, id string, allocs []Allocation) error {
	existing, err := q.ListAllocations(ctx)
	if err != nil {
		return err
	}
	for _, a := range allocs {
		for _, e := range existing {
			if e.ServerID != id && conflicts(a, Allocation{IP: e.Ip, Port: int(e.Port)}) {
				return fmt.Errorf("%w: %s:%d is allocated to another server", ErrInvalid, a.IP, a.Port)
			}
		}
	}
	return nil
}

func (m *Manager) checkHostPorts(allocs []Allocation) error {
	ports := make([]containers.Port, 0, len(allocs))
	for _, a := range allocs {
		ports = append(ports, containers.Port{IP: a.IP, Port: a.Port})
	}
	if err := containers.CheckPorts(ports); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	return nil
}

// Install queues a reinstall: the egg's install script runs over the
// existing files (docs/EGGS.md#install). The server must be stopped. It
// returns the job ID.
func (m *Manager) Install(ctx context.Context, id string) (string, error) {
	return m.Reinstall(ctx, id, InstallOptions{})
}

// Reinstall queues an install over an existing server, optionally wiping
// its files first (after a safety backup).
func (m *Manager) Reinstall(ctx context.Context, id string, opts InstallOptions) (string, error) {
	if opts.Wipe && m.o.JobBackup == nil {
		return "", errors.New("backups aren't available on this node, so files can't be wiped")
	}
	if err := m.diskCheck(); err != nil {
		return "", err
	}
	i, err := m.instance(id)
	if err != nil {
		return "", err
	}
	if i.isUp() {
		return "", ErrRunning
	}
	if active, err := m.o.Jobs.HasActive(ctx, id); err != nil || active {
		return "", errors.Join(ErrInstalling, err)
	}
	return m.o.Jobs.Enqueue(ctx, jobs.Spec{Type: JobInstall, ServerID: id, Payload: installPayload{Wipe: opts.Wipe}})
}

// installJob is the job handler for installs.
func (m *Manager) installJob(ctx context.Context, j jobs.Job, log io.Writer) (any, error) {
	var p installPayload
	if err := j.Decode(&p); err != nil {
		return nil, err
	}
	i, err := m.instance(j.ServerID)
	if err != nil {
		return nil, err
	}
	return nil, m.runInstall(ctx, i, j.ID, p, log)
}

// Start starts a server. Starting a starting or running server does nothing.
func (m *Manager) Start(ctx context.Context, id string) error {
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	i.power.Lock()
	defer i.power.Unlock()
	return i.startLocked(ctx, true)
}

// Stop stops a server with its egg's stop command, killing it after its stop
// timeout. Stopping a stopped server does nothing.
func (m *Manager) Stop(ctx context.Context, id string) error {
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	i.power.Lock()
	defer i.power.Unlock()
	return i.stopLocked(ctx, false, true)
}

// Kill stops a server immediately (SIGKILL).
func (m *Manager) Kill(ctx context.Context, id string) error {
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	i.power.Lock()
	defer i.power.Unlock()
	return i.stopLocked(ctx, true, true)
}

// Restart stops and starts a server. Crash counters aren't affected.
func (m *Manager) Restart(ctx context.Context, id string) error {
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	i.power.Lock()
	defer i.power.Unlock()
	if err := i.stopLocked(ctx, false, false); err != nil {
		return err
	}
	return i.startLocked(ctx, true)
}

// SendCommand writes a console command, on behalf of user (for the rate
// limit and the audit event).
func (m *Manager) SendCommand(id, user, cmd string) error {
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	if err := i.console.allowCommand(user, cmd); err != nil {
		return err
	}
	if err := i.send(cmd); err != nil {
		return err
	}
	m.publish(EventCommand, id, 0, map[string]any{"user": user, "command": cmd})
	return nil
}

// Delete stops a server (killing it after its stop timeout), removes its
// container and files, and frees its allocations. It can't be undone.
func (m *Manager) Delete(ctx context.Context, id string) error {
	return m.deleteNow(ctx, id, "", nil)
}

// deleteNow deletes a server; skipJob is the job doing it, if any, which
// isn't cancelled with the server's other jobs. data goes in the
// server.deleted event.
func (m *Manager) deleteNow(ctx context.Context, id, skipJob string, data map[string]any) error {
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	// A pending or running install for a server being deleted is cancelled.
	if err := m.cancelJobs(ctx, id, skipJob); err != nil {
		return err
	}
	i.power.Lock()
	defer i.power.Unlock()
	if err := i.stopLocked(ctx, false, true); err != nil {
		m.log.Warn("stop before delete failed; killing", "server", id, "err", err)
		if err := i.stopLocked(ctx, true, true); err != nil {
			return err
		}
	}
	i.detach()
	for _, name := range []string{containerName(id), containerName(id) + "-install"} {
		if err := m.o.Runtime.Remove(ctx, name); err != nil && !strings.Contains(err.Error(), "No such container") {
			return fmt.Errorf("remove container: %w", err)
		}
	}
	dir, err := m.serverDir(id)
	if err != nil {
		return err
	}
	srv, _ := m.Get(ctx, id)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove files: %w", err)
	}
	if srv != nil {
		if err := m.o.Storage.Release(srv.QuotaProject); err != nil {
			m.log.Warn("clearing the disk limit failed", "server", id, "err", err)
		}
	}
	deleted := newEvent(EventDeleted, id, 0, data)
	if err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error {
		if err := q.DeleteServer(ctx, id); err != nil {
			return err
		}
		return publishTx(ctx, q, deleted)
	}); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.servers, id)
	m.mu.Unlock()
	i.setDeleted()
	m.emit(deleted)
	if m.o.Deleted != nil {
		m.o.Deleted(ctx, id)
	}
	return nil
}

// cancelJobs cancels a server's queued and running jobs (but skip) and
// waits for them.
func (m *Manager) cancelJobs(ctx context.Context, id, skip string) error {
	list, err := m.o.Jobs.List(ctx, id, 100)
	if err != nil {
		return err
	}
	for _, j := range list {
		if j.ID == skip || (j.Status != jobs.Queued && j.Status != jobs.Running) {
			continue
		}
		if err := m.o.Jobs.Cancel(ctx, j.ID); err != nil {
			continue // it finished meanwhile
		}
		if _, err := m.o.Jobs.Wait(ctx, j.ID); err != nil {
			return err
		}
	}
	return nil
}

// ShutdownAll gracefully stops every running server in parallel, each with
// its egg's stop command and stop timeout, without changing desired_state:
// they start again when the host comes back (raptor-shutdown.service).
func (m *Manager) ShutdownAll(ctx context.Context) (stopped int, errs error) {
	m.mu.Lock()
	var up []*instance
	for _, i := range m.servers {
		if i.isUp() {
			up = append(up, i)
		}
	}
	m.mu.Unlock()

	var wg sync.WaitGroup
	var mu sync.Mutex
	var all []error
	for _, i := range up {
		wg.Go(func() {
			i.power.Lock()
			defer i.power.Unlock()
			if err := i.stopLocked(ctx, false, false); err != nil {
				mu.Lock()
				all = append(all, fmt.Errorf("%s: %w", i.id, err))
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return len(up), errors.Join(all...)
}

var idPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// serverDir returns a server's directory. IDs are validated so a corrupt
// row can never point file operations elsewhere.
func (m *Manager) serverDir(id string) (string, error) {
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("invalid server id %q", id)
	}
	return filepath.Join(m.o.VolumesDir, id), nil
}

func containerName(id string) string { return "raptor-" + id }

// runtimeEnv returns the container environment for a server.
func (m *Manager) runtimeEnv(s *Server) eggs.Runtime {
	p := s.Primary()
	return eggs.Runtime{
		ServerID:        s.ID,
		Startup:         s.Startup,
		MemoryMiB:       s.Limits.MemoryMiB,
		IP:              p.IP,
		Port:            p.Port,
		Timezone:        m.o.Timezone,
		Location:        m.o.Location,
		AllocationLimit: 0, // Raptor has no per-server allocation limits yet
		Variables:       s.Variables,
	}
}

// ChangesCode reports whether applying cfg to the server would change what
// code runs: the egg (and so its install script), the image, or the startup
// command. Such updates must be signed with the user's passkey
// (docs/SECURITY-MODEL.md#passkey-signed-commands). Wings decides this from
// its own records, so the Panel can't mislabel an update.
func (m *Manager) ChangesCode(ctx context.Context, id string, cfg Config) (bool, error) {
	cur, err := m.Get(ctx, id)
	if err != nil {
		return false, err
	}
	if eggHash(cfg.Egg) != cur.EggHash {
		return true, nil
	}
	// Empty image/startup mean the egg's defaults, as in validate.
	image, startup := cfg.Image, cfg.Startup
	if image == "" {
		image = cur.Egg().DefaultImage()
	}
	if startup == "" {
		startup = cur.Egg().DefaultStartup()
	}
	return image != cur.Image || startup != cur.Startup, nil
}

// assignQuotaProject gives a server the next free XFS quota project.
func assignQuotaProject(ctx context.Context, q *store.Queries, id string) error {
	n, err := q.NextQuotaProject(ctx)
	if err != nil {
		return err
	}
	return q.SetQuotaProject(ctx, store.SetQuotaProjectParams{QuotaProject: sql.NullInt64{Int64: n, Valid: true}, ID: id})
}

// prepareStorage checks the volume and puts the server's directory under
// its quota project and limit. Called before every install and start, so a
// missing volume stops the server from writing to the host disk instead, and
// a server created before quotas gets a project on its next start.
func (m *Manager) prepareStorage(ctx context.Context, srv *Server) error {
	if srv.QuotaProject == 0 && !m.o.Storage.Soft {
		if err := m.o.Store.WriteTx(ctx, func(q *store.Queries) error { return assignQuotaProject(ctx, q, srv.ID) }); err != nil {
			return err
		}
		fresh, err := m.Get(ctx, srv.ID)
		if err != nil {
			return err
		}
		srv.QuotaProject = fresh.QuotaProject
	}
	dir, err := m.serverDir(srv.ID)
	if err != nil {
		return err
	}
	return m.o.Storage.Prepare(dir, srv.QuotaProject, srv.Limits.DiskMiB<<20, m.o.UID, m.o.GID)
}

// DiskUsage returns a server's disk usage: instantly from its quota, or by
// scanning its directory when quotas are off.
func (m *Manager) DiskUsage(ctx context.Context, id string) (storage.Usage, error) {
	srv, err := m.Get(ctx, id)
	if err != nil {
		return storage.Usage{}, err
	}
	dir, err := m.serverDir(id)
	if err != nil {
		return storage.Usage{}, err
	}
	u, err := m.o.Storage.Usage(ctx, dir, srv.QuotaProject)
	if err == nil && u.LimitBytes == 0 {
		u.LimitBytes = srv.Limits.DiskMiB << 20
	}
	return u, err
}

// writeMachineID writes a server's machine-id file and returns its path ("" if
// machine-ids are off): the server ID without dashes, as Pterodactyl writes
// it, so a server keeps its machine-id across starts, Wings restarts, and
// moves from Pterodactyl.
func (m *Manager) writeMachineID(id string) (string, error) {
	if m.o.MachineIDDir == "" {
		return "", nil
	}
	if !idPattern.MatchString(id) {
		return "", fmt.Errorf("invalid server id %q", id)
	}
	// The server's user reads these. Wings runs with UMask=0077, so the
	// modes are set explicitly rather than left to the umask.
	if err := os.MkdirAll(m.o.MachineIDDir, 0o755); err != nil { //nolint:gosec // mounted into containers, must be readable
		return "", err
	}
	if err := os.Chmod(m.o.MachineIDDir, 0o755); err != nil { //nolint:gosec // see above
		return "", err
	}
	p := filepath.Join(m.o.MachineIDDir, id)
	if err := os.WriteFile(p, []byte(strings.ReplaceAll(id, "-", "")+"\n"), 0o644); err != nil { //nolint:gosec // read by the server's user
		return "", fmt.Errorf("machine-id: %w", err)
	}
	if err := os.Chmod(p, 0o644); err != nil { //nolint:gosec // see above
		return "", err
	}
	return p, nil
}
