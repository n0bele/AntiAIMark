// audit-website: Go port of service/scripts/audit_website.py.
//
// Aggregate AI-provenance audit over the URLs listed in a sitemap: downloads
// each URL, classifies it by content type/suffix/magic, and runs the same
// deterministic text/image/container inspections used by the local audit.
// Optional external tools (c2patool/exiftool) are not invoked for remote
// URLs; download the assets and run audit-dir locally for those.
//
// SSRF hardening mirrors the Python original: http(s) only, no credentials,
// same-origin redirects/sitemap URLs only, hostnames resolved to validated
// public numeric IPs and the connection pinned to them (Host header and TLS
// SNI still use the original hostname).
package main

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/xml"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/cliutil"
)

const (
	defaultMaxBytes             = 4 << 20
	defaultTimeout              = 15
	defaultMaxPages             = 200
	maxSitemapDecompressedBytes = 64 << 20
	maxRedirects                = 5
	userAgent                   = "remove-ai-marks-audit/1.0"
)

var extForKind = map[string]string{
	"png": ".png", "jpeg": ".jpg", "svg": ".svg", "pdf": ".pdf",
	"docx": ".docx", "odt": ".odt", "html": ".html", "markdown": ".md",
	"text": ".txt",
}

// urlOrigin is the (scheme, host, port) triple from _url_origin.
type urlOrigin struct {
	scheme string
	host   string
	port   int
}

func parseOrigin(u *url.URL) (urlOrigin, error) {
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return urlOrigin{}, fmt.Errorf("unsupported URL scheme: %s", orMissing(u.Scheme))
	}
	if u.User != nil {
		return urlOrigin{}, fmt.Errorf("credentials in URLs are not allowed")
	}
	host := u.Hostname()
	if host == "" {
		return urlOrigin{}, fmt.Errorf("URL has no hostname")
	}
	port := 80
	if scheme == "https" {
		port = 443
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil {
			return urlOrigin{}, fmt.Errorf("invalid URL port: %s", p)
		}
		port = n
	}
	return urlOrigin{scheme, strings.ToLower(strings.TrimSuffix(host, ".")), port}, nil
}

func urlOriginOf(rawURL string) (urlOrigin, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return urlOrigin{}, fmt.Errorf("unsupported URL scheme: (missing)")
	}
	return parseOrigin(u)
}

func orMissing(s string) string {
	if s == "" {
		return "(missing)"
	}
	return s
}

// originAllowed allows same-origin URLs plus a normal HTTP-to-HTTPS upgrade.
func originAllowed(candidate, expected urlOrigin) bool {
	if candidate == expected {
		return true
	}
	return expected.scheme == "http" && expected.port == 80 &&
		candidate.scheme == "https" && candidate.port == 443 &&
		candidate.host == expected.host
}

// isGlobalIP mirrors Python ipaddress.is_global (default-route-reachable
// public unicast), rejecting loopback/private/shared/documentation/etc.
func isGlobalIP(ip net.IP) bool {
	if ip.To4() != nil {
		ip = ip.To4()
	}
	for _, n := range privateNets {
		if n.Contains(ip) {
			return false
		}
	}
	return !ip.IsUnspecified() && !ip.IsMulticast()
}

var privateNets = mustCIDRs(
	"0.0.0.0/8", "10.0.0.0/8", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12",
	"192.0.0.0/24", "192.0.2.0/24", "192.168.0.0/16", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "255.255.255.255/32",
	"100.64.0.0/10",
	"::/128", "::1/128", "::ffff:0:0/96", "64:ff9b::/96", "100::/64",
	"2001::/23", "2001:db8::/32", "fc00::/7", "fe80::/10",
)

