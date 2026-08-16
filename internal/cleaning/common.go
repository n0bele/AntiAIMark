// Package cleaning is a faithful Go port of the Python `service/scripts`
// pipeline from the antiaimark project. It strips multi-vendor AI
// provenance marks (Unicode, C2PA/EXIF/XMP, containers) from text and files.
package cleaning

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"antiaimark/internal/i18n"
)

// Hard caps on attacker-influenced input sizes. Whole-file in-memory
// processing means a 1 GiB default is a host-memory DoS; keep defaults low.
// The env overrides remain as an explicit escape hatch.
var (
	MaxInputBytes = envInt("ANTIAIMARK_MAX_INPUT_BYTES", 256<<20)
	MaxStdinBytes = envInt("ANTIAIMARK_MAX_STDIN_BYTES", 64<<20)
)

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// ValueError mirrors Python's ValueError: the HTTP server maps it to a 400
// response the way server.py catches ValueError around inspect/clean.
type ValueError struct {
	msg string
}

func (e *ValueError) Error() string { return e.msg }

// NewValueError builds a *ValueError.
func NewValueError(msg string) error { return &ValueError{msg} }

// Eprint writes a line to stderr.
func Eprint(args ...interface{}) {
	fmt.Fprintln(os.Stderr, args...)
}

// Containers that get mistaken for text on the command line. Decoding one as
// text walks compressed bytes and reports whatever codepoints fall out of them:
// noise that tracks the compression, not the content. Worse, cleaning such a
// "text" writes the mangled bytes back and destroys the file.
// Each entry carries an i18n key so the refusal message can be localized;
// Error() paths still render the English (Python-identical) text.
var binaryMagic = []struct {
	magic []byte
	key   string
}{
	{[]byte("PK\x03\x04"), "binary.zip"},
	{[]byte("PK\x05\x06"), "binary.zip_empty"},
	{[]byte("PK\x07\x08"), "binary.zip_spanned"},
	{[]byte("%PDF-"), "binary.pdf"},
	{[]byte("\x89PNG\r\n\x1a\n"), "binary.png"},
	{[]byte("\xff\xd8\xff"), "binary.jpeg"},
	{[]byte("GIF87a"), "binary.gif"},
	{[]byte("GIF89a"), "binary.gif"},
	{[]byte("II*\x00"), "binary.tiff"},
	{[]byte("MM\x00*"), "binary.tiff"},
	{[]byte("RIFF"), "binary.riff"},
	{[]byte("OggS"), "binary.ogg"},
	{[]byte("ID3"), "binary.mp3"},
	{[]byte("fLaC"), "binary.flac"},
	{[]byte("\x1f\x8b"), "binary.gzip"},
	{[]byte("BZh"), "binary.bzip2"},
	{[]byte("\xfd7zXZ\x00"), "binary.xz"},
	{[]byte("7z\xbc\xaf\x27\x1c"), "binary.7z"},
	{[]byte("Rar!\x1a\x07"), "binary.rar"},
	{[]byte("\x7fELF"), "binary.elf"},
	{[]byte("\xca\xfe\xba\xbe"), "binary.cafebabe"},
	{[]byte("\xd0\xcf\x11\xe0\xa1\xb1\x1a\xe1"), "binary.legacy_office"},
	{[]byte("SQLite format 3\x00"), "binary.sqlite"},
	{[]byte("8BPS"), "binary.psd"},
	{[]byte("wOFF"), "binary.woff"},
	{[]byte("wOF2"), "binary.woff2"},
	{[]byte("\x00\x01\x00\x00\x00"), "binary.ttf"},
	{[]byte("OTTO"), "binary.otf"},
}

const BinarySniffBytes = 8192

// Real text runs ~0% control bytes; compressed and executable data runs far
// above this. Tab, LF, CR, FF and ESC are excluded as legitimate in text.
const controlRatioLimit = 0.05

var allowedControls = map[byte]bool{0x09: true, 0x0A: true, 0x0B: true, 0x0C: true, 0x0D: true, 0x1B: true}

// LooksBinary describes why data is not plausibly text, or returns "" when it
// looks like text. Deliberately conservative: encodings other than UTF-8 must
// keep working, so undecodable bytes alone are not proof. The label is the
// English text (Python parity); LooksBinaryKey returns its i18n key.
func LooksBinary(data []byte) string {
	if key := LooksBinaryKey(data); key != "" {
		return i18n.TFor(i18n.English, key)
	}
	return ""
}

