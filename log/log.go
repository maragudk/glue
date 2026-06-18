package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/trace"
)

type NewLoggerOptions struct {
	JSON   bool
	Level  slog.Level
	NoTime bool
	W      io.Writer
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

	return slog.New(newTraceHandler(handler))
}

// traceHandler wraps a [slog.Handler] and adds trace_id and span_id attributes from the active
// OpenTelemetry span context, so logs can be correlated with traces. The attributes are always
// added at the top level, even when the logger has opened groups via [slog.Logger.WithGroup].
type traceHandler struct {
	base    slog.Handler                      // handler with no groups or attributes opened on it
	applied slog.Handler                      // base with the logger's own groups and attributes applied
	withs   []func(slog.Handler) slog.Handler // the logger's WithGroup and WithAttrs operations, in order
}

// newTraceHandler wrapping base.
func newTraceHandler(base slog.Handler) *traceHandler {
	return &traceHandler{base: base, applied: base}
}

// Enabled reports whether the handler handles records at the given level.
func (h *traceHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.base.Enabled(ctx, level)
}

// Handle the record, adding trace_id and span_id attributes if the context carries a valid span context.
func (h *traceHandler) Handle(ctx context.Context, r slog.Record) error {
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return h.applied.Handle(ctx, r)
	}

	// Add trace_id and span_id to the base handler so they land at the top level even when the
	// logger has opened groups, then re-apply the logger's own groups and attributes on top.
	handler := h.base.WithAttrs([]slog.Attr{
		slog.String("trace_id", sc.TraceID().String()),
		slog.String("span_id", sc.SpanID().String()),
	})
	for _, with := range h.withs {
		handler = with(handler)
	}
	return handler.Handle(ctx, r)
}

// WithAttrs returns a new [traceHandler] that applies the given attributes after the trace attributes.
func (h *traceHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h.with(func(inner slog.Handler) slog.Handler {
		return inner.WithAttrs(attrs)
	})
}

// WithGroup returns a new [traceHandler] that opens the given group after the trace attributes.
func (h *traceHandler) WithGroup(name string) slog.Handler {
	return h.with(func(inner slog.Handler) slog.Handler {
		return inner.WithGroup(name)
	})
}

// with records op and returns a new [traceHandler] with it applied, so trace attributes keep landing
// at the top level while the logger's own groups and attributes nest as the caller expects.
func (h *traceHandler) with(op func(slog.Handler) slog.Handler) *traceHandler {
	return &traceHandler{
		base:    h.base,
		applied: op(h.applied),
		withs:   append(slices.Clip(h.withs), op),
	}
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
