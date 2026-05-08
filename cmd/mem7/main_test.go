package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/KTCrisis/flux7-memory/internal/memory"
	"github.com/KTCrisis/flux7-memory/internal/transport"
)

// TestStdioRoundTrip drives the stdio loop end-to-end through a Local
// transport. It mirrors how Claude Code / agent-mesh talks to mem7 in
// default (stdio) mode.
func TestStdioRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := memory.NewStore(dir, 10000)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	local := transport.NewLocal(memory.NewDispatcher(store))

	messages := []rpcRequest{
		{JSONRPC: "2.0", ID: json.RawMessage(`1`), Method: "initialize"},
		{JSONRPC: "2.0", ID: json.RawMessage(`2`), Method: "tools/list"},
	}

	storeParams, _ := json.Marshal(map[string]any{
		"name":      "memory_store",
		"arguments": map[string]any{"key": "hello", "value": "world"},
	})
	messages = append(messages, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "tools/call", Params: storeParams})

	recallParams, _ := json.Marshal(map[string]any{
		"name":      "memory_recall",
		"arguments": map[string]any{"key": "hello"},
	})
	messages = append(messages, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage(`4`), Method: "tools/call", Params: recallParams})

	var input bytes.Buffer
	for _, msg := range messages {
		data, _ := json.Marshal(msg)
		input.Write(data)
		input.WriteByte('\n')
	}

	var output bytes.Buffer
	if err := serveStdio(context.Background(), local, &input, &output); err != nil {
		t.Fatalf("serveStdio: %v", err)
	}

	respScanner := bufio.NewScanner(&output)
	var responses []rpcResponse
	for respScanner.Scan() {
		var resp rpcResponse
		if err := json.Unmarshal(respScanner.Bytes(), &resp); err == nil {
			responses = append(responses, resp)
		}
	}

	if len(responses) != 4 {
		t.Fatalf("expected 4 responses, got %d", len(responses))
	}

	if !strings.Contains(string(responses[3].Result), "world") {
		t.Fatalf("expected recall to contain 'world', got: %s", string(responses[3].Result))
	}
}
