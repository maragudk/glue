// Package oteltest provides test helpers for OpenTelemetry.
package oteltest

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// NewSpanRecorder for testing.
// It sets up a [tracetest.SpanRecorder] as the global [sdktrace.TracerProvider] for the duration of the test.
// It is not safe for use with parallel tests, as it mutates the global tracer provider.
func NewSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)

	t.Cleanup(func() {
		_ = tp.Shutdown(context.WithoutCancel(t.Context()))
		otel.SetTracerProvider(previous)
	})

	return sr
}

// HasAttribute checks whether the given [attribute.KeyValue] is present in the slice, matching both key and value.
func HasAttribute(attrs []attribute.KeyValue, want attribute.KeyValue) bool {
	for _, attr := range attrs {
		if attr.Key == want.Key && attr.Value == want.Value {
			return true
		}
	}
	return false
}
