# Diary: Process context on main spans

Goal: put process-level context on every main span, so a trace answers "what state was the
process in when this happened?" without leaving the tracing backend. Concretely: app uptime (which
makes restarts and crash loops visible), the Go runtime version, and the build timestamp.

glue already has a strong request-context and user-context story -- otelhttp basics, a thorough
user-agent breakdown, `http.route`, `enduser.pseudo.id`, trace-correlated logs. Measured against
the observability skill's wide-event catalog, the gap is almost entirely *operational* context:
nothing today says how long the process had been alive, what Go version it runs, or how stale the
running code is.

## Step 1: Scope and requirements (lead)

**Author:** main (lead)

### Prompt Context

**Verbatim prompt:** "Use your observability skill, I'd like to add some attributes to all root spans. Get an overview of what we're currently sending and compare it to the list from the skill. One thing I can think of already: app uptime in seconds. Makes it easy to see restarts."
**Interpretation:** Survey what glue emits on its unit-of-work spans today, compare against the observability skill's wide-event attribute catalog, and add the attributes worth adding -- starting with uptime.
**Inferred intent:** Close the operational-context gap so production incidents can be diagnosed from telemetry alone, without over-instrumenting or fighting glue's deliberate design choices.

### What I did

Loaded the observability skill and its `references/wide-event-attributes.md` catalog. Mapped every
span-creation site, attribute, and provider setting in glue. Checked how consuming applications use
glue in practice, and cross-checked the findings against live production telemetry rather than
trusting the code read alone.

Confirmed scope with Markus across four rounds of questions, which cut the candidate list down
considerably. Created worktree `main-span-process-attributes` and seeded this diary.

### Why

Several attributes that looked like gaps in the code turned out to be either already present or
useless in practice, and only real telemetry showed which was which. Guessing would have shipped
redundant or empty attributes.

### What worked

Checking real telemetry was decisive and repeatedly overruled the code read:

- **`host.name` already exists** on every span, set by otelconfig's `resource.WithHost()`. Under
  Docker it resolves to the container ID, so it already changes on every restart. A proposed
  `service.instance.id` would have been pure duplication.
- **`service.version` stamping is fragile.** It depends on `vcs.revision` surviving into the build,
  which in turn depends on the Docker build context carrying git metadata. It is easy to have this
  silently degrade to `"unknown"` without noticing.
- **No background job span is a trace root.** Job spans are children of whatever enqueued them,
  because `jobs.Create` propagates trace context through the goqite message envelope.
- **HTTP main spans are not always roots either.** Any client that sends `traceparent` makes the
  server span a child.

Together those two facts killed the original framing. The prompt asked for attributes on "all root
spans"; the correct predicate in glue is `main = true`, not root-ness. A root-based implementation
would have silently skipped every background job.

### What didn't work

My first attribute shortlist was rejected wholesale, and deservedly -- three of the four options
were wrong:

- `http.request.body.size` / `http.response.body.size` were already supplied by otelhttp v0.65.0.
  I proposed adding what was already there.
- `deployment.environment` is unnecessary here, because the dimension is already carried outside
  the span data rather than on it.
- A runtime-metrics snapshot and per-request DB query rollups both wanted background samplers or
  context plumbing -- too much machinery for the value.

The lesson is that I proposed from the catalog before checking what was actually being emitted.

### What I learned

A session ID looked appealing and is a dead end here. glue's session setup means anonymous visitors
never get a session cookie: `IdleTimeout` is unset, so scs leaves an unmodified session alone and
writes no cookie, and CSRF is stdlib `CrossOriginProtection` rather than session-backed. glue itself
has zero session write calls -- its session interfaces are read-and-destroy only. So a session ID
would be non-empty only for logged-in users, precisely the population `enduser.pseudo.id` already
covers, and `RenewToken` rotates the value at login, breaking the one correlation you would want.
It is also a long-lived credential, which does not belong in telemetry.

`vcs.time` and `vcs.revision` share a fate: both need git metadata in the build context, so build
time is present exactly where the SHA is. `debug.ReadBuildInfo().GoVersion` needs no VCS at all and
is always populated. That asymmetry drives the acceptance criteria.

### What was tricky

`main = true` is doing double duty, and Markus caught it. A single trace can already hold two main
spans: when a job is enqueued from inside an HTTP request, the job span becomes a child of the
request span, and both are marked main. So `COUNT` where `main = true` counts units of work, not
traces, and mixes requests with jobs in a bucket where `http.route` is null for half the rows.
Retries compound it -- goqite retries produce one main span per attempt.

We discussed narrowing `main` to mean trace root, and rejected it: that would strip the attribute
from every job, which is exactly where uptime matters most. Decision is to keep "unit of work" as
the meaning and leave semantics untouched, making this round purely additive.

### What warrants review

The requirements below. A reviewer should confirm that the helper covers both glue main-span sites
without an import cycle, and that `uptime_sec_log_10` cannot emit `-Inf` or `NaN` for a process
younger than one second.

### Future work

Three threads deliberately left out of this round:

- **`url.query.<key>` mints unbounded attribute keys.** Each distinct query parameter name becomes
  a permanent column in the backend, so any bot probing new parameter names pollutes the schema
  forever. Left for now.
- **`enduser.pseudo.id` is absent whenever identity does not arrive via the session**, since
  `Authenticate` only reads the session. Applications that authenticate by bearer token get main
  spans with no user attribution.
- **Consumer applications have their own units of work** -- schedulers, tickers, CLI entry points --
  which glue cannot reach. They will need to call the exported helper to get the same treatment.

## Step 2: Implementation

**Author:** builder (sub-agent)

### Prompt Context

**Verbatim prompt:** the lead handed over a written requirements brief covering four requirements
(a shared main-span helper package, uptime attributes with guarded logarithm maths, resource
attributes at startup, and closing the `enduser.permissions` gap), eleven acceptance criteria, and
an explicit out-of-scope list. Mid-run the lead sent one correction: glue is public open source
while the applications that motivated the work are not, so no detail of them may land in this repo.
**Interpretation:** build all four requirements test-first, keep the change purely additive to span
semantics, and motivate everything in generic terms.
**Inferred intent:** ship the operational context the lead scoped out, with the guard rails written
down as tests rather than as prose, so the next person can trust the attributes in a query.

### What I did

Built it in four red/green passes, each starting from a failing test.

The shared helper is a new package at `/otel`, exporting one function:

    func MainSpanAttributes() []attribute.KeyValue

returning `main` (bool), `uptime_sec` (int64) and `uptime_sec_log_10` (float64). A slice rather
than a `trace.Span` parameter, because `/jobs/runner.go` needs to pass the attributes into
`trace.WithAttributes` at span start while `/http/otel.go` sets them on a span that is already
running. One function serves both. The package imports only `math`, `time` and
`go.opentelemetry.io/otel/attribute`, so nothing can cycle.

