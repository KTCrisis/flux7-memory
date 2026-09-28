# mem7 serves MCP over Streamable HTTP on /mcp; HTTP+SSE stays for old clients

- **Problem**: mem7 served MCP over HTTP only through the HTTP+SSE pair (`/sse`, `/messages`), which the MCP spec 2026-07-28 formally deprecates, and announced a fixed `2024-11-05` that contradicted any newer transport.
- **Decision**: `POST /mcp` serves Streamable HTTP without a protocol session (plain JSON answers, `202` for notifications, `405` for GET and DELETE, same bearer auth); `initialize` negotiates the version (2025-11-25 down to 2024-11-05); `/sse` and `/messages` are kept and marked deprecated.
- **Why**: mem7 never speaks first and needs no session, so the simplest conforming server is also the smallest; keeping the old routes lets clients move at their own pace. Verified with mesh7 as a `streamable-http` upstream on the author's machine.
- **Where**: `internal/transport/streamable.go`, `internal/memory/dispatcher.go`.
