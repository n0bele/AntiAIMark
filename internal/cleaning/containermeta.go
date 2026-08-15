// Inspect/clean AI provenance metadata in non-raster containers.
// Faithful Go port of service/scripts/container_meta.py.
//
// Formats: SVG, PDF (best-effort), DOCX, ODT, HTML, Markdown frontmatter.
// Stdlib-first; PDF prefers optional exiftool/c2patool when present.
// Go web extension: common video formats are handled best-effort.
package cleaning

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// Frontmatter / meta keys that often carry AI provenance
var aiFrontmatterKeys = map[string]bool{
	"generator":           true,
	"ai":                  true,
	"ai_generated":        true,
	"ai-generated":        true,
	"claude":              true,
	"anthropic":           true,
	"openai":              true,
	"gemini":              true,
	"synthid":             true,
	"c2pa":                true,
	"content_credentials": true,
	"contentcredentials":  true,
	"provenance":          true,
	"digital_source_type": true,
	"digitalsourcetype":   true,
	"created_with":        true,
	"createdwith":         true,
	"model":               true,
	"llm":                 true,
}

// aiMetaNameRe mirrors AI_META_NAME_RE, including the public generator names
// added 2026-08. Container payloads can contain prose, so dictionary-word
// keys (imagen, firefly, flux, grok) are avoided here; the image hint table
// carries the wider list.
var aiMetaNameRe = regexp.MustCompile(`(?i)generator|ai[-_ ]?generated|claude|anthropic|openai|gemini|synthid|` +
	`c2pa|content.?credential|provenance|digital.?source|aigc|` +
	`midjourney|stable.?diffusion|sdxl|comfyui|automatic1111|` +
	`black.?forest|flux1|flux\.1|ideogram|recraft|leonardo\.ai|` +
	`dall-?e|gpt-image|adobe firefly|grok-aurora|` +
	`doubao|豆包|jimeng|即梦|dreamina|hunyuan|混元|通义万相|` +
	`cogview|cogvideo|文心一格|ernie.?vilg|hailuo|海螺|pixverse`)

var svgDropTags = map[string]bool{
	"{http://www.w3.org/2000/svg}metadata": true,
	"metadata":                             true,
	"{http://www.w3.org/1999/02/22-rdf-syntax-ns#}RDF": true,
	"{adobe:ns:meta/}xmpmeta":                          true,
}

// ContainerInspectReport mirrors the Python ContainerInspectReport dataclass.
type ContainerInspectReport struct {
	Path          string
	Format        string
	HasC2PA       bool
	HasAIMetadata bool
	Findings      []string
	Tools         map[string]interface{}
	Details       map[string]interface{}
	Notes         []string
}

// ToDict renders the report as the Python to_dict() JSON shape. Findings is
// normalized to [] so JSON never emits null (the Python dataclass always
// produces a list).
func (r ContainerInspectReport) ToDict() map[string]interface{} {
	findings := r.Findings
	if findings == nil {
		findings = []string{}
	}
	conf := make([]interface{}, 0, len(findings))
	for _, f := range findings {
		conf = append(conf, ClassifyFindingConfidence(f))
	}
	if r.Tools == nil {
		r.Tools = map[string]interface{}{}
	}
	if r.Details == nil {
		r.Details = map[string]interface{}{}
	}
	if r.Notes == nil {
		r.Notes = []string{}
	}
	return map[string]interface{}{
		"path":                r.Path,
		"format":              r.Format,
		"has_c2pa":            r.HasC2PA,
		"has_ai_metadata":     r.HasAIMetadata,
		"findings":            findings,
		"findings_confidence": conf,
		"tools":               r.Tools,
		"details":             r.Details,
		"notes":               r.Notes,
	}
}

var videoFormats = map[string]bool{
	"mp4": true, "m4v": true, "mov": true, "webm": true, "mkv": true,
	"avi": true, "wmv": true, "flv": true, "mpeg": true, "mpegts": true,
	"ogv": true,
}

func isVideoFormat(fmt string) bool { return videoFormats[fmt] }

