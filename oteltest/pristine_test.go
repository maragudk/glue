package oteltest_test

import (
	"os"
	"os/exec"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"maragu.dev/is"

	"maragu.dev/glue/oteltest"
)

// TestPreCachedTracerFollowsANewSpanRecorder verifies that a [trace.Tracer] obtained via [otel.Tracer]
// before anything in the process has ever called [oteltest.NewSpanRecorder] still delivers its spans to
// that recorder once it exists, rather than being bound forever to whatever (or nothing) was active when
// it was obtained. A constructor which resolves and keeps its own [trace.Tracer] up front, before a test
// gets a chance to install a recorder, is in exactly this situation.
//
// The situation this checks can only exist in a process where no otel global has been touched yet, which no
// test in this suite can guarantee for itself once another test has run, so this re-execs the test binary in
// a subprocess restricted to just this test. An environment variable marks the subprocess so it runs the
// actual check instead of spawning another subprocess of its own.
func TestPreCachedTracerFollowsANewSpanRecorder(t *testing.T) {
	if os.Getenv("OTELTEST_PRISTINE_SUBPROCESS") == "1" {
		checkPreCachedTracerFollowsANewSpanRecorder(t)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestPreCachedTracerFollowsANewSpanRecorder$", "-test.v")
	cmd.Env = append(os.Environ(), "OTELTEST_PRISTINE_SUBPROCESS=1")
	out, err := cmd.CombinedOutput()
	is.NotError(t, err, string(out))
}

// checkPreCachedTracerFollowsANewSpanRecorder runs as the subprocess [TestPreCachedTracerFollowsANewSpanRecorder]
// spawns, in a process where nothing has touched the global tracer provider yet.
func checkPreCachedTracerFollowsANewSpanRecorder(t *testing.T) {
	t.Helper()

	// Obtained before any call to oteltest below -- and, since this subprocess runs only this one test,
	// before anything else in the process has touched the global tracer provider either.
	tracer := otel.Tracer("test")

	sr := oteltest.NewSpanRecorder(t)

	_, span := tracer.Start(t.Context(), "pre-cached-tracer-span")
	span.End()

	is.Equal(t, 1, len(sr.Ended()), "expected the pre-cached tracer's span to reach the recorder")
}

// TestPreCachedPropagatorFollowsUsePropagators is the propagator counterpart of
// [TestPreCachedTracerFollowsANewSpanRecorder]: a [propagation.TextMapPropagator] reference obtained via
// [otel.GetTextMapPropagator] before anything in the process has ever called [oteltest.UsePropagators]
// still injects with whatever that call configures, rather than staying bound to whatever (or nothing) was
// active when the reference was obtained. A constructor which resolves and keeps its own propagator
// reference up front is in exactly this situation.
//
// Re-execs the test binary in a subprocess restricted to just this test, for the same reason as
// [TestPreCachedTracerFollowsANewSpanRecorder].
func TestPreCachedPropagatorFollowsUsePropagators(t *testing.T) {
	if os.Getenv("OTELTEST_PRISTINE_SUBPROCESS") == "1" {
		checkPreCachedPropagatorFollowsUsePropagators(t)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=^TestPreCachedPropagatorFollowsUsePropagators$", "-test.v")
	cmd.Env = append(os.Environ(), "OTELTEST_PRISTINE_SUBPROCESS=1")
	out, err := cmd.CombinedOutput()
	is.NotError(t, err, string(out))
}

// checkPreCachedPropagatorFollowsUsePropagators runs as the subprocess
// [TestPreCachedPropagatorFollowsUsePropagators] spawns, in a process where nothing has touched the global
// text map propagator yet.
func checkPreCachedPropagatorFollowsUsePropagators(t *testing.T) {
	t.Helper()

	// Obtained before any call to oteltest below -- and, since this subprocess runs only this one test,
	// before anything else in the process has touched the global text map propagator either.
	propagator := otel.GetTextMapPropagator()

	// A real recorder, so the span injected below carries a valid context to write a traceparent for.
	oteltest.NewSpanRecorder(t)
	oteltest.UsePropagators(t, propagation.TraceContext{})

	ctx, span := otel.Tracer("test").Start(t.Context(), "test-span")
	defer span.End()

	carrier := propagation.MapCarrier{}
	propagator.Inject(ctx, carrier)

	is.True(t, carrier.Get("traceparent") != "", "expected the pre-cached propagator to inject with the configured propagator")
}
