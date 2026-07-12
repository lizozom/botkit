// Package telemetry wires structured logging (slog, JSON to stderr) to
// OpenTelemetry's Logs API, exporting over OTLP/HTTP.
//
// Graceful no-op: when OTEL_EXPORTER_OTLP_ENDPOINT is unset, Init only
// configures slog and returns a no-op shutdown. Nothing else is required to
// run without an OTLP backend.
//
// botkit intentionally provides only the transport bootstrap here. The event
// vocabulary (what to emit, with which attributes) is a domain concern and
// stays app-side — an app emits via the global logger provider that Init sets
// up, or via its own slog lines.
package telemetry

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/log/global"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Init configures slog (structured JSON on stderr) and, when an OTLP endpoint
// is set, the OTel logs SDK + global logger provider. It returns a shutdown
// func that flushes buffered logs before exit; call it from a deferred cleanup
// in main.
func Init(ctx context.Context, serviceName, serviceVersion string) (shutdown func(context.Context) error, err error) {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if endpoint == "" {
		slog.Info("telemetry: OTLP disabled (OTEL_EXPORTER_OTLP_ENDPOINT unset); logs to stderr only")
		return func(context.Context) error { return nil }, nil
	}

	exporter, err := otlploghttp.New(ctx) // reads OTEL_EXPORTER_OTLP_* env vars
	if err != nil {
		return nil, fmt.Errorf("otlp log exporter: %w", err)
	}
	res, err := resource.New(ctx, resource.WithAttributes(
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(serviceVersion),
	))
	if err != nil {
		return nil, fmt.Errorf("otel resource: %w", err)
	}
	provider := sdklog.NewLoggerProvider(
		sdklog.WithResource(res),
		sdklog.WithProcessor(sdklog.NewBatchProcessor(exporter,
			sdklog.WithExportInterval(5*time.Second),
			sdklog.WithExportMaxBatchSize(64),
		)),
	)
	global.SetLoggerProvider(provider)
	slog.Info("telemetry: OTLP enabled",
		slog.String("service", serviceName), slog.String("version", serviceVersion))
	return provider.Shutdown, nil
}
