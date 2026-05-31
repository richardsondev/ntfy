"""Web-push subscription tests.

The MySQL-backed web push store keeps subscriptions in the
``webpush_subscription`` table and topic mappings in
``webpush_subscription_topic``. This exercises the upsert path that
this fork specifically rewrote (no ``RETURNING`` in MySQL — handled by
the ``upsertReturnsID`` queries flag in webpush/store.go).

The web push endpoint URL must match ntfy's allow-list of real web
push providers (FCM / Mozilla / WNS / Apple); see
``server/server_webpush.go::webPushAllowedEndpointsRegexes``. We use
fake but well-formed FCM URLs — the server never tries to POST to
them, it just stores the string in MySQL.
"""

from __future__ import annotations

import uuid


# These p256dh / auth blobs are base64url-encoded random bytes. They
# aren't real cryptographic keys — the server treats them as opaque
# strings to persist, no validation beyond non-empty + base64ish.
_FAKE_P256DH = (
    "BNRR-HK2H1NlPnGZyRSZSCi6QQHGZpFqUcKK7w7-XBKMVQv1G3UQwbY0lvJlFqxqRYi1bksdSDQ3PcUg7QlplJI"
)
_FAKE_AUTH = "k8JV6sjdEYQ-zjxBE-A4hg"


def _fake_endpoint() -> str:
    """Return a unique allow-listed endpoint URL.

    Uses Firebase Cloud Messaging's host pattern because it is the
    simplest match in ``webPushAllowedEndpointsRegexes`` (no
    instance-specific subdomain). The token portion is randomised
    per call so each subscribe gets a distinct row.
    """
    return f"https://fcm.googleapis.com/fcm/send/{uuid.uuid4().hex}"


def _payload(endpoint: str, topics: list[str]) -> dict:
    """Build a valid POST /v1/webpush body matching apiWebPushUpdateSubscriptionRequest."""
    return {
        "endpoint": endpoint,
        "auth": _FAKE_AUTH,
        "p256dh": _FAKE_P256DH,
        "topics": topics,
    }


def test_webpush_subscribe_then_resubscribe_idempotent(ntfy, unique_topic):
    """Subscribing the same endpoint twice must succeed via upsert.

    This is the canonical MySQL-vs-Postgres divergence: the SQLite
    and Postgres paths use ``RETURNING id`` on the upsert; the MySQL
    path issues the upsert then SELECTs the id back in the same
    transaction. Both calls must round-trip cleanly.
    """
    endpoint = _fake_endpoint()
    r1 = ntfy.request("POST", "/v1/webpush", json=_payload(endpoint, [unique_topic]))
    assert r1.status_code == 200, r1.text
    r2 = ntfy.request("POST", "/v1/webpush", json=_payload(endpoint, [unique_topic]))
    assert r2.status_code == 200, r2.text


def test_webpush_subscribe_multiple_topics(ntfy, unique_topic):
    """A single web-push subscription can be registered for many topics."""
    endpoint = _fake_endpoint()
    topics = [f"{unique_topic}-{i}" for i in range(3)]
    r = ntfy.request("POST", "/v1/webpush", json=_payload(endpoint, topics))
    assert r.status_code == 200, r.text


def test_webpush_unsubscribe(ntfy, unique_topic):
    """Subscribing and then unsubscribing should remove the record cleanly.

    The DELETE handler accepts a body containing just the endpoint
    (see server_webpush.go::handleWebPushDelete) and returns 200 on
    success.
    """
    endpoint = _fake_endpoint()
    sub = ntfy.request("POST", "/v1/webpush", json=_payload(endpoint, [unique_topic]))
    assert sub.status_code == 200, sub.text

    unsub = ntfy.request("DELETE", "/v1/webpush", json={"endpoint": endpoint})
    assert unsub.status_code == 200, unsub.text


def test_webpush_invalid_endpoint_rejected(ntfy, unique_topic):
    """An endpoint that isn't a URL should be rejected with 4xx, not 500."""
    payload = {
        "endpoint": "not-a-url",
        "auth": _FAKE_AUTH,
        "p256dh": _FAKE_P256DH,
        "topics": [unique_topic],
    }
    r = ntfy.request("POST", "/v1/webpush", json=payload)
    assert 400 <= r.status_code < 500, (
        f"invalid endpoint should be 4xx, got {r.status_code}: {r.text}"
    )


def test_webpush_disallowed_endpoint_rejected(ntfy, unique_topic):
    """A well-formed URL but not on the allow-list must be rejected.

    Regression-style check for the GHSA-w9hq-5jg7-q4j7 mitigation
    (see ``server_webpush.go::webPushAllowedEndpointsRegexes``).
    The MySQL path must short-circuit on the same validation as the
    other backends — the request never reaches the DB layer.
    """
    payload = _payload(
        "https://attacker.example.com/fcm.googleapis.com/send/abc",
        [unique_topic],
    )
    r = ntfy.request("POST", "/v1/webpush", json=payload)
    assert r.status_code == 400, r.text
    body = r.json()
    assert body.get("code") == 40039, body  # web push endpoint unknown


def test_webpush_missing_required_fields_rejected(ntfy, unique_topic):
    """Missing ``auth`` or ``p256dh`` must fail validation, not corrupt MySQL."""
    payload = {
        "endpoint": _fake_endpoint(),
        # no auth / p256dh
        "topics": [unique_topic],
    }
    r = ntfy.request("POST", "/v1/webpush", json=payload)
    assert r.status_code == 400, r.text
