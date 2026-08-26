package oteltest

import (
	"sync"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"maragu.dev/is"
)

// withFreshInstall resets [install] and the pristine baseline it compares against, so a test can exercise
// its first-use decision more than once within a single test binary, as though nothing in this package had
// run before it in this process. Whatever is globally active when this runs becomes the new baseline, and
// everything -- the actual global tracer provider and propagator, and this package's own bookkeeping -- is
// restored to what it was once the test ends.
//
// This does not, and cannot, undo a [trace.Tracer] or propagator reference some other test obtained and
// kept from before this reset -- that reference is bound to whatever was active when it was obtained,
// permanently, regardless of any reset here (see [install]'s own doc comment). No test in this package
// currently does that, so this stays safe to use in practice, but a future test which does would see it.
func withFreshInstall(t *testing.T) {
	t.Helper()

	previousActiveTP := otel.GetTracerProvider()
	previousActiveProp := otel.GetTextMapPropagator()
	previousInstall := install
	previousPristineTP := pristineTracerProvider
	previousPristineProp := pristinePropagator
	previousGlobalTP := globalTracerProvider
	previousGlobalProp := globalPropagator

	pristineTracerProvider = previousActiveTP
	pristinePropagator = previousActiveProp
	install = sync.OnceFunc(doInstall)

	t.Cleanup(func() {
		otel.SetTracerProvider(previousActiveTP)
		otel.SetTextMapPropagator(previousActiveProp)
		install = previousInstall
		pristineTracerProvider = previousPristineTP
		pristinePropagator = previousPristineProp
		globalTracerProvider = previousGlobalTP
		globalPropagator = previousGlobalProp
	})
}

func TestInstall(t *testing.T) {
	t.Run("uses an inert stand-in for both when nothing has configured either yet", func(t *testing.T) {
		withFreshInstall(t)

		// This is the state a genuinely pristine process is in, from install's point of view: nothing has
		// called otel.SetTracerProvider or otel.SetTextMapPropagator since withFreshInstall reset the
		// baseline above. install is meant to fall back to an inert stand-in of its own in this case, rather
		// than the still-untouched default -- which risks the switchable ending up targeting itself once
		// installed, looping forever.
		t.Run("inner", func(t *testing.T) {
			NewSpanRecorder(t)
			UsePropagators(t, propagation.TraceContext{})

			ctx, span := otel.Tracer("test").Start(t.Context(), "test-span")
			defer span.End()

			carrier := propagation.MapCarrier{}
			otel.GetTextMapPropagator().Inject(ctx, carrier)
			is.True(t, carrier.Get("traceparent") != "", "expected the trace-context propagator to be active")
		})

		// Nothing was configured before install ran, so nothing should be reachable after cleanup either.
		_, span := otel.Tracer("test").Start(t.Context(), "after-cleanup-span")
		span.End()
		is.True(t, !span.SpanContext().IsValid(), "expected no tracer provider to be active")

		carrier := propagation.MapCarrier{}
		otel.GetTextMapPropagator().Inject(t.Context(), carrier)
		is.Equal(t, "", carrier.Get("traceparent"), "expected no propagator to be active")
	})

	t.Run("keeps a tracer provider configured before its first run, restoring it after cleanup", func(t *testing.T) {
		withFreshInstall(t)

		sr := tracetest.NewSpanRecorder()
		preConfigured := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
		otel.SetTracerProvider(preConfigured) // simulates a consumer's own TestMain, run before any test

		t.Run("inner", func(t *testing.T) {
			NewSpanRecorder(t)
		})

		_, span := otel.Tracer("test").Start(t.Context(), "after-cleanup-span")
		span.End()

		is.Equal(t, 1, len(sr.Ended()), "expected the pre-configured provider to be active again")
	})

	t.Run("keeps a propagator configured before its first run, restoring it after cleanup", func(t *testing.T) {
		withFreshInstall(t)

		otel.SetTextMapPropagator(propagation.TraceContext{}) // simulates a consumer's own TestMain, run before any test

		t.Run("inner", func(t *testing.T) {
			UsePropagators(t, propagation.Baggage{})
		})

		// A real recorder, so the span injected below carries a valid context to write a traceparent for.
		NewSpanRecorder(t)
		ctx, span := otel.Tracer("test").Start(t.Context(), "test-span")
		defer span.End()

		carrier := propagation.MapCarrier{}
		otel.GetTextMapPropagator().Inject(ctx, carrier)
		is.True(t, carrier.Get("traceparent") != "", "expected the pre-configured trace-context propagator back")
	})
}
