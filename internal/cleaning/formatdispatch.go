// Route a file or byte stream to the text, image or container pipeline.
// Faithful Go port of service/scripts/format_dispatch.py, extended so common
// video formats are routed to the container pipeline (web upload/download).
package cleaning

import (
	"os"
	"path/filepath"
	"strings"
)

type Kind string

const (
	KindText      Kind = "text"
	KindImage     Kind = "image"
	KindContainer Kind = "container"
)

var ImageExts = map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true}

var ContainerExts = map[string]bool{
	".svg":      true,
	".pdf":      true,
	".docx":     true,
	".odt":      true,
	".html":     true,
	".htm":      true,
	".md":       true,
	".markdown": true,
	".mdx":      true,
}

// Video extensions routed to the container pipeline (Go web extension).
var VideoExts = map[string]bool{
	".mp4": true, ".m4v": true, ".mov": true, ".qt": true,
	".webm": true, ".mkv": true, ".avi": true, ".wmv": true,
	".flv": true, ".3gp": true, ".ts": true, ".m2ts": true,
	".mpg": true, ".mpeg": true, ".ogv": true,
}

var TextExts = map[string]bool{
	".txt": true, ".text": true, ".css": true, ".js": true, ".py": true,
	".rs": true, ".go": true, ".json": true, ".yaml": true, ".yml": true,
	".toml": true, ".csv": true,
}

// ClassifyBytes classifies data by extension first, then by magic bytes.
// The extension wins when it names a known format; otherwise the bytes are
// sniffed for image/container signatures. Unrecognized bytes fall back to
// "text" — callers that must not mangle unknown binaries guard themselves.
func ClassifyBytes(data []byte, suffix string) Kind {
	ext := strings.ToLower(suffix)
	if ImageExts[ext] {
		return KindImage
	}
	if ContainerExts[ext] || VideoExts[ext] {
		return KindContainer
	}
	if TextExts[ext] {
		return KindText
	}
	if f := DetectImageFormat(data); f == "png" || f == "jpeg" || f == "webp" {
		return KindImage
	}
	if len(data) > 0 {
		sniffName := "input"
		if ext != "" {
			sniffName = "input" + ext
		}
		if DetectContainerFormat(sniffName, data) != "unknown" {
			return KindContainer
		}
	}
	return KindText
}

// Classify classifies a file on disk by extension, then by its bytes. A read
// error propagates like the Python classify() raising OSError.
func Classify(path string) (Kind, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return KindText, err
	}
	return ClassifyBytes(data, filepath.Ext(path)), nil
}