// LooksBinaryKey is the i18n-key form of LooksBinary ("" when the data looks
// like text): binary.nul / binary.control / binary.<format>.
func LooksBinaryKey(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	for _, m := range binaryMagic {
		if bytes.HasPrefix(data, m.magic) {
			return m.key
		}
	}
	head := data
	if len(head) > BinarySniffBytes {
		head = head[:BinarySniffBytes]
	}
	if bytes.IndexByte(head, 0) >= 0 {
		return "binary.nul"
	}
	controls := 0
	for _, b := range head {
		if b < 0x20 && !allowedControls[b] {
			controls++
		}
	}
	if float64(controls)/float64(len(head)) > controlRatioLimit {
		return "binary.control"
	}
	return ""
}

// Advice for the text-only scripts: another tool in this repo handles the file.
// The values are the English (Python-identical) lines; render via i18n keys
// cli.advice_text_1/2 when localizing.
var TextToolAdvice = []string{
	"Use inspect_file.py / clean_file.py, which route by format,",
	"or pass --force-text to scan the raw bytes anyway.",
}

// Advice for the routers themselves. They *are* inspect_file / clean_file,
// and classify() has already ruled out every known container, so pointing back
// at them would be circular. i18n keys: cli.advice_router_1/2.
var RouterAdvice = []string{
	"These bytes match no supported text, image or container format.",
	"Pass --force-text to handle them as text anyway, or --as to force a format.",
}

// BinaryRefusal is the typed guard_binary refusal (exit code 2). It carries
// everything needed to render the message in any supported locale.
type BinaryRefusal struct {
	Origin   string   // path or "stdin"
	LabelKey string   // i18n key of the sniffed binary kind
	Advice   []string // raw advice lines when custom advice was passed
	Router   bool     // render the router advice keys instead of text-tool ones
}

func (e *BinaryRefusal) Error() string {
	return localizeRefusal(i18n.English, e)
}

