// Package mcp implements a Model Context Protocol server (JSON-RPC 2.0 over
// stdio) exposing the cleaning core as IDE-agent tools. This is the standard
// integration surface for AI IDEs and agents: Claude Code/Desktop, Cursor,
// Windsurf, Cline, Continue, Zed and any MCP client.
//
// Tools (descriptions localized to the client's initialize locale):
//
//	capabilities  — which optional tools / pixel backends are present
//	inspect_file  — inspect one local file (auto-routes text/image/container)
//	clean_file    — clean one local file (writes *.cleaned.* or in-place w/ .bak)
//	inspect_text  — inspect a text string for invisible Unicode steganography
//	clean_text    — clean a text string, return the cleaned text + stats
//
// Usage from a program:
//
//	srv := mcp.New("1.2.3")
//	srv.RunStdio()
package mcp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/i18n"
)

const protocolVersion = "2024-11-05"

// Server is an MCP server instance over the cleaning core.
type Server struct {
	version string
	locale  i18n.Tag
}

// New builds a Server reporting the given version.
func New(version string) *Server {
	return &Server{version: version, locale: i18n.Default}
}

// ---------------------------------------------------------------------------
// JSON-RPC plumbing
// ---------------------------------------------------------------------------

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Handle processes one JSON-RPC request and returns the response bytes
// (nil for notifications). Errors are returned inside the JSON-RPC error
// object, so a non-nil byte slice is always a valid response.
func (s *Server) Handle(raw []byte) []byte {
	var req rpcRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		resp, _ := json.Marshal(rpcResponse{
			JSONRPC: "2.0",
			ID:      json.RawMessage("null"),
			Error:   &rpcError{Code: -32700, Message: "parse error"},
		})
		return resp
	}

	// notifications get no response
	if len(req.ID) == 0 {
		if req.Method == "notifications/initialized" {
			return nil
		}
		return nil
	}

	result, rpcErr := s.dispatch(req.Method, req.Params)
	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	if rpcErr != nil {
		resp.Error = rpcErr
	} else {
		resp.Result = result
	}
	out, err := json.Marshal(resp)
	if err != nil {
		out, _ = json.Marshal(rpcResponse{
			JSONRPC: "2.0", ID: req.ID,
			Error: &rpcError{Code: -32603, Message: "internal error"},
		})
	}
	return out
}

func (s *Server) dispatch(method string, params json.RawMessage) (interface{}, *rpcError) {
	switch method {
	case "initialize":
		return s.handleInitialize(params)
	case "ping":
		return map[string]interface{}{}, nil
	case "tools/list":
		return s.handleToolsList(), nil
	case "tools/call":
		return s.handleToolsCall(params)
	case "resources/list":
		return map[string]interface{}{"resources": []interface{}{}}, nil
	case "prompts/list":
		return map[string]interface{}{"prompts": []interface{}{}}, nil
	}
	return nil, &rpcError{Code: -32601, Message: "method not found: " + method}
}

// ---------------------------------------------------------------------------
// protocol methods
// ---------------------------------------------------------------------------

type initializeParams struct {
	ProtocolVersion string                 `json:"protocolVersion"`
	Locale          string                 `json:"locale"`
	Capabilities    map[string]interface{} `json:"capabilities"`
	ClientInfo      map[string]interface{} `json:"clientInfo"`
}

func (s *Server) handleInitialize(params json.RawMessage) (interface{}, *rpcError) {
	var p initializeParams
	if len(params) > 0 {
		json.Unmarshal(params, &p)
	}
	if p.Locale != "" {
		s.locale = i18n.Normalize(p.Locale)
	}
	version := protocolVersion
	if p.ProtocolVersion != "" {
		version = p.ProtocolVersion
	}
	return map[string]interface{}{
		"protocolVersion": version,
		"capabilities": map[string]interface{}{
			"tools": map[string]interface{}{},
		},
		"serverInfo": map[string]interface{}{
			"name":    "antiaimark",
			"version": s.version,
		},
	}, nil
}

type toolDef struct {
	Name        string                 `json:"name"`
	Description string                 `json:"description"`
	InputSchema map[string]interface{} `json:"inputSchema"`
}

