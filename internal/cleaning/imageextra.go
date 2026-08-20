package cleaning

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// TIFF / GIF / HEIF-ISOBMFF / AVIF sniff helpers.
// These formats were previously "unsupported format". Now they are inspected
// via byte-scan + minimal native box/payload parsing.

func isTIFF(data []byte) bool {
	if len(data) < 4 {
		return false
	}
	return (data[0] == 0x49 && data[1] == 0x49 && data[2] == 0x2A && data[3] == 0x00) ||
		(data[0] == 0x4D && data[1] == 0x4D && data[2] == 0x00 && data[3] == 0x2A)
}

func isGIF(data []byte) bool {
	return bytes.HasPrefix(data, []byte("GIF87a")) || bytes.HasPrefix(data, []byte("GIF89a"))
}

func isHEIF(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	if !bytes.Equal(data[4:8], []byte("ftyp")) {
		return false
	}
	brand := string(data[8:12])
	switch brand {
	case "heic", "heix", "hevc", "hevx", "heim", "heis", "mif1", "msf1":
		return true
	}
	return bytes.Contains(data[8:12], []byte("heic")) || bytes.Contains(data[8:12], []byte("heif"))
}

func isAVIF(data []byte) bool {
	if len(data) < 12 || !bytes.Equal(data[4:8], []byte("ftyp")) {
		return false
	}
	return bytes.Equal(data[8:12], []byte("avif")) || bytes.Equal(data[8:12], []byte("avis"))
}

// inspectExtraImage does byte-scan provenance detection for the extra image formats.
func inspectExtraImage(data []byte, format string) (bool, bool, []string) {
	findings := []string{}
	hasC2PA, hasAI, hits := blobHits(data)
	for _, h := range hits {
		findings = append(findings, format+" byte-scan:"+h)
	}
	switch format {
	case "gif":
		// GIF comments and XMP blocks
		if bytes.Contains(data, []byte("XMP")) || bytes.Contains(data, []byte("xmp:")) {
			hasAI = true
			findings = append(findings, "GIF XMP block present")
		}
	case "tiff":
		// TIFF Artist/Software tags (270/305) often contain generator strings; byte-scan already covers markers
		// No extra heuristics beyond blobHits for now
	case "heif", "avif":
		// ISOBMFF-based: Check for uuid/jumb and udta/meta provenance boxes
		low := bytes.ToLower(data)
		if bytes.Contains(data, []byte("uuid")) && bytes.Contains(low, []byte("jumb")) {
			hasC2PA = true
			findings = append(findings, format+" uuid/jumb C2PA box")
		}
		if c2, ai, extra := isobmffInspect(data); c2 || ai {
			if c2 {
				hasC2PA = true
			}
			if ai {
				hasAI = true
			}
			findings = append(findings, extra...)
		}
	}
	return hasC2PA, hasAI || hasC2PA, findings
}

// stripExtraImage natively rebuilds extra image formats to drop provenance payloads.
// GIF: strip comment (0x21 0xFE) and application (0x21 0xFF) extensions containing AI/C2PA markers.
// TIFF: pass-through (TIFF tag rewriting is complex; fallback to exiftool in CleanImage).
// HEIF/AVIF: use ISOBMFF native strip.
func stripExtraImage(data []byte, format string, stripAll bool) ([]byte, []string, error) {
	switch format {
	case "gif":
		return stripGIF(data, stripAll)
	case "tiff":
		// TIFF native tag strip is non-trivial (IFD offsets); use exiftool fallback in CleanImage
		return nil, nil, fmt.Errorf("tiff native strip not implemented; use exiftool fallback")
	case "heif", "avif":
		actions := []string{"use isobmff native strip for " + format}
		out, innerActions, err := isobmffStrip(data)
		if err != nil {
			return nil, nil, err
		}
		actions = append(actions, innerActions...)
		return out, actions, nil
	default:
		return nil, nil, fmt.Errorf("unknown extra image format: %s", format)
	}
}

