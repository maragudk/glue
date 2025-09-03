package jobs

import (
	"context"
	"database/sql"
	"encoding/json"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
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

func Create(ctx context.Context, q *goqite.Queue, name string, m Message) error {
	m = wrapWithTrace(ctx, m)
	_, err := jobs.Create(ctx, q, name, m)
	return err
}

func CreateTx(ctx context.Context, tx *sql.Tx, q *goqite.Queue, name string, m Message) error {
	m = wrapWithTrace(ctx, m)
	_, err := jobs.CreateTx(ctx, tx, q, name, m)
	return err
}

func wrapWithTrace(ctx context.Context, m Message) Message {
	// Extract current trace context
	propagator := otel.GetTextMapPropagator()
	carrier := make(map[string]string)
	propagator.Inject(ctx, propagation.MapCarrier(carrier))

	// Wrap payload with trace context
	tracedM := tracedMessage{
		Body:         json.RawMessage(m.Body),
		TraceContext: carrier,
	}

	body, err := json.Marshal(tracedM)
	if err != nil {
		panic(err)
	}

	// Create message with traced payload, copying other options
	return Message{
		ID:       m.ID,
		Body:     body,
		Delay:    m.Delay,
		Priority: m.Priority,
	}
}

type Message = goqite.Message

// tracedMessage wraps any job payload with OpenTelemetry trace context
// for propagating traces from HTTP requests to background jobs.
type tracedMessage struct {
	Body         json.RawMessage
	TraceContext map[string]string
}

// WithTracing wraps a [Func] with OpenTelemetry tracing and trace context propagation.
// It extracts trace context from tracedMessage if present and creates a span with proper
// parent-child relationships. The wrapped function receives the raw payload bytes.
func WithTracing(operationName string, fn Func) Func {
	tracer := otel.Tracer("maragu.dev/glue/jobs")

	return func(ctx context.Context, m []byte) error {
		// Try to unmarshal as tracedMessage first to extract trace context
		var tracedM tracedMessage
		if err := json.Unmarshal(m, &tracedM); err == nil && len(tracedM.Body) > 0 && len(tracedM.TraceContext) > 0 {
			// Extract trace context
			propagator := otel.GetTextMapPropagator()
			ctx = propagator.Extract(ctx, propagation.MapCarrier(tracedM.TraceContext))

			// Use the wrapped payload
			m = tracedM.Body
		}

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
