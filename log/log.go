package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

type NewLoggerOptions struct {
	JSON   bool
	Level  slog.Level
	NoTime bool

	// W is where logs are written. It defaults to [os.Stderr] when nil.
	W io.Writer
}

func NewLogger(opts NewLoggerOptions) *slog.Logger {
	if opts.W == nil {
		opts.W = os.Stderr
	}

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
		handler = slog.NewJSONHandler(opts.W, &slog.HandlerOptions{
			Level:       opts.Level,
			ReplaceAttr: replaceAttr,
		})
	} else {
		handler = slog.NewTextHandler(opts.W, &slog.HandlerOptions{
			Level:       opts.Level,
			ReplaceAttr: replaceAttr,
		})
	}

	return slog.New(&traceHandler{Handler: handler})
}

// traceHandler wraps a [slog.Handler] and adds trace_id and span_id attributes from the active
// OpenTelemetry span context, so logs can be correlated with traces. The attributes follow the
// record's grouping: if the logger has opened a group via [slog.Logger.WithGroup], they nest under
// that group rather than at the top level, which may keep them from matching a backend's trace-log
// correlation. Loggers built without groups (the common case) get them at the top level.
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

// WithAttrs returns a new [traceHandler] wrapping the underlying handler with the given attributes,
// so trace correlation survives [slog.Logger.With].
func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &traceHandler{Handler: h.Handler.WithAttrs(attrs)}
}

// WithGroup returns a new [traceHandler] wrapping the underlying handler with the given group,
// so trace correlation survives [slog.Logger.WithGroup].
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
