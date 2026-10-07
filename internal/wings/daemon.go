// Package wings implements the Wings daemon.
package wings

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1/nodev1connect"
	"github.com/xena-studios/raptor/internal/shared/buildinfo"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
	"github.com/xena-studios/raptor/internal/wings/actions"
	"github.com/xena-studios/raptor/internal/wings/backup"
	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/events"
	"github.com/xena-studios/raptor/internal/wings/files"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/jobs"
	"github.com/xena-studios/raptor/internal/wings/link"
	"github.com/xena-studios/raptor/internal/wings/localapi"
	"github.com/xena-studios/raptor/internal/wings/metrics"
	"github.com/xena-studios/raptor/internal/wings/notify"
	"github.com/xena-studios/raptor/internal/wings/schedule"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/sftp"
	"github.com/xena-studios/raptor/internal/wings/storage"
	"github.com/xena-studios/raptor/internal/wings/store"
	"github.com/xena-studios/raptor/internal/wings/update"
)

// Snapshot schedule for the state database (docs/WINGS.md#local-state-sqlite).
const (
	snapshotInterval = time.Hour
	snapshotKeep     = 24
)

// How often the runtime is checked: setup is retried until it succeeds, and
// the firewall table is reapplied if something removed it.
const runtimeCheckInterval = time.Minute

// Run starts the daemon and blocks until ctx is cancelled, then shuts down
// gracefully. Stopping Wings never stops servers: they belong to Docker. It
// also returns (with no error) when an update needs a restart, which systemd
// does.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	ctx, restart := context.WithCancel(ctx)
	defer restart()

	for _, dir := range []string{filepath.Dir(cfg.Paths.State), cfg.Paths.Volumes, cfg.Paths.Backups, cfg.Paths.Tmp, cfg.Paths.Logs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	db, err := store.Open(ctx, cfg.Paths.State)
	if err != nil {
		return fmt.Errorf("open state: %w", err)
	}
	defer func() { _ = db.Close() }()
	if r := db.Recovered; r != nil {
		// Changes since the snapshot are lost (at most an hour's); the
		// running servers are reattached as usual.
		log.Error("the state database was corrupt and was replaced by its newest good snapshot",
			"snapshot", r.Snapshot, "corrupt_copy", r.Corrupt, "problem", r.Problem)
		if _, err := events.New(db).Append(ctx, events.Event{Type: eventStateRestored, Data: map[string]any{
			"snapshot": filepath.Base(r.Snapshot), "corrupt_copy": r.Corrupt, "problem": r.Problem,
		}}); err != nil {
			log.Error("recording the state restore failed", "err", err)
		}
	}

	disk := newDiskGuard(cfg, db, log)
	subnet, installSubnet := cfg.Docker.Subnets()
	dc, err := docker.New(docker.Config{
		NodeID:         cfg.NodeID,
		Network:        cfg.Docker.Network,
		Subnet:         subnet,
		InstallNetwork: cfg.Docker.InstallNetwork,
		InstallSubnet:  installSubnet,
		DiskCheck:      disk.Err,
	})
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer func() { _ = dc.Close() }()

	key, err := update.DefaultKey()
	if err != nil {
		return err
	}
	updates := &update.Updater{
		Layout:    update.Layout{Dir: update.DefaultDir},
		Source:    update.DefaultSource(),
		Key:       key,
		StatePath: update.StatePath(cfg.Paths.State),
		Channel:   cfg.Updates.Channel,
		Pin:       cfg.Updates.Pin,
		Current:   buildinfo.Version,
		Restart:   restart,
		Log:       log,
	}
	svc := &localapi.Service{
		NodeID:    cfg.NodeID,
		PanelURL:  cfg.Panel.URL,
		StartedAt: time.Now(),
		Docker:    dc,
		Storage:   &storage.Volume{Path: cfg.Paths.Volumes, Soft: !cfg.Storage.Quotas},
		Updates:   updates,
	}
	svc.Disk = disk
	notifier := newNotifier(cfg, db, log)
	if notifier != nil {
		svc.Notify = notifier
		go notifier.Run(ctx)
	}
	rt, err := newRuntimeSetup(dc, cfg, log, db, svc)
	if err == nil {
		rt.disk = disk
		if notifier != nil {
			rt.commands.OnAudit = func(command.AuditEntry) { notifier.Wake() }
		}
	}
	if err != nil {
		return err
	}
	defer rt.close()
	reportUpdate(ctx, updates.StatePath, rt.events, log)
	rt.check(ctx)

	actions.RegisterUpdates(rt.commands, updates, actions.UpdatePolicy{
		Automatic: cfg.Updates.Automatic, Pin: cfg.Updates.Pin, Current: buildinfo.Version,
	})
	linkDone := make(chan struct{})
	if lk := newLink(cfg, rt, log); lk != nil {
		svc.Link = lk
		rt.link = lk
		rt.panelLink.Store(lk)
		go func() { _ = lk.Run(ctx); close(linkDone) }()
	} else {
		close(linkDone)
	}

	srv, err := localapi.Listen(ctx, cfg.Paths.Socket, localapi.Group, svc, log)
	if err != nil {
		return fmt.Errorf("local api: %w", err)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()

	log.Info("wings started", "version", buildinfo.Version, "socket", cfg.Paths.Socket, "state", cfg.Paths.State, "linked", cfg.NodeID != "")
	rt.ready()

	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()
	runtimeTicker := time.NewTicker(runtimeCheckInterval)
	defer runtimeTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("wings stopping")
			// The freshest copy for recovering from a corrupt database.
			if path, err := db.Snapshot(context.WithoutCancel(ctx), "shutdown", 3); err != nil {
				log.Error("shutdown snapshot failed", "err", err)
			} else {
				log.Debug("state snapshot written", "path", path)
			}
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			<-linkDone
			return errors.Join(srv.Shutdown(shutdownCtx), <-errc)
		case err := <-errc:
			return fmt.Errorf("local api: %w", err)
		case <-runtimeTicker.C:
			disk.Check()
			rt.check(ctx)
			rt.ready()
		case <-ticker.C:
			rt.prune(ctx)
			if path, err := db.Snapshot(ctx, "hourly", snapshotKeep); err != nil {
				log.Error("state snapshot failed", "err", err)
			} else {
				log.Debug("state snapshot written", "path", path)
			}
		}
	}
}

