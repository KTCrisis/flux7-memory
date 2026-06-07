package memory

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(t.TempDir(), 10000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// First run on a fresh machine: the data dir does not exist yet and
// NewStore must create it rather than let SQLite fail with CANTOPEN.
func TestNewStoreCreatesMissingDataDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", ".mem7")
	s, err := NewStore(dir, 10000)
	if err != nil {
		t.Fatalf("NewStore on missing dir: %v", err)
	}
	defer s.Close()
	if res := s.ToolStore(map[string]any{"key": "k", "value": "v"}); res["isError"] == true {
		t.Fatalf("store after dir creation: %v", res)
	}
}

func call(t *testing.T, s *Store, name string, args map[string]any) Result {
	t.Helper()
	switch name {
	case "memory_store":
		return s.ToolStore(args)
	case "memory_recall":
		return s.ToolRecall(args)
	case "memory_list":
		return s.ToolList(args)
	case "memory_forget":
		return s.ToolForget(args)
	case "memory_search":
		return s.ToolSearch(args)
	}
	t.Fatalf("unknown tool: %s", name)
	return nil
}

func getText(t *testing.T, r Result) string {
	t.Helper()
	content := r["content"].([]map[string]any)
	return content[0]["text"].(string)
}

func assertText(t *testing.T, r Result, substr string) {
	t.Helper()
	text := getText(t, r)
	if !strings.Contains(text, substr) {
		t.Fatalf("expected %q in text, got: %s", substr, text)
	}
}

func TestStoreAndRecall(t *testing.T) {
	s := newStore(t)
	res := call(t, s, "memory_store", map[string]any{
		"key":   "test_key",
		"value": "test_value",
		"tags":  []any{"tag1", "tag2"},
		"agent": "test-agent",
	})
	assertText(t, res, "created")

	res = call(t, s, "memory_recall", map[string]any{"key": "test_key"})
	text := getText(t, res)
	if !strings.Contains(text, "test_value") {
		t.Fatalf("expected value in recall, got: %s", text)
	}
	if !strings.Contains(text, "tag1") {
		t.Fatalf("expected tags in recall, got: %s", text)
	}
}

func TestStoreUpsert(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "k1", "value": "v1"})
	call(t, s, "memory_store", map[string]any{"key": "k1", "value": "v2"})

	res := call(t, s, "memory_recall", map[string]any{"key": "k1"})
	text := getText(t, res)
	if !strings.Contains(text, "v2") {
		t.Fatalf("expected updated value v2, got: %s", text)
	}
	if strings.Contains(text, "v1") {
		t.Fatalf("old value v1 should not leak into latest recall, got: %s", text)
	}
}

func TestStoreMissingParams(t *testing.T) {
	s := newStore(t)
	res := call(t, s, "memory_store", map[string]any{"key": "k1"})
	if !res["isError"].(bool) {
		t.Fatal("expected error for missing value")
	}
}

func TestRecallByTags(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "a", "value": "va", "tags": []any{"x", "y"}})
	call(t, s, "memory_store", map[string]any{"key": "b", "value": "vb", "tags": []any{"x"}})
	call(t, s, "memory_store", map[string]any{"key": "c", "value": "vc", "tags": []any{"y"}})

	res := call(t, s, "memory_recall", map[string]any{"tags": []any{"x", "y"}})
	text := getText(t, res)
	if !strings.Contains(text, "va") {
		t.Fatalf("expected va, got: %s", text)
	}
	if strings.Contains(text, "vb") || strings.Contains(text, "vc") {
		t.Fatalf("should only match a, got: %s", text)
	}
}

func TestRecallByAgent(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "a", "value": "va", "agent": "llama3"})
	call(t, s, "memory_store", map[string]any{"key": "b", "value": "vb", "agent": "claude"})

	res := call(t, s, "memory_recall", map[string]any{"agent": "llama3"})
	text := getText(t, res)
	if !strings.Contains(text, "va") {
		t.Fatalf("expected va, got: %s", text)
	}
	if strings.Contains(text, "vb") {
		t.Fatalf("should not contain claude's memory, got: %s", text)
	}
}

func TestRecallLimit(t *testing.T) {
	s := newStore(t)
	for i := 0; i < 20; i++ {
		call(t, s, "memory_store", map[string]any{
			"key":   fmt.Sprintf("k%d", i),
			"value": fmt.Sprintf("v%d", i),
		})
	}

	res := call(t, s, "memory_recall", map[string]any{"limit": float64(3)})
	text := getText(t, res)
	count := strings.Count(text, "## ")
	if count != 3 {
		t.Fatalf("expected 3 results, got %d", count)
	}
}

