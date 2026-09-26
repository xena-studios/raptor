package server

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Errors returned by power actions and installs.
var (
	ErrNotFound     = errors.New("server not found")
	ErrInstalling   = errors.New("server is installing")
	ErrNotInstalled = errors.New("server isn't installed; reinstall it or enable skip install")
	ErrRunning      = errors.New("server is running; stop it first")
	ErrClosed       = errors.New("server manager is shut down")
)

// Options configure a Manager.
type Options struct {
	Runtime containers.Runtime
	Store   *store.DB
	Log     *slog.Logger

	VolumesDir string // server directories: <VolumesDir>/<id>
	TmpDir     string // install scripts
	LogDir     string // install logs: <LogDir>/install/<id>.log

	// UID and GID the server containers run as (the raptor user).
	UID, GID int
	// Timezone (TZ) and Location (P_SERVER_LOCATION) for every server.
	Timezone, Location string
	// DockerInterface is the server network's gateway, for
	// {{config.docker.interface}} in config files.
	DockerInterface string
	// ReservedPorts are Wings' own ports, never allocatable.
	ReservedPorts []int
	// ConcurrentInstalls limits parallel installs (default 2).
	ConcurrentInstalls int
	// StartStagger spaces out starts after a reboot (default 3s).
	StartStagger time.Duration

	// OOMKills, if set, returns the kernel's OOM kill count for all server
	// containers (host.OOMKills on raptor.slice). Docker doesn't reliably
	// report OOM kills, so a SIGKILL exit Wings didn't cause is attributed to
	// the OOM killer when this count went up.
	OOMKills func() (int64, error)

	// Crash policy overrides, for tests.
	CrashWindow time.Duration
	CrashDelays []time.Duration
}

// Manager owns every server on the node.
type Manager struct {
	o       Options
	log     *slog.Logger
	events  bus
	install chan struct{} // install slots

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
	if o.ConcurrentInstalls < 1 {
		o.ConcurrentInstalls = 2
	}
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
		install: make(chan struct{}, o.ConcurrentInstalls),
		servers: map[string]*instance{},
		ctx:     ctx,
		cancel:  cancel,
	}
	if o.OOMKills != nil {
		m.oomSeen, _ = o.OOMKills()
	}
	return m
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

func (m *Manager) publish(typ, id string, version int64, data map[string]any) {
	e := Event{Type: typ, ServerID: id, Version: version, Time: time.Now(), Data: data}
	attrs := []any{"server", id, "event", typ}
	for k, v := range data {
		if k != "console" {
			attrs = append(attrs, k, v)
		}
	}
	m.log.Info("server event", attrs...)
	m.events.publish(e)
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

// Close detaches from every server without stopping any: containers keep
// running and the next Wings reattaches to them (docs/SERVERS.md).
func (m *Manager) Close() {
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
}

// Create stores a new server and starts its install in the background.
func (m *Manager) Create(ctx context.Context, cfg Config, opts CreateOptions) (string, error) {
	if _, err := cfg.validate(m.o.ReservedPorts); err != nil {
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
		return insertAllocations(ctx, q, sid, cfg.Allocations)
	})
	if err != nil {
		return "", err
	}

	i := m.newInstance(sid, Installing)
	m.mu.Lock()
	m.servers[sid] = i
	m.mu.Unlock()
	m.publish(EventCreated, sid, 1, nil)
	m.goTracked(func() {
		if err := m.runInstall(m.ctx, i, opts.StartAfterInstall); err != nil && !errors.Is(err, context.Canceled) {
			m.log.Error("install failed", "server", sid, "err", err)
		}
	})
	return sid, nil
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
		return insertAllocations(ctx, q, id, cfg.Allocations)
	})
	if err != nil {
		return err
	}
	srv, err := m.Get(ctx, id)
	if err != nil {
		return err
	}
	m.publish(EventUpdated, id, srv.Version, map[string]any{"restart_needed": i.isUp()})
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

// Install reinstalls a server: runs the egg's install script over the
// existing files (docs/EGGS.md#install). The server must be stopped.
func (m *Manager) Install(ctx context.Context, id string) error {
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	return m.runInstall(ctx, i, false)
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
	i, err := m.instance(id)
	if err != nil {
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
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove files: %w", err)
	}
	if err := m.o.Store.Write.DeleteServer(ctx, id); err != nil {
		return err
	}
	m.mu.Lock()
	delete(m.servers, id)
	m.mu.Unlock()
	i.setDeleted()
	m.publish(EventDeleted, id, 0, nil)
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
		AllocationLimit: len(s.Allocations),
		Variables:       s.Variables,
	}
}
