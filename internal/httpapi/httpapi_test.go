package httpapi

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) (*API, *httptest.Server) {
	t.Helper()
	api := New(Options{Version: "test"})
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)
	return api, ts
}

func TestHealth(t *testing.T) {
	_, ts := newTestServer(t)
	res, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]interface{}
	json.NewDecoder(res.Body).Decode(&body)
	if body["ok"] != true || body["version"] != "test" {
		t.Fatalf("health = %v", body)
	}
}

func TestInspectCleanText(t *testing.T) {
	_, ts := newTestServer(t)
	b64 := base64.StdEncoding.EncodeToString([]byte("hi\u200bthere"))
	payload, _ := json.Marshal(map[string]interface{}{"file": b64, "name": "x.txt"})

	res, err := http.Post(ts.URL+"/inspect", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	json.NewDecoder(res.Body).Decode(&body)
	res.Body.Close()
	if body["ok"] != true || body["kind"] != "text" || body["suspicious"] != true {
		t.Fatalf("inspect = %v", body)
	}

	res, err = http.Post(ts.URL+"/clean", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(res.Body).Decode(&body)
	res.Body.Close()
	if body["ok"] != true {
		t.Fatalf("clean = %v", body)
	}
	out, _ := base64.StdEncoding.DecodeString(body["cleaned"].(string))
	if strings.Contains(string(out), "\u200b") {
		t.Fatalf("ZWSP survived: %q", out)
	}
}

func TestCleanUnknownOption400(t *testing.T) {
	_, ts := newTestServer(t)
	payload, _ := json.Marshal(map[string]interface{}{
		"file": base64.StdEncoding.EncodeToString([]byte("x")), "name": "x.txt",
		"options": map[string]interface{}{"bogus": true},
	})
	res, err := http.Post(ts.URL+"/clean", "application/json", bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", res.StatusCode)
	}
}

// TestErrorLocalizationByAcceptLanguage verifies 400 messages follow the
// Accept-Language header.
func TestErrorLocalizationByAcceptLanguage(t *testing.T) {
	_, ts := newTestServer(t)
	payload, _ := json.Marshal(map[string]interface{}{"file": base64.StdEncoding.EncodeToString([]byte("x")), "name": "x.txt", "options": map[string]interface{}{"bogus": 1}})
	req, _ := http.NewRequest("POST", ts.URL+"/clean", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body map[string]interface{}
	json.NewDecoder(res.Body).Decode(&body)
	if msg, _ := body["error"].(string); !strings.Contains(msg, "未知选项") {
		t.Fatalf("zh error message = %v", body["error"])
	}
}

func TestI18nEndpoint(t *testing.T) {
	_, ts := newTestServer(t)
	res, err := http.Get(ts.URL + "/api/i18n?lang=fr")
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var body struct {
		Lang      string              `json:"lang"`
		Languages []map[string]string `json:"languages"`
		Messages  map[string]string   `json:"messages"`
	}
	json.NewDecoder(res.Body).Decode(&body)
	if body.Lang != "fr" {
		t.Fatalf("lang = %q", body.Lang)
	}
	if len(body.Languages) != 5 {
		t.Fatalf("languages = %v", body.Languages)
	}
	if !strings.Contains(body.Messages["web.title"], "watermarks") {
		t.Fatalf("messages[web.title] = %q", body.Messages["web.title"])
	}
}

func TestUploadDownloadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	png := filepath.Join(dir, "a.png")
	// minimal PNG: signature + IHDR (no metadata chunks at all)
	os.WriteFile(png, []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x00\x00\x00\x00"), 0o600)

	_, ts := newTestServer(t)
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", "a.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x00\x00\x00\x00")); err != nil {
		t.Fatal(err)
	}
	mw.Close()

	res, err := http.Post(ts.URL+"/api/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	json.NewDecoder(res.Body).Decode(&body)
	res.Body.Close()
	if body["ok"] != true || body["kind"] != "image" {
		t.Fatalf("upload = %v", body)
	}
	token, _ := body["download_token"].(string)
	if token == "" {
		t.Fatal("no download token")
	}

	dl, err := http.Get(ts.URL + "/api/download/" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer dl.Body.Close()
	if dl.Header.Get("Content-Type") != "image/png" {
		t.Fatalf("content-type = %q", dl.Header.Get("Content-Type"))
	}
	// one-shot: second download is 404
	if again, err := http.Get(ts.URL + "/api/download/" + token); err == nil {
		if again.StatusCode != http.StatusNotFound {
			t.Fatalf("second download status = %d", again.StatusCode)
		}
		again.Body.Close()
	}
}

// TestDownloadEviction covers the janitor hooks: expired entries lose their
// files, and a subsequent download is 404.
func TestDownloadEviction(t *testing.T) {
	api, ts := newTestServer(t)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "b.png")
	fw.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x00\x00\x00\x00"))
	mw.Close()
	res, err := http.Post(ts.URL+"/api/upload", mw.FormDataContentType(), &buf)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]interface{}
	json.NewDecoder(res.Body).Decode(&body)
	res.Body.Close()
	token, _ := body["download_token"].(string)

	// TTL 0: everything is expired.
	freed, n := api.EvictExpiredDownloads(0)
	if n != 1 || freed <= 0 {
		t.Fatalf("evict = (%d, %d)", freed, n)
	}
	dl, err := http.Get(ts.URL + "/api/download/" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer dl.Body.Close()
	if dl.StatusCode != http.StatusNotFound {
		t.Fatalf("evicted download status = %d", dl.StatusCode)
	}

	// Purge with an empty store is a no-op.
	if freed, n := api.PurgeDownloads(); n != 0 || freed != 0 {
		t.Fatalf("purge on empty store = (%d, %d)", freed, n)
	}
}
