package sql

import (
	"context"
	"database/sql"
	"log/slog"
	"net/url"
	"time"

	"github.com/jmoiron/sqlx"
	"maragu.dev/errors"
	"maragu.dev/goqite"
)

type Helper struct {
	DB                    *sqlx.DB
	JobsQ                 *goqite.Queue
	connectionMaxIdleTime time.Duration
	connectionMaxLifetime time.Duration
	jobQueueTimeout       time.Duration
	log                   *slog.Logger
	maxIdleConnections    int
	maxOpenConnections    int
	path                  string
	url                   string
}

type NewHelperOptions struct {
	JobQueue JobQueueOptions
	Log      *slog.Logger
	Postgres PostgresOptions
	SQLite   SQLiteOptions
}

type PostgresOptions struct {
	ConnectionMaxIdleTime time.Duration
	ConnectionMaxLifetime time.Duration
	MaxIdleConnections    int
	MaxOpenConnections    int
	URL                   string
}

type SQLiteOptions struct {
	Path string
}

type JobQueueOptions struct {
	Timeout time.Duration
}

// NewHelper with the given options.
// If no logger is provided, logs are discarded.
func NewHelper(opts NewHelperOptions) *Helper {
	if opts.Log == nil {
		opts.Log = slog.New(slog.DiscardHandler)
	}

	return &Helper{
		connectionMaxIdleTime: opts.Postgres.ConnectionMaxIdleTime,
		connectionMaxLifetime: opts.Postgres.ConnectionMaxLifetime,
		jobQueueTimeout:       opts.JobQueue.Timeout,
		log:                   opts.Log,
		maxIdleConnections:    opts.Postgres.MaxIdleConnections,
		maxOpenConnections:    opts.Postgres.MaxOpenConnections,
		path:                  opts.SQLite.Path,
		url:                   opts.Postgres.URL,
	}
}

func (h *Helper) Connect(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var sqlFlavor goqite.SQLFlavor

	switch {
	case h.path != "":
		// - Set WAL mode (not strictly necessary each time because it's persisted in the database, but good for first run)
		// - Set busy timeout, so concurrent writers wait on each other instead of erroring immediately
		// - Enable foreign key checks
		// - Enable immediate transaction locking, so transactions that are upgraded to write transactions can't fail with a busy error
		path := h.path + "?_journal=WAL&_timeout=5000&_fk=true&_txlock=immediate"

		h.log.Info("Starting database", "path", path)

		var err error
		h.DB, err = sqlx.ConnectContext(ctx, "sqlite3", path)
		if err != nil {
			return err
		}

		sqlFlavor = goqite.SQLFlavorSQLite

	case h.url != "":
		scrubbedUrl := scrubURL(h.url)

		h.log.Info("Connecting to database", "url", scrubbedUrl)

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

		sqlFlavor = goqite.SQLFlavorPostgreSQL

	default:
		panic("neither postgres url nor sqlite path given")
	}

	h.JobsQ = goqite.New(goqite.NewOpts{
		DB:        h.DB.DB,
		Name:      "jobs",
		SQLFlavor: sqlFlavor,
		Timeout:   h.jobQueueTimeout,
	})

	return nil
}

func scrubURL(connectionURL string) string {
	u, err := url.Parse(connectionURL)
	if err != nil {
		panic("error parsing connection url")
	}
	if _, ok := u.User.Password(); ok {
		u.User = url.UserPassword(u.User.Username(), "xxx")
	}
	return u.String()
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
