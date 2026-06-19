# Diary: Observability improvements (trace-log correlation + S3 otelaws)

Goal: close the two highest-value observability gaps found in a thorough sweep of glue.
The codebase is already well-instrumented (HTTP, jobs, SQL, S3, Postmark all create spans
with sensible attributes and proper error recording). Two gaps remain worth closing this
round:

1. **Trace-log correlation.** All ~40 logging call sites use plain `slog` calls
   (`log.Info`, `s.log.Error`, ...). None use the context-aware variants, and the `log`
   package builds a vanilla `slog.Logger` with no handler that injects trace context. So
   logs and traces live in disconnected stores -- the anti-pattern the observability
   guidance calls out. Several logs fire inside active spans (Postmark errors, auth
   failures) where a `trace_id`/`span_id` would let you jump from a log line to its trace.

2. **S3 AWS SDK instrumentation via `otelaws`.** S3 ops have good app-level spans, but the
   AWS SDK client itself isn't instrumented. `otelaws.AppendMiddlewares` surfaces AWS
   request IDs, retries, and per-API-call timing -- high-cardinality fields that matter
   when debugging S3 issues.

Out of scope (deliberately): wrapping the Postmark HTTP client with `otelhttp.NewTransport`
(already manually traced; marginal), and enabling metrics (deliberately disabled; wide span
events are preferred over pre-aggregated metrics here).

## Step 1: Scope and requirements (lead)

**Author:** main (lead)

### Prompt Context

**Verbatim prompt:** "Use your observability skill, and make a thorough sweep through the codebase to check for observability improvements."
**Interpretation:** Survey the whole module for instrumentation gaps using the observability skill's principles, then act on the worthwhile ones.
**Inferred intent:** Improve production debuggability of glue without over-engineering; keep changes aligned with the project's existing, deliberate instrumentation choices.

### What I did
Loaded the observability skill and its Go instrumentation reference. Ran a thorough sweep
(via an Explore agent plus direct reads of `log/log.go`, `email/postmark/postmark.go`,
`s3/bucket.go`) and confirmed: existing OTel setup via honeycomb otel-config-go in
`app/app.go`; rich HTTP/jobs/SQL/S3/Postmark span instrumentation; zero context-aware log
calls (40 plain calls); `otelaws` not yet a dependency.

Confirmed scope with the user: do trace-log correlation (converting existing call sites)
plus S3 `otelaws` instrumentation. Created worktree
`worktree-observability-improvements` and seeded this diary. Briefed a builder teammate
with the requirements below.

### Why
The sweep showed the framework is mature; the remaining high-value work is correlating the
two telemetry stores (logs <-> traces) and deepening S3 visibility at the SDK layer.

### What worked
The Explore sweep matched direct file reads -- the instrumentation is consistent and
well-namespaced (`app.*`, `maragu.dev/glue/<pkg>` tracers), so the gaps were easy to isolate.

### What didn't work
Nothing yet -- requirements stage.

### What I learned
glue intentionally disables metrics and leans on spans. Any observability change should
respect that and prefer span attributes / trace context over new metrics.

### What was tricky
Distinguishing genuine gaps from deliberate design choices (metrics off, Postmark client
hand-instrumented). Pruned those out of scope rather than treating every "missing" wrap as
a defect.

### What warrants review
The requirements handed to the builder (below). Reviewer should confirm the trace-log
handler design matches how glue constructs and injects its logger.

### Future work
Possibly wrap the Postmark HTTP client with `otelhttp.NewTransport` and revisit metrics if a
need for exact unsampled counts arises. Not this round.

---

## Requirements handed to the builder

### Feature 1: Trace-log correlation

- Add a slog handler (in the `log` package) that, on each record, pulls the span context
  from the record's `context.Context` and, if the span context is valid, adds `trace_id`
  and `span_id` attributes to the log record. Use the OTel trace API
  (`trace.SpanContextFromContext`) -- no new heavy deps.
