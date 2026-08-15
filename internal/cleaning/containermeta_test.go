// Behavioral parity tests mirroring tests/test_container_meta.py.
package cleaning

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Markdown frontmatter
// ---------------------------------------------------------------------------

func TestMarkdownFrontmatter(t *testing.T) {
	text := "---\ntitle: Hello\ngenerator: Claude\nai_generated: true\n---\nBody\u200b text.\n"
	_, hasAI, findings, _ := inspectMarkdown(text)
	if !hasAI {
		t.Fatalf("inspectMarkdown: expected has_ai, findings=%v", findings)
	}
	found := false
	for _, f := range findings {
		if strings.Contains(f, "generator") || strings.Contains(strings.ToLower(f), "ai") {
			found = true
		}
	}
	if !found {
		t.Fatalf("inspectMarkdown: findings should mention generator/ai, got %v", findings)
	}
	cleaned, actions := cleanMarkdown(text)
	if strings.Contains(cleaned, "generator:") {
		t.Errorf("cleaned still has generator: %q", cleaned)
	}
	if strings.Contains(cleaned, "ai_generated:") {
		t.Errorf("cleaned still has ai_generated: %q", cleaned)
	}
	if !strings.Contains(cleaned, "title: Hello") {
		t.Errorf("cleaned lost title: %q", cleaned)
	}
	hasDrop := false
	for _, a := range actions {
		if strings.Contains(a, "drop") {
			hasDrop = true
		}
	}
	if !hasDrop {
		t.Errorf("actions should contain a drop, got %v", actions)
	}
}

func TestMarkdownFrontmatterBlankLineRegression(t *testing.T) {
	text := "---\ntitle: Demo\n\nauthor: you\n---\nBody\n"
	cleaned, actions := cleanMarkdown(text)
	if !strings.Contains(cleaned, "title: Demo") {
		t.Errorf("cleaned lost title: %q", cleaned)
	}
	if !strings.Contains(cleaned, "author: you") {
		t.Errorf("cleaned lost author: %q", cleaned)
	}
	if len(actions) == 0 {
		t.Errorf("actions should be non-empty, got %v", actions)
	}
}

func TestMarkdownDropsNestedChildren(t *testing.T) {
	text := "---\ntitle: Demo\nmodel:\n  name: claude-opus\n  version: 4\nauthor: you\n---\nBody\n"
	cleaned, actions := cleanMarkdown(text)
	if strings.Contains(cleaned, "claude-opus") {
		t.Errorf("nested leak: %q", cleaned)
	}
	if strings.Contains(cleaned, "version: 4") {
		t.Errorf("nested leak: %q", cleaned)
	}
	if !strings.Contains(cleaned, "title: Demo") {
		t.Errorf("sibling dropped: %q", cleaned)
	}
	if !strings.Contains(cleaned, "author: you") {
		t.Errorf("sibling dropped: %q", cleaned)
	}
	found := false
	for _, a := range actions {
		if a == "drop frontmatter key: model" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected action 'drop frontmatter key: model', got %v", actions)
	}
}

func TestMarkdownCleanRoundTrip(t *testing.T) {
	text := "---\ntitle: Demo\nmodel:\n  name: claude-opus\ngenerator: Claude\n---\nBody\n"
	cleaned, _ := cleanMarkdown(text)
	_, hasAI, findings, _ := inspectMarkdown(cleaned)
	if hasAI {
		t.Fatalf("round-trip still flagged, findings=%v", findings)
	}
}

func TestMarkdownPreservesCommentsAndNonAIKeys(t *testing.T) {
	text := "---\n# editorial notes\ntitle: Demo\ntags:\n  - one\n  - two\n---\nBody\n"
	cleaned, _ := cleanMarkdown(text)
	for _, want := range []string{"# editorial notes", "- one", "- two"} {
		if !strings.Contains(cleaned, want) {
			t.Errorf("cleaned lost %q: %q", want, cleaned)
		}
	}
}

// ---------------------------------------------------------------------------
// HTML
// ---------------------------------------------------------------------------