func stripGIF(data []byte, stripAll bool) ([]byte, []string, error) {
	if !isGIF(data) {
		return nil, nil, fmt.Errorf("not GIF")
	}
	actions := []string{}
	var out bytes.Buffer
	// GIF header 6 bytes + logical screen descriptor 7 bytes minimum
	if len(data) < 13 {
		return nil, nil, fmt.Errorf("truncated GIF")
	}
	out.Write(data[:13])
	pos := 13
	// Check for global color table
	packed := data[10]
	if packed&0x80 != 0 {
		colors := 3 * (1 << ((packed & 0x07) + 1))
		if pos+colors > len(data) {
			return nil, nil, fmt.Errorf("truncated GIF global color table")
		}
		out.Write(data[pos : pos+colors])
		pos += colors
	}
	for pos < len(data) {
		b := data[pos]
		if b == 0x3B { // trailer
			out.WriteByte(0x3B)
			break
		}
		if b == 0x21 { // extension
			if pos+1 >= len(data) {
				out.Write(data[pos:])
				break
			}
			label := data[pos+1]
			// Read sub-blocks to find extension end
			end := pos + 2
			// extensions are: label + sub-block sized chunks + 0x00 terminator
			// For comment (0xFE) and app (0xFF), check payload for AI/C2PA
			extStart := pos
			// Scan to extension terminator
			p := pos + 2
			for p < len(data) {
				if data[p] == 0x00 {
					end = p + 1
					break
				}
				sz := int(data[p])
				if p+1+sz > len(data) {
					end = len(data)
					break
				}
				p += 1 + sz
			}
			if end > len(data) {
				end = len(data)
			}
			extBytes := data[extStart:end]
			drop := false
			if label == 0xFE { // comment
				if stripAll || len(containsAny(extBytes, aiPlusC2paMarkers)) > 0 {
					drop = true
					actions = append(actions, "drop GIF comment extension")
				}
			} else if label == 0xFF { // application extension (often XMP / C2PA)
				if len(containsAny(extBytes, aiPlusC2paMarkers)) > 0 || bytes.Contains(bytes.ToLower(extBytes), []byte("xmp")) {
					drop = true
					actions = append(actions, "drop GIF application extension (XMP/C2PA)")
				} else if stripAll && bytes.Contains(extBytes, []byte("NETSCAPE")) {
					// keep NETSCAPE loop extension
				} else if stripAll {
					// In strip-all mode, keep only NETSCAPE
					if !bytes.Contains(extBytes, []byte("NETSCAPE")) {
						drop = true
						actions = append(actions, "drop GIF application extension")
					}
				}
			}
			if !drop {
				out.Write(extBytes)
			}
			pos = end
			continue
		}
		if b == 0x2C { // image descriptor
			if pos+10 > len(data) {
				out.Write(data[pos:])
				break
			}
			packed2 := data[pos+9]
			out.Write(data[pos : pos+10])
			pos += 10
			if packed2&0x80 != 0 {
				colors := 3 * (1 << ((packed2 & 0x07) + 1))
				if pos+colors > len(data) {
					out.Write(data[pos:])
					break
				}
				out.Write(data[pos : pos+colors])
				pos += colors
			}
			if pos >= len(data) {
				break
			}
			// LZW minimum code size
			out.WriteByte(data[pos])
			pos++
			// image data sub-blocks
			for pos < len(data) {
				sz := int(data[pos])
				out.WriteByte(data[pos])
				pos++
				if sz == 0 {
					break
				}
				if pos+sz > len(data) {
					out.Write(data[pos:])
					pos = len(data)
					break
				}
				out.Write(data[pos : pos+sz])
				pos += sz
			}
			continue
		}
		// unknown byte — copy one and advance
		out.WriteByte(b)
		pos++
	}
	if len(actions) == 0 {
		actions = append(actions, "no GIF metadata extensions removed")
	}
	return out.Bytes(), actions, nil
}

// Ensure binary import is used (for future HEIF box parsing)
var _ = binary.BigEndian
