# Diary: Adopt the shared reusable workflows

Convert glue's three GitHub Actions workflows from inline jobs to callers of the reusable workflows
in `maragudk/workflows`, following the organisation's starter templates. Pure CI work: no Go changes.

## Step 1: Convert the three workflows to callers

**Author:** glue-builder

### Prompt Context

**Verbatim prompt:** Can we do some of the same workflow conversions in ../glue?
**Interpretation:** Replace the inline jobs in glue's `ci.yml`, `compatibility.yml`, and `security.yml`
with calls to the shared reusable workflows, matching the starter templates and the conversion already
done elsewhere.
**Inferred intent:** Stop maintaining glue's CI definitions by hand. One shared definition per job kind
means a fix to the tags, the race detector, or the service containers lands everywhere at once.

### What I did
Read the four reusable workflows that could apply (`lint.yml`, `test.yml`, `compatibility.yml`,
`security.yml`) and the three starter templates, then rewrote glue's callers in `/.github/workflows/`:

- `/.github/workflows/ci.yml` now calls `lint.yml` and `test.yml`, passing `s3: true` and
  `postgres: true` to `test.yml`. It gains a top-level `permissions: contents: read`, which the file
  did not have before.
- `/.github/workflows/compatibility.yml` calls `compatibility.yml` with the same two inputs.
- `/.github/workflows/security.yml` is a pure caller of `security.yml`, with `contents: read` and the
  `issues: write` the shared workflow needs to open and close its own vulnerability issue. It also
  picks up the concurrency group the template defines.

The templates also define a `build` job for CI. I left it out: glue has no Dockerfile, so
`build.yml` (and `cd.yml`) do not apply. `dependabot.yml`, the `Makefile`, and `docker-compose.yml`
were left untouched.

Before committing I checked that glue survives the two new things the shared test workflow does that
the old inline job did not. `go mod tidy && git diff --exit-code go.mod go.sum` was already clean, so
the drift check passes. The `-race` flag and the SQLite build tags were left for CI to prove.

Then: branch `adopt-shared-workflows`, push, and PR #205 against `main`, with a dispatched
`Compatibility` run on the branch for the legs that only run on a schedule. The validation runs are
[CI 33849451485](https://github.com/maragudk/glue/actions/runs/33849451485),
[Security 33849451418](https://github.com/maragudk/glue/actions/runs/33849451418), and
[Compatibility 33849455165](https://github.com/maragudk/glue/actions/runs/33849455165).

Self-review before opening the PR was a mechanical diff of each converted file against its starter
template with `$default-branch` substituted. `security.yml` came out byte-identical; `ci.yml` and
`compatibility.yml` differed only by the two intended `with:` inputs and the dropped commented-out
`build` job. Nothing else drifted.

### Why
The service-container blocks were duplicated verbatim between glue's `ci.yml` and its
`compatibility.yml`, and again in every other repository doing the same thing. The reusable workflows
already encode the exact ports, images, and throwaway credentials glue's tests hardcode, so the
conversion is a straight substitution rather than a behaviour change — plus three improvements glue
gets for free.

### What worked
The inputs lined up with glue's tests without any adaptation. `/postgrestest/helper.go:95` defaults to
`postgres://test:test@localhost:5433/<name>` and `/s3test/bucket.go:100` sets
`AWS_ENDPOINT_URL=http://localhost:7072`; both match what `postgres: true` and `s3: true` start. That
matters more than it looks, because a called workflow inherits neither the caller's `env` nor its
secrets — hardcoded defaults are the only thing that could have worked here.

The SQLite build tags turn out to be a real gain rather than dead weight: glue depends on
`github.com/mattn/go-sqlite3`, the cgo driver those tags are for, so `sqlite_fts5`,
`sqlite_math_functions`, and `sqlite_foreign_keys` now actually compile in.

### What didn't work
The dispatched `Compatibility` run failed, but not because of the conversion. Both `deps: latest` legs
fail with

```
--- FAIL: TestMiddlewareErrors/Authenticate_should_keep_the_error_description_under_an_otelhttp_handler
    auth_test.go:734: no ended span named GET /, recorded [GET]
```

and the same at `auth_test.go:861`. That is the pre-existing daily failure on `main` since 2026-08-31:
an upstream `otelhttp` change renamed the server span from `GET /` to `GET`, and `go get -u -t ./...`
pulls it in. I filtered every failing line in the log down to distinct messages and got exactly those
two — no data races, no new failures from `-race` or the build tags — so the conversion adds nothing.
Left alone deliberately; it needs an `/http/auth_test.go` fix, not a CI one.

### What I learned
The shared `test.yml` gives its Postgres service an explicit `--health-cmd pg_isready` option, because
the `postgres` image ships no `HEALTHCHECK` of its own. Glue's old inline job had none, so it raced the
database on every run and only got away with it because Go compiles slowly enough to hide the gap. The
shared version waits properly.

### What was tricky
Nothing about the YAML. The sharp edge is outside the repository: the branch ruleset changes meaning
under the conversion. See "What warrants review".

### What warrants review
**The branch ruleset needs a matching update, and I deliberately did not make it.** The ruleset on
`main` requires the status checks `Test` and `Lint`. Those names came from the `name:` fields on the
old inline jobs. A caller job reports as `<caller job id> / <called job id>`, so the same checks now
arrive as `test / test` and `lint / lint` — and `govulncheck / govulncheck` for security. The required
contexts therefore never report at all: PR #205 sits at `mergeStateStatus: BLOCKED` with `Test` and
`Lint` permanently expected. The ruleset has to name the new contexts before this can merge, and every
other repository doing this conversion will hit the same wall.

Otherwise, the thing to check is that the shared workflow really did what the inline one did. The CI
`test / test` log shows both containers created —
`docker create ... -p 5433:5432 --health-cmd pg_isready ... postgres:18` and
`docker create ... -p 7072:7070 ... versity/versitygw:latest` — followed by
`go test -race -shuffle on -tags sqlite_fts5,sqlite_math_functions,sqlite_foreign_keys ./...`, all
green. Compatibility's two `locked` legs are green; the two `latest` legs carry only the known
upstream failure above.

### Future work
Glue's `docker-compose.yml` still pins `postgres:17` while the shared workflow now runs `postgres:18`,
so local test runs and CI are one major version apart. Worth aligning, but deliberately out of scope
here — the brief was to leave `docker-compose.yml` alone.
