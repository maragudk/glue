# Diary: Add s3.Bucket.Exists method

Add an `Exists` method to `s3.Bucket` that reports whether an object exists at a given key, fitting the existing key-based API (`Put`/`Get`/`Delete`/`List`/`GetPresignedURL`).

## Step 1: Refine requirements and set up the feature

**Author:** main

### Prompt Context

**Verbatim prompt:** I need s3.Bucket.Exists method, build that and make a PR.
**Interpretation:** Add a new method `Exists` on the `s3.Bucket` type in the `s3` package, with tests, and open a pull request.
**Inferred intent:** Markus wants a cheap existence check for objects in a bucket, consistent with the rest of the s3 abstraction, so callers can ask "is there an object at this key?" without fetching the body.

### What I did
Read the `s3` and `s3test` packages to understand the existing patterns. Clarified with Markus that `Bucket.Exists` should check whether an *object* exists at a key (not whether the *bucket* exists). Set up a feature worktree (`worktree-s3-bucket-exists`) and started this diary. Refined the requirements below and handed them to a builder.

### Why
The method name `Bucket.Exists` is ambiguous between "does this object exist" and "does this bucket exist". The existing API is entirely key-based, but the literal name reads as bucket-level, so this needed an explicit decision rather than a guess. Markus chose object-level existence.

### What worked
The existing package is small and consistent, so the new method has a clear template to follow: every operation opens a span via `operationTracerStart`, records errors, and returns. `Get` already establishes the convention that "nothing there" is not an error.

### What didn't work
Nothing yet -- requirements stage.

### What I learned
`Get` matches `*types.NoSuchKey` to treat a missing object as `nil, nil`. The analogous existence check via `HeadObject` surfaces a missing key as `*types.NotFound`, not `*types.NoSuchKey` -- this is the sharp edge for the builder.

### What was tricky
The naming ambiguity, resolved by asking. Also the not-found error type difference between `GetObject` and `HeadObject`.

### What warrants review
Requirements only so far -- see the builder's steps below.

### Future work
None implied beyond this method.

## Requirements handed to builder

- Signature: `func (b *Bucket) Exists(ctx context.Context, key string) (bool, error)`
- Use `HeadObject` (no body transfer) to check existence.
- Return `false, nil` when the object is absent -- mirroring `Get`'s convention. Match the not-found error with `errors.As` against `*types.NotFound` (note: not `*types.NoSuchKey`, which is what `GetObject` returns).
- Return `false, err` on any real error, recording the span error and status like the other methods.
- Emit an `s3.exists` span with bucket + key attributes via `operationTracerStart`.
- Tests in the existing `bucket_test.go` style: object present -> true, absent -> false, plus a span assertion for `s3.exists`.

## Step 2: Implement `Exists` and its tests

**Author:** s3-exists-builder

### Prompt Context

**Verbatim prompt:** Add a method to the `s3.Bucket` type (in `/s3/bucket.go`) that reports whether an *object* exists at a given key. [...] Signature: `func (b *Bucket) Exists(ctx context.Context, key string) (bool, error)`. Use the S3 SDK's `HeadObject` operation [...] Return `true, nil` when the object exists [...] Match the not-found case with `errors.As` [...] `HeadObject` surfaces a missing key as `*types.NotFound`, NOT `*types.NoSuchKey` [...] Verify this against the SDK types in the module cache; don't assume.
**Interpretation:** Implement the `Exists` method on `Bucket` using `HeadObject`, treating a missing object as `false, nil` (matched via `*types.NotFound`), recording span errors for any real error, and add tests plus a span assertion. Document my work in this diary.
**Inferred intent:** Give callers a cheap, body-free existence check consistent with the rest of the key-based `s3.Bucket` API, with the not-found sharp edge handled correctly so absence is never reported as an error.

### What I did
Implemented `Exists` in `/s3/bucket.go`, placed immediately after `Get` so the read operations sit together. It opens an `s3.exists` span via the existing `b.operationTracerStart(ctx, "s3.exists", key)` helper, calls `b.Client.HeadObject` with `Bucket` and `Key`, matches `*types.NotFound` with `errors.As` to return `false, nil`, records the error and sets `codes.Error, "exists failed"` on any other error returning `false, err`, and otherwise returns `true, nil`. No new imports were needed -- `errors`, `types`, and `codes` were already imported by the existing methods.

Added tests in `/s3/bucket_test.go`: a `TestBucket_Exists` function with two subtests (object present -> `true`; never-written key -> `false`), and a span subtest inside the existing `TestBucket` suite asserting an `s3.exists` span is recorded with the key attribute, matching the `oteltest.NewSpanRecorder` / `findSpan` pattern used for `s3.put`/`s3.get`/etc.

Before writing any code I verified the not-found type against the SDK in the module cache with `go doc github.com/aws/aws-sdk-go-v2/service/s3/types.NotFound` (exists; doc string "The specified content does not exist.") and confirmed `HeadObject`'s signature and that `HeadObjectInput` carries `Bucket` and `Key` fields.

### Why
The requirements called for an object-level existence check that mirrors `Get`'s "nothing there is not an error" convention but via a cheaper HEAD request. Matching `*types.NotFound` (rather than `*types.NoSuchKey`) is what makes absence resolve to `false, nil`; everything else follows the established per-operation span/error pattern so the new method is indistinguishable in style from its neighbors.

### What worked
The package's uniform operation template made this mechanical: every method opens a span, records errors with `span.RecordError` + `span.SetStatus`, and returns. Build (`go build ./...`), vet (`go vet ./...`), and `gofmt -l ./s3/` were all clean on the first run. Both `-short` and full (non-short) `go test ./s3/...` passed.

### What didn't work
Nothing broke. No failed commands or errors to record.

### What I learned
The full integration test run is the real proof of the not-found handling: the "returns false when an object does not exist" subtest passed against the live local S3 (`http://localhost:7072`), which exercises the actual `*types.NotFound` returned by a real `HeadObject` on a missing key. Had the type been wrong (`*types.NoSuchKey`), the `errors.As` match would have missed, the error would have fallen through to the real-error branch, and that subtest would have failed -- so passing it confirms the type empirically, not just by inspection.

### What was tricky
The only sharp edge was the `*types.NotFound` vs `*types.NoSuchKey` distinction, already flagged in Step 1. I verified it against the SDK module cache rather than trusting the note, then re-confirmed it empirically via the integration test. No other friction.

### What warrants review
Look at `/s3/bucket.go` lines for the `Exists` method (just after `Get`): confirm the `*types.NotFound` match, the span name `s3.exists`, and that the error path mirrors `Put`/`Get`/`Delete`/`List` exactly. In `/s3/bucket_test.go`, confirm `TestBucket_Exists` covers both present and absent, and that the `s3.exists` span subtest asserts the key attribute.

### Validation
- `go build ./...` -- clean.
- `go vet ./...` -- clean.
- `gofmt -l ./s3/` -- clean (no files listed). `goimports` is not installed in this environment; gofmt was used.
- `go test -short ./s3/...` -- pass.
- `go test ./s3/...` (full, non-short) -- pass; verbose run confirms the new integration and span subtests ran (not skipped) against the local S3 at `http://localhost:7072`.

### Future work
None implied beyond this method.
