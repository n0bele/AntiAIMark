// Unified container API — port of inspect_container / clean_container.
package cleaning

import (
	"os"
	"path/filepath"
)

// InspectContainer ports inspect_container(path) -> ContainerInspectReport.
func InspectContainer(path string) (ContainerInspectReport, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ContainerInspectReport{}, err
	}
	fmt := DetectContainerFormat(path, data)
	var tools map[string]interface{}
	details := map[string]interface{}{}
	var hasC2pa, hasAI bool
	var findings []string

	switch fmt {
	case "svg":
		hasC2pa, hasAI, findings, details = inspectSVG(data)
	case "pdf":
		hasC2pa, hasAI, findings, details = inspectPDF(path, data)
		if t, ok := details["tools"]; ok {
			tools, _ = t.(map[string]interface{})
		}
		delete(details, "tools")
	case "docx":
		var err error
		hasC2pa, hasAI, findings, details, err = inspectDocx(data)
		if err != nil {
			return ContainerInspectReport{}, err
		}
	case "odt":
		var err error
		hasC2pa, hasAI, findings, details, err = inspectOdt(data)
		if err != nil {
			return ContainerInspectReport{}, err
		}
	case "html":
		text := string(bytesToValidUTF8(data))
		hasC2pa, hasAI, findings, details = inspectHTML(text)
	case "markdown":
		text := string(bytesToValidUTF8(data))
		hasC2pa, hasAI, findings, details = inspectMarkdown(text)
	default:
		if isVideoFormat(fmt) {
			hasC2pa, hasAI, findings, details = inspectVideo(data, fmt)
		} else {
			hasC2pa, hasAI, findings = false, false, []string{"unsupported container: " + fmt}
			details = map[string]interface{}{"unsupported": true}
		}
	}

	notes := []string{}
	if fmt == "pdf" {
		notes = append(notes, "PDF inspection is best-effort; exiftool/c2patool give more reliable metadata detection")
	} else if fmt == "docx" {
		notes = append(notes, "DOCX: only metadata/provenance parts are scanned; visible body text is ignored")
	} else if isVideoFormat(fmt) {
		notes = append(notes, "video inspection is best-effort (Go web extension)")
	}
	if unsupported, _ := details["unsupported"].(bool); unsupported {
		notes = append(notes, "format not fully inspected: "+fmt)
	}

	if (fmt == "svg" || fmt == "pdf" || fmt == "docx") && len(tools) == 0 {
		tools = RunOptionalTools(path)
	}

	return ContainerInspectReport{
		Path:          path,
		Format:        fmt,
		HasC2PA:       hasC2pa,
		HasAIMetadata: hasAI,
		Findings:      findings,
		Tools:         tools,
		Details:       details,
		Notes:         notes,
	}, nil
}

// CleanContainerOptions mirrors clean_container keyword args. The Layer A
// switch is inverted (SkipLayerAText) so the zero-value options struct
// reproduces the Python default also_layer_a_text=True.
type CleanContainerOptions struct {
	SkipLayerAText bool
}

