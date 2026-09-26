package server

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/xena-studios/raptor/internal/eggs"
	"github.com/xena-studios/raptor/internal/eggs/configfile"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/install"
	"github.com/xena-studios/raptor/internal/wings/store"
)

// instance is one server's live state. power serializes everything that
// changes the container (power actions, installs, deletion); mu guards the
// fields.
type instance struct {
	m       *Manager
	id      string
	console *Console
	power   sync.Mutex

	mu           sync.Mutex
	state        State
	containerID  string
	input        containers.Input
	watchCancel  context.CancelFunc
	gen          uint64        // bumped per container; stale watchers ignore themselves
	exited       chan struct{} // closed when the current container has exited
	stopping     bool          // Wings asked the server to stop
	runningSince time.Time
	lastLine     time.Time // timestamp of the last output line seen
	crashes      crashTracker
	restartTimer *time.Timer
	deleted      bool
}

func (m *Manager) newInstance(id string, state State) *instance {
	return &instance{m: m, id: id, console: newConsole(), state: state}
}

func (i *instance) getState() State {
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.state
}

func (i *instance) isUp() bool {
	s := i.getState()
	return s == Starting || s == Running || s == Stopping
}

// setState records a state change: in memory, as the last seen state in
// SQLite, and as an event.
func (i *instance) setState(s State) {
	i.mu.Lock()
	if i.state == s || i.deleted {
		i.mu.Unlock()
		return
	}
	i.state = s
	if s == Running {
		i.runningSince = time.Now()
	}
	i.mu.Unlock()
	if err := i.m.o.Store.Write.SetLastState(context.Background(), store.SetLastStateParams{LastState: string(s), ID: i.id}); err != nil {
		i.m.log.Warn("saving server state failed", "server", i.id, "err", err)
	}
	i.m.publish(EventState, i.id, 0, map[string]any{"state": string(s)})
}

func (i *instance) setDeleted() {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.deleted = true
}

func (i *instance) setDesired(ctx context.Context, desired string) error {
	return i.m.o.Store.Write.SetDesiredState(ctx, store.SetDesiredStateParams{DesiredState: desired, ID: i.id})
}

// detach stops watching the container and closes stdin, leaving the
// container running.
func (i *instance) detach() {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.watchCancel != nil {
		i.watchCancel()
		i.watchCancel = nil
	}
	if i.input != nil {
		_ = i.input.Close()
		i.input = nil
	}
	if i.restartTimer != nil {
		i.restartTimer.Stop()
		i.restartTimer = nil
	}
}

func (i *instance) send(cmd string) error {
	i.mu.Lock()
	in, state, cid, gen := i.input, i.state, i.containerID, i.gen
	i.mu.Unlock()
	if in == nil || (state != Starting && state != Running && state != Stopping) {
		return ErrConsoleNotReady
	}
	if err := in.Send(cmd); err == nil {
		return nil
	}
	// The stdin connection broke (e.g. Docker restarted with live-restore
	// and the watcher hasn't reconnected yet): reconnect once and retry.
	fresh, err := i.m.o.Runtime.Input(i.m.ctx, cid)
	if err != nil {
		return fmt.Errorf("console: %w", err)
	}
	i.mu.Lock()
	if gen != i.gen {
		i.mu.Unlock()
		_ = fresh.Close()
		return ErrConsoleNotReady
	}
	if i.input != nil {
		_ = i.input.Close()
	}
	i.input = fresh
	i.mu.Unlock()
	return fresh.Send(cmd)
}

// --- install ---

func (m *Manager) runInstall(ctx context.Context, i *instance, startAfter bool) error {
	i.power.Lock()
	defer i.power.Unlock()
	if i.isUp() {
		return ErrRunning
	}
	srv, err := m.Get(ctx, i.id)
	if err != nil {
		return err
	}
	dir, err := m.serverDir(i.id)
	if err != nil {
		return err
	}

	i.setState(Installing)
	if err := m.o.Store.Write.SetInstallState(ctx, store.SetInstallStateParams{InstallState: installInstalling, ID: i.id}); err != nil {
		return err
	}
	select {
	case m.install <- struct{}{}:
		defer func() { <-m.install }()
	case <-ctx.Done():
		return m.finishInstall(i, srv, install.Result{}, ctx.Err())
	}

	m.publish(EventInstallStarted, i.id, srv.Version, nil)
	i.console.Notice("installing (%s)", srv.Egg().Name)
	res, err := install.Run(ctx, m.o.Runtime, install.Params{
		Egg:       srv.Egg(),
		Image:     srv.Image,
		ServerID:  i.id,
		Dir:       dir,
		TmpDir:    m.o.TmpDir,
		Variables: srv.Variables,
		Env:       m.runtimeEnv(srv),
		UID:       m.o.UID,
		GID:       m.o.GID,
		Skip:      srv.Settings.SkipInstall,
	})
	if err := m.finishInstall(i, srv, res, err); err != nil {
		return err
	}
	if startAfter {
		return i.startLocked(ctx, true)
	}
	return nil
}