func TestRecallEmpty(t *testing.T) {
	s := newStore(t)
	res := call(t, s, "memory_recall", map[string]any{"key": "nonexistent"})
	assertText(t, res, "No memories")
}

func TestList(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "a", "value": "va", "tags": []any{"t1"}})
	call(t, s, "memory_store", map[string]any{"key": "b", "value": "vb", "tags": []any{"t2"}})

	res := call(t, s, "memory_list", map[string]any{})
	text := getText(t, res)
	if !strings.Contains(text, "2 memories") {
		t.Fatalf("expected 2 memories, got: %s", text)
	}
	if strings.Contains(text, "va") || strings.Contains(text, "vb") {
		t.Fatalf("list should not contain values, got: %s", text)
	}
}

func TestListFilterByTags(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "a", "value": "va", "tags": []any{"staffd"}})
	call(t, s, "memory_store", map[string]any{"key": "b", "value": "vb", "tags": []any{"event7"}})

	res := call(t, s, "memory_list", map[string]any{"tags": []any{"staffd"}})
	text := getText(t, res)
	if !strings.Contains(text, "1 memories") {
		t.Fatalf("expected 1 memory, got: %s", text)
	}
}

func TestForgetByKey(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "a", "value": "va"})
	call(t, s, "memory_store", map[string]any{"key": "b", "value": "vb"})

	res := call(t, s, "memory_forget", map[string]any{"key": "a"})
	assertText(t, res, "Removed 1")

	res = call(t, s, "memory_recall", map[string]any{"key": "a"})
	assertText(t, res, "No memories")

	res = call(t, s, "memory_recall", map[string]any{"key": "b"})
	assertText(t, res, "vb")
}

func TestForgetByTags(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "a", "value": "va", "tags": []any{"temp"}})
	call(t, s, "memory_store", map[string]any{"key": "b", "value": "vb", "tags": []any{"temp"}})
	call(t, s, "memory_store", map[string]any{"key": "c", "value": "vc", "tags": []any{"keep"}})

	res := call(t, s, "memory_forget", map[string]any{"tags": []any{"temp"}})
	assertText(t, res, "Removed 2")

	res = call(t, s, "memory_list", map[string]any{})
	assertText(t, res, "1 memories")
}

func TestSearchFTS(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{
		"key": "go_interfaces", "value": "Go interfaces are satisfied implicitly", "tags": []any{"go"},
	})
	call(t, s, "memory_store", map[string]any{
		"key": "python_classes", "value": "Python classes use explicit inheritance", "tags": []any{"python"},
	})
	call(t, s, "memory_store", map[string]any{
		"key": "go_channels", "value": "Channels are typed conduits for goroutines", "tags": []any{"go"},
	})

	// Basic search — "interfaces" should hit go_interfaces only.
	res := s.ToolSearch(map[string]any{"query": "interfaces"})
	text := getText(t, res)
	if !strings.Contains(text, "go_interfaces") {
		t.Fatalf("expected go_interfaces, got: %s", text)
	}
	if strings.Contains(text, "python_classes") || strings.Contains(text, "go_channels") {
		t.Fatalf("unexpected hits: %s", text)
	}

	// Multi-token search — "goroutines channels" should hit go_channels.
	res = s.ToolSearch(map[string]any{"query": "goroutines channels"})
	assertText(t, res, "go_channels")

	// Tag post-filter — search "typed" with tag python yields nothing.
	res = s.ToolSearch(map[string]any{"query": "typed", "tags": []any{"python"}})
	assertText(t, res, "No memories")

	// Missing query returns error envelope.
	res = s.ToolSearch(map[string]any{})
	if !res["isError"].(bool) {
		t.Fatal("expected error for missing query")
	}
}

func TestSearchHandlesSpecialChars(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{
		"key": "hyphen_doc", "value": "Test entry mentioning claude-code and mem7-check integration",
	})
	call(t, s, "memory_store", map[string]any{
		"key": "accents_doc", "value": "Entrée avec des caractères accentués",
	})

	// Hyphen used to crash FTS5 with "no such column" — must now match.
	res := s.ToolSearch(map[string]any{"query": "claude-code"})
	assertText(t, res, "hyphen_doc")

	// Unicode punctuation must not break the parser either.
	res = s.ToolSearch(map[string]any{"query": "accentués"})
	assertText(t, res, "accents_doc")

	// Power-user operators still pass through.
	res = s.ToolSearch(map[string]any{"query": "claude-code AND mem7-check"})
	assertText(t, res, "hyphen_doc")
}

