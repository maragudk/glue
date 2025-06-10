package postgrestest

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"maragu.dev/env"

	"maragu.dev/glue/sql"
)

var once sync.Once

// NewHelper for testing.
func NewHelper(t *testing.T) *sql.Helper {
	t.Helper()

	if testing.Short() {
		t.SkipNow()
	}

	_ = env.Load("../.env.test")

	once.Do(func() {
		migrateTemplate1(t)
	})

	adminH, adminClose := connect(t, "postgres")

	name := createName(t)
	if err := adminH.Exec(t.Context(), `create database `+name); err != nil {
		t.Fatal(err)
	}
	h, close := connect(t, name)

	t.Cleanup(func() {
		close(t)
		if err := adminH.Exec(context.WithoutCancel(t.Context()), `drop database if exists `+name); err != nil {
			t.Fatal(err)
		}
		adminClose(t)
	})

	return h
}

func migrateTemplate1(t *testing.T) {
	t.Helper()

	h, close := connect(t, "template1")
	defer close(t)

	for err := h.Ping(t.Context()); err != nil; {
		time.Sleep(100 * time.Millisecond)
	}

	if err := h.MigrateUp(t.Context()); err != nil {
		t.Fatal(err)
	}

	if err := h.DB.Close(); err != nil {
		t.Fatal(err)
	}
}

func connect(t *testing.T, name string) (*sql.Helper, func(t *testing.T)) {
	t.Helper()

	h := sql.NewHelper(sql.NewHelperOptions{
		Log: slog.New(slog.NewTextHandler(&testWriter{t: t}, nil)),
		Postgres: sql.PostgresOptions{
			MaxIdleConnections: 10,
			MaxOpenConnections: 10,
			URL:                env.GetStringOrDefault("DATABASE_URL", "postgres://test:test@localhost:5433/"+name),
		},
	})
	if err := h.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	return h, func(t *testing.T) {
		if err := h.DB.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func createName(t *testing.T) string {
	t.Helper()

	const letters = "abcdefghijklmnopqrstuvwxyz"
	var b strings.Builder
	for range 16 {
		i := rand.IntN(len(letters))
		b.WriteByte(letters[i])
	}

	return b.String()
}

type testWriter struct {
	t *testing.T
}

func (t *testWriter) Write(p []byte) (n int, err error) {
	t.t.Log(string(p))
	return len(p), nil
}
