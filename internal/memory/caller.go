package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"strings"
)

// Caller is who a tool call comes from, as far as mem7 can tell.
//
// flux7-mesh puts two things in the `_meta` of tools/call: the W3C
// traceparent of the governed call, and, for an upstream configured with
// forward_identity, the agent it authenticated. The trace id is provenance:
// mem7 records it on every write. The agent is an identity: mem7 honours it
// only on a request that carried mem7's bearer token, since anybody can
// write a `_meta`.
type Caller struct {
	// Agent is the identity vouched for by the mesh. Empty for clients that
	// reach mem7 directly (supervisor, console, the mesh's own decision
	// writer, a local stdio session): they are not scoped.
	Agent string
	// TraceID is the trace of the governed call behind this request.
	TraceID string
}

// MetaAgent and MetaTraceparent are the `_meta` keys flux7-mesh sends.
const (
	MetaAgent       = "art.flux7/agent"
	MetaTraceparent = "traceparent"
)

type authKey struct{}

// WithAuthenticated marks ctx as coming from a client that presented the
// server's bearer token. Only such requests may carry an identity.
func WithAuthenticated(ctx context.Context) context.Context {
	return context.WithValue(ctx, authKey{}, true)
}

// Authenticated reports whether ctx was marked by WithAuthenticated.
func Authenticated(ctx context.Context) bool {
	ok, _ := ctx.Value(authKey{}).(bool)
	return ok
}

// callerFrom reads the caller out of a tools/call `_meta`.
func callerFrom(ctx context.Context, meta map[string]any) Caller {
	var c Caller
	if tp, ok := meta[MetaTraceparent].(string); ok {
		c.TraceID = traceIDOf(tp)
	}
	if Authenticated(ctx) {
		if a, ok := meta[MetaAgent].(string); ok {
			c.Agent = strings.TrimSpace(a)
		}
	}
	return c
}

// traceIDOf returns the trace id of a W3C traceparent
// ("00-<32 hex>-<16 hex>-<2 hex>"), or "" when it is not one.
func traceIDOf(tp string) string {
	parts := strings.Split(strings.TrimSpace(tp), "-")
	if len(parts) != 4 || len(parts[1]) != 32 || !isLowerHex(parts[1]) || parts[1] == strings.Repeat("0", 32) {
		return ""
	}
	return parts[1]
}

func isLowerHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

// Scopes says which memories an identified agent may reach beyond its own.
// Without a scopes file, identities still sign the writes but reads are not
// restricted (mem7 as it was). With one, an agent reads its own memories
// plus the owners listed for it, and only administrators forget other
// agents' memories, forget by tags, or read the raw workspace.
//
//	{
//	  "read":  {"claude": ["*"], "supervisor": ["*"], "scout7": []},
//	  "admin": ["claude"]
//	}
type Scopes struct {
	Read  map[string][]string `json:"read"`
	Admin []string            `json:"admin"`
}

// LoadScopes reads a scopes file (JSON).
func LoadScopes(file string) (*Scopes, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read scopes: %w", err)
	}
	var s Scopes
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse scopes %s: %w", file, err)
	}
	for agent, owners := range s.Read {
		for _, o := range owners {
			if _, err := path.Match(o, ""); err != nil {
				return nil, fmt.Errorf("scopes: bad pattern %q for %s", o, agent)
			}
		}
	}
	return &s, nil
}

// canRead reports whether caller may read a memory written by owner.
func (s *Scopes) canRead(caller, owner string) bool {
	if caller == owner || s.isAdmin(caller) {
		return true
	}
	for _, pattern := range s.Read[caller] {
		if ok, _ := path.Match(pattern, owner); ok {
			return true
		}
	}
	return false
}

func (s *Scopes) isAdmin(caller string) bool {
	for _, a := range s.Admin {
		if a == caller {
			return true
		}
	}
	return false
}

// scoped reports whether this caller's reads and deletions are restricted.
func (s *Store) scoped(c Caller) bool {
	return s.scopes != nil && c.Agent != "" && !s.scopes.isAdmin(c.Agent)
}

// visible keeps the facts the caller may read.
func (s *Store) visible(facts []fact, c Caller) []fact {
	if !s.scoped(c) {
		return facts
	}
	out := facts[:0]
	for _, f := range facts {
		if s.scopes.canRead(c.Agent, f.Agent) {
			out = append(out, f)
		}
	}
	return out
}

// SetScopes turns read scoping on (nil turns it off).
func (s *Store) SetScopes(sc *Scopes) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.scopes = sc
}