func TestGetFileAndRange(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "k1", "value": "first content"})
	call(t, s, "memory_store", map[string]any{"key": "k2", "value": "second content"})

	// Pick the daily file that was created. Use list to discover it.
	files, err := listDailyFiles(s.dir)
	if err != nil || len(files) == 0 {
		t.Fatalf("expected at least one daily file, got %v %v", files, err)
	}
	// Path argument is relative to workspace/
	rel := strings.TrimPrefix(files[0], filepath.Join(s.dir, "workspace")+string(filepath.Separator))

	res := s.ToolGet(map[string]any{"path": rel})
	text := getText(t, res)
	if !strings.Contains(text, "first content") || !strings.Contains(text, "second content") {
		t.Fatalf("expected both entries in full read, got: %s", text)
	}

	// Range read — just the first few lines.
	res = s.ToolGet(map[string]any{"path": rel, "from_line": float64(1), "to_line": float64(3)})
	text = getText(t, res)
	if !strings.Contains(text, "## k1") {
		t.Fatalf("expected k1 heading in range, got: %s", text)
	}
	if strings.Contains(text, "second content") {
		t.Fatalf("range should not include second entry, got: %s", text)
	}
}

func TestGetRefusesTraversal(t *testing.T) {
	s := newStore(t)
	res := s.ToolGet(map[string]any{"path": "../../../etc/passwd"})
	if !res["isError"].(bool) {
		t.Fatal("expected error for path traversal")
	}
	assertText(t, res, "escapes workspace")
}

func TestGetMissingPath(t *testing.T) {
	s := newStore(t)
	res := s.ToolGet(map[string]any{})
	if !res["isError"].(bool) {
		t.Fatal("expected error for missing path")
	}
}

func TestPruneRemovesExpiredAndRescanSkipsThem(t *testing.T) {
	s := newStore(t)
	// Permanent entry via the public API.
	call(t, s, "memory_store", map[string]any{
		"key": "permanent", "value": "stays forever",
	})
	// Ephemeral entry written through the markdown writer + index
	// directly so we can backdate Updated and make the TTL deterministic
	// without sleeping. The markdown trace is needed too, otherwise
	// Rescan would have nothing to skip.
	past := time.Now().UTC().Add(-1 * time.Hour)
	expired := fact{
		Entity:    "ephemeral",
		Predicate: defaultPredicate,
		Object:    "expires fast",
		Agent:     "test",
		TTL:       60,
		Created:   past,
		Updated:   past,
	}
	path, line, err := s.md.AppendStore(expired)
	if err != nil {
		t.Fatalf("append markdown: %v", err)
	}
	expired.SourceFile = path
	expired.SourceLine = line
	if _, err := s.index.Put(expired); err != nil {
		t.Fatalf("index put: %v", err)
	}

	// The live filter already hides ephemeral, but the row is still
	// physically in the index until prune runs.
	res := call(t, s, "memory_list", map[string]any{})
	assertText(t, res, "1 memories")

	n, err := s.Prune()
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 row pruned, got %d", n)
	}

	// FTS5 must also forget the pruned content (cascaded via the
	// AFTER DELETE trigger): searching for the expired body returns
	// nothing.
	res = call(t, s, "memory_search", map[string]any{"query": "expires"})
	assertText(t, res, "No memories")

	// Rescan must not bring the expired entry back : the markdown
	// still has the original store entry, but Rescan re-evaluates TTL.
	if _, err := s.Rescan(); err != nil {
		t.Fatalf("rescan: %v", err)
	}
	res = call(t, s, "memory_list", map[string]any{})
	assertText(t, res, "1 memories")
	res = call(t, s, "memory_recall", map[string]any{"key": "ephemeral"})
	assertText(t, res, "No memories")
}

func TestAccessTrackingIncrementsOnRecall(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "tracked", "value": "some content"})

	call(t, s, "memory_recall", map[string]any{"key": "tracked"})
	call(t, s, "memory_recall", map[string]any{"key": "tracked"})
	call(t, s, "memory_recall", map[string]any{"key": "tracked"})

	results, err := s.index.Query(filter{Entity: "tracked", Limit: 1})
	if err != nil || len(results) == 0 {
		t.Fatal("expected tracked entry")
	}

	var count int
	row := s.index.(*sqliteStore).db.QueryRow("SELECT access_count FROM facts WHERE id = ?", results[0].ID)
	if err := row.Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 3 {
		t.Fatalf("expected access_count=3, got %d", count)
	}
}