func localizeRefusal(tag i18n.Tag, e *BinaryRefusal) string {
	label := i18n.TFor(tag, e.LabelKey)
	var b strings.Builder
	fmt.Fprintln(&b, i18n.TFor(tag, "cli.guard_refusal", e.Origin, label))
	var lines []string
	if len(e.Advice) > 0 {
		lines = e.Advice
	} else if e.Router {
		lines = []string{i18n.TFor(tag, "cli.advice_router_1"), i18n.TFor(tag, "cli.advice_router_2")}
	} else {
		lines = []string{i18n.TFor(tag, "cli.advice_text_1"), i18n.TFor(tag, "cli.advice_text_2")}
	}
	for _, l := range lines {
		fmt.Fprintln(&b, l)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// GuardBinary refuses binary input for the text-only tools unless explicitly
// overridden; it returns a *BinaryRefusal (exit-2 class), or nil. Passing
// RouterAdvice selects the router advice keys; a custom advice slice is
// rendered verbatim.
func GuardBinary(data []byte, origin string, allowBinary bool, advice []string) error {
	if allowBinary {
		return nil
	}
	key := LooksBinaryKey(data)
	if key == "" {
		return nil
	}
	refusal := &BinaryRefusal{Origin: origin, LabelKey: key}
	if len(advice) > 0 && (len(advice) != len(RouterAdvice) || advice[0] != RouterAdvice[0]) {
		refusal.Advice = advice
	} else if len(advice) > 0 {
		refusal.Router = true
	}
	return refusal
}

// LocalizeError renders a core error in the requested locale. Typed errors
// (BinaryRefusal, exit-2 errors with i18n keys) are translated; everything
// else keeps its error text (technical / path-dependent strings).
func LocalizeError(err error, tag i18n.Tag) string {
	if err == nil {
		return ""
	}
	var refusal *BinaryRefusal
	if errorsAs(err, &refusal) {
		return localizeRefusal(tag, refusal)
	}
	var e2 ErrExit2
	if errorsAs(err, &e2) && e2.Key != "" {
		return i18n.TFor(tag, e2.Key, e2.Args...)
	}
	return err.Error()
}

// errorsAs is errors.As (local alias to keep imports tidy).
func errorsAs(err error, target interface{}) bool { return errors.As(err, target) }

// ReadTextInput reads a file (or "-" for stdin) and returns the text. The
// returned string keeps the raw bytes: invalid UTF-8 is represented through
// the rune pipeline (PyRunes) exactly like Python's errors="surrogateescape"
// round-trip, so cleaned output preserves the original non-UTF-8 bytes.
func ReadTextInput(path string, allowBinary bool, advice []string) (string, error) {
	if path == "" || path == "-" {
		return readStdinCapped(allowBinary, advice)
	}
	p := path
	fi, err := os.Stat(p)
	if err == nil && fi.Size() > int64(MaxInputBytes) {
		return "", errExit2{
			msg:  i18n.TFor(i18n.English, "cli.over_cap_file", MaxInputBytes, path),
			Key:  "cli.over_cap_file",
			Args: []interface{}{MaxInputBytes, path},
		}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	if err := GuardBinary(data, path, allowBinary, advice); err != nil {
		return "", err
	}
	return string(data), nil
}

// errExit2 marks a fatal CLI error carrying exit code 2 (Python SystemExit(2)
// paths: binary refusal, over-cap input, backup failure). Msg is the exact
// English stderr line; Key/Args allow facades to render it in any locale.
type errExit2 struct {
	msg  string
	Key  string
	Args []interface{}
}

func (e errExit2) Error() string { return e.msg }

// ErrExit2 is the fatal error type for CLI exit code 2. CLI mains use
// errors.As to detect it: print the message to stderr and exit 2.
type ErrExit2 = errExit2

// NewErrExit2 builds an exit-2 fatal error.
func NewErrExit2(msg string) error { return errExit2{msg: msg} }

// readStdinCapped mirrors _read_stdin_capped: read the raw byte stream in
// 1 MiB chunks, guard_binary on the first chunk's head (before the running
// total can exceed the cap), then refuse anything over MaxStdinBytes.
func readStdinCapped(allowBinary bool, advice []string) (string, error) {
	var buf bytes.Buffer
	total := 0
	chunk := make([]byte, 1<<20)
	first := true
	for {
		n, err := os.Stdin.Read(chunk)
		if n > 0 {
			if first {
				head := chunk[:n]
				if len(head) > BinarySniffBytes {
					head = head[:BinarySniffBytes]
				}
				if gerr := GuardBinary(head, "stdin", allowBinary, advice); gerr != nil {
					return "", gerr
				}
				first = false
			}
			total += n
			if total > MaxStdinBytes {
				return "", errExit2{
					msg:  i18n.TFor(i18n.English, "cli.over_cap_stdin", MaxStdinBytes),
					Key:  "cli.over_cap_stdin",
					Args: []interface{}{MaxStdinBytes},
				}
			}
			buf.Write(chunk[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
	}
	return buf.String(), nil
}

// WriteTextOutput writes text to stdout or, when path is set, atomically to it.
func WriteTextOutput(text string, path string) error {
	if path == "" || path == "-" {
		_, err := os.Stdout.WriteString(text)
		if err == nil && text != "" && !strings.HasSuffix(text, "\n") {
			_, err = os.Stdout.WriteString("\n")
		}
		return err
	}
	return SafeWriteText(path, text)
}

func defaultFileMode() os.FileMode {
	mask := umask()
	return os.FileMode(0o666 &^ mask)
}

// SafeWriteBytes atomically writes bytes to path without following symlinks.
// Writes to a temp file in the destination directory and renames it into
// place, defeating pre-placed symlinks redirecting the write elsewhere.
func SafeWriteBytes(path string, data []byte) error {
	dest := filepath.Clean(path)
	parent := filepath.Dir(dest)
	if err := os.MkdirAll(parent, 0o777); err != nil {
		return err
	}
	if fi, err := os.Lstat(dest); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to write through symlink: %s", dest)
	}
	tmp, err := os.CreateTemp(parent, "."+filepath.Base(dest)+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		tmp.Close()
		os.Remove(tmpName)
	}
	if err := tmp.Chmod(defaultFileMode()); err != nil {
		cleanup()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}

// SafeWriteText encodes text as UTF-8 and atomically writes it.
func SafeWriteText(path string, text string) error {
	return SafeWriteBytes(path, []byte(text))
}

// BackupPath creates a ".bak" copy of src via a safe write and returns the
// backup path. Used by "--in-place" flows so the original is never partially
// lost. Read and write failures both produce an exit-2 error carrying the
// Python "cannot create backup {bak}: {e}" stderr line.
func BackupPath(src string) (string, error) {
	bak := src + ".bak"
	data, err := os.ReadFile(src)
	if err != nil {
		return "", errExit2{
			msg:  i18n.TFor(i18n.English, "cli.backup_failed", bak, err),
			Key:  "cli.backup_failed",
			Args: []interface{}{bak, err},
		}
	}
	if err := SafeWriteBytes(bak, data); err != nil {
		return "", errExit2{
			msg:  i18n.TFor(i18n.English, "cli.backup_failed", bak, err),
			Key:  "cli.backup_failed",
			Args: []interface{}{bak, err},
		}
	}
	return bak, nil
}

// MarshalIndentNoEscape marshals v like Python's json.dumps(..., indent=2,
// ensure_ascii=False): no HTML escaping of <, >, &.
func MarshalIndentNoEscape(v interface{}) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	// Encoder.Encode appends a newline; MarshalIndent does not.
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

// EmitJSON writes data to stdout as indented JSON plus a newline, without
// HTML-escaping (mirrors emit_json's json.dumps(ensure_ascii=False)).
func EmitJSON(data interface{}) error {
	b, err := MarshalIndentNoEscape(data)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(append(b, '\n'))
	return err
}

// ConfidenceLevels mirrors the Python CONFIDENCE_LEVELS tuple.
var ConfidenceLevels = []string{"confirmed", "probable", "informational", "likely_false_positive"}

// ClassifyFindingConfidence maps a scanner finding to a confidence bucket,
// mirroring the heuristic mapping of the Python original.
func ClassifyFindingConfidence(finding string) string {
	t := strings.ToLower(finding)

	confirmed := []string{
		"c2patool reports",
		"c2pa-related manifest",
		"png chunk c2",
		"png chunk cabx",
		"png chunk jumb",
		"png chunk jumd",
		"jpeg app11 segment",
		"digital_source_type",
		"digitalsourcetype",
		"trainedalgorithmicmedia",
		"compositewithtrainedalgorithmicmedia",
		"softwareagent",
	}
	for _, s := range confirmed {
		if strings.Contains(t, s) {
			return "confirmed"
		}
	}

	if strings.HasPrefix(t, "info:") {
		return "informational"
	}
	informational := []string{
		"cms generator",
		"customxml parts",
		"xmp packet present",
		"unsupported",
		"not fully inspected",
		"format not",
		"svg <metadata> present",
		"not a valid",
		"truncated chunk",
		"bad segment length",
		"svg decode note",
	}
	for _, s := range informational {
		if strings.Contains(t, s) {
			return "informational"
		}
	}

	if strings.Contains(t, "byte-scan") {
		return "likely_false_positive"
	}

	probable := []string{
		"ai:",
		"marker:",
		"meta:",
		"frontmatter",
		"json-ld",
		"attr:",
		"png ",
		"jpeg app",
		"exif",
		"xmp",
		"interesting",
		"pdf-structured",
		"layer-a",
	}
	for _, s := range probable {
		if strings.Contains(t, s) {
			return "probable"
		}
	}

	return "informational"
}

// CleanedPath returns "path/to/file.ext" -> "path/to/file.cleaned.ext"
// (mirrors cleaned_path; src.stem + suffix + src.suffix). Leading-dot files
// (".gitignore") have an empty Path.suffix in Python, so the whole name is
// the stem: ".gitignore.cleaned".
func CleanedPath(src string, suffix string) string {
	if suffix == "" {
		suffix = ".cleaned"
	}
	base := filepath.Base(src)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	if strings.HasPrefix(base, ".") && !strings.Contains(base[1:], ".") {
		// dotfile: Python Path.suffix == "" and stem is the whole name
		ext = ""
		stem = base
	}
	return filepath.Join(filepath.Dir(src), stem+suffix+ext)
}

// Which returns the path to the named executable, or "" when not found.
func Which(cmd string) string {
	p, err := exec.LookPath(cmd)
	if err != nil {
		return ""
	}
	return p
}

// SafeArg guards paths passed to option-parsing CLIs (exiftool, c2patool).
// A filename starting with '-' would otherwise be interpreted as an option,
// turning a crafted filename into argv injection.
func SafeArg(path string) string {
	if strings.HasPrefix(path, "-") {
		return "./" + path
	}
	return path
}

// PyRunes decodes s like Python's bytes.decode("utf-8",
// errors="surrogateescape"): each invalid byte b becomes the surrogate rune
// 0xDC00+b, so the original bytes survive a later PyStringFromRunes encode.
// Valid UTF-8 sequences decode normally. A Go string already holds raw bytes,
// so this is purely an iteration layer: nothing is lossily replaced.
func PyRunes(s string) []rune {
	rs := make([]rune, 0, len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			rs = append(rs, 0xDC00+rune(s[i]))
			i++
			continue
		}
		rs = append(rs, r)
		i += size
	}
	return rs
}

// PyStringFromRunes encodes rs like Python's str.encode("utf-8",
// errors="surrogateescape"): surrogate-escape runes (0xDC80-0xDCFF) become
// their single original byte, everything else is UTF-8 encoded.
func PyStringFromRunes(rs []rune) string {
	var b strings.Builder
	b.Grow(len(rs))
	for _, r := range rs {
		if r >= 0xDC80 && r <= 0xDCFF {
			b.WriteByte(byte(r - 0xDC00))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// IsSurrogateEscapeRune reports whether r carries a surrogate-escaped byte.
func IsSurrogateEscapeRune(r rune) bool {
	return r >= 0xDC80 && r <= 0xDCFF
}
