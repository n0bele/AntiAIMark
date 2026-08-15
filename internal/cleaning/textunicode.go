// Layer A: invisible Unicode / homoglyph space detection and cleaning.
// Faithful Go port of service/scripts/text_unicode.py.
package cleaning

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"golang.org/x/text/unicode/norm"
	"golang.org/x/text/unicode/runenames"
)

// Format / invisible controls commonly used for steganography or broken pastes.
var stripCodepoints = map[rune]bool{
	0x00AD: true, // soft hyphen
	0x034F: true, // combining grapheme joiner
	0x061C: true, // Arabic letter mark
	0x115F: true, // Hangul choseong filler
	0x1160: true, // Hangul jungseong filler
	0x17B4: true, // Khmer vowel inherent AQ
	0x17B5: true, // Khmer vowel inherent AA
	0x180B: true, // Mongolian free variation selector-1
	0x180C: true,
	0x180D: true,
	0x180E: true, // Mongolian vowel separator
	0x200B: true, // zero width space
	0x200C: true, // zero width non-joiner
	0x200D: true, // zero width joiner
	0x200E: true, // LRM
	0x200F: true, // RLM
	0x202A: true, // LRE
	0x202B: true, // RLE
	0x202C: true, // PDF
	0x202D: true, // LRO
	0x202E: true, // RLO
	0x2060: true, // word joiner
	0x2061: true, // function application
	0x2062: true, // invisible times
	0x2063: true, // invisible separator
	0x2064: true, // invisible plus
	0x2066: true, // LRI
	0x2067: true, // RLI
	0x2068: true, // FSI
	0x2069: true, // PDI
	0x206A: true, // inhibit symmetric swapping
	0x206B: true,
	0x206C: true,
	0x206D: true,
	0x206E: true,
	0x206F: true,
	0xFEFF: true, // BOM / ZWNBSP
	0xFE00: true, // variation selectors
	0xFE01: true,
	0xFE02: true,
	0xFE03: true,
	0xFE04: true,
	0xFE05: true,
	0xFE06: true,
	0xFE07: true,
	0xFE08: true,
	0xFE09: true,
	0xFE0A: true,
	0xFE0B: true,
	0xFE0C: true,
	0xFE0D: true,
	0xFE0E: true,
	0xFE0F: true,
	0xFFF9: true, // interlinear annotation
	0xFFFA: true,
	0xFFFB: true,
}

// Spaces that look like (or substitute for) U+0020.
var spaceHomoglyphs = map[rune]string{
	0x00A0: " ", // no-break space
	0x1680: " ", // Ogham space mark
	0x2000: " ", // en quad
	0x2001: " ", // em quad
	0x2002: " ", // en space
	0x2003: " ", // em space
	0x2004: " ", // three-per-em space
	0x2005: " ", // four-per-em space
	0x2006: " ", // six-per-em space
	0x2007: " ", // figure space
	0x2008: " ", // punctuation space
	0x2009: " ", // thin space
	0x200A: " ", // hair space
	0x202F: " ", // narrow no-break space
	0x205F: " ", // medium mathematical space
	0x3000: " ", // ideographic space
}