func TestHybridSearchMergesResults(t *testing.T) {
	s := newStore(t)

	// Insert entries: one matches BM25 well, another has similar embedding.
	call(t, s, "memory_store", map[string]any{
		"key": "bm25_hit", "value": "kubernetes cluster deployment procedure",
	})
	call(t, s, "memory_store", map[string]any{
		"key": "semantic_hit", "value": "container orchestration setup steps",
	})
	call(t, s, "memory_store", map[string]any{
		"key": "noise", "value": "unrelated topic about cooking recipes",
	})

	// Manually store embeddings to simulate. Query vec is close to semantic_hit.
	bm25ID := int64(1)
	semID := int64(2)
	noiseID := int64(3)

	results, _ := s.index.Query(filter{Limit: 10})
	for _, f := range results {
		switch f.Entity {
		case "bm25_hit":
			bm25ID = f.ID
		case "semantic_hit":
			semID = f.ID
		case "noise":
			noiseID = f.ID
		}
	}

	// Embeddings: semantic_hit is close to query, bm25_hit is moderate, noise is far.
	queryVec := []float32{1.0, 0.0, 0.0}
	_ = s.index.StoreEmbedding(bm25ID, []float32{0.5, 0.5, 0.0})
	_ = s.index.StoreEmbedding(semID, []float32{0.9, 0.1, 0.0})
	_ = s.index.StoreEmbedding(noiseID, []float32{0.0, 0.0, 1.0})

	// Load cache
	s.embCache, _ = s.index.LoadEmbeddings()

	// Run cosine search directly
	cosine := cosineSearch(queryVec, s.embCache, 10)
	if len(cosine) < 2 {
		t.Fatalf("expected at least 2 cosine results, got %d", len(cosine))
	}
	if cosine[0].ID != semID {
		t.Fatalf("expected semantic_hit first in cosine, got id=%d", cosine[0].ID)
	}
}

func TestRRFMerge(t *testing.T) {
	bm25 := []fact{
		{ID: 1, Entity: "a"},
		{ID: 2, Entity: "b"},
		{ID: 3, Entity: "c"},
	}
	cosine := []scoredID{
		{ID: 4, Score: 0.95},
		{ID: 2, Score: 0.90},
		{ID: 1, Score: 0.80},
	}
	factMap := map[int64]fact{
		1: {ID: 1, Entity: "a"},
		2: {ID: 2, Entity: "b"},
		3: {ID: 3, Entity: "c"},
		4: {ID: 4, Entity: "d"},
	}

	merged := mergeRRF(bm25, cosine, factMap, 3)
	if len(merged) != 3 {
		t.Fatalf("expected 3 results, got %d", len(merged))
	}

	// ID 2 appears in both lists (rank 1 in BM25, rank 1 in cosine) → highest RRF
	// ID 1 appears in both (rank 0 in BM25, rank 2 in cosine) → second highest
	if merged[0].ID != 1 && merged[0].ID != 2 {
		t.Fatalf("expected id 1 or 2 first, got %d", merged[0].ID)
	}
	// All IDs should be in {1,2,3,4}
	for _, f := range merged {
		if f.ID < 1 || f.ID > 4 {
			t.Fatalf("unexpected id %d", f.ID)
		}
	}
}

func TestSearchRecencyBoost(t *testing.T) {
	s := newStore(t)

	// Insert two entries with identical BM25-relevant content but
	// different ages. We bypass ToolStore to control timestamps.
	old := fact{
		Entity:    "deploy_old",
		Predicate: "note",
		Object:    "deployment procedure for staging environment",
		Tags:      []string{"ops"},
		Created:   time.Now().Add(-60 * 24 * time.Hour), // 60 days ago
		Updated:   time.Now().Add(-60 * 24 * time.Hour),
	}
	recent := fact{
		Entity:    "deploy_recent",
		Predicate: "note",
		Object:    "deployment procedure for staging environment",
		Tags:      []string{"ops"},
		Created:   time.Now().Add(-1 * time.Hour), // 1 hour ago
		Updated:   time.Now().Add(-1 * time.Hour),
	}

	// Write markdown + index for both, old first so BM25 alone would
	// not favour either (identical content).
	for _, f := range []fact{old, recent} {
		path, line, err := s.md.AppendStore(f)
		if err != nil {
			t.Fatal(err)
		}
		f.SourceFile = path
		f.SourceLine = line
		if _, err := s.index.Put(f); err != nil {
			t.Fatal(err)
		}
	}

	res := s.ToolSearch(map[string]any{"query": "deployment staging"})
	text := getText(t, res)

	// The recent entry must appear before the old one.
	idxRecent := strings.Index(text, "deploy_recent")
	idxOld := strings.Index(text, "deploy_old")
	if idxRecent < 0 || idxOld < 0 {
		t.Fatalf("expected both entries, got: %s", text)
	}
	if idxRecent > idxOld {
		t.Fatalf("recency boost failed: old entry ranked above recent one\n%s", text)
	}
}

