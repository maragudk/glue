package sqlitetest_test

import (
	"testing"

	"maragu.dev/is"

	"maragu.dev/glue/sqlitetest"
)

func TestHelper_Ping(t *testing.T) {
	t.Run("can ping", func(t *testing.T) {
		db := sqlitetest.NewHelper(t)

		err := db.Ping(t.Context())
		is.NotError(t, err)
	})
}

func TestHelper_Migrate(t *testing.T) {
	t.Run("can migrate down and back up", func(t *testing.T) {
		h := sqlitetest.NewHelper(t)

		err := h.MigrateDown(t.Context())
		is.NotError(t, err)

		err = h.MigrateUp(t.Context())
		is.NotError(t, err)

		var version string
		err = h.Get(t.Context(), &version, `select version from migrations`)
		is.NotError(t, err)
		is.True(t, len(version) > 0)

		err = h.Get(t.Context(), &version, `select version from glue`)
		is.NotError(t, err)
		is.Equal(t, version, "1")
	})
}