- Wire this handler into `NewLogger` so both JSON and text loggers get correlation. Preserve
  existing behavior: `NoTime`, `Level`, JSON-vs-text selection, and the `time`-stripping
  `ReplaceAttr`.
- Convert glue's own internal logging call sites to the context-aware variants
  (`InfoContext`/`ErrorContext`/`WarnContext`/`DebugContext`) wherever a `context.Context`
  is in scope. Where no ctx is reasonably available (e.g. startup/shutdown logs in
  `app/app.go`, `http/server.go`), leave them as-is rather than inventing a ctx.
  Call sites to review include: `http/auth.go`, `email/postmark/postmark.go`, `sql/helper.go`,
  `sql/migrate.go`. Verify the full set with a grep.
- Field naming: use `trace_id` and `span_id` (lower snake_case, matching slog convention),
  hex-encoded as OTel produces them.

### Feature 2: S3 AWS SDK instrumentation via otelaws

- Add `go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws`
  and call `otelaws.AppendMiddlewares(&opts.Config.APIOptions)` (or equivalent) so the AWS
  SDK S3 client emits per-API-call spans with AWS request IDs and retry info.
- Keep the existing app-level spans (`s3.put`, `s3.get`, etc.) -- otelaws spans nest under
  them, giving both the business-level and SDK-level views.
- Make sure this doesn't double-wrap or break the existing `NewBucket` options
  (`PathStyle`, `DisableLogOutputChecksumValidationSkipped`).

### Acceptance criteria

- `go build ./...` and `go vet ./...` pass.
- `go test ./...` passes. Add/extend tests: the `oteltest` package + a span recorder can
  verify that logging within a span produces a record carrying `trace_id`/`span_id`, and
  that S3 operations still record their spans. Follow existing test patterns in
  `http/otel_test.go` and `oteltest/`.
- No new metrics; no change to the Postmark client.
- Builder self-reviews using the project code-review skill before reporting back.

---

## Step 2: Implementation (builder)

**Author:** builder

### Prompt Context

**Verbatim prompt:** "You're building two observability improvements in the glue Go module. [...] Feature 1: Trace-log correlation [...] Feature 2: S3 AWS SDK instrumentation via otelaws [...] Do NOT add metrics and do NOT touch the Postmark HTTP client."
**Interpretation:** Implement the two features exactly as specified in the lead's requirements: a slog handler that injects trace_id/span_id, converting in-scope call sites to context-aware logging, and wiring otelaws into the S3 client. Add tests, keep everything green, self-review.
**Inferred intent:** Close the two telemetry-correlation gaps without expanding scope or disturbing deliberate design choices.

### What I did

Feature 1 (`/log/log.go`): Added a `traceHandler` that wraps a `slog.Handler`. Its `Handle(ctx, record)` calls `trace.SpanContextFromContext(ctx)` and, when `IsValid()`, adds `trace_id`/`span_id` string attributes (hex via `TraceID().String()`/`SpanID().String()`). Refactored `NewLogger` to build the JSON or text base handler, then wrap it in `traceHandler` so both paths get correlation. Implemented `WithAttrs`/`WithGroup` to re-wrap so the correlation survives `logger.With(...)`. Preserved `NoTime`, `Level`, JSON-vs-text, and the `time`-stripping `ReplaceAttr`.

Converted context-aware call sites where a `ctx` is genuinely in scope: `/http/auth.go` (6 sites -- 5 use `ctx`, the `Logout` one uses `props.Ctx`), `/email/postmark/postmark.go` (3 sites in `sendRequest(ctx ...)`), `/sql/helper.go` (3 sites in `Connect(ctx ...)`). Left plain: `/app/app.go` and `/http/server.go` (startup/shutdown, no ctx), and `/sql/migrate.go`'s `getMigrations()` (gated by `sync.Once`, no ctx in scope).

