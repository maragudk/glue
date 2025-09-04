package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

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

		err := start(ctx, slog.New(slog.DiscardHandler), "test", startFunc)
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

		err := start(t.Context(), slog.New(slog.DiscardHandler), "test", startFunc)
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

		err := start(t.Context(), slog.New(slog.DiscardHandler), "test", startFunc)
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

		err := start(ctx, slog.New(slog.DiscardHandler), "test", startFunc)
		is.NotError(t, err)
	})
}
