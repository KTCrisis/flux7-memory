package transport

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/KTCrisis/flux7-memory/internal/memory"
)

type sseSession struct {
	id     string
	events chan []byte
	done   chan struct{}
}

type sseRegistry struct {
	mu       sync.RWMutex
	sessions map[string]*sseSession
}

func newSSERegistry() *sseRegistry {
	return &sseRegistry{sessions: make(map[string]*sseSession)}
}

func (r *sseRegistry) create() *sseSession {
	b := make([]byte, 16)
	rand.Read(b)
	s := &sseSession{
		id:     hex.EncodeToString(b),
		events: make(chan []byte, 64),
		done:   make(chan struct{}),
	}
	r.mu.Lock()
	r.sessions[s.id] = s
	r.mu.Unlock()
	return s
}

func (r *sseRegistry) get(id string) *sseSession {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sessions[id]
}

func (r *sseRegistry) remove(id string) {
	r.mu.Lock()
	delete(r.sessions, id)
	r.mu.Unlock()
}

func (s *HTTPServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	session := s.sse.create()
	defer func() {
		close(session.done)
		s.sse.remove(session.id)
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	fmt.Fprintf(w, "event: endpoint\ndata: /messages?sessionId=%s\n\n", session.id)
	flusher.Flush()

	s.logger.Printf("SSE session %s opened", session.id)

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case data := <-session.events:
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			s.logger.Printf("SSE session %s closed", session.id)
			return
		}
	}
}

func (s *HTTPServer) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := r.URL.Query().Get("sessionId")
	session := s.sse.get(sessionID)
	if session == nil {
		http.Error(w, "unknown session", http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		s.sseRPCError(session, nil, -32700, "read error")
		w.WriteHeader(http.StatusAccepted)
		return
	}

	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.sseRPCError(session, nil, -32700, "parse error: "+err.Error())
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if req.JSONRPC != "2.0" {
		s.sseRPCError(session, req.ID, -32600, "invalid request: jsonrpc must be 2.0")
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, err := s.transport.Call(r.Context(), req.Method, req.Params)
	if err != nil {
		code := -32603
		msg := err.Error()
		var rerr *memory.RPCError
		if errors.As(err, &rerr) {
			code = rerr.Code
			msg = rerr.Message
		}
		s.sseRPCError(session, req.ID, code, msg)
		w.WriteHeader(http.StatusAccepted)
		return
	}

	s.sseRPCResult(session, req.ID, result)
	w.WriteHeader(http.StatusAccepted)
}

func (s *HTTPServer) sseRPCResult(session *sseSession, id json.RawMessage, result json.RawMessage) {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
	data, _ := json.Marshal(resp)
	select {
	case session.events <- data:
	case <-session.done:
	}
}

func (s *HTTPServer) sseRPCError(session *sseSession, id json.RawMessage, code int, msg string) {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
	data, _ := json.Marshal(resp)
	select {
	case session.events <- data:
	case <-session.done:
	}
}