func mustCIDRs(specs ...string) []*net.IPNet {
	out := make([]*net.IPNet, 0, len(specs))
	for _, s := range specs {
		_, n, err := net.ParseCIDR(s)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

// resolvePublicAddresses resolves an origin to validated public numeric
// addresses (mirrors _resolve_public_addresses).
func resolvePublicAddresses(o urlOrigin) ([]string, error) {
	hostForIP := strings.SplitN(o.host, "%", 2)[0]
	var rawAddresses []string
	if ip := net.ParseIP(hostForIP); ip != nil {
		rawAddresses = append(rawAddresses, ip.String())
	} else {
		infos, err := net.LookupIP(o.host)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve hostname %s: %v", o.host, err)
		}
		for _, ip := range infos {
			rawAddresses = append(rawAddresses, strings.SplitN(ip.String(), "%", 2)[0])
		}
	}

	var addresses []string
	for _, raw := range rawAddresses {
		ip := net.ParseIP(raw)
		if ip == nil {
			continue
		}
		// unwrap IPv4-mapped IPv6 (::ffff:a.b.c.d)
		if v4 := ip.To4(); v4 != nil {
			ip = v4
		}
		if !isGlobalIP(ip) {
			return nil, fmt.Errorf("refusing non-public address for %s: %s", o.host, ip)
		}
		canonical := ip.String()
		dup := false
		for _, a := range addresses {
			if a == canonical {
				dup = true
				break
			}
		}
		if !dup {
			addresses = append(addresses, canonical)
		}
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("hostname resolved to no IP addresses: %s", o.host)
	}
	return addresses, nil
}

// validatedTarget validates URL policy and binds it to numeric public IPs.
func validatedTarget(rawURL string, expectedOrigin *urlOrigin) (urlOrigin, []string, error) {
	origin, err := urlOriginOf(rawURL)
	if err != nil {
		return urlOrigin{}, nil, err
	}
	if expectedOrigin != nil && !originAllowed(origin, *expectedOrigin) {
		return urlOrigin{}, nil, fmt.Errorf("cross-origin URL is not allowed: %s://%s:%d",
			origin.scheme, origin.host, origin.port)
	}
	addresses, err := resolvePublicAddresses(origin)
	if err != nil {
		return urlOrigin{}, nil, err
	}
	return origin, addresses, nil
}

func validatePublicHTTPURL(rawURL string) (urlOrigin, error) {
	origin, _, err := validatedTarget(rawURL, nil)
	return origin, err
}

// openPinnedConnection connects to a validated IP while preserving Host and
// TLS SNI (mirrors _open_pinned_connection).
func openPinnedConnection(o urlOrigin, addresses []string, timeout int) (net.Conn, error) {
	var lastErr error
	for _, address := range addresses {
		dialer := net.Dialer{Timeout: time.Duration(timeout) * time.Second}
		conn, err := dialer.Dial("tcp", net.JoinHostPort(address, strconv.Itoa(o.port)))
		if err != nil {
			lastErr = err
			continue
		}
		if o.scheme != "https" {
			return conn, nil
		}
		tlsConn := tls.Client(conn, &tls.Config{
			ServerName: host0(o.host),
			MinVersion: tls.VersionTLS12,
			NextProtos: []string{"http/1.1"},
		})
		if err := tlsConn.Handshake(); err != nil {
			conn.Close()
			lastErr = err
			continue
		}
		return tlsConn, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("no validated address available for %s", o.host)
}

// host0 strips a zone index if present (TLS SNI must be a bare hostname/IP).
func host0(host string) string {
	return strings.SplitN(host, "%", 2)[0]
}

// readHTTPResponse reads one HTTP/1.1 response off the pinned connection.
func readHTTPResponse(conn net.Conn) (*http.Response, error) {
	req, _ := http.NewRequest("GET", "/", nil)
	return http.ReadResponse(bufio.NewReader(conn), req)
}

func requestTarget(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "/"
	}
	target := u.Path
	if target == "" {
		target = "/"
	}
	if u.RawQuery != "" {
		target += "?" + u.RawQuery
	}
	return target
}

// fetch fetches with IP pinning, redirect validation and a byte cap.
func fetch(rawURL string, timeout, maxBytes int, allowedOrigin *urlOrigin) ([]byte, string, error) {
	currentURL := rawURL
	var expectedOrigin *urlOrigin
	if allowedOrigin != nil {
		copied := *allowedOrigin
		expectedOrigin = &copied
	}

	for redirectCount := 0; redirectCount <= maxRedirects; redirectCount++ {
		origin, addresses, err := validatedTarget(currentURL, expectedOrigin)
		if err != nil {
			return nil, "", err
		}
		if expectedOrigin == nil {
			copied := origin
			expectedOrigin = &copied
		}

		conn, err := openPinnedConnection(origin, addresses, timeout)
		if err != nil {
			return nil, "", err
		}

		host := origin.host
		if (origin.scheme == "http" && origin.port != 80) || (origin.scheme == "https" && origin.port != 443) {
			host = net.JoinHostPort(origin.host, strconv.Itoa(origin.port))
		}
		req := fmt.Sprintf("GET %s HTTP/1.1\r\nHost: %s\r\nUser-Agent: %s\r\nConnection: close\r\n\r\n",
			requestTarget(currentURL), host, userAgent)
		if _, err := conn.Write([]byte(req)); err != nil {
			conn.Close()
			return nil, "", err
		}
		resp, err := readHTTPResponse(conn)
		if err != nil {
			conn.Close()
			return nil, "", err
		}

		if resp.StatusCode == 301 || resp.StatusCode == 302 || resp.StatusCode == 303 ||
			resp.StatusCode == 307 || resp.StatusCode == 308 {
			location := resp.Header.Get("Location")
			resp.Body.Close()
			conn.Close()
			if location != "" {
				if redirectCount >= maxRedirects {
					return nil, "", fmt.Errorf("too many redirects (>%d)", maxRedirects)
				}
				currentURL, err = resolveRef(currentURL, location)
				if err != nil {
					return nil, "", err
				}
				continue
			}
			return nil, "", fmt.Errorf("redirect without Location header")
		}

		contentType := resp.Header.Get("Content-Type")
		data, err := readCapped(resp.Body, maxBytes)
		resp.Body.Close()
		conn.Close()
		if err != nil {
			return nil, "", err
		}
		return data, contentType, nil
	}
	return nil, "", fmt.Errorf("too many redirects (>%d)", maxRedirects)
}

func resolveRef(base, ref string) (string, error) {
	b, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	r, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return b.ResolveReference(r).String(), nil
}

func readCapped(r io.Reader, maxBytes int) ([]byte, error) {
	var buf bytes.Buffer
	chunk := make([]byte, 1<<16)
	total := 0
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			total += n
			if total > maxBytes {
				return nil, fmt.Errorf("exceeds %d bytes", maxBytes)
			}
			buf.Write(chunk[:n])
		}
		if err == io.EOF {
			return buf.Bytes(), nil
		}
		if err != nil {
			return nil, err
		}
	}
}

