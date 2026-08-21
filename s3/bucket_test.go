package s3_test

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"iter"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/config"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.43.0"
	"golang.org/x/sync/errgroup"
	"maragu.dev/env"
	"maragu.dev/is"

	"maragu.dev/glue/oteltest"
	"maragu.dev/glue/s3"
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

		is.EqualSlice(t, []string{"test"}, collectKeys(t, b.List(t.Context(), "")))

		err = b.Delete(t.Context(), "test")
		is.NotError(t, err)

		body, err = b.Get(t.Context(), "test")
		is.NotError(t, err)
		is.True(t, body == nil)

		err = b.Delete(t.Context(), "test")
		is.NotError(t, err)
	})

	t.Run("records a span with the bucket and key when putting an object", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := s3test.NewBucket(t)

		err := b.Put(t.Context(), "test", "text/plain", strings.NewReader("hello"))
		is.NotError(t, err)

		span := findSpan(t, sr.Ended(), "s3.put")
		is.True(t, span != nil)
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.AWSS3Key("test")))
	})

	t.Run("records a span with the bucket and key when getting an object", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := s3test.NewBucket(t)

		_, err := b.Get(t.Context(), "test")
		is.NotError(t, err)

		span := findSpan(t, sr.Ended(), "s3.get")
		is.True(t, span != nil)
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.AWSS3Key("test")))
	})

	t.Run("records a span with the bucket and key when deleting an object", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := s3test.NewBucket(t)

		err := b.Delete(t.Context(), "test")
		is.NotError(t, err)

		span := findSpan(t, sr.Ended(), "s3.delete")
		is.True(t, span != nil)
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.AWSS3Key("test")))
	})

	t.Run("records a span with the bucket and key when checking whether an object exists", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := s3test.NewBucket(t)

		_, err := b.Exists(t.Context(), "test")
		is.NotError(t, err)

		span := findSpan(t, sr.Ended(), "s3.exists")
		is.True(t, span != nil)
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.AWSS3Key("test")))
	})

	t.Run("records a span with the bucket and prefix when listing objects", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := s3test.NewBucket(t)

		_ = collectKeys(t, b.List(t.Context(), "test"))

		span := findSpan(t, sr.Ended(), "s3.list")
		is.True(t, span != nil)
		is.True(t, oteltest.HasAttribute(span.Attributes(), semconv.AWSS3Key("test")))
	})

	t.Run("records no span when a list iterator is never consumed", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := s3test.NewBucket(t)

		_ = b.List(t.Context(), "test")

		is.True(t, findSpan(t, sr.Ended(), "s3.list") == nil)
	})
}

func TestBucket_List(t *testing.T) {
	t.Run("lists all keys in lexical order", func(t *testing.T) {
		b := s3test.NewBucket(t)

		putKeys(t, b, "test2", "test1")

		is.EqualSlice(t, []string{"test1", "test2"}, collectKeys(t, b.List(t.Context(), "")))
	})

	t.Run("lists keys under prefix", func(t *testing.T) {
		b := s3test.NewBucket(t)

		putKeys(t, b, "test1", "test2")

		is.EqualSlice(t, []string{"test1", "test2"}, collectKeys(t, b.List(t.Context(), "test")))
		is.EqualSlice(t, []string{"test1"}, collectKeys(t, b.List(t.Context(), "test1")))
	})

	t.Run("pages through all keys under prefix in lexical order", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := s3test.NewBucket(t)

		sheep := make([]string, 1001)
		for i := range sheep {
			sheep[i] = fmt.Sprintf("sheep/%04d", i)
		}
		putKeys(t, b, sheep...)
		putKeys(t, b, "wolf")

		is.EqualSlice(t, sheep, collectKeys(t, b.List(t.Context(), "sheep/")))

		// One s3.list span covers the whole iteration, even across page boundaries.
		var listSpans int
		for _, span := range sr.Ended() {
			if span.Name() == "s3.list" {
				listSpans++
			}
		}
		is.Equal(t, 1, listSpans)

		// LastKey reuses this fixture to check the last key across the page boundary.
		key, err := b.LastKey(t.Context(), "sheep/")
		is.NotError(t, err)
		is.Equal(t, "sheep/1000", key)
	})

	t.Run("stops cleanly when breaking early", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := s3test.NewBucket(t)

		putKeys(t, b, "duck1", "duck2", "goose")

		var keys []string
		for key, err := range b.List(t.Context(), "") {
			is.NotError(t, err)
			keys = append(keys, key)
			break
		}
		is.EqualSlice(t, []string{"duck1"}, keys)

		// The s3.list span still ends when breaking early.
		is.True(t, findSpan(t, sr.Ended(), "s3.list") != nil)
	})

	t.Run("yields an empty key and the error once when the bucket does not exist", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := newMissingBucket(t)

		var count int
		for key, err := range b.List(t.Context(), "") {
			count++
			is.Equal(t, "", key)
			is.True(t, err != nil)
		}
		is.Equal(t, 1, count)

		span := findSpan(t, sr.Ended(), "s3.list")
		is.True(t, span != nil)
		is.Equal(t, codes.Error, span.Status().Code)
	})

	t.Run("yields an empty key and the error once when the context is canceled", func(t *testing.T) {
		b := s3test.NewBucket(t)

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		var count int
		for key, err := range b.List(ctx, "") {
			count++
			is.Equal(t, "", key)
			is.True(t, err != nil)
		}
		is.Equal(t, 1, count)
	})
}

