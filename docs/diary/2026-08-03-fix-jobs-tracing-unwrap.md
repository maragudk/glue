# Diary: Fix `jobs.WithTracing` silently zeroing job payloads when tracing is unconfigured (issue #177)

The `jobs` package wraps every enqueued payload in a `tracedMessage` envelope (`Body` + `TraceContext`) so trace context propagates from HTTP requests to background jobs. Issue #177 reports that the receiving side, `jobs.WithTracing`, only unwraps the envelope when `TraceContext` is non-empty. With no tracer configured, the noop propagator injects nothing, `TraceContext` is an empty map, the unwrap is skipped, and the handler receives the envelope JSON instead of the payload. Because `json.Unmarshal` into a struct ignores unknown fields, the handler silently sees zero values for every payload field.

## Step 1: Validate the issue and scope the fix

**Author:** main

### Prompt Context

**Verbatim prompt:** Have a look at issue 177. If it's valid, fix the bug and create a PR

**Interpretation:** Verify that issue #177 describes a real bug in `/jobs/runner.go`; if so, coordinate a fix and open a PR.

**Inferred intent:** Restore correct payload delivery for background jobs in apps that don't configure OpenTelemetry, without breaking the tracing path or direct (non-enveloped) payloads.

### What I did

Read issue #177 via `gh issue view 177`, then read `/jobs/runner.go` and `/jobs/runner_test.go` to verify the claim. Confirmed the asymmetry: `wrapWithTrace` (used by both `Create` and `CreateTx`) always wraps the payload in the envelope, while `WithTracing` only unwraps when `err == nil && len(tracedM.Body) > 0 && len(tracedM.TraceContext) > 0` (line 90). Confirmed the bug case has no test coverage — the existing tests cover only "envelope with trace context" and "direct payload".

Scoped the fix as requirements for a builder:

1. `WithTracing` unwraps the envelope whenever one is detected, regardless of whether `TraceContext` is empty; trace context extraction happens only when `TraceContext` is non-empty.
2. Direct (non-envelope) payloads still pass through unchanged — a supported path per the existing test.
3. Envelope detection must not get looser: dropping the `TraceContext` length check naively would misdetect any payload object that happens to have a `Body` field. Detection should be tightened (e.g. strict decoding with `DisallowUnknownFields`) so false-positive risk stays comparable to before.
4. New test coverage for the bug case: envelope with empty `TraceContext` delivers the original payload bytes.

Rejected the "fail loudly" alternative from the issue because direct payloads are an explicitly supported input to `WithTracing`.

### Why

The issue's repro logic checks out against the code, and silent zero-valued payloads are data corruption from the app's perspective — worth fixing immediately. Scoping detection strictness up front avoids trading one silent misbehavior for another.

### What worked

The issue report was precise enough to verify by reading two files; no repro run was needed to confirm validity.

### What didn't work

Nothing failed at this stage; this step was investigation only.

### What I learned

`json.RawMessage` fields make `json.Unmarshal`-based envelope sniffing succeed for nearly any JSON object, so the `TraceContext` length check was doing double duty as both "is tracing configured" and "is this an envelope" — which is exactly why the empty-map case fell through the cracks.

### What was tricky

The subtle part is that the correct condition for *unwrapping* (envelope present) differs from the correct condition for *extracting trace context* (envelope present and context non-empty). The original code conflated them.

### What warrants review

The detection heuristic in `/jobs/runner.go` after the fix: it must catch glue-produced envelopes (including empty `TraceContext`) while not misdetecting consumer payloads. Validate with `go test ./jobs/...`.

### Future work

None identified yet; later steps will record the implementation.

## Step 2: Implement the fix, and land on a different detection heuristic than the one specced

**Author:** tracing-fix-builder

### Prompt Context

**Verbatim prompt:**

