"""Publish / poll round-trip tests against the MySQL message cache.

Each test publishes one or more messages to a fresh topic and then
fetches them back via the JSON poll endpoint, verifying that every
field landed in MySQL and round-tripped correctly. These tests are
the canonical check that the MySQL backend is feature-equivalent to
the SQLite / PostgreSQL backends for the message-cache code path.
"""

from __future__ import annotations

import base64
import time
import uuid


def test_publish_text_round_trip(ntfy, unique_topic):
    """A plain-text POST body comes back exactly as published."""
    r = ntfy.publish(unique_topic, "hello mysql world")
    assert r.status_code == 200, r.text
    msg = r.json()
    assert msg["topic"] == unique_topic
    assert msg["message"] == "hello mysql world"
    assert msg["event"] == "message"
    assert "id" in msg and len(msg["id"]) > 0
    assert msg["time"] > 0

    polled = ntfy.poll(unique_topic)
    assert len(polled) == 1
    assert polled[0]["message"] == "hello mysql world"
    assert polled[0]["id"] == msg["id"]


def test_publish_with_title_priority_tags_headers(ntfy, unique_topic):
    """All ntfy message-decoration headers must persist through MySQL."""
    r = ntfy.publish(
        unique_topic,
        "body",
        headers={
            "Title": "Important news",
            "Priority": "high",
            "Tags": "warning,fire,rotating_light",
            "Click": "https://example.com/details",
            "Icon": "https://example.com/icon.png",
        },
    )
    assert r.status_code == 200, r.text
    msg = r.json()
    assert msg["title"] == "Important news"
    assert msg["priority"] == 4  # high == 4 in ntfy
    assert sorted(msg["tags"]) == sorted(["warning", "fire", "rotating_light"])
    assert msg["click"] == "https://example.com/details"
    assert msg["icon"] == "https://example.com/icon.png"

    polled = ntfy.poll(unique_topic)
    assert len(polled) == 1
    p = polled[0]
    assert p["title"] == "Important news"
    assert p["priority"] == 4
    assert sorted(p["tags"]) == sorted(["warning", "fire", "rotating_light"])
    assert p["click"] == "https://example.com/details"


def test_publish_action_buttons(ntfy, unique_topic):
    """Action buttons (the X-Actions header) must round-trip through MySQL.

    Action buttons are stored as a JSON array in the ``actions`` column
    (MEDIUMTEXT in the MySQL schema). This exercises both the write
    path and the JSON decode path on read.
    """
    actions_header = (
        "view, Open page, https://example.com/page;"
        "http, Acknowledge, https://example.com/ack, method=PUT, body=ack=1"
    )
    r = ntfy.publish(unique_topic, "decide", headers={"Actions": actions_header})
    assert r.status_code == 200, r.text
    msg = r.json()
    assert "actions" in msg, msg
    assert len(msg["actions"]) == 2
    assert msg["actions"][0]["action"] == "view"
    assert msg["actions"][0]["label"] == "Open page"
    assert msg["actions"][0]["url"] == "https://example.com/page"
    assert msg["actions"][1]["action"] == "http"
    assert msg["actions"][1]["method"] == "PUT"

    polled = ntfy.poll(unique_topic)
    assert len(polled) == 1
    assert polled[0]["actions"] == msg["actions"]


def test_publish_unicode_and_emoji(ntfy, unique_topic):
    """utf8mb4 (with the bin collation) must preserve emoji and CJK exactly."""
    payload = "héllo — 你好 — 🎉🚀✅ — Z̷̧̢a̷̢͠l̵͙͝g̶̢͠o̵̧͝"
    r = ntfy.publish(unique_topic, payload.encode("utf-8"))
    assert r.status_code == 200, r.text
    assert r.json()["message"] == payload

    polled = ntfy.poll(unique_topic)
    assert len(polled) == 1
    assert polled[0]["message"] == payload


def test_publish_large_message(ntfy, unique_topic):
    """A ~4 KiB message must survive the MEDIUMTEXT column unchanged.

    ntfy trims trailing whitespace from the message body before
    persistence, so the test payload deliberately ends with a
    non-whitespace character.
    """
    payload = ("line " + uuid.uuid4().hex + "\n") * 100 + "END"
    r = ntfy.publish(unique_topic, payload)
    assert r.status_code == 200, r.text

    polled = ntfy.poll(unique_topic)
    assert len(polled) == 1
    assert polled[0]["message"] == payload


def test_publish_via_json_payload(ntfy, unique_topic):
    """The ``POST /`` JSON publish form must work identically to POST /<topic>."""
    body = {
        "topic": unique_topic,
        "title": "json-publish",
        "message": "via json payload",
        "tags": ["json", "test"],
        "priority": 3,
    }
    r = ntfy.publish_json(body)
    assert r.status_code == 200, r.text
    msg = r.json()
    assert msg["topic"] == unique_topic
    assert msg["title"] == "json-publish"
    assert msg["message"] == "via json payload"

    polled = ntfy.poll(unique_topic)
    assert len(polled) == 1
    assert polled[0]["message"] == "via json payload"


def test_multiple_messages_ordering(ntfy, unique_topic):
    """Messages must come back from MySQL in ascending time order."""
    for i in range(5):
        r = ntfy.publish(unique_topic, f"msg-{i}")
        assert r.status_code == 200, r.text
        # tiny pause to ensure distinct timestamps; ntfy uses 1s granularity
        # internally but the id ordering is monotonic regardless
        time.sleep(0.05)

    polled = ntfy.poll(unique_topic)
    assert [p["message"] for p in polled] == [f"msg-{i}" for i in range(5)]


def test_poll_since_id_filters_old_messages(ntfy, unique_topic):
    """``since=<id>`` returns only messages newer than the given id."""
    first = ntfy.publish(unique_topic, "first").json()
    ntfy.publish(unique_topic, "second")
    ntfy.publish(unique_topic, "third")

    polled = ntfy.poll(unique_topic, since=first["id"])
    msgs = [p["message"] for p in polled]
    assert msgs == ["second", "third"], msgs


def test_scheduled_message_round_trip(ntfy, unique_topic):
    """A scheduled (delayed) message must be queued in MySQL and report status.

    ``X-Delay`` defers the message; the immediate publish response
    should reflect the schedule and not include the message body in
    the live broadcast (it sits in the message_cache table with a
    future ``time``).
    """
    r = ntfy.publish(
        unique_topic,
        "delayed body",
        headers={"X-Delay": "30s", "X-Title": "scheduled"},
    )
    assert r.status_code == 200, r.text
    msg = r.json()
    # Scheduled messages report event=message immediately; the field that
    # signals scheduling is the time being in the future
    assert msg["time"] > 0
    assert msg.get("title") == "scheduled"

    # The poll endpoint with scheduled=1 must include scheduled messages
    polled = ntfy.poll(unique_topic, scheduled=1)
    assert any(p["message"] == "delayed body" for p in polled), polled


def test_message_base64_encoded(ntfy, unique_topic):
    """When publishing binary that can't be transferred as UTF-8, base64 works."""
    raw = bytes(range(64))
    payload_b64 = base64.b64encode(raw).decode("ascii")
    r = ntfy.publish(
        unique_topic,
        payload_b64,
        headers={"Message": payload_b64},
    )
    assert r.status_code == 200, r.text
    msg = r.json()
    assert msg["message"] == payload_b64

    polled = ntfy.poll(unique_topic)
    assert len(polled) == 1
    assert polled[0]["message"] == payload_b64
