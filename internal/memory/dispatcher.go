package memory

import (
	"context"
	"encoding/json"
	"fmt"
)

// Version is the mem7 server version reported in the MCP initialize
// handshake. It defaults to "dev" and is overridden at build time by
// the Makefile via -ldflags "-X github.com/KTCrisis/flux7-memory/internal/memory.Version=<git-describe>".
// A plain `go build` without Makefile will report "dev".
var Version = "dev"

// ProtocolVersion is the MCP protocol version mem7 speaks.
const ProtocolVersion = "2024-11-05"

// --- MCP tool metadata ---

// Tool describes a single MCP tool exposed via tools/list.
type Tool struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	InputSchema ToolSchema `json:"inputSchema"`
}

// ToolSchema is the JSON Schema subset used for tool input schemas.
type ToolSchema struct {
	Type       string              `json:"type"`
	Properties map[string]ToolProp `json:"properties,omitempty"`
	Required   []string            `json:"required,omitempty"`
}

// ToolProp describes a single property of a tool input schema.
type ToolProp struct {
	Type        string     `json:"type"`
	Description string     `json:"description"`
	Items       *ToolItems `json:"items,omitempty"`
	Default     any        `json:"default,omitempty"`
}

// ToolItems describes array element types in a tool input schema.
type ToolItems struct {
	Type string `json:"type"`
}

// Tools is the canonical list of MCP tools exposed by mem7.
var Tools = []Tool{
	{
		Name:        "memory_store",
		Description: "Store or update a memory entry. If a key already exists, it is updated (upsert).",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]ToolProp{
				"key":   {Type: "string", Description: "Unique key for this memory"},
				"value": {Type: "string", Description: "The content to remember"},
				"tags":  {Type: "array", Description: "Tags for filtering and grouping", Items: &ToolItems{Type: "string"}},
				"agent": {Type: "string", Description: "Identifier of the agent storing this memory"},
				"ttl":   {Type: "number", Description: "Time-to-live in seconds (0 = permanent)", Default: 0},
			},
			Required: []string{"key", "value"},
		},
	},
	{
		Name:        "memory_recall",
		Description: "Recall memories by key, tags, or agent. Returns matching entries sorted by most recent.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]ToolProp{
				"key":   {Type: "string", Description: "Exact key to recall"},
				"tags":  {Type: "array", Description: "Filter by tags (AND logic: all tags must match)", Items: &ToolItems{Type: "string"}},
				"agent": {Type: "string", Description: "Filter by agent identifier"},
				"limit": {Type: "number", Description: "Max number of results (default 10)", Default: 10},
			},
		},
	},
	{
		Name:        "memory_search",
		Description: "Full-text search over memories using BM25 ranking. Use mode='natural' for plain-English questions ; mode='raw' (default) if you want to write FTS5 operators yourself (prefix foo*, AND, OR, NOT). Set include_neighbors=true to also return memories adjacent to sequential keys (e.g. conversational turns).",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]ToolProp{
				"query":             {Type: "string", Description: "Query string. In 'raw' mode this is an FTS5 expression ; in 'natural' mode it is a plain-English question."},
				"mode":              {Type: "string", Description: "'raw' (default, FTS5 operators preserved) or 'natural' (stop-word stripping, wildcard stemming, OR-join — use this for agent questions)."},
				"tags":              {Type: "array", Description: "Filter by tags (AND logic)", Items: &ToolItems{Type: "string"}},
				"agent":             {Type: "string", Description: "Filter by agent identifier"},
				"since":             {Type: "string", Description: "Lower bound on updated_at (RFC3339)"},
				"until":             {Type: "string", Description: "Upper bound on updated_at (RFC3339)"},
				"limit":             {Type: "number", Description: "Max number of results (default 10)", Default: 10},
				"include_neighbors": {Type: "boolean", Description: "When true, expands results with adjacent memories for sequential keys like 'conv.session.t005' (fetches t004 and t006). Useful for conversational memory where information spans multiple turns."},
				"neighbor_radius":   {Type: "number", Description: "How many memories to fetch on each side when include_neighbors=true (default 1).", Default: 1},
			},
			Required: []string{"query"},
		},
	},
	{
		Name:        "memory_get",
		Description: "Read a file from the markdown workspace, optionally between from_line and to_line (1-indexed, inclusive). The path is relative to the workspace root.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]ToolProp{
				"path":      {Type: "string", Description: "Path relative to the workspace (e.g. 'memory/2026-04-11.md' or 'MEMORY.md')"},
				"from_line": {Type: "number", Description: "Optional first line to read (1-indexed)"},
				"to_line":   {Type: "number", Description: "Optional last line to read (1-indexed, inclusive)"},
			},
			Required: []string{"path"},
		},
	},
	{
		Name:        "memory_list",
		Description: "List memory keys with metadata (without values). Useful for browsing what is stored.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]ToolProp{
				"tags":  {Type: "array", Description: "Filter by tags (AND logic)", Items: &ToolItems{Type: "string"}},
				"agent": {Type: "string", Description: "Filter by agent identifier"},
			},
		},
	},
	{
		Name:        "memory_forget",
		Description: "Delete memories by key or tags.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]ToolProp{
				"key":  {Type: "string", Description: "Exact key to delete"},
				"tags": {Type: "array", Description: "Delete all entries matching these tags (AND logic)", Items: &ToolItems{Type: "string"}},
			},
		},
	},
	{
		Name:        "memory_context",
		Description: "Search memories and return structured JSON results for programmatic use by agent SDKs. Same search capabilities as memory_search but returns a JSON array of objects with key, value, tags, agent, updated fields instead of formatted markdown.",
		InputSchema: ToolSchema{
			Type: "object",
			Properties: map[string]ToolProp{
				"query":             {Type: "string", Description: "Query string. In 'raw' mode this is an FTS5 expression ; in 'natural' mode it is a plain-English question."},
				"mode":              {Type: "string", Description: "'raw' (default) or 'natural' (stop-word stripping, wildcard stemming, OR-join)."},
				"tags":              {Type: "array", Description: "Filter by tags (AND logic)", Items: &ToolItems{Type: "string"}},
				"agent":             {Type: "string", Description: "Filter by agent identifier"},
				"since":             {Type: "string", Description: "Lower bound on updated_at (RFC3339)"},
				"until":             {Type: "string", Description: "Upper bound on updated_at (RFC3339)"},
				"limit":             {Type: "number", Description: "Max number of results (default 10)", Default: 10},
				"include_neighbors": {Type: "boolean", Description: "Expand results with adjacent memories for sequential keys."},
				"neighbor_radius":   {Type: "number", Description: "How many memories to fetch on each side (default 1).", Default: 1},
			},
			Required: []string{"query"},
		},
	},
}

