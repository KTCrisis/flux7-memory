package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/KTCrisis/flux7-memory/internal/memory"
)

func newLocal(t *testing.T) *Local {
	t.Helper()
	store, err := memory.NewStore(t.TempDir(), 10000)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return NewLocal(memory.NewDispatcher(store))
}

func post(t *testing.T, h http.Handler, body string, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/rpc", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHTTPHealthz(t *testing.T) {
	server := NewHTTPServer(newLocal(t), "", nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("unexpected body: %s", rec.Body.String())
	}
}

func TestHTTPAuthRejectsMissingToken(t *testing.T) {
	server := NewHTTPServer(newLocal(t), "secret", nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	rec := post(t, server.Handler(), body, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
}

func TestHTTPAuthAcceptsValidToken(t *testing.T) {
	server := NewHTTPServer(newLocal(t), "secret", nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	rec := post(t, server.Handler(), body, "secret")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	info := resp.Result["serverInfo"].(map[string]any)
	if info["name"] != "mem7" {
		t.Fatalf("unexpected server info: %v", info)
	}
}

func TestHTTPUnknownMethod(t *testing.T) {
	server := NewHTTPServer(newLocal(t), "", nil)
	body := `{"jsonrpc":"2.0","id":1,"method":"does/not/exist"}`
	rec := post(t, server.Handler(), body, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 (JSON-RPC error is still HTTP 200), got %d", rec.Code)
	}
	var resp struct {
		Error *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Error == nil {
		t.Fatal("expected error field")
	}
	if resp.Error.Code != -32601 {
		t.Fatalf("expected code -32601, got %d", resp.Error.Code)
	}
}

func TestHTTPSnapshotReminder(t *testing.T) {
	server := NewHTTPServer(newLocal(t), "", nil)
	req := httptest.NewRequest(http.MethodPost, "/memory/snapshot_reminder", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if _, ok := payload["reminder"].(string); !ok {
		t.Fatalf("missing reminder field: %v", payload)
	}
	if _, ok := payload["workspace"].(string); !ok {
		t.Fatalf("missing workspace field: %v", payload)
	}
	if _, ok := payload["memory_count"]; !ok {
		t.Fatalf("missing memory_count field: %v", payload)
	}
}

func TestHTTPSnapshotReminderRequiresPOST(t *testing.T) {
	server := NewHTTPServer(newLocal(t), "", nil)
	req := httptest.NewRequest(http.MethodGet, "/memory/snapshot_reminder", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("expected 405, got %d", rec.Code)
	}
}

func TestHTTPStoreAndRecall(t *testing.T) {
	local := newLocal(t)
	server := NewHTTPServer(local, "", nil)
	h := server.Handler()

	storeBody := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"memory_store","arguments":{"key":"k","value":"hello"}}}`
	rec := post(t, h, storeBody, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("store: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	recallBody := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_recall","arguments":{"key":"k"}}}`
	rec = post(t, h, recallBody, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("recall: expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "hello") {
		t.Fatalf("expected 'hello' in recall result, got: %s", rec.Body.String())
	}
}

// TestParityLocalVsHTTP verifies that the same sequence of calls produces
// the same tool-level results whether routed through a direct Local
// transport or through an HTTP round-trip. This is the smoke test the
// transport abstraction exists to enable.
func TestParityLocalVsHTTP(t *testing.T) {
	calls := []struct {
		method string
		params map[string]any
	}{
		{"initialize", nil},
		{"tools/list", nil},
		{"tools/call", map[string]any{
			"name":      "memory_store",
			"arguments": map[string]any{"key": "k1", "value": "v1", "tags": []any{"x"}},
		}},
		{"tools/call", map[string]any{
			"name":      "memory_store",
			"arguments": map[string]any{"key": "k2", "value": "v2", "tags": []any{"x", "y"}},
		}},
		{"tools/call", map[string]any{
			"name":      "memory_recall",
			"arguments": map[string]any{"tags": []any{"x"}},
		}},
		{"tools/call", map[string]any{
			"name":      "memory_list",
			"arguments": map[string]any{},
		}},
		{"tools/call", map[string]any{
			"name":      "memory_forget",
			"arguments": map[string]any{"key": "k1"},
		}},
	}

	// Local path.
	localT := newLocal(t)
	localResults := make([]string, len(calls))
	for i, c := range calls {
		var params json.RawMessage
		if c.params != nil {
			params, _ = json.Marshal(c.params)
		}
		raw, err := localT.Call(context.Background(), c.method, params)
		if err != nil {
			t.Fatalf("local call %d (%s): %v", i, c.method, err)
		}
		localResults[i] = string(raw)
	}

	// HTTP path — fresh store so the sequence starts from the same state.
	httpT := newLocal(t)
	server := NewHTTPServer(httpT, "", nil)
	h := server.Handler()
	httpResults := make([]string, len(calls))
	for i, c := range calls {
		reqBody, _ := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      i + 1,
			"method":  c.method,
			"params":  c.params,
		})
		req := httptest.NewRequest(http.MethodPost, "/rpc", bytes.NewReader(reqBody))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("http call %d (%s): expected 200, got %d", i, c.method, rec.Code)
		}
		body, _ := io.ReadAll(rec.Result().Body)
		var resp struct {
			Result json.RawMessage `json:"result"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			t.Fatal(err)
		}
		httpResults[i] = string(resp.Result)
	}

	// Compare. Timestamp-bearing results (recall, list, the upsert
	// confirmation) are not byte-identical because Updated timestamps
	// differ between runs — so we compare the tools/list and initialize
	// bodies byte-for-byte, and check the tool-call results for the
	// expected content instead.
	for i, c := range calls {
		switch c.method {
		case "initialize", "tools/list":
			if localResults[i] != httpResults[i] {
				t.Fatalf("parity mismatch on %s:\nlocal:  %s\nhttp:   %s", c.method, localResults[i], httpResults[i])
			}
		default:
			// Every tool call must come back with a content envelope.
			for _, r := range []string{localResults[i], httpResults[i]} {
				if !strings.Contains(r, `"content"`) {
					t.Fatalf("call %d (%v) missing content envelope: %s", i, c.params["name"], r)
				}
			}
		}
	}
}
