// Package oteltest provides test helpers for OpenTelemetry.
package oteltest

import (
	"context"
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace/noop"
)

// pinGlobals replaces the defaults behind [otel.GetTracerProvider] and [otel.GetTextMapPropagator] with inert
// stand-ins, once, before anything below saves either as "previous" to restore later.
//
// Both defaults are placeholders which, as currently implemented by [go.opentelemetry.io/otel], forward
// every call to whatever is set first, permanently, for the life of the process -- an internal detail of
// that module, not a documented guarantee of [otel.SetTracerProvider] or [otel.SetTextMapPropagator], but
// observably true of the versions this module depends on. Left alone, whichever helper below runs first in
// a test binary would capture that placeholder as "previous", and restoring it later would not actually undo
// the installation -- it would go on forwarding to whatever this package set up, forever. Pinning both once,
// up front, means "previous" is never that placeholder: it is always either this pin or a concrete
// provider/propagator a previous call to a helper below installed, and restoring either is safe.
var pinGlobals = sync.OnceFunc(func() {
	otel.SetTracerProvider(noop.NewTracerProvider())
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator())
})

// NewSpanRecorder for testing.
// It sets up a [tracetest.SpanRecorder] as the global [sdktrace.TracerProvider] for the duration of the test,
// restoring whatever [sdktrace.TracerProvider] (or no-op stand-in) was in place before once the test ends.
// It is not safe for use with parallel tests, as it mutates the global tracer provider.
func NewSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	pinGlobals()

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

// UsePropagators sets the given propagators, composed, as the global [propagation.TextMapPropagator] for
// the duration of the test, restoring whatever propagator (or no-op stand-in) was in place before once the
// test ends.
// It is not safe for use with parallel tests, as it mutates the global text map propagator.
func UsePropagators(t *testing.T, propagators ...propagation.TextMapPropagator) propagation.TextMapPropagator {
	t.Helper()
	pinGlobals()

	previous := otel.GetTextMapPropagator()
	p := propagation.NewCompositeTextMapPropagator(propagators...)
	otel.SetTextMapPropagator(p)

	t.Cleanup(func() {
		otel.SetTextMapPropagator(previous)
	})

	return p
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