// runtimeSetup prepares the container runtime: networks, the raptor.slice
// memory ceiling, and the firewall. Docker may not be up yet when Wings
// starts, so it's retried until it succeeds. Then it starts the server
// manager, which reattaches to running servers and starts the rest.
type runtimeSetup struct {
	rt          containers.Runtime
	cfg         config.Config
	log         *slog.Logger
	db          *store.DB
	svc         *localapi.Service
	rules       *firewall.Rules // set once setup succeeded
	servers     *server.Manager
	sched       *schedule.Scheduler
	backups     *backup.Manager
	files       *files.Service
	metrics     *metrics.Collector
	stopMetrics context.CancelFunc
	metricsDone chan struct{}
	sftp        *sftp.Service
	sftpKey     ssh.Signer
	sftpKeys    *sftp.KeyCache
	// panelLink is the node connection, for SFTP logins (set after SFTP starts).
	panelLink      atomic.Pointer[link.Link]
	stopSFTPEvents func()

	events         *events.Outbox
	commandsReady  atomic.Bool // every command is registered
	link           *link.Link  // nil if the node isn't linked
	transfers      atomic.Pointer[files.Service]
	serversReady   atomic.Pointer[server.Manager]
	schedulesReady atomic.Pointer[schedule.Scheduler]
	backupsReady   atomic.Pointer[backup.Manager]
	jobs           *jobs.Engine
	disk           *host.DiskGuard
	commands       *command.Executor // receives Panel commands (connected in Phase 3)
}

func newRuntimeSetup(rt containers.Runtime, cfg config.Config, log *slog.Logger, db *store.DB, svc *localapi.Service) (*runtimeSetup, error) {
	rp, err := command.RelyingPartyFor(cfg.Panel.AppURL)
	if err != nil {
		return nil, err
	}
	panelKey, err := command.LoadPanelKey(cfg.Identity.PanelKey)
	if err != nil {
		return nil, err
	}
	r := &runtimeSetup{rt: rt, cfg: cfg, log: log, db: db, svc: svc, events: events.New(db)}
	r.jobs = jobs.New(jobs.Options{
		Store:  db,
		LogDir: filepath.Join(cfg.Paths.Logs, "jobs"),
		Log:    log,
		// Schedule runs mostly wait, so many can run at once. Archive jobs
		// use CPU and disk beside the game servers.
		Limits: map[string]int{"install": cfg.Limits.ConcurrentInstalls, "backup": cfg.Limits.ConcurrentBackups, "schedule": 32, "files": 2},
	})
	r.commands = &command.Executor{DB: db, NodeID: cfg.NodeID, RP: rp, PanelKey: panelKey, Log: log, Pairing: &command.Pairing{}}
	svc.SetCommands(r.commands)
	// The host key exists from the first start, SFTP on or not, so its
	// fingerprint never changes when SFTP is turned on.
	if r.sftpKey, err = sftp.LoadHostKey(cfg.Identity.SFTPHostKey); err != nil {
		return nil, fmt.Errorf("sftp host key: %w", err)
	}
	r.sftpKeys = &sftp.KeyCache{DB: db}
	return r, nil
}

