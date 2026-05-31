"""Real-time subscription tests: JSON stream and WebSocket.

These tests use the ``/<topic>/json`` stream (newline-delimited JSON)
and ``/<topic>/ws`` (WebSocket) endpoints to verify that ntfy's
MySQL-backed cache + topic broadcaster correctly publish live
messages to subscribed clients.
"""

from __future__ import annotations

import json
import threading
import time
from contextlib import closing

import requests
import websocket


def _stream_collect(url: str, n: int, timeout: float = 15.0) -> list[dict]:
    """Open the JSON stream and read until n messages arrive or timeout."""
    out: list[dict] = []
    deadline = time.monotonic() + timeout
    with closing(requests.get(url, stream=True, timeout=timeout)) as r:
        r.raise_for_status()
        for line in r.iter_lines():
            if line:
                evt = json.loads(line)
                if evt.get("event") == "message":
                    out.append(evt)
                    if len(out) >= n:
                        break
            if time.monotonic() > deadline:
                break
    return out


def test_json_stream_receives_live_messages(ntfy, ntfy_base_url, unique_topic):
    """A live JSON stream subscriber sees messages as they're published."""
    received: list[dict] = []
    err: list[BaseException] = []

    def consume():
        try:
            received.extend(
                _stream_collect(f"{ntfy_base_url}/{unique_topic}/json", n=3, timeout=15.0)
            )
        except BaseException as exc:  # noqa: BLE001
            err.append(exc)

    t = threading.Thread(target=consume, daemon=True)
    t.start()

    # Give the subscriber a moment to attach
    time.sleep(0.7)

    for i in range(3):
        ntfy.publish(unique_topic, f"live-{i}").raise_for_status()
        time.sleep(0.1)

    t.join(timeout=20.0)
    assert not err, err
    assert [m["message"] for m in received] == ["live-0", "live-1", "live-2"]


def test_websocket_receives_live_messages(ntfy, ntfy_base_url, unique_topic):
    """The WebSocket endpoint must deliver published messages in real time.

    The ``open`` event is sent first when the WS attaches, then each
    published message arrives as a JSON frame with ``event=message``.
    """
    received: list[dict] = []
    err: list[BaseException] = []
    open_evt = threading.Event()

    ws_url = ntfy_base_url.replace("http://", "ws://").replace("https://", "wss://")
    ws_url = f"{ws_url}/{unique_topic}/ws"

    def on_message(_ws, raw):
        try:
            evt = json.loads(raw)
        except json.JSONDecodeError:
            return
        if evt.get("event") == "open":
            open_evt.set()
        elif evt.get("event") == "message":
            received.append(evt)
            if len(received) >= 3:
                _ws.close()

    def on_error(_ws, exc):
        err.append(exc)

    ws = websocket.WebSocketApp(ws_url, on_message=on_message, on_error=on_error)
    t = threading.Thread(target=ws.run_forever, daemon=True,
                         kwargs={"ping_interval": 0})
    t.start()

    assert open_evt.wait(timeout=5.0), "websocket never received an open event"

    for i in range(3):
        ntfy.publish(unique_topic, f"ws-{i}").raise_for_status()
        time.sleep(0.1)

    t.join(timeout=10.0)
    assert not err, err
    assert [m["message"] for m in received] == ["ws-0", "ws-1", "ws-2"]


def test_multiple_topics_one_stream(ntfy, ntfy_base_url, unique_topic):
    """A comma-joined topic list in the URL must multiplex broadcasts."""
    topic_a = f"{unique_topic}-a"
    topic_b = f"{unique_topic}-b"
    received: list[dict] = []
    err: list[BaseException] = []

    def consume():
        try:
            received.extend(
                _stream_collect(
                    f"{ntfy_base_url}/{topic_a},{topic_b}/json",
                    n=2,
                    timeout=15.0,
                )
            )
        except BaseException as exc:  # noqa: BLE001
            err.append(exc)

    t = threading.Thread(target=consume, daemon=True)
    t.start()

    time.sleep(0.7)
    ntfy.publish(topic_a, "from-a").raise_for_status()
    time.sleep(0.1)
    ntfy.publish(topic_b, "from-b").raise_for_status()

    t.join(timeout=20.0)
    assert not err, err
    topics_seen = sorted([m["topic"] for m in received])
    assert topics_seen == sorted([topic_a, topic_b])


def test_sse_stream_delivers_messages(ntfy, ntfy_base_url, unique_topic):
    """Server-Sent Events (``/sse``) deliver ``data:`` lines with JSON payloads."""
    received: list[dict] = []
    err: list[BaseException] = []

    def consume():
        try:
            with closing(requests.get(
                f"{ntfy_base_url}/{unique_topic}/sse", stream=True, timeout=15.0
            )) as r:
                r.raise_for_status()
                deadline = time.monotonic() + 15.0
                for line in r.iter_lines():
                    if line and line.startswith(b"data:"):
                        evt = json.loads(line[5:].strip())
                        if evt.get("event") == "message":
                            received.append(evt)
                            if len(received) >= 2:
                                return
                    if time.monotonic() > deadline:
                        return
        except BaseException as exc:  # noqa: BLE001
            err.append(exc)

    t = threading.Thread(target=consume, daemon=True)
    t.start()

    time.sleep(0.7)
    ntfy.publish(unique_topic, "sse-1").raise_for_status()
    time.sleep(0.1)
    ntfy.publish(unique_topic, "sse-2").raise_for_status()

    t.join(timeout=20.0)
    assert not err, err
    assert [m["message"] for m in received] == ["sse-1", "sse-2"]
