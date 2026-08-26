# Diary: Clean up global otel state leakage in jobs runner tests

`/jobs/runner_test.go` sets the global OpenTelemetry tracer provider and propagator and never restores them, so state leaks into other tests in the package run. This was spotted during the issue 197 work (see `/docs/diary/2026-08-24-public-endpoint-tracing.md`, Step 2 follow-ups) and left alone there as pre-existing and out of scope. The fix is surgical: register cleanup so the globals are contained, following the pattern established by `useTraceContextPropagator`/`usePropagators` in `/http/otel_test.go` — noting the sharp edge found there that the default global propagator is a delegator, so a naive "restore the previous value" does not actually undo the installation.

## Step 1: Requirements

**Author:** main

### Prompt Context

**Verbatim prompt:** "I've checked 2, all good. Check in Honeycomb too if you like. Spawn a builder to fix 1, keep it concise and surgical."

**Interpretation:** Verify the merged issue-197 fix in production telemetry (done: all 35 `POST /mcp` spans in the last 36h are trace roots with exactly one link), then delegate a minimal fix for the test hygiene issue.

**Inferred intent:** Tests should not leak global state; the fix should not grow into a refactor.

### What I did

Confirmed the production fix in Honeycomb (c6, 36h: `POST /mcp` volume 35, parented 0, `span.num_links` 1 across the board on build `0ec43200`). Wrote requirements for a builder: contain the global tracer provider and propagator mutations in `/jobs/runner_test.go` with test cleanup, mirroring the `http` package's approach and its delegator caveat.

### Why

Leaked globals make test outcomes order-dependent, which `-shuffle` turns into flakes.

### What worked

The Honeycomb verification took one query; `span.num_links = 1` and `parented = 0` on every span is exactly the designed behavior.

### What didn't work

Nothing failed in this step.

### What I learned

Nothing new this step; the delegator caveat was learned during the issue-197 work.

### What was tricky

Nothing; scope is deliberately minimal.

### What warrants review

The builder's diff should touch only test code in `/jobs`, and `go test -shuffle on ./...` should pass.

### Future work

None anticipated beyond the builder's step.

## Step 2: Implement the cleanup

**Author:** jobs-test-cleanup

### Prompt Context

**Verbatim prompt:** "Small, surgical task — no scope growth. Contain those global mutations with `t.Cleanup` (or the package's existing helper conventions) so they don't leak beyond the test that needs them. Touch ONLY test code in the `jobs` package. Sharp edge, learned in `http/otel_test.go` during the issue-197 work: the default global propagator is a delegator — setting a concrete propagator wires its delegate permanently, so "save and restore the previous propagator" does NOT undo the installation. See `useTraceContextPropagator`/`usePropagators` in `/Users/maragubot/Developer/glue/http/otel_test.go` for the working pattern. Same caveat may apply to the tracer provider delegation — check and handle accordingly. `go test -shuffle on ./...` and `go test -count=3 -race ./jobs` must pass."

**Interpretation:** Add `t.Cleanup` calls around the `otel.SetTracerProvider`/`otel.SetTextMapPropagator` calls at the top of `TestWithTracing` in `/Users/maragubot/Developer/glue/jobs/runner_test.go`, and verify whether the tracer provider has the same permanent-delegation quirk as the propagator before choosing a cleanup strategy.

**Inferred intent:** Stop this one test function from leaking global tracer/propagator state into the rest of the `jobs_test` binary under `-shuffle`, without turning this into a broader refactor of the file.

### What I did

Read `/Users/maragubot/Developer/glue/jobs/runner_test.go` and `/Users/maragubot/Developer/glue/http/otel_test.go` to see the `usePropagators` pattern (lines 665–685 of the latter) and the accompanying comment explaining why "save and restore" doesn't work for the propagator.

To settle whether the tracer provider has the same quirk, I read the vendored source of `go.opentelemetry.io/otel@v1.45.0` (`internal/global/state.go` and `internal/global/trace.go`). Both `SetTracerProvider` and `SetTextMapPropagator` share the identical shape: a `sync.Once` (`delegateTraceOnce` / `delegateTextMapPropagatorOnce`) that, on the *first-ever* call to `Set*` in the process, permanently wires the package's built-in delegating default (`*tracerProvider` / `*textMapPropagator`) to forward to whatever was passed in. Any later `Set*` call just stores a new pointer in an `atomic.Value` and doesn't touch the delegate again. So the danger is specifically: if `TestWithTracing` is the first thing in the `jobs_test` binary to call `otel.SetTracerProvider`, "saving `otel.GetTracerProvider()` beforehand and restoring it afterwards" would restore the *default delegating wrapper* — but that wrapper is now permanently pinned to the SDK provider `TestWithTracing` created, not to true no-op behavior. The same applies to the propagator. Confirmed `go.opentelemetry.io/otel/trace/noop.NewTracerProvider()` exists as the concrete no-op stand-in to install instead of trying to restore "previous".

