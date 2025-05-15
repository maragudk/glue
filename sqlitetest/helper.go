package sqlitetest

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	_ "github.com/mattn/go-sqlite3"
	"maragu.dev/env"

	"maragu.dev/glue/sql"
)

// NewHelper for testing.
func NewHelper(t *testing.T) *sql.Helper {
	t.Helper()

	_ = env.Load("../.env.test")

	cleanup(t)
	t.Cleanup(func() {
		cleanup(t)
	})

	h := sql.NewHelper(sql.NewHelperOptions{
		Log: slog.New(slog.NewTextHandler(&testWriter{t: t}, nil)),
		SQLite: sql.SQLiteOptions{
			Path: env.GetStringOrDefault("DATABASE_PATH", "test.db"),
		},
	})
	if err := h.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := h.MigrateUp(t.Context()); err != nil {
		t.Fatal(err)
	}

	return h
}

func cleanup(t *testing.T) {
	t.Helper()

	files, err := filepath.Glob("test.db*")
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
