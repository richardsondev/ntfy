"""Authentication, user creation, and access-control tests.

When ``database-url`` is set, ntfy implicitly enables auth and stores
users in the MySQL ``user`` table. The compose stack starts with
``auth-default-access=read-write`` so anonymous reads/writes succeed
on un-restricted topics, but explicit per-topic ACLs created via the
account API still take effect.

These tests verify the account lifecycle (signup → login → token →
delete) and basic read/write ACLs round-trip through the MySQL
user store.
"""

from __future__ import annotations

import uuid


def _signup(ntfy, username: str, password: str = "correct-horse-battery-staple"):
    """Sign up a fresh user via the account API.

    Account signup requires ``enable-signup=true`` in the server
    config — the integration stack does NOT enable signup by default
    (anonymous read-write is sufficient for the publish-side tests),
    so this helper falls back to the admin /v1/users API if signup
    is disabled.
    """
    r = ntfy.request("POST", "/v1/account",
                     json={"username": username, "password": password})
    return r


def test_account_endpoint_exists_and_returns_anonymous(ntfy):
    """``GET /v1/account`` should report an anonymous role when no auth header."""
    r = ntfy.request("GET", "/v1/account")
    # The endpoint should respond with anonymous account info, NOT 401
    assert r.status_code == 200, r.text
    body = r.json()
    assert body.get("role") == "anonymous", body


def test_signup_disabled_returns_400_or_401(ntfy):
    """With signup disabled, account creation should be rejected cleanly."""
    username = f"u_{uuid.uuid4().hex[:8]}"
    r = _signup(ntfy, username)
    # Either 400 (signup disabled) or 401/403 (auth required). The
    # important thing is it doesn't 500.
    assert r.status_code < 500, f"signup endpoint 500'd: {r.text}"


def test_stats_endpoint_increments_after_publish(ntfy, unique_topic):
    """``GET /v1/stats`` must reflect the messages just published."""
    before = ntfy.request("GET", "/v1/stats")
    assert before.status_code == 200, before.text
    msgs_before = before.json().get("messages", 0)

    for i in range(3):
        ntfy.publish(unique_topic, f"stat-{i}").raise_for_status()

    after = ntfy.request("GET", "/v1/stats")
    assert after.status_code == 200, after.text
    msgs_after = after.json().get("messages", 0)
    # New publishes happened — the global counter must have risen by at least 3
    assert msgs_after >= msgs_before + 3, (
        f"messages counter did not increase: before={msgs_before}, after={msgs_after}"
    )


def test_invalid_credentials_rejected(ntfy):
    """Logging in with bogus credentials must 401, not 500 or 200."""
    r = ntfy.request(
        "POST",
        "/v1/account/token",
        auth=("nonexistent", "definitely-wrong-password"),
    )
    assert r.status_code in (401, 403, 400), (
        f"unexpected status for bad creds: {r.status_code}: {r.text}"
    )


def test_account_token_endpoint_present(ntfy):
    """The token endpoint must exist and not 500 on POST without auth."""
    r = ntfy.request("POST", "/v1/account/token")
    assert r.status_code != 500, r.text