Feature 2 (`/s3/bucket.go`): Ran `go get go.opentelemetry.io/contrib/instrumentation/github.com/aws/aws-sdk-go-v2/otelaws` and called `otelaws.AppendMiddlewares(&opts.Config.APIOptions)` in `NewBucket` before `s3.NewFromConfig`. Existing app-level spans and the `PathStyle`/`DisableLogOutputChecksumValidationSkipped` options are untouched.

Tests: `/log/log_test.go` (internal `package log` test so it can wrap a buffer-backed handler with the unexported `traceHandler`) asserts trace_id/span_id present within a span, absent without one, and that correlation survives `With(...)`. `/s3/otel_test.go` uses `oteltest.NewSpanRecorder` to assert both the `s3.put` app span and the otelaws `S3.PutObject` SDK span are recorded, share a trace, and that the SDK span's parent is the app span.

### Why

The handler injects context at the slog layer so every existing call site benefits once converted, rather than threading IDs by hand. Re-wrapping in `WithAttrs`/`WithGroup` matters because `slog.Logger.With` is common and would otherwise strip the correlation. otelaws is wired at client construction so all S3 calls -- including presign and the test helper's bucket ops -- get SDK spans without touching each operation.

### What worked

`go build ./...`, `go vet ./...`, and `go test -shuffle on ./...` all pass with Docker test services up (`docker compose up -d` brings up postgres and versitygw for S3). The S3 test confirms the nesting (`appSpan.SpanID == sdkSpan.Parent().SpanID`, same TraceID) -- exactly the business+SDK view the requirements wanted.

### What didn't work

First S3 span lookup guess was wrong about the SDK span name; otelaws names spans `S3.PutObject` (`<Service>.<Operation>`), which the passing test confirmed. A draft `WithGroup` test failed: attributes added inside `Handle` nest under the active group, so top-level `trace_id` was nil and the `.(string)` assertion panicked. Since `NewLogger` never uses groups, I replaced that assertion with a `WithAttrs` test (which keeps the keys top-level) and left `WithGroup` implemented and exercised indirectly.

### What I learned

otelaws supports several AWS services, so adding it pulls dynamodb/sqs/sns/route53 in as `// indirect` deps and bumps `go.opentelemetry.io/otel` from 1.40.0 to 1.43.0. That bump is module-wide; all packages still build and test green against it.

### What was tricky

Deciding which call sites get a ctx. `getMigrations()` is the clearest "leave it plain" case -- it's behind `sync.Once` with no ctx parameter, and threading one purely for a log line would be invented plumbing the requirements explicitly warned against.

### What warrants review

Two decisions worth the lead's eye: (1) the otel 1.40 -> 1.43 bump and the transitive AWS-service indirect deps from otelaws -- acceptable but a visible footprint; (2) trace attributes added in `Handle` nest under any active slog group. glue doesn't use groups today, so this is latent, but if a future logger uses `WithGroup`, `trace_id`/`span_id` would land under that group rather than at top level.

### Future work

None required. If group-nesting of trace fields ever becomes a concern, the handler could be changed to add the attributes via a path that bypasses the group, but that's not needed now.

---

## Step 3: Merge `main` and resolve dependency conflicts

**Author:** main

### Prompt Context

**Verbatim prompt:** "merge origin/main and push"
**Interpretation:** The PR was marked CONFLICTING; merge the updated `main` into the branch, resolve, and push.
**Inferred intent:** Get the PR mergeable again without disturbing its actual changes.

### What I did
Merged `origin/main` into the branch. The only conflicts were in `/go.mod` and `/go.sum`, all from OpenTelemetry and transitive AWS dependency drift since the PR was opened. Both sides were already at otel v1.43.0, so I took `main`'s newer transitive versions and ran `go mod tidy` to reconcile with the then-present `otelaws` dependency. Verified `go build`, `go vet`, and the non-infra packages (`log`, `http`, `email/postmark`). Committed the merge (6cab972) and pushed.

