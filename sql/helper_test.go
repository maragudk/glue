package sql_test

import (
	"context"
	"testing"

	"maragu.dev/is"

	"maragu.dev/glue/sql"
	internaltesting "maragu.dev/glue/sql/internal/testing"
)

func TestHelper_Connect(t *testing.T) {
	internaltesting.Run(t, "has a jobs queue", func(t *testing.T, h *sql.Helper) {
		is.NotNil(t, h.JobsQ)
	})
}

func TestHelper_In(t *testing.T) {
	internaltesting.Run(t, "should expand a slice argument into placeholders the database understands", func(t *testing.T, h *sql.Helper) {
		query, args, err := h.In(`select count(*) from (select 1 as day union select 2 union select 3) as festival where day in (?)`, []int{1, 3})
		is.NotError(t, err)

		var count int
		err = h.Get(t.Context(), &count, query, args...)
		is.NotError(t, err)
		is.Equal(t, 2, count)
	})

	internaltesting.Run(t, "should error on an empty slice argument", func(t *testing.T, h *sql.Helper) {
		_, _, err := h.In(`select 1 where 1 in (?)`, []int{})
		is.True(t, err != nil)
	})
}

func TestTx_In(t *testing.T) {
	internaltesting.Run(t, "should expand a slice argument into placeholders the database understands", func(t *testing.T, h *sql.Helper) {
		err := h.InTx(t.Context(), func(ctx context.Context, tx *sql.Tx) error {
			query, args, err := tx.In(`select count(*) from (select 1 as day union select 2 union select 3) as festival where day in (?)`, []int{1, 3})
			is.NotError(t, err)

			var count int
			err = tx.Get(ctx, &count, query, args...)
			is.NotError(t, err)
			is.Equal(t, 2, count)
			return nil
		})
		is.NotError(t, err)
	})
}