// close stops schedules and jobs (running installs and schedule runs resume
// on the next start) and detaches from servers without stopping them.
// startMetrics samples every server's resources for local history
// (docs/WINGS.md#local-metrics).
func (r *runtimeSetup) startMetrics(mgr *server.Manager) {
	r.metrics = &metrics.Collector{Servers: mgr, DB: r.db, Log: r.log}
	ctx, cancel := context.WithCancel(context.Background())
	r.stopMetrics, r.metricsDone = cancel, make(chan struct{})
	go func() {
		defer close(r.metricsDone)
		r.metrics.Run(ctx)
	}()
	r.svc.SetMetrics(r.metrics)
}

func (r *runtimeSetup) close() {
	if r.stopMetrics != nil {
		r.stopMetrics() // writes the minutes in progress
		<-r.metricsDone
	}
	if r.sftp != nil {
		r.stopSFTPEvents()
		r.sftp.Close()
	}
	if r.sched != nil {
		r.sched.Close()
	}
	if r.backups != nil {
		r.backups.Close()
	}
	if r.servers != nil {
		r.servers.Close() // stops jobs first
	}
}

// prune drops old events, executed commands, and job records.
func (r *runtimeSetup) prune(ctx context.Context) {
	if n, err := r.events.Prune(ctx, time.Now()); err != nil {
		r.log.Error("pruning events failed", "err", err)
	} else if n > 0 {
		r.log.Debug("events pruned", "count", n)
	}
	if _, err := r.commands.Prune(ctx); err != nil {
		r.log.Error("pruning executed commands failed", "err", err)
	}
	if _, err := r.sftpKeys.Prune(ctx); err != nil {
		r.log.Error("pruning cached sftp keys failed", "err", err)
	}
	if r.files != nil {
		if _, err := r.files.Prune(ctx); err != nil {
			r.log.Error("pruning idle file transfers failed", "err", err)
		}
	}
}

// backupSlice holds backup workers: under raptor.slice, beside the game
// servers, so its low weights compare against theirs.
const backupSlice = "raptor-backup.slice"

// backupRunner runs backups in worker processes: in their own low-weight
// scope when containers run under systemd, and as plain child processes
// otherwise.
func (r *runtimeSetup) backupRunner(systemd bool) (backup.Runner, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("backup worker: %w", err)
	}
	p := backup.Process{Command: []string{exe, "wings", "backup-worker"}, MemoryMax: int64(r.cfg.Limits.BackupMemory)}
	if systemd {
		p.Slice = backupSlice
	}
	return p, nil
}

// ready tells an update's launcher that this version is healthy, once the
// runtime is up (the local API is already serving).
// ready tells an update trial that this version works: its container runtime
// is ready and, on a linked node, it got its node connection back
// (docs/WINGS.md#updates).
func (r *runtimeSetup) ready() {
	// commandsReady, not rules: this is also called from the link's
	// goroutine, and it's set once setup is done.
	if !r.commandsReady.Load() {
		return
	}
	if r.link != nil && r.link.Status().State != link.Connected {
		return
	}
	update.Ready()
}

func (r *runtimeSetup) check(ctx context.Context) {
	if r.rules == nil {
		if err := r.setup(ctx); err != nil {
			r.log.Error("runtime setup failed; retrying", "err", err, "retry_in", runtimeCheckInterval.String())
		}
		return
	}
	if !firewall.Present(ctx) {
		r.log.Warn("firewall table was removed; reapplying", "table", "inet "+firewall.Table)
		if err := firewall.Apply(ctx, *r.rules); err != nil {
			r.log.Error("firewall reapply failed", "err", err)
		}
	}
}