// Optional confusable Latin lookalikes (aggressive mode only).
var latinConfusables = map[rune]string{
	0x0410: "A", // Cyrillic
	0x0412: "B",
	0x0415: "E",
	0x041A: "K",
	0x041C: "M",
	0x041D: "H",
	0x041E: "O",
	0x0420: "P",
	0x0421: "C",
	0x0422: "T",
	0x0425: "X",
	0x0430: "a",
	0x0435: "e",
	0x043E: "o",
	0x0440: "p",
	0x0441: "c",
	0x0443: "y",
	0x0445: "x",
	0x0456: "i",
	0xFF21: "A", // fullwidth
	0xFF22: "B",
	0xFF23: "C",
	0xFF24: "D",
	0xFF25: "E",
	0xFF26: "F",
	0xFF27: "G",
	0xFF28: "H",
	0xFF29: "I",
	0xFF2A: "J",
	0xFF2B: "K",
	0xFF2C: "L",
	0xFF2D: "M",
	0xFF2E: "N",
	0xFF2F: "O",
	0xFF30: "P",
	0xFF31: "Q",
	0xFF32: "R",
	0xFF33: "S",
	0xFF34: "T",
	0xFF35: "U",
	0xFF36: "V",
	0xFF37: "W",
	0xFF38: "X",
	0xFF39: "Y",
	0xFF3A: "Z",
	0xFF41: "a",
	0xFF42: "b",
	0xFF43: "c",
	0xFF44: "d",
	0xFF45: "e",
	0xFF46: "f",
	0xFF47: "g",
	0xFF48: "h",
	0xFF49: "i",
	0xFF4A: "j",
	0xFF4B: "k",
	0xFF4C: "l",
	0xFF4D: "m",
	0xFF4E: "n",
	0xFF4F: "o",
	0xFF50: "p",
	0xFF51: "q",
	0xFF52: "r",
	0xFF53: "s",
	0xFF54: "t",
	0xFF55: "u",
	0xFF56: "v",
	0xFF57: "w",
	0xFF58: "x",
	0xFF59: "y",
	0xFF5A: "z",
}

// variation selector range beyond FE0x (VS17–VS256 in Supplementary
// Special-purpose Plane).
const vsSupplementStart = 0xE0100
const vsSupplementEnd = 0xE01F0

// Bidi / directional format controls (subset of strip set, finer inspect labels).
var bidiCps = map[rune]bool{
	0x061C: true, 0x200E: true, 0x200F: true, 0x202A: true, 0x202B: true,
	0x202C: true, 0x202D: true, 0x202E: true, 0x2066: true, 0x2067: true,
	0x2068: true, 0x2069: true,
}

// Zero-width family (common edit-based carriers).
var zwFamily = map[rune]bool{0x200B: true, 0x200C: true, 0x200D: true, 0x2060: true, 0xFEFF: true, 0x180E: true}

func isPrivateUse(cp rune) bool {
	return (0xE000 <= cp && cp <= 0xF8FF) || (0xF0000 <= cp && cp <= 0xFFFFD) || (0x100000 <= cp && cp <= 0x10FFFD)
}

func isStripCp(cp rune) bool {
	if stripCodepoints[cp] {
		return true
	}
	if vsSupplementStart <= cp && cp < vsSupplementEnd {
		return true
	}
	// Tag characters used in some stego schemes (U+E0001–U+E007F)
	if 0xE0001 <= cp && cp <= 0xE007F {
		return true
	}
	if isPrivateUse(cp) {
		return true
	}
	return false
}

func stripKind(cp rune) string {
	if 0xE0001 <= cp && cp <= 0xE007F {
		return "tag_chars"
	}
	if (vsSupplementStart <= cp && cp < vsSupplementEnd) || (0xFE00 <= cp && cp <= 0xFE0F) || (0x180B <= cp && cp <= 0x180D) {
		return "variation_selector"
	}
	if bidiCps[cp] {
		return "bidi"
	}
	if zwFamily[cp] {
		return "zwj_family"
	}
	if isPrivateUse(cp) {
		return "private_use"
	}
	return "strip"
}

// Emoji presentation glue: zero-width joiner and text/emoji variation
// selectors. These are invisible carriers when free-floating, but after an
// emoji base they are part of the visible sequence (⚖️, 👨‍👩‍👧, ❤️‍🔥) and
// stripping them visibly alters the text.
var emojiGlueCodepoints = map[rune]bool{0x200D: true, 0xFE0E: true, 0xFE0F: true}

func isEmojiGlue(cp rune) bool { return emojiGlueCodepoints[cp] }

func isEmojiBase(cp rune) bool {
	if 0x1F000 <= cp && cp <= 0x1FAFF {
		return true
	}
	if 0x2600 <= cp && cp <= 0x27BF { // misc symbols / dingbats / arrows
		return true
	}
	if 0x2B00 <= cp && cp <= 0x2BFF { // misc symbols and arrows
		return true
	}
	switch cp {
	case 0x00A9, 0x00AE, 0x2122, 0x3030, 0x303D, 0x3297, 0x3299:
		return true
	}
	if cp == 0x0023 || cp == 0x002A || (0x0030 <= cp && cp <= 0x0039) { // keycap bases
		return true
	}
	return false
}

