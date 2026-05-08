package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDaemonRunningNoServer(t *testing.T) {
	if daemonRunning("localhost:19999") {
		t.Error("expected false when no server is listening")
	}
}

func TestDaemonRunningWithServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")
	if !daemonRunning(addr) {
		t.Error("expected true when server is listening")
	}
}

func TestNormalizeAddr(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{":9070", "localhost:9070"},
		{"localhost:9070", "localhost:9070"},
		{"192.168.1.1:9070", "192.168.1.1:9070"},
	}
	for _, tt := range tests {
		got := normalizeAddr(tt.in)
		if got != tt.want {
			t.Errorf("normalizeAddr(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestStdioProxyForward(t *testing.T) {
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		gotToken = r.Header.Get("Authorization")
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"result":  map[string]any{"ok": true},
		})
	}))
	defer srv.Close()

	addr := strings.TrimPrefix(srv.URL, "http://")

	input := `{"jsonrpc":"2.0","id":1,"method":"memory_store","params":{"key":"test"}}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n" +
		`{"jsonrpc":"2.0","id":2,"method":"memory_recall","params":{"query":"test"}}` + "\n"

	var out bytes.Buffer
	err := runStdioProxyWith(addr, "test-token", strings.NewReader(input), &out)
	if err != nil {
		t.Fatal(err)
	}

	if gotToken != "Bearer test-token" {
		t.Errorf("expected Bearer token, got %s", gotToken)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2 responses (notification skipped), got %d: %v", len(lines), lines)
	}
}
