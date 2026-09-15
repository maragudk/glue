# Diary: Render error and not-found pages with the request's PageProps

Fix for issue #209. `html.ErrorPage` and `html.NotFoundPage` build a fresh `PageProps{Title: ...}`, so
the page function never sees `UserID`, `Permissions`, `R`, or `Ctx` for the request, and any layout
whose chrome varies on those renders the anonymous variant to a signed-in user on every error page.
The same change already exists on the open CSP-nonce PR #204 (where the nonce was dropped the same
way); this lifts it onto its own branch without touching #204.

## Step 1: Refine the requirements and hand off to a builder

**Author:** main (lead)

### Prompt Context

**Verbatim prompt:** "See issue 209. Is it easy to fix?" followed by "Lift into its own PR, don't touch 204 for now"
**Interpretation:** Assess issue #209, then ship the fix on its own PR, leaving PR #204 as-is.
**Inferred intent:** Get the props bug fixed on `main` independently of the larger CSP-nonce work, so it isn't held hostage by that PR's review.

### What I did

Read issue #209 and grepped the repo. Found the fix already implemented on branch `worktree-csp-nonce`
(PR #204): `/html/error.go` takes a `PageProps` and sets only `Title`; `/http/error.go` and
`/http/auth.go` pass `GetProps(w, r)` / the handler's `props`; tests in `/html/error_test.go` and
`TestLogoutErrorPage` in `/http/auth_test.go`. Created worktree `error-page-props` on branch
`worktree-error-page-props` from `main` and spawned a builder with the requirements below.

### Why

The change is small and mechanical, and Markus wants it decoupled from #204. Lifting it as a fresh
implementation on `main` (rather than cherry-picking) avoids pulling in the `Nonce` field, which only
exists on the nonce branch.

### What worked

The nonce branch is a ready-made reference for the API shape and the tests.

### What didn't work

Nothing failed at this stage.

### What I learned

Both the nonce and the user/permissions bugs have the same root cause: the error-page helpers construct
their own props instead of receiving the request's.

### What was tricky

The nonce-branch tests assert on `props.Nonce`, which does not exist on `main`; the builder must drop
those assertions rather than copy the tests verbatim.

### What warrants review

That the new signatures set only `Title` and pass everything else through untouched, and that both
internal callers thread the request's props.

### Future work

When #204 is rebased on this, its error-page hunks will conflict trivially; resolving to this branch's
version plus the nonce assertions in tests is the correct merge.

## Step 2: Implement the props pass-through and self-review it

**Author:** error-page-props-builder

### Prompt Context

**Verbatim prompt:** "Fix GitHub issue #209: 'Error and not-found pages render without the request's PageProps'. [...] Change signatures to `html.ErrorPage(page PageFunc, props PageProps) Node` and `html.NotFoundPage(page PageFunc, props PageProps) Node`. Each sets only `props.Title` ('Something went wrong' / 'Not found') and passes the rest of the props through to `page` unchanged."
**Interpretation:** Re-implement the nonce branch's error-page fix on top of `main`, dropping everything nonce-related, with tests for both the `html` helpers and the two internal callers.
**Inferred intent:** Get the signed-in chrome rendering correctly on error pages, on a branch that can merge to `main` without dragging PR #204 along.

### What I did

Worked red/green. Wrote `/html/error_test.go` first against the new two-argument signatures and watched it fail to
compile (`too many arguments in call to html.ErrorPage`), then changed `/html/error.go` so both functions take
`props PageProps`, assign `props.Title`, and pass the rest through. Added doc comments on both, with
`[PageProps.Title]` and `[ErrorPage]` doclinks.

Then the callers: `/http/error.go` `NotFound` now passes `GetProps(w, r)`, and `/http/auth.go` `Logout` passes the
handler's own `props` on the destroy-error path. Both were caught by the compiler first (`not enough arguments in
call to html.ErrorPage`), which is the nicest kind of red. Added `/http/error_test.go` with `TestNotFound` (no such
file existed on `main`) and `TestLogoutErrorPage` in `/http/auth_test.go`.

### Why

The signature change is the whole fix: the helpers had no way to see the request, so they invented props. Taking
props as a parameter and overriding just the title is the smallest change that lets every other field survive.

### What worked

Mutation testing as a way to check the tests rather than trust them. Reverting `/html/error.go` to
`props = PageProps{Title: ...}` and re-running made all four new tests fail with
`Expected "u_123", but got "<nil>" (type *model.UserID)`, which proved they actually pin the bug down.

### What didn't work

`go test ./...` fails in `/sql` and `/postgrestest` with
`dial tcp 127.0.0.1:5433: connect: connection refused` — no local Postgres running. Pre-existing and unrelated;
`./html/...` and `./http/...` are green, as are `go vet ./...`, `gofmt -l .` and `golangci-lint run`.

### What I learned

chi's `NotFound` handler only picks up a group's middleware when it is registered on that group's inline mux. I
verified this with a throwaway test in `/http`: a `NotFound` registered on the root mux, with a marker middleware
installed inside a later `Group`, reported `status=404 groupMiddlewareRan=false`. That matters here — see below.

### What was tricky

Two things in the tests. First, `PageProps` is not `comparable` (it has a `Permissions` slice), so the pass-through
has to be asserted field by field rather than with one `is.Equal` on the struct. Second, `got.W` is an
`http.ResponseWriter` while the test holds a `*httptest.ResponseRecorder`, so `is.Equal`'s type inference fails;
`is.True(t, got.W == rec, ...)` is the way through.

### What warrants review

The `Description` pass-through. `Title` is overridden but `Description` is not, so a consumer that set a
page-specific description and then returns `html.ErrorPage(page, props)` renders that description on the error page.
This is exactly what the requirements asked for ("sets only `props.Title`"), and both reviewers independently
questioned it, so it deserves a conscious yes or no rather than a default.

### Future work

`r.NotFound(NotFound(s.htmlPage))` in `/http/routes.go` is registered on the root router, above the HTML group that
installs `SM.LoadAndSave`, `Authenticate` and `SavePermissionsInContext`. Given the chi behaviour above, the
built-in `Server`'s 404 page still gets `UserID == nil` and `Permissions == nil` — so for glue's own server this
fix is only half-effective. Moving the registration into the HTML group would fix it, but it makes every 404 load a
session and hit the database for authentication, and changes which security headers 404s carry, so it is a
deliberate decision and not mine to make here. Consumers wiring their own router and registering `NotFound` inside
their auth group get the full benefit today. Separately, `Authorize` in `/http/auth.go` still answers 403 with a
plain-text `http.Error` instead of a rendered page — same family as #209, probably its own issue.

### Self-review

Ran the `code-review` skill: two competing reviewers over the diff. Neither found a correctness bug in the change
itself, and both explicitly cleared the two targeted checks (no comment context leakage, no new single-use
constants). They agreed on one real gap and several nitpicks, and I addressed the following.

The consensus finding was a test gap: `Ctx`, `W` and `HideAuth` were never asserted, and one reviewer demonstrated
by mutation that rebuilding the struct while copying only `Description`, `R`, `UserID` and `Permissions` passed the
entire suite. Issue #209 names `Ctx` and `W` explicitly, so this was worth closing. `/html/error_test.go` now
populates every field of `PageProps` and asserts each one; re-running that same mutation now fails. I also pulled
the two duplicated subtests into a shared `testPagePassesPropsThrough` helper — both reviewers flagged the
copy-paste — while keeping `TestErrorPage` and `TestNotFoundPage` as separate top-level tests, since collapsing
them into one table would have lost the one-test-per-function convention. In `/http`, `TestNotFound` and
`TestLogoutErrorPage` now also put permissions on the request context and assert them, and both check the rendered
`<h1>`, which they previously did not.

I did not take three suggestions. Resetting `Description` contradicts the stated requirement, so it is a question
for the lead rather than a unilateral change (recorded above). Folding `TestLogoutErrorPage` into `TestLogout` was
requested by name in the requirements, so it stays. And the `routes.go` registration is out of scope, as described
under Future work.