func TestHTMLMetaStrip(t *testing.T) {
	html := `<html><head>
<meta name="generator" content="ChatGPT">
<meta name="viewport" content="width=device-width">
<meta name="description" content="ok">
</head><body data-ai-model="gpt">Hi</body></html>`
	_, hasAI, findings, _ := inspectHTML(html)
	if !hasAI {
		t.Fatalf("inspectHTML: expected has_ai, findings=%v", findings)
	}
	cleaned, actions := cleanHTML(html)
	if strings.Contains(cleaned, "ChatGPT") {
		t.Errorf("cleaned still has ChatGPT: %q", cleaned)
	}
	if !strings.Contains(cleaned, "viewport") {
		t.Errorf("cleaned lost viewport: %q", cleaned)
	}
	if strings.Contains(cleaned, "data-ai-model") {
		t.Errorf("cleaned still has data-ai-model: %q", cleaned)
	}
	hasDrop := false
	for _, a := range actions {
		if strings.Contains(a, "drop") {
			hasDrop = true
		}
	}
	if !hasDrop {
		t.Errorf("actions should contain a drop, got %v", actions)
	}
}

func TestHTMLCMSGeneratorNotAI(t *testing.T) {
	html := `<meta name="generator" content="WordPress 6.0">`
	hasC2PA, hasAI, findings, _ := inspectHTML(html)
	if hasC2PA || hasAI {
		t.Fatalf("CMS generator misflagged: c2pa=%v ai=%v findings=%v", hasC2PA, hasAI, findings)
	}
	found := false
	for _, f := range findings {
		if strings.Contains(f, "cms") {
			found = true
		}
	}
	if !found {
		t.Errorf("findings should mention cms, got %v", findings)
	}
}

func TestHTMLCMSGeneratorPreservedByClean(t *testing.T) {
	html := `<html><head><meta name="generator" content="WordPress 6.0"><meta name="viewport" content="width=device-width"></head></html>`
	cleaned, _ := cleanHTML(html)
	if !strings.Contains(cleaned, "WordPress") {
		t.Errorf("CMS generator dropped: %q", cleaned)
	}
	if !strings.Contains(cleaned, "viewport") {
		t.Errorf("cleaned lost viewport: %q", cleaned)
	}
}

func TestHTMLAttrNamesCaseInsensitive(t *testing.T) {
	for _, html := range []string{
		`<META NAME="generator" CONTENT="WordPress 6.0">`,
		`<meta Name="generator" Content="WordPress 6.0">`,
	} {
		hasC2PA, hasAI, findings, _ := inspectHTML(html)
		if hasC2PA || hasAI {
			t.Fatalf("CMS misflagged for %q: c2pa=%v ai=%v", html, hasC2PA, hasAI)
		}
		found := false
		for _, f := range findings {
			if strings.Contains(f, "cms") {
				found = true
			}
		}
		if !found {
			t.Errorf("findings should mention cms for %q, got %v", html, findings)
		}
		if got, _ := cleanHTML(html); got != html {
			t.Errorf("CMS clean changed input %q -> %q", html, got)
		}
	}

	aiHTML := `<META NAME="generator" CONTENT="Claude">`
	if _, hasAI, _, _ := inspectHTML(aiHTML); !hasAI {
		t.Errorf("AI generator not flagged: %q", aiHTML)
	}
	if got, _ := cleanHTML(aiHTML); got != "" {
		t.Errorf("AI generator survived clean: %q -> %q", aiHTML, got)
	}
}

func TestHTMLAIGeneratorDropped(t *testing.T) {
	html := `<meta name="generator" content="Claude">`
	cleaned, actions := cleanHTML(html)
	if strings.Contains(cleaned, "Claude") {
		t.Errorf("AI generator survived: %q", cleaned)
	}
	hasDrop := false
	for _, a := range actions {
		if strings.Contains(a, "drop") {
			hasDrop = true
		}
	}
	if !hasDrop {
		t.Errorf("actions should contain a drop, got %v", actions)
	}
}

// ---------------------------------------------------------------------------
// PDF
// ---------------------------------------------------------------------------

