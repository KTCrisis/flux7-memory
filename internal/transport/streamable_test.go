package transport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func mcpPost(t *testing.T, h http.Handler, body, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMCPStreamableRoundTrip(t *testing.T) {
	h := NewHTTPServer(newLocal(t), "", nil).Handler()

	rec := mcpPost(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`, "")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("initialize: %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Mcp-Session-Id") != "" {
		t.Error("mem7 must not issue a session")
	}
	var init struct {
		Result struct{ ProtocolVersion string } `json:"result"`
	}
	json.Unmarshal(rec.Body.Bytes(), &init)
	if init.Result.ProtocolVersion != "2025-06-18" {
		t.Errorf("negotiated %q, want the client's supported 2025-06-18", init.Result.ProtocolVersion)
	}

	if rec := mcpPost(t, h, `{"jsonrpc":"2.0","method":"notifications/initialized"}`, ""); rec.Code != http.StatusAccepted || rec.Body.Len() != 0 {
		t.Errorf("notification: %d, body %q; want 202 and no body", rec.Code, rec.Body.String())
	}

	rec = mcpPost(t, h, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "memory_store") {
		t.Errorf("tools/list over /mcp: %d %s", rec.Code, rec.Body.String()[:min(120, rec.Body.Len())])
	}

	rec = mcpPost(t, h, `{"jsonrpc":"2.0","id":3,"method":"nope"}`, "")
	if !strings.Contains(rec.Body.String(), "-32601") {
		t.Errorf("unknown method must be a JSON-RPC error: %s", rec.Body.String())
	}
}

func TestMCPUnknownVersionGetsLatest(t *testing.T) {
	h := NewHTTPServer(newLocal(t), "", nil).Handler()
	rec := mcpPost(t, h, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2099-01-01"}}`, "")
	if !strings.Contains(rec.Body.String(), `"2025-11-25"`) {
		t.Errorf("an unknown version must be answered with the latest supported: %s", rec.Body.String())
	}
}

func TestMCPNoStreamNoSession(t *testing.T) {
	h := NewHTTPServer(newLocal(t), "", nil).Handler()
	for _, m := range []string{http.MethodGet, http.MethodDelete} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(m, "/mcp", nil))
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "POST" {
			t.Errorf("%s /mcp = %d allow=%q, want 405 POST", m, rec.Code, rec.Header().Get("Allow"))
		}
	}
}

func TestMCPRequiresToken(t *testing.T) {
	h := NewHTTPServer(newLocal(t), "s3cret", nil).Handler()
	if rec := mcpPost(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token = %d, want 401", rec.Code)
	}
	if rec := mcpPost(t, h, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`, "s3cret"); rec.Code != http.StatusOK {
		t.Errorf("with token = %d, want 200", rec.Code)
	}
}
