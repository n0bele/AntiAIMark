// Package httpapi is the embeddable HTTP facade over the cleaning core: the
// Python-compatible JSON API (/health, /capabilities, /openapi.json,
// /inspect, /clean), the web extension (/api/upload, /api/download/{token},
// the embedded web UI at /), and the i18n catalog endpoint (/api/i18n).
//
// Import it from any Go program — an IDE plugin, a desktop app, a test — and
// serve it on any mux:
//
//	api := httpapi.New(httpapi.Options{Version: "1.0"})
//	http.ListenAndServe("127.0.0.1:8765", api.Handler())
//
// Error messages are localized via the Accept-Language header; JSON report
// payloads stay in English for cross-tool parity.
package httpapi

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"watermarks-remover/internal/cleaning"
	"watermarks-remover/internal/i18n"
)

//go:embed static
var staticFS embed.FS

// Options configures an API instance.
type Options struct {
	Version string // reported by /health and /capabilities
	APIKey  string // non-empty requires "Authorization: Bearer <key>"
	MaxBody int    // JSON envelope cap; 0 = default (1.5x MaxInputBytes)
}

// API is a configured, ready-to-serve HTTP facade.
type API struct {
	opts         Options
	maxBodyBytes int
	downloads    *downloadStore
}

// New builds an API. The zero Version is reported as "dev".
func New(opts Options) *API {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	maxBody := opts.MaxBody
	if maxBody == 0 {
		maxBody = cleaning.MaxInputBytes + (cleaning.MaxInputBytes >> 1)
	}
	return &API{opts: opts, maxBodyBytes: maxBody, downloads: newDownloadStore()}
}

// Handler returns the fully-routed http.Handler.
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", a.auth(a.handleHealth))
	mux.HandleFunc("/capabilities", a.auth(a.handleCapabilities))
	mux.HandleFunc("/openapi.json", a.auth(a.handleOpenAPI))
	mux.HandleFunc("/api/i18n", a.handleI18n) // public: the UI needs it pre-auth too
	mux.HandleFunc("/inspect", a.requirePost(a.auth(a.handleInspectRoute)))
	mux.HandleFunc("/clean", a.requirePost(a.auth(a.handleCleanRoute)))
	mux.HandleFunc("/api/upload", a.requirePost(a.auth(a.handleUpload)))
	mux.HandleFunc("/api/download/", a.auth(a.handleDownload))
	mux.HandleFunc("/", a.serveIndex)
	return mux
}

// ---------------------------------------------------------------------------
// plumbing
// ---------------------------------------------------------------------------

func (a *API) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.opts.APIKey != "" && r.Header.Get("Authorization") != "Bearer "+a.opts.APIKey {
			respond(w, r, http.StatusUnauthorized, map[string]interface{}{"ok": false, "error": i18n.TFor(rlocale(r), "server.unauthorized")})
			return
		}
		next(w, r)
	}
}

func (a *API) requirePost(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			respond(w, r, http.StatusNotFound, map[string]interface{}{"ok": false, "error": i18n.TFor(rlocale(r), "server.not_found")})
			return
		}
		next(w, r)
	}
}

// rlocale resolves the request locale from Accept-Language.
func rlocale(r *http.Request) i18n.Tag {
	return i18n.NegotiateAcceptLanguage(r.Header.Get("Accept-Language"))
}

func jsonOK(payload map[string]interface{}) ([]byte, error) {
	return cleaning.MarshalIndentNoEscape(payload)
}

func respond(w http.ResponseWriter, r *http.Request, status int, payload map[string]interface{}) {
	data, err := jsonOK(payload)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	w.Write(data)
}

func respondErr(w http.ResponseWriter, r *http.Request, status int, key string, args ...interface{}) {
	respond(w, r, status, map[string]interface{}{"ok": false, "error": i18n.TFor(rlocale(r), key, args...)})
}

func nameOrInput(name string) string {
	if name == "" {
		return "input"
	}
	return name
}

func toBool(v interface{}) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		return t == "true" || t == "1"
	case int:
		return t != 0 // Python truthiness for ints
	case int64:
		return t != 0
	case float64:
		return t != 0
	}
	return false
}

func toBoolDefault(v interface{}, def bool) bool {
	if v == nil {
		return def
	}
	return toBool(v)
}

func utf8RuneCount(s string) int { return len([]rune(s)) }

// ---------------------------------------------------------------------------
// endpoints
// ---------------------------------------------------------------------------

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	respond(w, r, http.StatusOK, map[string]interface{}{"ok": true, "version": a.opts.Version})
}

func (a *API) handleCapabilities(w http.ResponseWriter, r *http.Request) {
	payload := capabilities(a.opts.Version)
	payload["ok"] = true
	respond(w, r, http.StatusOK, payload)
}

