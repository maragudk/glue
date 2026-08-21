package otel_test

import (
	"testing"

	"go.opentelemetry.io/otel/attribute"
	"maragu.dev/is"

	glueotel "maragu.dev/glue/otel"
	"maragu.dev/glue/oteltest"
)

func TestMainSpanAttributes(t *testing.T) {
	t.Run("marks the span as main", func(t *testing.T) {
		is.True(t, oteltest.HasAttribute(glueotel.MainSpanAttributes(), attribute.Bool("main", true)))
	})

	t.Run("has an uptime in whole seconds which is never negative", func(t *testing.T) {
		v := attributeValue(t, glueotel.MainSpanAttributes(), "uptime_sec")
		is.Equal(t, attribute.INT64, v.Type())
		is.True(t, v.AsInt64() >= 0, "uptime should not be negative")
	})

	t.Run("has an uptime bucket which is a non-negative integer", func(t *testing.T) {
		v := attributeValue(t, glueotel.MainSpanAttributes(), "uptime_sec_log_10")
		is.Equal(t, attribute.INT64, v.Type())
		is.True(t, v.AsInt64() >= 0, "uptime bucket should not be negative")
	})

	t.Run("returns a new slice on every call, so one caller cannot affect another", func(t *testing.T) {
		attrs := glueotel.MainSpanAttributes()
		attrs[0] = attribute.Bool("main", false)

		is.True(t, oteltest.HasAttribute(glueotel.MainSpanAttributes(), attribute.Bool("main", true)))
	})
}

// attributeValue for the given key, failing the test if there is no attribute with that key.
func attributeValue(t *testing.T, attrs []attribute.KeyValue, key attribute.Key) attribute.Value {
	t.Helper()

	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value
		}
	}

	t.Fatalf("no attribute with key %v", key)
	return attribute.Value{}
}
