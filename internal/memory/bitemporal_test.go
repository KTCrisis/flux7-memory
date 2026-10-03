package memory

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func day(t *testing.T, s string) time.Time { return mustTime(t, s+"T09:00:00Z") }

// put writes a version at a given transaction time, the way a store or a
// rescan does.
func put(t *testing.T, s *Store, key, value string, tx, validFrom time.Time) {
	t.Helper()
	if _, err := s.index.Put(fact{Entity: key, Object: value, Created: tx, Updated: tx, ValidFrom: validFrom}); err != nil {
		t.Fatal(err)
	}
}

func valueAt(t *testing.T, s *Store, key string, when temporal) string {
	t.Helper()
	got, err := s.index.Query(filter{Entity: key, When: when})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		return "(none)"
	}
	if len(got) > 1 {
		t.Fatalf("%d versions hold at once for %s: %+v", len(got), key, got)
	}
	return got[0].Object
}

// event7 moved twice; each new fact ends the previous one, which stays true
// for its own period.
func TestValidTimeTracksAMove(t *testing.T) {
	s := newStore(t)
	put(t, s, "event7:host", "Railway", day(t, "2026-02-15"), time.Time{})
	put(t, s, "event7:host", "Cloudflare + Supabase", day(t, "2026-03-20"), time.Time{})
	put(t, s, "event7:host", "local only", day(t, "2026-04-11"), time.Time{})

	for _, c := range []struct{ at, want string }{
		{"2026-03-01", "Railway"},
		{"2026-04-01", "Cloudflare + Supabase"},
		{"2026-05-01", "local only"},
		{"2026-02-01", "(none)"},
	} {
		if got := valueAt(t, s, "event7:host", temporal{ValidAt: day(t, c.at)}); got != c.want {
			t.Errorf("valid at %s: %q, want %q", c.at, got, c.want)
		}
	}
	if got := valueAt(t, s, "event7:host", temporal{}); got != "local only" {
		t.Errorf("now: %q", got)
	}
}

// A correction of the past replaces the belief about that past, and the old
// belief stays readable as of before the correction.
func TestTransactionTimeKeepsWhatWasBelieved(t *testing.T) {
	s := newStore(t)
	put(t, s, "event7:host", "Cloudflare", day(t, "2026-03-20"), time.Time{})
	// on the 11th: "local only since today"
	put(t, s, "event7:host", "local only", day(t, "2026-04-11"), time.Time{})
	// on the 12th: "in fact it was since the 5th"
	put(t, s, "event7:host", "local only", day(t, "2026-04-12"), day(t, "2026-04-05"))

	apr8 := day(t, "2026-04-08")
	if got := valueAt(t, s, "event7:host", temporal{ValidAt: apr8}); got != "local only" {
		t.Errorf("now, about the 8th: %q (the correction holds)", got)
	}
	if got := valueAt(t, s, "event7:host", temporal{AsOf: day(t, "2026-04-11"), ValidAt: apr8}); got != "Cloudflare" {
		t.Errorf("as believed on the 11th, about the 8th: %q (before the correction)", got)
	}
	if got := valueAt(t, s, "event7:host", temporal{ValidAt: day(t, "2026-04-01")}); got != "Cloudflare" {
		t.Errorf("about the 1st: %q (the correction does not reach before the 5th)", got)
	}
}

// A bounded fact in the middle of an open one splits it in two.
func TestBoundedFactSplitsAnOpenOne(t *testing.T) {
	s := newStore(t)
	put(t, s, "on-call", "Marc", day(t, "2026-09-01"), time.Time{})
	if _, err := s.index.Put(fact{Entity: "on-call", Object: "Yohann", Created: day(t, "2026-09-02"), Updated: day(t, "2026-09-02"),
		ValidFrom: day(t, "2026-09-10"), ValidTo: day(t, "2026-09-17")}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ at, want string }{
		{"2026-09-05", "Marc"}, {"2026-09-12", "Yohann"}, {"2026-09-20", "Marc"},
	} {
		if got := valueAt(t, s, "on-call", temporal{ValidAt: day(t, c.at)}); got != c.want {
			t.Errorf("on %s: %q, want %q", c.at, got, c.want)
		}
	}
}

