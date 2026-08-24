package oteltest_test

import (
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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

	t.Run("restores the previous tracer provider on cleanup", func(t *testing.T) {
		previous := otel.GetTracerProvider()

		// Run in a sub-test so its cleanup executes before our assertion.
		t.Run("inner", func(t *testing.T) {
			oteltest.NewSpanRecorder(t)
		})

		is.Equal(t, previous, otel.GetTracerProvider())
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
