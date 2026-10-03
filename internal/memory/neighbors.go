package memory

import (
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// neighbor inclusion — when a retrieved memory has a sequential key like
// "conv-26.session_1.t005", fetch and attach the adjacent memories
// (t004, t006) to the result set.
//
// Why : memory is often spread across consecutive turns ("she went to the
// group" / "when ?" / "May 7") — retrieving only the first turn misses the
// follow-up. This pattern recovers multi-turn context without embeddings
// and without a graph store.
//
// How : a sequential key ends with a `<letters><digits>` segment separated
// by dots. The segment identifies a position ; neighbors are at ±radius.

// seqKeyRe matches keys ending in a segment like ".t005", ".s02", ".fact0010".
// Group 1 = everything up to and including the letters ; group 2 = digits.
// Example : "conv-26.session_1.t005" → ("conv-26.session_1.t", "005").
var seqKeyRe = regexp.MustCompile(`^(.*\.[a-zA-Z]+)(\d+)$`)

// parseSequentialKey splits a key into its sequential prefix, numeric index,
// and digit width. Returns ok=false when the key does not follow the pattern.
// Width is preserved so that "t005" stays "t005", not "t5".
func parseSequentialKey(key string) (prefix string, idx int, width int, ok bool) {
	m := seqKeyRe.FindStringSubmatch(key)
	if m == nil {
		return "", 0, 0, false
	}
	prefix = m[1]
	width = len(m[2])
	n, err := strconv.Atoi(m[2])
	if err != nil {
		return "", 0, 0, false
	}
	return prefix, n, width, true
}

// buildNeighborKey reconstructs a key from a parsed prefix, an index, and a
// digit width. "t005" with idx=4 stays "t004".
func buildNeighborKey(prefix string, idx int, width int) string {
	return fmt.Sprintf("%s%0*d", prefix, width, idx)
}

// neighborCandidates returns the keys of the memories around `results` at
// ±radius. Keys that are already in `results` are excluded. Non-sequential
// keys contribute nothing.
func neighborCandidates(results []fact, radius int) []string {
	if radius <= 0 {
		return nil
	}
	have := make(map[string]struct{}, len(results))
	for _, f := range results {
		have[f.Entity] = struct{}{}
	}
	var out []string
	seen := make(map[string]struct{})
	for _, f := range results {
		prefix, idx, width, ok := parseSequentialKey(f.Entity)
		if !ok {
			continue
		}
		for off := -radius; off <= radius; off++ {
			if off == 0 {
				continue
			}
			candidate := idx + off
			if candidate < 0 {
				continue
			}
			key := buildNeighborKey(prefix, candidate, width)
			if _, already := have[key]; already {
				continue
			}
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, key)
		}
	}
	return out
}

// fetchByEntities loads facts whose entity is in the given list. Honours
// liveness (not deleted, TTL-valid) so that neighbors never resurface
// tombstoned or expired entries.
func (s *sqliteStore) fetchByEntities(entities []string) ([]fact, error) {
	if len(entities) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(entities))
	args := make([]any, len(entities))
	for i, e := range entities {
		placeholders[i] = "?"
		args[i] = e
	}
	// liveWhereClause contains `%s` tokens (strftime format specifier), so
	// we must not let Sprintf interpret it — concatenate instead.
	q := `
SELECT f.id, f.entity, f.predicate, f.object, f.tags, f.agent, f.trace_id, f.ttl,
       f.source_file, f.source_line, f.created_at, f.updated_at
FROM facts f
WHERE f.entity IN (` + strings.Join(placeholders, ",") + `)
  AND ` + liveWhereClause

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("fetch neighbors: %w", err)
	}
	defer rows.Close()
	return scanFacts(rows)
}

// scanFacts is a small helper to drain a *sql.Rows into a slice of fact.
// Extracted so neighbor fetch and Search can share the same scan logic.
func scanFacts(rows *sql.Rows) ([]fact, error) {
	out := make([]fact, 0)
	for rows.Next() {
		var fct fact
		var tagsRaw, createdStr, updatedStr string
		if err := rows.Scan(&fct.ID, &fct.Entity, &fct.Predicate, &fct.Object,
			&tagsRaw, &fct.Agent, &fct.TraceID, &fct.TTL, &fct.SourceFile, &fct.SourceLine,
			&createdStr, &updatedStr); err != nil {
			return nil, err
		}
		fct.Tags = unmarshalTags(tagsRaw)
		fct.Created, _ = time.Parse(time.RFC3339, createdStr)
		fct.Updated, _ = time.Parse(time.RFC3339, updatedStr)
		out = append(out, fct)
	}
	return out, rows.Err()
}

// weaveNeighbors returns a new slice where each original hit is followed by
// its discovered neighbors in chronological order within the same prefix.
// Already-present neighbors are kept in their original ranking position ;
// new neighbors are woven adjacent to their parent hit to preserve the
// conversational flow, which is what agents and LLMs expect.
func weaveNeighbors(results []fact, neighbors []fact) []fact {
	if len(neighbors) == 0 {
		return results
	}
	// Index neighbors by parsed (prefix, idx) for quick lookup.
	byPrefixIdx := make(map[string]map[int]fact, len(neighbors))
	for _, n := range neighbors {
		prefix, idx, _, ok := parseSequentialKey(n.Entity)
		if !ok {
			continue
		}
		if _, has := byPrefixIdx[prefix]; !has {
			byPrefixIdx[prefix] = make(map[int]fact)
		}
		byPrefixIdx[prefix][idx] = n
	}

	woven := make([]fact, 0, len(results)+len(neighbors))
	emitted := make(map[string]struct{}, len(results)+len(neighbors))

	emit := func(f fact) {
		if _, dup := emitted[f.Entity]; dup {
			return
		}
		emitted[f.Entity] = struct{}{}
		woven = append(woven, f)
	}

	for _, r := range results {
		prefix, idx, width, ok := parseSequentialKey(r.Entity)
		if !ok {
			emit(r)
			continue
		}
		// Determine how far the neighbor set extends on each side for this parent.
		// We walk outward and emit in chronological order : ..., idx-2, idx-1, idx, idx+1, idx+2, ...
		// This groups related turns together in the response.
		group := byPrefixIdx[prefix]

		// Walk backward from idx-1 down until we stop finding neighbors.
		var before []fact
		for off := 1; ; off++ {
			n, has := group[idx-off]
			if !has {
				break
			}
			before = append([]fact{n}, before...) // prepend so order is chronological
		}
		for _, n := range before {
			emit(n)
		}
		emit(r)
		// Walk forward.
		for off := 1; ; off++ {
			n, has := group[idx+off]
			if !has {
				break
			}
			emit(n)
		}
		_ = width
	}

	// Emit any remaining neighbors whose parent was not in results (shouldn't
	// happen with our candidate logic, but defensive).
	for _, group := range byPrefixIdx {
		for _, n := range group {
			emit(n)
		}
	}

	return woven
}

// expandWithNeighbors runs the neighbor discovery + fetch + weave pipeline
// for a search result set. Returns the results unchanged when radius <= 0
// or when no sequential keys are found.
func (s *sqliteStore) expandWithNeighbors(results []fact, radius int) ([]fact, error) {
	if radius <= 0 || len(results) == 0 {
		return results, nil
	}
	candidates := neighborCandidates(results, radius)
	if len(candidates) == 0 {
		return results, nil
	}
	neighbors, err := s.fetchByEntities(candidates)
	if err != nil {
		return results, err
	}
	return weaveNeighbors(results, neighbors), nil
}
