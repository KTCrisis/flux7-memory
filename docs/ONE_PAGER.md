# mem7 — Governed Memory for AI Agents

## The problem

You're building agents. They work for one session, then forget everything. You add memory — a vector store, maybe Mem0 or Zep — and single-agent workflows improve. Then you scale to multiple agents, and new problems appear that no memory system addresses :

- **Agent A approved something last week. Agent B doesn't know.** Human decisions aren't stored as queryable facts. They're lost in chat logs.
- **Three agents write to the same memory. Who wrote what ? Who can read what ?** No existing system tracks provenance or enforces access control at the fact level.
- **Your agent confidently uses a memory from 6 months ago.** No staleness signal, no lifecycle management. Memory rots silently.
- **A client asks for an audit trail of what the agent decided.** You have logs somewhere. They're not queryable. Good luck.

These aren't retrieval problems. They're governance problems. And they've been solved before — in databases, event platforms, schema registries. They just haven't been brought to AI memory yet.

## What mem7 is

Persistent, searchable, governed memory for your agents. `pip install mem7` and go :

```python
from mem7 import Mem7

m = Mem7("http://localhost:9070", token="my-token")

# Any agent stores facts
m.store("deploy.decision", "approved by ops lead, prod deploy greenlit",
        tags=["decision", "deploy"], agent="supervisor")

# Any agent searches
for mem in m.context("deployment approval status", limit=5):
    print(f"{mem.key}: {mem.value}")
```

The server is a single Go binary — zero dependencies, runs anywhere, deploys in one command. The SDK talks to it over HTTP, so your agents can be in Python, TypeScript, or anything that speaks JSON-RPC.

**Under the hood :**

- Hybrid search : BM25 + dense cosine + LLM reranking — 71% on the LoCoMo benchmark
- Markdown source of truth + SQLite index, rebuildable with `mem7 rescan`
- Natural language queries — agents don't need to learn FTS5 syntax
- Neighbor expansion — automatically fetches surrounding context for sequential entries
- Tag-based filtering, agent tracking, TTL, temporal range queries
- Three transports : MCP stdio (Claude Code, Cursor) + HTTP JSON-RPC (SDKs) + MCP SSE (daemon mode for agent-mesh)
- Provider-agnostic : works with Ollama, OpenAI, or any compatible embedding API. No vendor lock-in
- Python SDK with structured responses (`Memory` objects, not raw text to parse)

## How it fits in a multi-agent system

```
┌──────────────┐   ┌──────────────┐   ┌──────────────┐
│   Agent A    │   │   Agent B    │   │  Supervisor   │
│  (research)  │   │  (execution) │   │ (human-in-    │
│              │   │              │   │  the-loop)    │
└──────┬───────┘   └──────┬───────┘   └──────┬────────┘
       │ store/search      │ store/search     │ store policies
       │ tags=["research"] │ tags=["exec"]    │ tags=["decision"]
       └──────────┬────────┴──────────────────┘
                  │
            ┌─────▼─────┐
            │   mem7    │  ← one binary, shared memory
            │           │     with agent-scoped tags
            └───────────┘
```

**Agent memory** — each agent reads and writes its own observations, scoped by tags.

**Supervisor memory** — cross-agent view. Policies, human approvals, rejections stored as first-class facts. The supervisor checks "has this been approved ?" before acting, not "let me search the chat history."

**Audit** — every fact carries who wrote it, when, with which tags. Queryable.

## What makes it different

| | Mem0 / Zep / Letta | mem7 |
|---|---|---|
| **Scope** | Single agent, single user | Multi-agent, multi-role |
| **Human decisions** | Not modeled | First-class facts (tags, agent, timestamps) |
| **Access control** | None or coarse | Tag/agent-scoped (per-fact ACL in v0.5) |
| **Provenance** | None | Agent + timestamp on every fact |
| **Vendor lock-in** | Tied to specific LLM providers | Go binary + HTTP SDK, works with anything |
| **Storage** | Opaque | Markdown files you can read and edit |
| **Deployment** | SaaS or heavy dependencies | Single binary, zero CGO, runs anywhere |

## Current state (May 2026)

- **v0.4.1** — 7 MCP tools, Python SDK, hybrid search + LLM reranking, SSE daemon mode
- **71% LoCoMo benchmark** — competitive with VC-backed solutions, without gaming the eval
- **Production use** — backing [agent-mesh](https://github.com/KTCrisis/agent-mesh) orchestrator and [agent7](https://github.com/KTCrisis/agent7) management plane
- **Next** — per-fact ACL + provenance (v0.5), lifecycle states, temporal bi-temporal queries (v1.0)

## Get started

```bash
# Install the server
go install github.com/KTCrisis/mem7/cmd/mem7@latest

# Run with hybrid search (optional, needs Ollama)
MEM7_EMBED_URL=http://localhost:11434 mem7 serve --listen :9070

# Or pure BM25, no dependencies
mem7 serve --listen :9070
```

MIT licensed. [github.com/KTCrisis/mem7](https://github.com/KTCrisis/mem7)