func (r *runtimeSetup) setup(ctx context.Context) error {
	nets, err := r.rt.Setup(ctx)
	if err != nil {
		return err
	}
	// Docker's images are on the host disk too.
	if d, ok := r.rt.(interface {
		RootDir(ctx context.Context) (string, error)
	}); ok && r.disk != nil {
		if root, err := d.RootDir(ctx); err == nil {
			r.disk.Watch(root)
		}
	}
	rules := firewall.Rules{
		ServerBridge:  nets.Server.Bridge,
		InstallBridge: nets.Install.Bridge,
		DNS:           firewall.HostResolvers(),
		InstallAllow:  r.cfg.Docker.AllowedPrefixes(),
	}
	if nets.CgroupParent != "" {
		limit, err := host.ApplySlice(ctx, int64(r.cfg.Limits.ReservedMemory))
		if err != nil {
			return fmt.Errorf("slice: %w", err)
		}
		rules.Cgroup = nets.CgroupParent
		r.log.Info("container slice ready", "slice", nets.CgroupParent, "memory_max", limit)
	} else {
		r.log.Warn("docker doesn't use the systemd cgroup driver; containers run without the raptor.slice memory ceiling")
	}
	if err := firewall.Apply(ctx, rules); err != nil {
		return fmt.Errorf("firewall: %w", err)
	}

	u, err := user.Lookup(localapi.Group)
	if err != nil {
		return fmt.Errorf("the %q system user is missing (the installer creates it): %w", localapi.Group, err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	vol := &storage.Volume{Path: r.cfg.Paths.Volumes, Soft: !r.cfg.Storage.Quotas}
	if err := vol.Check(); err != nil {
		// Wings keeps running (status, local API); servers are refused
		// until the volume is back, and start again from reconcile.
		r.log.Error("server data volume unavailable; servers won't start", "err", err)
	} else if m, ok, _ := storage.FindMount(vol.Path); ok {
		if err := storage.EnableDirectIO(m); err != nil {
			r.log.Warn("couldn't enable direct I/O on the volume", "err", err)
		}
		r.log.Info("server data volume ready", "path", vol.Path, "source", m.Source, "quotas", !vol.Soft)
	}
	opts := server.Options{
		Storage:         vol,
		Runtime:         r.rt,
		Store:           r.db,
		Log:             r.log,
		VolumesDir:      r.cfg.Paths.Volumes,
		MachineIDDir:    filepath.Join(filepath.Dir(r.cfg.Paths.Socket), "machine-id"),
		TmpDir:          r.cfg.Paths.Tmp,
		LogDir:          r.cfg.Paths.Logs,
		UID:             uid,
		GID:             gid,
		Timezone:        host.Timezone(),
		DockerInterface: nets.Server.Gateway.String(),
		ReservedPorts:   []int{r.cfg.Ports.SFTP},
		Jobs:            r.jobs,
		Events:          r.events,
		DiskCheck:       r.disk.Err,
	}
	if nets.CgroupParent != "" {
		opts.OOMKills = func() (int64, error) { return host.OOMKills(nets.CgroupParent) }
	}
	var bk *backup.Manager
	opts.Deleted = func(ctx context.Context, id string) { bk.ServerDeleted(ctx, id) }
	opts.JobBackup = func(ctx context.Context, id, jobID, kind string, log io.Writer) (string, error) {
		return bk.JobBackup(ctx, id, jobID, kind, log)
	}
	mgr := server.New(opts) // registers the install job handler
	if err := mgr.Reconcile(ctx); err != nil {
		mgr.Close()
		return fmt.Errorf("reconcile servers: %w", err)
	}
	if err := r.commands.Start(ctx); err != nil {
		mgr.Close()
		return fmt.Errorf("commands: %w", err)
	}
	runner, err := r.backupRunner(nets.CgroupParent != "")
	if err != nil {
		mgr.Close()
		return err
	}
	tz := host.Timezone()
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	bk = backup.New(backup.Options{
		Store: r.db, Jobs: r.jobs, Events: r.events, Servers: mgr, Log: r.log, Runner: runner,
		LocalPath: r.cfg.Paths.Backups, StateDir: filepath.Join(filepath.Dir(r.cfg.Paths.State), "kopia"),
		Location: loc, MinFree: int64(r.cfg.Limits.HostDiskMinFree),
		FreeSpace: func(path string) (int64, error) {
			_, free, err := storage.Space(path)
			return free, err
		},
	})
	sched := schedule.New(schedule.Options{Store: r.db, Jobs: r.jobs, Events: r.events, Servers: mgr, Backups: bk, Log: r.log})
	actions.Register(r.commands, mgr, actions.Defaults{
		BackupSchedule: func(ctx context.Context, q *store.Queries, id string) error {
			_, err := sched.CreateTx(ctx, q, id, schedule.DefaultBackup(tz))
			return err
		},
	})
	actions.RegisterSchedules(r.commands, sched)
	actions.RegisterBackups(r.commands, bk)
	fsvc := files.NewService(files.Options{Servers: mgr, Store: r.db, Jobs: r.jobs, Events: r.events, Log: r.log, UID: uid, GID: gid})
	actions.RegisterFiles(r.commands, fsvc)
	// Jobs start after reconcile, so interrupted installs resume against
	// servers that are already loaded.
	if err := r.jobs.Start(ctx); err != nil {
		mgr.Close()
		return fmt.Errorf("jobs: %w", err)
	}
	// Schedules fire once servers are loaded; runs missed while Wings was
	// down are handled on the first tick.
	sched.Start()
	r.sched = sched
	if err := bk.Start(ctx); err != nil {
		r.log.Error("backup cleanup failed", "err", err)
	}
	r.backups = bk
	r.files = fsvc
	r.sftp = r.startSFTP(ctx, mgr, uid, gid)
	r.startMetrics(mgr)
	r.svc.SetSFTP(r.sftp)
	r.servers = mgr
	r.svc.SetServers(mgr)
	r.svc.SetBackups(bk, r.jobs)
	r.rules = &rules
	r.transfers.Store(fsvc)
	r.serversReady.Store(mgr)
	r.schedulesReady.Store(sched)
	r.backupsReady.Store(bk)
	r.commandsReady.Store(true)
	r.log.Info("runtime ready",
		"network", nets.Server.Name, "subnet", nets.Server.Subnet,
		"install_network", nets.Install.Name, "install_subnet", nets.Install.Subnet,
		"dns_allowed", rules.DNS)
	return nil
}

// newLink connects a linked node to the Panel (nil if it isn't linked, or
// its keys are missing: Wings runs the same without the Panel).
func newLink(cfg config.Config, rt *runtimeSetup, log *slog.Logger) *link.Link {
	if cfg.NodeID == "" {
		return nil
	}
	nodeKey, err := nodelink.LoadKey(cfg.Identity.Key)
	if err == nil && nodeKey == nil {
		err = errors.New("missing")
	}
	if err != nil {
		log.Error("not connecting to the Panel: node key "+cfg.Identity.Key, "err", err)
		return nil
	}
	if rt.commands.PanelKey == nil {
		log.Error("not connecting to the Panel: Panel key " + cfg.Identity.PanelKey + " is missing")
		return nil
	}
	return link.New(link.Config{
		PanelURL: cfg.Panel.URL, NodeID: cfg.NodeID, NodeKey: nodeKey, PanelKey: rt.commands.PanelKey,
		Software: buildinfo.Version, Commands: rt.commands, CommandsReady: rt.commandsReady.Load,
		Events: rt.events, Log: log,
		// A version on trial is healthy only once it's connected again.
		OnConnected: rt.ready,
		Servers: func() link.Servers {
			if m := rt.serversReady.Load(); m != nil {
				return m
			}
			return nil
		},
		Schedules: func() link.Schedules {
			if s := rt.schedulesReady.Load(); s != nil {
				return s
			}
			return nil
		},
		Backups: func() link.Backups {
			if b := rt.backupsReady.Load(); b != nil {
				return b
			}
			return nil
		},
		Transfers: func() link.Transfers {
			if f := rt.transfers.Load(); f != nil {
				return f
			}
			return nil
		},
	})
}

// newNotifier sends notifications straight from the node to the targets in
// config.yml (nil if there are none).
func newNotifier(cfg config.Config, db *store.DB, log *slog.Logger) *notify.Notifier {
	if len(cfg.Notifications) == 0 {
		return nil
	}
	name, _ := os.Hostname()
	return &notify.Notifier{Targets: cfg.Notifications, DB: db, Outbox: events.New(db), Node: name, NodeID: cfg.NodeID, Log: log}
}

// eventStateRestored is recorded when a corrupt state database was replaced
// by a snapshot.
const eventStateRestored = "node.state_restored"

// Events recorded when the host disk goes below limits.host_disk_min_free
// and when it recovers.
const (
	eventDiskLow = "node.disk_low"
	eventDiskOK  = "node.disk_ok"
)

// newDiskGuard watches the host disk where Wings keeps its state, logs, and
// local backups (Docker's directory is added once Docker is reachable).
func newDiskGuard(cfg config.Config, db *store.DB, log *slog.Logger) *host.DiskGuard {
	o := events.New(db)
	g := &host.DiskGuard{
		MinFree: int64(cfg.Limits.HostDiskMinFree),
		Space:   storage.Space,
		OnChange: func(low bool, spaces []host.DiskSpace) {
			typ := eventDiskOK
			if low {
				typ = eventDiskLow
				log.Warn("host disk low: installs and image pulls are refused until space is freed", "min_free", host.Bytes(int64(cfg.Limits.HostDiskMinFree)), "disks", spaces)
			} else {
				log.Info("host disk has enough free space again", "disks", spaces)
			}
			if _, err := o.Append(context.Background(), events.Event{Type: typ, Data: map[string]any{"disks": spaces, "min_free": int64(cfg.Limits.HostDiskMinFree)}}); err != nil {
				log.Error("recording the host disk state failed", "err", err)
			}
		},
	}
	g.Watch(filepath.Dir(cfg.Paths.State), cfg.Paths.Logs, cfg.Paths.Tmp, cfg.Paths.Backups)
	return g
}

// reportUpdate records how the last update went as a node.update event, once.
// The launcher that ran the trial can't: it doesn't open the database.
func reportUpdate(ctx context.Context, path string, o *events.Outbox, log *slog.Logger) {
	st, err := update.ReadState(path)
	if err != nil {
		log.Error("can't read the update state", "err", err)
		return
	}
	if st.Reported || (st.Status != update.StatusSucceeded && st.Status != update.StatusFailed) {
		return
	}
	data := map[string]any{"result": st.Status, "from": st.From, "to": st.To, "actor": st.Actor}
	if st.Error != "" {
		data["error"] = st.Error
	}
	if _, err := o.Append(ctx, events.Event{Type: "node.update", At: st.Finished, Data: data}); err != nil {
		log.Error("can't record the update", "err", err)
		return
	}
	st.Reported = true
	if err := update.WriteState(path, st); err != nil {
		log.Error("can't record the update", "err", err)
	}
}

// sftpAuth checks SFTP logins with the Panel over the node connection. SFTP
// starts before the link, so the link is looked up at each login; while
// there's none (not linked, or disconnected), only cached keys log in.
func (r *runtimeSetup) sftpAuth() sftp.Authenticator {
	return sftp.PanelAuth{Panel: func() nodev1connect.PanelServiceClient {
		if lk := r.panelLink.Load(); lk != nil {
			return lk.Panel()
		}
		return nil
	}}
}

// startSFTP resumes SFTP if it's enabled for the node. A port that can't be
// bound doesn't stop Wings; turning SFTP off and on again retries.
func (r *runtimeSetup) startSFTP(ctx context.Context, mgr *server.Manager, uid, gid int) *sftp.Service {
	svc := sftp.NewService(sftp.ServiceOptions{
		Options: sftp.Options{
			HostKey: r.sftpKey,
			Auth:    r.sftpAuth(),
			Keys:    r.sftpKeys,
			Servers: mgr,
			Events:  r.events,
			UID:     uid,
			GID:     gid,
			Log:     r.log,
		},
		Store:     r.db,
		Port:      r.cfg.Ports.SFTP,
		Allocated: mgr.AllocatedPorts,
	})
	actions.RegisterSFTP(r.commands, svc)
	if err := svc.Start(ctx); err != nil {
		r.log.Error("sftp didn't start", "err", err)
	}
	// Sessions end when the files are deleted, or an install, wipe, restore,
	// or final backup starts over them: open handles would otherwise keep writing into files
	// the install script or the restore is replacing.
	evs, stop := mgr.Events()
	r.stopSFTPEvents = stop
	go func() {
		for e := range evs {
			// Installing covers a wipe, which starts before the install does.
			st, _ := e.Data["state"].(string)
			taken := e.Type == server.EventState && (st == string(server.Restoring) || st == string(server.Installing) || st == string(server.Deleting))
			if e.Type == server.EventDeleted || e.Type == server.EventInstallStarted || taken {
				if n := svc.Disconnect(e.ServerID); n > 0 {
					r.log.Info("sftp sessions closed", "server", e.ServerID, "reason", e.Type, "count", n)
				}
			}
		}
	}()
	return svc
}
