package jobs

import (
	"context"
	"database/sql"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"maragu.dev/goqite"
	"maragu.dev/goqite/jobs"
)

type Runner = jobs.Runner

type Func = jobs.Func

type NewRunnerOpts = jobs.NewRunnerOpts

// NewRunner just calls [jobs.NewRunner].
func NewRunner(opts NewRunnerOpts) *Runner {
	return jobs.NewRunner(NewRunnerOpts{
		Extend:       opts.Extend,
		Limit:        opts.Limit,
		Log:          opts.Log,
		PollInterval: opts.PollInterval,
		Queue:        opts.Queue,
	})
}

func Create(ctx context.Context, q *goqite.Queue, name string, m goqite.Message) error {
	_, err := jobs.Create(ctx, q, name, m)
	return err
}

func CreateTx(ctx context.Context, tx *sql.Tx, q *goqite.Queue, name string, m goqite.Message) error {
	_, err := jobs.CreateTx(ctx, tx, q, name, m)
	return err
}

type Message = goqite.Message

// WithTracing wraps a [Func] with OpenTelemetry tracing.
// It creates a span with the given operation name and automatically handles
// error recording and span status based on the function's return value.
func WithTracing(operationName string, fn Func) Func {
	tracer := otel.Tracer("maragu.dev/glue/jobs")

	return func(ctx context.Context, m []byte) error {
		ctx, span := tracer.Start(ctx, operationName,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attribute.Bool("main", true)),
		)
		defer span.End()

		if err := fn(ctx, m); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "job failed")
			return err
		}

		return nil
	}
}
