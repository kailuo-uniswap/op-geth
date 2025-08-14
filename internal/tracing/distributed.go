// Package tracing provides distributed tracing utilities for op-geth.
package tracing

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"

	"github.com/ethereum/go-ethereum/log"
)

const (
	// W3C TraceContext header
	TraceParentHeader = "traceparent"
)

// Context keys for storing trace information
type contextKey string

const (
	traceParentKey contextKey = "traceparent"
	enabledKey     contextKey = "tracing_enabled"
	txHashKey      contextKey = "txhash"
	spanKey        contextKey = "otel_span"
)

// generateTraceID creates a new 32-character hex trace ID
func generateTraceID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// generateSpanID creates a new 16-character hex span ID
func generateSpanID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ExtractTraceContext extracts W3C TraceContext headers from the incoming request
// and stores them in the context for later propagation
func ExtractTraceContext(req *http.Request, ctx context.Context) context.Context {
	// Check if tracing is enabled in this context
	if enabled, ok := ctx.Value(enabledKey).(bool); !ok || !enabled {
		return ctx
	}

	// Use OpenTelemetry propagation to extract trace context
	if IsTracingInitialized() {
		propagator := otel.GetTextMapPropagator()
		ctx = propagator.Extract(ctx, propagation.HeaderCarrier(req.Header))
		
		// Also store the raw traceparent for backward compatibility
		if traceparent := req.Header.Get(TraceParentHeader); traceparent != "" {
			if isValidTraceParent(traceparent) {
				ctx = context.WithValue(ctx, traceParentKey, traceparent)
				log.Debug("Extracted trace context", "traceparent", traceparent)
			}
		}
	} else {
		// Fallback to custom extraction if OpenTelemetry is not initialized
		if traceparent := req.Header.Get(TraceParentHeader); traceparent != "" {
			if isValidTraceParent(traceparent) {
				ctx = context.WithValue(ctx, traceParentKey, traceparent)
				log.Debug("Extracted trace context", "traceparent", traceparent)
			}
		}
	}

	return ctx
}

// CreateTraceContext generates a new trace context from request data
func CreateTraceContext(requestData []byte, ctx context.Context) context.Context {
	// Check if tracing is enabled
	if enabled, ok := ctx.Value(enabledKey).(bool); !ok || !enabled {
		return ctx
	}

	// Generate new trace and span IDs
	traceID := generateTraceID()
	spanID := generateSpanID()

	// Create W3C traceparent header: version-traceid-spanid-flags
	// Version: 00, Flags: 01 (sampled)
	traceparent := fmt.Sprintf("00-%s-%s-01", traceID, spanID)

	// Store hash of request data for correlation (optional, for debugging)
	if len(requestData) > 0 {
		bodyHash := hashRequestData(requestData)
		log.Debug("Created trace context", "traceparent", traceparent, "data_hash", bodyHash)
	}

	return context.WithValue(ctx, traceParentKey, traceparent)
}

// GetTraceParent returns the traceparent value from context, if any
func GetTraceParent(ctx context.Context) (string, bool) {
	if traceparent, ok := ctx.Value(traceParentKey).(string); ok {
		return traceparent, true
	}
	return "", false
}

// EnableTracing adds tracing enabled flag to context
func EnableTracing(ctx context.Context) context.Context {
	return context.WithValue(ctx, enabledKey, true)
}

// IsTracingEnabled checks if tracing is enabled in the context
func IsTracingEnabled(ctx context.Context) bool {
	if enabled, ok := ctx.Value(enabledKey).(bool); ok {
		return enabled
	}
	return false
}

// GetTraceID extracts the trace ID from the traceparent header in the context
func GetTraceID(ctx context.Context) string {
	if traceparent, ok := ctx.Value(traceParentKey).(string); ok && len(traceparent) >= 36 {
		// Extract trace ID from traceparent format: 00-TRACEID-SPANID-01
		// TraceID is at positions 3-34 (32 chars)
		return traceparent[3:35]
	}
	return ""
}

// hashRequestData creates a SHA256 hash of the request data for trace correlation
func hashRequestData(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:8]) // Use first 8 bytes for brevity
}

// isValidTraceParent validates the format of a traceparent header
// Basic validation - should be 55 characters in format: 00-{32hex}-{16hex}-{2hex}
func isValidTraceParent(traceparent string) bool {
	if len(traceparent) != 55 {
		return false
	}
	if traceparent[2] != '-' || traceparent[35] != '-' || traceparent[52] != '-' {
		return false
	}
	return true
}

// SetTxHash stores transaction hash in the context for trace correlation
func SetTxHash(ctx context.Context, txHash string) context.Context {
	return context.WithValue(ctx, txHashKey, txHash)
}

// GetTxHash returns the transaction hash from context, if any
func GetTxHash(ctx context.Context) (string, bool) {
	if txHash, ok := ctx.Value(txHashKey).(string); ok {
		return txHash, true
	}
	return "", false
}

// StartSpan creates a new OpenTelemetry span and stores it in the context
func StartSpan(ctx context.Context, operationName string, attributes ...trace.SpanStartOption) (context.Context, trace.Span) {
	if !IsTracingInitialized() || !IsTracingEnabled(ctx) {
		return ctx, trace.SpanFromContext(ctx) // Return no-op span
	}

	tracer := GetTracer()
	if tracer == nil {
		return ctx, trace.SpanFromContext(ctx)
	}

	ctx, span := tracer.Start(ctx, operationName, attributes...)
	ctx = context.WithValue(ctx, spanKey, span)
	
	return ctx, span
}

// GetSpan retrieves the current span from context
func GetSpan(ctx context.Context) trace.Span {
	if span, ok := ctx.Value(spanKey).(trace.Span); ok {
		return span
	}
	return trace.SpanFromContext(ctx)
}

// FinishSpan ends the current span with optional error
func FinishSpan(ctx context.Context, err error) {
	span := GetSpan(ctx)
	if span == nil {
		return
	}
	
	if err != nil {
		span.SetStatus(codes.Error, err.Error())
		span.RecordError(err)
	} else {
		span.SetStatus(codes.Ok, "")
	}
	
	span.End()
}

// LogWithTrace logs a message with trace correlation and creates spans if tracing is enabled
func LogWithTrace(ctx context.Context, msg string, keyvals ...interface{}) {
	// Add trace correlation to logs
	if IsTracingEnabled(ctx) {
		if traceID := GetTraceID(ctx); traceID != "" {
			keyvals = append(keyvals, "trace_id", traceID)
		}
		if txHash, ok := GetTxHash(ctx); ok && txHash != "" {
			keyvals = append(keyvals, "tx_hash", txHash)
		}
	}
	
	// Create a span event for important logs
	if IsTracingInitialized() && IsTracingEnabled(ctx) {
		span := GetSpan(ctx)
		if span != nil {
			span.AddEvent(msg)
		}
	}
	
	log.Info(msg, keyvals...)
}
