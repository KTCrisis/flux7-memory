package transport

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/KTCrisis/flux7-memory/internal/memory"
)

// HTTPServer exposes a Transport over HTTP using JSON-RPC 2.0 on /rpc.
// Bearer token authentication guards /rpc when a non-empty token is
// configured ; /healthz is always public.
type HTTPServer struct {
	transport Transport
	token     string
	logger    *log.Logger
	sse       *sseRegistry
}

// NewHTTPServer wraps a Transport behind an HTTP server. An empty token
// disables authentication (intended for local smoke tests only).
func NewHTTPServer(t Transport, token string, logger *log.Logger) *HTTPServer {
	if logger == nil {
		logger = log.Default()
	}
	return &HTTPServer{transport: t, token: token, logger: logger, sse: newSSERegistry()}
}

// Handler returns the http.Handler exposing the server routes.
func (s *HTTPServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.Handle("/rpc", s.authMiddleware(http.HandlerFunc(s.handleRPC)))
	mux.Handle("/memory/snapshot_reminder", s.authMiddleware(http.HandlerFunc(s.handleSnapshotReminder)))
	mux.Handle("/sse", s.authMiddleware(http.HandlerFunc(s.handleSSE)))
	mux.Handle("/messages", s.authMiddleware(http.HandlerFunc(s.handleMessages)))
	return mux
}

// ListenAndServe binds the handler to addr and blocks until ctx is
// cancelled or the underlying server fails. It performs a graceful
// shutdown on cancellation.
func (s *HTTPServer) ListenAndServe(ctx context.Context, addr string) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *HTTPServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.token != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if got != s.token {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *HTTPServer) handleRPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		s.writeRPCError(w, nil, -32700, "parse error: "+err.Error())
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(body, &req); err != nil {
		s.writeRPCError(w, nil, -32700, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeRPCError(w, req.ID, -32600, "invalid request: jsonrpc must be 2.0")
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
		s.writeRPCError(w, req.ID, code, msg)
		return
	}
	s.writeRPCResult(w, req.ID, result)
}

// handleSnapshotReminder is a convenience route that returns the
// snapshot_reminder payload as plain JSON instead of wrapping it in a
// JSON-RPC envelope. It accepts POST so agent runtimes can signal the
// call explicitly even though the request body is currently ignored.
func (s *HTTPServer) handleSnapshotReminder(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	result, err := s.transport.Call(r.Context(), "memory/snapshot_reminder", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(result)
}

func (s *HTTPServer) writeRPCResult(w http.ResponseWriter, id json.RawMessage, result json.RawMessage) {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Result: result}
	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(resp)
	_, _ = w.Write(data)
}

func (s *HTTPServer) writeRPCError(w http.ResponseWriter, id json.RawMessage, code int, msg string) {
	resp := rpcResponse{JSONRPC: "2.0", ID: id, Error: &rpcError{Code: code, Message: msg}}
	w.Header().Set("Content-Type", "application/json")
	data, _ := json.Marshal(resp)
	_, _ = w.Write(data)
}