> You are fixing GitHub issue #177 in maragu.dev/glue. Work in the existing worktree at /Users/maragubot/Developer/glue/.claude/worktrees/jobs-tracing-unwrap (branch worktree-jobs-tracing-unwrap) — do NOT create a new worktree. All work happens in Go, so invoke the fabrik:go skill as your first action, and consult the fabrik:git skill before branching/committing.
>
> ## The bug (verified valid)
>
> In /jobs/runner.go, the enqueue side (`wrapWithTrace`, used by both `Create` and `CreateTx`) ALWAYS wraps the payload in the `tracedMessage` envelope (`{Body, TraceContext}`). The receive side (`WithTracing`, line 90) only unwraps when `err == nil && len(tracedM.Body) > 0 && len(tracedM.TraceContext) > 0`. When no tracer/propagator is configured, the noop propagator injects nothing, so `TraceContext` is an empty map, the unwrap is skipped, and the handler receives the envelope JSON instead of the payload. `json.Unmarshal` into a payload struct then silently yields zero values for every field. Silent data corruption, no error.
>
> ## Requirements
>
> 1. `WithTracing` must unwrap the envelope whenever one is detected, regardless of whether `TraceContext` is empty. Trace context extraction (propagator.Extract) happens only when `TraceContext` is non-empty.
> 2. Direct (non-envelope) payloads must still pass through unchanged — this is a supported path (see existing test "should handle direct payload without trace context"). Do NOT fail loudly on non-envelope input.
> 3. Envelope detection must not get looser than before. Dropping the `TraceContext` length check naively would misdetect any consumer payload object that happens to have a `Body` field. Tighten detection instead — e.g. strict decoding via `json.Decoder` with `DisallowUnknownFields` so only objects with exactly the envelope's fields (Body required and non-empty) are treated as envelopes. glue is a library: don't justify detection choices by assuming how consumers shape their payloads; the heuristic must be at least as safe as the old one for arbitrary payloads.
> 4. New tests:
>    - Envelope with empty `TraceContext` (as produced by `wrapWithTrace` with no propagator configured) → handler receives the original payload bytes, non-zero fields intact. This is the regression test for the bug.
>    - Keep/verify existing tests pass unchanged: envelope with trace context (context propagated, payload unwrapped), direct payload (passed through).
>    - Consider a test that a payload with an unrelated `Body` field but extra fields is NOT misdetected as an envelope (passes through unchanged).
>    - Dry humor in test payloads only is welcome; no emojis.
> 5. Update the doc comment on `WithTracing` if its behavior description changes.
>
> ## Scope boundaries
>
> - Do not change the enqueue side (`wrapWithTrace`, `Create`, `CreateTx`).
> - Do not restructure the envelope format — messages already in queues must keep working.
> - Do not touch other packages.
>
> ## Process
>
> - Diary already exists at docs/diary/2026-08-03-fix-jobs-tracing-unwrap.md (Step 1 is mine). Add your implementation step(s) via the fabrik:diary skill, authored under your name (tracing-fix-builder).
> - Run `go test ./jobs/...` (and gofmt/vet per the go skill) before committing.
> - Self-review your work once implementation is done (fabrik:code-review skill).
> - Commit code + diary together per the fabrik:git skill conventions. Reference issue #177 in the commit/PR so the PR closes it.
> - Push the branch and open a PR against main with `gh pr create`. The git skill governs branch naming — rename the branch if the auto-generated `worktree-jobs-tracing-unwrap` doesn't fit conventions.
> - Report back: what you changed, test results, PR URL, and anything you were unsure about.

**Interpretation:** Implement the fix scoped in Step 1: split "is this an envelope" from "does it carry trace context", cover it with tests, and open a PR closing #177.

**Inferred intent:** Ship the smallest correct fix that stops the silent corruption without introducing a second, subtler misdetection in its place.

### What I did

Split detection out of `WithTracing` into a new `unwrapTracedMessage` in `/jobs/runner.go`, which returns the envelope and whether the bytes were one at all. `WithTracing` now unwraps whenever detection succeeds and calls `propagator.Extract` only when `TraceContext` is non-empty, which is exactly the two conditions the old single `if` conflated. Detection requires a successful `json.Unmarshal` into `tracedMessage`, a non-empty `Body`, and a **non-nil** `TraceContext` map.

