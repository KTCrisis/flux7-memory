package transport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/KTCrisis/flux7-memory/internal/memory"
)

// handleMCP serves the MCP Streamable HTTP transport (spec 2025-03-26 and
// later) on one endpoint, /mcp. It replaces the HTTP+SSE pair (/sse and
// /messages), which the 2026-07-28 spec lists as deprecated.
//
// mem7 keeps no protocol session: each request is answered on its own POST,
// as plain JSON, and no Mcp-Session-Id is issued, which the spec allows.
// A notification (no id) is acknowledged with 202 and no body. GET, the
// optional server-initiated stream, is not offered: mem7 never speaks first.
func (s *HTTPServer) handleMCP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
	case http.MethodGet, http.MethodDelete:
		w.Header().Set("Allow", "POST")
		http.Error(w, "mem7 serves no stream and keeps no session: POST only", http.StatusMethodNotAllowed)
		return
	default:
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<20))
	if err != nil {
		s.writeRPCError(w, nil, -32700, "parse error: "+err.Error())
		return
	}
	req, err := parseRPC(body)
	if err != nil {
		s.writeRPCError(w, nil, -32700, "parse error: "+err.Error())
		return
	}
	if req.JSONRPC != "2.0" {
		s.writeRPCError(w, req.ID, -32600, "invalid request: jsonrpc must be 2.0")
		return
	}
	if len(req.ID) == 0 {
		// A notification expects no response. Unknown notifications
		// (notifications/initialized, cancellations) are accepted as-is.
		w.WriteHeader(http.StatusAccepted)
		return
	}

	result, err := s.transport.Call(r.Context(), req.Method, req.Params)
	if err != nil {
		code, msg := -32603, err.Error()
		var rerr *memory.RPCError
		if errors.As(err, &rerr) {
			code, msg = rerr.Code, rerr.Message
		}
		s.writeRPCError(w, req.ID, code, msg)
		return
	}
	s.writeRPCResult(w, req.ID, result)
}

func parseRPC(body []byte) (rpcRequest, error) {
	var req rpcRequest
	err := json.Unmarshal(body, &req)
	return req, err
}
