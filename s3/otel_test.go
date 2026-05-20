package s3_test

import (
	"strings"
	"testing"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"maragu.dev/is"

	"maragu.dev/glue/oteltest"
	"maragu.dev/glue/s3test"
)

func TestOpenTelemetry(t *testing.T) {
	t.Run("records the app-level span and a nested AWS SDK span for an operation", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := s3test.NewBucket(t)

		err := b.Put(t.Context(), "test", "text/plain", strings.NewReader("hello"))
		is.NotError(t, err)

		spans := sr.Ended()

		appSpan := findSpan(t, spans, "s3.put")
		is.True(t, appSpan != nil)

		// otelaws names SDK spans "<Service>.<Operation>", e.g. "S3.PutObject".
		sdkSpan := findSpan(t, spans, "S3.PutObject")
		is.True(t, sdkSpan != nil)

		// The SDK span should nest under the app-level span, sharing its trace and pointing at it as parent.
		is.Equal(t, appSpan.SpanContext().TraceID().String(), sdkSpan.SpanContext().TraceID().String())
		is.Equal(t, appSpan.SpanContext().SpanID().String(), sdkSpan.Parent().SpanID().String())
	})
}

func findSpan(t *testing.T, spans []sdktrace.ReadOnlySpan, name string) sdktrace.ReadOnlySpan {
	t.Helper()
	for _, s := range spans {
		if s.Name() == name {
			return s
		}
	}
	return nil
}
