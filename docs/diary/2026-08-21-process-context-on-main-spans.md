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
