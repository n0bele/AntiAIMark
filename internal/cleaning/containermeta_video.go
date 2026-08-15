// Video container inspect/clean — a Go web extension (the Python original does
// not handle video). Best-effort: byte-scan for provenance markers, and strip
// metadata via exiftool when available; otherwise pass through.
package cleaning

import (
	"bytes"
	"os"
	"path/filepath"
)

// inspectVideo does a best-effort provenance scan of a video byte blob.
// Returns (has_c2pa, has_ai, findings, details).
func inspectVideo(data []byte, fmt string) (bool, bool, []string, map[string]interface{}) {
	var findings []string
	hasC2pa, hasAI, hits := blobHits(data)
	for _, h := range hits {
		findings = append(findings, "video byte-scan:"+h)
	}
	// C2PA in ISO BMFF lives in a "uuid" box with a JUMBF brand.
	if bytes.Contains(data, []byte("uuid")) && bytes.Contains(bytes.ToLower(data), []byte("jumb")) {
		hasC2pa = true
		findings = append(findings, "video C2PA uuid box present")
	}
	// QuickTime metadata atoms (©-atoms) commonly carry AI generator strings.
	if bytes.Contains(bytes.ToLower(data), []byte("©too")) {
		hasAI = true
		findings = append(findings, "video QuickTime ©too software atom present")
	}
	return hasC2pa, hasAI || hasC2pa, findings, map[string]interface{}{"format": fmt}
}

// cleanVideo copies src to dest, stripping metadata via exiftool when present.
// Returns (actions, meta).
func cleanVideo(src, dest string, fmt string) ([]string, map[string]interface{}, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, nil, err
	}
	actions := []string{}
	meta := map[string]interface{}{"format": fmt, "video": true}

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
			actions = append(actions, "exiftool -all= pass (video) (rc="+itoa(res.code)+")")
		}
		return actions, meta, nil
	}

	// Pass-through fallback
	actions = append(actions, "video metadata strip: exiftool not available (pass-through)")
	meta["degraded"] = true
	if err := SafeWriteBytes(dest, data); err != nil {
		return nil, nil, err
	}
	return actions, meta, nil
}
