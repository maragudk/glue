# Diary: Record panics on spans

Goal: make a panicking HTTP handler or background job traceable. Before this, a panic unwound past
everything after `next.ServeHTTP` in `http.OpenTelemetry`, so the span reached the exporter with no
route, no name derived from a route, and `codes.Unset` -- indistinguishable from a healthy request.
`jobs.WithTracing` had the same blind spot, since goqite's runner recovers a panicking job above the
wrapper. GitHub issue #195.

This picks up the "Future work" item left at the end of
`/docs/diary/2026-08-21-process-context-on-main-spans.md`, which fixed the attribute half of the
problem (`main`, `uptime_sec` and `url.query` moved to the top of the middleware, so they survive a
panic) and deliberately left the naming half alone.

## Step 1: Move the middleware tail into a defer and record the panic

**Author:** builder (sub-agent)

### Prompt Context

**Verbatim prompt:** "Fix GitHub issue #195 ("Panicking handlers and jobs produce unattributable
spans"). [full issue text quoted in the prompt] Implementation notes: 1. `http/otel.go`,
`OpenTelemetry` middleware: move the post-`next.ServeHTTP` tail (client-disconnect check,
`span.SetName(spanName(r))`, the `http.route` attribute) into a `defer` registered before
`next.ServeHTTP`. In that defer, `recover()`; if non-nil, record the panic on the span
(`span.RecordError` with an error made from the panic value -- handle both `error` and non-error
panic values, e.g. `fmt.Errorf("panic: %v", v)`; use the existing style of the file), set span status
`codes.Error`, then re-panic so `net/http`'s server-level handling is unchanged. Note
`http.ErrAbortHandler` is a deliberate abort mechanism -- decide consciously whether to record it or
just re-panic it silently; document the choice in a short comment either way. Keep the ordering so
name and route are set whether or not there was a panic. Beware:
`chi.RouteContext(r.Context()).RoutePattern()` at defer time -- on a panic mid-router the pattern may
be partial; that's acceptable, don't over-engineer. 2. `jobs/runner.go`, `WithTracing`: same shape --
defer a recover that records the panic on the job span, sets error status, re-panics (goqite's runner
recovers above). Currently the wrapper starts a span, calls the wrapped func, and sets status from
the returned error; keep that logic working. 3. Tests: in `http` package, a test with a handler that
panics under the `OpenTelemetry` middleware [...] asserting the exported span has the right name
(`GET /panic` or similar via a chi route), carries `http.route`, has status Error, and has an
exception event. [...] Also a test for a panicking job in `jobs`."
**Interpretation:** Move the request-dependent tail of the middleware into a `defer`, record the
panic there, re-panic, and mirror the shape in `jobs.WithTracing`. Cover both with tests which fail
without the fix.
**Inferred intent:** A panic is the worst class of failure, and it is the one currently invisible in
tracing. Make it as queryable as any other failed request, without changing what the server or the
job runner do with the panic afterwards.

### What I did

Red first, in `/http/otel_test.go`: three new subtests asserting that a panicking handler's span is
named `GET /things/{id}`, carries `http.route`, has `codes.Error`, and carries an exception event
whose stack trace reaches the panic site; plus one asserting `http.ErrAbortHandler` is *not* counted
as an error. They failed as expected:

    --- FAIL: TestOpenTelemetry/names_a_panicking_handler's_span_and_sets_its_route
        otel_test.go:141: Expected "GET /things/{id}", but got "GET" (type string)
    --- FAIL: TestOpenTelemetry/records_a_panicking_handler_as_an_error_on_the_span/error
        otel_test.go:172: Expected "Error", but got "Unset" (type codes.Code)

Then in `/http/otel.go`, the client-disconnect check, `span.SetName(spanName(r))` and the
`http.route` attribute moved into a `defer` registered before `next.ServeHTTP`. The defer calls
`recover()` first, sets name and route either way, and on a non-nil value calls a new
`recordPanicOnSpan` before re-panicking. `recordPanicOnSpan` records the panic value as an error --
unwrapped when it already is one, so `exception.type` keeps naming the handler's own error type --
with `trace.WithStackTrace(true)`, and sets `codes.Error`.

