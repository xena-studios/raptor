package telemetry_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/xena-studios/raptor/internal/panel/paneltest"
	"github.com/xena-studios/raptor/internal/panel/telemetry"
)

// With an endpoint, metrics recorded anywhere in the Panel reach it.
func TestExport(t *testing.T) {
	var mu sync.Mutex
	got := map[string][]byte{}
	collector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got[r.URL.Path] = append(got[r.URL.Path], b...)
		mu.Unlock()
	}))
	defer collector.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", collector.URL)
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "http/protobuf")

	ctx := context.Background()
	stop, err := telemetry.Setup(ctx, "1.2.3", "test", slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := telemetry.Meter.Int64Counter("raptor.test.things")
	c.Add(ctx, 3)
	if err := stop(ctx); err != nil { // flushes
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if m := got["/v1/metrics"]; !bytes.Contains(m, []byte("raptor.test.things")) || !bytes.Contains(m, []byte("raptor-panel")) {
		t.Errorf("metrics export: %q", m)
	}
}

// Without one, nothing is set up and nothing fails.
func TestOff(t *testing.T) {
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	stop, err := telemetry.Setup(context.Background(), "1.2.3", "test", slog.New(slog.DiscardHandler))
	if err != nil || stop(context.Background()) != nil {
		t.Fatal(err)
	}
}

// The database gauges read real values.
func TestPostgres(t *testing.T) {
	pool := paneltest.NewDB(t)
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	prev := otel.GetMeterProvider()
	otel.SetMeterProvider(mp)
	t.Cleanup(func() { otel.SetMeterProvider(prev) })

	if err := telemetry.Postgres(pool); err != nil {
		t.Fatal(err)
	}
	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			seen[m.Name] = true
		}
	}
	for _, name := range []string{"raptor.postgres.pool.connections", "raptor.postgres.replicas", "raptor.postgres.replica.lag", "raptor.postgres.archive.failures"} {
		if !seen[name] {
			t.Errorf("%s not reported (got %v)", name, seen)
		}
	}
}