func (s *Server) handleToolsList() interface{} {
	t := func(key string) string { return i18n.TFor(s.locale, key) }
	obj := func(props map[string]interface{}, required ...string) map[string]interface{} {
		return map[string]interface{}{
			"type":       "object",
			"properties": props,
			"required":   strSlice(required),
		}
	}
	str := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "string", "description": desc}
	}
	boolean := func(desc string) map[string]interface{} {
		return map[string]interface{}{"type": "boolean", "description": desc}
	}

	tools := []toolDef{
		{
			Name:        "capabilities",
			Description: t("mcp.desc.capabilities"),
			InputSchema: obj(map[string]interface{}{}),
		},
		{
			Name:        "inspect_file",
			Description: t("mcp.desc.inspect_file"),
			InputSchema: obj(map[string]interface{}{
				"path":       str("absolute path of the file to inspect"),
				"aggressive": boolean("also flag Latin confusables when inspected as text"),
			}, "path"),
		},
		{
			Name:        "clean_file",
			Description: t("mcp.desc.clean_file"),
			InputSchema: obj(map[string]interface{}{
				"path":                 str("absolute path of the file to clean"),
				"output":               str("output path (default: *.cleaned.* next to the input)"),
				"in_place":             boolean("overwrite the input file (writes a .bak backup first)"),
				"keep_non_ai_metadata": boolean("images: only drop C2PA/AI-looking segments (less aggressive)"),
				"also_layer_a_text":    boolean("containers: also scrub invisible Unicode in HTML/Markdown (default true)"),
			}, "path"),
		},
		{
			Name:        "inspect_text",
			Description: t("mcp.desc.inspect_text"),
			InputSchema: obj(map[string]interface{}{
				"text":       str("the text to inspect"),
				"aggressive": boolean("also flag Latin confusables / fullwidth lookalikes"),
			}, "text"),
		},
		{
			Name:        "clean_text",
			Description: t("mcp.desc.clean_text"),
			InputSchema: obj(map[string]interface{}{
				"text":                  str("the text to clean"),
				"nfkc":                  boolean("apply Unicode NFKC after the scrub"),
				"aggressive_homoglyphs": boolean("map Cyrillic/fullwidth Latin confusables to ASCII"),
				"normalize_spaces":      boolean("rewrite exotic spaces to U+0020 (default true)"),
			}, "text"),
		},
	}
	return map[string]interface{}{"tools": tools}
}

func strSlice(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

type toolsCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

func (s *Server) handleToolsCall(params json.RawMessage) (interface{}, *rpcError) {
	var p toolsCallParams
	if len(params) > 0 {
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &rpcError{Code: -32602, Message: "invalid params"}
		}
	}
	argStr := func(k string) string { v, _ := p.Arguments[k].(string); return v }
	argBool := func(k string) bool { v, _ := p.Arguments[k].(bool); return v }

	var payload interface{}
	var err error
	switch p.Name {
	case "capabilities":
		payload = s.capabilities()
	case "inspect_file":
		payload, err = s.inspectFile(argStr("path"), argBool("aggressive"))
	case "clean_file":
		payload, err = s.cleanFile(argStr("path"), argStr("output"), argBool("in_place"), argBool("keep_non_ai_metadata"), argBool("also_layer_a_text"))
	case "inspect_text":
		payload, err = s.inspectText(argStr("text"), argBool("aggressive"))
	case "clean_text":
		payload, err = s.cleanText(argStr("text"), argBool("nfkc"), argBool("aggressive_homoglyphs"), argBool("normalize_spaces"))
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}

	if err != nil {
		return map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": err.Error()}},
			"isError": true,
		}, nil
	}
	data, mErr := cleaning.MarshalIndentNoEscape(payload)
	if mErr != nil {
		return nil, &rpcError{Code: -32603, Message: "internal error"}
	}
	return map[string]interface{}{
		"content": []map[string]interface{}{{"type": "text", "text": string(data)}},
	}, nil
}

// ---------------------------------------------------------------------------
// tool implementations
// ---------------------------------------------------------------------------

func (s *Server) capabilities() map[string]interface{} {
	return map[string]interface{}{
		"version": s.version,
		"tools": map[string]interface{}{
			"c2patool": cleaning.Which("c2patool") != "",
			"exiftool": cleaning.Which("exiftool") != "",
			"qpdf":     cleaning.Which("qpdf") != "",
		},
		"pixel_backends": map[string]interface{}{
			"ctrlregen": os.Getenv("NOAI_WATERMARK_DIR") != "",
			"diffusion": os.Getenv("MARKDIFFUSION_DIR") != "",
		},
		"scorers":   map[string]interface{}{"synthid": os.Getenv("REVERSE_SYNTHID_DIR") != ""},
		"harnesses": map[string]interface{}{"markllm": os.Getenv("MARKLLM_DIR") != ""},
		"languages": []string{"en", "zh", "es", "fr", "ru"},
	}
}

