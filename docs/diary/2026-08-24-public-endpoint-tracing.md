# Diary: Stop honoring external trace parents in the OpenTelemetry HTTP middleware (issue 197)

The OpenTelemetry HTTP middleware in `/http/otel.go` wraps handlers with `otelhttp.NewHandler`, which extracts incoming `traceparent`/`tracestate` headers via the default propagator and parents server spans to remote spans we don't control. Investigation in Honeycomb (c6 dataset, production, 7 days) confirmed the problem: all 177 `POST /mcp` server spans had external `trace.parent_id` values (root_count = 0), producing "missing root span" traces. The caller (Anthropic's MCP client, user agent `Claude-User`) even smeared two MCP calls and an oauth token refresh into one caller-controlled trace ID. `POST /oauth/token` had the same issue.

The fix: add `otelhttp.WithPublicEndpoint()` to the middleware so every server span starts a new trace root, with the incoming remote context recorded as a span link instead of a parent. Unconditional, no config -- glue apps are server-side HTML with no trusted upstream. We keep the link because it comes for free with the option and enables caller correlation if ever needed; suppressing it would require hand-rolling the middleware for no gain. Global propagators stay untouched -- the jobs runner uses them for enqueue-time context through the queue.

## Step 1: Investigation and requirements

**Author:** main

### Prompt Context

**Verbatim prompt:** "See issue 197. Check Honeycomb in the c6 dataset for evidence, the MCP calls for fetch/search in there have external trace parents. I don't think we want that, since we don't control the caller. Should we disable all external traces? We don't have a separate frontend after all, it's all server-side HTML, so I don't see a need. Agree? Investigate." Followed by: "Fair, WithPublicEndpoint it is. Launch a builder and make a PR when done"

**Interpretation:** Verify the external-trace-parent problem with real telemetry, decide whether to stop accepting incoming trace context, then delegate the fix and open a PR.

**Inferred intent:** Server spans should always be trace roots in our own telemetry, since no caller of glue apps is trusted to control trace grouping or sampling.

### What I did

Read issue 197 via `gh issue view`. Queried Honeycomb (production/c6): `list_spans` showed `POST /mcp` with 177 spans, root_count 0; `get_span_details` confirmed all 177 have distinct `trace.parent_id` values, `span.kind` server, `span.num_links` 0, user agent `Claude-User`; `get_trace` on `0c9f3efcc606614199c1a62404ef4f03` showed a missing root with two `POST /mcp` requests and one `POST /oauth/token` glued under one external trace. An Explore sub-agent mapped the code: extraction happens implicitly in `otelhttp.NewHandler` at `/http/otel.go:22-23` with default propagators (TraceContext + Baggage from `otelconfig`, `/app/app.go:69-78`); no `WithPublicEndpoint`, `WithFilter`, or `trace.WithNewRoot` anywhere; the jobs runner (`/jobs/runner.go:49,115`) legitimately uses the global propagator for queue context.

### Why

Evidence before opinion: the issue hypothesized the problem, Honeycomb proved it, and the code survey ruled out collateral damage (jobs propagation is separate from HTTP extraction).

### What worked

`list_spans` root_count immediately surfaced the affected endpoints without hand-written queries. The sample trace was a perfect illustration of caller-controlled grouping.

### What didn't work

Two `run_query` attempts came back empty misleadingly: a relational filter `root.name does-not-exist` and a `trace.parent_id exists` filter with a `root.name` breakdown both returned "No results found" even though `get_span_details` proved 177 matching spans exist. Relational joins apparently don't behave as expected when the trace has no root span. Lesson: verify with span details before trusting empty relational-query results.

### What I learned

`otelhttp.WithPublicEndpoint()` does exactly what issue 197's optional suggestion asks: new root span plus a span link to the remote parent. Disabling propagators globally would be wrong -- it would also kill outbound injection and the jobs queue's enqueue-time context.

### What was tricky