// ZWNJ/ZWJ are orthographic inside complex scripts (Persian می‌روم, Devanagari
// क्‍ष); flag emoji are an emoji base followed by tag chars (🏴); and a
// handful of Cf codepoints are normal Arabic/Syriac orthography, not carriers.
var scriptJoiners = map[rune]bool{0x200C: true, 0x200D: true}

const tagRangeStart = 0xE0020
const tagRangeEnd = 0xE0080

var orthographicCf = map[rune]bool{
	0x0600: true, 0x0601: true, 0x0602: true, 0x0603: true, 0x0604: true,
	0x0605: true, 0x06DD: true, 0x070F: true, 0x08E2: true, 0x110BD: true,
	0x110CD: true,
}

var mongolianFVS = map[rune]bool{0x180B: true, 0x180C: true, 0x180D: true}
var khmerVowels = map[rune]bool{0x17B4: true, 0x17B5: true}
var hangulFillers = map[rune]bool{0x115F: true, 0x1160: true}

func isScriptGlue(cp rune) bool {
	return mongolianFVS[cp] || khmerVowels[cp] || hangulFillers[cp]
}

// isLetterOrMark reports whether cp is in Unicode category L or M (Python's
// category(chr(cp))[0] in ("L", "M")).
func isLetterOrMark(cp rune) bool {
	if unicode.IsLetter(cp) {
		return true
	}
	return unicode.Is(unicode.Mn, cp) || unicode.Is(unicode.Mc, cp) || unicode.Is(unicode.Me, cp)
}

// isJoiningLetter ports _is_joining_letter: a NON-ASCII letter/mark — the
// neighbour that makes a joiner orthographic. ASCII letters never bind a
// ZWJ/ZWNJ (that would be a carrier, not orthography).
func isJoiningLetter(cp rune) bool {
	return cp > 0x7F && isLetterOrMark(cp)
}

func isMongolianLetter(cp rune) bool {
	return 0x1800 <= cp && cp <= 0x18AF && unicode.IsLetter(cp)
}

func isKhmerLetter(cp rune) bool {
	return 0x1780 <= cp && cp <= 0x17FF && unicode.IsLetter(cp)
}

func isHangulJamo(cp rune) bool {
	return (0x1100 <= cp && cp <= 0x11FF) ||
		(0xA960 <= cp && cp <= 0xA97C) || // Hangul Jamo Extended-A
		(0xD7B0 <= cp && cp <= 0xD7C6) // Hangul Jamo Extended-B
}

// isGlue reports a load-bearing invisible char: emoji glue, script joiner,
// flag tag char, or same-script filler/selector.
func isGlue(cp rune) bool {
	return isEmojiGlue(cp) || scriptJoiners[cp] || (tagRangeStart <= cp && cp < tagRangeEnd) || isScriptGlue(cp)
}

type charDecision struct {
	action  string // "keep" | "strip" | "replace"
	outChar string
	kind    string // inspect classification; "" when not suspicious
}

