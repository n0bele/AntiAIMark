// Audio container inspect/clean — a Go web extension mirroring the video
// pipeline. Inspection byte-scans tag payloads for AI-generator markers.
// Cleaning strips tag metadata natively in Go (MP3 ID3v1/v2, FLAC
// VORBIS_COMMENT, WAV LIST/INFO, AIFF text chunks); M4A goes through
// exiftool, which is currently the only audio format it can write.
// Formats without a safe native stripper (Ogg/Opus/WMA) pass through
// degraded, mirroring the video pipeline's exiftool-less fallback.
package cleaning

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
)

// inspectAudio does a best-effort provenance scan of an audio byte blob.
// Returns (has_c2pa, has_ai, findings, details).
func inspectAudio(data []byte, fmt string) (bool, bool, []string, map[string]interface{}) {
	hasC2pa, hasAI, hits := blobHits(data)
	findings := []string{}
	for _, h := range hits {
		findings = append(findings, "audio byte-scan:"+h)
	}
	return hasC2pa, hasAI || hasC2pa, findings, map[string]interface{}{"format": fmt}
}

// exiftoolWritableAudio lists audio formats `exiftool -all=` can rewrite.
var exiftoolWritableAudio = map[string]bool{"m4a": true, "m4b": true}

// cleanAudio strips AI-provenance-carrying tag metadata from an audio file.
// Returns (actions, meta).
func cleanAudio(src, dest string, fmt string) ([]string, map[string]interface{}, error) {
	data, err := os.ReadFile(src)
	if err != nil {
		return nil, nil, err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return nil, nil, err
	}
	actions := []string{}
	meta := map[string]interface{}{"format": fmt, "audio": true}

	switch fmt {
	case "mp3", "aac":
		var acts []string
		data, acts = stripID3Tags(data)
		actions = append(actions, acts...)
	case "flac":
		var acts []string
		data, acts = stripFlacComments(data)
		actions = append(actions, acts...)
	case "wav", "aiff":
		var acts []string
		data, acts = stripIFFMetadataChunks(data)
		actions = append(actions, acts...)
	}

	if exiftoolWritableAudio[fmt] {
		if err := SafeWriteBytes(dest, data); err != nil {
			return nil, nil, err
		}
		if exiftool := Which("exiftool"); exiftool != "" {
			res := runCaptured([]string{exiftool, "-all=", "-overwrite_original", SafeArg(dest)}, 60)
			if res.timedOut {
				actions = append(actions, "exiftool failed: "+pyTimeoutErr([]string{exiftool, "-all=", "-overwrite_original", SafeArg(dest)}, 60))
			} else if res.err != nil {
				actions = append(actions, "exiftool failed: "+res.err.Error())
			} else {
				actions = append(actions, "exiftool -all= pass (audio) (rc="+itoa(res.code)+")")
			}
		} else {
			actions = append(actions, "audio metadata strip: exiftool not available for "+fmt+" (pass-through)")
			meta["degraded"] = true
		}
		return actions, meta, nil
	}

	if len(actions) > 0 {
		if err := SafeWriteBytes(dest, data); err != nil {
			return nil, nil, err
		}
		return actions, meta, nil
	}

	// No native stripper for this audio format (Ogg/Opus/WMA…): removing
	// their header packets invalidates decoders, so pass through honestly.
	actions = append(actions, "audio metadata strip: no native stripper for "+fmt+" and exiftool cannot write it (pass-through)")
	meta["degraded"] = true
	if err := SafeWriteBytes(dest, data); err != nil {
		return nil, nil, err
	}
	return actions, meta, nil
}

// ---------------------------------------------------------------------------
// MP3 — ID3v2 header block, ID3v1 tail
// ---------------------------------------------------------------------------

func id3SyncsafeSize(b []byte) int {
	return int(b[0]&0x7f)<<21 | int(b[1]&0x7f)<<14 | int(b[2]&0x7f)<<7 | int(b[3]&0x7f)
}

// stripID3Tags removes a leading ID3v2 block and a trailing ID3v1 tag.
func stripID3Tags(data []byte) ([]byte, []string) {
	var actions []string
	out := data
	if len(out) >= 10 && bytes.HasPrefix(out, []byte("ID3")) {
		size := id3SyncsafeSize(out[6:10])
		end := 10 + size
		if out[5]&0x10 != 0 { // footer present
			end += 10
		}
		if end > 0 && end <= len(out) {
			out = out[end:]
			actions = append(actions, "drop ID3v2 tag ("+itoa(end)+" bytes)")
		}
	}
	if len(out) >= 128 && bytes.Equal(out[len(out)-128:len(out)-128+3], []byte("TAG")) {
		out = out[:len(out)-128]
		actions = append(actions, "drop ID3v1 tag (128 bytes)")
	}
	if len(actions) == 0 {
		actions = append(actions, "no ID3 tags present")
	}
	return out, actions
}

