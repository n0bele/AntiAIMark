package cleaning

import (
	"bytes"
	"encoding/binary"
)

// isobmffBox is a parsed ISOBMFF box.
type isobmffBox struct {
	typ     string
	size    int
	header  int // 8 or 16
	payload []byte
}

// parseISOBMFF parses top-level boxes from data[offset:end).
func parseISOBMFF(data []byte, off, end int) []isobmffBox {
	var out []isobmffBox
	for off+8 <= end {
		sz := int(binary.BigEndian.Uint32(data[off : off+4]))
		typ := string(data[off+4 : off+8])
		hdr := 8
		if sz == 1 {
			if off+16 > end {
				break
			}
			sz = int(binary.BigEndian.Uint64(data[off+8 : off+16]))
			hdr = 16
		} else if sz == 0 {
			sz = end - off
		}
		if sz < hdr || off+sz > end {
			break
		}
		out = append(out, isobmffBox{typ: typ, size: sz, header: hdr, payload: data[off+hdr : off+sz]})
		off += sz
	}
	return out
}

// isobmffInspect scans ISOBMFF payload for C2PA/AI markers and structure hints.
func isobmffInspect(data []byte) (bool, bool, []string) {
	findings := []string{}
	hasC2PA, hasAI, hits := blobHits(data)
	for _, h := range hits {
		findings = append(findings, "isobmff byte-scan:"+h)
	}
	low := bytes.ToLower(data)
	if bytes.Contains(data, []byte("uuid")) && bytes.Contains(low, []byte("jumb")) {
		hasC2PA = true
		findings = append(findings, "isobmff uuid/jumb box (C2PA)")
	}
	if bytes.Contains(low, []byte("©too")) || bytes.Contains(low, []byte("©gen")) {
		hasAI = true
		findings = append(findings, "isobmff QuickTime ©too/©gen atom")
	}
	// scan top-level box types for known provenance containers
	for _, b := range parseISOBMFF(data, 0, len(data)) {
		switch b.typ {
		case "uuid":
			if len(containsAny(b.payload, c2paMarkers)) > 0 || bytes.Contains(bytes.ToLower(b.payload), []byte("jumb")) {
				hasC2PA = true
				findings = append(findings, "isobmff uuid box with C2PA markers")
			}
		case "udta", "meta":
			if len(containsAny(b.payload, aiPlusC2paMarkers)) > 0 {
				hasAI = true
				findings = append(findings, "isobmff "+b.typ+" with AI/C2PA markers")
			}
		}
	}
	return hasC2PA, hasAI || hasC2PA, findings
}

// isobmffContainerTypes are boxes that contain child boxes and must be recursed.
var isobmffContainerTypes = map[string]bool{
	"moov": true, "trak": true, "mdia": true, "minf": true, "stbl": true, "edts": true,
}

func isobmffWriteBox(out *bytes.Buffer, typ string, header int, size int, payload []byte) {
	var hdr [16]byte
	if header == 16 {
		binary.BigEndian.PutUint32(hdr[0:4], 1)
		copy(hdr[4:8], []byte(typ))
		binary.BigEndian.PutUint64(hdr[8:16], uint64(size))
		out.Write(hdr[:16])
	} else {
		binary.BigEndian.PutUint32(hdr[0:4], uint32(size))
		copy(hdr[4:8], []byte(typ))
		out.Write(hdr[:8])
	}
	out.Write(payload)
}

// isobmffStrip natively rebuilds an ISOBMFF file, dropping provenance boxes.
// Dropped: uuid boxes containing C2PA/jumb, whole udta, meta/XMP payloads with AI hints.
// Container boxes (moov/trak/...) are recursed so nested udta/meta are also stripped.
func isobmffStrip(data []byte) ([]byte, []string, error) {
	return isobmffStripDepth(data, &[]string{}, 0)
}

func isobmffStripDepth(data []byte, actions *[]string, depth int) ([]byte, []string, error) {
	if depth > 8 {
		return data, *actions, nil
	}
	boxes := parseISOBMFF(data, 0, len(data))
	if len(boxes) == 0 {
		return data, *actions, nil
	}
	var out bytes.Buffer
	dropped := 0
	for _, b := range boxes {
		// Recurse into container boxes (moov etc.) to strip nested provenance.
		if isobmffContainerTypes[b.typ] {
			children := parseISOBMFF(b.payload, 0, len(b.payload))
			if len(children) > 0 {
				before := len(*actions)
				stripped, _, _ := isobmffStripDepth(b.payload, actions, depth+1)
				if len(*actions) > before || len(stripped) != len(b.payload) {
					// inner provenance was removed — rebuild container with stripped payload
					sz := 8
					if b.header == 16 {
						sz = 16
					}
					sz += len(stripped)
					isobmffWriteBox(&out, b.typ, b.header, sz, stripped)
					dropped++
					continue
				}
			}
			// no inner change — keep as-is
			isobmffWriteBox(&out, b.typ, b.header, b.size, b.payload)
			continue
		}
		drop := false
		switch b.typ {
		case "uuid":
			if len(containsAny(b.payload, c2paMarkers)) > 0 || bytes.Contains(bytes.ToLower(b.payload), []byte("jumb")) || len(containsAny(b.payload, aiPlusC2paMarkers)) > 0 {
				drop = true
				*actions = append(*actions, "drop isobmff uuid box (C2PA)")
			}
		case "udta":
			if len(containsAny(b.payload, aiPlusC2paMarkers)) > 0 || bytes.Contains(bytes.ToLower(b.payload), []byte("©too")) {
				drop = true
				*actions = append(*actions, "drop isobmff udta box")
			}
		case "meta":
			if len(containsAny(b.payload, aiPlusC2paMarkers)) > 0 {
				inner := parseISOBMFF(b.payload, 0, len(b.payload))
				if len(inner) > 0 {
					var innerOut bytes.Buffer
					innerDropped := 0
					for _, ib := range inner {
						if len(containsAny(ib.payload, aiPlusC2paMarkers)) > 0 {
							innerDropped++
							continue
						}
						isobmffWriteBox(&innerOut, ib.typ, ib.header, ib.size, ib.payload)
					}
					if innerDropped > 0 {
						*actions = append(*actions, "strip isobmff meta inner boxes with AI/C2PA")
						payload := innerOut.Bytes()
						sz := 8 + len(payload)
						var hdr [8]byte
						binary.BigEndian.PutUint32(hdr[0:4], uint32(sz))
						copy(hdr[4:8], []byte("meta"))
						out.Write(hdr[:])
						out.Write(payload)
						dropped++
						continue
					}
				}
				drop = true
				*actions = append(*actions, "drop isobmff meta box (AI/C2PA)")
			}
		}
		if drop {
			dropped++
			continue
		}
		isobmffWriteBox(&out, b.typ, b.header, b.size, b.payload)
	}
	if depth == 0 && dropped == 0 {
		*actions = append(*actions, "no isobmff provenance boxes removed")
	}
	if out.Len() == 0 {
		if depth == 0 {
			return data, *actions, nil
		}
		return []byte{}, *actions, nil
	}
	return out.Bytes(), *actions, nil
}
