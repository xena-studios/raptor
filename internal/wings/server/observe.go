package server

import (
	"context"
	"fmt"
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
	if _, err := m.instance(id); err != nil {
		return err
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
