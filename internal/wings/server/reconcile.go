package server

import (
	"context"
	"errors"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// Reconcile brings the manager in line with SQLite and Docker when Wings
// starts (docs/SERVERS.md#after-a-reboot-or-wings-restart):
//
//   - Running server containers are adopted, never restarted: Wings
//     reattaches to stdin and refills the console from Docker's logs.
//   - Servers whose desired_state is "running" but whose container isn't
//     running are started, a few seconds apart.
//   - Leftover install containers (Wings stopped mid-install) are removed
//     and the install is marked failed, so the owner can reinstall.
//   - Server containers with no server in SQLite are reported and left alone.
func (m *Manager) Reconcile(ctx context.Context) error {
	rows, err := m.o.Store.Read.ListServers(ctx)
	if err != nil {
		return err
	}
	ctrs, err := m.o.Runtime.List(ctx)
	if err != nil {
		return err
	}
	byServer := map[string]containers.Container{}
	known := map[string]bool{}
	for _, r := range rows {
		known[r.ID] = true
	}
	for _, c := range ctrs {
		switch {
		case c.Role == "install":
			m.log.Warn("removing leftover install container", "server", c.ServerID, "container", c.Name)
			_ = m.o.Runtime.Remove(ctx, c.ID)
		case !known[c.ServerID]:
			m.log.Warn("container for an unknown server; leaving it alone", "server", c.ServerID, "container", c.Name)
		default:
			byServer[c.ServerID] = c
		}
	}

	var toStart []*instance
	for _, r := range rows {
		i := m.newInstance(r.ID, Offline)
		m.mu.Lock()
		m.servers[r.ID] = i
		m.mu.Unlock()

		if r.InstallState == installInstalling || r.InstallState == installPending {
			// Installs are jobs: the job engine resumes them.
			if active, err := m.o.Jobs.HasActive(ctx, r.ID); err != nil {
				return err
			} else if active {
				i.state = Installing
				continue
			}
			msg := "interrupted (Wings stopped during the install); reinstall to retry"
			if err := m.o.Store.Write.SetInstallState(ctx, store.SetInstallStateParams{InstallState: installFailed, InstallError: msg, ID: r.ID}); err != nil {
				return err
			}
			i.console.Notice("install %s", msg)
			i.state = InstallFailed
			continue
		}
		if r.InstallState == installFailed {
			i.state = InstallFailed
		}

		srv, err := m.Get(ctx, r.ID)
		if err != nil {
			m.log.Error("can't load server; skipping it", "server", r.ID, "err", err)
			continue
		}
		if c, ok := byServer[r.ID]; ok {
			st, err := m.o.Runtime.Inspect(ctx, c.ID)
			if err == nil && st.Running {
				if err := m.adopt(ctx, i, srv, c.ID, r.LastState); err != nil {
					m.log.Error("reattaching failed", "server", r.ID, "err", err)
				}
				continue
			}
		}
		startable := srv.InstallState == installInstalled || srv.Settings.SkipInstall
		if srv.DesiredState == "running" && startable {
			toStart = append(toStart, i)
		}
	}
	m.log.Info("servers reconciled", "servers", len(rows), "adopted", len(byServer), "to_start", len(toStart))

	// Starts are staggered so a box with many servers doesn't spike at boot.
	m.goTracked(func() {
		for n, i := range toStart {
			if n > 0 {
				select {
				case <-time.After(m.o.StartStagger):
				case <-m.ctx.Done():
					return
				}
			}
			i.power.Lock()
			err := i.startLocked(m.ctx, false)
			i.power.Unlock()
			if err != nil && !errors.Is(err, context.Canceled) {
				m.log.Error("starting server after restart failed", "server", i.id, "err", err)
			}
		}
	})
	return nil
}

// adopt reattaches to a running container. Its state is what Wings last saw
// (running, or starting until the done string shows up in the output).
func (m *Manager) adopt(ctx context.Context, i *instance, srv *Server, cid, lastState string) error {
	in, err := m.o.Runtime.Input(ctx, cid)
	if err != nil {
		return err
	}
	i.state = Starting
	if lastState == string(Running) || len(srv.Egg().Config.Done) == 0 {
		i.state = Running
		i.runningSince = time.Now()
	}
	m.log.Info("reattached to running server", "server", i.id, "state", i.state)
	i.watch(cid, in, srv.Egg().Config, historyLines, time.Time{})
	return nil
}
