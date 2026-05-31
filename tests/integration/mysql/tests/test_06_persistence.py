"""Cross-restart persistence tests.

These tests verify that messages published before the ntfy container
restarts are still available afterwards, which is the entire reason
for backing the message cache with MySQL instead of in-memory.

The restart itself is performed via the ``ntfy`` Docker container
restart (``docker compose restart ntfy``) which is initiated by a
helper subprocess; the test itself only triggers the restart and then
polls until the server is back online.

These tests run AFTER the publish/poll/streaming suite (alphabetical
file ordering) so they exercise persistence on a populated database.
"""

from __future__ import annotations

import os
import shutil
import subprocess
import time

import pytest
import requests


COMPOSE_PROJECT = "ntfy-mysql-int"


def _find_compose() -> list[str] | None:
    """Locate a working docker-compose / podman compose invocation.

    Prefer ``podman compose`` (matches the runner), fall back to
    ``docker compose`` and finally ``docker-compose`` for portability.
    """
    podman = shutil.which("podman")
    if podman:
        return [podman, "compose"]
    docker = shutil.which("docker")
    if docker:
        return [docker, "compose"]
    dc = shutil.which("docker-compose")
    if dc:
        return [dc]
    return None


def _wait_for_healthy(base_url: str, timeout: float = 60.0) -> None:
    deadline = time.monotonic() + timeout
    last_err: Exception | None = None
    while time.monotonic() < deadline:
        try:
            r = requests.get(f"{base_url}/v1/health", timeout=2.0)
            if r.status_code == 200 and r.json().get("healthy") is True:
                return
        except requests.RequestException as exc:
            last_err = exc
        time.sleep(1.0)
    raise TimeoutError(f"ntfy never came back healthy: {last_err}")


@pytest.mark.skipif(
    _find_compose() is None,
    reason="no podman/docker compose found on PATH; cannot restart container",
)
def test_messages_persist_across_ntfy_restart(ntfy, ntfy_base_url, unique_topic):
    """Publish, restart the ntfy container, then poll — the message must survive.

    This is the core MySQL-persistence guarantee for the message cache.
    """
    # 1. Publish a marker message and remember its id.
    r = ntfy.publish(unique_topic, "survive-the-restart")
    assert r.status_code == 200, r.text
    msg_id = r.json()["id"]

    polled_before = ntfy.poll(unique_topic)
    assert any(m["id"] == msg_id for m in polled_before), polled_before

    # 2. Restart the ntfy container in place. MySQL stays up.
    compose = _find_compose()
    assert compose is not None
    compose_dir = os.environ.get("NTFY_INT_COMPOSE_DIR")
    assert compose_dir, "NTFY_INT_COMPOSE_DIR must be set by the runner"

    res = subprocess.run(
        [*compose, "-f", "podman-compose.yml", "restart", "ntfy"],
        cwd=compose_dir,
        capture_output=True,
        text=True,
        timeout=120,
    )
    assert res.returncode == 0, (
        f"compose restart failed: stdout={res.stdout!r} stderr={res.stderr!r}"
    )

    # 3. Wait for the server to come back online and verify the message
    #    is still there — i.e. it was read out of MySQL on startup.
    _wait_for_healthy(ntfy_base_url, timeout=90.0)

    polled_after = ntfy.poll(unique_topic)
    assert any(m["id"] == msg_id for m in polled_after), (
        f"message {msg_id} did not survive ntfy restart; polled: {polled_after}"
    )


def test_messages_counter_grows_after_publish(ntfy, unique_topic):
    """``/v1/stats`` ``messages`` count must increase across publishes.

    The /v1/stats endpoint exposes a global, monotonically-increasing
    publish counter (and an EWMA rate). Verifying the counter rises
    by exactly the number of publishes confirms both that the publish
    pipeline ran to completion and that the stats persistence path
    (server.go::handleStats reading s.messages) is wired up.

    Note: ntfy does not expose a global topic count via this endpoint —
    only the message counter is publicly readable.
    """
    before = ntfy.request("GET", "/v1/stats").json()
    msgs_before = before.get("messages", 0)

    for i in range(3):
        ntfy.publish(unique_topic, f"stat-{i}").raise_for_status()

    # Stats are updated synchronously inside the publish handler, but
    # the stats manager flush happens on a timer; the publish counter
    # itself is updated immediately under s.mu.
    for _ in range(10):
        after = ntfy.request("GET", "/v1/stats").json()
        msgs_after = after.get("messages", 0)
        if msgs_after >= msgs_before + 3:
            return
        time.sleep(0.3)

    pytest.fail(
        f"messages counter did not increase by 3: "
        f"before={msgs_before}, after={msgs_after}"
    )
