// Package wings implements the Wings daemon.
package wings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"time"

	"github.com/xena-studios/raptor/internal/wings/config"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/docker"
	"github.com/xena-studios/raptor/internal/wings/firewall"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/localapi"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
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
// gracefully. Stopping Wings never stops servers: they belong to Docker.
func Run(ctx context.Context, cfg config.Config, log *slog.Logger) error {
	for _, dir := range []string{filepath.Dir(cfg.Paths.State), cfg.Paths.Volumes, cfg.Paths.Tmp, cfg.Paths.Logs} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
	}

	db, err := store.Open(ctx, cfg.Paths.State)
	if err != nil {
		return fmt.Errorf("open state: %w", err)
	}
	defer func() { _ = db.Close() }()

	subnet, installSubnet := cfg.Docker.Subnets()
	dc, err := docker.New(docker.Config{
		NodeID:         cfg.NodeID,
		Network:        cfg.Docker.Network,
		Subnet:         subnet,
		InstallNetwork: cfg.Docker.InstallNetwork,
		InstallSubnet:  installSubnet,
	})
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	defer func() { _ = dc.Close() }()

	svc := &localapi.Service{
		NodeID:    cfg.NodeID,
		PanelURL:  cfg.Panel.URL,
		StartedAt: time.Now(),
		Docker:    dc,
	}
	rt := &runtimeSetup{rt: dc, cfg: cfg, log: log, db: db, svc: svc}
	defer rt.close()
	rt.check(ctx)

	srv, err := localapi.Listen(ctx, cfg.Paths.Socket, localapi.Group, svc, log)
	if err != nil {
		return fmt.Errorf("local api: %w", err)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()

	log.Info("wings started", "socket", cfg.Paths.Socket, "state", cfg.Paths.State, "linked", cfg.NodeID != "")

	ticker := time.NewTicker(snapshotInterval)
	defer ticker.Stop()
	runtimeTicker := time.NewTicker(runtimeCheckInterval)
	defer runtimeTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Info("wings stopping")
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			return errors.Join(srv.Shutdown(shutdownCtx), <-errc)
		case err := <-errc:
			return fmt.Errorf("local api: %w", err)
		case <-runtimeTicker.C:
			rt.check(ctx)
		case <-ticker.C:
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
	rt      containers.Runtime
	cfg     config.Config
	log     *slog.Logger
	db      *store.DB
	svc     *localapi.Service
	rules   *firewall.Rules // set once setup succeeded
	servers *server.Manager
}

// close detaches from servers without stopping them.
func (r *runtimeSetup) close() {
	if r.servers != nil {
		r.servers.Close()
	}
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
	opts := server.Options{
		Runtime:            r.rt,
		Store:              r.db,
		Log:                r.log,
		VolumesDir:         r.cfg.Paths.Volumes,
		TmpDir:             r.cfg.Paths.Tmp,
		LogDir:             r.cfg.Paths.Logs,
		UID:                uid,
		GID:                gid,
		Timezone:           host.Timezone(),
		DockerInterface:    nets.Server.Gateway.String(),
		ReservedPorts:      []int{r.cfg.Ports.SFTP},
		ConcurrentInstalls: r.cfg.Limits.ConcurrentInstalls,
	}
	if nets.CgroupParent != "" {
		opts.OOMKills = func() (int64, error) { return host.OOMKills(nets.CgroupParent) }
	}
	mgr := server.New(opts)
	if err := mgr.Reconcile(ctx); err != nil {
		mgr.Close()
		return fmt.Errorf("reconcile servers: %w", err)
	}
	r.servers = mgr
	r.svc.SetServers(mgr)
	r.rules = &rules
	r.log.Info("runtime ready",
		"network", nets.Server.Name, "subnet", nets.Server.Subnet,
		"install_network", nets.Install.Name, "install_subnet", nets.Install.Subnet,
		"dns_allowed", rules.DNS)
	return nil
}