func (m *Manager) finishInstall(i *instance, srv *Server, res install.Result, runErr error) error {
	ctx := context.WithoutCancel(m.ctx)
	if len(res.Log) > 0 {
		m.writeInstallLog(i.id, res.Log)
	}
	if runErr != nil {
		msg := runErr.Error()
		if errors.Is(runErr, context.Canceled) {
			msg = "interrupted (Wings stopped during the install)"
		}
		_ = m.o.Store.Write.SetInstallState(ctx, store.SetInstallStateParams{InstallState: installFailed, InstallError: msg, ID: i.id})
		i.console.Notice("install failed: %s", msg)
		i.setState(InstallFailed)
		m.publish(EventInstallFailed, i.id, srv.Version, map[string]any{"error": msg})
		return runErr
	}
	if err := m.o.Store.Write.SetInstallState(ctx, store.SetInstallStateParams{InstallState: installInstalled, ID: i.id}); err != nil {
		return err
	}
	data := map[string]any{"exit_code": res.ExitCode, "skipped": res.Skipped, "duration": res.Duration.Round(time.Second).String()}
	if res.ExitCode != 0 {
		i.console.Notice("install script exited with code %d (not an error; see the install log)", res.ExitCode)
	} else {
		i.console.Notice("install finished")
	}
	i.setState(Offline)
	m.publish(EventInstallDone, i.id, srv.Version, data)
	return nil
}

func (m *Manager) writeInstallLog(id string, log []byte) {
	if m.o.LogDir == "" {
		return
	}
	dir := filepath.Join(m.o.LogDir, "install")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		m.log.Warn("install log", "server", id, "err", err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, id+".log"), log, 0o600); err != nil {
		m.log.Warn("install log", "server", id, "err", err)
	}
}

// --- start ---

// startLocked starts the server. The power lock must be held.
func (i *instance) startLocked(ctx context.Context, setDesired bool) error {
	switch i.getState() {
	case Starting, Running:
		return nil
	case Installing:
		return ErrInstalling
	}
	m := i.m
	srv, err := m.Get(ctx, i.id)
	if err != nil {
		return err
	}
	if srv.InstallState != installInstalled && !srv.Settings.SkipInstall {
		if srv.InstallState == installInstalling {
			return ErrInstalling
		}
		return ErrNotInstalled
	}
	if setDesired {
		if err := i.setDesired(ctx, "running"); err != nil {
			return err
		}
	}
	i.mu.Lock()
	if i.restartTimer != nil {
		i.restartTimer.Stop()
		i.restartTimer = nil
	}
	i.mu.Unlock()

	if err := i.launch(ctx, srv); err != nil {
		i.console.Notice("start failed: %v", err)
		i.setState(Offline)
		return err
	}
	return nil
}

