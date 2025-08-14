// Package tracing provides OpenTelemetry initialization for op-geth.
package tracing

import (
	"context"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.25.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/ethereum/go-ethereum/log"
)

var (
	tracer trace.Tracer
	isInitialized bool
)

// InitializeTracing sets up OpenTelemetry tracing for op-geth
func InitializeTracing() error {
	if isInitialized {
		return nil
	}

	// Check if tracing should be enabled
	if os.Getenv("DD_APM_ENABLED") != "true" {
		log.Debug("APM tracing disabled, DD_APM_ENABLED not set to true")
		return nil
	}

	// Get OTLP endpoint from environment
	otlpEndpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")
	if otlpEndpoint == "" {
		otlpEndpoint = "http://localhost:4318" // Default fallback
	}
	
	// DEBUG: Log OTLP configuration
	log.Info("DEBUG: Configuring OTLP exporter", 
		"endpoint", otlpEndpoint,
		"using_default", os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "",
		"insecure", true)

	// Create OTLP HTTP exporter
	exporter, err := otlptracehttp.New(context.Background(),
		otlptracehttp.WithEndpoint(otlpEndpoint),
		otlptracehttp.WithInsecure(), // Use HTTP instead of HTTPS
	)
	if err != nil {
		log.Error("Failed to create OTLP exporter", "error", err, "endpoint", otlpEndpoint)
		return err
	}
	
	// DEBUG: Log successful exporter creation
	log.Info("DEBUG: OTLP exporter created successfully", "endpoint", otlpEndpoint)

	// Create resource with service information
	serviceName := os.Getenv("DD_SERVICE")
	if serviceName == "" {
		serviceName = "op-geth"
	}

	environment := os.Getenv("DD_ENV")
	if environment == "" {
		environment = "development"
	}

	version := os.Getenv("DD_VERSION")
	if version == "" {
		version = "unknown"
	}

	// DEBUG: Log resource configuration
	log.Info("DEBUG: Creating resource with service info",
		"service_name", serviceName,
		"version", version,
		"environment", environment)

	resource, err := resource.New(context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			semconv.ServiceVersion(version),
			semconv.DeploymentEnvironment(environment),
		),
	)
	if err != nil {
		log.Error("Failed to create resource", "error", err)
		return err
	}

	// DEBUG: Log trace provider configuration
	log.Info("DEBUG: Creating trace provider",
		"batch_timeout", "1s",
		"max_batch_size", 100,
		"sampler", "AlwaysSample")

	// Create trace provider
	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter,
			sdktrace.WithBatchTimeout(1*time.Second),
			sdktrace.WithMaxExportBatchSize(100),
		),
		sdktrace.WithResource(resource),
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)
	
	// DEBUG: Log trace provider creation success
	log.Info("DEBUG: Trace provider created successfully")

	// Set global trace provider and propagator
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Get tracer instance
	tracer = tp.Tracer("github.com/ethereum/go-ethereum/internal/tracing")

	isInitialized = true
	log.Info("OpenTelemetry tracing initialized", "endpoint", otlpEndpoint, "service", serviceName, "env", environment)
	return nil
}

// GetTracer returns the global tracer instance
func GetTracer() trace.Tracer {
	if !isInitialized {
		if err := InitializeTracing(); err != nil {
			log.Error("Failed to initialize tracing", "error", err)
			return nil
		}
	}
	return tracer
}

// IsTracingInitialized returns true if OpenTelemetry tracing is initialized
func IsTracingInitialized() bool {
	return isInitialized && tracer != nil
}