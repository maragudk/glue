package otel_test

import (
	"errors"
	"strings"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
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

func TestRecordPanic(t *testing.T) {
	tests := []struct {
		name            string
		value           any
		expectedType    string
		expectedMessage string
	}{
		// A value which is already an error is recorded as it is, so the exception keeps the type
		// panicked with. Wrapping would make every panic look like the wrapper's type instead.
		{name: "error", value: errors.New("the parrot has ceased to be"), expectedType: "*errors.errorString", expectedMessage: "the parrot has ceased to be"},
		{name: "string", value: "the parrot has ceased to be", expectedType: "*errors.errorString", expectedMessage: "panic: the parrot has ceased to be"},
		{name: "anything else", value: 42, expectedType: "*errors.errorString", expectedMessage: "panic: 42"},
	}

	for _, test := range tests {
		t.Run("records a "+test.name+" panic value as an exception with an error status", func(t *testing.T) {
			sr := oteltest.NewSpanRecorder(t)

			_, span := otel.Tracer("test").Start(t.Context(), "test-span")
			glueotel.RecordPanic(span, test.value)
			span.End()

			spans := sr.Ended()
			is.Equal(t, 1, len(spans))
			is.Equal(t, codes.Error, spans[0].Status().Code)
			is.Equal(t, "panic", spans[0].Status().Description)

			events := oteltest.ExceptionEventsWithStackTrace(spans[0])
			is.Equal(t, 1, len(events))
			is.True(t, oteltest.HasAttribute(events[0].Attributes, semconv.ExceptionType(test.expectedType)),
				"expected exception type "+test.expectedType)
			is.True(t, oteltest.HasAttribute(events[0].Attributes, semconv.ExceptionMessage(test.expectedMessage)),
				"expected exception message "+test.expectedMessage)
		})
	}

	t.Run("records the stack being unwound when called from a deferred function", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		_, span := otel.Tracer("test").Start(t.Context(), "test-span")

		func() {
			defer func() {
				v := recover()
				defer span.End()
				glueotel.RecordPanic(span, v)
			}()
			panicLikeAParrot()
		}()

		spans := sr.Ended()
		is.Equal(t, 1, len(spans))

		events := oteltest.ExceptionEventsWithStackTrace(spans[0])
		is.Equal(t, 1, len(events))
		is.True(t, strings.Contains(attributeValue(t, events[0].Attributes, semconv.ExceptionStacktraceKey).AsString(), "panicLikeAParrot"),
			"expected the panicking function in the stacktrace")
	})
}

// panicLikeAParrot so a stack trace has a distinctive frame to be recognised by.
func panicLikeAParrot() {
	panic("the parrot has ceased to be")
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
