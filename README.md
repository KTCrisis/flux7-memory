```
   ______ ____                  
  / __/ //_  /_____ _  ___ __ _ 
 / _// /__/ /___/  ' \/ -_)  ' \
/_/ /____/_/   /_/_/_/\__/_/_/_/
```

# flux7-memory

[![GitHub release](https://img.shields.io/github/v/release/KTCrisis/flux7-memory?style=flat-square&color=00bcd4)](https://github.com/KTCrisis/flux7-memory/releases)
[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev)
[![License](https://img.shields.io/badge/License-Apache_2.0-blue?style=flat-square)](LICENSE)

A lightweight MCP server in Go for shared memory across AI agents. Single binary, zero cgo, usable standalone over stdio or as a shared daemon behind [flux7-mesh](https://github.com/KTCrisis/flux7-mesh). Hybrid markdown + SQLite store with full-text search, optional dense-vector hybrid retrieval, LLM reranking, and three transports: MCP stdio, HTTP JSON-RPC, and MCP SSE. Comes with a [Python SDK](#python-sdk) for provider-agnostic integration.

## Features

- **7 MCP tools** — `memory_store`, `memory_recall`, `memory_search`, `memory_context`, `memory_get`, `memory_list`, `memory_forget`
- **Hybrid storage** — append-only markdown workspace as source of truth, SQLite (FTS5) as a rebuildable index
- **Field-weighted BM25** — FTS5 ranking with tuned weights: object content (5x), entity key (2x), tags (0.5x)
- **Hybrid search (opt-in)** — BM25 + dense cosine similarity merged via Reciprocal Rank Fusion (RRF). Requires an external embedding provider (Ollama or any OpenAI-compatible API)
- **LLM reranking (opt-in)** — post-RRF listwise reranking via Ollama, with graceful degradation if the reranker is unavailable
- **Natural language mode** — `mode="natural"` strips stop words, applies wildcard stemming, and OR-joins tokens so agents can query in plain language instead of FTS5 syntax
- **Neighbor inclusion** — `include_neighbors=true` automatically fetches sequential neighbors (e.g. `t004`, `t006` around `t005`) to capture context spread across consecutive entries
- **Access tracking** — `access_count` and `last_accessed` are bumped on `memory_recall`, providing usage signals without creating feedback loops
- **Three transports** — MCP stdio (default, for Claude Code / Cursor), HTTP JSON-RPC via `mem7 serve` (for SDKs and direct API calls), and MCP SSE via `GET /sse` (for flux7-mesh daemon mode — one process, shared DB)
- **Snapshot reminder** — `POST /memory/snapshot_reminder` (and the matching MCP method) lets an agent runtime inject a pre-compaction instruction into its context
- **Rebuildable index** — `mem7 rescan` drops the SQLite index and replays the markdown workspace to restore consistency
- **Tag filters, agent tracking, TTL**

## Quick start

```bash
go install github.com/KTCrisis/flux7-memory/cmd/mem7@latest
```

Or build from source :

```bash
cd flux7-memory
go build -o ~/go/bin/mem7 ./cmd/mem7
```

Default stdio mode (MCP client spawns the binary) :

```bash
~/go/bin/mem7
```

If a `mem7 serve` daemon is already running, stdio mode auto-detects it and becomes a thin proxy (stdin↔HTTP) instead of opening a second local store. Same command, zero config change.

Daemon mode (shared across multiple clients via HTTP + SSE) :

```bash
MEM7_TOKEN=mem7_secret123 ~/go/bin/mem7 serve --listen :9070
```

Exposes `/rpc` (HTTP JSON-RPC), `/sse` + `/messages` (MCP SSE transport), `/healthz`, and `/memory/snapshot_reminder`. flux7-mesh connects via SSE for MCP tool calls and via `/rpc` for decision writes — one daemon, one database.

Rebuild the SQLite index from the markdown workspace :

```bash
~/go/bin/mem7 rescan
```

Drop TTL-expired entries from the index (the markdown workspace is left untouched ; rescan re-evaluates TTL on replay) :

```bash
~/go/bin/mem7 prune
```

## Configuration

| Variable | Default | Description |
|----------|---------|-------------|
| `MEM7_DIR` | `~/.mem7` | Data directory (hosts `workspace/` and `index.db`) |
| `MEM7_LISTEN` | `:9070` | HTTP bind address when in `serve` mode |
| `MEM7_TOKEN` | *(empty)* | Bearer token required on `/rpc` and `/memory/*` when set |
| `MEM7_MAX_ENTRIES` | `10000` | Soft ceiling on live entries |
| `MEM7_EMBED_URL` | *(empty)* | Base URL of the embedding provider. Setting this enables hybrid search |
| `MEM7_EMBED_MODEL` | `nomic-embed-text` | Model name passed to the embedding API |
| `MEM7_EMBED_PROVIDER` | `ollama` | Provider format: `ollama` (POST `/api/embed`) or `openai` (POST `/v1/embeddings`) |
| `MEM7_EMBED_KEY` | *(empty)* | Bearer token for the embedding API (required for OpenAI, optional for Ollama) |
| `MEM7_RERANK_URL` | *(empty)* | Base URL of the reranking LLM. Setting this enables LLM reranking after RRF merge |
| `MEM7_RERANK_MODEL` | `gemma4:e4b` | Model name passed to the Ollama generate API for reranking |

Flags on `mem7 serve` mirror `MEM7_LISTEN` and `MEM7_TOKEN` : `--listen :9070 --token mem7_...`.

### Hybrid search setup

Hybrid search is entirely opt-in. Without `MEM7_EMBED_URL`, mem7 uses pure BM25.

**With local Ollama :**

```bash
MEM7_EMBED_URL=http://localhost:11434 \
MEM7_EMBED_MODEL=nomic-embed-text \
  ~/go/bin/mem7
```

**With OpenAI API :**

```bash
MEM7_EMBED_URL=https://api.openai.com \
MEM7_EMBED_MODEL=text-embedding-3-small \
MEM7_EMBED_PROVIDER=openai \
MEM7_EMBED_KEY=sk-... \
  ~/go/bin/mem7
```

**With any OpenAI-compatible endpoint** (vLLM, LiteLLM, Azure OpenAI, etc.) :

```bash
MEM7_EMBED_URL=http://localhost:8000 \
MEM7_EMBED_MODEL=BAAI/bge-small-en-v1.5 \
MEM7_EMBED_PROVIDER=openai \
  ~/go/bin/mem7
```

When enabled, `memory_store` computes and persists an embedding alongside each entry. `memory_search` retrieves BM25 top-2N and cosine top-2N candidates, then merges them via Reciprocal Rank Fusion (RRF, k=60) into the final top-N. Embeddings are stored as BLOBs in SQLite and cached in memory for sub-ms cosine search.

### LLM reranking setup

LLM reranking is opt-in on top of hybrid search. It over-fetches 3x candidates, merges via RRF, then uses an LLM to score relevance before returning the final top-N. Falls back to non-reranked results if the LLM is unavailable.

```bash
MEM7_EMBED_URL=http://localhost:11434 \
MEM7_RERANK_URL=http://localhost:11434 \
MEM7_RERANK_MODEL=gemma4:e4b \
  ~/go/bin/mem7
```

## Python SDK

A provider-agnostic Python client for mem7, wrapping all MCP tools via JSON-RPC over HTTP.

### Install

```bash
pip install flux7-memory
```

Or from source :

```bash
pip install ./sdk/python
```

### Usage

```python
from mem7 import Mem7

m = Mem7("http://localhost:9070", token="my-token")

# Store a memory
m.store("user.prefs", "prefers dark mode", tags=["user"])

# Search (returns formatted text)
print(m.search("dark mode", limit=5))

# Context (returns structured Memory objects)
for mem in m.context("dark mode", limit=5):
    print(f"{mem.key}: {mem.value}")

# Formatted block for LLM prompt injection
block = m.context_block("user preferences", limit=10)

# Other tools
m.recall(key="user.prefs")
m.list(tags=["user"])
m.get("memory/2026-05-07.md")
m.forget(key="user.prefs")
```

## Workspace layout

```
~/.mem7/
├── workspace/
│   ├── MEMORY.md                      # reserved for long-term notes
│   └── memory/
│       ├── 2026-04-11.md              # append-only daily logs
│       └── 2026-04-12.md
└── index.db                           # SQLite (facts + facts_fts + embeddings)
```

The markdown files are the source of truth ; `index.db` is a derived cache that can be dropped and rebuilt from the markdown at any time via `mem7 rescan`.

Each entry is written as a level-2 heading followed by a fenced `mem7` envelope (plain key/value metadata) and a free-form body, terminated by a horizontal rule. A human can edit these files in place — the next `rescan` picks up the changes.

Example :

````markdown
## example_key

```mem7
op: store
agent: claude
tags: demo, example
created: 2026-04-11T20:00:00Z
updated: 2026-04-11T20:00:00Z
```

Free-form markdown content lives here.

---
````

## Usage with flux7-mesh

In your `config.yaml` :

```yaml
mcp_servers:
  - name: memory
    transport: stdio
    command: /home/user/go/bin/mem7
    env:
      MEM7_DIR: /home/user/.mem7
```

flux7-mesh discovers the tools via `tools/list` ; no per-tool wiring is required. Grants and policies apply as usual.

To share the same memory across several machines behind flux7-mesh, run `mem7 serve` on one host and point the other hosts at it via the upcoming remote-client mode (Phase 1.5 of the roadmap).

## Tools

### memory_store

Upsert a memory entry by key. The markdown workspace receives an append-only section ; the SQLite index is updated in place. If hybrid search is enabled, an embedding is computed and stored alongside the entry.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `key` | string | yes | Unique key for this memory |
| `value` | string | yes | Content to remember (free-form markdown allowed) |
| `tags` | string[] | no | Tags for filtering and grouping |
| `agent` | string | no | Identifier of the storing agent |
| `ttl` | number | no | Time-to-live in seconds (0 = permanent) |

### memory_recall

Recall memories by key, tags, or agent, most recently updated first. Bumps `access_count` and `last_accessed` on returned entries.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `key` | string | no | Exact key to recall |
| `tags` | string[] | no | Filter by tags (AND logic) |
| `agent` | string | no | Filter by agent |
| `limit` | number | no | Max results (default 10) |

### memory_search

Full-text search over memories using SQLite FTS5, ranked by field-weighted BM25. When hybrid search is enabled, results are merged with dense cosine similarity via RRF. Supports FTS5 operators in raw mode : `foo*` prefix, `AND` / `OR` / `NOT`, quoted phrases.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | yes | Search query |
| `mode` | string | no | `raw` (default, FTS5 syntax) or `natural` (plain language, auto-stemmed) |
| `tags` | string[] | no | Post-filter by tags |
| `agent` | string | no | Post-filter by agent |
| `since` | string | no | Lower bound on `updated_at` (RFC3339) |
| `until` | string | no | Upper bound on `updated_at` (RFC3339) |
| `limit` | number | no | Max results (default 10) |
| `include_neighbors` | boolean | no | Fetch sequential neighbors around matching entries (default false) |
| `neighbor_radius` | number | no | How many neighbors to fetch on each side (default 1) |

### memory_context

Same search capabilities as `memory_search` but returns a JSON array of structured objects instead of formatted markdown. Designed for programmatic use by agent SDKs.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `query` | string | yes | Search query |
| `mode` | string | no | `raw` (default) or `natural` |
| `tags` | string[] | no | Post-filter by tags |
| `agent` | string | no | Post-filter by agent |
| `since` | string | no | Lower bound on `updated_at` (RFC3339) |
| `until` | string | no | Upper bound on `updated_at` (RFC3339) |
| `limit` | number | no | Max results (default 10) |
| `include_neighbors` | boolean | no | Fetch sequential neighbors (default false) |
| `neighbor_radius` | number | no | Neighbors on each side (default 1) |

Returns a JSON array of `{ "key", "value", "tags", "agent", "updated" }` objects.

### memory_get

Read a file from the markdown workspace, optionally between `from_line` and `to_line` (1-indexed, inclusive). Paths are resolved relative to the workspace root and refused if they escape it.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `path` | string | yes | Workspace-relative path (e.g. `memory/2026-04-11.md`) |
| `from_line` | number | no | First line to read |
| `to_line` | number | no | Last line to read |

### memory_list

List memory keys with metadata (without values).

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `tags` | string[] | no | Filter by tags |
| `agent` | string | no | Filter by agent |

### memory_forget

Delete memories by key and/or tags. A tombstone section is appended to the markdown workspace, and the SQLite index soft-deletes the matching rows.

| Parameter | Type | Required | Description |
|-----------|------|----------|-------------|
| `key` | string | no | Exact key to delete |
| `tags` | string[] | no | Delete all entries matching these tags (AND logic) |
| `agent` | string | no | Recorded on the tombstone |

## HTTP endpoints

`mem7 serve` exposes these routes :

| Method | Path | Description |
|--------|------|-------------|
| `GET`  | `/healthz` | Liveness probe (always public, no auth) |
| `POST` | `/rpc` | JSON-RPC 2.0 endpoint — same MCP tool surface as stdio |
| `POST` | `/memory/snapshot_reminder` | Returns a structured instructional payload for an agent runtime to inject into its context before compaction |

Bearer auth is applied to `/rpc` and `/memory/*` when `MEM7_TOKEN` (or `--token`) is set.

Example :

```bash
curl -s -X POST http://localhost:9070/rpc \
  -H "Authorization: Bearer $MEM7_TOKEN" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/call",
       "params":{"name":"memory_search","arguments":{"query":"roadmap*"}}}'
```

## Architecture

```
      Claude Code / flux7-mesh / Python SDK / scripts
                    │
          MCP stdio ┴ HTTP JSON-RPC
                    │
              ┌─────▼─────┐
              │ Dispatcher │   ← MCP protocol layer
              └─────┬─────┘
                    │
              ┌─────▼─────┐
              │   Store    │   ← orchestrator
              └──┬──┬──┬──┬┘
                 │  │  │  │
          ┌──────▼┐ │ ┌▼──────────┐ ┌▼─────────┐
          │markdown│ │ │ sqlite    │ │ reranker  │
          │workspace│ │ │ (facts +  │ │ (Ollama)  │
          │(truth) │ │ │ FTS5 +    │ │ opt-in    │
          └────────┘ │ │ embeds)   │ └───────────┘
                     │ └───────────┘
              ┌──────▼──────┐
              │  embedder   │  ← opt-in, external
              │ (Ollama /   │
              │  OpenAI)    │
              └─────────────┘
```

Every write goes through the markdown writer first and then updates the SQLite index. If hybrid search is enabled, an embedding is computed via the external provider and stored as a BLOB. Reads consult the index only ; embeddings are cached in memory for sub-ms cosine search. If the index is corrupted or out of sync, `mem7 rescan` drops it and replays the markdown chronologically to reconstruct a consistent state.

## License

Apache 2.0
