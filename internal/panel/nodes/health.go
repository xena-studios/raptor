package nodes

import (
	"context"
	"errors"
	"slices"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	nodev1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/node/v1"
	"github.com/xena-studios/raptor/internal/panel/telemetry"
	"github.com/xena-studios/raptor/internal/shared/nodelink"
)

// doctorChecks are the IDs of `raptor doctor`'s checks
// (internal/wings/doctor.Checks): the node says which fail, and only these
// get their own series.
var doctorChecks = []string{
	"os", "cgroups", "systemd", "config", "wings", "docker", "storage", "data-disks", "host-disk",
	"clock", "firewall", "networks", "databases", "panel", "hostname", "sftp", "pterodactyl", "ssh", "os-updates", "self-update",
}

func (p *panelService) ReportHealth(ctx context.Context, req *nodev1.ReportHealthRequest) (*nodev1.ReportHealthResponse, error) {
	s := nodelink.FromContext(ctx)
	if s == nil {
		return nil, connect.NewError(connect.CodeInternal, errors.New("no session"))
	}
	c, ok := p.h.Conn(s.Hello.NodeID)
	if !ok || c.Session != s {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("not this node's connection"))
	}
	old := c.health.Swap(req)
	// The metrics count nodes; the log says which.
	if old == nil || old.GetHostDiskLow() != req.GetHostDiskLow() || !slices.Equal(old.GetFailing(), req.GetFailing()) {
		if req.GetHostDiskLow() || len(req.GetFailing()) > 0 {
			p.h.Log.Warn("node unhealthy", "node", c.NodeID, "host_disk_low", req.GetHostDiskLow(), "failing", req.GetFailing())
		} else if old != nil {
			p.h.Log.Info("node healthy again", "node", c.NodeID)
		}
	}
	return &nodev1.ReportHealthResponse{}, nil
}

// Health is the node's latest report on its own health (nil if it hasn't
// sent one on this connection).
func (c *Conn) Health() *nodev1.ReportHealthRequest { return c.health.Load() }

// nodeHealth is what the connected nodes report about themselves.
type nodeHealth struct {
	reporting, diskLow int64
	// checks counts nodes by check and status ("fail" or "warn").
	checks map[[2]string]int64
}

func (h *Hub) health() nodeHealth {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := nodeHealth{checks: map[[2]string]int64{}}
	for _, c := range h.conns {
		r := c.health.Load()
		if r == nil {
			continue // not reported yet, or a Wings from before ReportHealth
		}
		out.reporting++
		if r.GetHostDiskLow() {
			out.diskLow++
		}
		for status, ids := range map[string][]string{"fail": r.GetFailing(), "warn": r.GetWarning()} {
			seen := map[string]bool{}
			for _, id := range ids {
				if !slices.Contains(doctorChecks, id) {
					id = "other" // the node says it; it isn't let make new series
				}
				if !seen[id] {
					seen[id] = true
					out.checks[[2]string{id, status}]++
				}
			}
		}
	}
	return out
}

// observeHealth reports the nodes' own health: how many report it
// (raptor.nodes.health_reporting), how many have a low host disk
// (raptor.nodes.host_disk_low), and how many fail or warn on each of
// doctor's checks (raptor.nodes.doctor_checks). They're counts, not per
// node, so series don't grow with nodes; a check failing on many nodes at
// once points at Raptor rather than one owner's machine.
func (h *Hub) observeHealth() error {
	reporting, err := telemetry.Meter.Int64ObservableGauge("raptor.nodes.health_reporting",
		metric.WithDescription("Nodes connected to this instance that report their health"))
	if err != nil {
		return err
	}
	diskLow, err := telemetry.Meter.Int64ObservableGauge("raptor.nodes.host_disk_low",
		metric.WithDescription("Nodes connected to this instance whose host disk is below its minimum free space"))
	if err != nil {
		return err
	}
	checks, err := telemetry.Meter.Int64ObservableGauge("raptor.nodes.doctor_checks",
		metric.WithDescription("Nodes connected to this instance failing or warning on a doctor check, by check and status"))
	if err != nil {
		return err
	}
	_, err = telemetry.Meter.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		nh := h.health()
		o.ObserveInt64(reporting, nh.reporting)
		o.ObserveInt64(diskLow, nh.diskLow)
		for k, n := range nh.checks {
			o.ObserveInt64(checks, n, metric.WithAttributes(attribute.String("check", k[0]), attribute.String("status", k[1])))
		}
		return nil
	}, reporting, diskLow, checks)
	return err
}
