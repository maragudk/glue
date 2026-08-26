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
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
)

// pristineTracerProvider and pristinePropagator are captured during this package's own initialization,
// which Go guarantees runs before any test in this package or a consumer's own [testing.M.Run] -- so before
// either of those can have configured the global tracer provider or text map propagator. (A sibling package
// with no dependency relationship to this one could in principle still run its own init first and configure
// a global before this capture happens; this package has no way to detect or guard against that.) [install]
// compares the current global against these captured values to tell a real configuration apart from nothing
// having touched the global yet.
var (
	pristineTracerProvider = otel.GetTracerProvider()
	pristinePropagator     = otel.GetTextMapPropagator()
)

// globalTracerProvider and globalPropagator are the stand-ins [install] puts in place of the global tracer
// provider and text map propagator. [NewSpanRecorder] and [UsePropagators] swap their targets instead of
// calling [otel.SetTracerProvider] or [otel.SetTextMapPropagator] again, so restoring "previous" afterwards
// is always a plain, safe assignment.
var (
	globalTracerProvider *switchableTracerProvider
	globalPropagator     *switchablePropagator
)

// install the switchable stand-ins as the global tracer provider and text map propagator, once, the first
// time either helper below runs.
//
// Naively saving whatever [otel.GetTracerProvider] or [otel.GetTextMapPropagator] currently returns and
// restoring it later is not reliable: a provider or propagator installed via [otel.SetTracerProvider] or
// [otel.SetTextMapPropagator] can go on affecting later calls even after being "restored" away this way,
// and a [trace.Tracer] obtained via [otel.Tracer] before the first such call in a process can fail to pick
// up any later one at all. Both problems disappear once this package's own stand-ins are the only thing
// ever passed to [otel.SetTracerProvider] and [otel.SetTextMapPropagator]: every later change becomes a
// plain field assignment on a stand-in this package owns, and both stand-ins resolve their current target
// on every call, live, rather than fixing one when a [trace.Tracer] or propagator reference is first
// obtained.
//
// A provider or propagator a consumer configured before this runs -- in a [testing.M.Run] before any test,
// for instance -- becomes the stand-in's starting target rather than being replaced, so it is what
// "previous" resolves back to once every helper below has cleaned up. Only when nothing has configured
// either yet does the starting target default to an inert stand-in of its own (a no-op tracer provider, an
// empty composite propagator); starting instead from whatever [otel.GetTracerProvider] or
// [otel.GetTextMapPropagator] returns in that case risks the stand-in ending up targeting itself once
// installed, looping forever.
//
// One case is out of reach regardless: a [trace.Tracer] or propagator reference a consumer obtained and
// kept, from before this runs, and from before that consumer configured its own provider or propagator --
// that reference is bound to whatever the consumer set, permanently, and no later call to a helper below
// can redirect it. That is an existing property of the OpenTelemetry API a test helper has no way to change,
// not something introduced here.
var install = sync.OnceFunc(doInstall)

// doInstall is [install]'s body, pulled out on its own so a test can re-run the decision it makes under a
// freshly reset [install] without duplicating it.
func doInstall() {
	tracerTarget := trace.TracerProvider(noop.NewTracerProvider())
	if current := otel.GetTracerProvider(); current != pristineTracerProvider {
		tracerTarget = current
	}
	globalTracerProvider = newSwitchableTracerProvider(tracerTarget)
	otel.SetTracerProvider(globalTracerProvider)

	propagatorTarget := propagation.TextMapPropagator(propagation.NewCompositeTextMapPropagator())
	if current := otel.GetTextMapPropagator(); current != pristinePropagator {
		propagatorTarget = current
	}
	globalPropagator = newSwitchablePropagator(propagatorTarget)
	otel.SetTextMapPropagator(globalPropagator)
}

// NewSpanRecorder for testing.
// It sets up a [tracetest.SpanRecorder] as the global [sdktrace.TracerProvider] for the duration of the test,
// restoring whatever provider was in place before once the test ends -- a consumer's own, an outer test's,
// or an inert stand-in if nothing had configured one yet.
// It is not safe for use with parallel tests, as it mutates the global tracer provider.
func NewSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	install()

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
// the duration of the test, restoring whatever propagator was in place before once the test ends -- a
// consumer's own, an outer test's, or an inert stand-in if nothing had configured one yet.
// It is not safe for use with parallel tests, as it mutates the global text map propagator.
func UsePropagators(t *testing.T, propagators ...propagation.TextMapPropagator) propagation.TextMapPropagator {
	t.Helper()
	install()

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
