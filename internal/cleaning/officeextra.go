package cleaning

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"strings"
)

// PPTX/XLSX are OOXML zip containers like DOCX. Reuse zip budget checks and
// provenance scanning on metadata parts.

func isOOXMLMetaPart(name string) bool {
	return strings.HasPrefix(name, "docProps/") || strings.HasPrefix(name, "customXml/")
}

func inspectOOXML(data []byte, kind string) (bool, bool, []string, map[string]interface{}, error) {
	var findings []string
	hasC2pa, hasAI := false, false
	budget := int64(0)
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return false, false, []string{"not a valid " + kind + " zip"}, map[string]interface{}{}, nil
	}
	parts := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		parts = append(parts, f.Name)
	}
	for _, f := range zr.File {
		if err := checkZipBudget(f.UncompressedSize64, &budget); err != nil {
			return false, false, nil, nil, err
		}
		if !isOOXMLMetaPart(f.Name) {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return false, false, []string{"not a valid " + kind + " zip"}, map[string]interface{}{}, nil
		}
		raw, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return false, false, []string{"not a valid " + kind + " zip"}, map[string]interface{}{}, nil
		}
		c2, ai, hits := blobHits(raw)
		if c2 {
			hasC2pa = true
		}
		if ai {
			hasAI = true
		}
		if len(hits) > 0 {
			if len(hits) > 6 {
				hits = hits[:6]
			}
			findings = append(findings, f.Name+": "+strings.Join(hits, ", "))
		}
	}
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

func cleanOOXML(data []byte) ([]byte, []string, error) {
	var actions []string
	var outBuf bytes.Buffer
	budget := int64(0)
	zin, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, nil, errors.New("not a valid OOXML zip")
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
		if strings.HasPrefix(name, "customXml/") {
			actions = append(actions, "drop part "+name)
			continue
		}
		if strings.HasPrefix(name, "docProps/") {
			text := string(bytesToValidUTF8(raw))
			newText := text
			for _, p := range docxScrubPats {
				newText = replaceGroups(newText, p.re, func(groups []string) string {
					inner := groups[2]
					if aiMetaNameRe.MatchString(inner) || aiMetaNameRe.MatchString(p.label) {
						actions = append(actions, "scrub "+name+" field "+p.label)
						return groups[1] + groups[3]
					}
					if (p.label == "Application" || p.label == "AppVersion") && appAIRe.MatchString(inner) {
						actions = append(actions, "scrub "+name+" field "+p.label)
						return groups[1] + groups[3]
					}
					return groups[0]
				})
			}
			if strings.HasSuffix(name, "custom.xml") {
				_, ai, _ := blobHits(raw)
				if ai || aiMetaNameRe.MatchString(text) {
					actions = append(actions, "drop part "+name)
					continue
				}
			}
			raw = []byte(newText)
		}
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
		actions = append(actions, "no OOXML metadata parts removed")
	}
	return outBuf.Bytes(), actions, nil
}

func inspectPptx(data []byte) (bool, bool, []string, map[string]interface{}, error) {
	return inspectOOXML(data, "PPTX")
}
func inspectXlsx(data []byte) (bool, bool, []string, map[string]interface{}, error) {
	return inspectOOXML(data, "XLSX")
}
func cleanPptx(data []byte) ([]byte, []string, error) { return cleanOOXML(data) }
func cleanXlsx(data []byte) ([]byte, []string, error) { return cleanOOXML(data) }
