package memory

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const tp = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"
const traceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func isErr(r Result) bool { return r["isError"] == true }

func TestTraceIDOf(t *testing.T) {
	if got := traceIDOf(tp); got != traceID {
		t.Errorf("got %q", got)
	}
	for _, bad := range []string{"", "nope", "00-" + strings.Repeat("0", 32) + "-00f067aa0ba902b7-01", "00-4BF92F3577B34DA6A3CE929D0E0E4736-00f067aa0ba902b7-01"} {
		if got := traceIDOf(bad); got != "" {
			t.Errorf("%q gave %q", bad, got)
		}
	}
}

func TestCallerIdentityNeedsAuthentication(t *testing.T) {
	meta := map[string]any{MetaAgent: "scout7", MetaTraceparent: tp}
	anon := callerFrom(context.Background(), meta)
	if anon.Agent != "" || anon.TraceID != traceID {
		t.Errorf("unauthenticated: %+v (trace kept, identity ignored)", anon)
	}
	auth := callerFrom(WithAuthenticated(context.Background()), meta)
	if auth.Agent != "scout7" || auth.TraceID != traceID {
		t.Errorf("authenticated: %+v", auth)
	}
}

// The trace id is written to the markdown, survives a rescan of the index,
// and comes back in memory_context.
func TestProvenanceSurvivesRescan(t *testing.T) {
	s := newStore(t)
	c := Caller{Agent: "scout7", TraceID: traceID}
	if r := s.ToolStoreAs(map[string]any{"key": "arch:x", "value": "an agent mesh with a registry", "agent": "someone-else"}, c); isErr(r) {
		t.Fatal(r)
	}
	md, _ := os.ReadFile(mdFile(t, s))
	if !strings.Contains(string(md), "trace: "+traceID) || !strings.Contains(string(md), "agent: scout7") {
		t.Fatalf("markdown lacks provenance:\n%s", md)
	}
	if _, err := s.Rescan(); err != nil {
		t.Fatal(err)
	}
	var items []map[string]any
	_ = json.Unmarshal([]byte(getText(t, s.ToolContext(map[string]any{"query": "registry"}))), &items)
	if len(items) != 1 || items[0]["trace_id"] != traceID || items[0]["agent"] != "scout7" {
		t.Errorf("context after rescan: %v (the vouched identity replaces the declared agent)", items)
	}
	if !strings.Contains(getText(t, s.ToolRecall(map[string]any{"key": "arch:x"})), "Trace: "+traceID) {
		t.Error("recall should show the trace")
	}
	if r := s.ToolForgetAs(map[string]any{"key": "arch:x"}, c); isErr(r) {
		t.Fatal(r)
	}
	md, _ = os.ReadFile(mdFile(t, s))
	if strings.Count(string(md), "trace: "+traceID) != 2 {
		t.Errorf("the deletion should carry its trace too:\n%s", md)
	}
}

func mdFile(t *testing.T, s *Store) string {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(s.dir, workspaceDir, "*", "*.md"))
	if len(files) == 0 {
		files, _ = filepath.Glob(filepath.Join(s.dir, workspaceDir, "*.md"))
	}
	if len(files) != 1 {
		t.Fatalf("workspace files: %v", files)
	}
	return files[0]
}

func scopedStore(t *testing.T) *Store {
	t.Helper()
	s := newStore(t)
	s.SetScopes(&Scopes{Read: map[string][]string{"claude": {"*"}, "scout7": {}, "sup7": {"scout7"}}, Admin: []string{"admin"}})
	for _, w := range []struct{ agent, key, value string }{
		{"scout7", "s:1", "scout7 found an agent mesh"},
		{"claude", "c:1", "claude notes about the mesh"},
	} {
		if r := s.ToolStoreAs(map[string]any{"key": w.key, "value": w.value, "tags": []any{"mesh"}}, Caller{Agent: w.agent}); isErr(r) {
			t.Fatal(r)
		}
	}
	return s
}