func capabilities(version string) map[string]interface{} {
	return map[string]interface{}{
		"version": version,
		"tools": map[string]interface{}{
			"c2patool": cleaning.Which("c2patool") != "",
			"exiftool": cleaning.Which("exiftool") != "",
			"qpdf":     cleaning.Which("qpdf") != "",
		},
		"pixel_backends": map[string]interface{}{
			"ctrlregen": os.Getenv("NOAI_WATERMARK_DIR") != "",
			"diffusion": os.Getenv("MARKDIFFUSION_DIR") != "",
		},
		"scorers": map[string]interface{}{
			"synthid": os.Getenv("REVERSE_SYNTHID_DIR") != "",
		},
		"harnesses": map[string]interface{}{
			"markllm": os.Getenv("MARKLLM_DIR") != "",
		},
	}
}

// handleI18n serves the web-UI message catalog: GET /api/i18n?lang=zh returns
// {"lang":"zh","languages":[...],"messages":{...}}.
func (a *API) handleI18n(w http.ResponseWriter, r *http.Request) {
	tag := i18n.NegotiateAcceptLanguage(r.URL.Query().Get("lang"))
	if r.URL.Query().Get("lang") != "" {
		tag = i18n.Normalize(r.URL.Query().Get("lang"))
	}
	if tag == i18n.Default && r.URL.Query().Get("lang") != "" && !strings.HasPrefix(strings.ToLower(r.URL.Query().Get("lang")), "en") {
		tag = i18n.Default
	}
	languages := make([]map[string]interface{}, 0, len(i18n.Tags))
	for _, t := range i18n.Tags {
		languages = append(languages, map[string]interface{}{"tag": string(t), "name": i18n.Names[t]})
	}
	respond(w, r, http.StatusOK, map[string]interface{}{
		"lang":      string(tag),
		"languages": languages,
		"messages":  i18n.Catalog(tag),
	})
}

func (a *API) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	respond(w, r, http.StatusOK, openapiSpec(a.opts.Version, a.opts.APIKey))
}

// readJSONBody mirrors the Python Content-Length pre-check (413 before
// anything is read), then decodes a capped JSON object body.
func (a *API) readJSONBody(r *http.Request) (body map[string]interface{}, ok, oversized bool) {
	if raw := r.Header.Get("Content-Length"); raw != "" {
		if length, err := strconv.Atoi(raw); err == nil && length > a.maxBodyBytes {
			return nil, false, true
		}
	}
	r.Body = http.MaxBytesReader(nil, r.Body, int64(a.maxBodyBytes))
	dec := json.NewDecoder(r.Body)
	var m map[string]interface{}
	if err := dec.Decode(&m); err != nil || m == nil {
		if err != nil && strings.Contains(err.Error(), "request body too large") {
			return nil, false, true
		}
		return nil, false, false
	}
	return m, true, false
}

func (a *API) readBodyOr413(w http.ResponseWriter, r *http.Request) (map[string]interface{}, bool) {
	body, ok, oversized := a.readJSONBody(r)
	if ok {
		return body, true
	}
	if oversized {
		respondErr(w, r, http.StatusRequestEntityTooLarge, "server.invalid_body")
	} else {
		respondErr(w, r, http.StatusBadRequest, "server.invalid_body")
	}
	return nil, false
}

// safeName mirrors _safe_name: a client filename reduced to a bare basename.
func safeName(name string) string {
	base := strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndexByte(base, '/'); i >= 0 {
		base = base[i+1:]
	}
	if base == "" || base == "." || base == ".." {
		return "input"
	}
	return base
}

// decodeInput mirrors _decode_input: (data, name) or an i18n key for the 400.
func decodeInput(body map[string]interface{}) ([]byte, string, string) {
	raw, ok := body["file"].(string)
	if !ok {
		return nil, "", "server.missing_file"
	}
	name, _ := body["name"].(string)
	data, err := base64.StdEncoding.Strict().DecodeString(raw)
	if err != nil {
		return nil, "", "server.bad_base64"
	}
	return data, safeName(name), ""
}

var allowedCleanOptions = map[string]bool{
	"nfkc":                  true,
	"aggressive_homoglyphs": true,
	"keep_non_ai_metadata":  true,
	"also_layer_a_text":     true,
	"remove_pixel":          true,
	"strip_all_metadata":    true,
}

func (a *API) handleInspect(w http.ResponseWriter, r *http.Request, data []byte, name string) {
	kind := cleaning.ClassifyBytes(data, filepath.Ext(name))
	tmp, err := os.MkdirTemp("", "wm-inspect-")
	if err != nil {
		respondErr(w, r, http.StatusInternalServerError, "server.internal")
		return
	}
	defer os.RemoveAll(tmp)
	path := filepath.Join(tmp, nameOrInput(name))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		respondErr(w, r, http.StatusInternalServerError, "server.internal")
		return
	}
	var report map[string]interface{}
	switch kind {
	case cleaning.KindText:
		if cleaning.LooksBinary(data) != "" {
			respondErr(w, r, http.StatusBadRequest, "server.refuse_inspect")
			return
		}
		report = cleaning.InspectText(string(data), false, false).ToDict()
	case cleaning.KindImage:
		rep, err := cleaning.InspectImage(path, "")
		if err != nil {
			respondErr(w, r, http.StatusInternalServerError, "server.internal")
			return
		}
		report = rep.ToDict()
	default:
		rep, err := cleaning.InspectContainer(path)
		if err != nil {
			// Python catches ValueError -> 400, everything else -> 500.
			var ve *cleaning.ValueError
			if errors.As(err, &ve) {
				respond(w, r, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": err.Error()})
			} else {
				respondErr(w, r, http.StatusInternalServerError, "server.internal")
			}
			return
		}
		report = rep.ToDict()
	}
	suspicious := toBool(report["suspicious_total"]) || toBool(report["has_c2pa"]) || toBool(report["has_ai_metadata"])
	respond(w, r, http.StatusOK, map[string]interface{}{"ok": true, "kind": string(kind), "report": report, "suspicious": suspicious})
}

