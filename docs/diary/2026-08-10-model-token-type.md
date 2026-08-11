# Diary: add a `model.Token` type

Issue #181 asks for a `Token` type in the `model` package, completing the pair with `EmailAddress`: the error sentinels `model.ErrorTokenExpired` and `model.ErrorTokenNotFound` already imply token-based flows, but the token itself has been a bare `string` for applications to define. Every application implementing magic-link login (or similar single-use token flows) ends up writing exactly this type, and typed signatures make it explicit which strings are credentials.

## Step 1: Scope the work and set requirements

**Author:** main (lead)

### Prompt Context

**Verbatim prompt:** "Now issue 181" (a follow-up line pointing at private reference material is redacted at the user's request). A later mid-work message added: "Make a PR after"
**Interpretation:** Implement issue #181 — add `model.Token` with `NewToken()`, `IsValid()`, and `String()` — then push and open a pull request without a further approval round.
**Inferred intent:** Give applications a ready-made, correctly-minted credential type so each one stops hand-rolling the same sixteen-bytes-of-`crypto/rand` token, and so function signatures can say "this string is a credential".

### What I did

Read issue #181 and the existing `model` package: `/model/model.go` (the `ID` types), `/model/email.go` (the `EmailAddress` pattern this type should mirror: package-level compiled matcher, `IsValid()`, `String()` with a compile-time `fmt.Stringer` assertion), `/model/error.go` (the token error sentinels), and `/model/auth.go` (`Role` and `Permission`). Checked `/go.mod`: glue is on Go 1.25. Created the worktree `model-token-type` and this diary, and drafted the requirements below for a builder.

Requirements handed to the builder:

1. New file `/model/token.go` with `type Token string` — a plain string type like `EmailAddress`, not derived from `ID`, because a token is a credential, not an identifier.
2. `NewToken() Token`: sixteen bytes of `crypto/rand`, hex-encoded, behind a `t_` prefix matching the `<letter>_<hex>` id shape. On Go 1.24+ `crypto/rand.Read` never returns an error (it panics irrecoverably instead), so ignoring the returned error is correct, not sloppy.
3. `Token.IsValid() bool`: anchored `^t_[0-9a-f]{32}$` via a package-level compiled regexp, so HTTP layers can refuse malformed tokens without a database read. Lowercase hex only — that is the only shape ever minted.
4. `Token.String() string` with the compile-time `var _ fmt.Stringer` assertion, matching house style.
5. Tests in `/model/token_test.go`: a mint/`IsValid` round-trip (the whole contract between the two functions), acceptance of the canonical shape, and a rejection table (empty, missing/wrong/doubled prefix, wrong length, non-hex, uppercase hex, leading/trailing junk including a trailing newline, and a full sentence). No uniqueness test — that would be testing `crypto/rand`.
6. Doc comments describe the token's own contract; per project rules, no justification by assumed consumer usage.

### Why

The issue is unusually concrete — it specifies the constructor, the validation regexp, and the rationale — so the lead work was mostly mapping it onto glue's existing `model` conventions and pinning down the details the issue leaves implicit (file placement, the `rand.Read` error question, what the tests owe).

### What worked

The `EmailAddress` type is a near-perfect template: matcher, `IsValid`, `String`, `Stringer` assertion. Following it means the new type needs no novel design decisions.

### What didn't work

Nothing failed at this stage; scoping was read-only.

### What I learned

Go 1.24 changed the `crypto/rand.Read` contract: it is now documented to never return an error, aborting the program instead if randomness is unavailable. That turns the `_, _ = rand.Read(...)` idiom from a smell into the correct call shape on a Go 1.25 module.

### What was tricky

One scope call: applications with single-use tokens also want an `ErrorTokenUsed` sentinel ("already used" and "no such token" are different things to whoever holds the link), and glue's `model.Error` set lacks it. Kept strictly out of this branch — issue #181 doesn't ask for it — but flagged to the user as a natural follow-up issue.

### What warrants review

The rejection table's coverage: the regexp is trivially right-looking, so the tests are where a subtly unanchored or case-insensitive matcher would be caught.

### Future work

- Possibly an `ErrorTokenUsed` sentinel in `/model/error.go`, as a separate issue if the user wants it.

## Step 2: Implement `model.Token`

**Author:** token-builder

### Prompt Context

**Verbatim prompt:** The lead's brief, opening "You are building issue #181 for maragu.dev/glue: add a `model.Token` type", followed by six numbered requirements (the plain string type, `NewToken()`, `IsValid()`, `String()` with the `fmt.Stringer` assertion, the tests, and the doc comments) and process notes: read `CLAUDE.md` and the existing `model` package first, verify with `go test -shuffle=on ./model/...`, `go vet ./model/...` and `gofmt -l model/`, append an implementation step and a separate self-review step to this diary, and land everything as one commit ending in `Fixes #181` without pushing. A reference implementation drawn from private material is redacted at the user's request.
**Interpretation:** Write `/model/token.go` and `/model/token_test.go` in the shape of the existing `EmailAddress` type, prove the matcher is anchored and case-sensitive by testing it adversarially, and keep every doc comment inward-looking.
**Inferred intent:** A credential type that is obviously correct on inspection, so that nobody implementing a token flow has to re-derive the entropy, encoding, or validation.

### What I did

Read `/CLAUDE.md` and the whole `model` package before writing anything: `/model/email.go` for the matcher/`IsValid`/`String` shape, `/model/model.go` for the `fmt.Stringer` assertion style, `/model/error.go` for the token sentinels, and `/model/email_test.go` and `/model/auth_test.go` for the table-driven test idiom (`is.Equal(t, expected, actual)` inside nested `t.Run` subtests). Confirmed in `/go.mod` that the module is on Go 1.25.

Before writing the doc comments, checked whether glue actually has the `<letter>_<hex>` id convention the brief mentions, with `grep -rn "crypto/rand\|hex.EncodeToString\|NewID" --include="*.go" .` and a scan of the SQL files. It does not: the only `crypto/rand` users are `/s3test/bucket.go` and `/sqlitetest/helper.go`, `/model/model.go` gives `ID` no shape at all, and the fixtures in `/sqlite/testdata/fixtures/` declare a bare `id text primary key`. So the prefix is documented on its own terms rather than as matching an id convention that this repo does not have.

Wrote `/model/token.go`: the package-level `tokenMatcher` compiled from `^t_[0-9a-f]{32}$`, `type Token string`, `NewToken()` reading 16 bytes with `rand.Read` into a `[16]byte` and hex-encoding them behind the `t_` prefix, `IsValid()`, `String()`, and `var _ fmt.Stringer = Token("")`. Wrote `/model/token_test.go` with the mint/`IsValid` round trip, acceptance of the canonical literal `t_0123456789abcdef0123456789abcdef` (which exercises all sixteen hex digits), a twelve-row rejection table, and a `String()` test. No uniqueness test: that would be a test of `crypto/rand`, not of this package.

`go test -shuffle=on ./model/...`, `go vet ./model/...` and `gofmt -l model/` all passed. `go doc ./model Token` and `go doc ./model NewToken` render the comments and resolve the `[NewToken]`, `[rand.Read]`, `[ErrorTokenExpired]`, `[ErrorTokenNotFound]` and `[fmt.Stringer]` links.

### Why

`EmailAddress` already answers every structural question — where the matcher lives, what `IsValid` returns, how `String` is asserted — so the only real work was the parts it does not answer: the entropy source, the encoding, and how to document a credential without describing the applications that will hold one.

### What worked

Checking the doc comments against `go doc` output rather than trusting the source. Reading the rendered paragraphs is what surfaced that the `IsValid` comment was making a claim the function cannot support (see Step 3).

Ignoring the error from `rand.Read` is correct on Go 1.25 and worth the one-line comment saying so, since the bare `_, _ =` is exactly the shape a reviewer is trained to flag.

### What didn't work

Nothing failed. The tests, `go vet` and `gofmt` all passed on the first run, and no command produced an error worth recording. The corrections in this branch came from the self-review in Step 3, not from a failure.

### What I learned

Go's `$` is not Perl's. Without the `(?m)` flag, Go's `regexp` anchors `$` at end of text only, and unlike Perl it does not also match before a trailing newline. So `t_…\n` is rejected by the anchor itself, with no explicit trimming needed — which is precisely why the trailing-newline row belongs in the table: it is the one row that fails if someone ever adds `(?m)`.

### What was tricky

The doc comments, not the code. The brief motivates `IsValid` by noting that HTTP layers can refuse malformed tokens without a database read, but `/CLAUDE.md` and the Go conventions forbid a package's comments from looking outward at its callers — a package does not know who imports it. The resolution was to write the library-side truth instead: the comment now says what the check proves and what it does not, and a reader in any layer can draw their own conclusion about when to call it.

`single-use` in the type comment is a description of what kind of credential a `Token` is, not a guarantee the type enforces; the sentence after it states what the type does guarantee, so the two are not confused.

### What warrants review

The wording of the `Token` doc comment, on the "single-use" point above. The rejection table is covered by Step 3.

### Future work

None beyond the `ErrorTokenUsed` sentinel already noted in Step 1.

## Step 3: Self-review the matcher, the table, and the comments

**Author:** token-builder

### Prompt Context

**Verbatim prompt:** From the same brief: "Self-review your work once implementation is done: adversarially check the regexp (anchoring, case, that every rejection-table row actually fails for the reason its label claims), the doc comments against the no-assumed-consumer-usage rule, and that the tests would catch a plausible mutation (e.g. matcher made unanchored or case-insensitive)."
**Interpretation:** Prove the table, rather than eyeballing it: each row must fail for its labelled reason, and the table as a whole must kill the ways the matcher could plausibly be broken.
**Inferred intent:** The regexp is short enough to look obviously right while being subtly wrong, so the tests are the only real defence and they need to be shown to work.

### What I did

Both halves of the review were mechanical rather than by eye, via a throwaway program in the session scratchpad (not committed).

First, each rejection row was analyzed structurally — prefix present, body length, count of non-hex and of uppercase characters — and the result compared against the row's label. Every row is rejected, and each fails for the reason it claims. Three rows deserve mention: `too short` and `too long` are pure length failures with an all-lowercase-hex body, so they isolate length from character class; `non-hex character` and `uppercase hex` are both exactly 32 body characters, so they isolate character class from length; and `doubled underscore` is 32 body characters where one is the extra `_`, which is the case that catches an underscore added to the character class.

Second, the table was run against fourteen mutated matchers. Every mutant is killed. Nine rows are the sole killer of some mutant: `empty` kills `^(t_[0-9a-f]{32})?$`, `no prefix` kills `^(t_)?[0-9a-f]{32}$`, `wrong prefix` kills `^[a-z]_[0-9a-f]{32}$`, `doubled underscore` kills `^t_[0-9a-f_]{32}$`, `too short` kills `^t_[0-9a-f]{31,32}$`, `too long` kills `^t_[0-9a-f]{32,}$`, `uppercase hex` kills both `(?i)` and `[0-9a-fA-F]`, `character before` kills the loss of `^`, and `trailing newline` kills `(?m)`. The remaining three (`non-hex character`, `character after`, `sentence`) are co-killers of the dot-instead-of-hex-class, missing-`$`, and substring-search mutants. No row is dead weight.

The doc comments were then re-read against the no-assumed-consumer-usage rule, which produced the one substantive fix below.

### Why

The brief was explicit that a subtly unanchored or case-insensitive matcher is the failure this work has to be armoured against, and an argument that the table "looks thorough" is not evidence. Mutating the matcher and watching the table catch each mutation is.

### What worked

Mutation testing paid for itself immediately. The first pass ran ten mutants and left four rows (`empty`, `wrong prefix`, `too short`, `sentence`) killing nothing, which made them look like padding. Rather than delete them, I asked what each would catch, and adding those four mutants (`^[a-z]_…`, `{31,32}`, the optional whole pattern, and a substring search) showed every one of them is a sole or joint killer. The table is unchanged, but now it is justified instead of assumed.

### What didn't work

No failures, but two fixes came out of the review:

- The `IsValid` comment read "reports whether the token has the shape `NewToken` mints, and thus whether it can be a token at all". The second clause is wrong: a well-formed string that was never minted also passes. Rewritten to "It says nothing about whether the token was ever minted, only that it could have been", which is both accurate and the honest inward-looking version of the "reject without a database read" motivation.
- The round-trip assertion was `is.True(t, token.IsValid())`, which on failure reports only that `true` was expected and hides the token that failed. Now `is.True(t, token.IsValid(), token.String())`, so a broken `NewToken` shows the malformed value it produced.

### What I learned

A rejection table's rows can each be individually valid and still leave the table as a whole untested. Asking "which mutant does this row alone catch?" is a sharper question than "is this row a plausible bad input?", and it is what turned four seemingly redundant rows into justified ones.

### What was tricky

Deciding whether `too short` and `too long` were redundant with `non-hex character`. They are not, and the mutation run is the reason: a length-range mutant passes every character-class row, and a character-class mutant passes every length row. The two axes have to be tested separately, and each row has to vary only one of them — which is why the length rows keep an all-hex body and the character-class rows keep the exact length.

### What warrants review

The `Token` type comment, as flagged in Step 2: "unguessable single-use credential" describes intent, while the type only guarantees the shape and the entropy. If that reads as overclaiming, the first sentence is the line to change.

### Future work

None. The `ErrorTokenUsed` sentinel from Step 1 remains the only follow-up, and it stays out of this branch.

## Step 4: Add the `ErrorTokenUsed` sentinel

**Author:** token-builder

### Prompt Context

**Verbatim prompt:** "Scope addition from the user (product owner), approved for this branch: also add an `ErrorTokenUsed` sentinel to `model/error.go`." The requirements that followed: the value is `Error("token used")`, "follows the existing lowercase-phrase pattern"; it keeps "the const block's alphabetical order: it goes after `ErrorTokenNotFound`"; the rationale is that "single-use token flows distinguish 'already used' from 'no such token' — they are different things to whoever is holding the link"; and it lands as "a SECOND, separate commit (it goes beyond what issue #181 specifies)" ending in `See #181` rather than `Fixes`. Everything else in the original brief still stands, including not pushing and not opening a pull request.
**Interpretation:** One line in `/model/error.go`, in its alphabetical slot, plus its own commit and this diary step.
**Inferred intent:** Close the gap the previous steps left open: `Token` can now be minted and validated, but the error vocabulary could not yet say that a well-formed, real token has already been spent.

### What I did

Added `ErrorTokenUsed = Error("token used")` to the const block in `/model/error.go`, between `ErrorTokenNotFound` and `ErrorUserInactive`. Left it without a doc comment, matching the other five sentinels — the block is introduced once by the comment on the `Error` type, and commenting only the newest constant would break that symmetry.

Confirmed the placement is genuinely alphabetical rather than merely plausible: `ErrorTokenNotFound` sorts before `ErrorTokenUsed` because `N` precedes `U`, and `ErrorTokenUsed` sorts before `ErrorUserInactive` because `Token` precedes `User`.

`gofmt -l model/`, `go vet ./model/...` and `go test -shuffle=on ./model/...` are all clean, and `git diff model/error.go` is a single added line: `ErrorEmailConflict` and `ErrorTokenNotFound` are both 18 characters and still set the alignment column, so the new constant padded to the existing width without moving any neighbouring line.

### Why

The `Token` work gave applications a credential they can mint and check the shape of, but the domain vocabulary stopped at expired and not-found. A single-use credential has a third terminal state — spent — and collapsing it into "not found" throws away the distinction between a link that never existed and one that already did its job.

### What worked

Checking the rendered diff rather than assuming gofmt would behave. The new name is shorter than the longest in the block, which is the case where alignment stays put; had it been longer, every line in the const block would have shifted and the one-line change would have become a six-line diff worth warning a reviewer about.

### What didn't work

Nothing failed. This was a one-line addition and every check passed on the first run.

### What I learned

Nothing new about Go here. The only thing worth recording is a review habit: in an aligned const block, whether a change is one line or the whole block depends entirely on how the new identifier's length compares to the current longest, so it is worth looking at the diff before describing the change to anyone.

### What was tricky

Only the judgement call about a doc comment. The rationale for this sentinel is more interesting than for its neighbours, which is a pull toward documenting it — but a comment on exactly one constant in an otherwise bare block reads as though the others are self-evident and this one is not. The rationale went into the commit message and this diary entry instead, where it does not distort the source.

### What warrants review

The value string. `"token used"` follows the lowercase-phrase pattern of its neighbours, but reads as a fragment rather than a state; `"token already used"` would be more precise at the cost of being longer than the pattern suggests. Worth a moment's thought before it becomes part of the public API.

### Future work

None. This was the follow-up flagged in Step 1, and it is now done.

## Step 5: Rephrase the `Token` doc comment so single use reads as intent

**Author:** token-builder

### Prompt Context

**Verbatim prompt:** In answer to the overclaiming question raised in Steps 2 and 3: "you're right that it overclaims — single use is what applications enforce, not what the type guarantees. Rephrase the opening sentence so single-use reads as intended use rather than enforced property, e.g. 'Token is an unguessable credential for single-use flows such as magic-link login.' (Naming magic-link login generically is fine — the public issue does.) Fold this into the commit below or amend as you see fit, but keep fa81d2e's message accurate."
**Interpretation:** Change the first line of the `Token` doc comment so it describes the flows the credential is built for, instead of asserting a property the type does not enforce.
**Inferred intent:** A doc comment that a reader cannot mistake for a guarantee, so nobody assumes the type is doing spend-tracking on their behalf.

### What I did

Rewrote the opening line of the `Token` comment in `/model/token.go` from "Token is an unguessable single-use credential" to "Token is an unguessable credential for single-use flows such as magic-link login", using the suggested wording as given.

Also extended the comment's last line from "See [ErrorTokenExpired] and [ErrorTokenNotFound]" to include `[ErrorTokenUsed]`, which did not exist when that line was written in Step 2. This was not requested, but leaving the newest token sentinel out of the list would have made the comment quietly incomplete the moment Step 4 landed, and `ErrorTokenUsed` is the sentinel that most directly corroborates the "single-use flows" phrasing in the new opening line.

`gofmt -l model/`, `go vet ./model/...` and `go test -shuffle=on ./model/...` are clean, and `go doc ./model Token` confirms the paragraph reads correctly and that all three sentinel links resolve.

Committed as a third commit rather than folding the change into either existing one. The prompt allowed amending, but both alternatives are worse. Amending fa81d2e would rewrite the commit that carries diary Steps 2 and 3, whose "What was tricky" and "What warrants review" sections describe the old wording as an open question — the amended commit would contain a diary flagging a concern its own code had already resolved. Folding it into the `ErrorTokenUsed` commit would put a `Token` doc change under a summary line about an error sentinel. A third commit leaves fa81d2e's message accurate, which was the stated constraint, and keeps each commit about one thing.

### Why

The distinction is a real one and not pedantry. Nothing in `/model/token.go` tracks whether a token has been spent; that is a storage concern, and a comment asserting "single-use credential" invites a reader to assume otherwise. Describing the flow the credential is built for says the same useful thing without making a promise the code does not keep.

### What worked

Raising the question in Steps 2 and 3 instead of quietly writing what the brief said. The wording was specified in the original requirements, so the alternative was to implement it and say nothing — and the correction came back on the first round precisely because the concern was recorded where a reviewer would see it.

### What didn't work

Nothing failed. This was a two-line comment change and every check passed on the first run.

### What I learned

Naming a concrete flow can be more honest than an abstract adjective. "Single-use credential" reads as a property of the type; "credential for single-use flows" reads as the purpose it serves, and the second is both weaker as a claim and more informative to someone deciding whether they need this type.

### What was tricky

Only the commit-shape decision above, which came down to which artifact must stay truthful. Commit messages and diary entries are both historical records, so the option that avoided rewriting either one won.

### What warrants review

Whether "such as magic-link login" belongs in a library comment at all. The Go conventions say a package's comments should not look outward at its callers, and this names a concrete application-level flow. The lead approved it explicitly and the public issue uses the same phrasing, so it stands — but it is the one line in this file that a strict reading of that rule would question.

### Future work

None. All three items from the brief and both scope additions are done, and the branch is ready for review.