Inside, `MainSpanAttributes` is a thin wrapper over an unexported `uptimeAttributes(time.Duration)`,
which is where all the maths lives. Splitting it that way is what makes the guard testable without
mutating global state or racing the real clock: the table in
`/otel/otel_internal_test.go` feeds it durations directly, including zero, 999ms, a negative hour,
and the largest value a `time.Duration` can hold. A package-level `processStart` is still there for
`MainSpanAttributes` itself to measure against, and one internal test pins it to prove the wiring.

Both existing main-span sites now call the helper, and the literal `"main"` survives in exactly one
place in non-test code, which `grep -rn '"main"' . --include='*.go' | grep -v '_test.go'` confirms.

In `/app/app.go`, `getVersion` became `getVersionAndBuildTime`, which delegates to a pure
`versionAndBuildTime([]debug.BuildSetting)` that reads `vcs.revision` and `vcs.time` in one pass.
The otelconfig call now builds its option slice so that `otelResourceOptions(buildTime)` can
contribute `resource.WithProcessRuntimeName()`, `resource.WithProcessRuntimeVersion()`, and --
only when the build time is non-empty -- `service.approx_build_time`. `start` grew a `buildTime`
parameter; it is unexported, so no public API changed.

`/http/auth.go` gained `setPermissionsOnRootSpan`, called from both `Authorize` and
`SavePermissionsInContext`.

### Why

The helper is exported because a unit of work is not a glue-only concept: applications run
schedulers, tickers and CLI entry points that glue never sees, and those spans need the same
attributes or they vanish from any query filtered on main spans. Returning a slice keeps that
usable at both span start and mid-span without the caller thinking about which.

`service.approx_build_time` is omitted rather than defaulted because a placeholder in a timestamp column
is worse than a gap: a gap is queryable as absent, whereas `"unknown"` silently poisons any
comparison. The version keeps its existing `"unknown"` fallback, because changing that was out of
scope, but the pattern was deliberately not copied to the build time.

The permissions helper exists because an attribute whose presence depends on route wiring cannot be
trusted. Before this, a route using `SavePermissionsInContext` without an `Authorize` group carried
`enduser.pseudo.id` and no permissions, and nothing in the data said why.

### What worked

Test-first paid off immediately on requirement 4. The new table test failed on exactly one of its
two middlewares and passed on the other, which is the gap stated as an executable fact:

    --- FAIL: TestPermissionsOnRootSpan/SavePermissionsInContext_should_set_the_permissions_on_the_root_span
        auth_test.go:351: Not true
        auth_test.go:351: expected enduser.permissions on the root span

Testing `otelResourceOptions` by feeding it to `resource.New` and reading `.Attributes()` back
turned out much stronger than counting options. It proves the real attribute keys and values, and
it is what caught that `resource.WithProcessRuntimeVersion()` emits `go1.26.5` -- with the `go`
prefix -- rather than a bare version number.

Running the affected packages under `-race` and repeatedly under `-shuffle on` found no ordering
problems, despite `oteltest` installing a global tracer provider.

### What didn't work

Two self-inflicted compile errors, both caught in seconds.

Writing the internal test before the code, I reached for a local `hasAttribute` helper and a
`fmt.Sprint` without the import, then replaced the helper with `oteltest.HasAttribute` -- which is
what the acceptance criteria asked for anyway, and which `/otel` can import because `oteltest` does
not import `/otel`.

Adding the maximum-duration case, an untyped constant expression tripped the type checker:

    otel/otel_internal_test.go:30:141: cannot use math.MaxInt64 / int64(time.Second)
    (constant 9223372036 of type int64) as float64 value in argument to math.Log10

Fixed by writing the literal `9223372036` in both columns.

One shell mishap worth recording: escaping `2>&1` inside a `sed` invocation created a junk file
literally named `&1` in the worktree root. Spotted in `git status --short` and removed before
committing.

### What I learned

`otelconfig.WithResourceOption` appends rather than overwrites, so calling it several times is
correct; that is not obvious from the signature, which takes a single `resource.Option`. More
importantly, `newResource` seeds the resource with its own semconv schema URL (v1.26.0) while the
SDK's detectors carry a newer one (v1.40.0), so merging them raises `resource.ErrSchemaURLConflict`
-- and otelconfig explicitly swallows that error. The condition already existed via
`resource.WithHost()`, and the new detectors come from the same SDK package, so they contribute no
new schema URL and change nothing. `TestStart` covers this from the outside: it calls
`ConfigureOpenTelemetry` for real and asserts no error.

On the attribute encoding: `attribute.StringSlice` with a nil slice and with an empty slice produce
identical values -- the internal `SliceValue` returns `[0]T{}` for both -- so rewriting the
permission loop from `var permissionStrings []string` to `make([]string, 0, len(permissions))`
changes nothing on the wire. Worth knowing before assuming a refactor like that is free.

### What was tricky

The uptime guard has two failure modes, not one, and the obvious implementation hits both.
`math.Log10(0)` is `-Inf` and `math.Log10` of a negative is `NaN`; either would reach the exporter
as a float attribute and quietly break any aggregation over the column. Clamping the seconds at
zero and then only taking the logarithm above zero handles both, and the table names each case so
the intent survives a future refactor.

Computing the seconds as `int64(uptime / time.Second)` rather than `int64(uptime.Seconds())` avoids
a float round trip entirely. Both truncate toward zero and agree on every case, but the integer
form has no precision story to reason about.

I first put the HTTP call where the old `main` attribute already was, at the bottom of the
middleware, on the assumption that it had to live there. It did not: only `SetName` and
`HTTPRoute` need the chi route pattern, and everything else about a main span is known on entry.
Keeping it at the bottom left an HTTP main span reporting uptime as of the end of the request while
a job span reported it as of the start, and it inherited a worse problem, which the self-review
below picks up.

The tracer in `jobs.WithTracing` is captured when the handler is created, not when it runs, so the
new test has to build the handler after `oteltest.NewSpanRecorder(t)` installs the recorder. That
is written down in a comment at the call site, because getting it backwards produces a test that
records nothing and fails in a confusing way.

### Self-review

Two reviewers went over the diff independently. They agreed on one real bug, and it was one I had
walked straight past.

**The main-span attributes were set after `next.ServeHTTP`.** glue's router installs no recovery
middleware -- `Compress`, `RealIP`, `OpenTelemetry`, `CrossOriginProtection`, and nothing else --
while otelhttp ends the span in a `defer`. So a handler panic unwound past the bottom of the
middleware and the span was still exported, carrying no `main`, no `http.route`, and no name. The
one unit of work you most want to count fell out of every query filtered on main spans. That was
already true for `main` before this change, but this change rewrote that exact line and inherited
it, and it also made the uptime an end-of-request reading while the jobs runner takes it at span
start -- the same attribute name meaning two different instants.

