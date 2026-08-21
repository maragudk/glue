package jobs_test

import (
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"maragu.dev/is"

	"maragu.dev/glue/jobs"
	"maragu.dev/glue/oteltest"
)

type TestPayload struct {
	Message string `json:"message"`
	Value   int    `json:"value"`
}

// testTracedMessage mirrors the envelope which the jobs package wraps payloads in on enqueue.
type testTracedMessage struct {
	Body         json.RawMessage
	TraceContext map[string]string
}

func TestWithTracing(t *testing.T) {
	// Set up a tracer provider that creates valid spans
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)

	// Set up propagator for trace context
	otel.SetTextMapPropagator(propagation.TraceContext{})

	t.Run("should handle tracedMessage with context propagation", func(t *testing.T) {
		// Create a real parent span to simulate HTTP request context
		tracer := otel.Tracer("test")
		parentCtx, parentSpan := tracer.Start(t.Context(), "test-http-request")
		defer parentSpan.End()

		// Extract trace context like wrapWithTrace would do
		propagator := otel.GetTextMapPropagator()
		carrier := make(map[string]string)
		propagator.Inject(parentCtx, propagation.MapCarrier(carrier))

		// Create a tracedMessage as would be created by Create
		payload := TestPayload{
			Message: "test message",
			Value:   42,
		}

		body, err := json.Marshal(payload)
		is.NotError(t, err)

		tracedPayload := testTracedMessage{
			Body:         json.RawMessage(body),
			TraceContext: carrier,
		}

		tracedBody, err := json.Marshal(tracedPayload)
		is.NotError(t, err)

		// Create a traced function handler
		var receivedCtx context.Context
		var receivedM []byte
		handler := jobs.WithTracing("test-operation", func(ctx context.Context, m []byte) error {
			receivedCtx = ctx
			receivedM = m
			return nil
		})

		// Execute the handler
		err = handler(t.Context(), tracedBody)
		is.NotError(t, err)

		// Verify payload was extracted correctly (should be the original payload bytes)
		var unmarshaled TestPayload
		err = json.Unmarshal(receivedM, &unmarshaled)
		is.NotError(t, err)
		is.Equal(t, "test message", unmarshaled.Message)
		is.Equal(t, 42, unmarshaled.Value)

		// Verify context has a span that derives from the parent span
		span := trace.SpanFromContext(receivedCtx)
		is.True(t, span.SpanContext().IsValid())
		is.Equal(t, parentSpan.SpanContext().TraceID(), span.SpanContext().TraceID())
	})

	t.Run("should handle direct payload without trace context", func(t *testing.T) {
		// Create direct payload (not wrapped in tracedMessage)
		payload := TestPayload{
			Message: "direct message",
			Value:   123,
		}

		body, err := json.Marshal(payload)
		is.NotError(t, err)

		// Create a traced function handler
		var receivedCtx context.Context
		var receivedM []byte
		handler := jobs.WithTracing("test-operation", func(ctx context.Context, m []byte) error {
			receivedCtx = ctx
			receivedM = m
			return nil
		})

		// Execute the handler
		err = handler(t.Context(), body)
		is.NotError(t, err)

		// Verify payload was passed through correctly
		var unmarshaled TestPayload
		err = json.Unmarshal(receivedM, &unmarshaled)
		is.NotError(t, err)
		is.Equal(t, "direct message", unmarshaled.Message)
		is.Equal(t, 123, unmarshaled.Value)

		// Verify context has a span (indicating tracing is active)
		span := trace.SpanFromContext(receivedCtx)
		is.True(t, span.SpanContext().IsValid())
	})

	t.Run("should handle tracedMessage with an empty trace context", func(t *testing.T) {
		// This is the envelope Create produces when no propagator is configured: the noop
		// propagator injects nothing, so the carrier is marshaled as an empty object.
		payload := TestPayload{
			Message: "the propagator was on holiday",
			Value:   1234,
		}

		body, err := json.Marshal(payload)
		is.NotError(t, err)

		tracedBody, err := json.Marshal(testTracedMessage{
			Body:         json.RawMessage(body),
			TraceContext: map[string]string{},
		})
		is.NotError(t, err)

		var receivedCtx context.Context
		var receivedM []byte
		handler := jobs.WithTracing("test-operation", func(ctx context.Context, m []byte) error {
			receivedCtx = ctx
			receivedM = m
			return nil
		})

		err = handler(t.Context(), tracedBody)
		is.NotError(t, err)

		// Verify the handler got the payload itself and not the envelope around it
		is.Equal(t, string(body), string(receivedM))

		var unmarshaled TestPayload
		err = json.Unmarshal(receivedM, &unmarshaled)
		is.NotError(t, err)
		is.Equal(t, "the propagator was on holiday", unmarshaled.Message)
		is.Equal(t, 1234, unmarshaled.Value)

		// Verify the job is still traced, even with no parent span to inherit from
		span := trace.SpanFromContext(receivedCtx)
		is.True(t, span.SpanContext().IsValid())
	})

	t.Run("should handle payload with a body field of its own", func(t *testing.T) {
		// A payload is free to have a body of its own, which mustn't be mistaken for an envelope
		type emailPayload struct {
			Body    string
			Subject string
		}

		payload := emailPayload{
			Body:    "Per my last email, please see my last email.",
			Subject: "Re: Re: Re: quick question",
		}

		body, err := json.Marshal(payload)
		is.NotError(t, err)

		var receivedM []byte
		handler := jobs.WithTracing("test-operation", func(ctx context.Context, m []byte) error {
			receivedM = m
			return nil
		})

		err = handler(t.Context(), body)
		is.NotError(t, err)

		is.Equal(t, string(body), string(receivedM))

		var unmarshaled emailPayload
		err = json.Unmarshal(receivedM, &unmarshaled)
		is.NotError(t, err)
		is.Equal(t, "Per my last email, please see my last email.", unmarshaled.Body)
		is.Equal(t, "Re: Re: Re: quick question", unmarshaled.Subject)
	})

	t.Run("should set the main span attributes", func(t *testing.T) {
		sr := oteltest.NewSpanRecorder(t)

		// The tracer is taken from the tracer provider when the handler is created, so create it after the recorder
		handler := jobs.WithTracing("test-operation", func(ctx context.Context, m []byte) error {
			return nil
		})

		err := handler(t.Context(), []byte(`{"parrot":"resting"}`))
		is.NotError(t, err)

		spans := sr.Ended()
		is.Equal(t, 1, len(spans))
		is.True(t, oteltest.HasAttribute(spans[0].Attributes(), attribute.Bool("main", true)), "expected main attribute")
		is.True(t, oteltest.HasAttributeKey(spans[0].Attributes(), "uptime_sec"), "expected uptime_sec attribute")
		is.True(t, oteltest.HasAttributeKey(spans[0].Attributes(), "uptime_sec_log_10"), "expected uptime_sec_log_10 attribute")
	})
}
