"""mem7 Python SDK — governed memory substrate for AI agents.

Usage::

    from mem7 import Mem7

    m = Mem7("http://localhost:9070", token="my-token")
    m.store("user.prefs", "prefers dark mode", tags=["user"])
    results = m.search("dark mode", limit=5)
    memories = m.context("dark mode", limit=5)  # structured JSON
"""
from __future__ import annotations

import json
from dataclasses import dataclass, field
from typing import Any

import requests


@dataclass
class Memory:
    key: str
    value: str
    tags: list[str] = field(default_factory=list)
    agent: str = ""
    updated: str = ""


class Mem7Error(Exception):
    pass


class Mem7:
    def __init__(self, url: str, token: str = "", timeout: int = 30) -> None:
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
    ) -> str:
        args: dict[str, Any] = {"key": key, "value": value}
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
    ) -> str:
        args: dict[str, Any] = {"limit": limit}
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
    ) -> str:
        args: dict[str, Any] = {"query": query, "mode": mode, "limit": limit}
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
    ) -> list[Memory]:
        args: dict[str, Any] = {"query": query, "mode": mode, "limit": limit}
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
        self, *, tags: list[str] | None = None, agent: str = ""
    ) -> str:
        args: dict[str, Any] = {}
        if tags:
            args["tags"] = tags
        if agent:
            args["agent"] = agent
        return self._call("memory_list", args)

    def forget(self, *, key: str = "", tags: list[str] | None = None) -> str:
        args: dict[str, Any] = {}
        if key:
            args["key"] = key
        if tags:
            args["tags"] = tags
        return self._call("memory_forget", args)

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
