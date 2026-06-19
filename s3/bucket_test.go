package s3_test

import (
	"io"
	"strings"
	"testing"
	"time"

	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"maragu.dev/is"

	"maragu.dev/glue/oteltest"
	"maragu.dev/glue/s3test"
)

func TestBucket(t *testing.T) {
	t.Run("puts, gets, lists, and deletes an object", func(t *testing.T) {
		b := s3test.NewBucket(t)

		err := b.Put(t.Context(), "test", "text/plain", strings.NewReader("hello"))
		is.NotError(t, err)

		body, err := b.Get(t.Context(), "test")
		is.NotError(t, err)
		bodyBytes, err := io.ReadAll(body)
		is.NotError(t, err)
		is.Equal(t, "hello", string(bodyBytes))

		keys, err := b.List(t.Context(), "", 100)
		is.NotError(t, err)
		is.EqualSlice(t, []string{"test"}, keys)

		err = b.Delete(t.Context(), "test")
		is.NotError(t, err)

		body, err = b.Get(t.Context(), "test")
		is.NotError(t, err)
		is.True(t, body == nil)

		err = b.Delete(t.Context(), "test")
		is.NotError(t, err)
	})

	t.Run("records an app-level span and a nested AWS SDK span when putting an object", func(t *testing.T) {
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

func TestBucket_List(t *testing.T) {
	t.Run("lists all objects", func(t *testing.T) {
		b := s3test.NewBucket(t)

		err := b.Put(t.Context(), "test1", "text/plain", strings.NewReader(""))
		is.NotError(t, err)

		err = b.Put(t.Context(), "test2", "text/plain", strings.NewReader(""))
		is.NotError(t, err)

		keys, err := b.List(t.Context(), "", 100)
		is.NotError(t, err)
		is.EqualSlice(t, []string{"test1", "test2"}, keys)
	})

	t.Run("lists objects with prefix", func(t *testing.T) {
		b := s3test.NewBucket(t)

		err := b.Put(t.Context(), "test1", "text/plain", strings.NewReader(""))
		is.NotError(t, err)

		err = b.Put(t.Context(), "test2", "text/plain", strings.NewReader(""))
		is.NotError(t, err)

		keys, err := b.List(t.Context(), "test", 100)
		is.NotError(t, err)
		is.EqualSlice(t, []string{"test1", "test2"}, keys)

		keys, err = b.List(t.Context(), "test1", 100)
		is.NotError(t, err)
		is.EqualSlice(t, []string{"test1"}, keys)
	})
}

func TestBucket_GetPresignedURL(t *testing.T) {
	t.Run("returns a presigned URL", func(t *testing.T) {
		b := s3test.NewBucket(t)

		url, err := b.GetPresignedURL(t.Context(), "test", time.Hour)
		is.NotError(t, err)

		t.Log(url)
		is.True(t, strings.Contains(url, "/test?X-Amz-Algorithm"))
		is.True(t, strings.Contains(url, "Expires=3600"))
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
