// Package telemetry sends the Panel's traces and metrics to an
// OpenTelemetry collector (Grafana Cloud's OTLP gateway in production;
// docs/DEPLOY.md#monitoring). It's configured with the standard OTEL_*
// variables, and off without OTEL_EXPORTER_OTLP_ENDPOINT: packages record
// through the global providers, which do nothing until Setup installs real
// ones.
package telemetry

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"strconv"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
)

const scope = "github.com/xena-studios/raptor/internal/panel"

// Meter is the meter Panel packages record with. Instruments made from it
// before Setup start sending once Setup has run.
var Meter = otel.Meter(scope)

// Setup installs the exporters and returns a function that flushes and
// stops them. Without an endpoint it does nothing.
func Setup(ctx context.Context, version, instance string, log *slog.Logger) (func(context.Context) error, error) {
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" || os.Getenv("OTEL_SDK_DISABLED") == "true" {
		log.Info("telemetry off: OTEL_EXPORTER_OTLP_ENDPOINT is not set")
		return func(context.Context) error { return nil }, nil
	}
	res, err := resource.Merge(resource.Default(), resource.NewWithAttributes(semconv.SchemaURL,
		semconv.ServiceName("raptor-panel"), semconv.ServiceVersion(version), semconv.ServiceInstanceID(instance)))
	if err != nil {
		return nil, err
	}
	traces, err := otlptracehttp.New(ctx)
	if err != nil {
		return nil, err
	}
	metrics, err := otlpmetrichttp.New(ctx)
	if err != nil {
		return nil, err
	}
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traces),
		sdktrace.WithResource(res),
		// A sample of requests is plenty to see latency; errors show in
		// metrics and logs regardless (OTEL_TRACES_SAMPLER_ARG changes it).
		sdktrace.WithSampler(sdktrace.ParentBased(sdktrace.TraceIDRatioBased(sampleRatio()))),
	)
	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metrics, sdkmetric.WithInterval(30*time.Second))),
		sdkmetric.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	otel.SetMeterProvider(mp)
	otel.SetTextMapPropagator(propagation.TraceContext{})
	otel.SetErrorHandler(otel.ErrorHandlerFunc(func(err error) { log.Warn("telemetry", "err", err) }))
	log.Info("telemetry on", "endpoint", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	return func(ctx context.Context) error {
		return errors.Join(tp.Shutdown(ctx), mp.Shutdown(ctx))
	}, nil
}

func sampleRatio() float64 {
	r, err := strconv.ParseFloat(os.Getenv("OTEL_TRACES_SAMPLER_ARG"), 64)
	if err != nil || r < 0 || r > 1 {
		return 0.1
	}
	return r
}