// guessKind classifies a downloaded URL from headers, suffix, then magic.
func guessKind(rawURL string, data []byte, contentType string) string {
	ct := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	switch {
	case strings.Contains(ct, "html"):
		return "html"
	case ct == "image/png":
		return "png"
	case ct == "image/jpeg":
		return "jpeg"
	case strings.Contains(ct, "svg"):
		return "svg"
	case ct == "application/pdf":
		return "pdf"
	case strings.Contains(ct, "wordprocessingml"):
		return "docx"
	case strings.Contains(ct, "opendocument.text"):
		return "odt"
	case strings.Contains(ct, "markdown"):
		return "markdown"
	case ct == "text/plain":
		return "text"
	}

	u, _ := url.Parse(rawURL)
	path := ""
	if u != nil {
		path = strings.ToLower(u.Path)
	}
	for _, ext := range []struct{ ext, kind string }{
		{".png", "png"}, {".jpg", "jpeg"}, {".jpeg", "jpeg"}, {".svg", "svg"},
		{".pdf", "pdf"}, {".docx", "docx"}, {".odt", "odt"}, {".html", "html"},
		{".htm", "html"}, {".md", "markdown"}, {".markdown", "markdown"}, {".txt", "text"},
	} {
		if strings.HasSuffix(path, ext.ext) {
			return ext.kind
		}
	}

	if bytes.HasPrefix(data, []byte("\x89PNG")) {
		return "png"
	}
	if bytes.HasPrefix(data, []byte("\xff\xd8")) {
		return "jpeg"
	}
	if bytes.HasPrefix(data, []byte("%PDF")) {
		return "pdf"
	}
	head := bytes.TrimLeft(firstN(data, 100), " \t\r\n\x0b\x0c")
	if len(head) > 0 && head[0] == '<' && bytes.Contains(lower(firstN(data, 500)), []byte("svg")) {
		return "svg"
	}
	if bytes.Contains(lower(firstN(data, 2000)), []byte("<html")) || (len(head) > 0 && head[0] == '<') {
		return "html"
	}
	return "text"
}

func firstN(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}

func lower(b []byte) []byte {
	return bytes.ToLower(b)
}

var locTagRe = regexp.MustCompile(`loc`)

func localName(tag string) string {
	if i := strings.LastIndex(tag, "}"); i >= 0 {
		return tag[i+1:]
	}
	return tag
}

