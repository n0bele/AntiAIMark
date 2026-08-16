// Streamable HTTP transport for the MCP server (MCP specification
// revision 2025-03-26, https://modelcontextprotocol.io/).
//
// This lets any MCP client that supports remote servers — Cursor, Windsurf,
// Claude Desktop, Cline, Zed, … — reach antiaimark over the network: run
// antiaimark-server and register the /mcp endpoint as the server URL, e.g.
// in Cursor / Windsurf / Cline mcp.json:
//
//	{ "mcpServers": { "antiaimark": { "type": "http",
//	    "url": "http://127.0.0.1:8765/mcp" } } }
//
// It reuses the same JSON-RPC dispatch as the stdio transport. Each MCP
// session gets its own Server instance (which carries the locale negotiated
// at initialize), so sessions never share mutable state.
//
// Transport rules implemented here:
//
//	POST   /mcp  JSON-RPC request. A session is created on initialize and
//	             the Mcp-Session-Id header is returned; it must be echoed on
//	             all later requests. Notifications get HTTP 202 with an empty
//	             body (JSON mode) or an empty SSE stream.
//	GET    /mcp  server->client SSE stream (keep-alive comments; this server
//	             never pushes messages on its own).
//	DELETE /mcp  terminate the session.
package mcp

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// maxHTTPBody caps a single JSON-RPC request body, matching the 64 MiB
	// buffer used by the stdio transport.
	maxHTTPBody = 64 << 20
	// sessionTTL is how long a session stays valid without activity.
	sessionTTL = 30 * time.Minute
	// keepAliveInterval is how often the GET stream emits an SSE comment.
	keepAliveInterval = 15 * time.Second
)

// HTTPServer serves the MCP protocol over Streamable HTTP.
type HTTPServer struct {
	version  string
	mu       sync.Mutex
	sessions map[string]*httpSession
}

type httpSession struct {
	srv    *Server
	expiry time.Time
}

// NewHTTPServer builds a handler exposing the same tools as the stdio server
// over Streamable HTTP.
func NewHTTPServer(version string) *HTTPServer {
	return &HTTPServer{version: version, sessions: make(map[string]*httpSession)}
}

// ServeHTTP implements the Streamable HTTP transport (see package comment).
func (h *HTTPServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		h.handlePost(w, r)
	case http.MethodGet:
		h.handleGet(w, r)
	case http.MethodDelete:
		h.handleDelete(w, r)
	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// ---------------------------------------------------------------------------
// POST /mcp
// ---------------------------------------------------------------------------

func (h *HTTPServer) handlePost(w http.ResponseWriter, r *http.Request) {
	acceptJSON := accepts(r.Header.Get("Accept"), "application/json")
	acceptSSE := accepts(r.Header.Get("Accept"), "text/event-stream")
	if !acceptJSON && !acceptSSE {
		http.Error(w, "client must accept application/json or text/event-stream", http.StatusNotAcceptable)
		return
	}

	raw, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxHTTPBody))
	if err != nil {
		http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
		return
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		http.Error(w, "empty request body", http.StatusBadRequest)
		return
	}

	// The method is needed before dispatch for session handling.
	var probe struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(raw, &probe)

	sessionID := r.Header.Get("Mcp-Session-Id")
	srv, sid, _ := h.getSession(probe.Method, sessionID)
	if srv == nil {
		if sessionID == "" {
			http.Error(w, "missing Mcp-Session-Id header", http.StatusBadRequest)
		} else {
			http.Error(w, "unknown Mcp-Session-Id", http.StatusNotFound)
		}
		return
	}

	resp := srv.Handle(raw)

	if sid != "" {
		w.Header().Set("Mcp-Session-Id", sid)
	}
	w.Header().Set("MCP-Protocol-Version", protocolVersion)

	if acceptJSON {
		if resp == nil { // notification
			w.WriteHeader(http.StatusAccepted)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(resp)))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusOK)
		w.Write(resp)
		return
	}

	// SSE mode: a single response is delivered as one "message" event;
	// notifications end an empty stream.
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	if resp != nil {
		fmt.Fprintf(w, "event: message\ndata: %s\n\n", resp)
	}
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}

// getSession resolves the Server for a session, creating one when the request
// is an initialize. It returns the effective session id and whether the
// session already existed. For non-initialize requests with an unknown or
// missing id it returns a nil Server.
func (h *HTTPServer) getSession(method, id string) (srv *Server, sessionID string, existed bool) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if id != "" {
		if s, ok := h.sessions[id]; ok {
			s.expiry = time.Now().Add(sessionTTL)
			return s.srv, id, true
		}
	}
	if method != "initialize" {
		return nil, "", false
	}
	if id == "" {
		id = newSessionID()
	}
	h.sweepLocked()
	s := &httpSession{srv: New(h.version), expiry: time.Now().Add(sessionTTL)}
	h.sessions[id] = s
	return s.srv, id, false
}

// sweepLocked evicts expired sessions so the map stays bounded. Callers must
// hold h.mu.
func (h *HTTPServer) sweepLocked() {
	now := time.Now()
	for id, s := range h.sessions {
		if now.After(s.expiry) {
			delete(h.sessions, id)
		}
	}
}

// ---------------------------------------------------------------------------
// GET /mcp (server->client SSE stream)
// ---------------------------------------------------------------------------

func (h *HTTPServer) handleGet(w http.ResponseWriter, r *http.Request) {
	// EventSource cannot set headers, so the session may come as a query
	// parameter too.
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		sessionID = r.URL.Query().Get("sessionId")
	}
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	s, ok := h.sessions[sessionID]
	if ok {
		s.expiry = time.Now().Add(sessionTTL)
	}
	h.mu.Unlock()
	if !ok {
		http.Error(w, "unknown Mcp-Session-Id", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)

	// This server never pushes messages of its own; the stream just stays
	// open with keep-alive comments until the client disconnects.
	ticker := time.NewTicker(keepAliveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			io.WriteString(w, ": keep-alive\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// ---------------------------------------------------------------------------
// DELETE /mcp
// ---------------------------------------------------------------------------

func (h *HTTPServer) handleDelete(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get("Mcp-Session-Id")
	if sessionID == "" {
		http.Error(w, "missing Mcp-Session-Id", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	delete(h.sessions, sessionID)
	h.mu.Unlock()
	w.WriteHeader(http.StatusOK)
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func newSessionID() string {
	b := make([]byte, 16)
	rand.Read(b) // best-effort; crypto/rand never errors on modern platforms
	return hex.EncodeToString(b)
}

// accepts reports whether the Accept header allows the given media type.
// A missing header and "*/*" are treated as accepting application/json.
func accepts(header, want string) bool {
	if strings.TrimSpace(header) == "" {
		return want == "application/json"
	}
	for _, part := range strings.Split(header, ",") {
		mime := strings.TrimSpace(strings.SplitN(part, ";", 2)[0])
		if mime == "" {
			continue
		}
		if mime == want || mime == "*/*" {
			return true
		}
	}
	return false
}
