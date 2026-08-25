// Package oteltest provides test helpers for OpenTelemetry.
package oteltest

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
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

// HasAttributeKey checks whether an attribute with the given key is present in the slice, ignoring its value.
func HasAttributeKey(attrs []attribute.KeyValue, key attribute.Key) bool {
	for _, attr := range attrs {
		if attr.Key == key {
			return true
		}
	}
	return false
}

// ExceptionEventsWithStackTrace recorded on the span, which is what an error recorded with
// [go.opentelemetry.io/otel/trace.WithStackTrace] leaves behind. A span the SDK ended while a panic was
// unwinding through it carries an exception event of that SDK's own making, without a stack trace, and
// the stack trace is what separates the two.
//
// The result is therefore not every exception on the span: an error recorded without a stack trace is
// invisible here, so this cannot show that a span carries no exception at all.
func ExceptionEventsWithStackTrace(span sdktrace.ReadOnlySpan) []sdktrace.Event {
	var events []sdktrace.Event
	for _, event := range span.Events() {
		if event.Name == semconv.ExceptionEventName && HasAttributeKey(event.Attributes, semconv.ExceptionStacktraceKey) {
			events = append(events, event)
		}
	}
	return events
}
