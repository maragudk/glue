# Diary: Make substituted email values inert (issue #200)

Fix `postmark.getEmail` so substituted values (preheader, keyword values) are treated as data, not templates. Today, sequential `strings.ReplaceAll` calls rescan text produced by earlier replacements, so a `{{...}}` inside a value can be expanded — always for the preheader, and nondeterministically for keyword values (map iteration order). Follow-up to #191, tracked in issue #200.

## Step 1: Requirements and scope

**Author:** main

### Prompt Context

**Verbatim prompt:** See issue 200
**Interpretation:** Refine issue #200 into requirements and delegate the fix.
**Inferred intent:** Ship the single-pass substitution fix described in the issue, with tests proving values are inert and rendering is deterministic.

### What I did
Read issue #200 and `/email/postmark/postmark.go` (`getEmail`, lines 386-412). Confirmed the sequential `ReplaceAll` structure the issue describes. Defined requirements:

- `{{body}}` is a template (the email file), not a value: replace it into the layout first so its placeholders keep expanding.
- Then substitute preheader, unsubscribe, and all keywords in a single pass with `strings.NewReplacer`, which never rescans its own output.
- Acceptance: values containing `{{...}}` arrive literally; identical input renders identically; template-file placeholders (layout and body) still expand; HTML-escaping behavior unchanged.
- No telemetry: pure rendering logic.

### Why
The issue is precise about both the bug and the fix; my job was to confirm the code matches the description and pin down the one subtlety (body is a template, values are not) before delegating.

### What worked
The issue text mapped one-to-one onto the code; no surprises.

### What didn't work
Nothing failed at this stage.

### What I learned
The `unsubscribe` substitution injects Postmark's `{{{ pm:unsubscribe }}}` literal — it must stay a layout/template-level substitution and can safely join the single-pass replacer since its value contains no `{{...}}` in glue's own placeholder syntax.

### What was tricky
Distinguishing template-level substitutions (`{{body}}`) from value substitutions (preheader, keywords): a naive "one replacer for everything" would stop body-template placeholders from expanding.

### What warrants review
The builder's test coverage: literal `{{...}}` in preheader and keyword values, determinism across renders, and body placeholders still expanding.

### Future work
None identified.

## Step 2: Implement the single-pass fix

**Author:** builder-email-substitution

### Prompt Context

**Verbatim prompt:** Fix GitHub issue #200 ("Substituted email values are reprocessed as templates") in `getEmail` (`/email/postmark/postmark.go`, around line 386): replace `{{body}}` into the layout first since the body file is a template, then do a single pass over the combined document with one `strings.NewReplacer` covering `{{preheader}}`, `{{unsubscribe}}`, and every keyword, all HTML-escaped as before. Add table-driven tests covering the acceptance criteria, run `go test ./...`, update the diary, self-review, and commit referencing issue #200.
**Interpretation:** Restructure `getEmail` exactly as Step 1 specified, add regression tests proving values are inert and rendering is deterministic, self-review with two competing reviewer agents, and commit.
**Inferred intent:** Ship a correct, well-tested fix for the issue with no behavior drift on unsubscribe handling or HTML-escaping, and no telemetry changes.

### What I did
Read `/email/postmark/postmark.go` and `/email/postmark/postmark_test.go` to understand the existing structure and test conventions, and `/email/emails/layout.html` / `generic.html` / `new-email-notification.html` to see how `{{body}}`, `{{preheader}}`, `{{unsubscribe}}`, and keywords are used in practice.

