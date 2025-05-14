package postgres_test

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