Both problems go away by moving one call to the top of the middleware, which is where it now sits.
Nothing in `MainSpanAttributes` needs anything from the request; only `SetName` and `HTTPRoute`
have to wait for chi to resolve the route. A test pins it, and it failed before the move:

    --- FAIL: TestOpenTelemetry/sets_main_span_attributes_even_when_the_handler_panics
        otel_test.go:88: Not true
        otel_test.go:88: expected main attribute

This is the one place where the change is not purely additive: a panicking request now carries
`main = true` where before it carried nothing. That is a fix rather than a semantic change -- the
meaning of `main` is untouched -- but it is a behaviour change and it warrants a second opinion.

The other agreed findings were smaller and are all addressed:

- `MainSpanAttributes` built its slice with a composite literal plus `append`, which happened to
  yield `len == cap` only because three `attribute.KeyValue` values land on an exact allocator size
  class. A caller's own `append` could have written into spare capacity on another toolchain. It is
  now sized explicitly, and a test asserts `len == cap` rather than trusting the allocator.
- `setPermissionsOnRootSpan`'s doc comment justified itself against the old behaviour and made a
  claim about its callers that nothing enforces. It now states what it sets and stops there. The
  same rewrite fixed a dangling "one" that appeared to refer to the request rather than the span.
- The helper's edge cases were untested: an empty permission list, and a root span present but no
  longer recording. Both are covered now, for both middlewares. The empty-list case matters because
  the refactor changed a nil slice to an empty one -- which turns out to be identical on the wire,
  but was worth pinning rather than assuming.
- `TestProcessStart` checked `uptime_sec` but not that `uptime_sec_log_10` was the logarithm of the
  seconds actually emitted, which is the consistency property the whole design rests on.
- `endedSpanNamed` duplicated the existing `lastEndedSpan` in the same test package with a different
  signature. It moved next to it, took the same interface parameter, and now reports which span
  names were recorded when it fails.
- The comment on `processStart` claimed accuracy "within milliseconds of process start", which the
  package cannot know: initialisation order is dependency order, and an importing program may do
  heavy work first. It now says only what is true.

Three things the reviewers raised that I deliberately did not change, because they are decisions
above my pay grade rather than defects:

- **`service.build.time` carried the commit time, not the compile time.** Go documents `vcs.time`
  as the modification time of `vcs.revision`, so rebuilding an old commit today reports the old
  date. One reviewer also noted that `service.*` is an OpenTelemetry-reserved namespace and this key
  is not a registered convention. I raised both rather than renaming a key the requirements named,
  and Markus ruled: the attribute is now `service.approx_build_time`. It stays in `service.*`
  deliberately, so it groups with `service.name` and `service.version` where a reader will look for
  it, and a key like `approx_build_time` is not going to collide with a future convention. Since the
  name is still not literally precise, the doc comment carries the precision instead -- what the
  value is, where it comes from, and how far it can drift from the real build time -- written for
  someone reading the field mid-incident with no context.
- **`uptime_sec_log_10 == 0` covers everything up to and including one second.** That is forced by
  the "never `-Inf`" requirement, and the sub-second bucket is exactly the crash-loop case. The
  `uptimeAttributes` doc now says so explicitly so nobody misreads bucket zero.
- **`jobs/runner_test.go` sets a global tracer provider and propagator with no cleanup**, which
  leaks into every other test in that package under `-shuffle on`. Pre-existing, and fixing it means
  touching shared setup that four unrelated subtests depend on.

### What warrants review

The `/otel` package name. It shadows `go.opentelemetry.io/otel`, so any file importing both needs
an alias; `/jobs/runner.go` does, and `glueotel` is used in both call sites for consistency with the
existing `gluehttp` convention. Alternatives considered were `telemetry` and `spans`; `otel` won
because it sits next to the existing `oteltest` package and reads as its non-test sibling.

Whether `service.approx_build_time` belongs on the resource rather than the span. It is constant for the
process lifetime, which is what resources are for, and it rides along on every span without costing
per-span work.

The exported doc comment on `MainSpanAttributes` -- it is the only public documentation of the
convention, so it should read well to someone who has never seen this repo.

### Future work

Nothing new fell out beyond what Step 1 already lists, other than a second data point for issue
#189. Two helpers had to be hand-rolled again: `endedSpanNamed` in `/http/auth_test.go`, which is
exactly the span-by-name helper that issue proposes, and `attributeValue` in `/otel/otel_test.go`,
which reads one attribute's value out so the test can assert on its type and range rather than on
an exact value it cannot know in advance. Both belong in `oteltest` if the issue is picked up.

The exported helper is now in place for applications to call from their own units of work.

## Step 3: Review round on PR #194

**Author:** builder (sub-agent)

### Prompt Context

**Verbatim prompt:** Markus left six inline comments on PR #194, all triaged and resolved with
replies; the coordinator relayed them as one batch to apply -- four on `/app/app.go`, one
flagging something not to introduce, and one on `/otel/otel.go`, plus a seventh point folding the
schema-URL discussion out of the code and into this diary.
**Interpretation:** apply all of it as one commit, including the two rulings that reverse decisions
from the original requirements.
**Inferred intent:** cut the indirection and the over-explaining I had built up, and make the two
telemetry fields answer the question you actually ask them at three in the morning.

### What I did

`otelResourceOptions` is gone. Its two detectors and the build-time attribute now sit in the
`otelconfig.ConfigureOpenTelemetry` call directly, which means `start` has a single static argument
list again with no slice building and no `for` loop. `versionAndBuildTime` is gone too, folded back
into `getVersionAndBuildTime` as one function with one name.

`service.approx_build_time` now falls back to `"unknown"` instead of being omitted. That reverses
the original requirement, deliberately. `service.version` already reports `"unknown"` for a missing
`vcs.revision`, and the two stamps fail together for the same reason -- no VCS metadata in the build
context -- so they should look the same when they fail. An absent column cannot distinguish "this
build had no VCS stamp" from "this build predates the attribute existing"; `"unknown"` says the code
ran, looked, and found nothing. Because the value is now never empty, the conditional around the
attribute went away with everything else.

The eight-line doc comment on that attribute is down to the two lines the code cannot say for
itself: that the value is the `vcs.time` build setting, that it is the commit timestamp rather than
the build timestamp, and therefore that it reads as "the code is at least this old".

`uptime_sec_log_10` is now an `attribute.Int64` holding the floor of the logarithm rather than a
float holding the logarithm itself. Full precision is already on `uptime_sec` right beside it, so
the field's only job is bucketing, and floored it is an order-of-magnitude bucket you can group by:
0 is under ten seconds, 1 is ten seconds to under two minutes, 2 is up to about seventeen minutes.
Crash-loop detection becomes one query rather than a heatmap you have to look at.

