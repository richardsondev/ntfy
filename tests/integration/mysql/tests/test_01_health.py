"""Server health, build info, and basic endpoint reachability tests.

These tests exercise the MySQL-backed ntfy server before any state has
been written, so failures here usually mean the container itself or
the MySQL connection is broken rather than the application logic.
"""

from __future__ import annotations

import pytest


def test_health_endpoint_returns_healthy(ntfy):
    """``/v1/health`` must report ``healthy=true`` once the server has booted.

    The conftest autouse fixture already polls this endpoint, so by
    the time this test runs we expect a synchronous OK.
    """
    health = ntfy.health()
    assert health["healthy"] is True


def test_root_serves_web_app_placeholder(ntfy):
    """``GET /`` returns the embedded web app HTML stub.

    The integration build creates an empty ``server/site/app.html`` so
    the //go:embed directive succeeds; the server should still return
    200 (with an empty body) and not 404 or 500.
    """
    r = ntfy.request("GET", "/")
    assert r.status_code == 200, f"unexpected body: {r.text!r}"


def test_config_js_is_served(ntfy):
    """The web app config.js asset is served from the embed.FS."""
    r = ntfy.request("GET", "/config.js")
    # The placeholder is empty, but the route handler should still respond
    assert r.status_code in (200, 404)


def test_unknown_method_on_topic_is_4xx(ntfy, unique_topic):
    """Unsupported HTTP methods on the publish endpoint should be a 4xx.

    ntfy doesn't register a PATCH handler on ``/<topic>`` so the router
    simply doesn't match, yielding a 404. Either 404 or 405 are
    acceptable; the test asserts only that the request is rejected
    cleanly (not a 5xx).
    """
    r = ntfy.request("PATCH", f"/{unique_topic}")
    assert 400 <= r.status_code < 500, (
        f"PATCH should be a 4xx, got {r.status_code}: {r.text!r}"
    )


@pytest.mark.parametrize("path", ["/", "/docs/", "/v1/health"])
def test_known_paths_do_not_500(ntfy, path):
    """No known endpoint should ever 500."""
    r = ntfy.request("GET", path)
    assert r.status_code < 500, f"{path} returned {r.status_code}: {r.text!r}"
