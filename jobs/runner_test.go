package jobs_test

import (
	"context"
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"maragu.dev/is"

	"maragu.dev/glue/jobs"
)

type TestPayload struct {
	Message string `json:"message"`
	Value   int    `json:"value"`
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

		type testTracedMessage struct {
			Body         json.RawMessage
			TraceContext map[string]string
		}

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
}