func TestPDFStreamByteCollisionNotAI(t *testing.T) {
	pdf := []byte("%PDF-1.4\n1 0 obj<< /Length 4 >>stream\nAIGC\nendstream\nendobj\n%%EOF\n")
	dir := t.TempDir()
	src := filepath.Join(dir, "collision.pdf")
	if err := os.WriteFile(src, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	hasC2PA, hasAI, _, _ := inspectPDF(src, pdf)
	if hasC2PA || hasAI {
		t.Fatalf("stream byte collision misflagged: c2pa=%v ai=%v", hasC2PA, hasAI)
	}
}

func TestPDFDegradedCleanWithoutCrash(t *testing.T) {
	xmp := []byte("<?xpacket begin='' id='W5M0MpCehiHzreSzNTczkc9d'?>" +
		"<x:xmpmeta xmlns:x='adobe:ns:meta/'>" +
		"<rdf:RDF xmlns:rdf='http://www.w3.org/1999/02/22-rdf-syntax-ns#'>" +
		"<rdf:Description>" +
		"<digitalSourceType>trainedAlgorithmicMedia</digitalSourceType>" +
		"</rdf:Description></rdf:RDF></x:xmpmeta>" +
		"<?xpacket end='w'?>")
	pdf := append([]byte("%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n"), xmp...)
	pdf = append(pdf, []byte("\n%%EOF\n")...)
	dir := t.TempDir()
	src := filepath.Join(dir, "t.pdf")
	dest := filepath.Join(dir, "t.cleaned.pdf")
	if err := os.WriteFile(src, pdf, 0o644); err != nil {
		t.Fatal(err)
	}
	hasC2PA, hasAI, findings, _ := inspectPDF(src, pdf)
	if !hasAI && !hasC2PA && len(findings) == 0 {
		t.Fatalf("expected findings from XMP packet, got c2pa=%v ai=%v findings=%v", hasC2PA, hasAI, findings)
	}
	actions, meta, err := cleanPDF(src, dest)
	if err != nil {
		t.Fatalf("cleanPDF: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("dest missing: %v", err)
	}
	if len(actions) == 0 {
		t.Errorf("expected non-empty actions, got %v", actions)
	}
	if mode, _ := meta["mode"].(string); mode != "exiftool" && mode != "stdlib-xmp" && mode != "copy" {
		t.Errorf("unexpected mode %v", meta["mode"])
	}
}

// ---------------------------------------------------------------------------
// SVG
// ---------------------------------------------------------------------------

func TestSVGMetadata(t *testing.T) {
	svg := []byte(`<?xml version="1.0"?>
<svg xmlns="http://www.w3.org/2000/svg">
  <metadata>c2pa contentcredentials Anthropic</metadata>
  <circle cx="1" cy="1" r="1"/>
</svg>`)
	hasC2PA, hasAI, findings, _ := inspectSVG(svg)
	if !hasC2PA && !hasAI {
		t.Fatalf("SVG metadata not flagged: c2pa=%v ai=%v findings=%v", hasC2PA, hasAI, findings)
	}
	cleaned, actions := cleanSVG(svg)
	low := strings.ToLower(string(cleaned))
	if strings.Contains(low, "<metadata") && strings.Contains(low, "c2pa") {
		t.Errorf("SVG metadata survived: %q", cleaned)
	}
	if !strings.Contains(string(cleaned), "<circle") {
		t.Errorf("cleaned lost circle: %q", cleaned)
	}
	found := false
	for _, a := range actions {
		if strings.Contains(a, "metadata") || strings.Contains(a, "drop") {
			found = true
		}
	}
	if !found {
		t.Errorf("actions should mention metadata/drop, got %v", actions)
	}
}

// ---------------------------------------------------------------------------
// DOCX / ODT (in-memory zip construction)
// ---------------------------------------------------------------------------

func makeDocxWithApp(t *testing.T, appName string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write("[Content_Types].xml", `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
  <Override PartName="/customXml/item1.xml" ContentType="application/xml"/>
</Types>`)
	write("word/document.xml", `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Hello</w:t></w:r></w:p></w:body></w:document>`)
	write("docProps/app.xml", `<?xml version="1.0"?><Properties><Application>`+appName+`</Application></Properties>`)
	write("customXml/item1.xml", `<?xml version="1.0"?><root>c2pa contentcredentials</root>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeDocxWithBody(t *testing.T, bodyText string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write("[Content_Types].xml", `<?xml version="1.0"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
</Types>`)
	write("word/document.xml", `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>`+bodyText+`</w:t></w:r></w:p></w:body></w:document>`)
	write("docProps/core.xml", `<?xml version="1.0"?><cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties"></cp:coreProperties>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func makeOdt(t *testing.T, generator string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write("mimetype", "application/vnd.oasis.opendocument.text")
	write("meta.xml", `<?xml version="1.0"?><office:document-meta xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:meta="urn:oasis:names:tc:opendocument:xmlns:meta:1.0"><meta:generator>`+generator+`</meta:generator></office:document-meta>`)
	write("content.xml", `<?xml version="1.0"?><office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0"/>`)
	write("META-INF/manifest.xml", `<?xml version="1.0"?><manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0"/>`)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDocxStripsAppAndCustomXml(t *testing.T) {
	data := makeDocxWithApp(t, "Claude AI Writer")
	cleaned, actions, err := cleanDocx(data)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range actions {
		if strings.Contains(a, "customXml") || strings.Contains(a, "Application") || strings.Contains(a, "drop") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected customXml/Application/drop actions, got %v", actions)
	}
	zr, err := zip.NewReader(bytes.NewReader(cleaned), int64(len(cleaned)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, f := range zr.File {
		names[f.Name] = true
	}
	if !names["word/document.xml"] {
		t.Errorf("document.xml missing from cleaned zip")
	}
	for n := range names {
		if strings.HasPrefix(n, "customXml/") {
			t.Errorf("customXml part survived: %s", n)
		}
	}
	rc, err := zr.Open("docProps/app.xml")
	if err != nil {
		t.Fatal(err)
	}
	appBytes, _ := io.ReadAll(rc)
	rc.Close()
	if strings.Contains(string(appBytes), "Claude") {
		t.Errorf("app.xml still has Claude: %s", appBytes)
	}
}

func TestDocxBodyVendorWordNotAI(t *testing.T) {
	data := makeDocxWithBody(t, "Claude wrote this.")
	hasC2PA, hasAI, findings, _, err := inspectDocx(data)
	if err != nil {
		t.Fatal(err)
	}
	if hasC2PA || hasAI {
		t.Fatalf("body vendor word misflagged: c2pa=%v ai=%v findings=%v", hasC2PA, hasAI, findings)
	}
	for _, f := range findings {
		if strings.Contains(f, "Claude") {
			t.Errorf("findings mention body word: %v", findings)
		}
	}
}

func TestDocxMetadataVendorWordFlagged(t *testing.T) {
	data := makeDocxWithApp(t, "Claude AI Writer")
	hasC2PA, hasAI, findings, _, err := inspectDocx(data)
	if err != nil {
		t.Fatal(err)
	}
	if !hasAI {
		t.Fatalf("metadata vendor word not flagged: c2pa=%v ai=%v findings=%v", hasC2PA, hasAI, findings)
	}
	found := false
	for _, f := range findings {
		if strings.Contains(f, "Claude") {
			found = true
		}
	}
	if !found {
		t.Errorf("findings should mention Claude, got %v", findings)
	}
}

func TestOdtDropsGenerator(t *testing.T) {
	data := makeOdt(t, "Anthropic Claude")
	cleaned, actions, err := cleanOdt(data)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range actions {
		if strings.Contains(a, "generator") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected generator action, got %v", actions)
	}
	zr, err := zip.NewReader(bytes.NewReader(cleaned), int64(len(cleaned)))
	if err != nil {
		t.Fatal(err)
	}
	rc, err := zr.Open("meta.xml")
	if err != nil {
		t.Fatal(err)
	}
	metaBytes, _ := io.ReadAll(rc)
	rc.Close()
	meta := string(metaBytes)
	if strings.Contains(meta, "Claude") {
		t.Errorf("meta.xml still has Claude: %s", meta)
	}
	if strings.Contains(meta, "meta:generator") && strings.Contains(meta, "Anthropic") {
		t.Errorf("meta:generator survived: %s", meta)
	}
}

// ---------------------------------------------------------------------------
// clean_container / inspect_container end-to-end
// ---------------------------------------------------------------------------

func TestCleanContainerMarkdownFile(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "x.md")
	if err := os.WriteFile(src, []byte("---\ngenerator: OpenAI\n---\nHi\u200b\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "x.cleaned.md")
	result, err := CleanContainer(src, dest, CleanContainerOptions{SkipLayerAText: false})
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "generator") {
		t.Errorf("cleaned still has generator: %q", body)
	}
	if strings.Contains(string(body), "\u200b") {
		t.Errorf("cleaned still has ZWSP: %q", body)
	}
	if fmt, _ := result["format"].(string); fmt != "markdown" {
		t.Errorf("format = %v, want markdown", result["format"])
	}
}

func TestInspectContainerSVG(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "a.svg")
	if err := os.WriteFile(src, []byte(`<svg xmlns="http://www.w3.org/2000/svg"><metadata>c2pa</metadata></svg>`), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := InspectContainer(src)
	if err != nil {
		t.Fatal(err)
	}
	if report.Format != "svg" {
		t.Errorf("format = %q, want svg", report.Format)
	}
	if !report.HasC2PA && !report.HasAIMetadata {
		t.Errorf("SVG c2pa not flagged: %+v", report)
	}
}

func TestFixturesRoundTrip(t *testing.T) {
	fixtures := filepath.Join("..", "..", "..", "tests", "fixtures")
	if _, err := os.Stat(fixtures); err != nil {
		t.Skipf("fixtures dir not found: %v", err)
	}
	for _, name := range []string{"sample_ai.md", "sample_ai.html", "sample_meta.svg"} {
		src := filepath.Join(fixtures, name)
		dest := filepath.Join(t.TempDir(), name+".cleaned")
		result, err := CleanContainer(src, dest, CleanContainerOptions{})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if _, err := os.Stat(dest); err != nil {
			t.Fatalf("%s: dest missing", name)
		}
		if fmt, _ := result["format"].(string); fmt != "markdown" && fmt != "html" && fmt != "svg" {
			t.Errorf("%s: format = %v", name, result["format"])
		}
		body, _ := os.ReadFile(dest)
		low := strings.ToLower(string(body))
		if strings.Contains(low, "chatgpt") {
			t.Errorf("%s: cleaned still contains chatgpt", name)
		}
		if strings.Contains(low, "generator: claude") {
			t.Errorf("%s: cleaned still contains 'generator: claude'", name)
		}
	}
}

// TestIsCMSGeneratorMetaAlternatives locks in the _is_cms_generator_meta
// semantics: name/property/generator are alternatives (first non-empty
// wins), never a concatenation.
func TestIsCMSGeneratorMetaAlternatives(t *testing.T) {
	if !isCMSGeneratorMeta(`<meta name="generator" content="WordPress 6.4">`) {
		t.Error("plain CMS generator should be CMS provenance")
	}
	if !isCMSGeneratorMeta(`<meta name="generator" property="og:title" content="WordPress">`) {
		t.Error("name=generator must win over property (Python first-non-empty; the old concatenation bug deleted such tags as AI meta)")
	}
	if isCMSGeneratorMeta(`<meta name="description" content="x">`) {
		t.Error("non-generator meta is not a CMS generator tag")
	}
	if isCMSGeneratorMeta(`<meta name="generator" content="Generated by Claude">`) {
		t.Error("AI generator content must not be treated as CMS")
	}
}

// TestZipBudgetOverflowsAsValueError mirrors the Python contract: the zip
// bomb guard raises (ValueError) instead of reporting a clean file.
func TestZipBudgetOverflowsAsValueError(t *testing.T) {
	var budget int64
	err := checkZipBudget(uint64(maxZipDecompressedBytes)+1, &budget)
	if err == nil {
		t.Fatal("budget overflow must error")
	}
	var ve *ValueError
	if !errors.As(err, &ve) {
		t.Fatalf("budget overflow must be a *ValueError, got %T", err)
	}
}
