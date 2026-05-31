# MySQL integration test suite

End-to-end tests that exercise the MySQL backend of this `ntfy` fork
against a real MySQL 8 container, with both services running inside an
isolated `internal: true` podman network. Only the ntfy HTTP port is
exposed (and only to the host loopback) so the test runner — which
runs on the host — can drive the system through real HTTP and
WebSocket traffic.

## Topology

```
                +------------------+   internal:true (no internet)
+----------+    |  ntfy container  |<------------------+
| host     |    |  built from src  |                    |
| pytest   |--->|  on this branch  |                    |
| runner   |    |  port 80         |                    |
+----------+    +---------+--------+                    |
   127.0.0.1:8888         |                             |
                          |  127.0.0.1:8888:80          |
                          v                             |
                  +---------------+                     |
                  | mysql:8.0     |<-------(no ports)---+
                  | port 3306     |
                  +---------------+
```

The MySQL container has no published ports — it is only reachable from
the ntfy container on the internal network. The ntfy container's
HTTP port is published only on the host loopback so external systems
cannot reach the test stack.

## Prerequisites

- `podman` 5.x with `podman compose` shim (or `docker compose` v2).
- Python 3.10+.
- `~150 MB` of disk for the MySQL image + the built ntfy image.

## Running

From the repository root:

```powershell
# Windows
pwsh tests/integration/mysql/run.ps1
```

```bash
# Linux / macOS
./tests/integration/mysql/run.sh
```

The runner:

1. Brings up the compose stack (`mysql` + `ntfy`) and waits for both
   healthchecks to pass.
2. Creates a local Python virtualenv at `tests/integration/mysql/.venv/`
   and installs `requirements.txt`.
3. Runs the pytest suite with
   `--junitxml=tests/integration/mysql/results/junit.xml`.
4. On exit (success or failure) tears the stack down with
   `compose down -v` to release the network and named volumes.

Pass `-NoTeardown` (PowerShell) or set `NO_TEARDOWN=1` (bash) to leave
the stack running for debugging.

## Files

| File | Purpose |
|------|---------|
| `Dockerfile.ntfy` | Multi-stage build of the `ntfy` binary from the current source tree. Pure-Go (`CGO_ENABLED=0`), ships on Alpine 3.20. |
| `podman-compose.yml` | Stack definition: one MySQL 8 container + one ntfy container on a single `internal: true` network. |
| `ntfy-server.yml` | Reference ntfy server config; the compose file uses environment variables that map onto these keys. |
| `requirements.txt` | Python dependencies (`pytest`, `requests`, `websocket-client`, `pytest-timeout`). |
| `pytest.ini` | pytest configuration — sets the test path, verbosity, default timeout. |
| `tests/conftest.py` | Shared fixtures: `ntfy_base_url`, `ntfy` HTTP client, `unique_topic`, plus an autouse readiness gate. |
| `tests/test_*.py` | The actual integration tests, grouped by area (health, publish/poll, streaming, auth, web push, persistence). |
| `run.ps1`, `run.sh` | Orchestration scripts. |
| `results/junit.xml` | (Generated) JUnit XML produced by pytest. |

## Coverage

Each test file focuses on one slice of the MySQL backend:

- **`test_01_health`** — server boots, `/v1/health` returns 200,
  embedded assets are served, no known endpoint 500s.
- **`test_02_publish_poll`** — message round-trip through MySQL with
  every supported header (title, priority, tags, click, icon,
  actions, large bodies, emoji, JSON form, scheduled messages,
  `since=<id>` filtering).
- **`test_03_streaming`** — live JSON stream, WebSocket, SSE, and
  multi-topic subscriptions all deliver messages published in
  real-time.
- **`test_04_auth`** — account endpoint behaviour, anonymous role
  reporting, stats counters, credential rejection.
- **`test_05_webpush`** — web-push subscribe/upsert/unsubscribe
  (exercises the no-`RETURNING` MySQL upsert path).
- **`test_06_persistence`** — messages survive an `ntfy` container
  restart (i.e. they were actually written to MySQL).

## Why pytest (and not Go) for these tests?

The Go test suite already covers each backend through the
`forEachBackend` helper — those are unit tests that talk to the
database directly. This suite is a higher-level integration
check that the whole composed system (HTTP server, message
cache, user manager, web push store, MySQL driver, network)
works end-to-end. Pytest is the simpler tool for that job: it
emits JUnit XML natively, has first-class HTTP and WebSocket
client libraries, and decouples the integration check from the
Go build/test cycle so we don't end up baking compose dependencies
into `go test`.
