package sql

import (
	"context"
	"database/sql"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/jmoiron/sqlx"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.34.0"
	"go.opentelemetry.io/otel/trace"
	"maragu.dev/errors"
	"maragu.dev/goqite"
)

type Helper struct {
	DB                    *sqlx.DB
	JobsQ, JobsQCPU       *goqite.Queue
	attributes            []attribute.KeyValue
	connectionMaxIdleTime time.Duration
	connectionMaxLifetime time.Duration
	jobQueueTimeout       time.Duration
	log                   *slog.Logger
	maxIdleConnections    int
	maxOpenConnections    int
	path                  string
	tracer                trace.Tracer
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
// For documentation on OTel spans and attributes, see https://opentelemetry.io/docs/specs/semconv/database/database-spans/
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
		tracer:                otel.Tracer("maragu.dev/glue/sql"),
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
		h.attributes = []attribute.KeyValue{
			semconv.DBSystemNameSQLite,
		}

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
		h.attributes = []attribute.KeyValue{
			semconv.DBSystemNamePostgreSQL,
		}

	default:
		panic("neither postgres url nor sqlite path given")
	}

	// Regular jobs
	h.JobsQ = goqite.New(goqite.NewOpts{
		DB:        h.DB.DB,
		Name:      "jobs",
		SQLFlavor: sqlFlavor,
		Timeout:   h.jobQueueTimeout,
	})

	// CPU bound jobs
	h.JobsQCPU = goqite.New(goqite.NewOpts{
		DB:        h.DB.DB,
		Name:      "jobs-cpu",
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
func (h *Helper) InTx(ctx context.Context, cb func(ctx context.Context, tx *Tx) error) (err error) {
	ctx, span := h.tracer.Start(ctx, "tx",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(h.attributes...),
	)
	defer span.End()

	tx, err := h.DB.BeginTxx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		err = errors.Wrap(err, "error beginning transaction")
		span.RecordError(err)
		span.SetStatus(codes.Error, "tx begin failed")
		return err
	}

	defer func() {
		if rec := recover(); rec != nil {
			err = rollback(tx, errors.Newf("panic: %v", rec))
			span.RecordError(err)
			span.SetStatus(codes.Error, "tx callback failed")
		}
	}()

	if err := cb(ctx, &Tx{Tx: tx, queryTracerStart: h.queryTracerStart}); err != nil {
		err = rollback(tx, err)
		span.RecordError(err)
		span.SetStatus(codes.Error, "tx callback failed")
		return err
	}
	if err := tx.Commit(); err != nil {
		err = errors.Wrap(err, "error committing transaction")
		span.RecordError(err)
		span.SetStatus(codes.Error, "tx commit failed")
		return err
	}

	span.SetStatus(codes.Ok, "")

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
	return h.InTx(ctx, func(ctx context.Context, tx *Tx) error {
		return tx.Exec(ctx, `select 1`)
	})
}

func (h *Helper) Select(ctx context.Context, dest any, query string, args ...any) error {
	ctx, span := h.queryTracerStart(ctx, query)
	defer span.End()

	if err := h.DB.SelectContext(ctx, dest, query, args...); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "query failed")
		return err
	}

	return nil
}

func (h *Helper) Get(ctx context.Context, dest any, query string, args ...any) error {
	ctx, span := h.queryTracerStart(ctx, query)
	defer span.End()

	if err := h.DB.GetContext(ctx, dest, query, args...); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "query failed")
		return err
	}

	return nil
}

func (h *Helper) Exec(ctx context.Context, query string, args ...any) error {
	ctx, span := h.queryTracerStart(ctx, query)
	defer span.End()

	if _, err := h.DB.ExecContext(ctx, query, args...); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "query failed")
		return err
	}

	return nil
}

type Tx struct {
	Tx               *sqlx.Tx
	queryTracerStart func(context.Context, string, ...trace.SpanStartOption) (context.Context, trace.Span)
}

func (t *Tx) Select(ctx context.Context, dest any, query string, args ...any) error {
	ctx, span := t.queryTracerStart(ctx, query)
	defer span.End()

	if err := t.Tx.SelectContext(ctx, dest, query, args...); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "query failed")
		return err
	}

	return nil
}

func (t *Tx) Get(ctx context.Context, dest any, query string, args ...any) error {
	ctx, span := t.queryTracerStart(ctx, query)
	defer span.End()

	if err := t.Tx.GetContext(ctx, dest, query, args...); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "query failed")
		return err
	}

	return nil
}

func (t *Tx) Exec(ctx context.Context, query string, args ...any) error {
	ctx, span := t.queryTracerStart(ctx, query)
	defer span.End()

	if _, err := t.Tx.ExecContext(ctx, query, args...); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "query failed")
		return err
	}

	return nil
}

var ErrNoRows = sql.ErrNoRows

func (h *Helper) queryTracerStart(ctx context.Context, query string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	allOpts := []trace.SpanStartOption{
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(h.attributes...),
		trace.WithAttributes(
			attribute.String("db.query.text", normalizeQuery(query)),
		),
	}
	allOpts = append(allOpts, opts...)
	return h.tracer.Start(ctx, "query", allOpts...)
}

// normalizeQuery by removing excessive whitespace and truncating long queries.
func normalizeQuery(query string) string {
	normalized := strings.Join(strings.Fields(query), " ")

	const maxLength = 1000
	if len(normalized) > maxLength {
		return normalized[:maxLength] + "…"
	}

	return normalized
}
