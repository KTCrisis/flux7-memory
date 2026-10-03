package memory

import (
	"fmt"
	"sort"
	"time"
)

// Rescan drops the SQLite index and replays every entry in the
// markdown workspace, in chronological order, to rebuild a fresh
// index that exactly reflects the current on-disk markdown state.
//
// Safe to call at any time : if the index is corrupted or out of sync
// with the markdown, running rescan restores consistency. Markdown
// remains the source of truth ; the index is a cache.
//
// Returns the number of live entries in the rebuilt index.
func (s *Store) Rescan() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := s.index.Reset(); err != nil {
		return 0, fmt.Errorf("reset index: %w", err)
	}

	files, err := listDailyFiles(s.dir)
	if err != nil {
		return 0, fmt.Errorf("list daily files: %w", err)
	}

	var all []mdEntry
	for _, path := range files {
		entries, err := parseDailyFile(path)
		if err != nil {
			return 0, fmt.Errorf("parse %s: %w", path, err)
		}
		all = append(all, entries...)
	}

	// Sort chronologically by updated/deleted timestamp, stable so
	// that entries with identical timestamps keep their file order.
	sort.SliceStable(all, func(i, j int) bool {
		return entryTime(all[i]).Before(entryTime(all[j]))
	})

	now := time.Now().UTC()
	for _, e := range all {
		switch e.Op {
		case "", "store":
			// Skip TTL-expired entries on replay : they would only
			// be filtered by liveWhereClause on the next query, but
			// would still bloat the index until the next prune.
			if e.TTL > 0 && !e.Updated.IsZero() &&
				e.Updated.Add(time.Duration(e.TTL)*time.Second).Before(now) {
				continue
			}
			f := fact{
				Entity:     e.Entity,
				Predicate:  e.Predicate,
				Object:     e.Body,
				Tags:       e.Tags,
				Agent:      e.Agent,
				TraceID:    e.TraceID,
				TTL:        e.TTL,
				Created:    e.Created,
				Updated:    e.Updated,
				SourceFile: e.SourceFile,
				SourceLine: e.SourceLine,
			}
			if f.Predicate == "" {
				f.Predicate = defaultPredicate
			}
			if _, err := s.index.Put(f); err != nil {
				return 0, fmt.Errorf("replay store %q: %w", e.Entity, err)
			}
		case "delete":
			if _, err := s.index.DeleteByEntity(e.Entity); err != nil {
				return 0, fmt.Errorf("replay delete %q: %w", e.Entity, err)
			}
		case "delete_tags":
			if _, err := s.index.DeleteByTags(e.Tags); err != nil {
				return 0, fmt.Errorf("replay delete_tags: %w", err)
			}
		}
	}

	return s.index.Count()
}

// entryTime returns the canonical timestamp to use when sorting a
// mixed list of store and delete entries chronologically. Store ops
// use updated ; delete ops use deleted ; both fall back to created.
func entryTime(e mdEntry) time.Time {
	switch e.Op {
	case "delete", "delete_tags":
		if !e.Deleted.IsZero() {
			return e.Deleted
		}
	}
	if !e.Updated.IsZero() {
		return e.Updated
	}
	return e.Created
}
