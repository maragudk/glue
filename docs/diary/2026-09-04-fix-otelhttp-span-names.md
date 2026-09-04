# Diary: Fix the failing compatibility `deps: latest` legs

Goal: get the nightly `compatibility.yml` workflow green again on all four legs. The two `deps: latest`
legs, which run `go get -u -t ./...` before the test suite, had failed every night with the same five
subtests in `maragu.dev/glue/http`:

    auth_test.go:734: no ended span named GET /, recorded [GET]
    auth_test.go:861: (same)

## Step 1: Find the cause, fix the naming, and cover it

**Author:** glue-otel-fix (sub-agent)

### Prompt Context

**Verbatim prompt:** "yes please" -- in reply to an offer to investigate and fix the failing
compatibility `deps: latest` legs.
**Interpretation:** Reproduce the failure locally, identify exactly which dependency changed and how,
fix the assumption in glue's own code rather than relaxing the assertions, and open a PR with the
compatibility workflow run to prove all four legs pass.
**Inferred intent:** A nightly job which is always red tells nobody anything. Make it mean something
again, and make sure the fix is the right one rather than the one which turns the colour green.

### What I did

Reproduced on a branch off `main`: `go get -u -t ./...` then
`go test -race -shuffle on -tags sqlite_fts5,sqlite_math_functions,sqlite_foreign_keys ./http/...`
failed with exactly the five subtests from CI; the same command on the locked `go.mod` passed. That
already settles which side changed, since the glue code was identical in both runs.