Edited `/Users/maragubot/Developer/glue/jobs/runner_test.go`:
- Added the `go.opentelemetry.io/otel/trace/noop` import.
- After `otel.SetTracerProvider(tp)`, added `t.Cleanup` that shuts the SDK provider down (`tp.Shutdown(context.WithoutCancel(t.Context()))`, mirroring `oteltest.NewSpanRecorder`'s own cleanup) and then installs `noop.NewTracerProvider()` as the global tracer provider.
- After `otel.SetTextMapPropagator(propagation.TraceContext{})`, added `t.Cleanup` that installs `propagation.NewCompositeTextMapPropagator()` (an empty composite, extracts nothing), exactly the stand-in `usePropagators` uses.
- Added comments explaining the delegator caveat for both, with the propagator one pointing at `usePropagators` in `/Users/maragubot/Developer/glue/http/otel_test.go` for the fuller writeup rather than repeating it.

Did not touch the three subtests further down in `TestWithTracing` that already call `oteltest.NewSpanRecorder(t)` — that helper already scopes its own tracer-provider cleanup per subtest and was out of scope here.

### Why

Global otel state set without cleanup is exactly the kind of thing `-shuffle` is meant to catch: whichever test happens to run afterward in the same binary inherits leftover tracer/propagator state and its outcome becomes order-dependent.

### What worked

Reading the vendored `internal/global` source directly settled the "does the same caveat apply to the tracer provider" question with certainty rather than guesswork — the `SetTracerProvider`/`SetTextMapPropagator` implementations are structurally identical (same `sync.Once`-guarded one-time delegate wiring), so the fix needed the same shape: never rely on "restore what was there before," always install a known-safe stand-in in cleanup, because "before" might have been the pinned default.

`go build ./...`, `go vet ./jobs/...`, `golangci-lint run ./jobs/...` (0 issues), `go test -count=3 -race ./jobs`, and `go test -shuffle on ./...` (and several repeats with `-shuffle on -count=1` targeted at `./jobs ./http ./oteltest ./otel`) all passed cleanly on the first attempt after the edit.

### What didn't work

Nothing failed here — build, vet, lint, and every test run (including race and repeated shuffle runs) passed on the first try after the edit.

### What I learned

`otel.SetTracerProvider` and `otel.SetTextMapPropagator` are backed by literally the same mechanism in `go.opentelemetry.io/otel/internal/global/state.go`: a `sync.Once` per-kind that fires only on the first `Set*` call for that kind in the process and permanently rewires the built-in delegating default. Every `Set*` call after that first one is a plain, fully-reversible store. So "restore the previous value" is only unsafe when the *first* mutation of that global in the process is the one being undone — which a test can't know in advance under `-shuffle`, so the safe rule is: never restore "previous," always install a concrete, known-inert stand-in (`noop.NewTracerProvider()` for the tracer provider, an empty `propagation.NewCompositeTextMapPropagator()` for the propagator).

The codebase already uses `[identifier]`-style doc-link brackets inside ordinary function-body comments, not just above declarations (e.g. `/Users/maragubot/Developer/glue/http/otel_test.go:60,233,667,670`), so using that syntax in the new comments here (`[otel.SetTracerProvider]`) is consistent with existing convention even though it's not a Go doc comment in the strict sense.

### What was tricky

Confirming the tracer-provider half of the caveat required reading OpenTelemetry SDK internals rather than trusting the propagator writeup by analogy — the two mechanisms turned out to be identical in shape, but that had to be verified against `internal/global/state.go` and `internal/global/trace.go` rather than assumed.

### What warrants review

The diff is entirely within `/Users/maragubot/Developer/glue/jobs/runner_test.go` (one new import, cleanup added around the two `otel.Set*` calls at the top of `TestWithTracing`, comments explaining why). To validate: `go test -count=3 -race ./jobs` and `go test -shuffle on ./...` both need to pass, and did. Worth a second look: whether shutting the SDK tracer provider down in cleanup (`tp.Shutdown`, mirroring `oteltest.NewSpanRecorder`) is wanted here or is scope creep beyond the literal ask of "contain the mutations" — I judged it in-scope since it's the same one-line idiom the existing `oteltest` helper already uses for the same kind of provider, and left it in.

### Future work

None anticipated. The `oteltest.NewSpanRecorder` helper's own "save current, restore previous" pattern for the tracer provider (`/Users/maragubot/Developer/glue/oteltest/oteltest.go` lines 24–29) has the identical theoretical exposure if it's ever the first thing in a binary to touch the global tracer provider — out of scope for this task, but worth a future look if it ever surfaces as a real flake.