func TestScopesRestrictReads(t *testing.T) {
	s := scopedStore(t)
	scout, claude, sup := Caller{Agent: "scout7"}, Caller{Agent: "claude"}, Caller{Agent: "sup7"}

	if got := getText(t, s.ToolListAs(map[string]any{}, scout)); !strings.Contains(got, "s:1") || strings.Contains(got, "c:1") {
		t.Errorf("scout7 lists only its own:\n%s", got)
	}
	if got := getText(t, s.ToolListAs(map[string]any{}, claude)); !strings.Contains(got, "s:1") || !strings.Contains(got, "c:1") {
		t.Errorf("claude reads * :\n%s", got)
	}
	if got := getText(t, s.ToolListAs(map[string]any{}, sup)); !strings.Contains(got, "s:1") || strings.Contains(got, "c:1") {
		t.Errorf("sup7 reads scout7 only:\n%s", got)
	}
	if got := getText(t, s.ToolSearchAs(map[string]any{"query": "mesh"}, scout)); strings.Contains(got, "c:1") {
		t.Errorf("search leaks claude's memory to scout7:\n%s", got)
	}
	if got := getText(t, s.ToolRecallAs(map[string]any{"key": "c:1"}, scout)); !strings.Contains(got, "No memories found") {
		t.Errorf("recall by key leaks:\n%s", got)
	}
	var items []map[string]any
	_ = json.Unmarshal([]byte(getText(t, s.ToolContextAs(map[string]any{"query": "mesh"}, scout))), &items)
	for _, it := range items {
		if it["agent"] != "scout7" {
			t.Errorf("context leaks %v", it)
		}
	}
	// not identified: the mesh's own writer, the supervisor, the console
	if got := getText(t, s.ToolList(map[string]any{})); !strings.Contains(got, "c:1") || !strings.Contains(got, "s:1") {
		t.Errorf("an unidentified client is not scoped:\n%s", got)
	}
}

func TestScopesGuardWritesAndDeletes(t *testing.T) {
	s := scopedStore(t)
	scout := Caller{Agent: "scout7"}
	if r := s.ToolStoreAs(map[string]any{"key": "c:1", "value": "overwritten"}, scout); !isErr(r) {
		t.Error("scout7 must not overwrite claude's memory")
	}
	if r := s.ToolForgetAs(map[string]any{"key": "c:1"}, scout); !isErr(r) {
		t.Error("scout7 must not forget claude's memory")
	}
	if r := s.ToolForgetAs(map[string]any{"tags": []any{"mesh"}}, scout); !isErr(r) {
		t.Error("forget by tags is for administrators")
	}
	if r := s.ToolGetAs(map[string]any{"path": "."}, scout); !isErr(r) {
		t.Error("the raw workspace is for administrators")
	}
	if r := s.ToolForgetAs(map[string]any{"key": "s:1"}, scout); isErr(r) {
		t.Errorf("scout7 forgets its own: %v", r)
	}
	if r := s.ToolForgetAs(map[string]any{"tags": []any{"mesh"}}, Caller{Agent: "admin"}); isErr(r) {
		t.Errorf("an administrator forgets by tags: %v", r)
	}
}

func TestNoScopesFileKeepsReadsOpen(t *testing.T) {
	s := newStore(t)
	_ = s.ToolStoreAs(map[string]any{"key": "c:1", "value": "v"}, Caller{Agent: "claude"})
	if got := getText(t, s.ToolListAs(map[string]any{}, Caller{Agent: "scout7"})); !strings.Contains(got, "c:1") {
		t.Errorf("without scopes, identities sign writes but reads stay open:\n%s", got)
	}
}

func TestLoadScopes(t *testing.T) {
	f := filepath.Join(t.TempDir(), "scopes.json")
	_ = os.WriteFile(f, []byte(`{"read":{"claude":["*"]},"admin":["claude"]}`), 0o600)
	sc, err := LoadScopes(f)
	if err != nil || !sc.canRead("claude", "scout7") || sc.canRead("scout7", "claude") || !sc.isAdmin("claude") {
		t.Errorf("scopes: %+v %v", sc, err)
	}
	_ = os.WriteFile(f, []byte(`{"read":{"x":["[" ]}}`), 0o600)
	if _, err := LoadScopes(f); err == nil {
		t.Error("a bad pattern should be refused at start")
	}
}
