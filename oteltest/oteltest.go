// Package oteltest provides test helpers for OpenTelemetry.
package oteltest

import (
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Setup a [tracetest.SpanRecorder] as the global [sdktrace.TracerProvider] for the duration of the test.
func Setup(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)

	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
	})

	return sr
}