### Why
`main` had moved on (notably the OTLP exporter bump for GO-2026-4985), and the PR's own otel bump overlapped, producing the conflict.

### What worked
`go mod tidy` cleanly pruned the now-redundant `backoff/v4` and settled `smithy-go` at v1.24.3; the PR went from CONFLICTING to MERGEABLE.

### What didn't work
The S3 and Postgres tests failed locally with connection-refused -- missing local Docker services, not the merge. CI runs them against containers.

### What I learned
Nothing surprising; standard dependency-conflict reconciliation.

### What was tricky
Telling genuine code failures apart from the local-infra failures in `go test ./...` output.

### What warrants review
The `/go.mod` resolution -- confirm the transitive versions match `main`'s intent.

### Future work
None at this point.

## Step 4: First review pass -- clone APIOptions, fix `WithGroup`, injectable writer

**Author:** main

### Prompt Context

**Verbatim prompt:** "/fabrik:code-review" then "/fabrik:address-code-review", with per-comment decisions including "Apply", "This is a library, so you can't inspect current usage and accept that it's not used.", and "A".
**Interpretation:** Run a competing-reviewer code review, then triage findings one at a time and apply the agreed ones.
**Inferred intent:** Harden the new code before merge.

### What I did
Two competing reviewers reached consensus on three items, all applied (ac033e8): (1) `/s3/bucket.go` -- `otelaws.AppendMiddlewares` mutated the caller's shared `APIOptions` backing array and re-instrumented per bucket; fixed with `slices.Clone` before appending. (2) `/log/log.go` -- the trace handler nested `trace_id`/`span_id` under an open `WithGroup` (the latent issue Step 2 flagged); first fix tracked `WithGroup`/`WithAttrs` ops and replayed them per record so trace fields stayed top-level. (3) `/log/log.go` + `/log/log_test.go` -- added an injectable `NewLoggerOptions.W` writer so the `NoTime` test could capture output instead of asserting only `logger != nil`.

### Why
All three were real: a shared-slice mutation hazard, the correctness bug under groups Step 2 had left latent, and a vacuous test.

### What worked
Keeping the reviewers adversarial surfaced the same load-bearing issues independently, which made triage fast.

### What didn't work
My initial framing of the `WithGroup` fix leaned on "groups aren't used today." The user rejected that outright -- this is a library, so current usage can't justify leaving a latent correctness bug. I recorded that as a memory and added it to the project guidance (CLAUDE.md, which is a symlink to AGENTS.md).

### What I learned
The "don't assume consumer usage" principle is now explicit project guidance.

### What was tricky
The replay-based `WithGroup` fix was correct but heavy -- it rebuilt the handler chain per record, which became the seed of the Step 6 redesign.

### What warrants review
Superseded by Step 6; the replay handler no longer exists.

### Future work
Reconsider the per-record cost of the replay handler (addressed in Step 6).

## Step 5: Reuse `oteltest.NewSpanRecorder` in the log tests

**Author:** main

### Prompt Context

**Verbatim prompt:** "/fabrik:address-code-review" (maintainer inline comment: "Looks like a duplicate of what's in oteltest? Can we use that instead?")
**Interpretation:** The `newSpan` test helper duplicated the global tracer-provider setup that `oteltest.NewSpanRecorder` already provides.
**Inferred intent:** Remove duplication, reuse the shared helper.

### What I did
Rewrote `newSpan` in `/log/log_test.go` to call `oteltest.NewSpanRecorder(t)` for the provider setup and just start/return the span, dropping the hand-rolled `tracetest`/`sdktrace` plumbing and save/restore. Committed (992643f), replied to and resolved the GitHub thread.

### Why
The helper reimplemented the exact global-provider dance `oteltest` exists to encapsulate; no import cycle since `oteltest` doesn't import `log`.

### What worked
Straightforward; tests stayed green.

