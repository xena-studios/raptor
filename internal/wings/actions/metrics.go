package actions

import (
	"context"
	"errors"
	"time"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/metrics"
	"github.com/xena-studios/raptor/internal/wings/server"
)

// ServerMetrics is server.metrics: a server's resources now and over a
// range, for the Panel's graphs (docs/WINGS.md#local-metrics).
const ServerMetrics = "server.metrics"

// MetricsParams are server.metrics' params.
type MetricsParams struct {
	// Range is "1h" (default), "6h", "24h", or "7d".
	Range string `json:"range,omitempty"`
}

var metricRanges = map[string]time.Duration{
	"1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour,
}

// MetricPoint is a point as the Panel gets it. For a bucket, Rx and Tx are
// per second over it, like a live sample's.
type MetricPoint struct {
	At         int64    `json:"at"`         // unix ms
	Resolution int      `json:"resolution"` // seconds; 0 for a live sample
	CPU        float64  `json:"cpu"`        // percent of one core
	CPUMax     float64  `json:"cpu_max"`
	Memory     int64    `json:"memory"` // bytes
	MemoryMax  int64    `json:"memory_max"`
	Rx         int64    `json:"rx"` // bytes per second
	Tx         int64    `json:"tx"`
	Disk       int64    `json:"disk"` // bytes
	Players    *float64 `json:"players,omitempty"`
	PlayersMax *int     `json:"players_max,omitempty"`
	Running    bool     `json:"running"` // samples were taken while it ran
}

// MetricLimits are what a server may use.
type MetricLimits struct {
	MemoryBytes int64 `json:"memory_bytes"`
	DiskBytes   int64 `json:"disk_bytes"`  // 0: no limit
	CPUPercent  int64 `json:"cpu_percent"` // 0: no limit
}

func metricPoint(p metrics.Point) MetricPoint {
	out := MetricPoint{
		At: p.At.UnixMilli(), Resolution: p.Resolution, CPU: p.CPUAvg, CPUMax: p.CPUMax,
		Memory: p.MemoryAvg, MemoryMax: p.MemoryMax, Rx: p.RxBytes, Tx: p.TxBytes, Disk: p.DiskBytes,
		Players: p.PlayersAvg, PlayersMax: p.PlayersMax, Running: p.Samples > 0,
	}
	if p.Resolution > 0 {
		out.Rx, out.Tx = p.RxBytes/int64(p.Resolution), p.TxBytes/int64(p.Resolution)
	}
	return out
}

// MetricsSource is what server.metrics reads (*metrics.Collector).
type MetricsSource interface {
	Latest(id string) metrics.Point
	History(ctx context.Context, id string, since time.Time) ([]metrics.Point, error)
}

// MetricsServers is what server.metrics needs from the server manager.
type MetricsServers interface {
	Get(ctx context.Context, id string) (*server.Server, error)
}

// RegisterMetrics adds server.metrics. It isn't signed: it changes nothing.
func RegisterMetrics(x *command.Executor, src MetricsSource, servers MetricsServers) {
	x.Register(ServerMetrics, command.Handler{Signed: command.Never, Run: func(ctx context.Context, e command.Envelope) (any, error) {
		if e.ServerID == "" {
			return nil, errors.New("command needs a server_id")
		}
		var p MetricsParams
		if err := decode(e, &p); err != nil {
			return nil, err
		}
		if p.Range == "" {
			p.Range = "1h"
		}
		span, ok := metricRanges[p.Range]
		if !ok {
			return nil, errors.New("range must be 1h, 6h, 24h, or 7d")
		}
		srv, err := servers.Get(ctx, e.ServerID)
		if err != nil {
			return nil, err
		}
		history, err := src.History(ctx, e.ServerID, time.Now().Add(-span))
		if err != nil {
			return nil, err
		}
		points := make([]MetricPoint, 0, len(history))
		for _, h := range history {
			points = append(points, metricPoint(h))
		}
		return map[string]any{
			"range":  p.Range,
			"latest": metricPoint(src.Latest(e.ServerID)),
			"points": points,
			"limits": MetricLimits{
				MemoryBytes: srv.Limits.MemoryMiB << 20, DiskBytes: srv.Limits.DiskMiB << 20, CPUPercent: srv.Limits.CPUPercent,
			},
		}, nil
	}})
}
