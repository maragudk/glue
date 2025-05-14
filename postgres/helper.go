package postgres

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/jmoiron/sqlx"
	"maragu.dev/errors"
)

type Helper struct {
	DB                    *sqlx.DB
	connectionMaxIdleTime time.Duration
	connectionMaxLifetime time.Duration
	log                   *slog.Logger
	maxIdleConnections    int
	maxOpenConnections    int
	url                   string
}

type NewHelperOptions struct {
	ConnectionMaxIdleTime time.Duration
	ConnectionMaxLifetime time.Duration
	Log                   *slog.Logger
	MaxIdleConnections    int
	MaxOpenConnections    int
	URL                   string
}

// NewHelper with the given options.
// If no logger is provided, logs are discarded.
func NewHelper(opts NewHelperOptions) *Helper {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}

	return &Helper{
		connectionMaxIdleTime: opts.ConnectionMaxIdleTime,
		connectionMaxLifetime: opts.ConnectionMaxLifetime,
		log:                   opts.Log,
		maxIdleConnections:    opts.MaxIdleConnections,
		maxOpenConnections:    opts.MaxOpenConnections,
		url:                   opts.URL,
	}
}

func (h *Helper) Connect(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	h.log.Info("Connecting to database", "url", h.url)

	var err error
	h.DB, err = sqlx.ConnectContext(ctx, "pgx", h.url)
	if err != nil {
		return err
	}

	h.log.Debug("Setting connection pool options",
		"max open connections", h.maxOpenConnections,
		"max idle connections", h.maxIdleConnections,
		"connection max lifetime", h.connectionMaxLifetime,
		"connection max idle time", h.connectionMaxIdleTime)
	h.DB.SetMaxOpenConns(h.maxOpenConnections)
	h.DB.SetMaxIdleConns(h.maxIdleConnections)
	h.DB.SetConnMaxLifetime(h.connectionMaxLifetime)
	h.DB.SetConnMaxIdleTime(h.connectionMaxIdleTime)

	return nil
}

// InTransaction runs callback in a transaction, and makes sure to handle rollbacks, commits etc.
func (h *Helper) InTransaction(ctx context.Context, cb func(tx *Tx) error) (err error) {
	tx, err := h.DB.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return errors.Wrap(err, "error beginning transaction")
	}
	defer func() {
		if rec := recover(); rec != nil {
			err = rollback(tx, errors.Newf("panic: %v", rec))
		}
	}()
	if err := cb(&Tx{Tx: tx}); err != nil {
		return rollback(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return errors.Wrap(err, "error committing transaction")
	}

	return nil
}

// rollback a transaction, handling both the original error and any transaction rollback errors.
func rollback(tx *sqlx.Tx, err error) error {
	if txErr := tx.Rollback(); txErr != nil {
		return errors.Wrap(err, "error rolling back transaction after error (transaction error: %v), original error", txErr)
	}
	return err
}

func (h *Helper) Ping(ctx context.Context) error {
	return h.InTransaction(ctx, func(tx *Tx) error {
		return tx.Exec(ctx, `select 1`)
	})
}

func (h *Helper) Select(ctx context.Context, dest any, query string, args ...any) error {
	return h.DB.SelectContext(ctx, dest, query, args...)
}

func (h *Helper) Get(ctx context.Context, dest any, query string, args ...any) error {
	return h.DB.GetContext(ctx, dest, query, args...)
}

func (h *Helper) Exec(ctx context.Context, query string, args ...any) error {
	_, err := h.DB.ExecContext(ctx, query, args...)
	return err
}

type Tx struct {
	Tx *sqlx.Tx
}

func (t *Tx) Select(ctx context.Context, dest any, query string, args ...any) error {
	return t.Tx.SelectContext(ctx, dest, query, args...)
}

func (t *Tx) Get(ctx context.Context, dest any, query string, args ...any) error {
	return t.Tx.GetContext(ctx, dest, query, args...)
}

func (t *Tx) Exec(ctx context.Context, query string, args ...any) error {
	_, err := t.Tx.ExecContext(ctx, query, args...)
	return err
}

var ErrNoRows = sql.ErrNoRows
