# ntfy MySQL fuzz suite

Native Go fuzz targets that exercise the ntfy code paths the MySQL backend
relies on. The framework is the standard-library `testing.F` API ([Go fuzz
docs](https://go.dev/security/fuzz/)) — zero new dependencies and run via
`go test -fuzz`.

## Why fuzz the MySQL surface

The MySQL backend (added on `mysql-support`) introduces:

- A new DSN format (`mysql://user:pass@host:port/db?…`) parsed by
  `db/mysql.open` and rewritten into the driver's native form by
  `buildDriverDSN`.
- A TLS bootstrap path that loads PEM bundles from `sslrootcert=`.
- A new entry in `isUniqueConstraintError` that has to recognize
  `*mysql.MySQLError{Number: 1062}` reliably and never misclassify other
  driver errors.
- The same `actions` JSON column that the SQLite/Postgres backends already
  use, but stored in MySQL `MEDIUMTEXT` — the marshal/unmarshal round-trip
  must remain a fixed-point regardless of what gets persisted.

All of these are pure-Go functions with no driver round-trip — they make
excellent fuzz targets that run hundreds of thousands of executions per
second on a developer laptop.

## Targets

| Target                                            | Package           | Invariant under test                                                                       |
|---------------------------------------------------|-------------------|--------------------------------------------------------------------------------------------|
| `FuzzOpen_URL`                                    | `db/mysql`        | URL-form DSN parsing never panics; any `*sql.DB` returned is closeable.                    |
| `FuzzBuildDriverDSN`                              | `db/mysql`        | `mysqldriver.ParseDSN(buildDriverDSN(u, q))` round-trips (catches fail-fast gaps).         |
| `FuzzParseMySQLVersion`                           | `db/mysql`        | Server `VERSION()` parsing never panics; returned components are non-negative.             |
| `FuzzSplitHostPort`                               | `db/mysql`        | Defensive `host:port` splitting never panics; output substrings are present in input.      |
| `FuzzExtractIntParam`                             | `db/mysql`        | Pool integer extraction never panics; error messages name the offending key.               |
| `FuzzExtractDurationParam`                        | `db/mysql`        | Pool duration extraction never panics; error messages name the offending key.              |
| `FuzzCensorPassword`                              | `db/mysql`        | The userinfo password is never recoverable from a logged URL.                              |
| `FuzzRegisterTLSConfig_PEM`                       | `db/mysql`        | Arbitrary PEM contents never panic TLS setup; errors mention `PEM` or `sslrootcert`.       |
| `FuzzIsUniqueConstraintError`                     | `user`            | Typed MySQL 1062 / pq 23505 errors always classify; nil never panics or misclassifies.     |
| `FuzzActionsJSONRoundTrip`                        | `message`         | `[]*model.Action` marshal/unmarshal reaches a canonical fixed point.                       |
| `FuzzMessageHeaderFieldsRoundTrip`                | `message`         | Valid-UTF-8 title/message/click/icon/tags/topic survive JSON round-trip byte-for-byte.     |

## Running locally

The orchestration scripts iterate every target above. Default per-target
budget is `10s`; override with `FUZZTIME=…` for a longer soak.

### Sequential runner (one target at a time)

```powershell
# Windows
pwsh scripts/fuzz-mysql.ps1                  # 10s/target, default
pwsh scripts/fuzz-mysql.ps1 -FuzzTime 60s    # 60s/target
pwsh scripts/fuzz-mysql.ps1 -FuzzTime 5m     # heavy pre-release sweep
```

```bash
# Linux / macOS
bash scripts/fuzz-mysql.sh                   # 10s/target, default
FUZZTIME=60s bash scripts/fuzz-mysql.sh      # 60s/target
FUZZTIME=5m  bash scripts/fuzz-mysql.sh      # heavy pre-release sweep

# Or via the Makefile
make fuzz-mysql
FUZZTIME=60s make fuzz-mysql
```

### Parallel runner (all targets at once)

For meaningful soak runs, prefer the parallel runner — it launches one
`go test -fuzz` process per target with each child capped at
`GOMAXPROCS=3` so 11 simultaneous targets stay near 1× physical cores.

```powershell
pwsh scripts/fuzz-parallel.ps1                       # 10m/target default
pwsh scripts/fuzz-parallel.ps1 -FuzzTime 30m         # 30m/target
pwsh scripts/fuzz-parallel.ps1 -FuzzTime 6h          # 6h/target overnight soak
pwsh scripts/fuzz-parallel.ps1 -FuzzTime 1h -WorkersPerTarget 2   # lower CPU load
```

Each run writes to `tests/fuzz/results/run-<YYYYMMDD-HHMMSS>/` (per-target
log, `summary.log`, `summary.json`, `run.json`) and updates
`tests/fuzz/results/latest.txt` with the run dir path so other tooling can
locate the current/last run without guessing the timestamp.

Conflict safety:
* Each target gets a dedicated `<TargetName>.log` (no shared writers).
* Each target writes corpus only to `<pkg>/testdata/fuzz/<TargetName>/`
  (per-target subdir, no collisions).
* Build cache locking is handled by Go's toolchain.

### Loop until a clean pass

When iterating on a fix, run the loop runner to repeat parallel passes
until one succeeds without crashes (or a max-iter cap is hit):

```powershell
pwsh scripts/fuzz-loop.ps1 -FuzzTime 10m -MaxIterations 5
```

### Status check on an in-progress run

`scripts/fuzz-status.ps1` reads `latest.txt` and prints per-target current
elapsed time, execs count, exec rate, corpus size, and whether each
underlying `go test` process has exited yet:

```powershell
pwsh scripts/fuzz-status.ps1                 # status of latest run
pwsh scripts/fuzz-status.ps1 -RunDir tests/fuzz/results/run-...   # specific run
```

### Single target via stock Go tooling

```bash
go test -run '^$' -fuzz '^FuzzBuildDriverDSN$' -fuzztime 30s ./db/mysql/...
```

`-run '^$'` disables non-fuzz test selection so only the fuzz target runs.

## Crash regressions

When `go test -fuzz` discovers an input that fails the invariant, it saves
that input under `<pkg>/testdata/fuzz/<TargetName>/<hash>`. **These files
are committed**: subsequent `go test ./<pkg>/...` runs replay them as
regular table-driven test cases, so a regression that re-introduces the bug
is caught even without re-fuzzing.

If you discover a new crash:

1. **Reproduce**: `go test -run 'FuzzXxx/<hash>' ./<pkg>/...` (replays the
   saved input under the regular test harness).
2. **Diagnose**: real production bug → fix the production code and keep the
   corpus file as a regression test. Test invariant too strict → loosen it
   with a comment explaining why, and delete the file.
3. **Commit**: the corpus file + any production fix in a single commit so
   reviewers can see both halves of the change.

## Bugs found during the initial fuzz sweep

Documented in the branch commit message; recap:

- `db/mysql/mysql.go::buildDriverDSN` now validates its output via
  `mysqldriver.ParseDSN` so misconfigured DSNs (e.g. `tls=2`) fail fast at
  startup with a clear error instead of much later, at first Ping.
- `db/mysql/mysql.go::censorPassword` now redacts URL-encoded passwords
  correctly. The previous implementation built a search string from the raw
  password byte-for-byte, which never matched when `u.String()` had
  URL-encoded the password (e.g. `@` → `%40`), and the original password
  silently leaked into log output.
- `user/manager.go::isUniqueConstraintError` is now `nil`-safe (the
  pre-existing typed-error path fell through to `err.Error()` which would
  have panicked on nil) and recognizes `*pq.Error{Code: "23505"}` via
  `errors.As`, not just substring matching against `err.Error()`.

## Adding a new fuzz target

1. Write `func FuzzMyThing(f *testing.F)` in `<pkg>/<file>_fuzz_test.go`
   alongside the function under test (same package if it needs unexported
   helpers; `<pkg>_test` external test package otherwise).
2. Seed with `f.Add(...)` calls covering the obvious shapes — the more
   diverse the seeds, the better the mutation coverage.
3. Implement the invariant inside `f.Fuzz(func(t *testing.T, in T) { … })`.
   Use `t.Skip()` for inputs that are out-of-scope for the invariant; use
   `t.Fatalf` for genuine invariant violations.
4. The orchestration scripts auto-discover any `FuzzXxx` target in the four
   packages they scan (`./db/mysql/...`, `./user/...`, `./message/...`,
   `./webpush/...`). No script edits are required for a new target inside
   those packages.
5. Run it locally for at least 30s before pushing: `go test -run '^$'
   -fuzz '^FuzzMyThing$' -fuzztime 30s ./<pkg>/...`.

## CI integration (future)

This branch does **not** wire fuzzing into CI; runs are developer-driven
for now. A reasonable follow-up is a scheduled GitHub Actions workflow that
runs `make fuzz-mysql` with `FUZZTIME=2m` nightly and uploads
`tests/fuzz/results/` as an artifact.