// parseSitemap parses a (possibly gzip-compressed) sitemap into (kind, urls).
func parseSitemap(data []byte) (string, []string, error) {
	if bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
		zr, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return "", nil, err
		}
		limit := io.LimitReader(zr, maxSitemapDecompressedBytes+1)
		decompressed, err := io.ReadAll(limit)
		zr.Close()
		if err != nil {
			return "", nil, err
		}
		if len(decompressed) > maxSitemapDecompressedBytes {
			return "", nil, fmt.Errorf("sitemap decompressed size exceeds cap (%d bytes)", maxSitemapDecompressedBytes)
		}
		data = decompressed
	} else if len(data) > maxSitemapDecompressedBytes {
		return "", nil, fmt.Errorf("sitemap size exceeds cap (%d bytes)", maxSitemapDecompressedBytes)
	}

	dec := xml.NewDecoder(bytes.NewReader(data))
	var urls []string
	kind := ""
	inLoc := false
	var locText strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := localName(t.Name.Local)
			if kind == "" {
				kind = name
			}
			if name == "loc" {
				inLoc = true
				locText.Reset()
			}
		case xml.CharData:
			if inLoc {
				locText.Write(t)
			}
		case xml.EndElement:
			if localName(t.Name.Local) == "loc" && inLoc {
				inLoc = false
				if s := strings.TrimSpace(locText.String()); s != "" {
					urls = append(urls, s)
				}
			}
		}
	}
	if kind == "" {
		return "", nil, fmt.Errorf("empty sitemap document")
	}
	_ = locTagRe
	return kind, urls, nil
}

// inspectRemote inspects downloaded bytes using the local scan pipeline.
func inspectRemote(rawURL string, data []byte, contentType string) (map[string]interface{}, error) {
	kind := guessKind(rawURL, data, contentType)
	ext := extForKind[kind]
	if ext == "" {
		ext = ".bin"
	}
	tmpDir, err := os.MkdirTemp("", "wm-audit-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmpDir)
	tmp := filepath.Join(tmpDir, "asset"+ext)
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return nil, err
	}
	item := cleaning.ScanFile(tmp, rawURL)
	if item.Err != "" {
		return nil, fmt.Errorf("%s", item.Err)
	}
	result := item.ToDict()
	result["kind"] = kind
	return result, nil
}

// discoverSitemap finds a same-site sitemap via standard paths then robots.txt.
func discoverSitemap(baseURL string, timeout int) (string, error) {
	origin, err := validatePublicHTTPURL(baseURL)
	if err != nil {
		return "", err
	}
	base := strings.TrimRight(baseURL, "/")

	for _, candidate := range []string{base + "/sitemap.xml", base + "/sitemap_index.xml"} {
		data, _, err := fetch(candidate, timeout, defaultMaxBytes, &origin)
		if err != nil {
			continue
		}
		if _, _, err := parseSitemap(data); err == nil {
			return candidate, nil
		}
	}

	data, _, err := fetch(base+"/robots.txt", timeout, 1<<20, &origin)
	if err != nil {
		return "", nil
	}
	text := string(data)
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(strings.ToLower(line), "sitemap:") {
			candidate := strings.TrimSpace(line[len("sitemap:"):])
			candidateOrigin, err := urlOriginOf(candidate)
			if err != nil {
				continue
			}
			if !originAllowed(candidateOrigin, origin) {
				return "", fmt.Errorf("cross-origin sitemap is not allowed: %s", candidate)
			}
			return candidate, nil
		}
	}
	return "", nil
}

// collectURLs collects same-site URLs while following nested sitemap indexes.
func collectURLs(sitemapURL string, timeout, maxPages int) ([]string, error) {
	var urls []string
	seen := map[string]bool{}
	origin, err := validatePublicHTTPURL(sitemapURL)
	if err != nil {
		return nil, err
	}

	checkLoc := func(loc string) error {
		candidateOrigin, err := urlOriginOf(loc)
		if err != nil {
			return err
		}
		if !originAllowed(candidateOrigin, origin) {
			return fmt.Errorf("cross-origin sitemap URL is not allowed: %s", loc)
		}
		return nil
	}

	var recurse func(u string, depth int) error
	recurse = func(u string, depth int) error {
		if len(urls) >= maxPages || depth > 3 {
			return nil
		}
		data, _, err := fetch(u, timeout, defaultMaxBytes, &origin)
		if err != nil {
			return err
		}
		kind, locs, err := parseSitemap(data)
		if err != nil {
			return err
		}
		if kind == "sitemapindex" {
			for _, loc := range locs {
				if err := checkLoc(loc); err != nil {
					return err
				}
				if !seen[loc] {
					seen[loc] = true
					if err := recurse(loc, depth+1); err != nil {
						return err
					}
				}
			}
		} else {
			for _, loc := range locs {
				if err := checkLoc(loc); err != nil {
					return err
				}
				if !seen[loc] {
					seen[loc] = true
					urls = append(urls, loc)
					if len(urls) >= maxPages {
						break
					}
				}
			}
		}
		return nil
	}

	if err := recurse(sitemapURL, 0); err != nil {
		return nil, err
	}
	return urls, nil
}

