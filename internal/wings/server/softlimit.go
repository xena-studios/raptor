package server

import (
	"context"
	"time"
)

// Soft limits (quota tier 3, docs/WINGS.md#disk-quotas): with quotas off,
// Wings scans each server's directory periodically. The first scan that
// finds a server over its limit warns; if it's still over at the next scan,
// the server is stopped and can't start until it's back under.
const softScanEvery = 5 * time.Minute

// EventDiskLimit is sent when a server goes over its soft disk limit.
const EventDiskLimit = "server.disk_limit_exceeded"

func (m *Manager) softLimitLoop() {
	m.scanSoftLimits(m.ctx) // so usage is known right away; a first finding only warns
	t := time.NewTicker(softScanEvery)
	defer t.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-t.C:
			m.scanSoftLimits(m.ctx)
		}
	}
}

func (m *Manager) scanSoftLimits(ctx context.Context) {
	m.mu.Lock()
	ids := make([]string, 0, len(m.servers))
	for id := range m.servers {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		srv, err := m.Get(ctx, id)
		if err != nil {
			continue
		}
		u, err := m.DiskUsage(ctx, id)
		i, ierr := m.instance(id)
		if err != nil || ierr != nil {
			continue
		}
		limit := srv.Limits.DiskMiB << 20
		i.mu.Lock()
		i.lastScan = u // reported by ListServers between scans
		if limit == 0 || u.Bytes <= limit {
			i.overLimit = 0
			i.mu.Unlock()
			continue
		}
		i.overLimit++
		n := i.overLimit
		i.mu.Unlock()
		m.publish(EventDiskLimit, id, srv.Version, map[string]any{"used_bytes": u.Bytes, "limit_bytes": limit})
		if n == 1 {
			i.console.Notice("using %d MiB of its %d MiB disk limit; it will be stopped if it's still over in %s", u.Bytes>>20, srv.Limits.DiskMiB, softScanEvery)
			continue
		}
		if i.isUp() {
			i.console.Notice("still over its disk limit (%d MiB of %d MiB); stopping", u.Bytes>>20, srv.Limits.DiskMiB)
			if err := m.Stop(ctx, id); err != nil {
				m.log.Error("stopping a server over its disk limit failed", "server", id, "err", err)
			}
		}
	}
}

// stillOverLimit re-checks a server flagged by a scan, so an owner who freed
// space can start it right away instead of waiting for the next scan.
func (m *Manager) stillOverLimit(ctx context.Context, i *instance, srv *Server) bool {
	u, err := m.DiskUsage(ctx, srv.ID)
	if err != nil || srv.Limits.DiskMiB == 0 || u.Bytes <= srv.Limits.DiskMiB<<20 {
		i.mu.Lock()
		i.overLimit = 0
		i.mu.Unlock()
		return false
	}
	return true
}
