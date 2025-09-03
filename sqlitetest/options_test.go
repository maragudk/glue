package sqlitetest_test

import (
	"context"
	"database/sql"
	"testing"

	"maragu.dev/is"

	"maragu.dev/glue/sqlitetest"
)

func TestHelper_WithFixtures(t *testing.T) {
	t.Run("should load single fixture", func(t *testing.T) {
		h := sqlitetest.NewHelper(t, sqlitetest.WithFixtures("users"))

		var count int
		err := h.Get(t.Context(), &count, `select count(*) from users`)
		is.NotError(t, err)
		is.Equal(t, count, 2)

		var name string
		err = h.Get(t.Context(), &name, `select name from users where id = 'user1'`)
		is.NotError(t, err)
		is.Equal(t, name, "Emma")
	})

	t.Run("should load multiple fixtures", func(t *testing.T) {
		h := sqlitetest.NewHelper(t, sqlitetest.WithFixtures("users", "products"))

		var userCount int
		err := h.Get(t.Context(), &userCount, `select count(*) from users`)
		is.NotError(t, err)
		is.Equal(t, userCount, 2)

		var productCount int
		err = h.Get(t.Context(), &productCount, `select count(*) from products`)
		is.NotError(t, err)
		is.Equal(t, productCount, 2)

		var price float64
		err = h.Get(t.Context(), &price, `select price from products where id = 'prod1'`)
		is.NotError(t, err)
		is.Equal(t, price, 10.5)
	})

	t.Run("should work with multiple WithFixture calls", func(t *testing.T) {
		h := sqlitetest.NewHelper(t,
			sqlitetest.WithFixtures("users"),
			sqlitetest.WithFixtures("products"))

		var userCount int
		err := h.Get(t.Context(), &userCount, `select count(*) from users`)
		is.NotError(t, err)
		is.Equal(t, userCount, 2)

		var productCount int
		err = h.Get(t.Context(), &productCount, `select count(*) from products`)
		is.NotError(t, err)
		is.Equal(t, productCount, 2)
	})

	t.Run("should work without fixtures", func(t *testing.T) {
		h := sqlitetest.NewHelper(t)

		var version string
		err := h.Get(t.Context(), &version, `select version from glue`)
		is.NotError(t, err)
		is.Equal(t, version, "1")
	})
}

func TestHelper_WithMigrationFunc(t *testing.T) {
	t.Run("should run custom migration function instead of built-in migrations", func(t *testing.T) {
		migrationRan := false
		customMigration := func(ctx context.Context, db *sql.DB) error {
			migrationRan = true
			return nil
		}

		h := sqlitetest.NewHelper(t, sqlitetest.WithMigrationFunc(customMigration))

		is.True(t, migrationRan)

		// Should not have the default glue table since we used custom migration
		var count int
		err := h.Get(t.Context(), &count, `select count(*) from sqlite_master where type='table' and name='glue'`)
		is.NotError(t, err)
		is.Equal(t, count, 0)

		// Verify the helper is functional even with custom migration
		var result string
		err = h.Get(t.Context(), &result, `select 'test' as value`)
		is.NotError(t, err)
		is.Equal(t, result, "test")
	})
}