func (a *API) handleClean(w http.ResponseWriter, r *http.Request, data []byte, name string, body map[string]interface{}) {
	kind := cleaning.ClassifyBytes(data, filepath.Ext(name))
	rawOpts, _ := body["options"].(map[string]interface{})
	if rawOpts == nil {
		rawOpts = map[string]interface{}{}
	}
	for key := range rawOpts {
		if !allowedCleanOptions[key] {
			respondErr(w, r, http.StatusBadRequest, "server.unknown_option", key)
			return
		}
	}
	tmp, err := os.MkdirTemp("", "wm-clean-")
	if err != nil {
		respondErr(w, r, http.StatusInternalServerError, "server.internal")
		return
	}
	defer os.RemoveAll(tmp)
	src := filepath.Join(tmp, nameOrInput(name))
	if err := os.WriteFile(src, data, 0o600); err != nil {
		respondErr(w, r, http.StatusInternalServerError, "server.internal")
		return
	}

	var cleanedBytes []byte
	var report map[string]interface{}
	switch kind {
	case cleaning.KindText:
		if cleaning.LooksBinary(data) != "" {
			respondErr(w, r, http.StatusBadRequest, "server.refuse_clean")
			return
		}
		text := string(data)
		res := cleaning.CleanText(text, toBool(rawOpts["nfkc"]), toBool(rawOpts["aggressive_homoglyphs"]), true, false)
		cleanedBytes = []byte(res.Text)
		report = map[string]interface{}{
			"kind":   "text",
			"stats":  res.Stats,
			"length": utf8RuneCount(res.Text),
		}
	case cleaning.KindImage:
		dest := filepath.Join(tmp, "out.png")
		stripAll := !toBool(rawOpts["keep_non_ai_metadata"])
		if _, ok := rawOpts["strip_all_metadata"]; ok {
			stripAll = toBool(rawOpts["strip_all_metadata"])
		}
		removePixel, _ := rawOpts["remove_pixel"].(string)
		if removePixel != "" && removePixel != "ctrlregen" && removePixel != "diffusion" {
			respondErr(w, r, http.StatusBadRequest, "server.remove_pixel")
			return
		}
		result, err := cleaning.CleanImage(src, dest, cleaning.CleanImageOptions{
			StripAllMetadata: cleaning.BoolPtr(stripAll),
			RemovePixel:      removePixel,
		})
		if err != nil {
			respondErr(w, r, http.StatusInternalServerError, "server.internal")
			return
		}
		cleanedBytes, err = os.ReadFile(dest)
		if err != nil {
			respondErr(w, r, http.StatusInternalServerError, "server.internal")
			return
		}
		report = map[string]interface{}{"kind": "image"}
		for k, v := range result {
			report[k] = v
		}
	default:
		suffix := filepath.Ext(name)
		dest := filepath.Join(tmp, "out"+suffix)
		result, err := cleaning.CleanContainer(src, dest, cleaning.CleanContainerOptions{
			SkipLayerAText: !toBoolDefault(rawOpts["also_layer_a_text"], true),
		})
		if err != nil {
			respond(w, r, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		cleanedBytes, err = os.ReadFile(dest)
		if err != nil {
			respondErr(w, r, http.StatusInternalServerError, "server.internal")
			return
		}
		report = map[string]interface{}{"kind": "container"}
		for k, v := range result {
			report[k] = v
		}
	}
	delete(report, "input")
	delete(report, "output")

	respond(w, r, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"kind":    string(kind),
		"cleaned": base64.StdEncoding.EncodeToString(cleanedBytes),
		"report":  report,
	})
}

func (a *API) handleInspectRoute(w http.ResponseWriter, r *http.Request) {
	body, ok := a.readBodyOr413(w, r)
	if !ok {
		return
	}
	data, name, errKey := decodeInput(body)
	if errKey != "" {
		respondErr(w, r, http.StatusBadRequest, errKey)
		return
	}
	a.handleInspect(w, r, data, name)
}

func (a *API) handleCleanRoute(w http.ResponseWriter, r *http.Request) {
	body, ok := a.readBodyOr413(w, r)
	if !ok {
		return
	}
	data, name, errKey := decodeInput(body)
	if errKey != "" {
		respondErr(w, r, http.StatusBadRequest, errKey)
		return
	}
	a.handleClean(w, r, data, name, body)
}

func (a *API) serveIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		respondErr(w, r, http.StatusNotFound, "server.not_found")
		return
	}
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.FileServer(http.FS(sub)).ServeHTTP(w, r)
}