func (s *Server) inspectFile(path string, aggressive bool) (map[string]interface{}, error) {
	if path == "" {
		return nil, errors.New(i18n.TFor(s.locale, "mcp.err.path", "required"))
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return nil, fmt.Errorf("%s", i18n.TFor(s.locale, "mcp.err.not_file", path))
	}
	if fi.Size() > int64(cleaning.MaxInputBytes) {
		return nil, errors.New(i18n.TFor(s.locale, "cli.over_cap_file", cleaning.MaxInputBytes, path))
	}
	kind, err := cleaning.Classify(path)
	if err != nil {
		return nil, err
	}
	switch kind {
	case cleaning.KindImage:
		report, err := cleaning.InspectImage(path, "")
		if err != nil {
			return nil, err
		}
		return report.ToDict(), nil
	case cleaning.KindContainer:
		report, err := cleaning.InspectContainer(path)
		if err != nil {
			return nil, err
		}
		return report.ToDict(), nil
	default:
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := cleaning.GuardBinary(data, path, false, cleaning.RouterAdvice); err != nil {
			return nil, err
		}
		report := cleaning.InspectText(string(data), aggressive, false)
		return report.ToDict(), nil
	}
}

func (s *Server) cleanFile(path, output string, inPlace, keepNonAI, alsoLayerA bool) (map[string]interface{}, error) {
	if path == "" {
		return nil, errors.New(i18n.TFor(s.locale, "mcp.err.path", "required"))
	}
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		return nil, fmt.Errorf("%s", i18n.TFor(s.locale, "mcp.err.not_file", path))
	}
	if fi.Size() > int64(cleaning.MaxInputBytes) {
		return nil, errors.New(i18n.TFor(s.locale, "cli.over_cap_file", cleaning.MaxInputBytes, path))
	}

	src := path
	dest := output
	if inPlace {
		if _, err := cleaning.BackupPath(path); err != nil {
			return nil, err
		}
		src = path + ".bak"
		dest = path
	} else if dest == "" {
		dest = cleaning.CleanedPath(path, ".cleaned")
	}

	kind, err := cleaning.Classify(src)
	if err != nil {
		return nil, err
	}
	switch kind {
	case cleaning.KindImage:
		result, err := cleaning.CleanImage(src, dest, cleaning.CleanImageOptions{
			StripAllMetadata: cleaning.BoolPtr(!keepNonAI),
		})
		if err != nil {
			return nil, err
		}
		result["kind"] = "image"
		return result, nil
	case cleaning.KindContainer:
		result, err := cleaning.CleanContainer(src, dest, cleaning.CleanContainerOptions{
			SkipLayerAText: !alsoLayerA,
		})
		if err != nil {
			return nil, err
		}
		result["kind"] = "container"
		return result, nil
	default:
		data, err := os.ReadFile(src)
		if err != nil {
			return nil, err
		}
		if err := cleaning.GuardBinary(data, path, false, cleaning.RouterAdvice); err != nil {
			return nil, err
		}
		res := cleaning.CleanText(string(data), false, false, true, false)
		if err := cleaning.SafeWriteText(dest, res.Text); err != nil {
			return nil, err
		}
		return map[string]interface{}{
			"kind":   "text",
			"input":  path,
			"output": dest,
			"stats":  res.Stats,
		}, nil
	}
}

func (s *Server) inspectText(text string, aggressive bool) (map[string]interface{}, error) {
	if text == "" {
		return nil, errors.New(i18n.TFor(s.locale, "mcp.err.text", "required"))
	}
	report := cleaning.InspectText(text, aggressive, false)
	return report.ToDict(), nil
}

func (s *Server) cleanText(text string, nfkc, aggressive, normalizeSpaces bool) (map[string]interface{}, error) {
	if text == "" {
		return nil, errors.New(i18n.TFor(s.locale, "mcp.err.text", "required"))
	}
	res := cleaning.CleanText(text, nfkc, aggressive, normalizeSpaces, false)
	return map[string]interface{}{
		"cleaned": res.Text,
		"stats":   res.Stats,
	}, nil
}

// ---------------------------------------------------------------------------
// stdio transport
// ---------------------------------------------------------------------------

// RunStdio runs the newline-delimited JSON-RPC loop over stdin/stdout until
// EOF. Logs go to stderr only (stdout is the protocol channel).
func (s *Server) RunStdio() error {
	return s.Run(os.Stdin, os.Stdout)
}

// Run is RunStdio over explicit streams (used by tests and embedders).
func (s *Server) Run(reader io.Reader, writer io.Writer) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 1<<20), 64<<20)
	w := bufio.NewWriter(writer)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		resp := s.Handle(line)
		if resp == nil {
			continue
		}
		w.Write(resp)
		w.WriteByte('\n')
		if err := w.Flush(); err != nil {
			return err
		}
	}
	return scanner.Err()
}

var _ = filepath.Join
