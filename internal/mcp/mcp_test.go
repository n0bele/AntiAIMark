package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func call(t *testing.T, s *Server, req map[string]interface{}) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	resp := s.Handle(raw)
	if resp == nil {
		t.Fatal("nil response for a request with id")
	}
	var out map[string]interface{}
	if err := json.Unmarshal(resp, &out); err != nil {
		t.Fatalf("bad response %s: %v", resp, err)
	}
	return out
}

func TestInitializeAndLocale(t *testing.T) {
	s := New("1.0")
	out := call(t, s, map[string]interface{}{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
		"params": map[string]interface{}{"locale": "zh-CN"},
	})
	if out["error"] != nil {
		t.Fatalf("initialize error: %v", out["error"])
	}
	result := out["result"].(map[string]interface{})
	info := result["serverInfo"].(map[string]interface{})
	if info["name"] != "antiaimark" || info["version"] != "1.0" {
		t.Fatalf("serverInfo = %v", info)
	}
	// locale must localize tool descriptions
	list := call(t, s, map[string]interface{}{"jsonrpc": "2.0", "id": 2, "method": "tools/list"})
	tools := list["result"].(map[string]interface{})["tools"].([]interface{})
	first := tools[0].(map[string]interface{})
	if !strings.Contains(first["description"].(string), "exiftool") {
		t.Fatalf("unexpected zh description: %v", first["description"])
	}
}

func TestNotificationNoResponse(t *testing.T) {
	s := New("1.0")
	raw, _ := json.Marshal(map[string]interface{}{"jsonrpc": "2.0", "method": "notifications/initialized"})
	if resp := s.Handle(raw); resp != nil {
		t.Fatalf("notification produced a response: %s", resp)
	}
}

func TestToolsCallCleanText(t *testing.T) {
	s := New("1.0")
	call(t, s, map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]interface{}{}})
	out := call(t, s, map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]interface{}{
			"name":      "clean_text",
			"arguments": map[string]interface{}{"text": "a\u200bb"},
		},
	})
	if out["error"] != nil {
		t.Fatalf("tools/call error: %v", out["error"])
	}
	result := out["result"].(map[string]interface{})
	content := result["content"].([]interface{})[0].(map[string]interface{})
	var payload struct {
		Cleaned string `json:"cleaned"`
	}
	json.Unmarshal([]byte(content["text"].(string)), &payload)
	if payload.Cleaned != "ab" {
		t.Fatalf("cleaned = %q", payload.Cleaned)
	}
}

func TestToolsCallInspectFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "x.md")
	os.WriteFile(path, []byte("---\ngenerator: OpenAI\n---\nbody"), 0o644)

	s := New("1.0")
	call(t, s, map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]interface{}{}})
	out := call(t, s, map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]interface{}{
			"name":      "inspect_file",
			"arguments": map[string]interface{}{"path": path},
		},
	})
	result := out["result"].(map[string]interface{})
	if isError, _ := result["isError"].(bool); isError {
		t.Fatalf("inspect_file failed: %v", result)
	}
	var payload struct {
		Format   string   `json:"format"`
		HasAI    bool     `json:"has_ai_metadata"`
		Findings []string `json:"findings"`
	}
	content := result["content"].([]interface{})[0].(map[string]interface{})
	json.Unmarshal([]byte(content["text"].(string)), &payload)
	if payload.Format != "markdown" || !payload.HasAI {
		t.Fatalf("report = %+v", payload)
	}
}

func TestToolsCallErrorPath(t *testing.T) {
	s := New("1.0")
	call(t, s, map[string]interface{}{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]interface{}{}})
	out := call(t, s, map[string]interface{}{
		"jsonrpc": "2.0", "id": 2, "method": "tools/call",
		"params": map[string]interface{}{"name": "inspect_file", "arguments": map[string]interface{}{"path": "C:\\no\\such\\file.png"}},
	})
	result := out["result"].(map[string]interface{})
	if isError, _ := result["isError"].(bool); !isError {
		t.Fatalf("expected isError, got %v", result)
	}
}

func TestRunStdioLoop(t *testing.T) {
	s := New("1.0")
	var in bytes.Buffer
	in.WriteString(`{"jsonrpc":"2.0","id":1,"method":"ping"}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n")
	in.WriteString(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}` + "\n")
	var out bytes.Buffer
	if err := s.Run(&in, &out); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d: %q", len(lines), out.String())
	}
	if !strings.Contains(lines[0], `"ping"`) && !strings.Contains(lines[0], `"result"`) {
		t.Fatalf("ping response: %s", lines[0])
	}
}
