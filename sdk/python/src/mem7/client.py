"""mem7 Python SDK — governed memory substrate for AI agents.

Usage::

    from mem7 import Mem7

    m = Mem7("http://localhost:9070", token="my-token")
    m.store("user.prefs", "prefers dark mode", tags=["user"])
    results = m.search("dark mode", limit=5)
    memories = m.context("dark mode", limit=5)  # structured JSON

    # mem7 >= 0.8: what held at a date, what mem7 believed at a date
    m.store("event7.host", "Cloudflare", valid_from="2026-03-20")
    m.recall(key="event7.host", valid_at="2026-03-01")
    m.history("event7.host")
"""
from __future__ import annotations

import json
import os
import re
from dataclasses import dataclass, field
from datetime import date, datetime, timezone
from typing import Any, Union

import requests


# A moment for mem7: a date (midnight UTC), an aware datetime, or a string
# mem7 accepts ("2026-03-20", RFC3339).
Moment = Union[str, date, datetime, None]


def _moment(value: Moment) -> str:
    if value is None or value == "":
        return ""
    if isinstance(value, datetime):
        if value.tzinfo is None:
            raise ValueError("naive datetime: give a timezone (mem7 works in UTC)")
        return value.astimezone(timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")
    if isinstance(value, date):
        return value.isoformat()
    return str(value)


def _when(args: dict[str, Any], valid_at: Moment, as_of: Moment) -> None:
    if _moment(valid_at):
        args["valid_at"] = _moment(valid_at)
    if _moment(as_of):
        args["as_of"] = _moment(as_of)


@dataclass
class Memory:
    key: str
    value: str
    tags: list[str] = field(default_factory=list)
    agent: str = ""
    updated: str = ""
    #: trace of the governed call that wrote it (mem7 >= 0.6)
    trace_id: str = ""
    #: when it holds in the world, and when mem7 believed it (mem7 >= 0.8);
    #: None means open
    valid_from: str | None = None
    valid_to: str | None = None
    tx_from: str | None = None
    tx_to: str | None = None


@dataclass
class HistoryEvent:
    """One step in a key's life, from memory_history (mem7 >= 0.7)."""

    when: str
    what: str
    agent: str = ""
    trace: str = ""
    #: first characters of the entry's seal; empty before the hash chain
    seal: str = ""
    #: validity the write declared, "from → to"
    valid: str = ""


class Mem7Error(Exception):
    pass


class Mem7:
    def __init__(self, url: str, token: str | None = None, timeout: int = 30) -> None:
        """token: mem7's bearer token; None reads MEM7_TOKEN from the
        environment, "" sends none."""
        if token is None:
            token = os.environ.get("MEM7_TOKEN", "")
        self._url = url.rstrip("/")
        self._token = token
        self._timeout = timeout
        self._session = requests.Session()
        if token:
            self._session.headers["Authorization"] = f"Bearer {token}"
        self._session.headers["Content-Type"] = "application/json"
        self._req_id = 0

    def _call(self, tool: str, arguments: dict[str, Any]) -> Any:
        self._req_id += 1
        payload = {
            "jsonrpc": "2.0",
            "id": self._req_id,
            "method": "tools/call",
            "params": {"name": tool, "arguments": arguments},
        }
        resp = self._session.post(
            f"{self._url}/rpc", json=payload, timeout=self._timeout
        )
        resp.raise_for_status()
        data = resp.json()
        if "error" in data and data["error"]:
            raise Mem7Error(data["error"].get("message", str(data["error"])))
        result = data.get("result", {})
        if result.get("isError"):
            text = result.get("content", [{}])[0].get("text", "unknown error")
            raise Mem7Error(text)
        content = result.get("content", [])
        if content:
            return content[0].get("text", "")
        return ""

    # ── Core tools ───────────────────────────────────────────────

    def store(
        self,
        key: str,
        value: str,
        *,
        tags: list[str] | None = None,
        agent: str = "",
        ttl: int = 0,
        valid_from: Moment = None,
        valid_to: Moment = None,
    ) -> str:
        """valid_from / valid_to: when the fact holds in the world (mem7 >=
        0.8); valid_from defaults to now, a past date corrects history."""
        args: dict[str, Any] = {"key": key, "value": value}
        if _moment(valid_from):
            args["valid_from"] = _moment(valid_from)
        if _moment(valid_to):
            args["valid_to"] = _moment(valid_to)
        if tags:
            args["tags"] = tags
        if agent:
            args["agent"] = agent
        if ttl > 0:
            args["ttl"] = ttl
        return self._call("memory_store", args)

    def recall(
        self,
        *,
        key: str = "",
        tags: list[str] | None = None,
        agent: str = "",
        limit: int = 10,
        valid_at: Moment = None,
        as_of: Moment = None,
    ) -> str:
        """valid_at: what held then; as_of: what mem7 believed then (mem7 >= 0.8)."""
        args: dict[str, Any] = {"limit": limit}
        _when(args, valid_at, as_of)
        if key:
            args["key"] = key
        if tags:
            args["tags"] = tags
        if agent:
            args["agent"] = agent
        return self._call("memory_recall", args)

    def search(
        self,
        query: str,
        *,
        mode: str = "natural",
        tags: list[str] | None = None,
        agent: str = "",
        limit: int = 10,
        include_neighbors: bool = False,
        neighbor_radius: int = 1,
        since: str = "",
        until: str = "",
        valid_at: Moment = None,
        as_of: Moment = None,
    ) -> str:
        args: dict[str, Any] = {"query": query, "mode": mode, "limit": limit}
        _when(args, valid_at, as_of)
        if tags:
            args["tags"] = tags
        if agent:
            args["agent"] = agent
        if include_neighbors:
            args["include_neighbors"] = True
            args["neighbor_radius"] = neighbor_radius
        if since:
            args["since"] = since
        if until:
            args["until"] = until
        return self._call("memory_search", args)

    def context(
        self,
        query: str,
        *,
        mode: str = "natural",
        tags: list[str] | None = None,
        agent: str = "",
        limit: int = 10,
        include_neighbors: bool = False,
        neighbor_radius: int = 1,
        since: str = "",
        until: str = "",
        valid_at: Moment = None,
        as_of: Moment = None,
    ) -> list[Memory]:
        args: dict[str, Any] = {"query": query, "mode": mode, "limit": limit}
        _when(args, valid_at, as_of)
        if tags:
            args["tags"] = tags
        if agent:
            args["agent"] = agent
        if include_neighbors:
            args["include_neighbors"] = True
            args["neighbor_radius"] = neighbor_radius
        if since:
            args["since"] = since
        if until:
            args["until"] = until
        raw = self._call("memory_context", args)
        items = json.loads(raw) if raw else []
        return [
            Memory(
                key=it.get("key", ""),
                value=it.get("value", ""),
                tags=it.get("tags") or [],
                agent=it.get("agent", ""),
                updated=it.get("updated", ""),
                trace_id=it.get("trace_id") or "",
                valid_from=it.get("valid_from"),
                valid_to=it.get("valid_to"),
                tx_from=it.get("tx_from"),
                tx_to=it.get("tx_to"),
            )
            for it in items
        ]

    def get(self, path: str, *, from_line: int = 0, to_line: int = 0) -> str:
        args: dict[str, Any] = {"path": path}
        if from_line > 0:
            args["from_line"] = from_line
        if to_line > 0:
            args["to_line"] = to_line
        return self._call("memory_get", args)

    def list(
        self,
        *,
        tags: list[str] | None = None,
        agent: str = "",
        valid_at: Moment = None,
        as_of: Moment = None,
    ) -> str:
        args: dict[str, Any] = {}
        _when(args, valid_at, as_of)
        if tags:
            args["tags"] = tags
        if agent:
            args["agent"] = agent
        return self._call("memory_list", args)

    def forget(
        self, *, key: str = "", tags: list[str] | None = None, agent: str = ""
    ) -> str:
        """Stop believing a key (or every key with these tags); a read as_of
        an earlier moment still sees it. agent signs the deletion."""
        args: dict[str, Any] = {}
        if agent:
            args["agent"] = agent
        if key:
            args["key"] = key
        if tags:
            args["tags"] = tags
        return self._call("memory_forget", args)

    def history(self, key: str) -> list[HistoryEvent]:
        """The life of one key, oldest first: every write and deletion with
        its author, trace, seal and declared validity (mem7 >= 0.7)."""
        text = self._call("memory_history", {"key": key})
        return _parse_history(text)

    def chain(self) -> dict[str, Any]:
        """The workspace's hash chain report, as `mem7 verify` prints it:
        {"holds": bool, "report": {entries, sealed, legacy, keyed, break}}
        (mem7 >= 0.7)."""
        resp = self._session.get(f"{self._url}/memory/chain", timeout=self._timeout)
        resp.raise_for_status()
        return resp.json()

    # ── Convenience ──────────────────────────────────────────────

    def health(self) -> bool:
        try:
            resp = self._session.get(
                f"{self._url}/healthz", timeout=self._timeout
            )
            return resp.status_code == 200
        except requests.RequestException:
            return False

    def context_block(
        self,
        query: str,
        *,
        limit: int = 10,
        **kwargs: Any,
    ) -> str:
        """Search and return a formatted text block ready for LLM prompt injection."""
        memories = self.context(query, limit=limit, **kwargs)
        if not memories:
            return ""
        parts = []
        for m in memories:
            header = f"[{m.key}]" if m.key else ""
            if header:
                parts.append(f"{header}\n{m.value}")
            else:
                parts.append(m.value)
        return "\n\n".join(parts)


_BY = re.compile(r" by (\S+)$")


def _parse_history(text: str) -> list[HistoryEvent]:
    """Lines look like "- 2026-10-03T08:47:01Z update by scout7 · valid … ·
    trace 1a2b… · seal ab47df04d620", or end with "· unsealed (…)"."""
    events: list[HistoryEvent] = []
    for line in text.splitlines():
        if not line.startswith("- "):
            continue
        parts = line[2:].split(" · ")
        when, _, what = parts[0].partition(" ")
        ev = HistoryEvent(when=when, what=what)
        m = _BY.search(what)
        if m:
            ev.agent, ev.what = m.group(1), what[: m.start()]
        for p in parts[1:]:
            if p.startswith("trace "):
                ev.trace = p[6:]
            elif p.startswith("seal "):
                ev.seal = p[5:]
            elif p.startswith("valid "):
                ev.valid = p[6:]
        events.append(ev)
    return events