// decide classifies one input char for both inspect and clean.
func decide(ch rune, prevKept rune, prevKeptSet bool, normalizeSpaces, treatConfusables, stripEmojiGlue bool) charDecision {
	cp := ch
	if isEmojiGlue(cp) && !stripEmojiGlue {
		if prevKeptSet && isEmojiBase(prevKept) {
			return charDecision{"keep", string(ch), ""}
		}
	}
	if !stripEmojiGlue {
		if scriptJoiners[cp] && prevKeptSet && isJoiningLetter(prevKept) {
			return charDecision{"keep", string(ch), ""}
		}
		if tagRangeStart <= cp && cp < tagRangeEnd && prevKeptSet && isEmojiBase(prevKept) {
			return charDecision{"keep", string(ch), ""}
		}
		if mongolianFVS[cp] && prevKeptSet && isMongolianLetter(prevKept) {
			return charDecision{"keep", string(ch), ""}
		}
		if khmerVowels[cp] && prevKeptSet && isKhmerLetter(prevKept) {
			return charDecision{"keep", string(ch), ""}
		}
		if hangulFillers[cp] && prevKeptSet && isHangulJamo(prevKept) {
			return charDecision{"keep", string(ch), ""}
		}
		if orthographicCf[cp] {
			return charDecision{"keep", string(ch), ""}
		}
	}
	if isStripCp(cp) {
		return charDecision{"strip", "", stripKind(cp)}
	}
	if normalizeSpaces {
		if repl, ok := spaceHomoglyphs[cp]; ok {
			return charDecision{"replace", repl, "space"}
		}
	}
	if treatConfusables {
		if repl, ok := latinConfusables[cp]; ok {
			return charDecision{"replace", repl, "confusable"}
		}
	}
	_, isSpaceHomoglyph := spaceHomoglyphs[cp]
	if unicode.Is(unicode.Cf, cp) && !isSpaceHomoglyph {
		return charDecision{"strip", "", "other_cf"}
	}
	return charDecision{"keep", string(ch), ""}
}

func charLabel(ch rune) string {
	name := runenames.Name(ch)
	if name == "" {
		name = "UNKNOWN"
	}
	cat := runeCategory(ch)
	return fmt.Sprintf("U+%04X %s (%s)", ch, name, cat)
}

func runeCategory(ch rune) string {
	for _, rt := range []struct {
		name string
		rt   *unicode.RangeTable
	}{
		{"Lu", unicode.Lu}, {"Ll", unicode.Ll}, {"Lt", unicode.Lt}, {"Lm", unicode.Lm}, {"Lo", unicode.Lo},
		{"Mn", unicode.Mn}, {"Mc", unicode.Mc}, {"Me", unicode.Me},
		{"Nd", unicode.Nd}, {"Nl", unicode.Nl}, {"No", unicode.No},
		{"Pc", unicode.Pc}, {"Pd", unicode.Pd}, {"Ps", unicode.Ps}, {"Pe", unicode.Pe},
		{"Pi", unicode.Pi}, {"Pf", unicode.Pf}, {"Po", unicode.Po},
		{"Sm", unicode.Sm}, {"Sc", unicode.Sc}, {"Sk", unicode.Sk}, {"So", unicode.So},
		{"Zs", unicode.Zs}, {"Zl", unicode.Zl}, {"Zp", unicode.Zp},
		{"Cc", unicode.Cc}, {"Cf", unicode.Cf}, {"Cs", unicode.Cs}, {"Co", unicode.Co},
	} {
		if unicode.Is(rt.rt, ch) {
			return rt.name
		}
	}
	return "Cn"
}

func hitConfidence(kind string) string {
	// Layer A hits are edit-based carriers; space homoglyphs are weaker context.
	if kind == "space" {
		return "informational"
	}
	return "probable"
}

// CharHit mirrors the Python CharHit dataclass.
type CharHit struct {
	Codepoint rune
	Char      string
	Label     string
	Count     int
	Kind      string // strip | bidi | tag_chars | variation_selector | zwj_family | private_use | space | confusable | other_cf
	Samples   []int  // character offsets
}

// TextInspectReport mirrors the Python TextInspectReport dataclass.
type TextInspectReport struct {
	Length          int
	SuspiciousTotal int
	Hits            []CharHit
	Notes           []string
}

// ToDict renders the report as the Python to_dict() JSON shape.
func (r TextInspectReport) ToDict() map[string]interface{} {
	hits := make([]map[string]interface{}, 0, len(r.Hits))
	for _, h := range r.Hits {
		samples := h.Samples
		if len(samples) > 10 {
			samples = samples[:10]
		}
		hits = append(hits, map[string]interface{}{
			"codepoint":      fmt.Sprintf("U+%04X", h.Codepoint),
			"label":          h.Label,
			"count":          h.Count,
			"kind":           h.Kind,
			"confidence":     hitConfidence(h.Kind),
			"sample_offsets": samples,
		})
	}
	notes := r.Notes
	if notes == nil {
		notes = []string{}
	}
	return map[string]interface{}{
		"length":           r.Length,
		"suspicious_total": r.SuspiciousTotal,
		"hits":             hits,
		"notes":            notes,
	}
}

