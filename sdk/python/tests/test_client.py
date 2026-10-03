"""Tests for the mem7 Python SDK client."""
from __future__ import annotations

import json
from unittest.mock import MagicMock, patch

import pytest

from mem7 import Mem7, Memory
from mem7.client import Mem7Error


def _rpc_ok(text: str = "") -> dict:
    return {
        "jsonrpc": "2.0",
        "id": 1,
        "result": {
            "content": [{"type": "text", "text": text}],
        },
    }


def _rpc_tool_error(msg: str) -> dict:
    return {
        "jsonrpc": "2.0",
        "id": 1,
        "result": {
            "isError": True,
            "content": [{"type": "text", "text": msg}],
        },
    }


def _rpc_error(code: int, msg: str) -> dict:
    return {
        "jsonrpc": "2.0",
        "id": 1,
        "error": {"code": code, "message": msg},
    }


@pytest.fixture
def client():
    return Mem7("http://localhost:9070", token="test-token")


class TestInit:
    def test_url_trailing_slash_stripped(self):
        m = Mem7("http://localhost:9070/")
        assert m._url == "http://localhost:9070"

    def test_auth_header_set(self):
        m = Mem7("http://localhost:9070", token="abc")
        assert m._session.headers["Authorization"] == "Bearer abc"

    def test_no_auth_header_without_token(self, monkeypatch):
        monkeypatch.delenv("MEM7_TOKEN", raising=False)
        m = Mem7("http://localhost:9070")
        assert "Authorization" not in m._session.headers

    def test_token_from_environment(self, monkeypatch):
        monkeypatch.setenv("MEM7_TOKEN", "from-env")
        assert Mem7("http://x")._session.headers["Authorization"] == "Bearer from-env"
        assert "Authorization" not in Mem7("http://x", token="")._session.headers

    def test_content_type_set(self):
        m = Mem7("http://localhost:9070")
        assert m._session.headers["Content-Type"] == "application/json"