That nil check is the whole fix. Before writing any code I probed `encoding/json` (throwaway test in the scratchpad) and confirmed that a `map[string]string` struct field decodes to a non-nil empty map for `{}`, but stays nil when the key is absent or `null`. So `TraceContext != nil` means precisely "the key was present as a JSON object", which distinguishes a glue envelope with nothing propagated from a payload that merely happens to have a `Body` field — the exact false positive the requirements worried about.

Tests are in two files. `/jobs/runner_test.go` keeps the existing two subtests (I only hoisted their locally declared `testTracedMessage` to package level so the new ones can share it) and gains the regression test for an envelope with an empty `TraceContext`, plus an end-to-end check that a payload with a `Body` and a `Subject` of its own reaches the handler byte-for-byte. `/jobs/runner_internal_test.go` is new and, being in `package jobs`, can test the parts that matter most: a `wrapWithTrace` → `unwrapTracedMessage` round trip from a span-less context (the real #177 repro, driven by the producer rather than a hand-written envelope), a table of nine detection cases, and a guard on the marshaled field names so the wire format can't drift away from the messages already sitting in queues.

Verification, in order: `gofmt -l ./jobs` (clean), `go vet ./jobs/...`, `go test -shuffle on -race -count=2 ./jobs/...`, `golangci-lint run ./jobs/...` (0 issues), and the full `go test -shuffle on ./...` (all packages ok). I also checked the regression test against the old code by temporarily restoring `git show HEAD:jobs/runner.go`, which failed with exactly the reported symptom:

```
runner_test.go:154: Expected "{"message":"the propagator was on holiday","value":1234}",
but got "{"Body":{"message":"the propagator was on holiday","value":1234},"TraceContext":{}}"
```

Only that one subtest failed against the old code, which is the evidence that the other paths are behaviourally untouched.

Finally I mutation-tested the three conditions in `unwrapTracedMessage` by rewriting them one at a time with `perl -0pi` and rerunning the package: dropping the `TraceContext` presence check fails four subtests, changing it back to `len(...) == 0` (the original bug) fails three including the producer round trip, and dropping the `Body` check fails one. Every condition is pinned by at least one test.

### Why

The requirements suggested `json.Decoder` with `DisallowUnknownFields` for strictness, and I implemented that first. Two competing review sub-agents then independently found two problems with it, and I measured both before acting.

First, `json.NewDecoder(bytes.NewReader(m)).Decode` buffers the entire message into the decoder's own growing buffer, where `json.Unmarshal` parses the caller's slice in place. Both reviewers benchmarked it: a 100 KB direct payload went from 184 B and 3 allocations to ~260 KB and 19 allocations, and `DisallowUnknownFields` additionally calls `fmt.Errorf` for every unknown field even though only the first error is kept. That is a per-job cost on payloads whose size this library has no business assuming.

Second, `Decoder.More()` — which I used to reject trailing data, since a `Decoder` otherwise ignores it where `json.Unmarshal` does not — returns false when the next byte is `]` or `}`. So `{"Body":…,"TraceContext":{}}]` was unwrapped by my version and rejected by the old code: a widening in exactly the direction the requirements ruled out.

Plain `json.Unmarshal` removes both problems at once (it natively rejects trailing data, verified, while still allowing trailing whitespace) and keeps the allocation profile identical to the code being replaced. What it gives up is rejecting an envelope look-alike that carries *extra* fields alongside `Body` and `TraceContext` — but the old code unwrapped those too, so this is not a regression, and reaching it requires a payload with a `TraceContext` field holding a string map. Measured against the actual bar in requirement 3, "at least as safe as the old one", the delta versus old detection is now exactly one shape: an object with `Body` and an empty `TraceContext`, which is the bug being fixed. I judged that a better trade than a full message copy per job, and it leaves the code simpler than what it replaces.

### What worked

Probing `encoding/json` semantics with a throwaway test before designing anything, rather than trusting memory about nil-versus-empty map decoding. The whole fix rests on that one distinction, and it took two minutes to confirm.

Running two competing reviewers paid for itself: they independently converged on the `More()` hole and the allocation regression, both of which I would have shipped. Agreement between two of them was a good filter — every issue I acted on was found by both.

Mutation testing turned "the tests pass" into "each condition is load-bearing". One reviewer did this first and found that my original tests pinned only the bug fix itself, with `DisallowUnknownFields` and the `Body` check invisible to the suite.

### What didn't work

The first implementation, with `json.Decoder` and `DisallowUnknownFields`, passed every test and every linter and was still wrong on two counts. Neither showed up in `go test`; both needed either a benchmark or an adversarial input.

My first round of tests was too shallow. It exercised the public API with hand-built envelopes only, so it never touched `wrapWithTrace` — meaning the producer and consumer could have drifted apart again and the suite would have stayed green. That is the very class of bug #177 is, which is why `/jobs/runner_internal_test.go` now round-trips through the producer.

### What I learned

For a struct field of map type, `encoding/json` allocates on `{}` and leaves nil on `null` or an absent key. That single behaviour is what makes safe envelope detection possible without touching the wire format, and it is strictly more precise for this purpose than `DisallowUnknownFields`, which answers "are there extra fields" rather than "was this field present at all".

`Decoder.More()` is not a trailing-data check. It answers "is there another element in the current array or object", so it returns false for a stray `]` or `}` at the top level. `json.Unmarshal` is the stricter of the two here, which is the opposite of what its convenience-wrapper status suggests.

An empty `TraceContext` is not only an unconfigured-app phenomenon: a fully configured `propagation.TraceContext{}` also injects nothing when the enqueue context has no active span. So the branch fires in properly instrumented apps too, for anything enqueued outside a request — cron work, startup tasks, one job creating another. Both reviewers verified this independently, and the comment in `/jobs/runner.go` now says both causes rather than just the noop propagator.

### What was tricky

Deciding to deviate from the suggested implementation. The requirement named `DisallowUnknownFields` explicitly, but it named it as an example in service of a stated goal ("at least as safe as the old one"), and the presence check meets that goal at lower cost. Getting there meant characterising the detection-set delta precisely — which inputs the old code unwrapped, which the new one does, in both directions — rather than reasoning loosely about which is "stricter". The two are not comparable by a simple ordering: `DisallowUnknownFields` tightens one family of inputs while the buffering it forces costs every input.

The other sharp edge is that the internal round-trip test shares a test binary with `/jobs/runner_test.go`, which sets the global OpenTelemetry propagator. Under `-shuffle on` I could not rely on the noop default. Building the envelope from a context with no active span sidesteps it: the carrier comes out empty under either propagator, so the test is deterministic without mutating globals.

### What warrants review

The three conditions in `unwrapTracedMessage` at `/jobs/runner.go`, and specifically whether `TraceContext != nil` is accepted as the detection signal in place of the specced `DisallowUnknownFields`. The reasoning is above; the trade is a full message copy per job against rejecting envelope look-alikes that carry extra fields, which the old code already unwrapped. If the extra-fields family matters more than the copy, the strict-decoder version is a small edit away — but it needs `d.Token() == io.EOF` rather than `d.More()` for the trailing-data check.

`/jobs/runner_internal_test.go` is a new file in `package jobs`, following the `_internal_test.go` convention already used by `/sql/helper_internal_test.go`.

### Future work

Both reviewers independently flagged the same larger gap: `Create` and `CreateTx` always wrap, but `WithTracing` is opt-in, since glue re-exports goqite's `Runner` untouched and consumers call `Register` themselves. A handler registered without `WithTracing` receives the envelope and silently unmarshals to zero values — byte for byte the corruption in #177, reached by a different door. A glue-owned `Register` that always wraps would close it. Out of scope here, and an API addition worth its own decision.

Also pre-existing and adjacent: `wrapWithTrace` panics for a non-JSON `Message.Body` (`invalid character 'h' looking for beginning of value`) and for an empty non-nil one (`unexpected end of JSON input`), even though goqite documents the body as arbitrary bytes. A library panicking on valid upstream input deserves its own issue.

Smaller follow-ups: `/jobs/runner_test.go` sets the global tracer provider and propagator with no restore, where `/oteltest/oteltest.go` already provides `NewSpanRecorder(t)` doing it correctly with `t.Cleanup` — the `jobs` package is the outlier among `s3`, `http`, and `log`. Harmless today because it holds the package's only external test, and left alone to keep this diff to the bug.
