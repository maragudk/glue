package postgrestest_test

import (
	"testing"

	"maragu.dev/is"

	"maragu.dev/gloo/postgrestest"
)

func TestHelper_Ping(t *testing.T) {
	t.Run("can ping", func(t *testing.T) {
		db := postgrestest.NewHelper(t)

		err := db.Ping(t.Context())
		is.NotError(t, err)
	})
}

func TestHelper_Migrate(t *testing.T) {
	t.Run("can migrate down and back up", func(t *testing.T) {
		h := postgrestest.NewHelper(t)

		err := h.MigrateDown(t.Context())
		is.NotError(t, err)

		err = h.MigrateUp(t.Context())
		is.NotError(t, err)

		var version string
		err = h.Get(t.Context(), &version, `select version from migrations`)
		is.NotError(t, err)
		is.True(t, len(version) > 0)

		var id string
		err = h.Get(t.Context(), &id, `select id from gloo`)
		is.NotError(t, err)
		is.Equal(t, id, "1")
	})
}
