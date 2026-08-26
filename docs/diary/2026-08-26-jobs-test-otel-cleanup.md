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

## Step 3: Fix the root cause in `oteltest` instead of the call site

**Author:** jobs-test-cleanup

### Prompt Context

**Verbatim prompt:** "shouldn't this use the oteltest package? He's right, and the problem is bigger than the call site you fixed. Rework PR 199... Fix the root cause in `oteltest`: make restore-previous safe. Note the nested case — an inner `NewSpanRecorder` must restore the outer's provider, so unconditionally installing noop in cleanup is wrong there. Suggested shape: a package-level `sync.Once` in oteltest that, on first helper use, pins the global tracer provider (and propagator) to inert implementations (noop provider / empty composite) before any save happens, so 'previous' is never the pristine delegator and save/restore becomes always-correct... Add a propagator helper to `oteltest`... and migrate `usePropagators`/`useTraceContextPropagator` in `/Users/maragubot/Developer/glue/http/otel_test.go` to it. Rework `jobs/runner_test.go` to use the `oteltest` helpers instead of hand-rolled globals... Fix or strengthen the vacuous 'restores the previous tracer provider' test... `go test -shuffle on ./...` and `-count=3 -race` on `./jobs ./http ./oteltest ./otel` must pass... this is exported-API surface now, so a proper review pass."

**Interpretation:** Markus (via the lead) spotted that Step 2's fix only patched the one call site in `/Users/maragubot/Developer/glue/jobs/runner_test.go` while `oteltest.NewSpanRecorder` — exported library API, used across the module — carried the exact same "save `otel.GetTracerProvider()`, restore it in cleanup" pattern Step 2 had already proven unsafe for propagators. The ask was to fix the shared helper package itself, add the missing propagator helper there too, migrate both existing call sites (`jobs` and `http`) onto it, and make the regression test in `oteltest_test.go` actually assert behavior instead of pointer identity.

**Inferred intent:** Stop re-deriving the same fix at every call site; put the safety guarantee in the one shared package so every current and future caller of `oteltest.NewSpanRecorder`/a propagator helper gets it for free, and prove the guarantee with tests that would actually fail if it regressed.

### What I did

Re-read `/Users/maragubot/Developer/glue/oteltest/oteltest.go` and `/Users/maragubot/Developer/glue/oteltest/oteltest_test.go`, and confirmed `NewSpanRecorder` did exactly what Markus described: `previous := otel.GetTracerProvider()` before `otel.SetTracerProvider(tp)`, then `otel.SetTracerProvider(previous)` in cleanup.

Before implementing, I wanted to know whether this was actually an *observable* bug for the tracer provider specifically, since the SDK's `sdktrace.TracerProvider.Shutdown()` makes every later `.Tracer()` call on that provider return a no-op tracer, checked live on every call (confirmed by reading `provider.go` in the vendored `go.opentelemetry.io/otel/sdk@v1.45.0`). I wrote a standalone repro at `/private/tmp/.../scratchpad/otelprobe` (a throwaway module, not part of this repo) that reproduced the exact "first-ever `SetTracerProvider` call in the process, save-then-restore in cleanup" scenario against the real SDK. Result: a span started after the restored-but-still-permanently-wired default came back with `valid=false recording=false` — i.e. functionally a no-op, because the provider it silently pointed to was already shut down. So for `NewSpanRecorder` specifically, the existing "shutdown, then restore previous" idiom was accidentally safe in practice, because `Shutdown()` neuters the stale delegate regardless of what the global points at. The propagator has no such self-neutralizing mechanism (no shutdown concept), so that half of Markus's report is a real, currently-inert bug in the tracer-provider case and a real, live bug in spirit for any future propagator helper built the naive way.

