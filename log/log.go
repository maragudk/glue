package log

import (
	"context"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

type NewLoggerOptions struct {
	JSON   bool
	Level  slog.Level
	NoTime bool
}

func NewLogger(opts NewLoggerOptions) *slog.Logger {
	replaceAttr := func(groups []string, a slog.Attr) slog.Attr {
		switch a.Key {
		case "time":
			if opts.NoTime {
				return slog.Attr{}
			}
			return a
		default:
			return a
		}
	}

	var handler slog.Handler
	if opts.JSON {
		handler = slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level:       opts.Level,
			ReplaceAttr: replaceAttr,
		})
	} else {
		handler = slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
			Level:       opts.Level,
			ReplaceAttr: replaceAttr,
		})
	}

	return slog.New(&traceHandler{Handler: handler})
}

// traceHandler wraps a [slog.Handler] and adds trace_id and span_id attributes from the
// active OpenTelemetry span context, so logs can be correlated with traces.
type traceHandler struct {
	slog.Handler
}

// Handle the record, adding trace_id and span_id attributes if the context carries a valid span context.
func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	if sc := trace.SpanContextFromContext(ctx); sc.IsValid() {
		r.AddAttrs(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return h.Handler.Handle(ctx, r)
}

// WithAttrs returns a new [traceHandler] wrapping the underlying handler with the given attributes.
func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup returns a new [traceHandler] wrapping the underlying handler with the given group.
func (h *traceHandler) WithGroup(name string) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithGroup(name)}
}

func StringToLevel(level string) slog.Level {
	level = strings.ToLower(level)

	switch level {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		panic("invalid log level " + level)
	}
}
