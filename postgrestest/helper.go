package postgrestest

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"maragu.dev/env"

	"maragu.dev/glue/sql"
)

// NewHelper for testing.
func NewHelper(t *testing.T) *sql.Helper {
	t.Helper()

	if testing.Short() {
		t.SkipNow()
	}

	_ = env.Load("../.env.test")

	migrateTemplate1(t)

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

// migrateTemplate1 uses PostgreSQL advisory lock to ensure template1 migration happens only once
// across all parallel test executions (even across different packages/processes).
// Go runs tests for different packages in parallel by default.
func migrateTemplate1(t *testing.T) {
	t.Helper()

	// First, acquire an advisory lock using the postgres database
	adminH, adminClose := connect(t, "postgres")
	defer adminClose(t)

	// Use a well-known advisory lock ID for template1 migration.
	// This ensures only one process/package can migrate at a time.
	const key = 1618033

	// Use a blocking lock. This will wait until lock is available.
	// The first one to get the lock does the actual migration.
	// The rest just try to migrate, which is effectively a noop because migrations are idempotent.
	if err := adminH.Exec(t.Context(), `select pg_advisory_lock($1)`, key); err != nil {
		t.Fatal(err)
	}

	defer func() {
		if err := adminH.Exec(context.WithoutCancel(t.Context()), `select pg_advisory_unlock($1)`, key); err != nil {
			t.Fatal(err)
		}
	}()

	h, close := connect(t, "template1")
	defer close(t)

	for err := h.Ping(t.Context()); err != nil; {
		time.Sleep(100 * time.Millisecond)
	}

	if err := h.MigrateUp(t.Context()); err != nil {
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