func TestBucket_LastKey(t *testing.T) {
	t.Run("returns the lexically last key under prefix", func(t *testing.T) {
		b := s3test.NewBucket(t)

		putKeys(t, b, "animals/aardvark", "animals/marmot", "animals/zebra", "plants/cactus")

		key, err := b.LastKey(t.Context(), "animals/")
		is.NotError(t, err)
		is.Equal(t, "animals/zebra", key)
	})

	t.Run("returns an empty string and no error when there are no keys under prefix", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		b := s3test.NewBucket(t)

		key, err := b.LastKey(t.Context(), "unicorns/")
		is.NotError(t, err)
		is.Equal(t, "", key)

		// LastKey has no span of its own, just the s3.list span from the iterator it drains.
		spans := sr.Ended()
		is.Equal(t, 1, len(spans))
		is.Equal(t, "s3.list", spans[0].Name())
	})

	t.Run("returns the error when listing fails", func(t *testing.T) {
		b := newMissingBucket(t)

		key, err := b.LastKey(t.Context(), "")
		is.True(t, err != nil)
		is.Equal(t, "", key)
	})
}

func TestBucket_Exists(t *testing.T) {
	t.Run("returns true when an object exists", func(t *testing.T) {
		b := s3test.NewBucket(t)

		err := b.Put(t.Context(), "test", "text/plain", strings.NewReader("hello"))
		is.NotError(t, err)

		exists, err := b.Exists(t.Context(), "test")
		is.NotError(t, err)
		is.True(t, exists)
	})

	t.Run("returns false when an object does not exist", func(t *testing.T) {
		b := s3test.NewBucket(t)

		exists, err := b.Exists(t.Context(), "test")
		is.NotError(t, err)
		is.Equal(t, false, exists)
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

// newMissingBucket for testing error paths: configured like [s3test.NewBucket], but the underlying bucket is never created.
func newMissingBucket(t *testing.T) *s3.Bucket {
	t.Helper()

	if testing.Short() {
		t.SkipNow()
	}

	_ = env.Load("../.env.test")

	for key, value := range map[string]string{
		"AWS_ACCESS_KEY_ID":     "access",
		"AWS_SECRET_ACCESS_KEY": "secretsecret",
		"AWS_REGION":            "us-east-1",
		"AWS_ENDPOINT_URL":      "http://localhost:7072",
	} {
		if os.Getenv(key) == "" {
			t.Setenv(key, value)
		}
	}

	awsConfig, err := config.LoadDefaultConfig(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	return s3.NewBucket(s3.NewBucketOptions{
		Config:    awsConfig,
		Name:      strings.ToLower(rand.Text()),
		PathStyle: true,
	})
}

// putKeys into the bucket concurrently, each with an empty body.
func putKeys(t *testing.T, b *s3.Bucket, keys ...string) {
	t.Helper()

	var eg errgroup.Group
	eg.SetLimit(16)
	for _, key := range keys {
		eg.Go(func() error {
			return b.Put(t.Context(), key, "text/plain", strings.NewReader(""))
		})
	}
	if err := eg.Wait(); err != nil {
		t.Fatal(err)
	}
}

// collectKeys from seq, failing the test on any yielded error.
func collectKeys(t *testing.T, seq iter.Seq2[string, error]) []string {
	t.Helper()

	var keys []string
	for key, err := range seq {
		is.NotError(t, err)
		keys = append(keys, key)
	}
	return keys
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
