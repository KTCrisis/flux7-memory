package transport

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func sseServer(t *testing.T) (*httptest.Server, *HTTPServer) {
	t.Helper()
	local := newLocal(t)
	hs := NewHTTPServer(local, "", nil)
	srv := httptest.NewServer(hs.Handler())
	t.Cleanup(srv.Close)
	return srv, hs
}

type sseEvent struct {
	Type string
	Data string
}

func readSSEEvent(scanner *bufio.Scanner) (sseEvent, bool) {
	var ev sseEvent
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if ev.Type != "" || ev.Data != "" {
				return ev, true
			}
			continue
		}
		if strings.HasPrefix(line, "event:") {
			ev.Type = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		} else if strings.HasPrefix(line, "data:") {
			ev.Data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		}
	}
	return ev, false
}

func openSSE(t *testing.T, baseURL string) (endpoint string, scanner *bufio.Scanner, cancel context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/sse", nil)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	if resp.StatusCode != 200 {
		cancel()
		t.Fatalf("expected 200 on /sse, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		cancel()
		t.Fatalf("expected text/event-stream, got %s", ct)
	}

	scanner = bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	ev, ok := readSSEEvent(scanner)
	if !ok {
		cancel()
		t.Fatal("no endpoint event received")
	}
	if ev.Type != "endpoint" {
		cancel()
		t.Fatalf("expected endpoint event, got %q", ev.Type)
	}

	endpoint = ev.Data
	if !strings.HasPrefix(endpoint, "http") {
		endpoint = baseURL + endpoint
	}
	return endpoint, scanner, cancel
}

func postMessage(t *testing.T, endpoint string, id int, method string, params any) {
	t.Helper()
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}
	data, _ := json.Marshal(body)

	resp, err := http.Post(endpoint, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		t.Fatalf("POST to %s: status %d", endpoint, resp.StatusCode)
	}
}

func readMessageEventRaw(t *testing.T, scanner *bufio.Scanner) json.RawMessage {
	t.Helper()
	done := make(chan sseEvent, 1)
	go func() {
		ev, ok := readSSEEvent(scanner)
		if ok {
			done <- ev
		}
	}()
	select {
	case ev := <-done:
		if ev.Type != "message" {
			t.Fatalf("expected message event, got %q", ev.Type)
		}
		return json.RawMessage(ev.Data)
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for SSE message event")
		return nil
	}
}

func readMessageEvent(t *testing.T, scanner *bufio.Scanner) map[string]any {
	t.Helper()
	raw := readMessageEventRaw(t, scanner)
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatalf("invalid JSON in message event: %v", err)
	}
	return result
}

func TestSSEEndpointEvent(t *testing.T) {
	srv, _ := sseServer(t)
	endpoint, _, cancel := openSSE(t, srv.URL)
	defer cancel()

	if !strings.Contains(endpoint, "/messages?sessionId=") {
		t.Fatalf("endpoint should contain /messages?sessionId=, got %s", endpoint)
	}
}

func TestSSEInitialize(t *testing.T) {
	srv, _ := sseServer(t)
	endpoint, scanner, cancel := openSSE(t, srv.URL)
	defer cancel()

	postMessage(t, endpoint, 1, "initialize", nil)
	resp := readMessageEvent(t, scanner)

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", resp)
	}
	info := result["serverInfo"].(map[string]any)
	if info["name"] != "mem7" {
		t.Fatalf("unexpected server info: %v", info)
	}
}

func TestSSEToolsList(t *testing.T) {
	srv, _ := sseServer(t)
	endpoint, scanner, cancel := openSSE(t, srv.URL)
	defer cancel()

	postMessage(t, endpoint, 1, "tools/list", nil)
	resp := readMessageEvent(t, scanner)

	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("expected result object, got %v", resp)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("expected non-empty tools list, got %v", result)
	}
}