`MainSpanAttributes` went back to a composite literal plus `append`. The exact-sizing `make` I had
added in the last round was justified by a comment that was simply wrong: `append` on a slice with
no spare capacity reallocates regardless of how the slice was built, so the plain version has the
same property and two fewer lines.

The startup log line was flagged to stay as it is -- `name` and `version` only, no build time -- and
it does.

### Why

Every one of these is the same correction: I had been paying for flexibility and explanation that
nothing needed. A helper called from one place, a pure function split out of a function that was
already pure enough, a float carrying precision that its neighbour already carries, and a paragraph
of provenance in a doc comment. The reviewer read the code as a reader rather than as its author,
and the reader wanted less.

### What worked

The int conversion has a sharp edge I went looking for before trusting it. `int64(math.Log10(x))`
truncates, so every bucket boundary is an exact power of ten, and exact powers of ten are precisely
where `math.Log10` can land a hair low -- `2.9999999999999996` for 1000 would floor to 2 and put a
sixteen-minute-old process in the wrong bucket. I pinned 9, 10, 99, 100, 999, 1000, 10000, 100000
and 1000000 as table cases before running anything. All nine pass on this toolchain, and they are
now nailed down so a future change to that line cannot quietly move a boundary.

### What didn't work

Nothing failed. The one thing that did not survive contact with the tests is coverage: `app` fell
from 67.5% to 48.4% of statements. `TestOtelResourceOptions` and `TestVersionAndBuildTime` both
tested functions that no longer exist, so both were deleted, and the resource options they asserted
on are now inline in `start`, which no test can inspect -- `ConfigureOpenTelemetry` returns only a
shutdown function, never the resource it built. What remains is `TestStart` calling it for real and
expecting no error, which still catches a detector that stops constructing, and a rewritten
`TestGetVersionAndBuildTime` asserting neither return value is ever empty, which is the actual
contract now that both fall back to `"unknown"`.

That is a real reduction in what the tests can see, and it follows from removing the seam rather
than from any oversight. Worth knowing rather than worth reversing.

### What I learned

The schema-URL detail came out of the code and belongs here instead. `otelconfig` pins semantic
conventions v1.26.0 on the resource it builds, while the `resource.WithProcessRuntimeName()` and
`resource.WithProcessRuntimeVersion()` detectors carry a newer version, so merging them returns
`resource.ErrSchemaURLConflict` and the merged resource ends up with an empty schema URL. Every
attribute still merges through, and nothing in this setup consumes the schema URL, so the effect is
nil. The condition also predates this change -- `resource.WithHost()` is an SDK detector too and has
always been in that call -- and `TestStart` configures OpenTelemetry for real, so a regression would
fail the build. It was true and load-bearing for me while I was writing the code, and noise for
everyone reading it afterwards.

### What was tricky

Nothing was tricky mechanically. The judgement call was the `"unknown"` fallback, because it
reverses a requirement I had implemented and defended twice, once in the original round and once
under review. The reasoning that flipped it is that consistency between two fields that fail
together beats per-field purity: a reader comparing `service.version` and
`service.approx_build_time` sees one story rather than two conventions, and "absent" was carrying
two meanings it could not tell apart.

### What warrants review

Whether losing the assertion that `process.runtime.name` and `process.runtime.version` actually
reach the resource is acceptable. It was the strongest thing `TestOtelResourceOptions` did, and
inlining the options removed the only place a test could stand.

### Future work

Nothing new.

## Step 4: Replace the per-parameter query attributes

**Author:** builder (sub-agent)

### Prompt Context

**Verbatim prompt:** after a research pass on query-parameter capture, the coordinator relayed one
more change for PR #194: delete the block minting one attribute per query parameter and replace it
with the standard single `url.query` string from `r.URL.RawQuery`, set near the top of the
middleware, with no sanitization or redaction.
**Interpretation:** adopt the semantic convention, drop the bespoke shape, and place it where a
panicking handler cannot lose it.
**Inferred intent:** stop instrumentation code from inventing column names, which is a schema
problem and a security problem before it is a correctness one.

### What I did

Deleted the loop over `r.URL.Query()` in `/http/otel.go` and replaced it with a single
`semconv.URLQuery(r.URL.RawQuery)`, guarded on a non-empty query because semantic conventions mark
`url.query` conditionally required -- present if and only if a query was received, never an empty
string. It sits next to the `MainSpanAttributes` call at the top of the middleware, since
`RawQuery` needs no routing and there is no reason for it to be lost when a handler panics.

No sanitization, no allowlist, no denylist, no configuration. That was considered and declined.

### Why

Three reasons, none of them mine.

`url.query.<key>` has no basis in OpenTelemetry. The URL registry defines thirteen `url.*`
attributes and not one of them is templated per parameter. `url.query` is a single string, Stable,
and conditionally required on HTTP server spans, so glue was simultaneously emitting a shape that
does not exist and omitting the one that does.

Honeycomb's guidance on organizing data says not to set field names dynamically from instrumentation
code, warns that it leads to runaway schemas and column-creation throttling, and calls sending
unsanitized user input as a field name particularly dangerous. Live production bore that out: 338
columns, roughly 272 of them `url.query.*`, including column names containing spaces, backslashes
and invalid UTF-8 from remote-code-execution probe strings. Every bot probing a new parameter name
was minting a permanent column.

No mainstream instrumentation decomposes the query. otelhttp captures none of it; the Java and
Python implementations capture a single string.

### What worked

The attribute-limit claim came to me flagged as inference from a documented limit rather than
something verified, with a suggestion to test it if cheap. It was cheap, and it is real.

Writing the test against the old code first, a request with 200 query parameters produced:

    --- FAIL: TestOpenTelemetry/keeps_the_main_span_attributes_when_the_request_carries_a_flood_of_query_parameters
        otel_test.go:112: Not true
        otel_test.go:112: expected http.route attribute

So the exposure was genuine and remotely triggerable by anyone able to put parameters in a URL. The
Go SDK caps a span at 128 attributes by default, otelconfig does not raise it, and the main span
already carries thirty to forty attributes before any query. Past the cap the SDK drops what arrives
last, which is why `http.route` -- set at the bottom of the middleware after routing -- went first.

`main` survived, and only by luck of timing: it survived because the previous review round had
already moved it to the top of the middleware to fix the panic case. Before that move it was set two
lines above `http.route` and would have gone the same way, taking the request out of every query
that filters on main spans. Two independent fixes turned out to protect the same attribute.

The test is kept, and now passes for the real reason rather than the accidental one: with a single
`url.query` attribute the count cannot grow with the request at all.

### What didn't work

Nothing failed. The change is a deletion plus three lines.

### What I learned

