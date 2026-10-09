package server

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/storage"
)

// PowerAction is start, stop, restart, or kill.
type PowerAction string

// Power actions.
const (
	PowerStart   PowerAction = "start"
	PowerStop    PowerAction = "stop"
	PowerRestart PowerAction = "restart"
	PowerKill    PowerAction = "kill"
)

// EventPower records who asked for a power action.
const EventPower = "server.power"

// Power runs a power action on behalf of user ("local:root", or a Panel user
// ID). Who asked is recorded before the action runs, so a stop that takes
// minutes is attributed right away.
func (m *Manager) Power(ctx context.Context, id string, a PowerAction, user string) error {
	var fn func(context.Context, string) error
	switch a {
	case PowerStart:
		fn = m.Start
	case PowerStop:
		fn = m.Stop
	case PowerRestart:
		fn = m.Restart
	case PowerKill:
		fn = m.Kill
	default:
		return fmt.Errorf("%w: unknown power action %q", ErrInvalid, a)
	}
	i, err := m.instance(id)
	if err != nil {
		return err
	}
	// A restore or a final backup holds the server for as long as it
	// takes; say so rather than making the caller wait.
	switch i.getState() {
	case Restoring:
		return ErrRestoring
	case Deleting:
		return ErrDeleting
	}
	m.publish(EventPower, id, 0, map[string]any{"action": string(a), "user": user})
	return fn(ctx, id)
}

// Usage is a server's live resource usage.
type Usage struct {
	State        State
	RunningSince time.Time     // zero unless running
	CPUPercent   float64       // of one core; 0 when not running
	MemoryBytes  int64         // 0 when not running
	Disk         storage.Usage // Bytes is 0 until the first scan when quotas are off
}

// cpuSample is how long CPU usage is measured over.
const cpuSample = 500 * time.Millisecond

// Usage measures a server's resource usage. It takes about half a second
// for a running server (CPU is measured over an interval); call it for many
// servers in parallel.
func (m *Manager) Usage(ctx context.Context, id string) (Usage, error) {
	i, err := m.instance(id)
	if err != nil {
		return Usage{}, err
	}
	i.mu.Lock()
	u := Usage{State: i.state, RunningSince: i.runningSince, Disk: i.lastScan}
	cid := i.containerID
	i.mu.Unlock()
	if u.State != Running {
		u.RunningSince = time.Time{}
	}

	if !m.o.Storage.Soft {
		if d, err := m.DiskUsage(ctx, id); err == nil {
			u.Disk = d
		}
	} else if srv, err := m.Get(ctx, id); err == nil {
		u.Disk.LimitBytes = srv.Limits.DiskMiB << 20
	}

	if cid == "" || (u.State != Running && u.State != Starting && u.State != Stopping) {
		return u, nil
	}
	// If the container just stopped or Docker is restarting, there are no
	// numbers: report the rest.
	a, err := m.o.Runtime.Stats(ctx, cid)
	if err != nil {
		return u, nil //nolint:nilerr // see above
	}
	select {
	case <-time.After(cpuSample):
	case <-ctx.Done():
		return u, ctx.Err()
	}
	b, err := m.o.Runtime.Stats(ctx, cid)
	if err != nil {
		return u, nil //nolint:nilerr // see above
	}
	u.MemoryBytes = b.MemoryBytes
	if dt := b.Time.Sub(a.Time); dt > 0 && b.CPUNanos >= a.CPUNanos {
		u.CPUPercent = float64(b.CPUNanos-a.CPUNanos) / float64(dt.Nanoseconds()) * 100
	}
	return u, nil
}

// Sample is a server's raw counters at one moment, for metrics.
type Sample struct {
	At      time.Time
	State   State
	Running bool // the container is up; the counters below are set
	containers.Stats
	DiskBytes int64
	// QueryAddr is where to ask the game for its players ("" = the egg
	// doesn't say how), and Query the protocol.
	Query, QueryAddr string
}

// Sample reads a server's counters without waiting (unlike Usage, which
// measures CPU over an interval): rates come from two samples.
func (m *Manager) Sample(ctx context.Context, id string) (Sample, error) {
	i, err := m.instance(id)
	if err != nil {
		return Sample{}, err
	}
	i.mu.Lock()
	s := Sample{At: time.Now(), State: i.state, DiskBytes: i.lastScan.Bytes}
	cid := i.containerID
	i.mu.Unlock()
	if !m.o.Storage.Soft {
		if d, err := m.DiskUsage(ctx, id); err == nil {
			s.DiskBytes = d.Bytes
		}
	}
	if cid == "" || (s.State != Running && s.State != Starting && s.State != Stopping) {
		return s, nil
	}
	st, err := m.o.Runtime.Stats(ctx, cid)
	if err != nil {
		return s, nil //nolint:nilerr // stopped meanwhile, or Docker is restarting
	}
	s.Stats, s.Running = st, true
	if srv, err := m.Get(ctx, id); err == nil {
		s.Query, s.QueryAddr = queryAddr(srv)
	}
	return s, nil
}

// queryAddr is where the game answers player queries: the primary
// allocation's port, moved by the egg's x-raptor.players.port ("+1", or a
// variable such as QUERY_PORT).
func queryAddr(srv *Server) (string, string) {
	p := srv.Egg().Raptor.Players
	if p.Query == "" {
		return "", ""
	}
	a := srv.Primary()
	port := a.Port
	switch v := strings.TrimSpace(p.Port); {
	case v == "":
	case strings.HasPrefix(v, "+") || strings.HasPrefix(v, "-"):
		if n, err := strconv.Atoi(v); err == nil {
			port += n
		}
	default:
		name := strings.Trim(v, "{} ")
		if n, err := strconv.Atoi(srv.Variables[name]); err == nil {
			port = n
		}
	}
	ip := a.IP
	if ip == "" || ip == "0.0.0.0" || ip == "::" {
		ip = "127.0.0.1"
	}
	return p.Query, net.JoinHostPort(ip, strconv.Itoa(port))
}

// Logs streams a server's output from Docker's log store: the last tail
// lines (all if tail < 0), then live output if follow. It covers the current
// container, which survives stops; each start replaces it.
func (m *Manager) Logs(ctx context.Context, id string, tail int, follow bool) (<-chan containers.Line, <-chan error, error) {
	if _, err := m.instance(id); err != nil {
		return nil, nil, err
	}
	lines, errc := m.o.Runtime.Logs(ctx, containerName(id), containers.LogOptions{Follow: follow, Tail: tail})
	return lines, errc, nil
}

// ContainerStarted reports when the server's container last started, and
// whether it's running, from Docker's own record (unlike Usage's
// RunningSince, which restarts when Wings reattaches).
func (m *Manager) ContainerStarted(ctx context.Context, id string) (time.Time, bool, error) {
	if _, err := m.instance(id); err != nil {
		return time.Time{}, false, err
	}
	st, err := m.o.Runtime.Inspect(ctx, containerName(id))
	if err != nil {
		return time.Time{}, false, err
	}
	return st.StartedAt, st.Running, nil
}

// ContainerPid returns the server's running container's main process (0 if
// it isn't running), for reading its network namespace.
func (m *Manager) ContainerPid(ctx context.Context, id string) (int, error) {
	if _, err := m.instance(id); err != nil {
		return 0, err
	}
	st, err := m.o.Runtime.Inspect(ctx, containerName(id))
	if err != nil || !st.Running {
		return 0, nil //nolint:nilerr // no container is "not running"
	}
	return st.Pid, nil
}
