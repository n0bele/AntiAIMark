// Web upload/download handlers — the Go web extension on top of the JSON API.
package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"watermarks-remover/internal/cleaning"
)

// downloadStore maps one-shot tokens to cleaned files.
type downloadStore struct {
	sync.Mutex
	m map[string]downloadEntry
}

type downloadEntry struct {
	path  string
	name  string
	added time.Time
}

func newDownloadStore() *downloadStore {
	return &downloadStore{m: map[string]downloadEntry{}}
}

func newToken() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// removeVictims deletes the files of already-detached store entries and
// returns (freed bytes, count). Shared by the eviction paths below.
func removeVictims(victims []downloadEntry) (int64, int) {
	var freed int64
	for _, v := range victims {
		if fi, err := os.Stat(v.path); err == nil {
			freed += fi.Size()
		}
		os.RemoveAll(filepath.Dir(v.path))
	}
	return freed, len(victims)
}

// EvictExpiredDownloads removes entries older than ttl (deleting their files)
// and returns (freed bytes, count). It backs the janitor's TTL pass and is
// safe to call concurrently with downloads.
func (a *API) EvictExpiredDownloads(ttl time.Duration) (int64, int) {
	cutoff := time.Now().Add(-ttl)
	var victims []downloadEntry
	a.downloads.Lock()
	for token, entry := range a.downloads.m {
		if entry.added.Before(cutoff) {
			victims = append(victims, entry)
			delete(a.downloads.m, token)
		}
	}
	a.downloads.Unlock()
	if len(victims) == 0 {
		return 0, 0
	}
	freed, n := removeVictims(victims)
	return freed, n
}

// PurgeDownloads removes ALL pending entries (last resort when disk space is
// critically low; downloads are re-generatable) and returns (freed, count).
func (a *API) PurgeDownloads() (int64, int) {
	var victims []downloadEntry
	a.downloads.Lock()
	for token, entry := range a.downloads.m {
		victims = append(victims, entry)
		delete(a.downloads.m, token)
	}
	a.downloads.Unlock()
	if len(victims) == 0 {
		return 0, 0
	}
	freed, n := removeVictims(victims)
	return freed, n
}