// InspectText inspects text for invisible/format Unicode carriers. The input
// string is decoded with PyRunes (Python errors="surrogateescape"), so
// invalid UTF-8 bytes ride along as surrogate-escape runes exactly like the
// Python original.
func InspectText(text string, aggressive bool, stripEmojiGlue bool) TextInspectReport {
	runes := PyRunes(text)
	type key struct {
		cp   rune
		kind string
	}
	buckets := map[key][]int{}
	var prevKept rune
	prevKeptSet := false
	for pos, ch := range runes {
		d := decide(ch, prevKept, prevKeptSet, true, aggressive, stripEmojiGlue)
		if d.kind == "" {
			// Kept; glue (emoji/script joiner/tag) does not advance the
			// "previous kept" base so ZWJ chains and flag runs stay bound.
			if !isGlue(ch) {
				prevKept = ch
				prevKeptSet = true
			}
			continue
		}
		k := key{ch, d.kind}
		if _, ok := buckets[k]; !ok {
			buckets[k] = []int{}
		}
		buckets[k] = append(buckets[k], pos)
		if d.action == "replace" {
			// Python: prev_kept = out_char (the replacement), not the original.
			if r := firstRune(d.outChar); r >= 0 {
				prevKept, prevKeptSet = r, true
			}
		}
	}

	type hit struct {
		cp      rune
		kind    string
		offsets []int
	}
	var hitsList []hit
	for k, offsets := range buckets {
		hitsList = append(hitsList, hit{k.cp, k.kind, offsets})
	}
	// sorted(buckets.items(), key=lambda x: (-len(x[1]), x[0][0]))
	sort.Slice(hitsList, func(i, j int) bool {
		if len(hitsList[i].offsets) != len(hitsList[j].offsets) {
			return len(hitsList[i].offsets) > len(hitsList[j].offsets)
		}
		return hitsList[i].cp < hitsList[j].cp
	})

	hits := make([]CharHit, 0, len(hitsList))
	total := 0
	for _, h := range hitsList {
		samples := h.offsets
		if len(samples) > 10 {
			samples = samples[:10]
		}
		hits = append(hits, CharHit{
			Codepoint: h.cp,
			Char:      string(h.cp),
			Label:     charLabel(h.cp),
			Count:     len(h.offsets),
			Kind:      h.kind,
			Samples:   samples,
		})
		total += len(h.offsets)
	}

	notes := []string{
		"Layer A only: invisible/format Unicode and space homoglyphs (edit-based carriers).",
		"Statistical (token-sampling) watermarks are not detectable here; use Layer B rewrite.",
		"Inspect kinds: strip, bidi, tag_chars, variation_selector, zwj_family, private_use, space, confusable, other_cf.",
		"Load-bearing invisibles are preserved by default: emoji glue (ZWJ/VS after an emoji base), script joiners (ZWNJ/ZWJ inside complex scripts), flag tag chars, same-script fillers/selectors (Mongolian FVS, Khmer inherent vowels, Hangul jamo fillers), and orthographic Arabic/Syriac Cf marks. Use --strip-emoji-glue for paranoid mode (strips them all).",
	}
	if len(hits) == 0 {
		notes = append(notes,
			"No deterministic Layer A (invisible Unicode/format) carriers detected; "+
				"statistical and pixel-domain marks are out of scope here.")
	}

	return TextInspectReport{
		Length:          len(runes),
		SuspiciousTotal: total,
		Hits:            hits,
		Notes:           notes,
	}
}

// firstRune returns the first rune of s, or -1 when s is empty.
func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return -1
}

// CleanTextResult carries the cleaned text and stats dict.
type CleanTextResult struct {
	Text  string
	Stats map[string]interface{}
}

