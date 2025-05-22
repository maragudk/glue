package testing

import (
	"testing"

	"maragu.dev/glue/postgrestest"
	"maragu.dev/glue/sql"
	"maragu.dev/glue/sqlitetest"
)

func Run(t *testing.T, name string, f func(t *testing.T, h *sql.Helper)) {
	t.Run(name, func(t *testing.T) {
		t.Run("sqlite", func(t *testing.T) {
			db := sqlitetest.NewHelper(t)
			f(t, db)
		})

		t.Run("postgresql", func(t *testing.T) {
			db := postgrestest.NewHelper(t)
			f(t, db)
		})
	})
}