// handleUpload processes a multipart upload (image / video / any file),
// runs inspect + clean, and registers the cleaned file for download.
func (a *API) handleUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(nil, r.Body, int64(a.maxBodyBytes))
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		respondErr(w, r, http.StatusBadRequest, "server.invalid_multipart", err.Error())
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		respondErr(w, r, http.StatusBadRequest, "server.missing_field")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, int64(cleaning.MaxInputBytes)+1))
	if err != nil {
		respondErr(w, r, http.StatusBadRequest, "server.read_failed")
		return
	}
	if len(data) > cleaning.MaxInputBytes {
		respondErr(w, r, http.StatusRequestEntityTooLarge, "server.file_exceeds", cleaning.MaxInputBytes)
		return
	}
	name := safeName(header.Filename)

	// Optional "options" JSON field
	rawOpts := map[string]interface{}{}
	if optsStr := r.FormValue("options"); optsStr != "" {
		if err := json.Unmarshal([]byte(optsStr), &rawOpts); err != nil {
			respondErr(w, r, http.StatusBadRequest, "server.bad_options_json")
			return
		}
	}
	for key := range rawOpts {
		if !allowedCleanOptions[key] {
			respondErr(w, r, http.StatusBadRequest, "server.unknown_option", key)
			return
		}
	}

	tmp, err := os.MkdirTemp("", "wm-web-")
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

	kind := cleaning.ClassifyBytes(data, filepath.Ext(name))
	var report map[string]interface{}
	switch kind {
	case cleaning.KindText:
		if cleaning.LooksBinary(data) != "" {
			respondErr(w, r, http.StatusBadRequest, "server.refuse_clean")
			return
		}
		report = cleaning.InspectText(string(data), false, false).ToDict()
	case cleaning.KindImage:
		rep, err := cleaning.InspectImage(src, "")
		if err != nil {
			respondErr(w, r, http.StatusInternalServerError, "server.internal")
			return
		}
		report = rep.ToDict()
	default:
		rep, err := cleaning.InspectContainer(src)
		if err != nil {
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

	// Run the clean pipeline.
	var cleanedBytes []byte
	var cleanReport map[string]interface{}
	switch kind {
	case cleaning.KindText:
		res := cleaning.CleanText(string(data), toBool(rawOpts["nfkc"]), toBool(rawOpts["aggressive_homoglyphs"]), true, false)
		cleanedBytes = []byte(res.Text)
		cleanReport = map[string]interface{}{"kind": "text", "stats": res.Stats, "length": utf8RuneCount(res.Text)}
	case cleaning.KindImage:
		dest := filepath.Join(tmp, "out"+filepath.Ext(name))
		stripAll := !toBool(rawOpts["keep_non_ai_metadata"])
		if _, ok := rawOpts["strip_all_metadata"]; ok {
			stripAll = toBool(rawOpts["strip_all_metadata"])
		}
		removePixel, _ := rawOpts["remove_pixel"].(string)
		if removePixel != "" && removePixel != "ctrlregen" && removePixel != "diffusion" {
			respondErr(w, r, http.StatusBadRequest, "server.remove_pixel")
			return
		}
		result, err := cleaning.CleanImage(src, dest, cleaning.CleanImageOptions{StripAllMetadata: cleaning.BoolPtr(stripAll), RemovePixel: removePixel})
		if err != nil {
			respondErr(w, r, http.StatusInternalServerError, "server.internal")
			return
		}
		cleanedBytes, err = os.ReadFile(dest)
		if err != nil {
			respondErr(w, r, http.StatusInternalServerError, "server.internal")
			return
		}
		cleanReport = map[string]interface{}{"kind": "image"}
		for k, v := range result {
			cleanReport[k] = v
		}
	default:
		suffix := filepath.Ext(name)
		dest := filepath.Join(tmp, "out"+suffix)
		result, err := cleaning.CleanContainer(src, dest, cleaning.CleanContainerOptions{SkipLayerAText: !toBoolDefault(rawOpts["also_layer_a_text"], true)})
		if err != nil {
			respond(w, r, http.StatusBadRequest, map[string]interface{}{"ok": false, "error": err.Error()})
			return
		}
		cleanedBytes, err = os.ReadFile(dest)
		if err != nil {
			respondErr(w, r, http.StatusInternalServerError, "server.internal")
			return
		}
		cleanReport = map[string]interface{}{"kind": "container"}
		for k, v := range result {
			cleanReport[k] = v
		}
	}
	delete(cleanReport, "input")
	delete(cleanReport, "output")

	// Persist cleaned bytes for download.
	token := newToken()
	cleanDir, err := os.MkdirTemp("", "wm-dl-")
	if err != nil {
		respondErr(w, r, http.StatusInternalServerError, "server.internal")
		return
	}
	cleanPath := filepath.Join(cleanDir, nameOrInput(name))
	if err := os.WriteFile(cleanPath, cleanedBytes, 0o600); err != nil {
		respondErr(w, r, http.StatusInternalServerError, "server.internal")
		return
	}
	a.downloads.Lock()
	a.downloads.m[token] = downloadEntry{path: cleanPath, name: name, added: time.Now()}
	a.downloads.Unlock()

	respond(w, r, http.StatusOK, map[string]interface{}{
		"ok":             true,
		"kind":           string(kind),
		"name":           name,
		"bytes_in":       len(data),
		"bytes_out":      len(cleanedBytes),
		"suspicious":     suspicious,
		"report":         report,
		"clean_report":   cleanReport,
		"download_token": token,
	})
}

// handleDownload serves a cleaned file registered by /api/upload (one-shot).
func (a *API) handleDownload(w http.ResponseWriter, r *http.Request) {
	token := strings.TrimPrefix(r.URL.Path, "/api/download/")
	token = safeName(token)
	a.downloads.Lock()
	entry, ok := a.downloads.m[token]
	if ok {
		delete(a.downloads.m, token)
	}
	a.downloads.Unlock()
	if !ok {
		respondErr(w, r, http.StatusNotFound, "server.download_gone")
		return
	}
	data, err := os.ReadFile(entry.path)
	os.RemoveAll(filepath.Dir(entry.path))
	if err != nil {
		respondErr(w, r, http.StatusInternalServerError, "server.internal")
		return
	}
	w.Header().Set("Content-Type", contentTypeFor(entry.name))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"cleaned-%s\"", entry.name))
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(data)))
	w.Header().Set("Cache-Control", "no-store")
	w.Write(data)
}

func contentTypeFor(name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".webp":
		return "image/webp"
	case ".svg":
		return "image/svg+xml"
	case ".pdf":
		return "application/pdf"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".odt":
		return "application/vnd.oasis.opendocument.text"
	case ".mp4", ".m4v":
		return "video/mp4"
	case ".mov":
		return "video/quicktime"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".wmv":
		return "video/x-ms-wmv"
	case ".flv":
		return "video/x-flv"
	case ".mpg", ".mpeg":
		return "video/mpeg"
	case ".txt", ".md", ".markdown":
		return "text/plain; charset=utf-8"
	case ".html", ".htm":
		return "text/html; charset=utf-8"
	}
	return "application/octet-stream"
}
