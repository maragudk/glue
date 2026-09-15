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