Distinguishing "disable external traces" (the user's first framing) from the narrower correct fix: keep propagators, stop *parenting* to remote context in the HTTP server middleware only.

### What warrants review

Nothing code-wise yet; this step is investigation. The requirements handed to the builder are in this entry's preamble.

### Future work

The builder implements the change and opens a PR (next step).

## Step 2: Implementation, self-review, and PR

**Author:** public-endpoint

### Prompt Context

**Verbatim prompt:** "Implement the fix for GitHub issue 197 and open a PR. [...] 1. Add `otelhttp.WithPublicEndpoint()` to the `otelhttp.NewHandler` call in `http/otel.go`. Unconditional — no new config or options on the middleware. glue apps have no trusted upstream. 2. Do NOT touch global propagator setup (`app/app.go`, `otelconfig`) or the jobs runner (`jobs/runner.go`) [...] 3. Add/extend a test in the `http` package: send a request with a valid `traceparent` header through the middleware and assert that the resulting server span (a) is a trace root [...] and (b) carries a span link to the incoming remote span context. [...] 4. Update any doc comment on `OpenTelemetry` to state that incoming trace context is not used as parent but recorded as a span link."

**Interpretation:** Make the middleware start a new trace root instead of parenting to remote trace context, link the remote context, cover it with tests in the existing style, and document it.

**Inferred intent:** No client should decide how this service's traces are grouped or sampled, while correlation to the client's trace stays available.

### What I did

Red first: added the traceparent subtests to `/http/otel_test.go` and watched them fail (`--- FAIL [...] expected the server span to have no parent`, and `expected at least one ended span` for the unsampled case). Then added the public-endpoint option to `/http/otel.go` and a doc comment on `OpenTelemetry`.

The option is not the one the requirements name. `otelhttp` v0.65.0 has no `WithPublicEndpoint()` — `go doc` lists only `WithPublicEndpointFn(fn func(*http.Request) bool)`, and `grep PublicEndpoint` over the module in the cache confirms the plain form is gone. The predicate form was therefore the only shape available.

That opened a question the plain option would have hidden, which both self-review agents then raised independently: what should the predicate answer? Answering `true` for everything reproduces the old `WithPublicEndpoint`, and `trace.WithNewRoot` severs *any* parent in the context while `otelhttp` links only a parent which `IsRemote()` (handler.go:101-107 in the module cache). A span started above the middleware in the same process would be dropped with no link recording it. So the predicate is `trace.SpanContextFromContext(r.Context()).IsRemote()`: a remote parent becomes a root plus a link, an in-process parent stays a parent. `otelhttp` hands the predicate the already-extracted context, which is what makes this work. Recorded in `/docs/decisions.md`.

Tests in `/http/otel_test.go` now cover a sampled and an unsampled remote parent (new root, own sampling decision, one remote link, `tracestate` on the link and not on our span), three no-link cases (no header, malformed header, zero trace ID), and an in-process parent being kept. `useTraceContextPropagator` installs `propagation.TraceContext` globally, since the middleware reads the global propagator and the default extracts nothing.

`go test -shuffle on ./...` passes, as does `-count=5 -race` over `./http ./jobs ./otel ./oteltest`; `golangci-lint run ./http` reports 0 issues.

### Why

The requirements named an API, but the reason behind them was that clients must not steer our telemetry. Where the API had moved on, the reason still decided the shape.

### What worked

Writing the unsampled (`-00` trace flags) case as a table row alongside the sampled one. Under the default `ParentBased(AlwaysSample)` sampler it fails as `expected at least one ended span` without the fix, because the span is never recorded at all — the sampling half of the problem asserted by construction rather than by comment.

Flipping the predicate back to `return true` after the fact and rerunning confirmed the in-process-parent test is a real guard: `Expected "0467b34decb28078", but got "0000000000000000" (type trace.SpanID)`.

### What didn't work

`go doc go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp WithPublicEndpoint` → `doc: no symbol WithPublicEndpoint in package`, which is how the missing API surfaced.

The first version of `useTraceContextPropagator` saved `otel.GetTextMapPropagator()` and set it back on cleanup, and its comment claimed to restore the previous propagator. One review agent said that cannot work; a throwaway test in the package proved it:

```
before:                   type=*global.textMapPropagator valid=false remote=false
during:                   type=propagation.TraceContext  valid=true  remote=true
after restoring previous: type=*global.textMapPropagator valid=true  remote=true
after empty composite:    type=propagation.compositeTextMapPropagator valid=false remote=false
```

The default is a delegator, and setting a concrete propagator wires its delegate for good, so handing the delegator back leaves it delegating to `TraceContext` — every later test in the shuffled package would have run with live extraction. The cleanup now installs `propagation.NewCompositeTextMapPropagator()`, which extracts nothing, and the comment says so instead of claiming a restore.

### What I learned

The same delegator behaviour settles a question the other review agent raised: `otelhttp` snapshots the propagator at `NewHandler` time, which sounds like an ordering trap for a consumer who builds a router before configuring OpenTelemetry. It is not, because what gets snapshotted is the global delegator, which resolves per call.

`trace.WithNewRoot` and "link the parent" are not one feature. The link is conditional on the parent being remote, so any code path that makes a new root out of a local parent loses it silently.

### What was tricky

Deciding to deviate from an explicit requirement. "Unconditional — no new config or options" reads as "answer true always", but the intent behind it — no trusted upstream — is about clients, not about in-process spans. The predicate keeps the no-knobs property (nothing can turn it off) while limiting the severing to what the intent covers. It is flagged for the lead rather than buried.

### What warrants review

The predicate in `/http/otel.go` is the deviation to check. Behind it, `WithPublicEndpointFn` receives the extracted context, so `IsRemote()` is the discriminator; if the preference is a root with no exceptions, the change is one line and the "keeps a parent span from the same process" test inverts.

Baggage is untouched and still flows from the client into the request context and onward into job envelopes via `/jobs/runner.go`. Both review agents raised it; it is out of scope here and stated as such in `/docs/decisions.md`, not silently claimed as covered.

The preamble and Step 1 above name `otelhttp.WithPublicEndpoint()`, which does not exist in the pinned version. Left as written, since Step 1 is not mine to edit; the correction is here.

### Future work

Decide whether client-supplied baggage should be stripped in the middleware, which is the remaining channel through which a client influences our telemetry.

A test helper which sets the global propagator with honest cleanup could live in `/oteltest` — `/jobs/runner_test.go` sets one globally and never cleans up at all. Not done here to keep the public surface of `oteltest` unchanged for a fix about HTTP tracing.

## Step 3: Discarding the client's baggage too

**Author:** public-endpoint

### Prompt Context

**Verbatim prompt:** "Markus agreed with your baggage follow-up and wants it in this same PR (198), not a separate issue. Same rationale as the trace parent: caller-controlled propagation state we don't trust. Requirements: 1. In the `OpenTelemetry` middleware in `http/otel.go`, strip incoming baggage from the request context before the app handler runs, so client-sent `baggage` headers never reach handlers, outbound calls, or job envelopes. Use `baggage.ContextWithoutBaggage` (go.opentelemetry.io/otel/baggage) on the request context inside the wrapped handler — note otelhttp extracts baggage before invoking the inner handler, so clear it there. 2. Baggage the app sets itself (after the middleware) must keep working — don't touch propagator setup or the jobs runner. 3. Mirror the parenting decision: unconditional for remote... actually keep it simpler than the trace-parent predicate — strip extracted baggage regardless, since unlike a severed parent span there's no in-process signal being lost: an in-process caller invoking ServeHTTP retains its baggage in its own context; only the header-derived baggage in the request context is cleared. If you find a case where that reasoning breaks (e.g. in-process ServeHTTP where the outer context carries app-set baggage that would be wrongly cleared), match the `IsRemote()` predicate approach instead and say so in your report. [...]"

**Interpretation:** Extend the same distrust to baggage: what the request carried must not reach handlers or anything downstream, while the process's own baggage keeps working.

**Inferred intent:** Close the remaining channel through which a client writes state into this service's telemetry and job payloads.

### What I did

Checked the premise in requirement 3 first, and it does not hold. `propagation.Baggage.Extract` (read in the module cache) builds baggage from the headers alone and calls `baggage.ContextWithBaggage`, so extraction *replaces* the context's baggage rather than adding to it, and returns the parent untouched when there is no valid header. An in-process caller therefore does not retain its baggage "in its own context" as far as the handler is concerned: a plain `baggage.ContextWithoutBaggage` inside the wrapped handler would throw its baggage away whenever the request carried no header. The requirements anticipated this and told me to say so, which this is.

The first implementation captured the baggage the context held before extraction, under a private context key set by a wrapper around the `otelhttp` handler, and restored it inside. It worked, and self-review found a better shape: `otelhttp.WithPropagators` lets the middleware supply the propagator `otelhttp` extracts with, and inside `Extract` the "before" and "after" are two lines apart. `/http/otel.go` now has a `keepOwnBaggage` propagator which delegates to the global one and puts the previous baggage back, with `otelhttp.WithPropagators(keepOwnBaggage{})` in the option list. No context key, no wrapper handler, no request replacement.

Tests in `/http/otel_test.go` cover the request's baggage being discarded, the process's own surviving (no header, a header, a malformed header), the discard holding under a second copy of the middleware, and a handler adding to the context's baggage and injecting it, where the carrier must come out as exactly `job.id=123`. `usePropagators` now installs trace context and baggage together. `/docs/decisions.md` describes the shipped design.

### Why

The requirement named a mechanism, and the mechanism turned out to rest on an assumption about a dependency which the dependency does not make. Checking it first was the difference between shipping the requirement and shipping the intent.

### What worked

Deleting the fix and rerunning is what made every claim checkable. With the restore removed entirely, `Expected "0", but got "2"`; with the plain unconditional `baggage.ContextWithoutBaggage` the requirements described, `Expected "tenant=acme", but got ""` in both rows of the in-process table — the failure the requirement asked me to look for, reproduced rather than argued. With `otelhttp.WithPropagators(keepOwnBaggage{})` removed at the end, all four baggage tests failed, including `Expected "job.id=123", but got "user.id=evil,job.id=123"`.

Benchmarking settled the shape question: 44 allocs/op with the propagator, against 48 for the capture-and-restore version, which paid two `http.Request` copies and two context values on every request whether or not any baggage existed.

### What didn't work

Nothing failed outright, but the first design was worse in three ways at once, and only review surfaced them together: the extra allocations, the private context key riding the whole request context, and a request replacement inside the middleware which stopped `http.Request.Pattern` from reaching `otelhttp`'s second span-name formatter run — the very thing the comment at `/http/otel.go:147` warns about. The propagator version has none of them.

### What I learned

`otelhttp` extracts before it starts the span, so undoing the baggage in the propagator rather than in the handler also keeps it away from samplers and span processors. The handler-side version could not have done that.

The W3C limits mean baggage is not "unbounded" — `propagation.Baggage` caps it at 64 members and 8192 bytes — so the argument for discarding it is that it is client-controlled, not that it is unlimited. Both words appeared in an earlier draft of the doc comment; only one survives checking.

### What was tricky

The guarantee is narrower than "the client's baggage never reaches a handler", and saying so precisely took some care. What the middleware tells apart is *when* baggage entered the context, so tracing above it which extracts baggage itself hands that baggage to the middleware as though it were the process's own. Closing that would mean subtracting the request's own members from the context's, which needs the header knowledge this design avoids. Documented on `OpenTelemetry` and in `/docs/decisions.md` rather than half-fixed.

### What warrants review

`keepOwnBaggage` in `/http/otel.go` is where the behaviour lives; the doc comment on `OpenTelemetry` states the boundary above. Worth deciding whether the boundary is acceptable or whether the subtraction is wanted after all.

`Inject` and `Fields` delegate to the global propagator unchanged, so outbound injection and the job queue's enqueue-time context are unaffected, and `/jobs/runner.go` is untouched.

### Future work

If baggage from an upstream extractor ever matters, the subtraction described above is the way to close it.

## Step 4: Discarding all baggage instead

**Author:** public-endpoint

### Prompt Context

**Verbatim prompt:** "Markus's call on the baggage shape: discard ALL baggage in the middleware, unconditionally. glue apps don't use baggage at all; preserving app/in-process baggage is speculative machinery. YAGNI — it can be re-added if ever needed. Changes to PR 198: 1. Simplify `keepOwnBaggage` in `http/otel.go`: after delegating `Extract`, return the context with baggage removed entirely (`baggage.ContextWithoutBaggage`), instead of restoring pre-extraction baggage. Rename the type accordingly (e.g. `discardBaggage` or similar). Keep it as a propagator via `WithPropagators` — that shape stays right (client baggage never reaches samplers/processors). 2. This also closes the documented boundary: baggage extracted by a consumer's own middleware above ours gets dropped too, which is now the intended behavior. [...] 3. Tests: drop/invert the "process's own baggage kept" and "handler-set baggage propagates" subtests as appropriate — the invariant is now: no baggage in the handler context regardless of source above the middleware. [...]"

**Interpretation:** Replace the careful preservation of in-process baggage with a flat discard, keeping the propagator shape, and make the docs and tests say the simpler thing.

**Inferred intent:** Less machinery for a case nothing has, and one invariant with no hole in it.

### What I did

`keepOwnBaggage` in `/http/otel.go` became `discardBaggage`: `Extract` still delegates to the global propagator, then returns `baggage.ContextWithoutBaggage(ctx)` rather than putting the previous baggage back. The `Len() == 0` fast path stayed, now checking only the extracted context. `Inject` and `Fields` still delegate whole.

In `/http/otel_test.go` the "keeps baggage set in this process" table inverted into "discards baggage already in the context", keeping all three rows — no header, a header, a malformed header — since those are the branches where the discard rather than the extraction is doing the work. The outbound test dropped its handler-set member and now asserts the injected carrier has no baggage at all, with a `traceparent` assertion beside it so the empty result cannot pass for the wrong reason. `/docs/decisions.md` records the flat discard, with the preserving version as the rejected alternative.

Self-review found four things, all fixed. The doc comment justified the behaviour with "there is nothing here which wants baggage across the HTTP layer", which is this project's appetite rather than something a reader of the exported godoc can act on; it now gives the reason which holds for anyone — once extraction has run, nothing below can tell the request's baggage from the process's own. `/docs/decisions.md` credited `Inject` and `Fields` delegating for the queue being unaffected, which is wrong: `otelhttp` only ever extracts with the propagator it is given, and the queue is unaffected because `/jobs/runner.go` calls the global propagator directly. Every baggage assertion is now an absence, so `usePropagators` asserts the propagator under test actually carries baggage. And the trace-root test now sends a `baggage` header alongside its `traceparent`, so the discard is pinned as not taking the trace context with it.

`go test -shuffle on ./...` passes, `golangci-lint run ./http` reports 0 issues.

### Why

The preserving version bought one speculative case and paid for it with a hole: it told baggage apart by when it entered the context, so an upstream extractor's baggage passed as the process's own. Discarding everything has neither.

### What worked

Mutation checks again. Removing `otelhttp.WithPropagators(discardBaggage{})` fails all five baggage subtests, including `Expected "", but got "user.id=evil"` on the outbound one. Pointing `usePropagators` at `propagation.TraceContext{}` alone fails with `expected the propagator under test to carry baggage`, which is the point of adding it.

### What didn't work

Nothing failed. The simplification is smaller than what it replaced: `Extract` lost a variable and a branch, and `/http/otel.go` lost the paragraph explaining a boundary which no longer exists.

### What I learned

An absence is a weak assertion. Once every baggage test asserts "there is none", the harness itself has to be pinned, or a propagator which never carried baggage would satisfy all of them.

### What was tricky

Nothing in the code. The care went into the prose: the doc comment had to give a reason which holds for a reader who is not this project, and the decision entry had to stop crediting the wrong mechanism for the queue being unaffected.

### What warrants review

`discardBaggage` in `/http/otel.go`, and whether the flat invariant reads right on `OpenTelemetry`. Baggage set while handling a request still propagates from there; that is a consequence of where the discard sits, not a feature, and neither the doc comment nor the tests claim it.

### Future work

None. Carrying baggage through the HTTP layer would be a deliberate change if something ever needs it.
