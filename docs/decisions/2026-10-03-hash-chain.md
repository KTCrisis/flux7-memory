# The markdown workspace is a hash chain

- **Problem**: the workspace is the source of truth and "safe to edit by hand", so nothing showed whether a memory, or a decision kept as a fact, had been changed or removed after the fact.
- **Decision**: every entry mem7 writes carries `prev:` and `hash:`, a seal over its parsed fields and the previous seal (HMAC-SHA256 with `MEM7_CHAIN_KEY`, SHA-256 without); `mem7 verify` names the first break; `memory_history` returns a key's life with author, trace and seal. Older entries count as legacy, the chain starts at the first sealed one.
- **Why**: provenance says who and which call; the chain makes it evidence rather than observation, as the HMAC traces of mesh7 do for decisions. Sealing parsed fields keeps the markdown hand-readable and tolerant of whitespace; a key keeps the holder of the files from resealing an edit.
- **Where**: `internal/memory/chain.go`, `history.go`, `markdown.go` (`appendSealed`, `parseEntries`), `cmd/mem7/main.go` (`verify`, `MEM7_CHAIN_KEY`).
