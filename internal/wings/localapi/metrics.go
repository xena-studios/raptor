package localapi

import (
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	localv1 "github.com/xena-studios/raptor/internal/gen/proto/raptor/wings/local/v1"
	"github.com/xena-studios/raptor/internal/wings/metrics"
)

// Metrics is servers' resource history (*metrics.Collector).
type Metrics interface {
	Latest(id string) metrics.Point
	History(ctx context.Context, id string, since time.Time) ([]metrics.Point, error)
}

// SetMetrics makes resource history available, once the runtime is ready.
func (s *Service) SetMetrics(m Metrics) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.metrics = m
}

// GetMetrics returns a server's history. The raptor group may look.
func (s *Service) GetMetrics(ctx context.Context, req *localv1.GetMetricsRequest) (*localv1.GetMetricsResponse, error) {
	srv, err := s.serverManager()
	if err != nil {
		return nil, err
	}
	s.mu.RLock()
	m := s.metrics
	s.mu.RUnlock()
	if m == nil {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("metrics aren't ready yet"))
	}
	id, err := resolve(ctx, srv, req.GetServer())
	if err != nil {
		return nil, err
	}
	since := time.Now().Add(-time.Hour)
	if req.GetSince() != nil {
		since = req.GetSince().AsTime()
	}
	points, err := m.History(ctx, id, since)
	if err != nil {
		return nil, err
	}
	resp := &localv1.GetMetricsResponse{Latest: metricPoint(m.Latest(id))}
	for _, p := range points {
		resp.Points = append(resp.Points, metricPoint(p))
	}
	return resp, nil
}

func metricPoint(p metrics.Point) *localv1.MetricPoint {
	out := &localv1.MetricPoint{
		At: timestamppb.New(p.At), Resolution: int32(p.Resolution), Samples: int32(p.Samples), //nolint:gosec // small
		CpuAvg: p.CPUAvg, CpuMax: p.CPUMax, MemoryAvg: p.MemoryAvg, MemoryMax: p.MemoryMax,
		RxBytes: p.RxBytes, TxBytes: p.TxBytes, DiskBytes: p.DiskBytes, PlayersAvg: p.PlayersAvg,
	}
	if p.PlayersMax != nil {
		v := int32(*p.PlayersMax) //nolint:gosec // a player count
		out.PlayersMax = &v
	}
	return out
}