`/jobs/runner.go` got the same shape in `WithTracing`, registered after `defer span.End()` so the
recording defer runs first and the span is still recording when it does.

Both test packages needed to tell a deliberate recording apart from the SDK's own (see below), so
`/oteltest/oteltest.go` gained `ExceptionEventsWithStackTrace`, with its own tests.

Three commits: `ee4b46a` (oteltest helper), `cc09a69` (http), `147cf57` (jobs).

### Why

The defer is the only place which runs on both the normal and the panicking path, and the name and
route are only knowable after the router below has matched -- so the tail has to be there, and the
panic recording belongs with it rather than in a separate `recover`.

Re-panicking rather than swallowing keeps `net/http`'s server-level handling and goqite's runner
behaviour exactly as they were. Verified rather than assumed: a probe through `httptest` with a real
`http.Server` and a captured `ErrorLog` shows the server still logs `http: panic serving ...` with a
stack which reaches down to the handler, with two extra frames from the re-panic
(`OpenTelemetry.func1.1` and `runtime.panic`) inserted above it.

### What worked

Writing the tests first paid immediately: the first red run showed the span was named `GET`, not the
empty string the issue reports. That is #194's doing -- `otelhttp` names the span from
`spanName(r)` at start time, before any route has matched -- so the issue's "named `""`" is now out
of date, though "unattributable" still held.

Deferring the whole tail also fixed a case nobody asked about: `runtime.Goexit` in a handler used to
skip the tail as well, and now gets a named span too.

### What didn't work

The first green run wasn't green. Both panic subtests failed on the exception event count:

    otel_test.go:174: expected exactly one exception event, got 2

The second event comes from the SDK itself. `recordingSpan.End` in `sdk@v1.45.0/trace/span.go` calls
`recover()` and, if a panic is in flight, adds an `exception` event of its own with
`ExceptionType(typeStr(recovered))` and `ExceptionMessage(fmt.Sprint(recovered))` -- and a stack
trace only if the caller passed `trace.WithStackTrace` to `End`, which neither `otelhttp` nor this
package does. So a panicking request already had an exception event before this change; what it
lacked was a status, a name, a route, and any indication of where the panic came from.

That forced a real decision, described under "What was tricky".

### What I learned

`recover()` inside a deferred function does not truncate the stack for the code which runs after it:
`runtime.Stack` there still walks down through the frames being unwound. That is what makes
`RecordError(err, trace.WithStackTrace(true))` in the defer worth anything at all -- it records the
stack of the panic, not the stack of the recorder. The test pins this by asserting the recorded
stack mentions `otel_test.go`.

The SDK's panic recording in `End` is not something a library can lean on. It is switchable at the
provider level (`panicRecordingDisabled`), and it only fires where `End` was deferred directly rather
than wrapped in a closure. glue does not build the tracer provider -- `app.start` hands that to
`otelconfig.ConfigureOpenTelemetry`, and `http.OpenTelemetry` is exported and usable with any
provider at all -- so the middleware cannot assume it is on.

`fmt.Errorf` with no `%w` verb returns `*errors.errorString`, not `*fmt.wrapError`, which is why
wrapping a panic value that is already an error would have been a loss: `RecordError` derives
`exception.type` from the concrete type, and wrapping turns every panic into `*fmt.wrapError`.

### What was tricky

Deciding whether to record the exception at all, given the SDK already does. Recording means a
panicking span can carry two exception events, which double-counts panics for anyone counting
`exception` events. Not recording means depending on a provider setting glue does not own, and giving
up the stack trace, which is the single most useful thing about a panic.

Recording won, on the grounds that glue is a library and cannot assume how the provider was built.
The duplicate is documented at both call sites, and the tests filter on the presence of a stack
trace so they pin *this* package's recording rather than the SDK's. If it turns out to be a nuisance
in a backend, the honest fix is at the provider, not here.

