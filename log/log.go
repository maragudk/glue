package log

import (
	"log/slog"
	"os"
	"strings"
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

	if opts.JSON {
		return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{
			Level:       opts.Level,
			ReplaceAttr: replaceAttr,
		}))
	}

	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:       opts.Level,
		ReplaceAttr: replaceAttr,
	}))
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
