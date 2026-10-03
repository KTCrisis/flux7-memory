package memory

import "time"

// storage is the private index contract consumed by Store. It abstracts
// whatever backend holds the derived index — today SQLite, tomorrow
// possibly Postgres (Phase 4). The interface lives where it is
// consumed, not where it is implemented : see the Phase 1.0 discussion
// on consumer-side interfaces.
//
// The index is a cache of the current state reconstructible from the
// markdown workspace at any time via Rescan. Every method except
// Rescan / Reset / Close is expected to be fast and side-effect-free
// on disk beyond the index itself.
type storage interface {
	// Put records a new version of a fact keyed by (entity, predicate),
	// bi-temporally (see sqliteStore.Put). The returned fact carries any
	// server-assigned fields (ID).
	Put(f fact) (fact, error)

	// Query returns facts matching the filter, most recently updated
	// first, honouring TTL expiry and soft deletion.
	Query(f filter) ([]fact, error)

	// List returns fact metadata (without the object body) for
	// browsing. Same filter semantics as Query.
	List(f filter) ([]fact, error)

	// Search runs a full-text query over the FTS5 index, ranked by
	// BM25, with post-filters applied. Returns empty slice (not nil)
	// when there are no matches.
	Search(q searchQuery) ([]fact, error)

	// DeleteByEntity soft-deletes the fact with this exact entity
	// (any predicate). Returns the number of rows affected.
	DeleteByEntity(entity string, at time.Time) (int, error)

	// DeleteByTags soft-deletes every fact whose tag set contains
	// all of the supplied tags. Returns the number of rows affected.
	DeleteByTags(tags []string, at time.Time) (int, error)

	// TouchAccessed increments the access_count and sets last_accessed
	// for the given fact IDs. Used to track usage frequency for scoring.
	TouchAccessed(ids []int64) error

	// StoreEmbedding persists a dense vector for the given fact ID.
	StoreEmbedding(id int64, vec []float32) error

	// LoadEmbeddings returns all live fact embeddings keyed by ID.
	LoadEmbeddings(when temporal) (map[int64][]float32, error)

	// FetchByIDs returns facts for the given IDs, respecting liveness.
	FetchByIDs(ids []int64, when temporal) ([]fact, error)

	// Count returns the number of live facts (not deleted, not expired).
	Count() (int, error)

	// PurgeExpired physically removes facts whose TTL has elapsed
	// (ttl > 0 AND updated_at + ttl <= now). Soft-deleted rows are
	// left untouched — call DeleteByEntity / DeleteByTags first if
	// you want to also reclaim those. Returns the number of rows
	// removed.
	PurgeExpired() (int, error)

	// Reset drops and recreates the index tables. Used by rescan
	// before replaying the markdown workspace.
	Reset() error

	// Close releases any underlying resources.
	Close() error
}