The other judgement call was `http.ErrAbortHandler`. It is how a handler says it is giving up on
purpose, and `net/http` suppresses it in its own log, so counting it in the error rate would be
wrong. `recordPanicOnSpan` returns early on it -- via `errors.Is`, so a wrapped one counts too -- and
the re-panic still happens, since only the server can act on it. The SDK's own event still appears
for it, which is out of this package's hands.

### What warrants review

The two-exception-events decision is the part most worth a second opinion, and it is easy to reverse:
dropping `span.RecordError` from `recordPanicOnSpan` and from `jobs.WithTracing` leaves the status
and loses the stack trace.

Second, the panic-recording block is now duplicated between `/http/otel.go` and `/jobs/runner.go`.
Sharing it would mean an exported helper in `/otel/otel.go`, which `AGENTS.md` argues against
("package-level identifiers must begin with lowercase by default, ... to make the API surface area
towards other packages smaller"). The two copies also differ: only the HTTP one knows about
`http.ErrAbortHandler`, and the status descriptions differ (`panic` vs `job panicked`). Left
duplicated on purpose.

Third: `panic(nil)`. Under Go 1.21+ semantics `recover()` returns a `*runtime.PanicNilError`, so the
`if v == nil { return }` guard is unreachable for a real panic and everything is fine. Under
`GODEBUG=panicnil=1` it is reachable, and the middleware then *swallows* the panic instead of letting
it reach the server. Confirmed with a probe: the panic reaches the caller with the default GODEBUG
and does not with `panicnil=1`. Every recover-based middleware in the ecosystem behaves the same way
(chi's `Recoverer` included), the module declares `go 1.25.0`, and the alternatives -- tracking
completion with a flag -- turn a `runtime.Goexit` into a panic, which is worse. Left as is.

### Future work

`http.ErrAbortHandler` currently leaves nothing on the span at all beyond the SDK's exception event:
the status stays `Unset` and the request looks healthy. An attribute marking a deliberate abort would
make those countable without putting them in the error rate. Out of scope here, and it would need a
name outside semantic conventions.

`jobs.WithTracing`'s `defer span.End()` could pass `trace.WithStackTrace(true)`, which costs nothing
on the normal path and would give the SDK's own event a stack trace too. Not done, because it makes
the duplicate event more useful rather than less, which is the wrong direction.

## Step 2: Self-review and follow-up

**Author:** builder (sub-agent)

### Prompt Context

**Verbatim prompt:** (same task) "Once the implementation is done, self-review before handing back to
the lead. Work in two phases: first, review the code you wrote -- read the diff, check logic, look
for missing edge cases and test coverage gaps; second, run automated checks (tests and linters). Be
honest about real issues; if everything looks good, say so rather than inventing problems."
**Interpretation:** Run the `code-review` skill over the three commits, triage what comes back, fix
what is real.
**Inferred intent:** Catch what a single pass missed, particularly the trade-offs taken on judgement
rather than evidence.

### What I did

Dispatched two competing reviewers over `5e40bf3..HEAD` (they first got `main...HEAD`, which on this
worktree also contains the merged #194, so both were re-scoped mid-run). Four findings had consensus
and two more were single-reviewer calls serious enough to act on. Every one of them was fixed in
`33c7e6e`:

**The recording moved to `otel.RecordPanic`** in `/otel/otel.go`. Both reviewers wanted the ten
duplicated lines shared, and both pointed at the drift which had already started: two wordings of the
same SDK argument, and two status descriptions (`panic` vs `job panicked`) for the same condition. The
cost is one exported symbol, which `AGENTS.md` argues against in general; the `otel` package is the
right home, since its `MainSpanAttributes` doc already invites applications to instrument units of
work of their own. The `http.ErrAbortHandler` skip stayed in `/http/otel.go`, where it belongs.

**The re-panic became `defer panic(v)` at the top of the recovering function**, which is what
`recordingSpan.End` does. As written before, ~25 lines ran between `recover()` and `panic(v)`, and
anything panicking in there would have replaced the handler's panic with its own -- the very
unattributable panic this change exists to remove. Nothing bites today, since
`chi.RouteContext(nil).RoutePattern()` is nil-safe, but the failure mode is silent.

**`http.ErrAbortHandler` is now matched by identity**, not `errors.Is`. `net/http` compares it by
identity before suppressing its own log, so a *wrapped* `ErrAbortHandler` is a panic as far as the
server is concerned -- and dropping it from the span would have been the same bug again. A table row
panicking with `fmt.Errorf("giving up: %w", http.ErrAbortHandler)` now pins that it is recorded.

**Comments stopped explaining other people's code.** Three said what the server or goqite's runner
does with the panic afterwards; the worst,"The runner recovers a panicking job and logs it", reached
into `maragu.dev/goqite`'s internals from behind a type alias. They state the local constraint now.

**Test gaps.** Both reviewers ran mutations and found four which survived: wrapping error panic values
(the `exception.type` argument was undefended), blanking the status description, swapping `errors.Is`
for `==`, and deleting the event-name check in `ExceptionEventsWithStackTrace`. All four are now
caught. Added: `exception.type` and status-description assertions in all three packages, a panic
through the middleware with no chi route context at all, a stack-trace assertion on the jobs side, and
an identity comparison of the re-raised value instead of comparing rendered strings. A final sweep of
ten mutations over the finished code caught all ten.

**The abort test was reading as more than it proved.** It asserted no exception event with a stack
trace and looked like it said "nothing was recorded", while the SDK's own event was sitting on the
span. It now also asserts the total event count, with a comment saying which event that is, and
`ExceptionEventsWithStackTrace` documents that it cannot show a span carries no exception at all.

### Why

Both reviewers independently reached the same three conclusions -- share the recording, fix the
comments, close the mutation gaps -- which is a stronger signal than either one alone. The two
single-reviewer findings promoted (the `defer panic(v)` ordering and the `errors.Is` semantics) were
both cases where the code could silently produce exactly the failure the issue is about.

### What didn't work

Mutation testing with uncommitted work in the tree, using `git checkout <file>` to undo each mutation.
That reverts to `HEAD`, and `HEAD` was the pre-review commit, so it silently threw away every
uncommitted fix in `/http/otel.go`, `/otel/otel.go` and `/oteltest/oteltest.go` while leaving the test
files alone:

    FAIL	maragu.dev/glue/http	0.216s
    FAIL	maragu.dev/glue/jobs [build failed]

The fixes were in context and went back in by hand, and the second run used `cp` to a scratch file per
mutation instead. `git checkout` is not an undo when the thing being undone was never committed.

### What I learned

Two reviewers looking at the same diff with the same brief converge on the structural findings and
diverge on the tail, which is what makes the "only report what both found, unless it is serious" rule
work in practice: the consensus set was worth acting on without further thought, and the disjoint set
needed judgement one case at a time.

The mutation sweep is what separated real test coverage from apparent coverage. Every assertion added
in this step exists because a specific mutation survived, not because a line looked untested.

### What was tricky

Deciding to export `otel.RecordPanic` against `AGENTS.md`'s preference for a small API surface. What
tipped it was that the alternative was not "one copy" but "two copies of a subtle argument about SDK
behaviour which must be kept in sync", and the drift had already begun before either reviewer looked.

### What warrants review

The exported `otel.RecordPanic` is the one deliberate widening of the public API here, and the
judgement call most worth a second opinion.

The two-exception-events trade-off from Step 1 stands, now documented in one place instead of two.
The tests select this package's recording by the presence of a stack trace, so they neither assert
nor depend on the SDK's event, apart from the one abort test which counts events deliberately.

### Future work

Unchanged from Step 1: an attribute for deliberate aborts, and whether the SDK's own panic recording
should be switched off with `sdktrace.WithoutPanicRecording` where the provider is built -- which is
in the application, not in glue.
