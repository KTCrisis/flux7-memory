# Memories are bi-temporal versions

- **Problem**: a store on an existing key overwrote the index row: mem7 could not say what held on a past date, what it believed then, or what replaced a decision; only the markdown kept the old values.
- **Decision**: the index keeps versions with `valid_from`/`valid_to` and `tx_from`/`tx_to`; a new version closes the overlapping ones and re-believes their parts outside its period; forget closes `tx_to`; reads take `valid_at` and `as_of`; an index from before versions is rebuilt from the markdown at start, embeddings kept.
- **Why**: "what replaced Monday's decision, and who decided" is the question governed memories must answer, and the markdown already held the history; defaults keep every existing call unchanged.
- **Where**: `internal/memory/sqlite.go` (`Put`, `copyVersion`, `liveWhere`, migration), `temporal.go`, `store.go`, `rescan.go`, `markdown.go`, `chain.go` (valid fields sealed only when present). Out of scope: contradictions between different keys.
