# Memories carry the governed call's trace; agents are scoped by identities the mesh vouches for

- **Problem**: a memory recorded only a self-declared `agent`; any client could read and write under any name, and nothing linked a memory to the decision that produced it (audit of 13/09).
- **Decision**: the trace id from `_meta.traceparent` is stored on every write and deletion (markdown + index); the agent in `_meta.art.flux7/agent` is honoured only on a request with the bearer token and replaces the declared one; a JSON scopes file restricts what identified agents read, overwrite and forget. `memory_forget` declares `agent`.
- **Why**: provenance makes "decisions as facts" literal; the token is what separates the mesh from any local process; per-agent scopes stay simple and leave per-fact ACLs out, consistent with governance living in the mesh.
- **Where**: `internal/memory/caller.go`, `store.go` (`Tool*As`), `markdown.go`, `sqlite.go` (also: `Reset` now runs migrations), `internal/transport/http.go`, `cmd/mem7/main.go` (`--scopes`), `contrib/systemd/enable-token.sh`.
