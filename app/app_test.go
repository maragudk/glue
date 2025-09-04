package app

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
	"maragu.dev/is"
)

func TestStart(t *testing.T) {
	t.Run("should start and stop cleanly within timeout", func(t *testing.T) {
		called := false

		startFunc := func(ctx context.Context, log *slog.Logger, eg *errgroup.Group) error {
			called = true

			// Add a goroutine that will be stopped when context is done
			eg.Go(func() error {
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
	})

	t.Run("should return with error", func(t *testing.T) {
		expectedErr := errors.New("oh no")

		startFunc := func(ctx context.Context, log *slog.Logger, eg *errgroup.Group) error {
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

		startFunc := func(ctx context.Context, log *slog.Logger, eg *errgroup.Group) error {
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
}
