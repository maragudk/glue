package app

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"

	"github.com/honeycombio/otel-config-go/otelconfig"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
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

// Start sets up the main application context, the [slog.Logger], an [errgroup.Group], and OpenTelemetry tracing, and calls the given callback.
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
	version, buildTime := getVersionAndBuildTime()
	log.InfoContext(ctx, "Starting app", "name", name, "version", version)

	// We call the callback so it can return errors and we can handle it just here.
	// Also makes it easier to test starting the app if needed, because tests don't handle os.Exit well.
	if err := start(ctx, log, name, version, buildTime, startCallback); err != nil {
		log.ErrorContext(ctx, "Error starting app", "name", name, "error", err)
		os.Exit(1)
	}

	log.InfoContext(ctx, "Stopped app", "name", name)
}

func start(ctx context.Context, log *slog.Logger, name, version, buildTime string, startCallback StartFunc) error {
	opts := []otelconfig.Option{
		otelconfig.WithServiceName(name), otelconfig.WithServiceVersion(version),
		otelconfig.WithMetricsEnabled(false),
		otelconfig.WithExporterProtocol(otelconfig.ProtocolHTTPProto), otelconfig.WithExporterEndpoint("https://api.honeycomb.io"),
	}
	for _, opt := range otelResourceOptions(buildTime) {
		opts = append(opts, otelconfig.WithResourceOption(opt))
	}

	otelShutdown, err := otelconfig.ConfigureOpenTelemetry(opts...)
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
	log.InfoContext(ctx, "Stopping app", "name", name)

	return eg.Wait()
}

// otelResourceOptions describing the process and the build it came from, for the OpenTelemetry resource.
//
// service.approx_build_time is the value of the vcs.time build setting, which is the commit timestamp of
// vcs.revision rather than the moment the binary was compiled. It approximates the build time only as
// closely as the build follows the commit: a pipeline building on merge lands within minutes of it, whereas
// rebuilding an old commit today reports that commit's old date. Read it as "the code is at least this
// old", never as "this binary was produced then". It is in RFC 3339 format, as the build setting supplies
// it.
//
// The attribute is left out when there is no timestamp, because a missing attribute is honest, whereas a
// placeholder value in a timestamp field is not.
//
// The detectors carry a newer semantic conventions schema URL than [otelconfig.ConfigureOpenTelemetry] pins
// on the resource, so merging them yields [resource.ErrSchemaURLConflict], which it tolerates: every
// attribute still reaches the resource. The tests for [Start] configure OpenTelemetry for real, so they fail
// if that ever stops being true.
func otelResourceOptions(buildTime string) []resource.Option {
	opts := []resource.Option{
		resource.WithProcessRuntimeName(),
		resource.WithProcessRuntimeVersion(),
	}

	if buildTime != "" {
		opts = append(opts, resource.WithAttributes(attribute.String("service.approx_build_time", buildTime)))
	}

	return opts
}

// getVersionAndBuildTime from the VCS information stamped into the build info.
// See [versionAndBuildTime] for what the two are.
func getVersionAndBuildTime() (string, string) {
	var settings []debug.BuildSetting
	if info, ok := debug.ReadBuildInfo(); ok {
		settings = info.Settings
	}
	return versionAndBuildTime(settings)
}

// versionAndBuildTime from the given build settings.
// The version is the VCS revision, or "unknown" if the settings carry no such stamp.
// The build time is the commit timestamp of that revision in RFC 3339 format, and is empty if the settings carry
// no such stamp. Both need VCS metadata in the build context, so in practice they are stamped together.
func versionAndBuildTime(settings []debug.BuildSetting) (string, string) {
	version := "unknown"
	var buildTime string

	for _, setting := range settings {
		switch setting.Key {
		case "vcs.revision":
			version = setting.Value
		case "vcs.time":
			buildTime = setting.Value
		}
	}

	return version, buildTime
}