// launch applies config files, creates a fresh container (so configuration
// changes take effect), starts it, and watches it.
func (i *instance) launch(ctx context.Context, srv *Server) error {
	m := i.m
	dir, err := m.serverDir(i.id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // the server's own directory
		return err
	}
	if err := os.Chown(dir, m.o.UID, m.o.GID); err != nil {
		return err
	}

	env := m.runtimeEnv(srv)
	envList := env.Environment()
	if files := srv.Egg().Config.Files; len(files) > 0 {
		vals := eggs.Values{
			ServerID: i.id, IP: env.IP, Port: env.Port, MemoryMiB: srv.Limits.MemoryMiB, SwapMiB: srv.Limits.SwapMiB,
			CPU: srv.Limits.CPUPercent, Env: envMap(envList), DockerInterface: m.o.DockerInterface,
		}
		if root, err := os.OpenRoot(dir); err == nil {
			if err := configfile.Apply(root, files, vals, configfile.Owner{UID: m.o.UID, GID: m.o.GID}); err != nil {
				// Like Pterodactyl, a broken config file doesn't stop the start.
				for _, line := range strings.Split(err.Error(), "\n") {
					i.console.Notice("config file: %s", line)
				}
				m.publish(EventConfigError, i.id, srv.Version, map[string]any{"error": err.Error()})
			}
			_ = root.Close()
		}
	}

	ports := make([]containers.Port, 0, len(srv.Allocations))
	for _, a := range srv.Allocations {
		ports = append(ports, containers.Port{IP: a.IP, Port: a.Port})
	}
	cid, err := m.o.Runtime.Create(ctx, containers.ServerSpec{
		ServerID:    i.id,
		Dir:         dir,
		Image:       srv.Image,
		Env:         envList,
		UID:         m.o.UID,
		GID:         m.o.GID,
		Limits:      srv.Limits,
		HostNetwork: srv.HostNetwork,
		Ports:       ports,
	})
	if err != nil {
		return err
	}
	in, err := m.o.Runtime.Input(ctx, cid)
	if err != nil {
		_ = m.o.Runtime.Remove(context.WithoutCancel(ctx), cid)
		return fmt.Errorf("attach: %w", err)
	}
	i.console.Notice("starting")
	i.setState(Starting)
	if err := m.o.Runtime.Start(ctx, cid); err != nil {
		_ = in.Close()
		_ = m.o.Runtime.Remove(context.WithoutCancel(ctx), cid)
		return err
	}
	i.watch(cid, in, srv.Egg().Config, -1, time.Time{})
	if len(srv.Egg().Config.Done) == 0 {
		i.setState(Running)
	}
	return nil
}

func envMap(env []string) map[string]string {
	out := make(map[string]string, len(env))
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		out[k] = v
	}
	return out
}

// watch follows a container's output until it exits. tail < 0 reads all of
// the container's output; since resumes after a line already seen.
func (i *instance) watch(cid string, in containers.Input, cfg eggs.Config, tail int, since time.Time) {
	ctx, cancel := context.WithCancel(i.m.ctx)
	i.mu.Lock()
	if i.watchCancel != nil {
		i.watchCancel()
	}
	i.gen++
	gen := i.gen
	i.containerID, i.input, i.watchCancel = cid, in, cancel
	i.exited = make(chan struct{})
	i.stopping = false
	i.lastLine = since
	i.mu.Unlock()

	i.m.goTracked(func() { i.follow(ctx, gen, cid, cfg, tail) })
}

func (i *instance) follow(ctx context.Context, gen uint64, cid string, cfg eggs.Config, tail int) {
	rt := i.m.o.Runtime
	backoff := time.Second
	for {
		i.mu.Lock()
		since := i.lastLine
		i.mu.Unlock()
		lines, errc := rt.Logs(ctx, cid, containers.LogOptions{Follow: true, Tail: tail, Since: since})
		for l := range lines {
			i.mu.Lock()
			if gen != i.gen {
				i.mu.Unlock()
				return
			}
			dup := !since.IsZero() && !l.Time.After(since)
			if !l.Time.IsZero() && l.Time.After(i.lastLine) {
				i.lastLine = l.Time
			}
			starting := i.state == Starting
			i.mu.Unlock()
			if dup {
				continue // the line Docker's "since" repeats
			}
			i.console.Write(l.Text)
			if starting && cfg.IsDone(l.Text) {
				i.setState(Running)
			}
		}
		<-errc
		if ctx.Err() != nil {
			return // Wings is shutting down or the container was replaced
		}
		tail = -1 // resume: everything after the last line seen

		// The stream ended: either the container stopped, or Docker went
		// away (a Docker restart with live-restore keeps containers running).
		st, err := rt.Inspect(ctx, cid)
		switch {
		case err == nil && !st.Running:
			i.handleExit(gen, st)
			return
		case err != nil && strings.Contains(err.Error(), "No such container"):
			i.handleExit(gen, containers.State{ExitCode: -1})
			return
		case err == nil && st.Running:
			// Still running: reconnect stdin too, then resume the output.
			if in, err := rt.Input(ctx, cid); err == nil {
				i.mu.Lock()
				if gen == i.gen {
					if i.input != nil {
						_ = i.input.Close()
					}
					i.input = in
				} else {
					_ = in.Close()
				}
				i.mu.Unlock()
			}
			backoff = time.Second
		}
		select {
		case <-time.After(backoff):
		case <-ctx.Done():
			return
		}
		backoff = min(backoff*2, 10*time.Second)
	}
}