### What didn't work
My verification commands initially ran against the wrong directory: an earlier `cd` to the main checkout meant `go test ./log/...` reported `[no test files]` (the test only exists on the PR branch). Re-ran from the worktree to confirm for real.

### What I learned
The shell's working directory persists across commands -- a stray `cd` silently retargets later ones. Use absolute paths or re-`cd` into the worktree per command.

### What was tricky
The misleading `[no test files]` output looked like a build/exclusion problem before I realized it was a cwd issue.

### What warrants review
`/log/log_test.go` `newSpan` -- confirm it reads cleanly as a thin wrapper.

### Future work
None.

## Step 6: Consumer audit and `traceHandler` redesign

**Author:** main

### Prompt Context

**Verbatim prompt:** "Launch subagents to check log usage in ../missionfocus/edc and ../app (the former is proprietary code, so don't mention any specifics in this public repo). I don't think this approach is good enough."
**Interpretation:** Don't assume how the logger is used -- go measure it in the real consumers -- then reconsider the replay handler.
**Inferred intent:** Validate the design against actual usage rather than guesses.

### What I did
Ran two `Explore` subagents over `../app` and `../missionfocus/edc` (keeping the proprietary one generic). Both showed the same pattern: loggers are built once at startup with `.With("component", …)` and then used directly -- including in hot per-item loops within spans -- and `WithGroup` is never used. That overturned my proposed `len(withs) == 0` fast path (every real logger has at least one `.With`, so it would never fire). The user then proposed letting trace fields follow the record's grouping. I reverted `/log/log.go` to a thin `slog.Handler`-embedding handler that adds `trace_id`/`span_id` via `r.AddAttrs` (top-level for ungrouped loggers, nested under a group otherwise -- documented), deleting the `base`/`applied`/`withs` machinery. Added comprehensive scenario tests in `/log/log_test.go`, documented `NewLoggerOptions.W`, and converted in-scope startup/shutdown logs in `/app/app.go` and `/http/server.go` to the `*Context` variants. Committed (d86aacd).

### Why
The replay handler paid a per-record handler-chain rebuild on exactly the pattern consumers use (component loggers in hot loops within spans). Since `WithAttrs` doesn't nest and only `WithGroup` does -- and nobody uses `WithGroup` -- the simple `r.AddAttrs` handler is both correct for the common case and the cheapest possible, with grouped nesting as a documented contract rather than a bug left unfixed.

### What worked
Measuring beat assuming: the audit produced a concrete table that immediately invalidated the optimization and pointed at the simpler design.

### What didn't work
The original "optimize for the base logger" instinct was wrong -- real loggers are derived, not base. Caught only because the user pushed back and we looked.

### What I learned
`slog`'s `WithAttrs` emits attributes at the top level; only `WithGroup` opens nesting. So trace fields added to the record land top-level unless a group is open -- which is why the simple handler is correct for all current usage.

### What was tricky
Reconciling "don't assume consumer usage" with "don't build machinery for usage that doesn't exist." Resolved by making the simple handler's grouped behavior a documented contract (correct for any usage) rather than relying on groups being unused.

### What warrants review
`/log/log.go` `traceHandler` and the eight scenario subtests in `/log/log_test.go` -- especially the documented group-nesting behavior.

### Future work
None; the grouping caveat is documented on the handler.

## Step 7: Relocate the S3 OpenTelemetry test into `bucket_test.go`

**Author:** main

### Prompt Context

**Verbatim prompt:** "/fabrik:address-code-review" (maintainer: "Don't create a separate test file, put in bucket_test.go as subtests"), then "TestBucket_MethodName is the convention" and "Fair enough, just use subtests in TestBucket htne".
**Interpretation:** Fold the standalone `otel_test.go` into `bucket_test.go` as a subtest of `TestBucket`.
**Inferred intent:** Match the file's existing test organization.

