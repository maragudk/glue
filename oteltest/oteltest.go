// Package oteltest provides test helpers for OpenTelemetry.
package oteltest

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// pristineTracerProvider and pristinePropagator hold what [otel.GetTracerProvider] and
// [otel.GetTextMapPropagator] returned as of this package's own initialization. [ensureAttached] compares
// the current global against these to tell a value worth adopting as a starting target from one that is
// still untouched.
var (
	pristineTracerProvider = otel.GetTracerProvider()
	pristinePropagator     = otel.GetTextMapPropagator()
)

// globalTracerProvider and globalPropagator are this package's stand-ins for the global tracer provider and
// text map propagator. Each is created once and never replaced: [ensureAttached] always reuses the same
// object, updating only its target, and [NewSpanRecorder]/[UsePropagators] do the same for the duration of
// a test.
var (
	globalTracerProvider *switchableTracerProvider
	globalPropagator     *switchablePropagator
)

// ensureAttached makes [globalTracerProvider] and [globalPropagator] the current global tracer provider
// and text map propagator, reconciling on every call rather than assuming a single attachment holds for
// good.
//
// [otel.SetTracerProvider] and [otel.SetTextMapPropagator] can be called by anything, at any time, so
// neither stand-in can assume it stays attached once it is. When the current global is not this package's
// own stand-in -- whether because nothing has attached one yet, or because something else took its place
// since the last attachment -- this reattaches it, adopting whatever is current as its new target first.
// The one exception is a global nothing has ever configured (verified against [pristineTracerProvider] or
// [pristinePropagator]): the target then falls back to an inert stand-in of its own, never the
// still-untouched global itself, to avoid the stand-in ending up targeting itself once attached.
//
// Because the same [globalTracerProvider]/[globalPropagator] object is always reused, never replaced, a
// [trace.Tracer] or propagator reference already resolving through one keeps doing so correctly across any
// number of detachments and reattachments -- only its target changes underneath it. That covers every
// reference which resolves through [globalTracerProvider]/[globalPropagator] at all; one which was bound
// directly to something else before either stand-in ever existed stays bound to that other value, and no
// reattachment here reaches it.
func ensureAttached() {
	if globalTracerProvider == nil {
		globalTracerProvider = newSwitchableTracerProvider(noop.NewTracerProvider())
	}
	if current := otel.GetTracerProvider(); current != trace.TracerProvider(globalTracerProvider) {
		target := trace.TracerProvider(noop.NewTracerProvider())
		if current != pristineTracerProvider {
			target = current
		}
		globalTracerProvider.setTarget(target)
		otel.SetTracerProvider(globalTracerProvider)
	}

	if globalPropagator == nil {
		globalPropagator = newSwitchablePropagator(propagation.NewCompositeTextMapPropagator())
	}
	if current := otel.GetTextMapPropagator(); current != propagation.TextMapPropagator(globalPropagator) {
		target := propagation.TextMapPropagator(propagation.NewCompositeTextMapPropagator())
		if current != pristinePropagator {
			target = current
		}
		globalPropagator.setTarget(target)
		otel.SetTextMapPropagator(globalPropagator)
	}
}

// NewSpanRecorder for testing.
// It sets up a [tracetest.SpanRecorder] as the global [sdktrace.TracerProvider] for the duration of the test,
// restoring whatever provider was active before once the test ends.
// It is not safe for use with parallel tests, as it mutates the global tracer provider.
func NewSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	ensureAttached()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	previous := globalTracerProvider.getTarget()
	globalTracerProvider.setTarget(tp)

	t.Cleanup(func() {
		_ = tp.Shutdown(context.WithoutCancel(t.Context()))
		globalTracerProvider.setTarget(previous)
	})

	return sr
}

// UsePropagators sets the given propagators, composed, as the global [propagation.TextMapPropagator] for
// the duration of the test, restoring whatever propagator was active before once the test ends.
// It is not safe for use with parallel tests, as it mutates the global text map propagator.
func UsePropagators(t *testing.T, propagators ...propagation.TextMapPropagator) propagation.TextMapPropagator {
	t.Helper()
	ensureAttached()

	previous := globalPropagator.getTarget()
	p := propagation.NewCompositeTextMapPropagator(propagators...)
	globalPropagator.setTarget(p)

	t.Cleanup(func() {
		globalPropagator.setTarget(previous)
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