// handleExit decides what a container exit means (docs/SERVERS.md#crash-policy).
func (i *instance) handleExit(gen uint64, st containers.State) {
	m := i.m
	i.mu.Lock()
	if gen != i.gen {
		i.mu.Unlock()
		return
	}
	stopping := i.stopping
	i.stopping = false
	runningSince := i.runningSince
	i.runningSince = time.Time{}
	if i.input != nil {
		_ = i.input.Close()
		i.input = nil
	}
	exited := i.exited
	i.mu.Unlock()
	defer close(exited)

	ctx := context.WithoutCancel(m.ctx)
	srv, err := m.Get(ctx, i.id)
	if err != nil {
		i.setState(Offline)
		return
	}
	switch {
	case stopping || srv.DesiredState != "running":
		i.console.Notice("stopped")
		i.setState(Offline)
		return
	case st.ExitCode == 0 && srv.Settings.CleanExitIsStop:
		_ = i.setDesired(ctx, "stopped")
		i.console.Notice("exited cleanly; treating it as a stop")
		i.setState(Offline)
		return
	}

	reason := "exit"
	switch {
	case st.OOMKilled:
		reason = "oom"
	case st.ExitCode == 137 && m.oomKilled():
		reason = "oom"
	case st.ExitCode == 137:
		reason = "killed"
	}
	i.console.Notice("crashed (exit code %d, %s)", st.ExitCode, reason)
	i.setState(Crashed)
	m.publish(EventCrashed, i.id, srv.Version, map[string]any{
		"exit_code": st.ExitCode, "reason": reason, "console": i.console.Tail(crashLastLogs),
	})
	if !srv.Settings.CrashAutoRestart {
		return
	}
	i.mu.Lock()
	delay, loop := i.crashes.record(time.Now(), runningSince, m.o.CrashWindow, m.o.CrashDelays, crashLoopAt)
	i.mu.Unlock()
	if loop {
		i.console.Notice("crashed %d times in %s; not restarting until someone starts it", crashLoopAt, m.o.CrashWindow)
		m.publish(EventCrashLoop, i.id, srv.Version, map[string]any{"crashes": crashLoopAt})
		return
	}
	if delay > 0 {
		i.console.Notice("restarting in %s", delay)
	}
	i.mu.Lock()
	i.restartTimer = time.AfterFunc(delay, func() {
		m.goTracked(func() { i.autoRestart() })
	})
	i.mu.Unlock()
}

// autoRestart restarts a crashed server if nobody has intervened since.
func (i *instance) autoRestart() {
	if i.m.ctx.Err() != nil {
		return
	}
	i.power.Lock()
	defer i.power.Unlock()
	if i.getState() != Crashed {
		return
	}
	srv, err := i.m.Get(i.m.ctx, i.id)
	if err != nil || srv.DesiredState != "running" {
		return
	}
	if err := i.startLocked(i.m.ctx, false); err != nil {
		i.m.log.Error("restart after crash failed", "server", i.id, "err", err)
	}
}

// --- stop ---

// stopLocked stops the server and waits for it to exit. The power lock must
// be held. With setDesired the server stays stopped (a user stop); without
// it, it comes back after a Wings or host restart (restart, host shutdown).
func (i *instance) stopLocked(ctx context.Context, kill, setDesired bool) error {
	m := i.m
	if setDesired {
		if err := i.setDesired(ctx, "stopped"); err != nil {
			return err
		}
	}
	i.mu.Lock()
	if i.restartTimer != nil {
		i.restartTimer.Stop()
		i.restartTimer = nil
	}
	state, cid, in, exited := i.state, i.containerID, i.input, i.exited
	up := state == Starting || state == Running || state == Stopping
	if up {
		i.stopping = true
	}
	i.mu.Unlock()
	if !up {
		if state == Crashed && setDesired {
			i.setState(Offline)
		}
		return nil
	}

	srv, err := m.Get(ctx, i.id)
	if err != nil {
		return err
	}
	stop, timeout := srv.Egg().Config.Stop, time.Duration(srv.Settings.StopTimeout)
	if kill {
		stop, timeout = eggs.Stop{Signal: syscall.SIGKILL}, 10*time.Second
		i.console.Notice("killing")
	} else {
		i.console.Notice("stopping")
	}
	i.setState(Stopping)
	var sender containers.Sender
	if in != nil {
		sender = in
	}
	if err := m.o.Runtime.Stop(ctx, cid, sender, stop, timeout); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	// The watcher sees the exit and settles the state.
	select {
	case <-exited:
	case <-time.After(timeout + 30*time.Second):
		return errors.New("stop: the container didn't exit")
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}