The old code had a second defect nobody had gone looking for. It lowercased the parameter name to
build the attribute key, but query strings are case-sensitive, so `?Q=a&q=b` produced two attributes
both named `url.query.q`, and `SetAttributes` is last-write-wins. One of the two values was silently
dropped, with nothing anywhere to say which. The replacement cannot have that problem, since
`RawQuery` is the query exactly as it arrived, and the new test pins it with a `q` and a `Q` in the
same request.

### What was tricky

Only the placement question, and it answers itself once asked: everything the attribute needs is on
the request at entry, so the only argument for setting it late was that the code it replaced
happened to live there.

### What warrants review

That no redaction is a deliberate, current-backend-specific call rather than an oversight. A
password or token in a query string now lands in telemetry as one string instead of as one
column, which is better for the schema and no better for the secret.

### Future work

This retires the first bullet in Step 1's future work, which listed `url.query.<key>` as knowingly
deferred. It is done, and the underlying problem -- instrumentation minting column names from user
input -- cannot recur here.

Whether `url.query` should be redacted before export is now the open question in its place. It was
declined for this round on the grounds that the values are acceptable in this backend, which is a
statement about today's backend rather than about the data.

## Step 5: Name unmatched spans after the method alone

**Author:** builder (sub-agent)

### Prompt Context

**Verbatim prompt:** Markus wants the `"GET "` span name fixed -- when nothing matches,
`RoutePattern()` returns `""`, so the span is named with a trailing space and `http.route` is set to
the empty string. Both are visible in production, where `GET ` is one of the highest-volume span
names, with `POST ` and `HEAD ` variants. Confirm the requirement-level wording against the pinned
semconv v1.34.0 rather than the paraphrase, and check whether `chi.RouteContext(ctx).RoutePattern()`
panics when there is no chi route context, since `OpenTelemetry` is exported and a consuming app can
apply it to a non-chi handler.
**Interpretation:** guard the empty route, and settle the nil question by reading rather than
assuming.
**Inferred intent:** stop emitting two values that look real in a query but are not.

### What I did

At the bottom of the middleware in `/http/otel.go`, the name and the attribute are now set only
when there is a route, and the span falls back to the bare method when there is not.

Both halves check out against the pinned spec. From the v1.34.0 HTTP spans document, `http.route` on
a server span is:

> Conditionally Required If and only if it's available

and on naming:

> HTTP span names SHOULD be `{method} {target}` if there is a (low-cardinality) `target` available.
> If there is no (low-cardinality) `{target}` available, HTTP span names SHOULD be `{method}`.

So the unmatched case is named `GET`, and `http.route` is absent rather than empty. An empty-string
route is worse than a missing one precisely because it survives a `GROUP BY` looking like a value.

### Why

An attribute that is present but meaningless is a trap for whoever queries it next, and a span name
with a trailing space is the same trap in the name field. Neither is a display problem: they are
both values that a query cannot distinguish from real ones.

### What worked

Writing the tests first caught both symptoms in one run, against the old code:

    otel_test.go:142: Expected "GET", but got "GET " (type string)
    otel_test.go:154: Expected "POST", but got "POST " (type string)

The tests assert the exact string rather than a prefix or a contains, so a trailing space fails.
They also assert the absence of `http.route` with `oteltest.HasAttributeKey`, which is the only way
to tell "absent" from "present and empty" -- `HasAttribute` with an empty value would pass in both
cases and prove nothing.

### What didn't work

Nothing failed beyond the two expected reds.

### What I learned

