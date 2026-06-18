package log

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"maragu.dev/is"

	"maragu.dev/glue/oteltest"
)

func TestNewLogger(t *testing.T) {
	t.Run("adds trace_id and span_id at the top level when logging within an active span", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(&traceHandler{Handler: slog.NewJSONHandler(&buf, nil)})

		ctx, span := newSpan(t)
		defer span.End()

		logger.InfoContext(ctx, "hello")

		entry := decode(t, buf.Bytes())
		is.Equal(t, span.SpanContext().TraceID().String(), entry["trace_id"].(string))
		is.Equal(t, span.SpanContext().SpanID().String(), entry["span_id"].(string))
	})

	t.Run("does not add trace_id or span_id without an active span", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(&traceHandler{Handler: slog.NewJSONHandler(&buf, nil)})

		logger.InfoContext(t.Context(), "hello")

		entry := decode(t, buf.Bytes())
		_, hasTraceID := entry["trace_id"]
		_, hasSpanID := entry["span_id"]
		is.True(t, !hasTraceID)
		is.True(t, !hasSpanID)
	})

	t.Run("does not add trace_id or span_id when not logging with a context", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(&traceHandler{Handler: slog.NewJSONHandler(&buf, nil)})

		logger.Info("hello")

		entry := decode(t, buf.Bytes())
		_, hasTraceID := entry["trace_id"]
		_, hasSpanID := entry["span_id"]
		is.True(t, !hasTraceID)
		is.True(t, !hasSpanID)
	})

	t.Run("keeps trace correlation at the top level after WithAttrs", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(&traceHandler{Handler: slog.NewJSONHandler(&buf, nil)}).
			With("component", "test")

		ctx, span := newSpan(t)
		defer span.End()

		logger.InfoContext(ctx, "hello")

		entry := decode(t, buf.Bytes())
		is.Equal(t, "test", entry["component"].(string))
		is.Equal(t, span.SpanContext().TraceID().String(), entry["trace_id"].(string))
		is.Equal(t, span.SpanContext().SpanID().String(), entry["span_id"].(string))
	})

	t.Run("nests trace_id and span_id under a group, alongside the record's attributes", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(&traceHandler{Handler: slog.NewJSONHandler(&buf, nil)}).
			WithGroup("g")

		ctx, span := newSpan(t)
		defer span.End()

		logger.InfoContext(ctx, "hello", "k", "v")

		entry := decode(t, buf.Bytes())

		// The trace fields follow the record's grouping, so they nest under the group together with
		// the record's own attribute. Nothing trace-related is left at the top level.
		_, hasTopLevelTraceID := entry["trace_id"]
		is.True(t, !hasTopLevelTraceID)

		group := entry["g"].(map[string]any)
		is.Equal(t, span.SpanContext().TraceID().String(), group["trace_id"].(string))
		is.Equal(t, span.SpanContext().SpanID().String(), group["span_id"].(string))
		is.Equal(t, "v", group["k"].(string))
	})

	t.Run("keeps WithAttrs attributes top level and nests trace under a later group", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(&traceHandler{Handler: slog.NewJSONHandler(&buf, nil)}).
			With("component", "test").
			WithGroup("g")

		ctx, span := newSpan(t)
		defer span.End()

		logger.InfoContext(ctx, "hello", "k", "v")

		entry := decode(t, buf.Bytes())

		// The attribute added before the group stays at the top level.
		is.Equal(t, "test", entry["component"].(string))

		// The trace fields and the record attribute land inside the open group.
		group := entry["g"].(map[string]any)
		is.Equal(t, span.SpanContext().TraceID().String(), group["trace_id"].(string))
		is.Equal(t, span.SpanContext().SpanID().String(), group["span_id"].(string))
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

	t.Run("keeps the time attribute by default", func(t *testing.T) {
		var buf bytes.Buffer
		logger := NewLogger(NewLoggerOptions{JSON: true, W: &buf})

		logger.Info("hello")

		entry := decode(t, buf.Bytes())
		_, hasTime := entry["time"]
		is.True(t, hasTime)
	})
}

// newSpan starts a recording span backed by [oteltest.NewSpanRecorder] and returns the context
// carrying it together with the span.
func newSpan(t *testing.T) (context.Context, trace.Span) {
	t.Helper()

	oteltest.NewSpanRecorder(t)

	return otel.Tracer("test").Start(t.Context(), "test")
}

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	is.NotError(t, json.Unmarshal(b, &m))
	return m
}
