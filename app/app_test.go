package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"maragu.dev/is"
)

func TestStart(t *testing.T) {
	t.Run("should start and stop cleanly within timeout and call callback", func(t *testing.T) {
		var called, goroutineCalled bool

		startFunc := func(ctx context.Context, log *slog.Logger, eg Goer) error {
			called = true

			// Add a goroutine that will be stopped when context is done
			eg.Go(func() error {
				goroutineCalled = true
				<-ctx.Done()
				return nil
			})

			return nil
		}

		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()

		err := start(ctx, slog.New(slog.DiscardHandler), "test", "abc123", "2026-08-21T09:41:00Z", startFunc)
		is.NotError(t, err)
		is.True(t, called)
		is.True(t, goroutineCalled)
	})

	t.Run("should return with error", func(t *testing.T) {
		expectedErr := errors.New("oh no")

		startFunc := func(ctx context.Context, log *slog.Logger, eg Goer) error {
			eg.Go(func() error {
				<-ctx.Done()
				return nil
			})

			return expectedErr
		}

		err := start(t.Context(), slog.New(slog.DiscardHandler), "test", "abc123", "2026-08-21T09:41:00Z", startFunc)
		is.Error(t, expectedErr, err)
	})

	t.Run("should return early with error from error group", func(t *testing.T) {
		expectedErr := errors.New("oh no")

		startFunc := func(ctx context.Context, log *slog.Logger, eg Goer) error {
			eg.Go(func() error {
				<-ctx.Done()
				return nil
			})

			eg.Go(func() error {
				return expectedErr
			})

			return nil
		}

		err := start(t.Context(), slog.New(slog.DiscardHandler), "test", "abc123", "2026-08-21T09:41:00Z", startFunc)
		is.Error(t, expectedErr, err)
	})

	t.Run("should return early if context already cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		startFunc := func(ctx context.Context, log *slog.Logger, eg Goer) error {
			eg.Go(func() error {
				<-ctx.Done()
				return nil
			})

			return nil
		}

		err := start(ctx, slog.New(slog.DiscardHandler), "test", "abc123", "2026-08-21T09:41:00Z", startFunc)
		is.NotError(t, err)
	})
}

func TestGetVersionAndBuildTime(t *testing.T) {
	t.Run("should never return an empty version or build time", func(t *testing.T) {
		version, buildTime := getVersionAndBuildTime()
		is.True(t, version != "")
		is.True(t, buildTime != "")
	})
}

func TestRaiseSpanAttributeCountLimit(t *testing.T) {
	t.Run("should raise the limit when the operator has set neither key", func(t *testing.T) {
		// Clearing both also undoes any value left behind by another test which called start
		t.Setenv("OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT", "")
		t.Setenv("OTEL_ATTRIBUTE_COUNT_LIMIT", "")

		is.NotError(t, raiseSpanAttributeCountLimit())
		is.Equal(t, "512", os.Getenv("OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT"))
	})

	t.Run("should keep the operator's value for the span specific key", func(t *testing.T) {
		t.Setenv("OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT", "64")
		t.Setenv("OTEL_ATTRIBUTE_COUNT_LIMIT", "")

		is.NotError(t, raiseSpanAttributeCountLimit())
		is.Equal(t, "64", os.Getenv("OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT"))
	})

	t.Run("should keep the operator's value for the general key, which the SDK also honours", func(t *testing.T) {
		t.Setenv("OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT", "")
		t.Setenv("OTEL_ATTRIBUTE_COUNT_LIMIT", "64")

		is.NotError(t, raiseSpanAttributeCountLimit())
		// Setting the span specific key here would silently beat the operator, since the SDK reads it first
		is.Equal(t, "", os.Getenv("OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT"))
	})

	t.Run("should let a span keep more than the default 128 attributes once raised", func(t *testing.T) {
		// Proves the variable this writes still means what it is written for. A provider reads the
		// limit when it is constructed, so it has to be built after the variable is set.
		t.Setenv("OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT", "")
		t.Setenv("OTEL_ATTRIBUTE_COUNT_LIMIT", "")
		is.NotError(t, raiseSpanAttributeCountLimit())

		sr := tracetest.NewSpanRecorder()
		tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
		t.Cleanup(func() {
			_ = tp.Shutdown(context.WithoutCancel(t.Context()))
		})

		attrs := make([]attribute.KeyValue, 0, 300)
		for i := range 300 {
			attrs = append(attrs, attribute.Int(fmt.Sprint("attr", i), i))
		}

		_, span := tp.Tracer("test").Start(t.Context(), "wide")
		span.SetAttributes(attrs...)
		span.End()

		spans := sr.Ended()
		is.Equal(t, 1, len(spans))
		is.Equal(t, 300, len(spans[0].Attributes()))
	})
}
