package oteltest

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
	"maragu.dev/is"
)

func TestSwitchableTracerProvider(t *testing.T) {
	t.Run("a tracer obtained before a target swap still resolves against the new target", func(t *testing.T) {
		provider := newSwitchableTracerProvider(noop.NewTracerProvider())

		// Obtained while the provider still targets noop -- the situation a [trace.Tracer] cached before
		// [install] ever configures anything is in.
		tracer := provider.Tracer("test")

		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
		provider.setTarget(tp)
		t.Cleanup(func() { _ = tp.Shutdown(context.WithoutCancel(t.Context())) })

		_, span := tracer.Start(t.Context(), "test-span")
		span.End()

		is.Equal(t, 1, len(sr.Ended()))
	})

	t.Run("stops delivering spans once the target is swapped away", func(t *testing.T) {
		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
		t.Cleanup(func() { _ = tp.Shutdown(context.WithoutCancel(t.Context())) })

		provider := newSwitchableTracerProvider(tp)
		tracer := provider.Tracer("test")

		provider.setTarget(noop.NewTracerProvider())

		_, span := tracer.Start(t.Context(), "test-span")
		span.End()

		is.Equal(t, 0, len(sr.Ended()))
	})
}

func TestSwitchablePropagator(t *testing.T) {
	t.Run("a reference obtained before a target swap still resolves against the new target", func(t *testing.T) {
		propagator := newSwitchablePropagator(propagation.NewCompositeTextMapPropagator())

		provider := newSwitchableTracerProvider(noop.NewTracerProvider())
		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
		provider.setTarget(tp)
		t.Cleanup(func() { _ = tp.Shutdown(context.WithoutCancel(t.Context())) })

		ctx, span := provider.Tracer("test").Start(t.Context(), "test-span")
		defer span.End()

		propagator.setTarget(propagation.TraceContext{})

		carrier := propagation.MapCarrier{}
		propagator.Inject(ctx, carrier)
		is.True(t, carrier.Get("traceparent") != "", "expected the swapped-in propagator to be used")
	})
}
