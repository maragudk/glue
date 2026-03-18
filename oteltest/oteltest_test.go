package oteltest_test

import (
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
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