The dependency is `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`, which
`go get -u` moves from the locked v0.65.0 to v0.71.0. Reading both versions in the module cache:
v0.65.0 installs `defaultHandlerFormatter`, which returns the `operation` string handed to
`otelhttp.NewHandler`. v0.71.0 has no such default -- `NewMiddleware` falls back to
`h.semconv.SpanName(r)`, which builds `{method} {route}` from `http.Request.Pattern` and returns
`{method}` alone when there is no pattern. The upstream changelog entry is under
`1.44.0/2.5.1/0.69.0`, dated 2026-05-28 (PR #8871):

> The default span name formatter in `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp`
> now conforms to the OpenTelemetry HTTP semantic conventions for server span names.
> The default span name is now `{method} {route}` (e.g. `GET /foo/{id}`) when a route pattern is
> available, or `{method}` (e.g. `GET`) otherwise.

Three changes went in, in `/http/otel.go`, `/http/otel_test.go` and `/http/auth_test.go`, as commit
`1a3e8a6`.

First, `spanName` became a pure function of a method and a route, and a new `routePattern` decides the
route. It reads `chi.Context.RoutePattern` as before, and falls back to `http.Request.Pattern` when
chi has nothing, dropping everything before the first `/` because a `http.ServeMux` pattern can name a
method and a host in front of the path. So `http.OpenTelemetry` above a standard library mux now names
spans `GET /things/{id}` and sets `http.route`, where it used to produce a bare `GET`. The defer in the
middleware computes the route once and uses it for both the name and the attribute.

Second, `routePattern` refuses to read `http.Request.Pattern` for a CONNECT request. See "What was
tricky".

Third, the two failing tests in `/http/auth_test.go` stopped looking their span up by name. They pass
a plain `otelhttp.NewHandler(h, "GET /")` to stand in for tracing middleware which is not glue's own,
and they are about where the auth middleware writes its attributes, not about what otelhttp calls the
span. They now take `lastEndedSpan`.

`/http/otel_test.go` gained a table for a standard library mux below the middleware -- path-only,
method-and-path, host-method-and-path and root patterns, plus a no-match and a method-not-allowed row
which pin the bare `GET` and the absence of `http.route` -- a subtest pinning that a chi pattern beats
one on the request, and a subtest for the CONNECT case.

### Why

The failing assertion was glue's, not otelhttp's. `endedSpanNamed(t, sr, "GET /")` under a handler with
no router below it only ever passed because otelhttp echoed the operation string; semantic conventions
say a request which matched no route is named after its method alone, so `GET` is the correct name for
that scaffolding and the test was pinning an implementation detail of a dependency.

The `routePattern` fallback is the part which actually makes server spans better rather than merely
green. `http.OpenTelemetry` is exported and usable outside a chi router, and until now it ignored the
one place the standard library reports a match.

### What worked

Bisecting by dependency rather than by commit. Running the suite twice over identical source, once
locked and once upgraded, took two minutes and removed any doubt about which side had moved.

Red-first on the new naming: the standard-library-mux table failed with
`Expected "GET /things/{id}", but got "GET"` on all four rows before `routePattern` existed.

A mutation sweep over the finished code. Deleting the CONNECT guard, deleting the `http.Request.Pattern`
fallback, and swapping the two branches of `routePattern` each turned a test red -- after the precedence
test was fixed, which is where the sweep earned its keep. See "What didn't work".

### What didn't work

The first version of the precedence subtest proved nothing. It mounted a `http.ServeMux` registered
for `GET /{id}` at `/leaf` on a chi router and asserted the span was named `GET /leaf/*`. It passed --
and went on passing when the two branches of `routePattern` were swapped. Instrumenting the middleware
showed why:

    at defer: r.Pattern=  chi= /leaf/*

chi's `Mount` does not strip the prefix from `r.URL.Path`, so the mounted mux was asked for `/leaf/42`,
matched nothing, and `net/http` set `r.Pattern` back to the empty string on the way out -- overwriting
the `/leaf/*` chi had put there. There was never a competing pattern to prefer. Registering the leaf
mux for the full `GET /leaf/{id}` makes both sources non-empty, and the swap mutation now fails.

The `deps: latest` legs also cannot be fully reproduced on this machine: Docker is unavailable, so
`postgrestest` and `sql` fail to connect on port 5433. Only `./http/...` and the other container-free
packages were run locally.

### What I learned

The failure did not start when otelhttp changed. The new default formatter shipped on 2026-05-28; the
nightly went red on 2026-08-25, the morning after `abc7da1` ("Write middleware telemetry on the current
span", merged in #196) added `endedSpanNamed(t, sr, "GET /")` to `/http/auth_test.go`. The tests were
written against the locked otelhttp and contradicted upstream behaviour which was already three months
old. The task described the failures as starting 2026-08-31; `gh run list` shows 2026-08-25.

chi v5.3.1 *does* set `http.Request.Pattern`, at `mux.go:481`, right before it calls the matched
handler. An earlier version of the comment in `/http/otel.go` claimed it never does. What is true is
narrower: the value does not survive a middleware which replaces the request, and glue's own stack
replaces it twice, at `scs.LoadAndSave` and in `Authenticate`. That is the real reason the middleware
sets the name itself instead of leaving it to otelhttp's second formatter run, and the reason chi's
route context comes first in `routePattern` -- a context outlives a request being swapped, a struct
field does not.

`net/http` mutates `r.Pattern` in place in `ServeMux.ServeHTTP` rather than on a copy, which is what
makes the fallback reach back up to the middleware at all -- and also what let the 404 above erase a
pattern set further up.

### What was tricky

The CONNECT hole, which the fallback introduced and which a review caught. `ServeMux.findHandler` has
one branch which returns a request path instead of a registered pattern: a CONNECT request for a
registered subtree gets a trailing-slash redirect, and CONNECT paths are deliberately not canonicalized
first, so the path arrives as the client wrote it. Confirmed over a real socket against
`httptest.NewServer(gluehttp.OpenTelemetry(mux))` with `/things/{id}/` registered:

    CONNECT /things/ATTACKER-1234 HTTP/1.1
    => span "CONNECT /things/ATTACKER-1234/", http.route=/things/ATTACKER-1234/

One request per distinct span name, unauthenticated, unbounded. Every other redirect branch in
`findHandler` returns `n.pattern.String()` or the empty string, so the guard is exactly one method wide.
Worth knowing that otelhttp v0.71.0 has the same hole in its own default formatter, which reads
`r.Pattern` unconditionally.

Choosing the precedence between the two sources is a real trade-off rather than an obvious win. Where
both are populated, chi's pattern can be the coarser of the two -- `/leaf/*` against the mounted mux's
`/leaf/{id}`. Chi still comes first, because it is the source which is still there when a middleware
has replaced the request, which is the common case in glue's own stack. The subtest says so and the
doc comment says so.

### What warrants review

`routePattern`'s precedence is the judgement call. The alternative -- read `http.Request.Pattern` first
and fall back to chi -- gives a more precise route in the one topology above and is otherwise identical,
at the cost of being silently wrong whenever something between the middleware and the router replaces
the request.

Whether the `http.Request.Pattern` fallback belongs in this change at all is the second one. It is not
needed to make CI green; reverting `/http/otel.go` to `main` and keeping only the `auth_test.go` change
passes on both dependency versions. It is here because it is what makes spans better rather than merely
green, and because it is the reason the CONNECT guard exists.

`go.mod` is untouched. The fix works on both v0.65.0 and v0.71.0, so nothing needed bumping;
`go mod tidy && git diff --exit-code go.mod go.sum` is clean.

### Future work

`spanName` interpolates `r.Method` raw, so a client can mint an unbounded number of distinct span
names by sending arbitrary RFC-token methods -- `net/http` accepts them, and a probe produced a span
named `WEIRDMETHOD-ABCDEF /things/{id}`. Semantic conventions say an unrecognised method collapses to
`HTTP` in the span name, and otelhttp does exactly that in `internal/semconv/server.go`. This predates
the change here and is the same class of problem as the CONNECT hole, but it changes behaviour for
legitimate uncommon methods (WebDAV's `PROPFIND` and friends would become `HTTP`), so it wants its own
issue and its own decision rather than riding along with a compatibility fix.

A reviewer suggested writing the resolved route back to `r.Pattern` in the middleware's defer. glue
holds the same `*http.Request` otelhttp does, so the assignment would stay inside otelhttp's own copy,
and it would give `http.server.request.duration` an `http.route` in the topologies where a
request-replacing middleware currently swallows it. More invasive than this change wanted to be, and
worth its own look.

## Step 2: Self-review and the Go version regression

**Author:** glue-otel-fix (sub-agent)

### Prompt Context

**Verbatim prompt:** "Once the implementation is done, self-review before handing back to the lead.
Work in two phases: first, review the code you wrote -- read the diff, check logic, look for missing
edge cases and test coverage gaps; second, run automated checks (tests and linters). Be honest about
real issues; if everything looks good, say so rather than inventing problems."
**Interpretation:** Run the `code-review` skill over the change, act on what two independent reviewers
agree on and on anything serious either one finds alone, then prove the result with tests and linters.
**Inferred intent:** A first pass which turned CI green is not the same as a change which is right.
Find what the first pass assumed rather than checked.

### What I did

Dispatched two competing reviewers over the working tree. They converged on six findings, all acted on:

The comment claiming a chi router never sets `http.Request.Pattern` was false, and both reviewers
pointed at `chi/v5@v5.3.1/mux.go:481`. The true and sufficient reason otelhttp's second formatter run
cannot be relied on is the request-replacing middleware, so the false clause simply went.

The `http.ServeMux` scaffolding the first pass added to `/http/auth_test.go` went away again. Both
reviewers independently showed it was unnecessary and that its comment was wrong for the pinned
otelhttp v0.65.0, where the operation string is still what names the span. The tests take
`lastEndedSpan` now.

The sentence about what otelhttp's default "has changed across otelhttp versions" was history
narration about a dependency's internals, which `AGENTS.md` bans. `routePattern`'s doc described the
caller's wiring ("the router below", "a mux further down") from inside a function which is a pure
function of a request. Both were rewritten. `http.route` "stays off entirely when nothing matched" had
gone stale against its own code, and now reads "when no router matched".

`routePattern` was being computed twice in the defer; the route is hoisted into a variable and
`spanName` became a pure function of a method and a route.

One reviewer alone found the CONNECT hole, which was serious enough to act on without corroboration
and is written up under "What was tricky" in Step 1.

Then a mutation sweep over the finished code, and `make lint` plus the suite under `-race -shuffle on`
with the sqlite tags, on both the locked and the upgraded dependency set.

### Why

Two reviewers agreeing is a much stronger signal than one, and the disagreement was informative too:
one asserted that `http.Request.Pattern` can only ever hold registration-time literals, and the other
produced the socket transcript showing a CONNECT request putting a client-chosen path there. The
second reviewer was right, and the guard exists because of it.

### What worked

The mutation sweep, again. Deleting the CONNECT guard and deleting the fallback both turned tests red
immediately. Swapping the two branches of `routePattern` did not, which is how the broken precedence
test was found -- it would otherwise have shipped as coverage which proved nothing.

### What didn't work

The compatibility run on the branch came back three green and one red, on `(go.mod, locked)`:

    otel_test.go:182: Expected "307", but got "301" (type int)

My own CONNECT test, not the fix. `GOTOOLCHAIN=go1.25.0 go test ./http/...` reproduced it locally in
one command: `http.ServeMux` answers the trailing-slash redirect with 301 on Go 1.25 and 307 on Go 1.26
and later, and the local toolchain here is 1.27, which is also what CI's `stable` leg runs. So the test
passed in all four dependency-and-toolchain combinations I had actually tried, and failed in the one I
had not. The redirect still has to happen for the test to mean anything, so it asserts the `Location`
header now, which is the same on both.

The lesson generalises: this repository's compatibility matrix is two-dimensional, and running only
`go get -u -t ./...` against one toolchain covers half of it. All four combinations are cheap to run
locally with `GOTOOLCHAIN`.

### What I learned

Reviewers disagreeing on a factual claim is worth more than either verdict on its own. Both claims
were checkable, one transcript settled it, and the losing claim was the reassuring one.

### What was tricky

Deciding how much of what the reviewers found belonged in this change. The raw `r.Method` in span
names is the same class of bug as the CONNECT hole and both reviewers flagged it, but it predates this
work and collapsing unrecognised methods to `HTTP` would change behaviour for legitimate ones. It is
in "Future work" in Step 1 and in the PR rather than in the diff, on the grounds that a compatibility
fix which also changes span names for WebDAV clients is harder to review and harder to revert.

### What warrants review

Same as Step 1: the precedence in `routePattern`, and whether the `http.Request.Pattern` fallback
belongs in a compatibility fix at all.

### Future work

Unchanged from Step 1: the `r.Method` normalisation, and writing the resolved route back to
`r.Pattern` so otelhttp's own metrics carry `http.route`.