func main() {
	var sitemap, baseURL string
	var maxPages, timeoutSec, maxBytes int
	var jsonOut bool
	flag.StringVar(&sitemap, "sitemap", "", "Sitemap URL to audit")
	flag.StringVar(&baseURL, "base", "", "Base URL; discover the sitemap automatically")
	flag.IntVar(&maxPages, "max-pages", defaultMaxPages, "maximum pages to audit")
	flag.IntVar(&timeoutSec, "timeout", defaultTimeout, "per-request timeout in seconds")
	flag.IntVar(&maxBytes, "max-bytes", defaultMaxBytes, "per-asset byte cap")
	flag.BoolVar(&jsonOut, "json", false, "emit a JSON report")
	var langFlag string
	cliutil.AddLangFlag(&langFlag)
	flag.Parse()
	cliutil.Init(langFlag)

	if sitemap == "" && baseURL == "" {
		cleaning.Eprint("provide --sitemap URL or --base URL")
		os.Exit(2)
	}

	if sitemap == "" {
		found, err := discoverSitemap(baseURL, timeoutSec)
		if err != nil {
			cleaning.Eprint("invalid base URL: " + err.Error())
			os.Exit(2)
		}
		if found == "" {
			cleaning.Eprint("no sitemap found for " + baseURL)
			os.Exit(2)
		}
		sitemap = found
	}

	urls, err := collectURLs(sitemap, timeoutSec, maxPages)
	if err != nil {
		cleaning.Eprint(fmt.Sprintf("could not collect URLs from %s: %v", sitemap, err))
		os.Exit(2)
	}
	if len(urls) == 0 {
		cleaning.Eprint("no URLs collected from sitemap")
		os.Exit(2)
	}

	var files []map[string]interface{}
	var failures []map[string]interface{}
	if maxPages < len(urls) {
		urls = urls[:maxPages]
	}
	for _, u := range urls {
		data, contentType, err := fetch(u, timeoutSec, maxBytes, nil)
		if err != nil {
			failures = append(failures, map[string]interface{}{"url": u, "error": err.Error()})
			continue
		}
		item, err := inspectRemote(u, data, contentType)
		if err != nil {
			failures = append(failures, map[string]interface{}{"url": u, "error": "inspect failed: " + err.Error()})
			continue
		}
		files = append(files, item)
	}

	summary := cleaning.Aggregate(files)
	if files == nil {
		files = []map[string]interface{}{}
	}
	if failures == nil {
		failures = []map[string]interface{}{}
	}
	var baseField interface{}
	if baseURL != "" {
		baseField = baseURL
	}
	report := map[string]interface{}{
		"sitemap":        sitemap,
		"base":           baseField,
		"urls_collected": len(urls),
		"urls_scanned":   len(files),
		"urls_failed":    failures,
		"summary":        summary,
		"files":          files,
	}

	if jsonOut {
		if err := cleaning.EmitJSON(report); err != nil {
			cleaning.Eprint("error: " + err.Error())
			os.Exit(1)
		}
	} else {
		cleaning.PrintHumanReport(files, summary, [][2]string{
			{"audit.sitemap", sitemap},
			{"audit.urls_collected", strconv.Itoa(len(urls))},
			{"audit.urls_scanned", strconv.Itoa(len(files))},
			{"audit.urls_failed", strconv.Itoa(len(failures))},
		})
		for _, f := range failures {
			fmt.Printf("  [error] %s: %v\n", f["url"], f["error"])
		}
	}

	if n, _ := summary["actionable_files"].(int); n > 0 {
		os.Exit(1)
	}
	os.Exit(0)
}