func TestForgetKeepsThePastReadable(t *testing.T) {
	s := newStore(t)
	put(t, s, "k", "v", day(t, "2026-05-01"), time.Time{})
	if _, err := s.index.DeleteByEntity("k", day(t, "2026-06-01")); err != nil {
		t.Fatal(err)
	}
	if got := valueAt(t, s, "k", temporal{}); got != "(none)" {
		t.Errorf("after forget: %q", got)
	}
	if got := valueAt(t, s, "k", temporal{AsOf: day(t, "2026-05-15"), ValidAt: day(t, "2026-05-15")}); got != "v" {
		t.Errorf("as of before the forget: %q", got)
	}
}

// Through the tools: valid_from in the past, valid_at and as_of on reads,
// validity in history, and the defaults unchanged.
func TestBitemporalTools(t *testing.T) {
	s := newStore(t)
	if r := s.ToolStore(map[string]any{"key": "event7:host", "value": "Railway", "valid_from": "2026-02-15"}); isErr(r) {
		t.Fatal(r)
	}
	if r := s.ToolStore(map[string]any{"key": "event7:host", "value": "Cloudflare", "valid_from": "2026-03-20"}); isErr(r) {
		t.Fatal(r)
	}
	if got := getText(t, s.ToolRecall(map[string]any{"key": "event7:host", "valid_at": "2026-03-01"})); !strings.Contains(got, "Railway") || !strings.Contains(got, "Valid: 2026-02-15T00:00:00Z → 2026-03-20T00:00:00Z") {
		t.Errorf("valid_at March 1st:\n%s", got)
	}
	if got := getText(t, s.ToolRecall(map[string]any{"key": "event7:host"})); !strings.Contains(got, "Cloudflare") || strings.Contains(got, "Railway") {
		t.Errorf("now:\n%s", got)
	}
	if got := getText(t, s.ToolHistory(map[string]any{"key": "event7:host"})); !strings.Contains(got, "valid 2026-03-20T00:00:00Z → now") {
		t.Errorf("history shows validity:\n%s", got)
	}
	if r := s.ToolStore(map[string]any{"key": "x", "value": "v", "valid_from": "2026-05-01", "valid_to": "2026-04-01"}); !isErr(r) {
		t.Error("valid_to before valid_from must be refused")
	}
	if r := s.ToolRecall(map[string]any{"key": "x", "as_of": "yesterday"}); !isErr(r) {
		t.Error("a moment that is not a date must be refused")
	}
	if r := s.ToolStore(map[string]any{"key": "plain", "value": "no dates"}); isErr(r) {
		t.Fatal(r)
	}
	if got := getText(t, s.ToolRecall(map[string]any{"key": "plain"})); strings.Contains(got, "Valid:") {
		t.Errorf("a fact without dates shows no validity line:\n%s", got)
	}
	// the chain still holds with the new fields
	if r, _ := s.VerifyChain(); r.Break != nil {
		t.Errorf("chain: %+v", r.Break)
	}
}

// An index made before versions (one row per key, unique index) is rebuilt
// from the markdown, which kept every version, when the store opens.
func TestOldIndexIsRebuiltIntoVersions(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	_ = s.ToolStore(map[string]any{"key": "a", "value": "first", "valid_from": "2026-02-01"})
	_ = s.ToolStore(map[string]any{"key": "a", "value": "second", "valid_from": "2026-03-01"})
	_ = s.Close()

	// make it look like the old layout: one row per key, unique index
	db, err := sql.Open("sqlite", filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM facts WHERE tx_to IS NOT NULL OR valid_to IS NOT NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX idx_facts_entity_predicate ON facts(entity, predicate)`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	s, err = NewStore(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if got := valueAt(t, s, "a", temporal{ValidAt: day(t, "2026-02-15")}); got != "first" {
		t.Errorf("the rebuilt index lost the earlier version: %q", got)
	}
	if got := valueAt(t, s, "a", temporal{}); got != "second" {
		t.Errorf("now: %q", got)
	}
}