### What I did
Moved the span-nesting check into `TestBucket` as a subtest, brought the `findSpan` helper along, merged imports, and deleted `/s3/otel_test.go`. Committed (c773781), replied to and resolved the thread.

### Why
House convention groups S3 tests in `bucket_test.go`; a separate file and top-level `TestOpenTelemetry` didn't fit.

### What worked
Clean relocation; the subtest ran under `TestBucket` and stayed green.

### What didn't work
`git rm` had already staged the deletion, so a combined `git add s3/otel_test.go …` failed with `pathspec 's3/otel_test.go' did not match any files` and aborted the commit. Re-staged just the modified file (the deletion was already staged) and committed.

### What I learned
After `git rm`, don't re-`git add` the deleted path -- it's already staged.

### What was tricky
Briefly debated naming (`TestBucket_Put` vs a subtest in `TestBucket`); the user chose the subtest.

### What warrants review
Superseded by Step 8 -- this subtest was rewritten when otelaws was dropped.

### Future work
None.

## Step 8: Drop the S3 `otelaws` instrumentation

**Author:** main

### Prompt Context

**Verbatim prompt:** "Wait, so we now have two spans per S3 request?" -> "I lean towards dropping the app-level S3 spans. Do we lose any attributes or other info we don't have in the AWS span?" -> "2"
**Interpretation:** Investigate the double-span behavior, compare app-level vs SDK spans, and act on the chosen option.
**Inferred intent:** Decide deliberately whether the SDK-level spans earn their cost.

### What I did
Read `/s3/bucket.go` and the `otelaws@v0.68.0` source. Found that otelaws ships attribute builders for DynamoDB/SNS/SQS but **none for S3**, so its S3 span carried `rpc.*`, `aws.region`, `http.response.status_code`, and `aws.request_id` -- but no bucket and no key -- and it would mark the expected NoSuchKey miss as an error span. The app-level `s3.*` spans carry `aws.s3.bucket`, `aws.s3.key`, region, and treat a missing key on `Get` as success. The user chose to drop otelaws and keep the app spans. Removed the `AppendMiddlewares`/`slices.Clone` call and imports from `/s3/bucket.go`, repurposed the `/s3/bucket_test.go` subtest to assert the `s3.put` app span carries the `aws.s3.key` attribute, and ran `go mod tidy`, which dropped `otelaws` and its `dynamodb`/`sns`/`sqs`/`endpoint-discovery` indirect deps. Committed (099a760), then retitled the PR to "Correlate logs with traces" and rewrote its description.

### Why
Two spans per S3 op doubled trace volume for an SDK span that lacked the most useful debugging attributes (bucket/key) and introduced error-span noise on benign misses. The app spans alone give the better signal.

### What worked
Reading the otelaws source rather than guessing -- the absent S3 attribute builder and the NoSuchKey-as-error behavior were the deciding facts, and neither was obvious from the outside.

### What didn't work
Nothing broke; `go build`, `go vet`, the `s3` tests, and lint were all clean after the removal.

### What I learned
`otelaws.AppendMiddlewares` with no options produces generic RPC spans with no service-specific attributes for S3; useful instrumentation there would require a custom `AttributeBuilder`. Dropping otelaws is therefore close to free in lost signal for S3, and it reverses the dependency-footprint and otel-bump concerns Step 2 had flagged.

### What was tricky
The decision hinged on a non-obvious omission in a dependency; it took reading the package to be confident the app spans weren't losing anything the SDK span uniquely had (they lose only `aws.request_id`/HTTP status).

### What warrants review
`/s3/bucket.go` (otelaws fully removed, app spans intact) and the rewritten `/s3/bucket_test.go` subtest; confirm `/go.mod` dropped only the otelaws-pulled indirect deps and kept the base AWS SDK.

### Future work
If AWS request IDs ever matter for S3 debugging, revisit with a custom otelaws `AttributeBuilder` that adds bucket/key and a way to suppress the NoSuchKey error noise.
