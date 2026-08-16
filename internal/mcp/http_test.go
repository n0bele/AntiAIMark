package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func httpRequest(t *testing.T, h *HTTPServer, method, sessionID, accept, body string) *httptest.ResponseRecorder {
	t.Helper()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, "/mcp", nil)
	} else {
		req = httptest.NewRequest(method, "/mcp", strings.NewReader(body))
	}
	if sessionID != "" {
		req.Header.Set("Mcp-Session-Id", sessionID)
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func initializeOverHTTP(t *testing.T, h *HTTPServer) string {
	t.Helper()
	rr := httpRequest(t, h, http.MethodPost, "", "application/json",
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"locale":"zh-CN"}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("initialize status = %d, body = %s", rr.Code, rr.Body.String())
	}
	sid := rr.Header().Get("Mcp-Session-Id")
	if sid == "" {
		t.Fatal("initialize did not return Mcp-Session-Id")
	}
	return sid
}

func TestHTTPInitializeCreatesSession(t *testing.T) {
	h := NewHTTPServer("1.0")
	sid := initializeOverHTTP(t, h)
	if len(sid) != 32 {
		t.Fatalf("session id length = %d", len(sid))
	}
	if v := h.sessions[sid]; v == nil {
		t.Fatal("session not stored")
	}
}

func TestHTTPToolsListWithSession(t *testing.T) {
	h := NewHTTPServer("1.0")
	sid := initializeOverHTTP(t, h)
	rr := httpRequest(t, h, http.MethodPost, sid, "application/json",
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("tools/list status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `"name":"capabilities"`) {
		t.Fatalf("tools/list body = %s", rr.Body.String())
	}
}

func TestHTTPMissingSessionRejected(t *testing.T) {
	h := NewHTTPServer("1.0")
	rr := httpRequest(t, h, http.MethodPost, "", "application/json",
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHTTPUnknownSessionRejected(t *testing.T) {
	h := NewHTTPServer("1.0")
	rr := httpRequest(t, h, http.MethodPost, "deadbeef", "application/json",
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestHTTPNotificationAccepted(t *testing.T) {
	h := NewHTTPServer("1.0")
	sid := initializeOverHTTP(t, h)
	rr := httpRequest(t, h, http.MethodPost, sid, "application/json",
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202", rr.Code)
	}
	if rr.Body.Len() != 0 {
		t.Fatalf("notification body = %q, want empty", rr.Body.String())
	}
}

func TestHTTPSingleResponseAsSSE(t *testing.T) {
	h := NewHTTPServer("1.0")
	sid := initializeOverHTTP(t, h)
	rr := httpRequest(t, h, http.MethodPost, sid, "text/event-stream",
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	body := rr.Body.String()
	if !strings.Contains(body, "event: message") || !strings.Contains(body, `"name":"capabilities"`) {
		t.Fatalf("SSE body = %s", body)
	}
}

func TestHTTPToolsCall(t *testing.T) {
	h := NewHTTPServer("1.0")
	sid := initializeOverHTTP(t, h)
	rr := httpRequest(t, h, http.MethodPost, sid, "application/json",
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"clean_text","arguments":{"text":"a\u200bb"}}}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad response: %v", err)
	}
	if len(out.Result.Content) == 0 {
		t.Fatalf("no content in %s", rr.Body.String())
	}
	var payload struct {
		Cleaned string `json:"cleaned"`
	}
	json.Unmarshal([]byte(out.Result.Content[0].Text), &payload)
	if payload.Cleaned != "ab" {
		t.Fatalf("cleaned = %q", payload.Cleaned)
	}
}

func TestHTTPDeleteTerminatesSession(t *testing.T) {
	h := NewHTTPServer("1.0")
	sid := initializeOverHTTP(t, h)
	rr := httpRequest(t, h, http.MethodDelete, sid, "", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rr.Code)
	}
	if _, ok := h.sessions[sid]; ok {
		t.Fatal("session survived DELETE")
	}
	rr = httpRequest(t, h, http.MethodPost, sid, "application/json",
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status after delete = %d, want 404", rr.Code)
	}
}

func TestHTTPGetSSEStream(t *testing.T) {
	h := NewHTTPServer("1.0")
	sid := initializeOverHTTP(t, h)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil).WithContext(ctx)
	req.Header.Set("Mcp-Session-Id", sid)
	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rr, req)
		close(done)
	}()
	time.Sleep(100 * time.Millisecond) // let the handler reach the loop
	cancel()
	<-done

	if rr.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestHTTPGetSSEWithoutSession(t *testing.T) {
	h := NewHTTPServer("1.0")
	rr := httpRequest(t, h, http.MethodGet, "", "", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHTTPNotAcceptable(t *testing.T) {
	h := NewHTTPServer("1.0")
	rr := httpRequest(t, h, http.MethodPost, "", "application/xml",
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`)
	if rr.Code != http.StatusNotAcceptable {
		t.Fatalf("status = %d, want 406", rr.Code)
	}
}