// RPCError is a JSON-RPC 2.0 level error returned by Dispatcher.Call.
// Tool-level failures (invalid args, storage errors) are carried inside
// the result envelope with isError=true instead ; RPCError is reserved
// for "this method does not exist" and similar protocol-level problems.
type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string { return e.Message }

// Dispatcher is the single implementation of the MCP tool layer.
// Every Transport — local or HTTP — ultimately invokes a Dispatcher.
// It owns the initialize / tools.list / tools.call contract and the
// tool schemas, and delegates actual work to the wrapped Store.
type Dispatcher struct {
	store *Store
}

// NewDispatcher wires a Dispatcher to a Store.
func NewDispatcher(store *Store) *Dispatcher {
	return &Dispatcher{store: store}
}

// Call executes a single MCP method. The returned bytes are the raw
// JSON "result" field of a JSON-RPC response ; errors are JSON-RPC
// level (method not found, invalid params) and should be rendered into
// the "error" field.
func (d *Dispatcher) Call(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	var result any
	switch method {
	case "initialize":
		result = d.initialize()
	case "tools/list":
		result = d.toolsList()
	case "tools/call":
		result = d.toolsCall(params)
	case "memory/snapshot_reminder":
		result = d.store.SnapshotReminder()
	default:
		return nil, &RPCError{Code: -32601, Message: "method not found: " + method}
	}
	return json.Marshal(result)
}

func (d *Dispatcher) initialize() any {
	return map[string]any{
		"protocolVersion": ProtocolVersion,
		"capabilities": map[string]any{
			"tools": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "mem7",
			"version": Version,
		},
	}
}

func (d *Dispatcher) toolsList() any {
	return map[string]any{"tools": Tools}
}

func (d *Dispatcher) toolsCall(params json.RawMessage) any {
	if len(params) == 0 {
		return ErrResult("missing params")
	}
	var call struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &call); err != nil {
		return ErrResult(fmt.Sprintf("invalid params: %v", err))
	}
	args := call.Arguments
	if args == nil {
		args = map[string]any{}
	}
	switch call.Name {
	case "memory_store":
		return d.store.ToolStore(args)
	case "memory_recall":
		return d.store.ToolRecall(args)
	case "memory_search":
		return d.store.ToolSearch(args)
	case "memory_get":
		return d.store.ToolGet(args)
	case "memory_list":
		return d.store.ToolList(args)
	case "memory_forget":
		return d.store.ToolForget(args)
	case "memory_context":
		return d.store.ToolContext(args)
	default:
		return ErrResult("unknown tool: " + call.Name)
	}
}