class TestCall:
    def test_successful_call(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("done")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            result = client._call("memory_store", {"key": "k", "value": "v"})
        assert result == "done"
        call_args = mock_post.call_args
        assert call_args[0][0] == "http://localhost:9070/rpc"
        payload = call_args[1]["json"]
        assert payload["method"] == "tools/call"
        assert payload["params"]["name"] == "memory_store"

    def test_rpc_error_raises(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_error(-32601, "method not found")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp):
            with pytest.raises(Mem7Error, match="method not found"):
                client._call("bad_method", {})

    def test_tool_error_raises(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_tool_error("unknown tool: nope")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp):
            with pytest.raises(Mem7Error, match="unknown tool"):
                client._call("nope", {})

    def test_empty_result(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = {"jsonrpc": "2.0", "id": 1, "result": {}}
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp):
            assert client._call("something", {}) == ""

    def test_request_id_increments(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            client._call("a", {})
            client._call("b", {})
        ids = [c[1]["json"]["id"] for c in mock_post.call_args_list]
        assert ids == [1, 2]


class TestStore:
    def test_minimal(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("created")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            result = client.store("k", "v")
        args = mock_post.call_args[1]["json"]["params"]["arguments"]
        assert args == {"key": "k", "value": "v"}
        assert result == "created"

    def test_all_params(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("created")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            client.store("k", "v", tags=["a"], agent="bot", ttl=3600)
        args = mock_post.call_args[1]["json"]["params"]["arguments"]
        assert args == {"key": "k", "value": "v", "tags": ["a"], "agent": "bot", "ttl": 3600}


class TestSearch:
    def test_minimal(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("results")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            client.search("dark mode")
        args = mock_post.call_args[1]["json"]["params"]["arguments"]
        assert args["query"] == "dark mode"
        assert args["mode"] == "natural"
        assert args["limit"] == 10

    def test_with_neighbors(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("results")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            client.search("q", include_neighbors=True, neighbor_radius=3)
        args = mock_post.call_args[1]["json"]["params"]["arguments"]
        assert args["include_neighbors"] is True
        assert args["neighbor_radius"] == 3

    def test_with_time_range(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("results")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            client.search("q", since="2026-01-01", until="2026-05-01")
        args = mock_post.call_args[1]["json"]["params"]["arguments"]
        assert args["since"] == "2026-01-01"
        assert args["until"] == "2026-05-01"


class TestContext:
    def test_returns_memory_objects(self, client):
        items = [
            {"key": "user.prefs", "value": "dark mode", "tags": ["user"], "agent": "bot", "updated": "2026-05-07T18:00:00Z"},
            {"key": "user.role", "value": "engineer", "tags": [], "agent": "", "updated": "2026-05-07T17:00:00Z"},
        ]
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok(json.dumps(items))
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp):
            memories = client.context("prefs")
        assert len(memories) == 2
        assert isinstance(memories[0], Memory)
        assert memories[0].key == "user.prefs"
        assert memories[0].value == "dark mode"
        assert memories[0].tags == ["user"]
        assert memories[0].agent == "bot"
        assert memories[0].updated == "2026-05-07T18:00:00Z"
        assert memories[1].tags == []

    def test_empty_result(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp):
            memories = client.context("nothing")
        assert memories == []

    def test_calls_memory_context_tool(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("[]")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            client.context("q")
        assert mock_post.call_args[1]["json"]["params"]["name"] == "memory_context"


class TestContextBlock:
    def test_formats_memories(self, client):
        items = [
            {"key": "a.b", "value": "hello world", "tags": [], "agent": "", "updated": ""},
            {"key": "c.d", "value": "foo bar", "tags": [], "agent": "", "updated": ""},
        ]
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok(json.dumps(items))
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp):
            block = client.context_block("q")
        assert "[a.b]" in block
        assert "hello world" in block
        assert "[c.d]" in block

    def test_empty_returns_empty_string(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp):
            assert client.context_block("nothing") == ""


class TestOtherTools:
    def test_recall(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("recalled")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            client.recall(key="k", tags=["t"], agent="a", limit=5)
        args = mock_post.call_args[1]["json"]["params"]["arguments"]
        assert args == {"key": "k", "tags": ["t"], "agent": "a", "limit": 5}

    def test_get(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("content")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            client.get("user.prefs", from_line=1, to_line=10)
        args = mock_post.call_args[1]["json"]["params"]["arguments"]
        assert args == {"path": "user.prefs", "from_line": 1, "to_line": 10}

    def test_list(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("2 memories")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            client.list(tags=["user"])
        args = mock_post.call_args[1]["json"]["params"]["arguments"]
        assert args == {"tags": ["user"]}

    def test_forget(self, client):
        mock_resp = MagicMock()
        mock_resp.json.return_value = _rpc_ok("removed")
        mock_resp.raise_for_status = MagicMock()
        with patch.object(client._session, "post", return_value=mock_resp) as mock_post:
            client.forget(key="k")
        args = mock_post.call_args[1]["json"]["params"]["arguments"]
        assert args == {"key": "k"}


class TestHealth:
    def test_healthy(self, client):
        mock_resp = MagicMock()
        mock_resp.status_code = 200
        with patch.object(client._session, "get", return_value=mock_resp):
            assert client.health() is True

    def test_unhealthy(self, client):
        mock_resp = MagicMock()
        mock_resp.status_code = 500
        with patch.object(client._session, "get", return_value=mock_resp):
            assert client.health() is False

    def test_connection_error(self, client):
        import requests
        with patch.object(client._session, "get", side_effect=requests.ConnectionError):
            assert client.health() is False


class TestTime:
    """mem7 >= 0.8: valid_from/valid_to on writes, valid_at/as_of on reads."""

    def _sent(self, client, method, *args, **kwargs):
        with patch.object(client._session, "post") as post:
            post.return_value = MagicMock(json=lambda: _rpc_ok("[]"), raise_for_status=lambda: None)
            getattr(client, method)(*args, **kwargs)
            return post.call_args.kwargs["json"]["params"]["arguments"]

    def test_store_validity(self, client):
        from datetime import date, datetime, timezone
        args = self._sent(client, "store", "k", "v", valid_from=date(2026, 3, 20),
                          valid_to=datetime(2026, 4, 5, 9, 30, tzinfo=timezone.utc))
        assert args["valid_from"] == "2026-03-20"
        assert args["valid_to"] == "2026-04-05T09:30:00Z"

    def test_store_without_dates_sends_none(self, client):
        args = self._sent(client, "store", "k", "v")
        assert "valid_from" not in args and "valid_to" not in args

    def test_naive_datetime_refused(self, client):
        from datetime import datetime
        with pytest.raises(ValueError):
            self._sent(client, "store", "k", "v", valid_from=datetime(2026, 3, 20))

    @pytest.mark.parametrize("method,extra", [("recall", ()), ("search", ("q",)), ("context", ("q",)), ("list", ())])
    def test_reads_take_valid_at_and_as_of(self, client, method, extra):
        args = self._sent(client, method, *extra, valid_at="2026-03-01", as_of="2026-04-11")
        assert args["valid_at"] == "2026-03-01" and args["as_of"] == "2026-04-11"

    def test_context_returns_provenance_and_time(self, client):
        item = {"key": "k", "value": "v", "tags": [], "agent": "scout7", "updated": "t",
                "trace_id": "abc", "valid_from": "2026-03-20T00:00:00Z", "valid_to": None,
                "tx_from": "2026-10-03T09:00:00Z", "tx_to": None}
        with patch.object(client._session, "post") as post:
            post.return_value = MagicMock(json=lambda: _rpc_ok(json.dumps([item])), raise_for_status=lambda: None)
            m = client.context("q")[0]
        assert (m.trace_id, m.valid_from, m.valid_to) == ("abc", "2026-03-20T00:00:00Z", None)

    def test_forget_signs_with_agent(self, client):
        args = self._sent(client, "forget", key="k", agent="claude")
        assert args == {"key": "k", "agent": "claude"}


class TestHistory:
    def test_parse(self, client):
        text = (
            "History of k (3 events, oldest first):\n"
            "- 2026-10-03T09:12:06Z store by scout7 · valid 2026-02-15T00:00:00Z → now · trace 58c93603 · seal 8cf332f6\n"
            "- 2026-10-03T09:13:00Z delete by tags t1, t2 by claude · trace 90486cd1 · seal ab47df04\n"
            "- 2026-06-07T10:00:00Z store · unsealed (written before the chain)\n"
        )
        with patch.object(client._session, "post") as post:
            post.return_value = MagicMock(json=lambda: _rpc_ok(text), raise_for_status=lambda: None)
            ev = client.history("k")
        assert [e.what for e in ev] == ["store", "delete by tags t1, t2", "store"]
        assert ev[0].agent == "scout7" and ev[0].valid == "2026-02-15T00:00:00Z → now" and ev[0].seal == "8cf332f6"
        assert ev[1].agent == "claude" and ev[2].seal == ""

    def test_chain(self, client):
        with patch.object(client._session, "get") as get:
            get.return_value = MagicMock(json=lambda: {"holds": True, "report": {"sealed": 5}}, raise_for_status=lambda: None)
            assert client.chain()["holds"] is True
            assert get.call_args.args[0].endswith("/memory/chain")