func TestSSEStoreAndRecall(t *testing.T) {
	srv, _ := sseServer(t)
	endpoint, scanner, cancel := openSSE(t, srv.URL)
	defer cancel()

	postMessage(t, endpoint, 1, "tools/call", map[string]any{
		"name":      "memory_store",
		"arguments": map[string]any{"key": "sse-k", "value": "sse-v"},
	})
	storeResp := readMessageEvent(t, scanner)
	if storeResp["error"] != nil {
		t.Fatalf("store failed: %v", storeResp["error"])
	}

	postMessage(t, endpoint, 2, "tools/call", map[string]any{
		"name":      "memory_recall",
		"arguments": map[string]any{"key": "sse-k"},
	})
	recallResp := readMessageEvent(t, scanner)
	if recallResp["error"] != nil {
		t.Fatalf("recall failed: %v", recallResp["error"])
	}

	raw, _ := json.Marshal(recallResp["result"])
	if !strings.Contains(string(raw), "sse-v") {
		t.Fatalf("expected 'sse-v' in recall result, got: %s", raw)
	}
}

func TestSSEUnknownSession(t *testing.T) {
	srv, _ := sseServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	resp, err := http.Post(srv.URL+"/messages?sessionId=nonexistent", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 for unknown session, got %d", resp.StatusCode)
	}
}

func TestSSEAuthOnMessages(t *testing.T) {
	local := newLocal(t)
	hs := NewHTTPServer(local, "secret-sse", nil)
	srv := httptest.NewServer(hs.Handler())
	defer srv.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize"}`
	resp, err := http.Post(srv.URL+"/messages?sessionId=any", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d", resp.StatusCode)
	}
}

func TestSSEMethodNotFound(t *testing.T) {
	srv, _ := sseServer(t)
	endpoint, scanner, cancel := openSSE(t, srv.URL)
	defer cancel()

	postMessage(t, endpoint, 1, "does/not/exist", nil)
	resp := readMessageEvent(t, scanner)

	errObj, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("expected error field, got %v", resp)
	}
	if int(errObj["code"].(float64)) != -32601 {
		t.Fatalf("expected code -32601, got %v", errObj["code"])
	}
}

func TestSSEParityVsHTTP(t *testing.T) {
	calls := []struct {
		method string
		params map[string]any
	}{
		{"initialize", nil},
		{"tools/list", nil},
		{"tools/call", map[string]any{
			"name":      "memory_store",
			"arguments": map[string]any{"key": "pk", "value": "pv", "tags": []any{"t1"}},
		}},
		{"tools/call", map[string]any{
			"name":      "memory_recall",
			"arguments": map[string]any{"key": "pk"},
		}},
		{"tools/call", map[string]any{
			"name":      "memory_list",
			"arguments": map[string]any{},
		}},
	}

	// HTTP path
	httpLocal := newLocal(t)
	httpServer := NewHTTPServer(httpLocal, "", nil)
	h := httpServer.Handler()
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
		body, _ := io.ReadAll(rec.Result().Body)
		var resp struct {
			Result json.RawMessage `json:"result"`
		}
		json.Unmarshal(body, &resp)
		httpResults[i] = string(resp.Result)
	}

	// SSE path — fresh store
	sseLocal := newLocal(t)
	sseSrv := NewHTTPServer(sseLocal, "", nil)
	ts := httptest.NewServer(sseSrv.Handler())
	defer ts.Close()

	endpoint, scanner, cancel := openSSE(t, ts.URL)
	defer cancel()

	sseResults := make([]string, len(calls))
	for i, c := range calls {
		postMessage(t, endpoint, i+1, c.method, c.params)
		raw := readMessageEventRaw(t, scanner)
		var resp struct {
			Result json.RawMessage `json:"result"`
		}
		json.Unmarshal(raw, &resp)
		sseResults[i] = string(resp.Result)
	}

	for i, c := range calls {
		switch c.method {
		case "initialize", "tools/list":
			if httpResults[i] != sseResults[i] {
				t.Fatalf("parity mismatch on %s:\nhttp: %s\nsse:  %s", c.method, httpResults[i], sseResults[i])
			}
		default:
			for label, r := range map[string]string{"http": httpResults[i], "sse": sseResults[i]} {
				if !strings.Contains(r, `"content"`) {
					t.Fatalf("call %d (%v) %s missing content envelope: %s", i, c.params["name"], label, r)
				}
			}
		}
	}

	fmt.Println("SSE/HTTP parity verified for", len(calls), "calls")
}