// CleanContainer ports clean_container(path, dest, also_layer_a_text=True).
func CleanContainer(src, dest string, opts CleanContainerOptions) (map[string]interface{}, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, err
	}
	fmt := DetectContainerFormat(src, data)
	var actions []string
	meta := map[string]interface{}{"format": fmt}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, err
	}

	switch fmt {
	case "svg":
		cleaned, a := cleanSVG(data)
		actions = a
		if err := SafeWriteBytes(dest, cleaned); err != nil {
			return nil, err
		}
	case "pdf":
		a, metaExtra, err := cleanPDF(src, dest)
		if err != nil {
			return nil, err
		}
		actions = a
		for k, v := range metaExtra {
			meta[k] = v
		}
	case "docx":
		cleaned, a, err := cleanDocx(data)
		if err != nil {
			return nil, err
		}
		actions = a
		if err := SafeWriteBytes(dest, cleaned); err != nil {
			return nil, err
		}
	case "odt":
		cleaned, a, err := cleanOdt(data)
		if err != nil {
			return nil, err
		}
		actions = a
		if err := SafeWriteBytes(dest, cleaned); err != nil {
			return nil, err
		}
	case "html":
		text := string(data)
		text, actions = cleanHTML(text)
		if !opts.SkipLayerAText {
			res := CleanText(text, false, false, true, false)
			rc := intStat(res.Stats, "removed_count")
			rp := intStat(res.Stats, "replaced_count")
			if rc != 0 || rp != 0 {
				actions = append(actions, "layer A text: removed="+itoa(rc)+" replaced="+itoa(rp))
				text = res.Text
			}
		}
		if err := SafeWriteText(dest, text); err != nil {
			return nil, err
		}
	case "markdown":
		text := string(data)
		text, actions = cleanMarkdown(text)
		if !opts.SkipLayerAText {
			res := CleanText(text, false, false, true, false)
			rc := intStat(res.Stats, "removed_count")
			rp := intStat(res.Stats, "replaced_count")
			if rc != 0 || rp != 0 {
				actions = append(actions, "layer A text: removed="+itoa(rc)+" replaced="+itoa(rp))
				text = res.Text
			}
		}
		if err := SafeWriteText(dest, text); err != nil {
			return nil, err
		}
	default:
		if isVideoFormat(fmt) {
			a, metaExtra, err := cleanVideo(src, dest, fmt)
			if err != nil {
				return nil, err
			}
			actions = a
			for k, v := range metaExtra {
				meta[k] = v
			}
		} else {
			return nil, NewValueError("unsupported container format: " + fmt)
		}
	}

	after, err := InspectContainer(dest)
	if err != nil {
		return nil, err
	}
	fi, err := os.Stat(dest)
	if err != nil {
		return nil, err
	}
	postFindings := after.Findings
	if postFindings == nil {
		postFindings = []string{}
	}
	return map[string]interface{}{
		"input":                 src,
		"output":                dest,
		"format":                fmt,
		"actions":               actions,
		"bytes_in":              len(data),
		"bytes_out":             fi.Size(),
		"still_has_c2pa":        after.HasC2PA,
		"still_has_ai_metadata": after.HasAIMetadata,
		"post_findings":         postFindings,
		"meta":                  meta,
	}, nil
}

func intStat(m map[string]interface{}, key string) int {
	if v, ok := m[key]; ok {
		switch t := v.(type) {
		case int:
			return t
		case int64:
			return int(t)
		case float64:
			return int(t)
		}
	}
	return 0
}

func bytesToValidUTF8(data []byte) []byte {
	out := make([]byte, 0, len(data))
	for len(data) > 0 {
		r, size := decodeRune(data)
		if r == 0xFFFD && size == 1 {
			out = append(out, 0xEF, 0xBF, 0xBD) // U+FFFD replacement
			data = data[1:]
			continue
		}
		out = append(out, data[:size]...)
		data = data[size:]
	}
	return out
}

func decodeRune(b []byte) (rune, int) {
	if len(b) == 0 {
		return 0, 0
	}
	if b[0] < 0x80 {
		return rune(b[0]), 1
	}
	if b[0] >= 0xC2 && b[0] <= 0xDF && len(b) >= 2 {
		c1 := b[1]
		if c1 >= 0x80 && c1 <= 0xBF {
			return (rune(b[0]&0x1F) << 6) | rune(c1&0x3F), 2
		}
	}
	if b[0] >= 0xE0 && b[0] <= 0xEF && len(b) >= 3 {
		c1, c2 := b[1], b[2]
		if c1 >= 0x80 && c1 <= 0xBF && c2 >= 0x80 && c2 <= 0xBF {
			if b[0] == 0xE0 && c1 < 0xA0 {
				return 0xFFFD, 1
			}
			if b[0] == 0xED && c1 >= 0xA0 {
				return 0xFFFD, 1
			}
			return (rune(b[0]&0x0F) << 12) | (rune(c1&0x3F) << 6) | rune(c2&0x3F), 3
		}
	}
	if b[0] >= 0xF0 && b[0] <= 0xF4 && len(b) >= 4 {
		c1, c2, c3 := b[1], b[2], b[3]
		if c1 >= 0x80 && c1 <= 0xBF && c2 >= 0x80 && c2 <= 0xBF && c3 >= 0x80 && c3 <= 0xBF {
			if b[0] == 0xF0 && c1 < 0x90 {
				return 0xFFFD, 1
			}
			if b[0] == 0xF4 && c1 >= 0x90 {
				return 0xFFFD, 1
			}
			return (rune(b[0]&0x07) << 18) | (rune(c1&0x3F) << 12) | (rune(c2&0x3F) << 6) | rune(c3&0x3F), 4
		}
	}
	return 0xFFFD, 1
}
