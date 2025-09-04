package app

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/honeycombio/otel-config-go/otelconfig"
	"golang.org/x/sync/errgroup"
	"maragu.dev/env"
	"maragu.dev/errors"

	"maragu.dev/glue/log"
)

// Goer is just the executing part of [errgroup.Group].
type Goer interface {
	Go(func() error)
}

// StartFunc is given to [Start] and should not block, instead starting components with the given error group.
type StartFunc = func(ctx context.Context, log *slog.Logger, eg Goer) error

// Start sets up the main application context, the [slog.Logger], an [errgroup.Group], and Open Telemetry tracing, and calls the given callback.
// The callback function should start up all necessary components of the app using the error group, and not block on anything itself in the main goroutine.
func Start(startCallback StartFunc) {
	// Catch SIGTERM and SIGINT from the terminal, so we can do clean shutdowns.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	// This loads from .env in development and /run/secrets/env when run in a container through Docker compose.
	// We ignore errors because neither may exist.
	_ = env.Load(".env")
	_ = env.Load("/run/secrets/env")

	// Create a new default log that is injected into all parts of the application.
	// It uses the slog package from stdlib, which is a leveled, structured logger.
	// We can output plain text or JSON as needed.
	log := log.NewLogger(log.NewLoggerOptions{
		JSON:   env.GetBoolOrDefault("LOG_JSON", true),
		Level:  log.StringToLevel(env.GetStringOrDefault("LOG_LEVEL", "info")),
		NoTime: env.GetBoolOrDefault("LOG_NO_TIME", false),
	})

	name := env.GetStringOrDefault("APP_NAME", "App")
	log.Info("Starting app", "name", name)

	// We call the callback so it can return errors and we can handle it just here.
	// Also makes it easier to test starting the app if needed, because tests don't handle os.Exit well.
	if err := start(ctx, log, name, startCallback); err != nil {
		log.Error("Error starting app", "name", name, "error", err)
		os.Exit(1)
	}

	log.Info("Stopped app", "name", name)
}

func start(ctx context.Context, log *slog.Logger, name string, startCallback StartFunc) error {
	otelShutdown, err := otelconfig.ConfigureOpenTelemetry(
		otelconfig.WithServiceName(name), otelconfig.WithServiceVersion(getVersion()),
		otelconfig.WithMetricsEnabled(false),
		otelconfig.WithExporterProtocol(otelconfig.ProtocolHTTPProto), otelconfig.WithExporterEndpoint("https://api.honeycomb.io"),
	)
	if err != nil {
		return errors.Wrap(err, "error configuring open telemetry")
	}
	defer otelShutdown()

	// An error group is used to start and wait for multiple goroutines that can each fail with an error.
	eg, ctx := errgroup.WithContext(ctx)

	if err := startCallback(ctx, log, eg); err != nil {
		return err
	}

	// Wait for the context to be done, which happens when the user sends a SIGTERM or SIGINT signal.
	<-ctx.Done()
	log.Info("Stopping app", "name", name)

	return eg.Wait()
}

func getVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, setting := range info.Settings {
			if setting.Key == "vcs.revision" {
				return setting.Value
			}
		}
	}
	return "unknown"
}