func TestSearchIgnoresDeleted(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "alive", "value": "phoenix"})
	call(t, s, "memory_store", map[string]any{"key": "dead", "value": "phoenix"})
	call(t, s, "memory_forget", map[string]any{"key": "dead"})

	res := s.ToolSearch(map[string]any{"query": "phoenix"})
	text := getText(t, res)
	if !strings.Contains(text, "alive") {
		t.Fatalf("expected alive, got: %s", text)
	}
	if strings.Contains(text, "## dead") {
		t.Fatalf("deleted entry leaked: %s", text)
	}
}

func TestForgetMissingParams(t *testing.T) {
	s := newStore(t)
	res := call(t, s, "memory_forget", map[string]any{})
	if !res["isError"].(bool) {
		t.Fatal("expected error when no key or tags")
	}
}

func TestStoreBodyWithSeparator(t *testing.T) {
	s := newStore(t)
	body := "line one\n---\nline three"
	call(t, s, "memory_store", map[string]any{"key": "sep_test", "value": body})

	// Recall must return the full body including the --- line.
	res := call(t, s, "memory_recall", map[string]any{"key": "sep_test"})
	text := getText(t, res)
	if !strings.Contains(text, "line one") || !strings.Contains(text, "line three") {
		t.Fatalf("body truncated at separator: %s", text)
	}

	// Rescan must reconstruct the same entry intact.
	n, err := s.Rescan()
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 entry after rescan, got %d", n)
	}
	res = call(t, s, "memory_recall", map[string]any{"key": "sep_test"})
	text = getText(t, res)
	if !strings.Contains(text, "line one") || !strings.Contains(text, "line three") {
		t.Fatalf("body truncated after rescan: %s", text)
	}
}

func TestStoreBodyWithFencedBlock(t *testing.T) {
	s := newStore(t)
	body := "here is code:\n```mem7\nfake envelope\n```\nend"
	call(t, s, "memory_store", map[string]any{"key": "fence_test", "value": body})

	res := call(t, s, "memory_recall", map[string]any{"key": "fence_test"})
	text := getText(t, res)
	if !strings.Contains(text, "fake envelope") || !strings.Contains(text, "end") {
		t.Fatalf("body corrupted by fenced block: %s", text)
	}

	n, err := s.Rescan()
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 entry after rescan, got %d", n)
	}
	res = call(t, s, "memory_recall", map[string]any{"key": "fence_test"})
	text = getText(t, res)
	if !strings.Contains(text, "fake envelope") || !strings.Contains(text, "end") {
		t.Fatalf("body corrupted after rescan: %s", text)
	}
}

func TestStoreBodyWithMultiplePoisons(t *testing.T) {
	s := newStore(t)
	// Body with all three dangerous patterns at once.
	body := "---\n```mem7\nop: delete\n```\n---\nreal content"
	call(t, s, "memory_store", map[string]any{"key": "poison_test", "value": body})

	res := call(t, s, "memory_recall", map[string]any{"key": "poison_test"})
	text := getText(t, res)
	if !strings.Contains(text, "real content") {
		t.Fatalf("body lost after poison patterns: %s", text)
	}

	n, err := s.Rescan()
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 entry after rescan, got %d", n)
	}
}

// TestRescanRebuildsFromMarkdown verifies that dropping the SQLite
// index and calling Rescan reconstructs an identical live state from
// the markdown workspace alone.
func TestRescanRebuildsFromMarkdown(t *testing.T) {
	s := newStore(t)
	call(t, s, "memory_store", map[string]any{"key": "a", "value": "va", "tags": []any{"x"}})
	call(t, s, "memory_store", map[string]any{"key": "b", "value": "vb", "agent": "claude"})
	call(t, s, "memory_store", map[string]any{"key": "a", "value": "va2"}) // upsert
	call(t, s, "memory_forget", map[string]any{"key": "b"})

	beforeList := getText(t, call(t, s, "memory_list", map[string]any{}))

	n, err := s.Rescan()
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 live entry after rescan, got %d", n)
	}

	afterList := getText(t, call(t, s, "memory_list", map[string]any{}))
	if beforeList != afterList {
		t.Fatalf("list mismatch after rescan:\nbefore: %s\nafter:  %s", beforeList, afterList)
	}

	res := call(t, s, "memory_recall", map[string]any{"key": "a"})
	assertText(t, res, "va2")
}

