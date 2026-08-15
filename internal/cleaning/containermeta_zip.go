// DOCX / ODT (zip + XML) inspect/clean — port of container_meta.py.
package cleaning

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"regexp"
	"strings"
)

var docxMetaParts = []string{
	"docProps/core.xml",
	"docProps/app.xml",
	"docProps/custom.xml",
}

var docxCustomPrefixes = []string{"customXml/", "docProps/"}

const maxZipDecompressedBytes = 128 * 1024 * 1024

// checkZipBudget rejects zip bombs before decompression (uncompressed size
// cap). Overflow raises a *ValueError like the Python original, which the
// HTTP server maps to a 400 response.
func checkZipBudget(size uint64, budget *int64) error {
	*budget += int64(size)
	if *budget > maxZipDecompressedBytes {
		return NewValueError("zip decompressed size exceeds cap (" +
			itoa(maxZipDecompressedBytes) + " bytes); refusing to process")
	}
	return nil
}

// isDocxMetaPart returns True for DOCX parts that carry provenance, not visible content.
func isDocxMetaPart(name string) bool {
	return strings.HasPrefix(name, "docProps/") || strings.HasPrefix(name, "customXml/")
}

// replaceGroups mirrors re.sub(pattern, fn, text): fn receives the capture groups.
func replaceGroups(text string, re *regexp.Regexp, fn func(groups []string) string) string {
	loc := re.FindAllStringSubmatchIndex(text, -1)
	if len(loc) == 0 {
		return text
	}
	var b strings.Builder
	last := 0
	for _, m := range loc {
		b.WriteString(text[last:m[0]])
		groups := make([]string, 0, len(m)/2)
		for i := 0; i < len(m); i += 2 {
			if m[i] < 0 {
				groups = append(groups, "")
				continue
			}
			groups = append(groups, text[m[i]:m[i+1]])
		}
		b.WriteString(fn(groups))
		last = m[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

// inspectDocx mirrors inspect_docx. A zip-bomb budget overflow raises
// *ValueError (Python lets it propagate out of inspect_container); a broken
// zip or an unreadable entry collapses to the "not a valid DOCX zip"
// finding, because Python's zf.read raises BadZipFile which the outer
// except catches (discarding earlier findings).
func inspectDocx(data []byte) (bool, bool, []string, map[string]interface{}, error) {
	var findings []string
	hasC2pa := false
	hasAI := false
	budget := int64(0)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false, false, []string{"not a valid DOCX zip"}, map[string]interface{}{}, nil
	}
	parts := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		parts = append(parts, f.Name)
	}
	for _, f := range zr.File {
		if err := checkZipBudget(f.UncompressedSize64, &budget); err != nil {
			return false, false, nil, nil, err
		}
		name := f.Name
		// Only metadata/provenance parts carry AI markers. The visible body
		// (word/*.xml) may legitimately mention vendor names such as "Claude"
		// without being AI-generated metadata.
		if !isDocxMetaPart(name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return false, false, []string{"not a valid DOCX zip"}, map[string]interface{}{}, nil
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return false, false, []string{"not a valid DOCX zip"}, map[string]interface{}{}, nil
		}
		c2, ai, hits := blobHits(raw)
		if c2 || ai {
			if c2 {
				hasC2pa = true
			}
			if ai {
				hasAI = true
			}
			if len(hits) > 6 {
				hits = hits[:6]
			}
			findings = append(findings, name+": "+strings.Join(hits, ", "))
		}
	}
	// always flag customXml presence lightly
	custom := 0
	for _, n := range parts {
		if strings.HasPrefix(n, "customXml/") {
			custom++
		}
	}
	if custom > 0 {
		findings = append(findings, "customXml parts: "+itoa(custom))
	}
	return hasC2pa, hasAI || hasC2pa, findings, map[string]interface{}{"parts": len(parts)}, nil
}

var docxScrubPats = []struct {
	re    *regexp.Regexp
	label string
}{
	{regexp.MustCompile(`(?is)(<dc:creator[^>]*>)(.*?)(</dc:creator>)`), "dc:creator"},
	{regexp.MustCompile(`(?is)(<cp:lastModifiedBy[^>]*>)(.*?)(</cp:lastModifiedBy>)`), "cp:lastModifiedBy"},
	{regexp.MustCompile(`(?is)(<Application[^>]*>)(.*?)(</Application>)`), "Application"},
	{regexp.MustCompile(`(?is)(<AppVersion[^>]*>)(.*?)(</AppVersion>)`), "AppVersion"},
}

var appAIRe = regexp.MustCompile(`(?i)claude|openai|anthropic|gemini|chatgpt|synthid|copilot`)

var contentTypeOverrideRe = regexp.MustCompile(`<Override\b[^>]*PartName="/customXml/[^"]*"[^>]*/>`)

func cleanDocx(data []byte) ([]byte, []string, error) {
	var actions []string
	var outBuf bytes.Buffer
	budget := int64(0)
	zin, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, errors.New("not a valid DOCX zip")
	}
	zw := zip.NewWriter(&outBuf)
	for _, f := range zin.File {
		name := f.Name
		if err := checkZipBudget(f.UncompressedSize64, &budget); err != nil {
			return nil, nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, err
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, nil, err
		}
		// Drop entire customXml trees (often provenance injects); body stays in word/
		if strings.HasPrefix(name, "customXml/") {
			actions = append(actions, "drop part "+name)
			continue
		}
		if name == "docProps/core.xml" || name == "docProps/app.xml" || name == "docProps/custom.xml" || strings.HasPrefix(name, "docProps/") {
			text := string(bytesToValidUTF8(raw)) // errors="replace" per maximal subpart
			newText := text
			for _, p := range docxScrubPats {
				newText = replaceGroups(newText, p.re, func(groups []string) string {
					inner := groups[2]
					if aiMetaNameRe.MatchString(inner) || aiMetaNameRe.MatchString(p.label) {
						actions = append(actions, "scrub "+name+" field "+p.label)
						return groups[1] + groups[3]
					}
					// Always clear Application if it looks like AI
					if (p.label == "Application" || p.label == "AppVersion") && appAIRe.MatchString(inner) {
						actions = append(actions, "scrub "+name+" field "+p.label)
						return groups[1] + groups[3]
					}
					return groups[0]
				})
			}
			// Drop custom.xml entirely if AI-ish
			if strings.HasSuffix(name, "custom.xml") {
				_, ai, _ := blobHits(raw)
				if ai || aiMetaNameRe.MatchString(text) {
					actions = append(actions, "drop part "+name)
					continue
				}
			}
			raw = []byte(newText)
		}
		// content types: leave as-is (removing overrides for dropped customXml is nice-to-have)
		if name == "[Content_Types].xml" {
			text := string(bytesToValidUTF8(raw))
			if n := len(contentTypeOverrideRe.FindAllString(text, -1)); n > 0 {
				actions = append(actions, "drop Content_Types customXml overrides x"+itoa(n))
				text = contentTypeOverrideRe.ReplaceAllString(text, "")
				raw = []byte(text)
			}
		}
		h := cloneZipHeader(f)
		w, err := zw.CreateHeader(&h)
		if err != nil {
			return nil, nil, err
		}
		if _, err := w.Write(raw); err != nil {
			return nil, nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, nil, err
	}
	if len(actions) == 0 {
		actions = append(actions, "no DOCX metadata parts removed")
	}
	return outBuf.Bytes(), actions, nil
}

// cloneZipHeader builds the output FileHeader for a re-written entry.
// Python's writestr(info, raw) reuses the source ZipInfo, preserving the
// entry's non-UTF-8 name flag, external attributes and comment; mirror that
// (raw Extra fields are skipped — the writer manages ZIP64 itself).
func cloneZipHeader(f *zip.File) zip.FileHeader {
	h := zip.FileHeader{Name: f.Name, Method: f.Method}
	h.NonUTF8 = f.NonUTF8
	h.ExternalAttrs = f.ExternalAttrs
	h.Comment = f.Comment
	h.SetModTime(f.Modified)
	h.SetMode(f.Mode())
	return h
}

// inspectOdt mirrors inspect_odt: every part is scanned; budget overflow
// raises *ValueError; broken zip / unreadable entry collapses to the
// "not a valid ODT zip" finding.
func inspectOdt(data []byte) (bool, bool, []string, map[string]interface{}, error) {
	var findings []string
	hasC2pa := false
	hasAI := false
	budget := int64(0)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false, false, []string{"not a valid ODT zip"}, map[string]interface{}{}, nil
	}
	hasMeta := false
	for _, f := range zr.File {
		if err := checkZipBudget(f.UncompressedSize64, &budget); err != nil {
			return false, false, nil, nil, err
		}
		if f.Name == "meta.xml" {
			hasMeta = true
		}
		rc, err := f.Open()
		if err != nil {
			return false, false, []string{"not a valid ODT zip"}, map[string]interface{}{}, nil
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return false, false, []string{"not a valid ODT zip"}, map[string]interface{}{}, nil
		}
		c2, ai, hits := blobHits(raw)
		if c2 || ai {
			if c2 {
				hasC2pa = true
			}
			if ai {
				hasAI = true
			}
			if len(hits) > 6 {
				hits = hits[:6]
			}
			findings = append(findings, f.Name+": "+strings.Join(hits, ", "))
		}
	}
	if hasMeta {
		rc, err := zr.Open("meta.xml")
		if err == nil {
			metaBytes, _ := io.ReadAll(rc)
			rc.Close()
			meta := string(bytesToValidUTF8(metaBytes))
			if odtGenRe.MatchString(meta) {
				hasAI = true
				findings = append(findings, "meta.xml generator-like fields")
			}
		}
	}
	return hasC2pa, hasAI || hasC2pa, findings, map[string]interface{}{}, nil
}

