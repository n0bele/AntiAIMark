// PDF inspect/clean — best-effort; prefers exiftool + qpdf when present.
// Port of the PDF section of container_meta.py.
package cleaning

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
)

var xmpPacketRe = regexp.MustCompile(`(?is)<\?xpacket begin.*?<\?xpacket end[^?]*\?>`)
var pdfStreamRe = regexp.MustCompile(`(?s)stream\r?\n.*?endstream`)
var pdfXmpAIRe = regexp.MustCompile(`(?i)digitalSourceType|trainedAlgorithmicMedia|SoftwareAgent|c2pa`)

// pdfStructuredBlob ports _pdf_structured_blob: stream payloads are often
// compressed binary where an AI-marker byte sequence (e.g. "AIGC") can occur
// by chance; scanning only dictionaries and XMP packets avoids false hits.
func pdfStructuredBlob(data []byte) []byte {
	noStreams := pdfStreamRe.ReplaceAll(data, []byte("stream endstream"))
	xmp := bytes.Join(xmpPacketRe.FindAll(data, -1), []byte("\n"))
	out := make([]byte, 0, len(noStreams)+len(xmp)+1)
	out = append(out, noStreams...)
	out = append(out, '\n')
	out = append(out, xmp...)
	return out
}

func inspectPDF(path string, data []byte) (bool, bool, []string, map[string]interface{}) {
	var findings []string
	hasC2pa, hasAI, hits := blobHits(pdfStructuredBlob(data))
	for _, h := range hits {
		findings = append(findings, "pdf-structured:"+h)
	}
	xmpBlob := bytes.Join(xmpPacketRe.FindAll(data, -1), []byte("\n"))
	if len(xmpBlob) > 0 {
		findings = append(findings, "XMP packet present")
		if pdfXmpAIRe.Match(xmpBlob) {
			hasAI = true
		}
	}
	tools := RunOptionalTools(path)
	if ct, ok := tools["c2patool"].(map[string]interface{}); ok {
		if hm, ok := ct["has_manifest"].(bool); ok && hm {
			hasC2pa = true
			findings = append(findings, "c2patool reports C2PA-related manifest")
		}
	}
	return hasC2pa, hasAI || hasC2pa, findings, map[string]interface{}{"tools": tools}
}

// pdfStructuralRewrite ports _pdf_structural_rewrite: rebuild a PDF so
// unreferenced objects are dropped (qpdf), since exiftool edits are
// incremental. No-op with a warning when qpdf is absent.
func pdfStructuralRewrite(dest string, actions []string) bool {
	qpdf := Which("qpdf")
	if qpdf == "" {
		actions = append(actions,
			"warning: exiftool PDF edits are incremental — the original metadata "+
				"bytes remain recoverable; install qpdf for a structural rewrite")
		return false
	}
	tmp := dest + ".qpdf-tmp"
	qpdfArgs := []string{qpdf, "--linearize", "--", SafeArg(dest), SafeArg(tmp)}
	res := runCaptured(qpdfArgs, 120)
	if res.timedOut || res.err != nil {
		os.Remove(tmp)
		reason := pyTimeoutErr(qpdfArgs, 120)
		if !res.timedOut {
			reason = res.err.Error()
		}
		actions = append(actions, "qpdf rewrite failed: "+reason+"; metadata bytes may remain recoverable")
		return false
	}
	// qpdf exit codes: 0 = clean, 3 = succeeded with warnings (output written).
	if (res.code == 0 || res.code == 3) && fileExists(tmp) && fileSize(tmp) > 0 {
		os.Rename(tmp, dest)
		actions = append(actions, "qpdf --linearize structural rewrite (rc="+itoa(res.code)+")")
		return true
	}
	os.Remove(tmp)
	actions = append(actions, "qpdf rewrite skipped (rc="+itoa(res.code)+"); metadata bytes may remain recoverable")
	return false
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func fileSize(p string) int64 {
	fi, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// cleanPDF ports clean_pdf. Returns (actions, meta, error).
func cleanPDF(path, dest string) ([]string, map[string]interface{}, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, nil, err
	}
	var actions []string

	exiftool := Which("exiftool")
	if exiftool != "" {
		if err := SafeWriteBytes(dest, data); err != nil {
			return nil, nil, err
		}
		res := runCaptured([]string{exiftool, "-all=", "-overwrite_original", SafeArg(dest)}, 60)
		if res.timedOut {
			actions = append(actions, "exiftool failed: "+pyTimeoutErr([]string{exiftool, "-all=", "-overwrite_original", SafeArg(dest)}, 60))
		} else if res.err != nil {
			actions = append(actions, "exiftool failed: "+res.err.Error())
		} else {
			actions = append(actions, "exiftool -all= (rc="+itoa(res.code)+")")
		}
		rewritten := pdfStructuralRewrite(dest, actions)
		if Which("c2patool") != "" {
			actions = append(actions, "c2patool available for inspect; strip via exiftool/re-export")
		}
		return actions, map[string]interface{}{"mode": "exiftool", "structural_rewrite": rewritten}, nil
	}

	// Degraded: strip obvious XMP packets between <?xpacket begin and end
	all := xmpPacketRe.FindAll(data, -1)
	if n := len(all); n > 0 {
		newData := xmpPacketRe.ReplaceAll(data, []byte{})
		actions = append(actions, "stripped XMP xpacket x"+itoa(n)+" (degraded; may leave offsets broken)")
		if err := SafeWriteBytes(dest, newData); err != nil {
			return nil, nil, err
		}
		actions = append(actions, "warning: pure-stdlib PDF strip is best-effort; prefer exiftool")
		return actions, map[string]interface{}{"mode": "stdlib-xmp", "degraded": true}, nil
	}

	if err := SafeWriteBytes(dest, data); err != nil {
		return nil, nil, err
	}
	actions = append(actions,
		"no PDF cleaner available (install exiftool for reliable metadata strip); copied as-is")
	return actions, map[string]interface{}{"mode": "copy", "degraded": true}, nil
}
