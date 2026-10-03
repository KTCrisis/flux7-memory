package memory

import (
	"time"
)

// Fact is the internal row shape shared by the markdown writer and the
// SQLite index. It is intentionally unexported : the Dispatcher talks
// to the outside world in terms of map[string]any (MCP arguments) and
// the Store converts between the two.
//
// The schema mirrors the roadmap's v0.2.0 facts table. For v0.2.0 the
// tool API is still key/value, so every fact has predicate="note" ;
// richer predicates land with the v2 temporal graph (Phase 4).
type fact struct {
	ID         int64
	Entity     string
	Predicate  string
	Object     string
	Tags       []string
	Agent      string
	TraceID    string // W3C trace id of the governed call that wrote it (provenance)
	TTL        int
	SourceFile string
	SourceLine int
	Created    time.Time
	Updated    time.Time
	Deleted    *time.Time

	// Bi-temporal: when the fact holds in the world (valid) and when mem7
	// believed it (tx). Zero means open: no start, or still holding.
	ValidFrom time.Time
	ValidTo   time.Time
	TxFrom    time.Time
	TxTo      time.Time
}

// temporal selects which versions of a fact a read sees. The zero value is
// the usual question: what mem7 believes now, of what holds now.
type temporal struct {
	AsOf    time.Time // what mem7 believed at that moment (zero: now)
	ValidAt time.Time // what held in the world at that moment (zero: now)
}

func (t temporal) zero() bool { return t.AsOf.IsZero() && t.ValidAt.IsZero() }

// filter is the query shape accepted by the storage layer. All fields
// are optional ; an empty filter matches every live row.
type filter struct {
	Entity string
	Tags   []string
	Agent  string
	Limit  int
	When   temporal
}

// searchQuery carries the parameters of a full-text search. Query is
// passed through to FTS5 MATCH ; the rest act as post-filters.
//
// Mode selects how Query is interpreted :
//   - "raw" (default, back-compat) : the query is passed verbatim to FTS5
//     after sanitisation (quoting hyphens/accents/punctuation). Callers
//     own the FTS5 operators.
//   - "natural" : the query is first naturalised (stop words stripped,
//     wildcard stemming, OR-joined) so that a plain-English question like
//     "When did Melanie paint a sunrise?" returns sensible results.
//
// IncludeNeighbors, when true, expands the result set with adjacent
// sequential memories. NeighborRadius controls how far (default 1).
// See internal/memory/neighbors.go for the full logic.
type searchQuery struct {
	Query            string
	Mode             string
	Tags             []string
	Agent            string
	Since            time.Time
	Until            time.Time
	When             temporal
	Limit            int
	IncludeNeighbors bool
	NeighborRadius   int
}

// defaultPredicate is what the v0.2.0 key/value tool API writes into
// the predicate column. Phase 4 will introduce richer predicates.
const defaultPredicate = "note"
