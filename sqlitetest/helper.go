package sqlitetest

import (
	"crypto/rand"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"

	"maragu.dev/glue/sql"
)

// NewHelper for testing.
func NewHelper(t *testing.T) *sql.Helper {
	t.Helper()

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

	if err := h.MigrateUp(t.Context()); err != nil {
		t.Fatal(err)
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
