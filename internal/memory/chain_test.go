package memory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func storeOK(t *testing.T, s *Store, key, value string, c Caller) {
	t.Helper()
	if r := s.ToolStoreAs(map[string]any{"key": key, "value": value}, c); isErr(r) {
		t.Fatal(r)
	}
}

func dailyFile(t *testing.T, dir string) string {
	t.Helper()
	files, _ := listDailyFiles(dir)
	if len(files) != 1 {
		t.Fatalf("daily files: %v", files)
	}
	return files[0]
}

func TestChainHoldsAndSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	storeOK(t, s, "a", "first", Caller{})
	storeOK(t, s, "b", "second", Caller{})
	_ = s.Close()

	s, err = NewStore(dir, 1000) // the head is read back from the workspace
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	storeOK(t, s, "a", "first, updated", Caller{})
	if r := s.ToolForget(map[string]any{"key": "b"}); isErr(r) {
		t.Fatal(r)
	}
	r, err := s.VerifyChain()
	if err != nil || r.Break != nil || r.Sealed != 4 || r.Legacy != 0 {
		t.Fatalf("report %+v %v (break %+v)", r, err, r.Break)
	}
}

func TestChainCatchesAnEditedEntry(t *testing.T) {
	s := newStore(t)
	storeOK(t, s, "a", "the agent may read the repo", Caller{})
	storeOK(t, s, "b", "unrelated", Caller{})
	f := dailyFile(t, s.dir)
	data, _ := os.ReadFile(f)
	_ = os.WriteFile(f, []byte(strings.Replace(string(data), "may read", "may write", 1)), 0o644)

	r, _ := s.VerifyChain()
	if r.Break == nil || r.Break.Entity != "a" || !strings.Contains(r.Break.Reason, "content changed") {
		t.Fatalf("edit not caught: %+v", r.Break)
	}
}

func TestChainCatchesARemovedEntry(t *testing.T) {
	s := newStore(t)
	storeOK(t, s, "a", "one", Caller{})
	storeOK(t, s, "b", "two", Caller{})
	storeOK(t, s, "c", "three", Caller{})
	f := dailyFile(t, s.dir)
	data, _ := os.ReadFile(f)
	text := string(data)
	start := strings.Index(text, "\n## b\n")
	end := strings.Index(text[start:], "\n---\n") + start + len("\n---\n")
	_ = os.WriteFile(f, []byte(text[:start]+text[end-1:]), 0o644)

	r, _ := s.VerifyChain()
	if r.Break == nil || r.Break.Entity != "c" || !strings.Contains(r.Break.Reason, "does not follow") {
		t.Fatalf("removal not caught: %+v", r.Break)
	}
}

func TestChainStartsAfterLegacyEntries(t *testing.T) {
	dir := t.TempDir()
	day := filepath.Join(dir, workspaceDir, memoryDir)
	_ = os.MkdirAll(day, 0o755)
	// an entry written by mem7 before the chain existed
	legacy := formatStoreEntry(fact{Entity: "old", Object: "from June", Created: mustTime(t, "2026-06-07T10:00:00Z"), Updated: mustTime(t, "2026-06-07T10:00:00Z")})
	_ = os.WriteFile(filepath.Join(day, "2026-06-07.md"), []byte(legacy), 0o644)

	s, err := NewStore(dir, 1000)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	storeOK(t, s, "new", "sealed", Caller{})
	r, _ := s.VerifyChain()
	if r.Break != nil || r.Legacy != 1 || r.Sealed != 1 {
		t.Fatalf("report %+v (break %+v)", r, r.Break)
	}
}

func TestChainWithKey(t *testing.T) {
	s := newStore(t)
	s.SetChainKey([]byte("k3y"))
	storeOK(t, s, "a", "v", Caller{})
	if r, _ := VerifyChain(s.dir, []byte("k3y")); r.Break != nil || !r.Keyed {
		t.Fatalf("with the key: %+v", r.Break)
	}
	if r, _ := VerifyChain(s.dir, nil); r.Break == nil || !strings.Contains(r.Break.Reason, "MEM7_CHAIN_KEY") {
		t.Fatalf("without the key the HMAC must not verify: %+v", r.Break)
	}
	if r, _ := VerifyChain(s.dir, []byte("other")); r.Break == nil {
		t.Fatal("a wrong key must not verify")
	}
}

func TestHistory(t *testing.T) {
	s := newStore(t)
	scout := Caller{Agent: "scout7", TraceID: traceID}
	if r := s.ToolStoreAs(map[string]any{"key": "k", "value": "v1", "tags": []any{"t1", "t2"}}, scout); isErr(r) {
		t.Fatal(r)
	}
	if r := s.ToolStoreAs(map[string]any{"key": "k", "value": "v2", "tags": []any{"t1"}}, scout); isErr(r) {
		t.Fatal(r)
	}
	if r := s.ToolForget(map[string]any{"tags": []any{"t1"}}); isErr(r) {
		t.Fatal(r)
	}
	got := getText(t, s.ToolHistory(map[string]any{"key": "k"}))
	for _, want := range []string{"3 events", "store by scout7 · trace " + traceID, "update by scout7", "delete by tags t1", "seal "} {
		if !strings.Contains(got, want) {
			t.Errorf("history lacks %q:\n%s", want, got)
		}
	}
	s.SetScopes(&Scopes{})
	if got := getText(t, s.ToolHistoryAs(map[string]any{"key": "k"}, Caller{Agent: "audit7"})); got != "No history." {
		t.Errorf("history leaks another agent's key: %s", got)
	}
}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()
	tm, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return tm
}