// CleanText returns cleaned text and a stats dict. The input is processed
// over PyRunes (surrogateescape), and the result is re-encoded with
// PyStringFromRunes, so invalid UTF-8 input bytes round-trip byte-exactly —
// mirroring Python's decode/encode with errors="surrogateescape".
func CleanText(text string, nfkc bool, aggressiveHomoglyphs bool, normalizeSpaces bool, stripEmojiGlue bool) CleanTextResult {
	runes := PyRunes(text)
	removed := map[string]int{}
	replaced := map[string]int{}
	var out []rune
	var prevKept rune
	prevKeptSet := false

	for _, ch := range runes {
		d := decide(ch, prevKept, prevKeptSet, normalizeSpaces, aggressiveHomoglyphs, stripEmojiGlue)
		switch d.action {
		case "keep":
			out = append(out, ch)
			// Glue (emoji/script joiner/tag) does not advance the "previous
			// kept" base, so ZWJ chains (❤️‍🔥) and flag runs stay bound.
			if !isGlue(ch) {
				prevKept, prevKeptSet = ch, true
			}
		case "replace":
			for _, r := range d.outChar {
				out = append(out, r)
			}
			replaced[charLabel(ch)]++
			// Python: prev_kept = out_char (the replacement), not the original.
			if r := firstRune(d.outChar); r >= 0 {
				prevKept, prevKeptSet = r, true
			}
		default: // strip
			removed[charLabel(ch)]++
			// prev_kept unchanged
		}
	}

	result := PyStringFromRunes(out)
	if nfkc {
		outRunes := PyRunes(result)
		nfkced := nfkcRunes(outRunes)
		newResult := PyStringFromRunes(nfkced)
		if newResult != result {
			delta := absInt(len(nfkced) - len(outRunes))
			if delta == 0 {
				delta = 1
			}
			replaced["NFKC_normalize"] += delta
			result = newResult
		}
	}

	removedCount := 0
	for _, v := range removed {
		removedCount += v
	}
	replacedCount := 0
	for k, v := range replaced {
		if k != "NFKC_normalize" {
			replacedCount += v
		}
	}

	stats := map[string]interface{}{
		"input_length":   len(runes),
		"output_length":  len(PyRunes(result)),
		"removed":        removed,
		"replaced":       replaced,
		"removed_count":  removedCount,
		"replaced_count": replacedCount,
	}
	return CleanTextResult{Text: result, Stats: stats}
}

// nfkcRunes applies NFKC to rs without touching surrogate-escape runes: they
// are passed through unchanged and act as segment barriers. This matches
// Python's unicodedata.normalize on a surrogate-escaped string, because
// surrogates never participate in composition or reordering.
func nfkcRunes(rs []rune) []rune {
	var out []rune
	var seg []rune
	flush := func() {
		if len(seg) > 0 {
			out = append(out, PyRunes(norm.NFKC.String(string(seg)))...)
			seg = seg[:0]
		}
	}
	for _, r := range rs {
		if IsSurrogateEscapeRune(r) {
			flush()
			out = append(out, r)
			continue
		}
		seg = append(seg, r)
	}
	flush()
	return out
}

func absInt(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

// HumanReport renders a text inspect report as the Python human_report() does.
func HumanReport(report TextInspectReport) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Length: %d chars\n", report.Length)
	fmt.Fprintf(&b, "Suspicious: %d\n", report.SuspiciousTotal)
	if len(report.Hits) > 0 {
		b.WriteString("Hits:\n")
		for _, h := range report.Hits {
			samples := h.Samples
			if len(samples) > 5 {
				samples = samples[:5]
			}
			fmt.Fprintf(&b, "  [%s/%s] %s x%d @ %s\n", h.Kind, hitConfidence(h.Kind), h.Label, h.Count, pyIntList(samples))
		}
	}
	for _, n := range report.Notes {
		fmt.Fprintf(&b, "Note: %s\n", n)
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// pyIntList renders ints like Python's list repr: [0, 5, 9].
func pyIntList(xs []int) string {
	parts := make([]string, len(xs))
	for i, x := range xs {
		parts[i] = fmt.Sprintf("%d", x)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