I implemented the suggested shape anyway, since it removes reliance on that Shutdown-based accident (a future change to `NewSpanRecorder` that skips or reorders the shutdown call would silently reintroduce a real bug) and unifies the fix for both globals. In `/Users/maragubot/Developer/glue/oteltest/oteltest.go`:
- Added `var pinGlobals = sync.OnceFunc(func() { otel.SetTracerProvider(noop.NewTracerProvider()); otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator()) })`, called at the top of both `NewSpanRecorder` and the new `UsePropagators`, before either captures "previous". Because it runs before the first-ever save, "previous" downstream is never the SDK's internal delegating placeholder — it's always this pin or a concrete value a prior helper call installed — so plain save/restore becomes correct everywhere, including the nested case (an inner `NewSpanRecorder`/`UsePropagators` restoring the outer's still-live provider/propagator, which was already correct before this change and remains so).
- Added `func UsePropagators(t *testing.T, propagators ...propagation.TextMapPropagator) propagation.TextMapPropagator`, mirroring `NewSpanRecorder`'s shape: composes the given propagators, installs them globally, registers `t.Cleanup` to restore "previous", and returns the composed propagator for the caller to inspect (`.Fields()` etc.).

Migrated `/Users/maragubot/Developer/glue/http/otel_test.go`'s `usePropagators` to call `oteltest.UsePropagators(t, propagation.TraceContext{}, propagation.Baggage{})` instead of managing the global and cleanup itself, keeping only the package-specific self-check ("expected the propagator under test to carry baggage") inline, now checked against the returned propagator's `.Fields()` rather than re-fetching the global.

Reworked `/Users/maragubot/Developer/glue/jobs/runner_test.go`'s `TestWithTracing`: replaced the Step-2 hand-rolled `sdktrace.NewTracerProvider()` + `otel.SetTracerProvider` + `otel.SetTextMapPropagator(propagation.TraceContext{})` (with the noop/empty-composite cleanup Step 2 added) with a single `oteltest.NewSpanRecorder(t)` + `oteltest.UsePropagators(t, propagation.TraceContext{})` at the top of the test function. Checked every subtest: the first four don't call their own recorder and only assert `span.SpanContext().IsValid()`/trace-ID equality, so a shared, working (non-noop) provider from the top-level call is sufficient — they never inspect what got recorded, so the shared recorder accumulating unread spans doesn't matter. The later three subtests already called their own nested `oteltest.NewSpanRecorder(t)`, which layers correctly on top of the shared one and restores it afterward. Dropped the now-unused `sdktrace` and `noop` imports from the file; `context` stayed, since it's still used for `context.Context` parameter types elsewhere in the file (the `context.WithoutCancel` cleanup call that needed it went away with the hand-rolled setup).

Reworked `/Users/maragubot/Developer/glue/oteltest/oteltest_test.go`'s vacuous `is.Equal(t, previous, otel.GetTracerProvider())` test into two behavioral ones (`"does not record spans through the global tracer after its own cleanup"` and `"restores the outer recorder for a nested test"`), and added `TestUsePropagators` with three behavioral tests (injection works with a real recorder in scope, no leakage after cleanup, correct nested restore). Verified these tests actually discriminate by temporarily removing the `pinGlobals()` calls from `NewSpanRecorder`/`UsePropagators`: with them removed, `go test -v -run '^TestUsePropagators$' ./oteltest/...` failed exactly as expected — `does_not_propagate_through_the_global_propagator_after_its_own_cleanup` got a leaked `traceparent` header (`"00-7d8cb0ab...-01"`) instead of `""` — then restored `pinGlobals()` and confirmed everything passed again. Ran into one test-design gotcha along the way: `propagation.TraceContext{}.Inject` writes nothing for an invalid span context, which every no-op tracer produces, so both propagator tests needed a real `oteltest.NewSpanRecorder(t)` in scope to produce a valid span to inject — the first version of the "injects with the given propagators" test failed for this reason before I added the recorder.

Ran `golangci-lint run` and `gofmt -l` across the four changed packages; `gofmt` flagged an import-ordering issue in `oteltest_test.go` (the new `tracetest` import landed alphabetically before `semconv` instead of after) which `gofmt -w` fixed.

Then dispatched two independent code-review subagents (via the fabrik `code-review` skill's two-competing-reviewer method, since this is now exported library API) with a detailed brief covering the mechanism, the diff, and nine specific things to check per reviewer, including a mandatory "read every new/changed comment as an outside reader" pass for context leakage. Both reviewers independently ran their own builds/tests/races and one wrote its own standalone repro to verify the `otel` SDK's delegate-wiring mechanics directly against the vendored source, rather than trusting the brief. Both converged on the same moderate finding: the `pinGlobals` doc comment (and two echoing comments in `oteltest_test.go`) cited `[otel.SetTracerProvider]`/`[otel.SetTextMapPropagator]` via doc-link syntax as though their public godoc documented the permanent-delegate-wiring behavior, when that behavior is actually undocumented, internal to `go.opentelemetry.io/otel/internal/global`, and could change without notice in a future otel release without being flagged as a breaking API change. I fixed this by rewording the `pinGlobals` comment to say plainly that this is "an internal detail of that module, not a documented guarantee of `[otel.SetTracerProvider]` or `[otel.SetTextMapPropagator]`, but observably true of the versions this module depends on," and softened the two test-file echoes to say "the SDK's internal placeholder" instead of implying it was a documented default. One reviewer additionally raised (single-reviewer, not corroborated) a real but pre-existing and currently-inert SDK sharp edge: a `trace.Tracer` obtained via `otel.Tracer(name)` and cached on a struct field *before* the very first `SetTracerProvider` call in a process becomes permanently deaf to later provider changes — true independent of this fix, confirmed not currently triggered anywhere in the repo (`jobs.WithTracing`, `s3.NewBucket`, etc. are never constructed before `oteltest` helpers run in the same test binary), and not something `pinGlobals` introduces or worsens, so I left it undocumented in code and noted it here instead. The same reviewer's naming nitpick (`UsePropagators` vs. matching `NewSpanRecorder`'s `NewX` convention) was single-reviewer, non-serious, and the coordinator's own request had explicitly floated "`UsePropagators`... or similar naming" as an acceptable option, so I kept the name as-is.

After the comment fixes, reran `go build ./...`, `go vet ./...`, `gofmt -l`, `golangci-lint run` (0 issues) on all four changed packages, `go test -count=3 -race ./jobs ./http ./oteltest ./otel`, and `go test -shuffle on ./...` (full suite) — all clean.

### Why

A shared test helper is exactly the place a global-state safety fix belongs: fixing it once in `oteltest` protects every current and future caller, instead of every caller having to independently discover and re-derive the same fix Step 2 worked out for one call site.

### What worked

Writing a throwaway repro against the real SDK (twice: once to characterize whether the tracer-provider case was actually observable, once more implicitly via the deliberate "remove `pinGlobals`, watch the test fail" check) turned "I believe this fix is correct" into "I watched this fix's absence produce the exact leaked `traceparent` the bug report predicted, and watched it disappear when the fix went back in." That's a much stronger validation than reasoning about `sync.Once` semantics from the SDK docs alone. Dispatching two independent reviewers with a detailed technical brief (rather than a generic "review this diff") meant both did real verification work — one traced the exact vendored source for `SetTracerProvider`, the other wrote its own independent repro — and their convergence on the same comment-accuracy issue, despite differing focus otherwise, made that finding easy to trust and fix confidently.

### What didn't work

The first draft of `TestUsePropagators`'s "injects with the given propagators" test failed with `Expected true, but got false` / `expected a traceparent header to be injected`, because it started a span with no real tracer provider active, so the span context was invalid and `propagation.TraceContext{}.Inject` silently wrote nothing regardless of which propagator was installed. Fixed by adding `oteltest.NewSpanRecorder(t)` alongside `oteltest.UsePropagators(t, ...)` in that test. The same class of mistake showed up in the "does not propagate... after its own cleanup" test's first draft too, caught before running it this time by reasoning through the same gotcha, and fixed by adding a recorder scoped to the whole test (not the inner sub-test) so the outer injection always has a valid span regardless of the inner cleanup.

### What I learned

`sdktrace.TracerProvider.Shutdown()` checks `isShutdown` live on every `.Tracer()` call (confirmed in `provider.go` of the vendored SDK, not cached at shutdown time against already-issued `Tracer` values only), so a shut-down provider is self-neutralizing no matter how many old references or delegate wirings still point at it — this is why the pre-existing `NewSpanRecorder` "shutdown then restore previous" pattern never actually manifested as a bug in practice, even though it relied on an implementation detail the coordinator was right to distrust. `propagation.TraceContext{}.Inject` is a no-op for an invalid span context, which is exactly what any no-op tracer produces — so any test asserting propagator *injection* behavior needs a real recording tracer in scope, or it passes (or fails) for the wrong reason regardless of which propagator is actually active.

### What was tricky

Distinguishing "this bug is real and worth fixing the way the coordinator described" from "this bug happens to not currently manifest for this specific case" required going past the propagator-only precedent in `/Users/maragubot/Developer/glue/http/otel_test.go` and actually reading the SDK's `Shutdown()` implementation and reasoning through a repro — the temptation was to pattern-match "propagator was unsafe, so tracer provider must be too" without checking whether the two globals' failure modes were actually equivalent (they weren't, because of `Shutdown()`'s live check), even though implementing the requested fix was correct regardless of that nuance.

### What warrants review

The diff spans `/Users/maragubot/Developer/glue/oteltest/oteltest.go`, `/Users/maragubot/Developer/glue/oteltest/oteltest_test.go`, `/Users/maragubot/Developer/glue/http/otel_test.go`, and `/Users/maragubot/Developer/glue/jobs/runner_test.go`. `oteltest.go` is exported library API now carrying a new public function (`UsePropagators`) and a new package-level `sync.OnceFunc` (`pinGlobals`) that every test in the module touching these globals implicitly depends on being called first — worth confirming no code outside `oteltest.go` calls `otel.SetTracerProvider`/`otel.SetTextMapPropagator` directly (both reviewers independently grepped and confirmed none does, as of this diff). To validate: `go test -count=3 -race ./jobs ./http ./oteltest ./otel` and `go test -shuffle on ./...` both need to pass, and did, repeatedly. The two-reviewer report is summarized above; both are satisfied, no serious or corroborated-moderate issues remain open.

### Future work

The single-reviewer finding about pre-first-call `otel.Tracer(name)` caching being permanently deaf to later provider changes is real (verified independently by that reviewer with its own repro) but not caused or worsened by this change, not currently triggered anywhere in this repo, and orthogonal to `pinGlobals`'s guarantee (it's about *tracer objects obtained before pinning fires*, not about the globals themselves) — worth keeping in mind if a future package ever constructs something that caches `otel.Tracer(...)` at package-init time or before test setup runs, but not actionable today.
