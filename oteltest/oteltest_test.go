package oteltest_test

import (
	"errors"
	"slices"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"go.opentelemetry.io/otel/trace"
	"maragu.dev/is"

	"maragu.dev/glue/oteltest"
)

func TestNewSpanRecorder(t *testing.T) {
	t.Run("records spans from the global tracer provider", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		_, span := otel.Tracer("test").Start(t.Context(), "test-span")
		span.End()

		spans := sr.Ended()
		is.Equal(t, 1, len(spans))
		is.Equal(t, "test-span", spans[0].Name())
	})

	t.Run("does not record spans through the global tracer after its own cleanup", func(t *testing.T) {
		// Run in a sub-test so its cleanup executes before the span below is started, so a span started
		// afterwards must not still reach the recorder that cleanup was supposed to retire.
		var sr *tracetest.SpanRecorder
		t.Run("inner", func(t *testing.T) {
			sr = oteltest.NewSpanRecorder(t)
		})

		_, span := otel.Tracer("test").Start(t.Context(), "after-cleanup-span")
		span.End()

		is.Equal(t, 0, len(sr.Ended()), "expected the shut down recorder to receive no further spans")
	})

	t.Run("restores the outer recorder for a nested test", func(t *testing.T) {
		outer := oteltest.NewSpanRecorder(t)

		t.Run("inner", func(t *testing.T) {
			inner := oteltest.NewSpanRecorder(t)

			_, span := otel.Tracer("test").Start(t.Context(), "inner-span")
			span.End()

			is.Equal(t, 1, len(inner.Ended()))
		})

		// The inner recorder's cleanup has already run, so a span started now has to land back on the
		// outer recorder rather than going nowhere or leaking into the inner one.
		_, span := otel.Tracer("test").Start(t.Context(), "outer-span")
		span.End()

		spans := outer.Ended()
		is.Equal(t, 1, len(spans))
		is.Equal(t, "outer-span", spans[0].Name())
	})
}

func TestUsePropagators(t *testing.T) {
	t.Run("injects with the given propagators", func(t *testing.T) {
		// A real recorder, so the started span carries a valid span context to inject: the propagator has
		// nothing to write for the invalid context a no-op tracer produces.
		oteltest.NewSpanRecorder(t)
		oteltest.UsePropagators(t, propagation.TraceContext{})

		ctx, span := otel.Tracer("test").Start(t.Context(), "test-span")
		defer span.End()

		carrier := propagation.MapCarrier{}
		otel.GetTextMapPropagator().Inject(ctx, carrier)
		is.True(t, carrier.Get("traceparent") != "", "expected a traceparent header to be injected")
	})

	t.Run("does not propagate through the global propagator after its own cleanup", func(t *testing.T) {
		// A real recorder for the whole test, so the span injected below always carries a valid context
		// regardless of what the inner sub-test does -- otherwise a propagator with nothing to inject would
		// pass this check for the wrong reason.
		oteltest.NewSpanRecorder(t)

		// Run in a sub-test so its cleanup executes before the injection below, so an injection afterwards
		// must not still carry what the inner test's propagator would have written.
		t.Run("inner", func(t *testing.T) {
			oteltest.UsePropagators(t, propagation.TraceContext{})
		})

		ctx, span := otel.Tracer("test").Start(t.Context(), "test-span")
		defer span.End()

		carrier := propagation.MapCarrier{}
		otel.GetTextMapPropagator().Inject(ctx, carrier)
		is.Equal(t, "", carrier.Get("traceparent"), "expected no propagator to be installed after cleanup")
	})

	t.Run("restores the outer propagator for a nested test", func(t *testing.T) {
		oteltest.UsePropagators(t, propagation.Baggage{})

		t.Run("inner", func(t *testing.T) {
			oteltest.UsePropagators(t, propagation.TraceContext{})
		})

		// The inner test's cleanup has already run, so only baggage should propagate again, not trace
		// context and not nothing.
		fields := otel.GetTextMapPropagator().Fields()
		is.True(t, slices.Contains(fields, "baggage"), "expected the outer propagator's baggage field back")
		is.True(t, !slices.Contains(fields, "traceparent"), "expected the inner propagator to be gone")
	})
}

func TestHasAttribute(t *testing.T) {
	t.Run("returns true when attribute is present", func(t *testing.T) {
		attrs := []attribute.KeyValue{
			attribute.String("foo", "bar"),
			attribute.Int("count", 42),
		}
		is.True(t, oteltest.HasAttribute(attrs, attribute.String("foo", "bar")))
		is.True(t, oteltest.HasAttribute(attrs, attribute.Int("count", 42)))
	})

	t.Run("returns false when attribute key is missing", func(t *testing.T) {
		attrs := []attribute.KeyValue{attribute.String("foo", "bar")}
		is.True(t, !oteltest.HasAttribute(attrs, attribute.String("missing", "bar")))
	})

	t.Run("returns false when attribute value differs", func(t *testing.T) {
		attrs := []attribute.KeyValue{attribute.String("foo", "bar")}
		is.True(t, !oteltest.HasAttribute(attrs, attribute.String("foo", "baz")))
	})

	t.Run("returns false for empty slice", func(t *testing.T) {
		is.True(t, !oteltest.HasAttribute(nil, attribute.String("foo", "bar")))
	})
}

func TestHasAttributeKey(t *testing.T) {
	t.Run("returns true when the key is present, regardless of value", func(t *testing.T) {
		attrs := []attribute.KeyValue{attribute.String("foo", "bar")}
		is.True(t, oteltest.HasAttributeKey(attrs, "foo"))
	})

	t.Run("returns false when the key is missing", func(t *testing.T) {
		attrs := []attribute.KeyValue{attribute.String("foo", "bar")}
		is.True(t, !oteltest.HasAttributeKey(attrs, "missing"))
	})

	t.Run("returns false for empty slice", func(t *testing.T) {
		is.True(t, !oteltest.HasAttributeKey(nil, "foo"))
	})
}

func TestExceptionEventsWithStackTrace(t *testing.T) {
	t.Run("returns only the exception events which carry a stack trace", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		_, span := otel.Tracer("test").Start(t.Context(), "test-span")
		span.RecordError(errors.New("the parrot has ceased to be"), trace.WithStackTrace(true))
		span.RecordError(errors.New("it is an ex-parrot"))

		// An event which carries a stack trace without being an exception, so the name is the only
		// thing which can rule it out
		span.AddEvent("something else entirely", trace.WithAttributes(semconv.ExceptionStacktrace("goroutine 1 [running]:")))
		span.End()

		spans := sr.Ended()
		is.Equal(t, 1, len(spans))

		events := oteltest.ExceptionEventsWithStackTrace(spans[0])
		is.Equal(t, 1, len(events))
		is.True(t, oteltest.HasAttribute(events[0].Attributes, semconv.ExceptionMessage("the parrot has ceased to be")))
	})

	t.Run("returns nothing for a span with no events at all", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		_, span := otel.Tracer("test").Start(t.Context(), "test-span")
		span.End()

		spans := sr.Ended()
		is.Equal(t, 1, len(spans))
		is.Equal(t, 0, len(oteltest.ExceptionEventsWithStackTrace(spans[0])))
	})
}