// ---------------------------------------------------------------------------
// FLAC — drop VORBIS_COMMENT metadata blocks, rebuilding block flags
// ---------------------------------------------------------------------------

// stripFlacComments removes type-4 (VORBIS_COMMENT) metadata blocks. A FLAC
// stream without a comment block stays valid for decoders.
func stripFlacComments(data []byte) ([]byte, []string) {
	if !bytes.HasPrefix(data, []byte("fLaC")) {
		return data, []string{"not a FLAC stream (no native strip)"}
	}
	pos := 4
	type block struct {
		header []byte // 4 bytes without flags
		body   []byte
	}
	var blocks []block
	dropped := 0
	for pos+4 <= len(data) {
		btype := data[pos] & 0x7f
		last := data[pos]&0x80 != 0
		blen := int(data[pos+1])<<16 | int(data[pos+2])<<8 | int(data[pos+3])
		start, end := pos, pos+4+blen
		if end > len(data) {
			return data, []string{"malformed FLAC metadata (no native strip)"}
		}
		pos = end
		if btype == 4 {
			dropped++
		} else {
			blocks = append(blocks, block{header: []byte{data[start] & 0x7f, data[start+1], data[start+2], data[start+3]}, body: data[start+4 : end]})
		}
		if last {
			break // audio frames follow
		}
	}
	if dropped == 0 {
		return data, []string{"no VORBIS_COMMENT block present"}
	}
	out := make([]byte, 0, len(data))
	out = append(out, []byte("fLaC")...)
	for i, b := range blocks {
		h := append([]byte{}, b.header...)
		if i == len(blocks)-1 {
			h[0] |= 0x80 // last-metadata-block flag
		}
		out = append(out, h...)
		out = append(out, b.body...)
	}
	out = append(out, data[pos:]...) // audio frames
	return out, []string{"drop VORBIS_COMMENT block x" + itoa(dropped)}
}

// ---------------------------------------------------------------------------
// IFF chunk walking — WAV (RIFF, LE sizes) and AIFF (FORM, BE sizes)
// ---------------------------------------------------------------------------

// stripIFFMetadataChunks removes metadata chunks from WAV/AIFF containers:
// WAV LIST…INFO and ID3 chunks; AIFF COMT/ANNO/NAME/AUTH/(c)/ID3 chunks.
func stripIFFMetadataChunks(data []byte) ([]byte, []string) {
	var actions []string
	if len(data) < 12 {
		return data, []string{"too short for IFF (no native strip)"}
	}
	var le bool // RIFF=LE sizes, FORM=BE
	switch {
	case bytes.HasPrefix(data, []byte("RIFF")) && bytes.Equal(data[8:12], []byte("WAVE")):
		le = true
	case bytes.HasPrefix(data, []byte("FORM")) && (bytes.Equal(data[8:12], []byte("AIFF")) || bytes.Equal(data[8:12], []byte("AIFC"))):
		le = false
	default:
		return data, []string{"not a WAV/AIFF container (no native strip)"}
	}
	readU32 := func(b []byte) int {
		if le {
			return int(binary.LittleEndian.Uint32(b))
		}
		return int(binary.BigEndian.Uint32(b))
	}
	pos := 12
	out := make([]byte, 0, len(data))
	out = append(out, data[:12]...)
	for pos+8 <= len(data) {
		id := string(data[pos : pos+4])
		size := readU32(data[pos+4 : pos+8])
		body := pos + 8 + size
		padded := body
		if size%2 == 1 && body < len(data) {
			padded++ // IFF chunks are word-aligned with a pad byte
		}
		if padded > len(data) {
			return data, []string{"malformed IFF chunk (no native strip)"}
		}
		drop := false
		if le { // WAV
			if id == "LIST" && bytes.HasPrefix(data[pos+8:body], []byte("INFO")) {
				drop = true
				actions = append(actions, "drop LIST/INFO chunk ("+itoa(padded-pos)+" bytes)")
			} else if id == "id3 " || id == "ID3 " {
				drop = true
				actions = append(actions, "drop ID3 chunk ("+itoa(padded-pos)+" bytes)")
			}
		} else { // AIFF
			switch id {
			case "COMT", "ANNO", "NAME", "AUTH", "(c) ", "ID3 ":
				drop = true
				actions = append(actions, "drop AIFF "+id+" chunk ("+itoa(padded-pos)+" bytes)")
			}
		}
		if !drop {
			out = append(out, data[pos:padded]...)
		}
		pos = padded
	}
	if len(actions) == 0 {
		return data, []string{"no metadata chunks present"}
	}
	// recompute the RIFF/FORM container size
	if le {
		binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
	} else {
		binary.BigEndian.PutUint32(out[4:8], uint32(len(out)-8))
	}
	return out, actions
}
