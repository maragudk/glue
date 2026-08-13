# Diary: update go-sqlite3 to pick up the SQLite WAL-reset bug fix

Tailscale published https://tailscale.com/blog/sqlite-wal-reset-bug, describing a
16-year-old data race in SQLite between checkpointing and a concurrent write
transaction. When triggered, pages that appear committed are silently never copied
from the WAL into the main database file, causing permanent data loss and
corruption. The fix landed in SQLite 3.51.3 and 3.53.x.

glue depends on `github.com/mattn/go-sqlite3`, which bundles the SQLite C
amalgamation, and glue runs SQLite in WAL mode, so the bug is relevant here. The
goal of this task is to update the dependency to a version bundling a fixed SQLite.

## Step 1: verify the dependency is affected and scope the update

**Author:** main (lead)

### Prompt Context

**Verbatim prompt:** "See https://tailscale.com/blog/sqlite-wal-reset-bug . Check whether our sqlite dep is up to date, and create a PR if not"

**Interpretation:** Check whether glue's SQLite driver dependency bundles a SQLite version with the WAL-reset bug fix, and if not, update it and open a PR.

**Inferred intent:** Protect applications built on glue from a known SQLite corruption bug by keeping the bundled SQLite current.

### What I did

Read the blog post and `/go.mod`. glue pins `github.com/mattn/go-sqlite3 v1.14.34`.
Downloaded both that version and the latest (`v1.14.49`) into the module cache and
grepped `sqlite3-binding.c` for `SQLITE_VERSION`:

- v1.14.34 bundles SQLite 3.51.2 — affected by the bug
- v1.14.49 bundles SQLite 3.53.4 — contains the fix (plus 3.53.0's self-healing index feature)

### Why

The blog post names SQLite 3.51.3 as the minimal fixed release, so the check is
simply whether the bundled amalgamation is at or past that. It is not, so an
update is warranted.

### What worked

`go list -m -versions github.com/mattn/go-sqlite3` plus grepping the module cache
for `#define SQLITE_VERSION` answered the question in two commands, no guessing.

### What didn't work

Nothing failed in this step.

### What I learned

`mattn/go-sqlite3` release versions don't encode the bundled SQLite version, so
the only reliable way to map driver version to SQLite version is to inspect
`sqlite3-binding.c` in the module source.

### What was tricky

The blog post's version story is slightly confusing: 3.52.0 first carried the fix
but was withdrawn over an unrelated stale-expression-index issue; 3.51.3 is the
minimal patch release, and 3.53.x carries it forward. The takeaway for us is just
"3.53.4 is safe".

### What warrants review

The update itself is a version bump in `/go.mod` and `/go.sum`. Review is mostly
confirming the full test suite passes against the new bundled SQLite, since the
amalgamation jumps 3.51.2 → 3.53.4.

### Future work

Consider whether glue's checkpointing settings (we use WAL mode defaults) deserve
a second look in light of the blog post; Tailscale hit the bug often because they
checkpoint aggressively.

## Step 2: perform the update and validate it

**Author:** sqlite-bumper (sub-agent)

### Prompt Context

**Verbatim prompt:** "Update `github.com/mattn/go-sqlite3` from v1.14.34 to v1.14.49 and open a PR."

**Interpretation:** Carry out the bump scoped in Step 1, confirm the resolved module
really bundles SQLite 3.53.4, run the full test suite and linters, write this diary
step, and open a PR against main.

**Inferred intent:** Land the fix with enough validation that a reviewer can trust
the amalgamation jump 3.51.2 → 3.53.4 didn't change behaviour glue depends on.

### What I did

Ran `go get github.com/mattn/go-sqlite3@v1.14.49 && go mod tidy`. The result touches
only `/go.mod` and `/go.sum`, three lines total — no transitive dependency moved,
so the "only this dependency" constraint held without any manual pruning.

Verified the bundled amalgamation two ways. Statically, `#define SQLITE_VERSION` in
`sqlite3-binding.c` in the module cache reads `"3.53.4"` (`SQLITE_VERSION_NUMBER
3053004`). Dynamically, a throwaway program outside the repo that opens a
`:memory:` database through the driver and selects `sqlite_version()` also reports
`3.53.4`, with `sqlite_source_id()` dated 2026-07-24. The runtime check matters
because cgo can be pointed at a system libsqlite3 via build tags; this confirms the
build actually compiles the vendored amalgamation.

Confirmed the premise still holds in code: `/sql/helper.go:94` opens SQLite with
`?_journal=WAL&_timeout=5000&_fk=true&_txlock=immediate`, so every glue-backed
SQLite database runs in WAL mode and was exposed to the bug.

`make test` passes across all packages. `make lint` reports `0 issues.` Beyond the
required runs, I re-ran the SQLite-touching packages (`/sql`, `/sqlitetest`,
`/sqlitestore`, `/jobs`, `/model`) with `-race -count=3 -shuffle on`; all clean.

### Why

The static grep proves what's on disk; the runtime query proves what got linked.
Only together do they establish that glue is actually running fixed SQLite. The
extra race runs target the packages whose behaviour depends on the changed C code,
since a checkpointing/locking fix is exactly the kind of change that would surface
as intermittent failure rather than a hard break.

### What worked

Scoping the bump to a single `go get` and letting `go mod tidy` confirm nothing else
needed to move. The three-line diff is trivially reviewable, which is the right shape
for a dependency bump whose risk lives entirely in the vendored C.

### What didn't work

`make test-up` failed:

```
Error response from daemon: failed to set up container networking: driver failed programming external connectivity on endpoint update-go-sqlite3-wal-reset-bug-postgres-test-1 (5d821d6d…): Bind for 0.0.0.0:5433 failed: port is already allocated
make: *** [test-up] Error 1
```

`docker compose ps` from this worktree showed no containers, which made it look like
the test dependencies were down. They weren't: Compose derives its project name from
the working directory, so the worktree is a *different* project from the main
checkout, and `ps` scoped to it saw nothing while `glue-postgres-test-1` (main
checkout, 12 days up) still held port 5433. `docker ps` unscoped showed the truth.
I ran `make test-down` to remove the half-created worktree project and let the tests
use the already-running containers on the same ports.

### What I learned

Docker Compose project scoping is a trap when working from git worktrees: every
worktree gets its own project name from its directory, so `docker compose ps` and
`make test-up`/`test-down` operate on a namespace that is empty even when the ports
they need are occupied by an identical stack. Reach for unscoped `docker ps` before
concluding the test dependencies aren't running.

Also worth recording: go-sqlite3 raised its own `go` directive from 1.19 to 1.21
between these versions, which is why `/go.sum` shows a changed `/go.mod` hash line
as well as a changed module hash. glue requires go 1.25.0, so this is a non-event —
but the second changed `go.sum` line is otherwise easy to misread as something
unexpected sneaking in.

### What was tricky

Nothing in the update itself. The only friction was the misleading empty
`docker compose ps` described above, which cost a couple of minutes of believing
the test containers needed starting when the real answer was that they were already
up under another project name.

### What warrants review

The diff is three lines in `/go.mod` and `/go.sum`; there is nothing to review in
glue's own code. The substance of the review is the version claim, and both the
static `SQLITE_VERSION` grep and the runtime `sqlite_version()` result are recorded
above so it can be re-checked in one command. If a reviewer wants independent
confirmation, grepping `sqlite3-binding.c` in the module cache is enough.

### Future work

Unchanged from Step 1: the checkpointing question is worth a separate look and was
deliberately left out of this PR. Nothing new fell out of the update.