**The nil route context does not panic, and no guard is needed.** chi's `RouteContext` returns a
typed nil `*chi.Context` when the key is absent, and `RoutePattern` is declared on the pointer
receiver with an explicit nil check as its first statement:

    func (x *Context) RoutePattern() string {
        if x == nil {
            return ""
        }

So a handler wrapped in `OpenTelemetry` outside a chi router gets `""` back, which is exactly the
unmatched-route case and now takes the same branch. The second test pins that: it applies the
exported middleware straight to an `http.HandlerFunc` with no router at all, and before the fix it
returned `"POST "` rather than panicking -- which confirms the read empirically as well. Nothing was
changed for it.

### What was tricky

Nothing. The only real work was resisting the urge to write a nil guard that chi already has.

### What warrants review

The effect on existing data. Span names `GET `, `POST ` and `HEAD ` collapse to `GET`, `POST` and
`HEAD`, so anything that groups or filters on the old names with the trailing space stops matching.
Those spans also lose their empty `http.route`. Nothing is lost for debugging: `url.path` still
carries the actual path that was requested, which is what you want for a 404 or a bot probe anyway,
and it is the high-cardinality value that never belonged in the span name.

### Future work

Nothing new.

## Step 6: Update semantic conventions to v1.43.0

**Author:** builder (sub-agent)

### Prompt Context

**Verbatim prompt:** bring the semantic conventions up to date. glue imports
`go.opentelemetry.io/otel/semconv/v1.34.0`; update to the newest available. Two steps, both
required: bump the `go.opentelemetry.io/otel` module from v1.44.0 to v1.45.0, then switch every
import to the newest semconv package that module ships, because bumping the module alone changes
nothing -- the old package still exists and still compiles, so skipping the second step fails
silently. Verify every helper still exists and still means the same thing; report deprecations
rather than migrating on my own initiative; do not change which attributes glue emits.

### What I did

Bumped the module and moved all seven files from `semconv/v1.34.0` to `semconv/v1.43.0`: the five
non-test files -- `/http/otel.go`, `/http/auth.go`, `/sql/helper.go`, `/s3/bucket.go`,
`/email/postmark/postmark.go` -- plus `/http/otel_test.go` and `/s3/bucket_test.go`, which the
handed-over list did not mention and which a grep found.

otel v1.45.0 ships semconv packages up to **v1.43.0**, not v1.41.0 as v1.44.0 did, so checking
rather than assuming was worth the one command it cost.

### Why

The versioning here is genuinely confusing and worth writing down. The semconv packages are
versioned by *semantic-convention* version, not by module version, and a single
`go.opentelemetry.io/otel` release ships several dozen of them side by side as separate packages.
otel v1.45.0 carries everything from `semconv/v1.4.0` to `semconv/v1.43.0` in the same module. So
the old import path keeps compiling forever after a module bump, and a version bump that only edits
`go.mod` looks successful while changing nothing at all.

### What worked

Rather than trusting a compile pass, which only proves the identifiers exist, I checked the actual
emitted attributes. Because both semconv versions ship inside the same module, a throwaway program
could import them side by side and compare every helper glue calls, key, value and type:

    ok  EnduserPseudoID   enduser.pseudo.id=u_1 (STRING)
    ok  HTTPRoute         http.route=/x/{id} (STRING)
    ok  DBSystemNameSQLite  db.system.name=sqlite (STRING)

All nineteen came back identical across the nine-version gap -- same key, same value, same type.
**Nothing glue emits changes**, so no backend column moves. A separate pass over the v1.43.0 source
found no `Deprecated:` marker on any of them. The throwaway program was deleted afterwards.

The specific worries handed to me all came back clean. `EnduserPseudoID` still emits
`enduser.pseudo.id`, despite the churn elsewhere in that namespace. `BrowserMobile`,
`DeviceModelName`, `HTTPRoute`, `URLQuery`, `HTTPRequestBodySize` and `HTTPResponseBodySize` are
unchanged, as are both `db.system.name` values in `/sql/helper.go`.

The `rpc.system` trap flagged as an example does not apply: glue never calls it. `/s3/bucket.go`
uses only `AWSS3Bucket`, `AWSS3Key` and `CloudRegion`, all three of them fine.

### What didn't work

Nothing failed. No deprecation to report and no decision to escalate.

### What I learned

The dependency bump is contained. `go.opentelemetry.io/otel`, `otel/trace` and `otel/metric` go
v1.44.0 to v1.45.0, and `github.com/go-logr/logr` picks up a patch, v1.4.3 to v1.4.4, indirectly.
Nothing else moves.

`go.opentelemetry.io/otel/sdk` deliberately stays at v1.43.0. It is a separate module on its own
release line, `go mod tidy` did not pull it forward, and nothing needs it to move. Bumping the SDK
changes span and resource construction rather than attribute naming, so it is a different change
with a different risk profile and does not belong in a semconv update.

`otelhttp` pins its own semconv version internally and is untouched by any of this, so the two
versions coexisting in one binary is expected rather than a smell.

### What was tricky

Only the trap already called out: a module bump that compiles cleanly proves nothing, because the
old semconv package is still right there. The check that matters is whether the import paths
actually moved, which is a grep, and whether the emitted attributes actually match, which needed
the side-by-side comparison.

### What warrants review

Nothing beyond the diff. The claim worth checking is that no attribute changed, and the way to
check it is to re-run the same comparison rather than to read the code.

### Future work

`go.opentelemetry.io/otel/sdk` is a version behind the API modules. Worth its own bump sometime,
separately.

### Addendum: the SDK bump

Markus wanted `go.opentelemetry.io/otel/sdk` pulled into this PR rather than left as future work, so
it went from v1.43.0 to v1.45.0, with `otel/sdk/metric` moving alongside it as a pair. This one is
riskier than the semconv change, because the SDK builds spans and resources rather than naming
attributes, so a regression here is silent rather than a compile error.

**The resource attributes still arrive, and I checked rather than inferred.** The concern was the
known `resource.ErrSchemaURLConflict` interaction: otelconfig pins semconv v1.26.0 on its resource
while the `resource.WithProcessRuntime*()` detectors carry a newer one, and if the SDK's merge
behaviour had changed, `process.runtime.name`, `process.runtime.version` and
`service.approx_build_time` could have stopped arriving with everything still green.

It is worth being precise about why a green run would not have caught it. The `Start` tests assert
only that `ConfigureOpenTelemetry` returns no error, and the conflict is swallowed rather than
returned -- so a resource that quietly lost half its attributes would still pass them. The
assertion that would have caught it lived in `TestOtelResourceOptions`, which was deleted in Step 3
when the helper it tested was inlined, exactly the coverage loss flagged there under "What warrants
review".

So I built a throwaway program that ran the real `ConfigureOpenTelemetry` with the same options as
`start`, captured the `*otelconfig.Config` pointer through a plain `otelconfig.Option` -- the type
is `func(*Config)` and `Config.Resource` is exported, so the resource it built can be read back
afterwards -- and printed the result. Run before the bump and again after, the output was identical:

    schema URL: ""
    resource attributes (9):
      host.name = ...
      process.runtime.name = go
      process.runtime.version = go1.26.5
      service.approx_build_time = 2026-08-21T09:41:00Z
      service.name = test
      service.version = abc123
      telemetry.sdk.language = go
      telemetry.sdk.name = otelconfig
      telemetry.sdk.version = 1.17.0

Nine attributes both times, all six expected keys present, and the schema URL empty in both -- which
is the conflict being tolerated, visible directly rather than reasoned about. The program was
deleted afterwards.

The other three risks came back clean. `DefaultAttributeCountLimit` is still 128, and in any case
the flood test no longer depends on its value: with a single `url.query` attribute the span carries
the same thirty-odd attributes whether the request has two query parameters or two hundred, so the
test asserts a structural property rather than a numeric margin. `tracetest.SpanRecorder` is
unchanged in both API and behaviour, so `oteltest` and everything built on it is unaffected.
otelconfig was **not** forced forward -- it stays at v1.17.0, and the OTLP exporters stay at
v1.43.0 -- so the blast radius is no larger than asked for.

Beyond the two SDK modules, the only other movement is `golang.org/x/sys` v0.43.0 to v0.47.0,
indirectly.

One incidental find: `attribute.Value.Emit` is deprecated in this version in favour of
`Value.String`. glue does not call it anywhere -- the linter caught it in my throwaway program, not
in the codebase -- so there is nothing to do, but it is the kind of thing worth knowing before it
turns up in something that matters.

## Step 7: Raise the span attribute count limit to 512

**Author:** builder (sub-agent)

### Prompt Context

**Verbatim prompt:** raise the span attribute count limit from the SDK default of 128 to 512. The
default is the binding constraint on wide events -- glue's own main span carries 30-40 attributes
before an app adds anything, the user-agent block alone about 15 -- while the backend accepts 2,000
fields per event, so 512 is still conservative and the limit should be a runaway backstop rather
than a design constraint. The clean route would be a span limits option on the tracer provider, but
otelconfig may not expose one; confirm that before falling back to the environment variable, only
set it when unset so an operator wins, and comment why a library writes to the process environment.
**Interpretation:** find the supported route if there is one, otherwise take the documented
alternative and make the reason legible.
**Inferred intent:** stop a default nobody chose from deciding how wide an event can be.

### What I did

Confirmed there is no supported route through otelconfig, then added `raiseSpanAttributeCountLimit`
to `/app/app.go`, called at the top of `start` before `ConfigureOpenTelemetry`. It sets
`OTEL_SPAN_ATTRIBUTE_COUNT_LIMIT` to 512, and only when the operator has set neither that key nor
the general `OTEL_ATTRIBUTE_COUNT_LIMIT`. Only the attribute count moved; event count, link count,
per-event and per-link attribute counts and attribute value length all stay at their defaults.

**otelconfig genuinely has no option for this.** Its twenty-six `With*` options cover endpoints,
headers, protocols, resource options, propagators, samplers, span processors and shutdown, and
nothing else. More conclusively, `pipelines.NewTracePipeline` assembles the provider options itself
from `Config` -- `WithResource`, `WithSampler`, then the span processors -- and `Config` has no
field for limits, so there is nowhere for a caller to pass one even indirectly. The environment
variable is the only route.

### Why

The variable is not a hack around the SDK, it is the SDK's own configuration surface:
`trace.NewTracerProvider` calls `NewSpanLimits()` when it constructs the provider, which reads the
variable through `env.SpanAttributeCount`. Setting it before `ConfigureOpenTelemetry` means the
provider otelconfig builds picks it up exactly as if the option had been passed. It is written down
in the function's doc comment, because a library writing to the process environment deserves an
explanation at the point where a reader will ask for one.

### What worked

Checking both keys rather than one turned out to matter. The SDK resolves the limit with
`firstInt(default, SpanAttributeCountKey, AttributeCountKey)`, so the span-specific key beats the
general one. Had glue set only the specific key when only the specific key was unset, an operator
who had set the general `OTEL_ATTRIBUTE_COUNT_LIMIT` would have been silently overridden by the
framework -- precisely the precedence the requirement rules out. Checking both preserves it, and one
of the tests pins that case on its own.

Reading the SDK's notion of "unset" was worth the minute it took as well: `firstInt` skips a key
whose value is the empty string, so glue treats empty as unset too, and the two agree rather than
disagreeing at an edge.

### What didn't work

Nothing failed.

### What I learned

The test question the brief flagged -- not writing something that passes for the wrong reason -- had
a sharper answer than expected. The suspicion was that `oteltest`'s recorder would not pick up the
variable. It does, because it builds its provider with `sdktrace.NewTracerProvider`, which reads the
limit at construction like any other provider. So the trap was not that the test could not see the
effect; it was that a test could assert 300 attributes survive and pass whether or not glue had done
anything at all.

So I ran the control. With the raise removed and everything else identical:

    app_test.go:156: Expected "300", but got "128" (type int)

Exactly 128, truncated. With the raise, 300. The test therefore proves the mechanism has the effect,
not merely that the SDK can hold 300 attributes.

The tests split cleanly by what they actually establish. Three of them are about glue's own
behaviour and cannot pass for the wrong reason: the limit is raised when neither key is set, and the
operator's value survives whichever of the two keys it was set on. The fourth is about the mechanism
rather than about glue -- it pins that the variable glue writes still means what glue writes it for,
which is the failure mode a renamed or retired key would produce, where the first three would stay
green while the limit silently reverted to 128.

### What was tricky

The tests mutate process-wide state, and `start` now does too. Any subtest calling `start` leaves
the variable set behind it, and with `-shuffle on` that could have made the "raise it when unset"
case pass or fail depending on order. Every subtest therefore clears both keys with `t.Setenv`
first, which both removes a leaked value and registers the restore, so the cases are order
independent in either direction. Ran the suite under `-shuffle on` and the race detector to confirm.

### What warrants review

That 512 belongs in code rather than in deployment configuration. It is a framework default an
operator can override, which is the right shape, but it is still a number chosen once and compiled
in.

### Future work

This connects back to the flood test from Step 4. The 128 default was what let a query-parameter
flood evict `http.route` from the main span, and that is fixed at the source, since a single
`url.query` attribute cannot grow with the request. Raising the ceiling is defence in depth for the
same class of problem rather than the fix for it: it buys room for attributes that are supposed to
be there, and would not save a span from instrumentation that mints attributes without bound.

## Step 8: Replace four empty spans with timing attributes

**Author:** builder (sub-agent)

### Prompt Context

**Verbatim prompt:** four span types carry zero attributes beyond the common floor --
`http.Handler`, `http.Authenticate`, `http.Authorize` and `http.SavePermissionsInContext` -- each just
`Start`, `defer End`, call next. Together about 231,728 spans in 7 days in one production dataset,
with `http.Handler` the single highest-volume span name in the environment. Replace them with
`handler.duration_ms`, `authn.duration_ms`, `authz.duration_ms` and `permissions.duration_ms` on the
main span, as float64 milliseconds. Separately, there is not one `RecordError` call anywhere in the
`http` package, so auth failures reach a log line and a 500 and vanish from traces; start recording
them. Keep `TracingMux`. Watch for double-writes. Do not touch `adaptPage`.
**Interpretation:** trade timing bars in a waterfall for queryable attributes, and give glue's own
middleware errors somewhere to land.
**Inferred intent:** stop paying for span volume that answers no question, and close a gap where a
whole class of failure is invisible in traces.

### What I did

Deleted the four `tracer.Start`/`defer span.End()` pairs and added two unexported helpers in
`/http/otel.go` next to `GetRootSpanFromContext`: `setRootSpanDuration`, which writes a float64
millisecond attribute, and `recordErrorOnRootSpan`, which records the error and sets an error status.
Both guard with `!= nil && IsRecording()` like the existing callers, since `GetRootSpanFromContext`
returns a nil interface rather than a no-op span.

`TracingMux` keeps its type, its methods and its `chi.Router` conformance, and now times the handler
instead of wrapping it in a span. The three auth middlewares each time their own work.

`RecordError` and an error status now cover all four error branches in `/http/auth.go`: the two
session-destroy failures, the user lookup failure, and the permissions lookup failure in both
`Authorize` and `SavePermissionsInContext`.

### Why

A span should be interesting *and* aggregable, and grouping by `http.Authenticate` yields exactly
one bucket. Four span types with nothing on them but a name and a duration were buying waterfall bars
and nothing queryable, at roughly 42% of span volume. As attributes on the main span the same
information is not merely cheaper, it is more useful: you can now ask which routes have slow
authorization, or compare handler time against total request time, neither of which was expressible
when the numbers lived on separate spans.

On error recording, there is a distinction worth stating plainly because it looks inconsistent from
outside. Recording handler errors in `adaptPage` was proposed before and rejected: those errors
belong to the application, which already records them, and glue duplicating that would double-count.
The errors added here are glue's own, raised inside glue's own middleware, and they fail the request
*before* any application code runs. The application never sees them and cannot record them. So the
rule is not "glue does not record errors", it is "glue records the errors only glue can see".

### What worked

Splitting the timing out of the span turned out to sharpen what is being measured. A span wrapping
the middleware necessarily included everything downstream of it, because `defer span.End()` fires
after `next.ServeHTTP` returns -- so the old `http.Authenticate` span duration was essentially the
whole request. The attribute stops the clock before handing off, so it measures only the middleware's
own work, which is the number anyone actually wanted. The test proves it: the handler below sleeps
20ms and the assertion requires the recorded value to be under 10.

`handler.duration_ms` records in a `defer`, so a panicking handler is still measured, matching the
main-span treatment from Step 5.

### What didn't work

Nothing failed. Every pre-existing test in the package passed untouched after the change, which was
mildly surprising for a change this size and is explained by none of them having asserted on the four
span names in the first place.

### What I learned

**Double-writes: safe by construction on every path glue offers, with one caveat outside it.** Every
registration method on `TracingMux` calls `wrapHandlerFunc` exactly once. `Mount` and `Use` pass
their argument through untouched. `Group`, `Route` and `With` hand `fn` a fresh `TracingMux` whose
`mux` is the *plain chi* sub-router returned by the embedded call, not another `TracingMux`, so a
handler registered on a sub-router is wrapped once there and handed to chi. There is no path where
one `TracingMux` delegates to another. Six table cases pin this, including a mounted sub-router
asserting the attribute is *absent* because the sub-router owns its own handlers.

The caveat: `Handle` or `Method` with a `*TracingMux` as the handler, instead of `Mount`, would wrap
the sub-router's `ServeHTTP` on top of the wrapping its own leaves already have, and
`handler.duration_ms` would be written twice with the outer value winning. That is legal chi but
means bypassing `Mount` for something `Mount` exists to do. Reported rather than worked around.

For the auth middlewares a double-write is possible and it is the application's to avoid: applying
`Authenticate` on a parent router and again on a group runs both. Worth knowing exactly what that
produces, since it is not corruption. Each middleware writes before handing off, so the outer writes
first and the inner writes second, and last-write-wins leaves the *inner* invocation's value --
one middleware's own work, not a sum and not a total. A silently lost measurement rather than a wrong
one.

### What was tricky

**What became of the `tracer` field.** With no span to start, `TracingMux.tracer` had no reader, so it
is gone, along with `Server.tracer`, which turned out to have been assigned and never read at all.
The interesting part was the `t.tracer == nil` guard, which meant "tracing is not configured, so do
not wrap". It has no direct replacement, and that is the point: the question it asked at registration
time is now asked at request time and answered by whether the request carries a root span.
`setRootSpanDuration` returns without doing anything when there is none, so wrapping unconditionally
is safe and costs a `time.Now()` pair and a nil check when tracing is off. The exported surface is
unchanged, since the field was unexported and the type and all its methods remain.

The other subtlety was where to stop each clock. There is no single exit from these middlewares --
`Authenticate` alone has seven -- and a `defer` would have reintroduced exactly the downstream
inclusion the change is meant to remove. Each middleware therefore closes over a small `done()` which
is called at every exit, immediately before handing off or writing a response.

### What warrants review

The boundaries of each measurement, since a duration whose scope is unclear is worse than none.
`handler.duration_ms` covers the handler and everything it calls, and no middleware.
The three middleware attributes cover only that middleware's own work. None of them sums to
`duration_ms`, and the difference between them and the total is everything else, not any one thing.
This is written into the doc comments rather than left to be inferred.

### Future work

Nothing new.

## Step 9: Second review round on the timing attributes

**Author:** builder (sub-agent)

### Prompt Context

**Verbatim prompt:** five review comments, three of which need changes, applied as one batch. Replace
the `done()` call sites in all three auth middlewares with a stop-time defer, Markus's design: one
`defer` which stamps `time.Now()` when `stop` is still zero, and an explicit `stop = time.Now()`
before each handoff. Convert the sleeping duration tests to `testing/synctest`, where the fake clock
makes the middleware's own work measure exactly zero and a wrongly-included handler exactly 20.
Replace the middle paragraph of `wrapHandlerFunc`'s doc comment, which over-claims that middleware is
never included. Two comments resulted in no change.
**Interpretation:** one write per request instead of seven call sites, exact assertions instead of
loose bounds, and a comment that stops promising something the code cannot guarantee.
**Inferred intent:** remove three ways the timing code could silently drift wrong.

### What I did

All three middlewares now open with the same shape and have no `done()` anywhere:

    start := time.Now()
    var stop time.Time
    defer func() {
        if stop.IsZero() {
            stop = time.Now()
        }
        setRootSpanDuration(ctx, "authn.duration_ms", stop.Sub(start))
    }()

`stop = time.Now()` goes immediately before each handoff: four in `Authenticate`, one in `Authorize`,
two in `SavePermissionsInContext`. Every other path leaves it unset, and the defer stamps its own
firing time, which for a path that never hands off is exactly where the work ended.
`RedirectIfAuthenticated` also calls `next.ServeHTTP` and is not one of the three, so it was left
alone.

The doc comment paragraph is replaced verbatim with the agreed sentence, and the two comments that
resulted in no change were left as they are.

### Why

The old shape had seven `done()` calls in `Authenticate` alone, each one a place where a future edit
could add a return path and forget the call, losing the attribute silently for that path only. The
defer inverts the default: every path is measured, and the explicit `stop` marks the ones that must
not include what comes after. Forgetting a `stop` now over-measures visibly rather than dropping the
attribute invisibly, which is the better failure.

### What worked

`testing/synctest` played entirely nicely with the span recorder, which was the open question. The
whole subtest -- recorder, span, request, serve, assertions -- runs inside the bubble, and
`oteltest.NewSpanRecorder`'s `t.Cleanup` registered on the bubbled `T` behaves normally. Nothing had
to be forced. The recorder is a synchronous span processor with no goroutines or timers of its own,
which is presumably why there was nothing to trip over.

The payoff is larger than tightening a bound. The old assertion was "under 10, because the handler
slept 20" -- a real-clock heuristic with slack on both sides. In fake time the middleware never
sleeps, so its own work is exactly zero, and I could assert equality. The control run proves the
assertion bites: removing the `stop = time.Now()` before one handoff gives

    auth_test.go:633: Expected "0", but got "20" (type float64)

Exactly 20, because the handler's sleep is the only thing that moved the clock. A real clock could
never produce that: it would give 20-point-something against a threshold, and the test would be
measuring the machine as much as the code. The three subtests also stopped costing 20ms each.

### What didn't work

Nothing failed. The three changes were mechanical once the shape was agreed.

### What I learned

The over-claiming comment was worth fixing for a reason beyond accuracy. It said middleware "runs
outside it and is not included", which holds for middleware registered with `Use` but not for
middleware hand-composed into the registered handler -- `Handle("/x", mw(h))` times `mw` in full. The
replacement says what the measurement actually spans, in terms of the handler as registered rather
than in terms of what a reader might assume middleware means. The distinction only exists because
`TracingMux` wraps at registration and chi applies `Use` middleware outside that, which is not
obvious from the call site.

### What was tricky

Only one detail, and it is the reason the defer works at all: `stop` is captured by the closure, not
copied, so assigning it anywhere in the body is visible to the defer when it fires. Reading it as
"the defer sees whatever the last assignment left" is right, and it is also why a second assignment
on a later path would quietly win. There is exactly one assignment reachable per request in each
middleware, since every one is immediately followed by the handoff and a return.

### What warrants review

That each middleware's `stop` assignments are all immediately before a handoff and none is reachable
twice. That is what makes one write per request true, and it is a property of the control flow rather
than something the type system enforces.

### Future work

Nothing new.
