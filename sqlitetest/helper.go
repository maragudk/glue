package sqlitetest

import (
	"context"
	"crypto/rand"
	stdsql "database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"maragu.dev/glue/sql"
)

// NewHelper for testing, with optional options.
// Options:
// - [WithFixtures] to load fixtures after migration.
// - [WithMigrationFunc] to use a custom migration function instead of the built-in one.
func NewHelper(t *testing.T, opts ...HelperOption) *sql.Helper {
	t.Helper()

	var config helperConfig
	for _, opt := range opts {
		opt(&config)
	}

	databaseName := "test-" + strings.ToLower(rand.Text()) + ".db"

	t.Cleanup(func() {
		cleanup(t, databaseName)
	})

	h := sql.NewHelper(sql.NewHelperOptions{
		Log: slog.New(slog.NewTextHandler(&testWriter{t: t}, nil)),
		SQLite: sql.SQLiteOptions{
			Path: databaseName,
		},
	})
	if err := h.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}

	if config.migrationFunc == nil {
		config.migrationFunc = func(ctx context.Context, _ *stdsql.DB) error {
			return h.MigrateUp(ctx)
		}
	}

	if err := config.migrationFunc(t.Context(), h.DB.DB); err != nil {
		t.Fatal(err)
	}

	if len(config.fixtures) > 0 {
		loadFixtures(t, h, config.fixtures)
	}

	return h
}

func cleanup(t *testing.T, databaseName string) {
	t.Helper()

	files, err := filepath.Glob(databaseName + "*")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if err := os.Remove(file); err != nil {
			t.Fatal(err)
		}
	}
}

type testWriter struct {
	t *testing.T
}

func (t *testWriter) Write(p []byte) (n int, err error) {
	t.t.Log(string(p))
	return len(p), nil
}
