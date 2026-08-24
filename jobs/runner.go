package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"maragu.dev/goqite"
	"maragu.dev/goqite/jobs"

	glueotel "maragu.dev/glue/otel"
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

// unwrapTracedMessage from the given bytes, also reporting whether they were an envelope at all.
// A job payload is arbitrary JSON which may well have a body of its own, so both envelope fields
// must be there: a non-empty body, and a trace context object, which is empty when nothing was
// propagated at enqueue time. Anything else is a payload in its own right.
func unwrapTracedMessage(m []byte) (tracedMessage, bool) {
	var tracedM tracedMessage
	if err := json.Unmarshal(m, &tracedM); err != nil {
		return tracedMessage{}, false
	}

	// A missing or null trace context leaves the map nil, an empty one doesn't
	if len(tracedM.Body) == 0 || tracedM.TraceContext == nil {
		return tracedMessage{}, false
	}

	return tracedM, true
}

// WithTracing wraps a [Func] with OpenTelemetry tracing and trace context propagation.
// It unwraps the [tracedMessage] envelope if there is one, and extracts trace context from it if
// the envelope carries any, so the span becomes a child of the span which created the job.
// Payloads which aren't wrapped in an envelope are passed through untouched.
// The wrapped function receives the raw payload bytes in both cases.
func WithTracing(operationName string, fn Func) Func {
	tracer := otel.Tracer("maragu.dev/glue/jobs")

	return func(ctx context.Context, m []byte) error {
		if tracedM, ok := unwrapTracedMessage(m); ok {
			m = tracedM.Body

			// The envelope has no trace context if there was nothing to propagate on enqueue,
			// either because there was no propagator configured, or no span to inherit from
			if len(tracedM.TraceContext) > 0 {
				propagator := otel.GetTextMapPropagator()
				ctx = propagator.Extract(ctx, propagation.MapCarrier(tracedM.TraceContext))
			}
		}

		ctx, span := tracer.Start(ctx, operationName,
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(glueotel.MainSpanAttributes()...),
		)
		defer span.End()

		// The runner recovers a panicking job and logs it, so this is the last chance to describe the
		// panic on the span. A panic value which is already an error is recorded as it is, so
		// exception.type keeps naming the type the job panicked with, and only anything else is wrapped.
		// The stack trace is the one being unwound, which is the only record of where the panic came from
		// once the span is all that is left of the job.
		//
		// The SDK records an exception of its own when a panic unwinds through [trace.Span.End], so a
		// panicking job can end up with two exception events. Recording here anyway, because that
		// behaviour belongs to whichever tracer provider the application configured and can be turned
		// off, and it sets neither the status nor the stack trace.
		defer func() {
			v := recover()
			if v == nil {
				return
			}

			err, ok := v.(error)
			if !ok {
				err = fmt.Errorf("panic: %v", v)
			}

			span.RecordError(err, trace.WithStackTrace(true))
			span.SetStatus(codes.Error, "job panicked")

			// Re-panic so the runner still handles this as the panic it is
			panic(v)
		}()

		if err := fn(ctx, m); err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, "job failed")
			return err
		}

		return nil
	}
}
