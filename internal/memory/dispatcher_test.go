package memory

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func newDispatcher(t *testing.T) *Dispatcher {
	t.Helper()
	store, err := NewStore(t.TempDir(), 10000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewDispatcher(store)
}

func TestDispatcherInitialize(t *testing.T) {
	d := newDispatcher(t)
	raw, err := d.Call(context.Background(), "initialize", nil)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["protocolVersion"] != ProtocolVersion {
		t.Fatalf("expected protocol %s, got %v", ProtocolVersion, m["protocolVersion"])
	}
	info := m["serverInfo"].(map[string]any)
	if info["name"] != "mem7" {
		t.Fatalf("expected server name mem7, got %v", info["name"])
	}
}

func TestDispatcherToolsList(t *testing.T) {
	d := newDispatcher(t)
	raw, err := d.Call(context.Background(), "tools/list", nil)
	if err != nil {
		t.Fatalf("tools/list: %v", err)
	}
	var m struct {
		Tools []Tool `json:"tools"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Tools) != 7 {
		t.Fatalf("expected 7 tools, got %d", len(m.Tools))
	}
}

func TestDispatcherUnknownMethod(t *testing.T) {
	d := newDispatcher(t)
	_, err := d.Call(context.Background(), "does/not/exist", nil)
	if err == nil {
		t.Fatal("expected error for unknown method")
	}
	rerr, ok := err.(*RPCError)
	if !ok {
		t.Fatalf("expected *RPCError, got %T", err)
	}
	if rerr.Code != -32601 {
		t.Fatalf("expected code -32601, got %d", rerr.Code)
	}
}

func TestDispatcherUnknownTool(t *testing.T) {
	d := newDispatcher(t)
	params, _ := json.Marshal(map[string]any{"name": "nope", "arguments": map[string]any{}})
	raw, err := d.Call(context.Background(), "tools/call", params)
	if err != nil {
		t.Fatalf("tools/call: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !m["isError"].(bool) {
		t.Fatal("expected tool-level error envelope")
	}
	content := m["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "unknown tool") {
		t.Fatalf("expected 'unknown tool' message, got %q", text)
	}
}