Rewrote `getEmail` (`/email/postmark/postmark.go:386-424`):
- `{{body}}` is substituted into the layout on its own first, via `strings.ReplaceAll`, unescaped (it's trusted markup, same as before).
- The `unsubscribe` value (`"{{{ pm:unsubscribe }}}"` or `""`) is computed ahead of time from the same presence check as before (`_, ok := keywords["unsubscribe"]`).
- One `oldnew` slice is built with `{{preheader}}`, `{{unsubscribe}}`, then every keyword (HTML-escaped), skipping `"unsubscribe"` itself in the loop since it's already handled by the dedicated entry.
- `strings.NewReplacer(oldnew...).Replace(email)` does the whole substitution in a single pass, so no substituted value can be rescanned as a placeholder.

Updated the doc comment above `getEmail` to describe the two-phase approach (body substituted first as a template, everything else in one `[strings.Replacer]` pass) and why that makes substituted values inert.

Added tests to `/email/postmark/postmark_test.go`:
- `does not reprocess a {{...}} sequence in a substituted value as a template` — a preheader containing `{{appName}}` and two keywords whose values reference each other's placeholders (`title: "About {{content}}"`, `content: "About {{title}}"`) all arrive literally in the output.
- `renders identically every time, regardless of keyword map order` — the same cross-referencing keywords rendered 25 times in a loop, to catch map-iteration-order flakiness (this is the actual bug: it wouldn't reproduce reliably on a single run).
- `renders the Postmark unsubscribe tag when the keyword is present` / `renders nothing when the keyword is absent` — added after self-review flagged this gap (see below).

Ran `go build ./...`, `go vet ./...`, `gofmt -l email/postmark/`, `golangci-lint run ./email/postmark/...`, and `go test -shuffle on ./...` (full suite, all packages) — all clean.

### Why
The bug was that sequential `strings.ReplaceAll` calls each rescan the whole document, including text produced by earlier replacements, so a `{{...}}`-shaped value gets treated as a placeholder. `strings.NewReplacer` performs all replacements in a single simultaneous pass without rescanning its own output, which is exactly the property needed: substituted values become inert data instead of templates. Keeping `{{body}}` on its own `ReplaceAll` beforehand preserves the one legitimate case where a placeholder-like thing genuinely should keep expanding (the body file is itself a template, using keywords like `{{name}}`, `{{appName}}`, `{{baseURL}}`).

### What worked
The restructure was mechanical once the two-phase split (template substitution vs. value substitution) was clear from Step 1's framing. `strings.NewReplacer`'s documented "argument order" tie-break (first matching old-string wins) meant the `unsubscribe`-before-keywords ordering in `oldnew` was sufficient to preserve exact old behavior without needing extra guards, and skipping `"unsubscribe"` in the keyword loop keeps the list free of a dead/misleading duplicate entry.

### What didn't work
Nothing failed outright, but my first pass at self-review had a real gap: no test exercised the unsubscribe substitution at all (neither before nor after this change), even though this diff directly restructured that exact code path.

### What I learned
`strings.NewReplacer` with duplicate "old" strings doesn't panic — the first pair in argument order wins at each match position. That's what makes it safe to list `{{unsubscribe}}` once, ahead of the keyword loop, even in the (unlikely) case a caller passes a keyword literally named `"unsubscribe"` with some other value: the dedicated entry still wins, matching the old code's behavior where the keyword loop's `{{unsubscribe}}` replacement was already a no-op by the time it ran, since the earlier `ReplaceAll` had consumed every occurrence.

### What was tricky
Confirming the `unsubscribe` keyword's *value* was genuinely never used, in either the old or new code, required tracing the old sequential logic carefully: the old code's keyword loop iterated over `"unsubscribe"` too and would call `ReplaceAll(email, "{{unsubscribe}}", template.HTMLEscapeString(replacement))`, but by that point every `{{unsubscribe}}` occurrence had already been consumed by the earlier dedicated `ReplaceAll`, so that call was always inert. The new code makes this explicit by skipping `"unsubscribe"` in the loop instead of relying on it being accidentally dead.

### What warrants review
The `oldnew` slice ordering in `getEmail` (`/email/postmark/postmark.go`) — correctness depends on `{{preheader}}` and `{{unsubscribe}}` being listed before the keyword loop's entries, so a keyword named `preheader` or `unsubscribe` can't shadow the dedicated substitution. This matches old behavior exactly but is worth a second look. The two map-order/cross-reference tests in `/email/postmark/postmark_test.go` are the ones that would have caught the original bug; worth checking they'd actually fail against the pre-fix code (I didn't verify this myself, but reviewer A did and confirmed it).

### Future work
Self-review below surfaced one gap (unsubscribe test coverage), which I closed in this same step. No other follow-up identified.

### Step: Self-review

**Author:** builder-email-substitution

Dispatched two independent reviewer sub-agents (per the `fabrik:code-review` skill) against the diff in `/email/postmark/postmark.go` and `/email/postmark/postmark_test.go`, told they were competing with each other. Both independently reached the same verdict:

- **No correctness bugs** in the `strings.NewReplacer` restructure. Both reviewers traced through `strings.NewReplacer`'s duplicate-old-string tie-break behavior, keyword prefix-collision scenarios, nil keywords, and the exact old-vs-new behavior for an `"unsubscribe"` keyword with a non-trivial value, and found the fix sound and behavior-preserving.
- **Doc comment** judged accurate, correctly using the `[strings.Replacer]` identifier-link convention, with no caller/consumer-internals leakage.
- **No new package-level constants** worth flagging.
- **Consensus finding:** both reviewers independently flagged that acceptance criterion (d), "unsubscribe behavior unchanged," had zero test coverage — neither before nor after the change did any test assert what `{{unsubscribe}}` resolves to. Since this diff directly restructured that code path (moving it into the shared `Replacer` and adding the keyword-skip logic), this was a real, diff-relevant gap, not a pre-existing-and-out-of-scope one.

Addressed the consensus finding by adding the two `unsubscribe`/`renders nothing` table-driven tests described above, then re-ran the full test suite, `go vet`, `gofmt`, and `golangci-lint` to confirm everything still passes clean.

One single-reviewer, non-serious nit (doc comment links `[strings.Replacer]` the type rather than `[strings.NewReplacer]` the function actually called) was not acted on, per the code-review skill's signal-to-noise guidance: minor nitpicks are only surfaced when both reviewers flag them.