var videoExtToFormat = map[string]string{
	".mp4": "mp4", ".m4v": "m4v", ".mov": "mov", ".qt": "mov",
	".webm": "webm", ".mkv": "mkv", ".avi": "avi", ".wmv": "wmv",
	".flv": "flv", ".ts": "mpegts", ".m2ts": "mpegts",
	".mpg": "mpeg", ".mpeg": "mpeg", ".ogv": "ogv", ".3gp": "mp4",
}

// DetectContainerFormat ports detect_container_format(path, data=None):
// extension first, then magic bytes. Returns "unknown" when nothing matches.
// Video formats are a Go web extension.
func DetectContainerFormat(path string, data []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".svg":
		return "svg"
	case ".pdf":
		return "pdf"
	case ".docx":
		return "docx"
	case ".odt":
		return "odt"
	case ".html", ".htm":
		return "html"
	case ".md", ".markdown", ".mdx":
		return "markdown"
	}
	if data != nil {
		if len(data) >= 4 && bytes.HasPrefix(data, []byte("%PDF")) {
			return "pdf"
		}
		head100 := bytes.TrimLeft(data[:min(100, len(data))], " \t\r\n\x0b\x0c")
		head500 := bytes.ToLower(data[:min(500, len(data))])
		if bytes.HasPrefix(head100, []byte("<")) && bytes.Contains(head500, []byte("svg")) {
			return "svg"
		}
		if len(data) >= 2 && bytes.HasPrefix(data, []byte("PK")) {
			// zip-based; sniff
			if fmt := sniffZipFormat(data); fmt != "" {
				return fmt
			}
		}
		// Go web extension: video magic bytes
		if fmt := sniffVideoFormat(ext, data); fmt != "" {
			return fmt
		}
	}
	if fmt, ok := videoExtToFormat[ext]; ok {
		return fmt
	}
	return "unknown"
}

func sniffZipFormat(data []byte) string {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return ""
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if names["word/document.xml"] {
		return "docx"
	}
	if names["content.xml"] && names["meta.xml"] {
		return "odt"
	}
	return ""
}

func sniffVideoFormat(ext string, data []byte) string {
	if len(data) >= 12 && bytes.Equal(data[4:8], []byte("ftyp")) {
		brand := string(data[8:12])
		switch brand {
		case "qt  ":
			return "mov"
		case "M4V ":
			return "m4v"
		}
		return "mp4"
	}
	if bytes.HasPrefix(data, []byte{0x1A, 0x45, 0xDF, 0xA3}) {
		if ext == ".webm" {
			return "webm"
		}
		return "mkv"
	}
	if len(data) >= 12 && bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("AVI ")) {
		return "avi"
	}
	if bytes.HasPrefix(data, []byte{0x30, 0x26, 0xB2, 0x75, 0x8E, 0x66, 0xCF, 0x11, 0xA6, 0xD9}) {
		return "wmv"
	}
	if bytes.HasPrefix(data, []byte("FLV")) {
		return "flv"
	}
	if bytes.HasPrefix(data, []byte{0x00, 0x00, 0x01, 0xBA}) {
		return "mpeg"
	}
	if len(data) > 188 && data[0] == 0x47 && data[188] == 0x47 {
		return "mpegts"
	}
	if bytes.HasPrefix(data, []byte("OggS")) {
		return "ogv"
	}
	return ""
}

// blobHits ports _blob_hits: scan a byte blob for C2PA / AI markers.
// Python's bytes.lower() folds ASCII only, so asciiLowerBytes matches it
// exactly (bytes.ToLower would fold non-ASCII runs too).
func blobHits(blob []byte) (bool, bool, []string) {
	lower := asciiLowerBytes(blob)
	var findings []string
	hasC2pa := false
	hasAI := false
	for _, n := range c2paMarkers {
		if bytes.Contains(lower, asciiLowerBytes(n)) {
			hasC2pa = true
			findings = append(findings, "marker:"+string(n))
		}
	}
	seen := map[string]bool{}
	for _, f := range findings {
		parts := strings.SplitN(f, ":", 2)
		seen[parts[len(parts)-1]] = true
	}
	for _, n := range aiMetaHints {
		if bytes.Contains(lower, asciiLowerBytes(n)) {
			hasAI = true
			label := string(n)
			if !seen[label] {
				findings = append(findings, "ai:"+label)
			}
		}
	}
	if len(findings) > 30 {
		findings = findings[:30]
	}
	return hasC2pa, hasAI || hasC2pa, findings
}

