package actions

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/xena-studios/raptor/internal/wings/command"
	"github.com/xena-studios/raptor/internal/wings/containers"
	"github.com/xena-studios/raptor/internal/wings/doctor"
	"github.com/xena-studios/raptor/internal/wings/host"
	"github.com/xena-studios/raptor/internal/wings/metrics"
	"github.com/xena-studios/raptor/internal/wings/server"
	"github.com/xena-studios/raptor/internal/wings/store"
)

type fakeMetrics struct{ since time.Time }

func (f *fakeMetrics) Latest(string) metrics.Point {
	return metrics.Point{At: time.Now(), Samples: 1, CPUAvg: 42, MemoryAvg: 512 << 20, RxBytes: 1000}
}

func (f *fakeMetrics) History(_ context.Context, _ string, since time.Time) ([]metrics.Point, error) {
	f.since = since
	return []metrics.Point{{At: since, Resolution: metrics.Minute, Samples: 6, CPUAvg: 10, RxBytes: 6000}}, nil
}

func (f *fakeMetrics) HostLatest() metrics.HostPoint {
	return metrics.HostPoint{At: time.Now(), Samples: 1, CPUAvg: 12, MemoryAvg: 3 << 30, Load: 0.5}
}

func (f *fakeMetrics) HostHistory(_ context.Context, since time.Time) ([]metrics.HostPoint, error) {
	f.since = since
	return []metrics.HostPoint{{At: since, Resolution: metrics.QuarterHour, Samples: 90, CPUAvg: 20, TxBytes: 9000}}, nil
}

type fakeMetricServers struct{}

func (fakeMetricServers) Get(context.Context, string) (*server.Server, error) {
	return &server.Server{Limits: containers.Limits{MemoryMiB: 1024, DiskMiB: 2048}}, nil
}

func (fakeMetricServers) List() map[string]server.State {
	return map[string]server.State{"s1": server.Running}
}

// panelRun runs one Panel-signed command.
func panelRun(t *testing.T, x *command.Executor, key ed25519.PrivateKey, action, serverID string, params any) (json.RawMessage, error) {
	t.Helper()
	id, _ := uuid.NewV7()
	raw, _ := json.Marshal(params)
	e := command.Envelope{CommandID: id.String(), NodeID: "node-1", UserID: "u1", Action: action, ServerID: serverID, Params: raw, ExpiresAt: time.Now().Add(time.Minute).Unix()}
	e.Grant = command.Grant{UserID: e.UserID, NodeID: e.NodeID, CommandID: e.CommandID, Action: e.Action, ServerID: e.ServerID, ExpiresAt: e.ExpiresAt}
	pl, _ := e.Grant.Payload()
	e.Grant.Signature = ed25519.Sign(key, pl)
	res, err := x.Execute(context.Background(), e)
	return res.Value, err
}

func TestMetricsAndHealth(t *testing.T) {
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	pub, key, _ := ed25519.GenerateKey(rand.Reader)
	x := &command.Executor{DB: db, NodeID: "node-1", PanelKey: pub}
	m := &fakeMetrics{}
	RegisterMetrics(x, m, fakeMetricServers{})
	RegisterNodeMetrics(x, m, fakeMetricServers{}, func() int64 { return 100 })
	ran := make(chan struct{}, 1)
	RegisterHealth(x, &Health{
		Servers: fakeMetricServers{}, Metrics: m, Volumes: "/srv/raptor", Labels: map[string]string{"/srv/raptor": "Servers' files"},
		Space: func(string) (int64, int64, error) { return 100, 40, nil },
		Run: func(context.Context) []doctor.Result {
			ran <- struct{}{}
			return []doctor.Result{{ID: "docker", Title: "Docker", Status: doctor.Pass}}
		},
	})

	raw, err := panelRun(t, x, key, ServerMetrics, "s1", MetricsParams{Range: "24h"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Latest MetricPoint   `json:"latest"`
		Points []MetricPoint `json:"points"`
		Limits MetricLimits  `json:"limits"`
	}
	_ = json.Unmarshal(raw, &got)
	if got.Latest.CPU != 42 || got.Limits.MemoryBytes != 1<<30 || got.Limits.DiskBytes != 2<<30 || len(got.Points) != 1 || got.Points[0].Rx != 100 {
		t.Errorf("metrics: %s", raw)
	}
	if d := time.Since(m.since); d < 23*time.Hour || d > 25*time.Hour {
		t.Errorf("24h asked from %s ago", d)
	}
	if _, err := panelRun(t, x, key, ServerMetrics, "s1", MetricsParams{Range: "1y"}); err == nil || !strings.Contains(err.Error(), "range") {
		t.Errorf("a bad range: %v", err)
	}

	raw, err = panelRun(t, x, key, NodeMetrics, "", MetricsParams{Range: "7d"})
	if err != nil {
		t.Fatal(err)
	}
	var node struct {
		Latest  HostMetricPoint   `json:"latest"`
		Points  []HostMetricPoint `json:"points"`
		Totals  HostTotals        `json:"totals"`
		Servers []ServerUse       `json:"servers"`
	}
	_ = json.Unmarshal(raw, &node)
	if node.Latest.CPU != 12 || len(node.Points) != 1 || node.Points[0].Tx != 10 || node.Totals.DiskBytes != 100 ||
		node.Totals.CPUs == 0 || len(node.Servers) != 1 || node.Servers[0].CPU != 42 {
		t.Errorf("node metrics: %s", raw)
	}
	if d := time.Since(m.since); d < 6*24*time.Hour {
		t.Errorf("7d asked from %s ago", d)
	}

	// The first report starts the checks; the next has them.
	raw, err = panelRun(t, x, key, NodeHealth, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	<-ran
	deadline := time.Now().Add(5 * time.Second)
	var rep Report
	for time.Now().Before(deadline) {
		raw, _ = panelRun(t, x, key, NodeHealth, "", nil)
		_ = json.Unmarshal(raw, &rep)
		if rep.CheckedAt != 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(rep.Checks) != 1 || rep.DiskFree != 40 || len(rep.Servers) != 1 || rep.Servers[0].CPU != 42 || rep.Host.CPUs == 0 ||
		len(rep.Disks) != 1 || rep.Disks[0].Paths[0].Label != "Servers' files" {
		t.Errorf("health: %s", raw)
	}
}

// Paths on one filesystem are one disk.
func TestGroupDisks(t *testing.T) {
	got := groupDisks([]host.DiskSpace{
		{Path: "/var/lib/raptor/volumes", Total: 100, Free: 40},
		{Path: "/var/lib/raptor", Total: 100, Free: 40},
		{Path: "/var/lib/raptor/volumes", Total: 100, Free: 40},
		{Path: "/mnt/backups", Total: 500, Free: 400},
	}, map[string]string{"/mnt/backups": "Local backups"})
	if len(got) != 2 || len(got[0].Paths) != 2 || got[1].Paths[0].Label != "Local backups" {
		t.Errorf("disks: %+v", got)
	}
}
