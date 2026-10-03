package transport

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// what flux7-mesh sends for a governed memory_store
const storeWithMeta = `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"memory_store",
 "arguments":{"key":"k","value":"v","agent":"declared"},
 "_meta":{"traceparent":"00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01","art.flux7/agent":"scout7"}}}`

const contextCall = `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"memory_context","arguments":{"query":"v"}}}`

func storedAgent(t *testing.T, h http.Handler, token string) map[string]any {
	t.Helper()
	if rec := mcpPost(t, h, storeWithMeta, token); rec.Code != http.StatusOK {
		t.Fatalf("store: %d %s", rec.Code, rec.Body.String())
	}
	rec := mcpPost(t, h, contextCall, token)
	var resp struct {
		Result struct {
			Content []struct{ Text string } `json:"content"`
		} `json:"result"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	var items []map[string]any
	if len(resp.Result.Content) > 0 {
		_ = json.Unmarshal([]byte(resp.Result.Content[0].Text), &items)
	}
	if len(items) != 1 {
		t.Fatalf("context: %s", rec.Body.String())
	}
	return items[0]
}

func TestIdentityHonouredOnlyWithToken(t *testing.T) {
	withToken := NewHTTPServer(newLocal(t), "s3cret", nil).Handler()
	got := storedAgent(t, withToken, "s3cret")
	if got["agent"] != "scout7" || got["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("with the token, the mesh's identity and trace are kept: %v", got)
	}

	open := NewHTTPServer(newLocal(t), "", nil).Handler()
	got = storedAgent(t, open, "")
	if got["agent"] != "declared" || got["trace_id"] != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Errorf("without a token, _meta cannot speak for an agent (trace still kept): %v", got)
	}

	if rec := mcpPost(t, withToken, storeWithMeta, "wrong"); rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "unauthorized") {
		t.Errorf("wrong token: %d", rec.Code)
	}
}