var odtGenRe = regexp.MustCompile(`(?i)generator|claude|openai|anthropic|gemini`)

var metaGeneratorBlockRe = regexp.MustCompile(`(?is)<meta:generator\b[^>]*>.*?</meta:generator\s*>`)
var dcCreatorBlockRe = regexp.MustCompile(`(?is)<dc:creator\b[^>]*>.*?</dc:creator\s*>`)

var odtProtectedParts = map[string]bool{
	"content.xml":           true,
	"styles.xml":            true,
	"mimetype":              true,
	"META-INF/manifest.xml": true,
}

func cleanOdt(data []byte) ([]byte, []string, error) {
	var actions []string
	var outBuf bytes.Buffer
	budget := int64(0)
	zin, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, errors.New("not a valid ODT zip")
	}
	zw := zip.NewWriter(&outBuf)
	for _, f := range zin.File {
		name := f.Name
		if err := checkZipBudget(f.UncompressedSize64, &budget); err != nil {
			return nil, nil, err
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, err
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, nil, err
		}
		if name == "meta.xml" {
			text := string(bytesToValidUTF8(raw))
			if n := len(metaGeneratorBlockRe.FindAllString(text, -1)); n > 0 {
				actions = append(actions, "drop meta:generator")
				text = metaGeneratorBlockRe.ReplaceAllString(text, "")
			}
			// scrub creator-like if AI
			text = dcCreatorBlockRe.ReplaceAllStringFunc(text, func(m string) string {
				if aiMetaNameRe.MatchString(m) {
					actions = append(actions, "scrub creator-like meta")
					return ""
				}
				return m
			})
			raw = []byte(text)
		} else {
			c2, ai, _ := blobHits(raw)
			if (c2 || ai) && !odtProtectedParts[name] {
				actions = append(actions, "drop part "+name+" (AI/C2PA markers)")
				continue
			}
		}
		h := cloneZipHeader(f)
		w, err := zw.CreateHeader(&h)
		if err != nil {
			return nil, nil, err
		}
		if _, err := w.Write(raw); err != nil {
			return nil, nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, nil, err
	}
	if len(actions) == 0 {
		actions = append(actions, "no ODT metadata removed")
	}
	return outBuf.Bytes(), actions, nil
}
