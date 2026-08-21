package app

import (
	"context"
	"errors"
	"log/slog"
	"runtime"
	"runtime/debug"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	"maragu.dev/is"

	"maragu.dev/glue/oteltest"
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
	t.Run("should return a non-empty version", func(t *testing.T) {
		version, _ := getVersionAndBuildTime()
		is.True(t, version != "")
	})
}

func TestVersionAndBuildTime(t *testing.T) {
	tests := []struct {
		name      string
		settings  []debug.BuildSetting
		version   string
		buildTime string
	}{
		{
			name:      "no build settings at all",
			version:   "unknown",
			buildTime: "",
		},
		{
			name:      "build settings without VCS stamps",
			settings:  []debug.BuildSetting{{Key: "GOARCH", Value: "arm64"}},
			version:   "unknown",
			buildTime: "",
		},
		{
			name:      "both VCS stamps",
			settings:  []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}, {Key: "vcs.time", Value: "2026-08-21T09:41:00Z"}},
			version:   "abc123",
			buildTime: "2026-08-21T09:41:00Z",
		},
		{
			name:      "a revision but no time",
			settings:  []debug.BuildSetting{{Key: "vcs.revision", Value: "abc123"}},
			version:   "abc123",
			buildTime: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			version, buildTime := versionAndBuildTime(test.settings)
			is.Equal(t, test.version, version)
			is.Equal(t, test.buildTime, buildTime)
		})
	}
}

func TestOtelResourceOptions(t *testing.T) {
	t.Run("should describe the Go runtime the process is running on", func(t *testing.T) {
		r, err := resource.New(t.Context(), otelResourceOptions("")...)
		is.NotError(t, err)

		is.True(t, oteltest.HasAttribute(r.Attributes(), attribute.String("process.runtime.name", "go")))
		is.True(t, oteltest.HasAttribute(r.Attributes(), attribute.String("process.runtime.version", runtime.Version())))
	})

	t.Run("should have the build time when the binary carries a VCS timestamp", func(t *testing.T) {
		r, err := resource.New(t.Context(), otelResourceOptions("2026-08-21T09:41:00Z")...)
		is.NotError(t, err)

		is.True(t, oteltest.HasAttribute(r.Attributes(), attribute.String("service.build.time", "2026-08-21T09:41:00Z")))
	})

	t.Run("should have no build time at all when the binary carries no VCS timestamp", func(t *testing.T) {
		r, err := resource.New(t.Context(), otelResourceOptions("")...)
		is.NotError(t, err)

		is.True(t, !oteltest.HasAttributeKey(r.Attributes(), "service.build.time"), "expected no service.build.time attribute")
	})
}
