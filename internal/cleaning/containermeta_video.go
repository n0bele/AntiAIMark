// Video container inspect/clean — native ISOBMFF parsing with exiftool fallback.
package cleaning

import (
	"bytes"
	"os"
	"path/filepath"
)

// isobmffVideoFormats are natively parsed via ISOBMFF boxes.
var isobmffVideoFormats = map[string]bool{"mp4": true, "m4v": true, "mov": true}

// inspectVideo does provenance scan: ISOBMFF-native for mp4/mov/m4v, byte-scan otherwise.
func inspectVideo(data []byte, fmt string) (bool, bool, []string, map[string]interface{}) {
	if isobmffVideoFormats[fmt] {
		hasC2PA, hasAI, findings := isobmffInspect(data)
		// Keep legacy uuid/jumb and ©too findings for parity if not already reported
		return hasC2PA, hasAI, findings, map[string]interface{}{"format": fmt, "native": "isobmff"}
	}
	var findings []string
	hasC2pa, hasAI, hits := blobHits(data)
	for _, h := range hits {
		findings = append(findings, "video byte-scan:"+h)
	}
	if bytes.Contains(data, []byte("uuid")) && bytes.Contains(bytes.ToLower(data), []byte("jumb")) {
		hasC2pa = true
		findings = append(findings, "video C2PA uuid box present")
	}
	if bytes.Contains(bytes.ToLower(data), []byte("©too")) {
		hasAI = true
		findings = append(findings, "video QuickTime ©too software atom present")
	}
	return hasC2pa, hasAI || hasC2pa, findings, map[string]interface{}{"format": fmt}
}

// cleanVideo natively strips ISOBMFF provenance boxes; falls back to exiftool for
// non-ISOBMFF formats or as supplemental pass.
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

	if isobmffVideoFormats[fmt] {
		stripped, innerActions, stripErr := isobmffStrip(data)
		if stripErr == nil {
			actions = append(actions, innerActions...)
			meta["native"] = "isobmff"
			// Supplemental exiftool pass when available (cleans XMP/udta remnants)
			if exiftool := Which("exiftool"); exiftool != "" {
				if err := SafeWriteBytes(dest, stripped); err != nil {
					return nil, nil, err
				}
				res := runCaptured([]string{exiftool, "-all=", "-overwrite_original", SafeArg(dest)}, 60)
				if res.timedOut {
					actions = append(actions, "exiftool failed: "+pyTimeoutErr([]string{exiftool, "-all=", "-overwrite_original", SafeArg(dest)}, 60))
				} else if res.err != nil {
					actions = append(actions, "exiftool failed: "+res.err.Error())
				} else {
					actions = append(actions, "exiftool -all= supplemental (rc="+itoa(res.code)+")")
				}
				return actions, meta, nil
			}
			if err := SafeWriteBytes(dest, stripped); err != nil {
				return nil, nil, err
			}
			return actions, meta, nil
		}
		// native strip failed — fall through to exiftool
		actions = append(actions, "isobmff native strip failed: "+stripErr.Error())
	}

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
