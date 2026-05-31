"""pytest fixtures shared across the MySQL integration test suite.

The suite assumes the podman-compose stack defined alongside it has
already been brought up by the runner (run.ps1 / run.sh). The fixtures
here only configure pytest, expose a base URL, and provide a small
``ntfy_client`` helper for HTTP requests with sensible defaults.

Environment variables consumed:

* ``NTFY_INT_URL``         — base URL of the ntfy server under test
                             (default: ``http://127.0.0.1:8888``).
* ``NTFY_INT_HTTP_TIMEOUT`` — per-request HTTP timeout in seconds
                             (default: 10.0).
"""

from __future__ import annotations

import json
import os
import time
import uuid
from dataclasses import dataclass
from typing import Any

import pytest
import requests


DEFAULT_BASE_URL = "http://127.0.0.1:8888"
DEFAULT_TIMEOUT = 10.0


@dataclass(frozen=True)
class NtfyConfig:
    base_url: str
    timeout: float


@pytest.fixture(scope="session")
def ntfy_config() -> NtfyConfig:
    base_url = os.environ.get("NTFY_INT_URL", DEFAULT_BASE_URL).rstrip("/")
    timeout = float(os.environ.get("NTFY_INT_HTTP_TIMEOUT", DEFAULT_TIMEOUT))
    return NtfyConfig(base_url=base_url, timeout=timeout)


@pytest.fixture(scope="session")
def ntfy_base_url(ntfy_config: NtfyConfig) -> str:
    return ntfy_config.base_url


@pytest.fixture(scope="session", autouse=True)
def _wait_for_ntfy_ready(ntfy_config: NtfyConfig) -> None:
    """Block test collection until the ntfy server reports healthy.

    The runner already waits for the compose healthcheck, but on a
    cold-start compose the ntfy container may still be performing
    schema migrations against MySQL when the first test executes —
    this fixture polls /v1/health for up to 60s as a belt-and-braces
    guard.
    """
    deadline = time.monotonic() + 60.0
    last_error: Exception | None = None
    while time.monotonic() < deadline:
        try:
            r = requests.get(f"{ntfy_config.base_url}/v1/health",
                             timeout=ntfy_config.timeout)
            if r.status_code == 200 and r.json().get("healthy") is True:
                return
            last_error = RuntimeError(
                f"health endpoint returned status={r.status_code} body={r.text!r}"
            )
        except (requests.RequestException, json.JSONDecodeError) as exc:
            last_error = exc
        time.sleep(1.0)
    pytest.fail(f"ntfy server never became healthy: {last_error}")


@pytest.fixture
def unique_topic() -> str:
    """Return a unique topic name for the current test.

    Topics are namespaced per-test so parallel pytest workers (or
    successive runs against a persistent MySQL volume) don't cross-
    contaminate cached messages.
    """
    return f"itest-{uuid.uuid4().hex[:12]}"


class NtfyClient:
    """Tiny convenience wrapper around requests with sane defaults."""

    def __init__(self, base_url: str, timeout: float) -> None:
        self._base_url = base_url
        self._timeout = timeout
        self._session = requests.Session()

    @property
    def base_url(self) -> str:
        return self._base_url

    def publish(self, topic: str, body: str | bytes, **kwargs: Any) -> requests.Response:
        url = f"{self._base_url}/{topic}"
        return self._session.post(url, data=body, timeout=self._timeout, **kwargs)

    def publish_json(self, payload: dict[str, Any], **kwargs: Any) -> requests.Response:
        url = f"{self._base_url}/"
        kwargs.setdefault("headers", {}).setdefault("Content-Type", "application/json")
        return self._session.post(url, data=json.dumps(payload), timeout=self._timeout, **kwargs)

    def poll(self, topic: str, **params: Any) -> list[dict[str, Any]]:
        url = f"{self._base_url}/{topic}/json"
        params.setdefault("poll", "1")
        r = self._session.get(url, params=params, timeout=self._timeout, stream=True)
        r.raise_for_status()
        out: list[dict[str, Any]] = []
        for line in r.iter_lines():
            if not line:
                continue
            out.append(json.loads(line))
        return out

    def health(self) -> dict[str, Any]:
        r = self._session.get(f"{self._base_url}/v1/health", timeout=self._timeout)
        r.raise_for_status()
        return r.json()

    def request(self, method: str, path: str, **kwargs: Any) -> requests.Response:
        kwargs.setdefault("timeout", self._timeout)
        return self._session.request(method, f"{self._base_url}{path}", **kwargs)


@pytest.fixture
def ntfy(ntfy_config: NtfyConfig) -> NtfyClient:
    return NtfyClient(ntfy_config.base_url, ntfy_config.timeout)