// ---------------------------------------------------------------------------
// Markdown frontmatter
// ---------------------------------------------------------------------------

var fmRe = regexp.MustCompile(`(?s)\A---\r?\n(.*?)\r?\n---\r?\n?`)

// parseSimpleYamlKeys returns (key, full_line, line_index) for top-level keys only.
func parseSimpleYamlKeys(block string) [][3]string {
	keyRe := regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:`)
	var rows [][3]string
	for i, line := range pySplitLines(block) {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		first := line[0]
		if first == ' ' || first == '\t' || first == '-' {
			continue // nested / list — leave alone
		}
		if m := keyRe.FindStringSubmatch(line); m != nil {
			rows = append(rows, [3]string{m[1], line, itoa(i)})
		}
	}
	return rows
}

func inspectMarkdown(text string) (bool, bool, []string, map[string]interface{}) {
	var findings []string
	hasAI := false
	idx := fmRe.FindStringSubmatchIndex(text)
	if idx == nil {
		return false, false, []string{}, map[string]interface{}{"has_frontmatter": false}
	}
	block := text[idx[2]:idx[3]]
	var keys []string
	for _, row := range parseSimpleYamlKeys(block) {
		key, line := row[0], row[1]
		keys = append(keys, key)
		if aiFrontmatterKeys[strings.ToLower(key)] || aiMetaNameRe.MatchString(key) {
			hasAI = true
			findings = append(findings, "frontmatter key: "+key)
		}
		val := ""
		if idx := strings.Index(line, ":"); idx >= 0 {
			val = line[idx+1:]
		}
		if aiMetaNameRe.MatchString(val) {
			hasAI = true
			findings = append(findings, "frontmatter value hit on "+key)
		}
	}
	c2pa := false
	for _, f := range findings {
		lf := strings.ToLower(f)
		if strings.Contains(lf, "c2pa") || strings.Contains(lf, "content") {
			c2pa = true
		}
	}
	return c2pa, hasAI, findings, map[string]interface{}{"has_frontmatter": true, "keys": keys}
}

func cleanMarkdown(text string) (string, []string) {
	var actions []string
	idx := fmRe.FindStringSubmatchIndex(text)
	if idx == nil {
		return text, []string{"no YAML frontmatter"}
	}
	block := text[idx[2]:idx[3]]
	body := text[idx[1]:]
	var kept []string
	dropping := false // inside the nested block of a dropped top-level key
	for _, line := range pySplitLines(block) {
		stripped := strings.TrimSpace(line)

		// Blank lines and comments belong to whichever block we are inside.
		if stripped == "" || strings.HasPrefix(stripped, "#") {
			if !dropping {
				kept = append(kept, line)
			}
			continue
		}

		// Continuation lines (nested mappings, list items) follow their parent.
		first := line[0]
		if first == ' ' || first == '\t' || first == '-' {
			if !dropping {
				kept = append(kept, line)
			}
			continue
		}

		km := yamlKeyLineRe.FindStringSubmatch(line)
		if km == nil {
			dropping = false
			kept = append(kept, line)
			continue
		}

		key := km[1]
		val := ""
		if idx := strings.Index(line, ":"); idx >= 0 {
			val = line[idx+1:]
		}
		if aiFrontmatterKeys[strings.ToLower(key)] || aiMetaNameRe.MatchString(key) {
			actions = append(actions, "drop frontmatter key: "+key)
			dropping = true
			continue
		}
		if aiMetaNameRe.MatchString(val) {
			actions = append(actions, "drop frontmatter key (value hit): "+key)
			dropping = true
			continue
		}

		dropping = false
		kept = append(kept, line)
	}
	if len(actions) == 0 {
		actions = append(actions, "no AI frontmatter keys removed")
	}
	newBlock := strings.Trim(strings.Join(kept, "\n"), "\n")
	var out string
	if newBlock != "" {
		out = "---\n" + newBlock + "\n---\n" + body
	} else {
		out = strings.TrimLeft(body, "\n")
		actions = append(actions, "removed empty frontmatter block")
	}
	return out, actions
}

var yamlKeyLineRe = regexp.MustCompile(`^([A-Za-z0-9_.-]+)\s*:`)

// ---------------------------------------------------------------------------
// HTML
// ---------------------------------------------------------------------------

var metaTagRe = regexp.MustCompile(`(?i)<meta\b[^>]*>`)
var metaAttrRe = regexp.MustCompile(`(?i)(name|property|content|generator)\s*=\s*["']([^"']*)["']`)

// Known AI vendor names for the "generator" meta tag. A plain CMS generator
// (WordPress, Elementor) is CMS provenance, not AI-generator metadata.
var generatorAIRe = regexp.MustCompile(`(?i)claude|anthropic|openai|chatgpt|gemini|synthid|copilot|midjourney|dall.?e|` +
	`stable.?diffusion|sdxl|comfyui|automatic1111|black.?forest|flux1|flux\.1|` +
	`ideogram|recraft|leonardo\.ai|gpt-image|imagen|adobe firefly|grok|` +
	`doubao|豆包|jimeng|即梦|dreamina|hunyuan|混元|通义万相|wanx|kling|可灵|` +
	`cogview|智谱|文心|ernie.?vilg|hailuo|海螺|vidu|pixverse`)

func metaAttrs(tag string) map[string]string {
	attrs := map[string]string{}
	for _, m := range metaAttrRe.FindAllStringSubmatch(tag, -1) {
		attrs[strings.ToLower(m[1])] = m[2]
	}
	return attrs
}

// isCMSGeneratorMeta returns True for a generator meta tag that is CMS
// provenance, not AI. Mirrors _is_cms_generator_meta: the first non-empty of
// name / property / generator decides — they are alternatives, not a
// concatenation.
func isCMSGeneratorMeta(tag string) bool {
	attrs := metaAttrs(tag)
	nameOrProp := ""
	for _, k := range []string{"name", "property", "generator"} {
		if v, ok := attrs[k]; ok && v != "" {
			nameOrProp = strings.ToLower(v)
			break
		}
	}
	if nameOrProp != "generator" {
		return false
	}
	if generatorAIRe.MatchString(attrs["content"]) || generatorAIRe.MatchString(tag) {
		return false
	}
	return true
}

var jsonLdRe = regexp.MustCompile(`(?is)<script\b[^>]*type\s*=\s*["']application/ld\+json["'][^>]*>.*?</script>`)

func inspectHTML(text string) (bool, bool, []string, map[string]interface{}) {
	var findings []string
	hasAI := false
	hasC2pa := false
	for _, tag := range metaTagRe.FindAllString(text, -1) {
		if c2paCredRe.MatchString(tag) {
			hasC2pa = true
		}
		if isCMSGeneratorMeta(tag) {
			findings = append(findings, "info: cms generator: "+trunc(tag, 120))
			continue
		}
		if aiMetaNameRe.MatchString(tag) || aiHintInTag(tag) {
			hasAI = true
			findings = append(findings, "meta: "+trunc(tag, 120))
		}
	}
	for _, blob := range jsonLdRe.FindAllString(text, -1) {
		if aiMetaNameRe.MatchString(blob) || aiLdRe.MatchString(blob) {
			hasAI = true
			findings = append(findings, "json-ld provenance-like block")
			// Python checks the stricter "c2pa|contentcredential" (no .?
			// wildcard) inside json-ld blobs, unlike meta tags.
			if jsonLdC2paRe.MatchString(blob) {
				hasC2pa = true
			}
		}
	}
	// data-ai* attributes (inspect uses the \b word-boundary pattern)
	for _, m := range dataAIInspectRe.FindAllString(text, -1) {
		hasAI = true
		findings = append(findings, "attr: "+trunc(m, 80))
	}
	return hasC2pa, hasAI, findings, map[string]interface{}{}
}

func cleanHTML(text string) (string, []string) {
	var actions []string

	metaSub := func(tag string) string {
		if isCMSGeneratorMeta(tag) {
			return tag
		}
		if aiMetaNameRe.MatchString(tag) || htmlGenRe.MatchString(tag) {
			actions = append(actions, "drop meta: "+trunc(tag, 80))
			return ""
		}
		return tag
	}
	out := metaTagRe.ReplaceAllStringFunc(text, metaSub)

	jsonLdSub := func(blob string) string {
		if aiMetaNameRe.MatchString(blob) || aiLdRe.MatchString(blob) {
			actions = append(actions, "drop json-ld provenance-like script")
			return ""
		}
		return blob
	}
	out = jsonLdRe.ReplaceAllStringFunc(out, jsonLdSub)

	if n := countReplace(out, dataAIRe, ""); n > 0 {
		actions = append(actions, "drop data-ai* attributes x"+itoa(n))
		out = dataAIRe.ReplaceAllString(out, "")
	}
	if len(actions) == 0 {
		actions = append(actions, "no HTML AI meta removed")
	}
	return out, actions
}

var c2paCredRe = regexp.MustCompile(`(?i)c2pa|content.?credential`)
var jsonLdC2paRe = regexp.MustCompile(`(?i)c2pa|contentcredential`)
var aiLdRe = regexp.MustCompile(`(?i)DigitalSourceType|trainedAlgorithmicMedia|SoftwareAgent`)
var dataAIInspectRe = regexp.MustCompile(`(?i)\bdata-ai[\w-]*\s*=\s*["'][^"']*["']`)
var dataAIRe = regexp.MustCompile(`(?i)\sdata-ai[\w-]*\s*=\s*["'][^"']*["']`)
var htmlGenRe = regexp.MustCompile(`(?i)generator|claude|anthropic|openai|gemini|synthid|c2pa|aigc`)

func aiHintInTag(tag string) bool {
	tl := strings.ToLower(tag)
	for _, h := range aiMetaHints[:12] {
		if strings.Contains(tl, strings.ToLower(string(h))) {
			return true
		}
	}
	return false
}

// trunc truncates s to at most n characters (Python str[:n]), never splitting
// a UTF-8 sequence.
func trunc(s string, n int) string {
	if len(s) <= n {
		return s
	}
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// pySplitLines mirrors Python str.splitlines(): splits on \n, \r, \r\n,
// \v, \f, \x1c, \x1d, \x1e, \x85, \u2028 and \u2029.
func pySplitLines(s string) []string {
	var lines []string
	start := 0
	rs := PyRunes(s)
	for i := 0; i < len(rs); i++ {
		switch rs[i] {
		case '\n', '\v', '\f', 0x1c, 0x1d, 0x1e, 0x85, 0x2028, 0x2029:
			lines = append(lines, PyStringFromRunes(rs[start:i]))
			start = i + 1
		case '\r':
			if i+1 < len(rs) && rs[i+1] == '\n' {
				i++
			}
			lines = append(lines, PyStringFromRunes(rs[start:i]))
			start = i + 1
		}
	}
	lines = append(lines, PyStringFromRunes(rs[start:]))
	return lines
}

func countReplace(s string, re *regexp.Regexp, _ string) int {
	return len(re.FindAllString(s, -1))
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	var b []byte
	for i > 0 {
		b = append(b, byte('0'+i%10))
		i /= 10
	}
	if neg {
		b = append(b, '-')
	}
	for l, r := 0, len(b)-1; l < r; l, r = l+1, r-1 {
		b[l], b[r] = b[r], b[l]
	}
	return string(b)
}

var _ = os.Getenv
