package memory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// sqliteStore is the SQLite-backed implementation of the storage
// interface. It mirrors the facts schema documented in the roadmap
// and uses FTS5 as the v0.2.0 substrate for memory_search (Phase 1.2).
type sqliteStore struct {
	db *sql.DB
	// needsRebuild: the index was made before bi-temporal versions
	needsRebuild bool
}

// newSQLiteStore opens (and creates if needed) the index database at
// <dir>/index.db and applies the schema.
func newSQLiteStore(dir string) (*sqliteStore, error) {
	path := filepath.Join(dir, "index.db")
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	s := &sqliteStore{db: db}
	if err := s.applySchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS facts (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  entity      TEXT    NOT NULL,
  predicate   TEXT    NOT NULL DEFAULT 'note',
  object      TEXT    NOT NULL,
  tags        TEXT    NOT NULL DEFAULT '[]',
  agent       TEXT    NOT NULL DEFAULT '',
  ttl         INTEGER NOT NULL DEFAULT 0,
  source_file TEXT    NOT NULL,
  source_line INTEGER NOT NULL,
  created_at  TEXT    NOT NULL,
  updated_at    TEXT    NOT NULL,
  deleted_at    TEXT,
  access_count  INTEGER NOT NULL DEFAULT 0,
  last_accessed TEXT
);
CREATE INDEX IF NOT EXISTS idx_facts_versions ON facts(entity, predicate);
CREATE INDEX IF NOT EXISTS idx_facts_updated ON facts(updated_at);
CREATE INDEX IF NOT EXISTS idx_facts_agent ON facts(agent);

CREATE VIRTUAL TABLE IF NOT EXISTS facts_fts USING fts5(
  entity, predicate, object, tags,
  content='facts', content_rowid='id'
);

CREATE TRIGGER IF NOT EXISTS facts_ai AFTER INSERT ON facts BEGIN
  INSERT INTO facts_fts(rowid, entity, predicate, object, tags)
  VALUES (new.id, new.entity, new.predicate, new.object, new.tags);
END;

CREATE TRIGGER IF NOT EXISTS facts_ad AFTER DELETE ON facts BEGIN
  INSERT INTO facts_fts(facts_fts, rowid, entity, predicate, object, tags)
  VALUES ('delete', old.id, old.entity, old.predicate, old.object, old.tags);
END;

CREATE TRIGGER IF NOT EXISTS facts_au AFTER UPDATE ON facts BEGIN
  INSERT INTO facts_fts(facts_fts, rowid, entity, predicate, object, tags)
  VALUES ('delete', old.id, old.entity, old.predicate, old.object, old.tags);
  INSERT INTO facts_fts(rowid, entity, predicate, object, tags)
  VALUES (new.id, new.entity, new.predicate, new.object, new.tags);
END;

CREATE TABLE IF NOT EXISTS fact_tags (
  fact_id INTEGER NOT NULL,
  tag     TEXT    NOT NULL,
  FOREIGN KEY (fact_id) REFERENCES facts(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_fact_tags_tag ON fact_tags(tag, fact_id);
CREATE INDEX IF NOT EXISTS idx_fact_tags_fact ON fact_tags(fact_id);
`

func (s *sqliteStore) applySchema() error {
	_, err := s.db.Exec(schemaSQL)
	if err != nil {
		return fmt.Errorf("apply schema: %w", err)
	}
	return nil
}

func (s *sqliteStore) migrate() error {
	// before bi-temporal versions, (entity, predicate) was unique: one row
	// per key. Its presence means the index predates versions and must be
	// rebuilt from the markdown to recover them (NewStore does it).
	var unique int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_facts_entity_predicate'`).Scan(&unique); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	s.needsRebuild = unique > 0
	alters := []string{
		"ALTER TABLE facts ADD COLUMN access_count INTEGER NOT NULL DEFAULT 0",
		"ALTER TABLE facts ADD COLUMN last_accessed TEXT",
		"ALTER TABLE facts ADD COLUMN embedding BLOB",
		"ALTER TABLE facts ADD COLUMN trace_id TEXT NOT NULL DEFAULT ''",
		"ALTER TABLE facts ADD COLUMN valid_from TEXT",
		"ALTER TABLE facts ADD COLUMN valid_to TEXT",
		"ALTER TABLE facts ADD COLUMN tx_from TEXT",
		"ALTER TABLE facts ADD COLUMN tx_to TEXT",
	}
	for _, ddl := range alters {
		if _, err := s.db.Exec(ddl); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	if _, err := s.db.Exec(`DROP INDEX IF EXISTS idx_facts_entity_predicate`); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// Reset drops and recreates all tables. Used by rescan.
func (s *sqliteStore) Reset() error {
	const dropSQL = `
DROP TRIGGER IF EXISTS facts_ai;
DROP TRIGGER IF EXISTS facts_ad;
DROP TRIGGER IF EXISTS facts_au;
DROP TABLE IF EXISTS fact_tags;
DROP TABLE IF EXISTS facts_fts;
DROP TABLE IF EXISTS facts;
`
	if _, err := s.db.Exec(dropSQL); err != nil {
		return fmt.Errorf("drop tables: %w", err)
	}
	if err := s.applySchema(); err != nil {
		return err
	}
	// the columns added since the first schema: a rescan replays facts
	// that carry them (trace_id) right after this
	return s.migrate()
}

func (s *sqliteStore) Close() error { return s.db.Close() }

func marshalTags(tags []string) string {
	if len(tags) == 0 {
		return "[]"
	}
	b, _ := json.Marshal(tags)
	return string(b)
}

func unmarshalTags(raw string) []string {
	if raw == "" || raw == "[]" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// Put records a new version of the fact, bi-temporally.
//
// f.Updated is the transaction time (when mem7 learns it); f.ValidFrom and
// f.ValidTo bound when it holds in the world (ValidFrom defaults to the
// transaction time, ValidTo to open). Every version mem7 currently believes
// for the same key and whose validity overlaps the new one stops being
// believed (tx_to); the parts of it outside the new validity are believed
// again as their own versions. A new value from now on therefore ends the
// old one now, and a correction of the past replaces only that past.
func (s *sqliteStore) Put(f fact) (fact, error) {
	if f.Predicate == "" {
		f.Predicate = defaultPredicate
	}
	if f.Created.IsZero() {
		f.Created = time.Now().UTC()
	}
	if f.Updated.IsZero() {
		f.Updated = f.Created
	}
	now := f.Updated.UTC()
	if f.ValidFrom.IsZero() {
		f.ValidFrom = now
	}
	f.TxFrom, f.TxTo = now, time.Time{}

	tx, err := s.db.Begin()
	if err != nil {
		return f, fmt.Errorf("put fact: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT id, valid_from, valid_to FROM facts
WHERE entity = ? AND predicate = ? AND tx_to IS NULL AND deleted_at IS NULL`, f.Entity, f.Predicate)
	if err != nil {
		return f, fmt.Errorf("put fact: current versions: %w", err)
	}
	type version struct {
		id     int64
		vf, vt time.Time
	}
	var current []version
	for rows.Next() {
		var v version
		var vf, vt sql.NullString
		if err := rows.Scan(&v.id, &vf, &vt); err != nil {
			rows.Close()
			return f, err
		}
		v.vf, v.vt = parseTS(vf), parseTS(vt)
		current = append(current, v)
	}
	rows.Close()

	for _, p := range current {
		if !overlaps(p.vf, p.vt, f.ValidFrom, f.ValidTo) {
			continue
		}
		if _, err := tx.Exec(`UPDATE facts SET tx_to = ? WHERE id = ?`, ts(now), p.id); err != nil {
			return f, fmt.Errorf("put fact: close version: %w", err)
		}
		// what the old version said outside the new validity still holds
		if p.vf.IsZero() || p.vf.Before(f.ValidFrom) {
			if err := copyVersion(tx, p.id, p.vf, f.ValidFrom, now); err != nil {
				return f, err
			}
		}
		if !f.ValidTo.IsZero() && (p.vt.IsZero() || p.vt.After(f.ValidTo)) {
			if err := copyVersion(tx, p.id, f.ValidTo, p.vt, now); err != nil {
				return f, err
			}
		}
	}

	res, err := tx.Exec(`
INSERT INTO facts (entity, predicate, object, tags, agent, trace_id, ttl, source_file, source_line,
                   created_at, updated_at, deleted_at, valid_from, valid_to, tx_from, tx_to)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, NULL)`,
		f.Entity, f.Predicate, f.Object, marshalTags(f.Tags), f.Agent, f.TraceID, f.TTL,
		f.SourceFile, f.SourceLine,
		ts(f.Created), ts(f.Updated), ts(f.ValidFrom), nullTS(f.ValidTo), ts(now),
	)
	if err != nil {
		return f, fmt.Errorf("put fact: %w", err)
	}
	f.ID, _ = res.LastInsertId()
	for _, tag := range f.Tags {
		if _, err := tx.Exec(`INSERT INTO fact_tags (fact_id, tag) VALUES (?, ?)`, f.ID, tag); err != nil {
			return f, fmt.Errorf("insert fact_tag: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return f, fmt.Errorf("put fact: %w", err)
	}
	return f, nil
}

// copyVersion believes again, from now on, the part [vf, vt) of a version
// that a newer one cut: same content, tags and embedding.
func copyVersion(tx *sql.Tx, id int64, vf, vt, now time.Time) error {
	res, err := tx.Exec(`
INSERT INTO facts (entity, predicate, object, tags, agent, trace_id, ttl, source_file, source_line,
                   created_at, updated_at, deleted_at, access_count, last_accessed, embedding,
                   valid_from, valid_to, tx_from, tx_to)
SELECT entity, predicate, object, tags, agent, trace_id, ttl, source_file, source_line,
       created_at, updated_at, NULL, access_count, last_accessed, embedding,
       ?, ?, ?, NULL
FROM facts WHERE id = ?`, nullTS(vf), nullTS(vt), ts(now), id)
	if err != nil {
		return fmt.Errorf("copy version: %w", err)
	}
	newID, _ := res.LastInsertId()
	if _, err := tx.Exec(`INSERT INTO fact_tags (fact_id, tag) SELECT ?, tag FROM fact_tags WHERE fact_id = ?`, newID, id); err != nil {
		return fmt.Errorf("copy version tags: %w", err)
	}
	return nil
}

// overlaps reports whether [af, at) and [bf, bt) intersect; a zero bound is
// open (no start, or no end).
func overlaps(af, at, bf, bt time.Time) bool {
	startsBeforeBEnds := at.IsZero() || bf.IsZero() || bf.Before(at)
	bStartsBeforeAEnds := bt.IsZero() || af.IsZero() || af.Before(bt)
	return startsBeforeBEnds && bStartsBeforeAEnds
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func nullTS(t time.Time) any {
	if t.IsZero() {
		return nil
	}
	return ts(t)
}

func parseTS(v sql.NullString) time.Time {
	if !v.Valid || v.String == "" {
		return time.Time{}
	}
	t, _ := time.Parse(time.RFC3339, v.String)
	return t
}

// liveWhereClause builds the WHERE fragment of the usual read: versions
// mem7 believes now (not superseded, not deleted), that hold now, not
// TTL-expired. Times are RFC3339 UTC text, so they compare as strings.
const liveWhereClause = `
  deleted_at IS NULL AND tx_to IS NULL
  AND (valid_from IS NULL OR valid_from <= strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
  AND (valid_to IS NULL OR valid_to > strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))
  AND (ttl = 0 OR strftime('%s', updated_at) + ttl > strftime('%s', 'now'))
`

// liveWhere is liveWhereClause for a moment other than now: as_of picks what
// mem7 believed then, valid_at what held then. The moments are formatted by
// mem7, never taken from the caller as text.
func liveWhere(t temporal) string {
	if t.zero() {
		return liveWhereClause
	}
	var sb strings.Builder
	if t.AsOf.IsZero() {
		sb.WriteString("deleted_at IS NULL AND tx_to IS NULL")
	} else {
		at := ts(t.AsOf)
		fmt.Fprintf(&sb, "tx_from <= '%s' AND (tx_to IS NULL OR tx_to > '%s')", at, at)
	}
	valid := "strftime('%Y-%m-%dT%H:%M:%SZ', 'now')"
	if !t.ValidAt.IsZero() {
		valid = "'" + ts(t.ValidAt) + "'"
	}
	sb.WriteString(" AND (valid_from IS NULL OR valid_from <= " + valid + ")")
	sb.WriteString(" AND (valid_to IS NULL OR valid_to > " + valid + ")")
	return sb.String()
}

func (s *sqliteStore) Query(f filter) ([]fact, error) {
	return s.selectFacts(f, true)
}

func (s *sqliteStore) List(f filter) ([]fact, error) {
	return s.selectFacts(f, false)
}

// selectFacts runs a filtered SELECT. withObject=false swaps the body
// column for an empty string so callers don't have to pay the cost of
// transferring the content when they only want metadata.
func (s *sqliteStore) selectFacts(f filter, withObject bool) ([]fact, error) {
	objCol := "''"
	if withObject {
		objCol = "object"
	}
	var sb strings.Builder
	sb.WriteString("SELECT id, entity, predicate, ")
	sb.WriteString(objCol)
	sb.WriteString(`, tags, agent, trace_id, ttl, source_file, source_line, created_at, updated_at,
  valid_from, valid_to, tx_from, tx_to FROM facts WHERE `)
	sb.WriteString(liveWhere(f.When))

	args := []any{}
	if f.Entity != "" {
		sb.WriteString(" AND entity = ?")
		args = append(args, f.Entity)
	}
	if f.Agent != "" {
		sb.WriteString(" AND agent = ?")
		args = append(args, f.Agent)
	}
	for _, t := range f.Tags {
		sb.WriteString(" AND EXISTS (SELECT 1 FROM fact_tags ft WHERE ft.fact_id = facts.id AND ft.tag = ?)")
		args = append(args, t)
	}
	sb.WriteString(" ORDER BY updated_at DESC")
	if f.Limit > 0 {
		sb.WriteString(" LIMIT ?")
		args = append(args, f.Limit)
	}

	rows, err := s.db.Query(sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("select facts: %w", err)
	}
	defer rows.Close()

	var out []fact
	for rows.Next() {
		var fct fact
		var tagsRaw, createdStr, updatedStr string
		var vf, vt, tf, tt sql.NullString
		if err := rows.Scan(&fct.ID, &fct.Entity, &fct.Predicate, &fct.Object,
			&tagsRaw, &fct.Agent, &fct.TraceID, &fct.TTL, &fct.SourceFile, &fct.SourceLine,
			&createdStr, &updatedStr, &vf, &vt, &tf, &tt); err != nil {
			return nil, err
		}
		fct.ValidFrom, fct.ValidTo, fct.TxFrom, fct.TxTo = parseTS(vf), parseTS(vt), parseTS(tf), parseTS(tt)
		fct.Tags = unmarshalTags(tagsRaw)
		fct.Created, _ = time.Parse(time.RFC3339, createdStr)
		fct.Updated, _ = time.Parse(time.RFC3339, updatedStr)
		out = append(out, fct)
	}
	return out, rows.Err()
}

// DeleteByEntity stops believing every current version of this entity, at
// the given time: a read as of an earlier moment still sees them.
func (s *sqliteStore) DeleteByEntity(entity string, at time.Time) (int, error) {
	t := ts(at)
	res, err := s.db.Exec(`UPDATE facts SET deleted_at = ?, tx_to = ? WHERE entity = ? AND deleted_at IS NULL AND tx_to IS NULL`,
		t, t, entity)
	if err != nil {
		return 0, fmt.Errorf("delete by entity: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// DeleteByTags stops believing every current version whose tag set
// contains all of the supplied tags.
func (s *sqliteStore) DeleteByTags(tags []string, at time.Time) (int, error) {
	if len(tags) == 0 {
		return 0, nil
	}
	t := ts(at)
	var sb strings.Builder
	sb.WriteString("UPDATE facts SET deleted_at = ?, tx_to = ? WHERE deleted_at IS NULL AND tx_to IS NULL")
	args := []any{t, t}
	for _, tag := range tags {
		sb.WriteString(" AND EXISTS (SELECT 1 FROM fact_tags ft WHERE ft.fact_id = facts.id AND ft.tag = ?)")
		args = append(args, tag)
	}
	res, err := s.db.Exec(sb.String(), args...)
	if err != nil {
		return 0, fmt.Errorf("delete by tags: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// PurgeExpired physically deletes rows whose TTL has elapsed. The
// FTS5 contentless mirror is kept in sync via the AFTER DELETE trigger
// declared in the schema. Soft-deleted rows are not touched here.
func (s *sqliteStore) PurgeExpired() (int, error) {
	res, err := s.db.Exec(`DELETE FROM facts
WHERE ttl > 0
  AND strftime('%s', updated_at) + ttl <= strftime('%s', 'now')`)
	if err != nil {
		return 0, fmt.Errorf("purge expired: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// Search runs FTS5 MATCH with BM25 ranking. It joins the FTS virtual
// table back onto facts to honour liveness filters, TTL, and the
// usual tag/agent post-filters. The query string is passed through
// to FTS5 verbatim, so callers can use prefix "foo*" or boolean
// "foo AND bar" operators directly.
func (s *sqliteStore) Search(q searchQuery) ([]fact, error) {
	if strings.TrimSpace(q.Query) == "" {
		return nil, fmt.Errorf("search query is empty")
	}

	var sb strings.Builder
	sb.WriteString(`
SELECT f.id, f.entity, f.predicate, f.object, f.tags, f.agent, f.trace_id, f.ttl,
       f.source_file, f.source_line, f.created_at, f.updated_at,
       f.valid_from, f.valid_to, f.tx_from, f.tx_to
FROM facts f
JOIN facts_fts fts ON fts.rowid = f.id
WHERE facts_fts MATCH ?
  AND `)
	sb.WriteString(liveWhere(q.When))

	effective := q.Query
	if q.Mode == "natural" {
		effective = naturalizeFTSQuery(effective)
	}
	args := []any{sanitizeFTSQuery(effective)}
	if q.Agent != "" {
		sb.WriteString(" AND f.agent = ?")
		args = append(args, q.Agent)
	}
	for _, t := range q.Tags {
		sb.WriteString(" AND EXISTS (SELECT 1 FROM fact_tags ft WHERE ft.fact_id = f.id AND ft.tag = ?)")
		args = append(args, t)
	}
	if !q.Since.IsZero() {
		sb.WriteString(" AND f.updated_at >= ?")
		args = append(args, q.Since.UTC().Format(time.RFC3339))
	}
	if !q.Until.IsZero() {
		sb.WriteString(" AND f.updated_at <= ?")
		args = append(args, q.Until.UTC().Format(time.RFC3339))
	}
	sb.WriteString(` ORDER BY bm25(facts_fts, 2.0, 0.0, 5.0, 0.5) + (-1.0 / (1.0 + (strftime('%s','now') - strftime('%s', f.updated_at)) / 86400.0))`)
	if q.Limit > 0 {
		sb.WriteString(" LIMIT ?")
		args = append(args, q.Limit)
	}

	rows, err := s.db.Query(sb.String(), args...)
	if err != nil {
		return nil, fmt.Errorf("search: %w", err)
	}
	out, err := scanFacts(rows)
	rows.Close()
	if err != nil {
		return nil, err
	}

	if q.IncludeNeighbors {
		radius := q.NeighborRadius
		if radius <= 0 {
			radius = 1
		}
		out, err = s.expandWithNeighbors(out, radius)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// sanitizeFTSQuery makes a user query safe to pass to FTS5 MATCH while
// preserving power-user operators. Bare tokens that look like identifiers
// ("foo", "foo*") and reserved operators (AND, OR, NOT, NEAR, parens) are
// passed through unchanged. Anything else — tokens with hyphens, accents,
// punctuation — gets wrapped in double quotes so FTS5 treats it as a
// literal phrase instead of parsing it as NOT/column/operator syntax.
// Already-quoted phrases are preserved verbatim.
func sanitizeFTSQuery(q string) string {
	var out strings.Builder
	runes := []rune(q)
	i := 0
	for i < len(runes) {
		r := runes[i]
		switch {
		case r == ' ' || r == '\t' || r == '\n':
			out.WriteRune(r)
			i++
		case r == '(' || r == ')':
			out.WriteRune(r)
			i++
		case r == '"':
			j := i + 1
			for j < len(runes) && runes[j] != '"' {
				j++
			}
			if j < len(runes) {
				j++
			}
			out.WriteString(string(runes[i:j]))
			i = j
		default:
			j := i
			for j < len(runes) {
				c := runes[j]
				if c == ' ' || c == '\t' || c == '\n' || c == '(' || c == ')' || c == '"' {
					break
				}
				j++
			}
			tok := string(runes[i:j])
			out.WriteString(quoteFTSToken(tok))
			i = j
		}
	}
	return out.String()
}

// quoteFTSToken returns tok unchanged if it is a reserved FTS5 operator or
// a bare identifier (optionally with trailing *); otherwise it wraps tok in
// double quotes, escaping embedded quotes per FTS5 rules ("" ).
func quoteFTSToken(tok string) string {
	if tok == "" {
		return tok
	}
	switch tok {
	case "AND", "OR", "NOT", "NEAR":
		return tok
	}
	bare := true
	for idx, r := range tok {
		if r >= '0' && r <= '9' {
			continue
		}
		if r >= 'a' && r <= 'z' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			continue
		}
		if r == '_' {
			continue
		}
		if r == '*' && idx == len(tok)-1 {
			continue
		}
		bare = false
		break
	}
	if bare {
		return tok
	}
	return `"` + strings.ReplaceAll(tok, `"`, `""`) + `"`
}

func (s *sqliteStore) TouchAccessed(ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids)+1)
	args[0] = time.Now().UTC().Format(time.RFC3339)
	for i, id := range ids {
		placeholders[i] = "?"
		args[i+1] = id
	}
	q := fmt.Sprintf(
		"UPDATE facts SET access_count = access_count + 1, last_accessed = ? WHERE id IN (%s)",
		strings.Join(placeholders, ","),
	)
	_, err := s.db.Exec(q, args...)
	return err
}

func (s *sqliteStore) StoreEmbedding(id int64, vec []float32) error {
	_, err := s.db.Exec("UPDATE facts SET embedding = ? WHERE id = ?", float32ToBytes(vec), id)
	return err
}

func (s *sqliteStore) LoadEmbeddings(when temporal) (map[int64][]float32, error) {
	rows, err := s.db.Query("SELECT id, embedding FROM facts WHERE " + liveWhere(when) + " AND embedding IS NOT NULL")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make(map[int64][]float32)
	for rows.Next() {
		var id int64
		var blob []byte
		if err := rows.Scan(&id, &blob); err != nil {
			return nil, err
		}
		if len(blob) > 0 {
			result[id] = bytesToFloat32(blob)
		}
	}
	return result, rows.Err()
}

func (s *sqliteStore) FetchByIDs(ids []int64, when temporal) ([]fact, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	q := "SELECT id, entity, predicate, object, tags, agent, trace_id, ttl, source_file, source_line, created_at, updated_at, valid_from, valid_to, tx_from, tx_to FROM facts WHERE id IN (" +
		strings.Join(placeholders, ",") + ") AND " + liveWhere(when)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFacts(rows)
}

func (s *sqliteStore) Count() (int, error) {
	row := s.db.QueryRow(`SELECT COUNT(*) FROM facts WHERE ` + liveWhereClause)
	var n int
	if err := row.Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// snapshotEmbeddings returns the stored embeddings keyed by entity and
// content, so a rebuild of the index can give them back.
func (s *sqliteStore) snapshotEmbeddings() (map[[2]string][]byte, error) {
	rows, err := s.db.Query(`SELECT entity, object, embedding FROM facts WHERE embedding IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[[2]string][]byte{}
	for rows.Next() {
		var entity, object string
		var blob []byte
		if err := rows.Scan(&entity, &object, &blob); err != nil {
			return nil, err
		}
		out[[2]string{entity, object}] = blob
	}
	return out, rows.Err()
}

// restoreEmbeddings puts snapshot embeddings back on the rows that embed
// the same content.
func (s *sqliteStore) restoreEmbeddings(kept map[[2]string][]byte) error {
	for k, blob := range kept {
		if _, err := s.db.Exec(`UPDATE facts SET embedding = ? WHERE entity = ? AND object = ? AND embedding IS NULL`, blob, k[0], k[1]); err != nil {
			return err
		}
	}
	return nil
}
