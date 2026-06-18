package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"maragu.dev/is"
)

func TestNewLogger(t *testing.T) {
	t.Run("adds trace_id and span_id when logging within an active span", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(newTraceHandler(slog.NewJSONHandler(&buf, nil)))

		ctx, span := newSpan(t)
		defer span.End()

		logger.InfoContext(ctx, "hello")

		entry := decode(t, buf.Bytes())
		is.Equal(t, span.SpanContext().TraceID().String(), entry["trace_id"].(string))
		is.Equal(t, span.SpanContext().SpanID().String(), entry["span_id"].(string))
	})

	t.Run("does not add trace_id or span_id without an active span", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(newTraceHandler(slog.NewJSONHandler(&buf, nil)))

		logger.InfoContext(t.Context(), "hello")

		entry := decode(t, buf.Bytes())
		_, hasTraceID := entry["trace_id"]
		_, hasSpanID := entry["span_id"]
		is.True(t, !hasTraceID)
		is.True(t, !hasSpanID)
	})

	t.Run("propagates trace context through WithAttrs", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(newTraceHandler(slog.NewJSONHandler(&buf, nil))).
			With("component", "test")

		ctx, span := newSpan(t)
		defer span.End()

		logger.InfoContext(ctx, "hello")

		entry := decode(t, buf.Bytes())
		is.Equal(t, "test", entry["component"].(string))
		is.Equal(t, span.SpanContext().TraceID().String(), entry["trace_id"].(string))
		is.Equal(t, span.SpanContext().SpanID().String(), entry["span_id"].(string))
	})

	t.Run("keeps trace_id and span_id at the top level under a group", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(newTraceHandler(slog.NewJSONHandler(&buf, nil))).
			WithGroup("g")

		ctx, span := newSpan(t)
		defer span.End()

		logger.InfoContext(ctx, "hello", "k", "v")

		entry := decode(t, buf.Bytes())
		is.Equal(t, span.SpanContext().TraceID().String(), entry["trace_id"].(string))
		is.Equal(t, span.SpanContext().SpanID().String(), entry["span_id"].(string))

		// The caller's own attribute nests under the group, but the trace fields stay at the top level.
		group := entry["g"].(map[string]any)
		is.Equal(t, "v", group["k"].(string))
	})

	t.Run("strips the time attribute when NoTime is set", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLogger(NewLoggerOptions{JSON: true, NoTime: true, W: &buf})

		logger.Info("hello")

		entry := decode(t, buf.Bytes())
		_, hasTime := entry["time"]
		is.True(t, !hasTime)
	})
}

// newSpan starts a recording span backed by a [tracetest.SpanRecorder] and returns the context
// carrying it together with the span.
func newSpan(t *testing.T) (context.Context, trace.Span) {
	t.Helper()

	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))

	previous := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(previous)
	})

	return tp.Tracer("test").Start(t.Context(), "test")
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	is.NotError(t, json.Unmarshal(b, &m))
	return m
}
