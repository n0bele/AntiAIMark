package cleaning

import (
	"strings"
	"testing"
)

func ct(text string, opts ...bool) CleanTextResult {
	var nfkc, aggressive, normSpaces, stripGlue bool
	normSpaces = true
	if len(opts) > 0 {
		nfkc = opts[0]
	}
	if len(opts) > 1 {
		aggressive = opts[1]
	}
	if len(opts) > 2 {
		normSpaces = opts[2]
	}
	if len(opts) > 3 {
		stripGlue = opts[3]
	}
	return CleanText(text, nfkc, aggressive, normSpaces, stripGlue)
}

func it(m map[string]interface{}, key string) int {
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

func TestStripsZeroWidthAndSoftHyphen(t *testing.T) {
	raw := "Hello\u200bWorld\u00ad!"
	res := ct(raw)
	if res.Text != "HelloWorld!" {
		t.Fatalf("got %q", res.Text)
	}
	if it(res.Stats, "removed_count") < 2 {
		t.Fatalf("removed_count = %d", it(res.Stats, "removed_count"))
	}
}

func TestNormalizesExoticSpaces(t *testing.T) {
	raw := "a\u2003b\u3000c"
	res := ct(raw)
	if res.Text != "a b c" {
		t.Fatalf("got %q", res.Text)
	}
	if it(res.Stats, "replaced_count") < 2 {
		t.Fatalf("replaced_count = %d", it(res.Stats, "replaced_count"))
	}
}

func TestInspectFindsZWSP(t *testing.T) {
	rep := InspectText("x\u200by", false, false)
	if rep.SuspiciousTotal < 1 {
		t.Fatal("suspicious_total = 0")
	}
	hasKind := false
	for _, h := range rep.Hits {
		if h.Kind == "zwj_family" || h.Kind == "strip" {
			hasKind = true
		}
	}
	if !hasKind {
		t.Fatal("no zwj_family/strip hit")
	}
}

func TestInspectTagChars(t *testing.T) {
	raw := "hi" + string(rune(0xE0041)) + "there"
	rep := InspectText(raw, false, false)
	if rep.SuspiciousTotal < 1 {
		t.Fatal("suspicious_total = 0")
	}
	found := false
	for _, h := range rep.Hits {
		if h.Kind == "tag_chars" {
			found = true
		}
	}
	if !found {
		t.Fatal("no tag_chars hit")
	}
	res := ct(raw)
	if strings.ContainsRune(res.Text, rune(0xE0041)) {
		t.Fatal("tag char not removed")
	}
}

func TestInspectBidi(t *testing.T) {
	raw := "ab\u202eef"
	rep := InspectText(raw, false, false)
	found := false
	for _, h := range rep.Hits {
		if h.Kind == "bidi" {
			found = true
		}
	}
	if !found {
		t.Fatal("no bidi hit")
	}
	res := ct(raw)
	if strings.ContainsRune(res.Text, 0x202E) {
		t.Fatal("RLO not removed")
	}
}

func TestCleanPreservesNormalText(t *testing.T) {
	raw := "Normal ASCII and café — fine."
	res := ct(raw)
	if res.Text != raw {
		t.Fatalf("got %q", res.Text)
	}
	if it(res.Stats, "removed_count") != 0 {
		t.Fatal("removed_count != 0")
	}
}

func TestAggressiveConfusable(t *testing.T) {
	res := ct("p\u0430y", false, true)
	if res.Text != "pay" {
		t.Fatalf("got %q", res.Text)
	}
}

func TestCleanPreservesEmojiVS16(t *testing.T) {
	raw := "Balance returns. \u2696\ufe0f"
	res := ct(raw)
	if res.Text != raw {
		t.Fatalf("got %q", res.Text)
	}
	if it(res.Stats, "removed_count") != 0 {
		t.Fatal("removed_count != 0")
	}
}

func TestCleanPreservesZWJFamily(t *testing.T) {
	raw := "Family time: \U0001F468\u200D\U0001F469\u200D\U0001F467"
	res := ct(raw)
	if res.Text != raw {
		t.Fatalf("got %q", res.Text)
	}
	if it(res.Stats, "removed_count") != 0 {
		t.Fatal("removed_count != 0")
	}
}

func TestCleanPreservesZWJChain(t *testing.T) {
	raw := "\u2764\ufe0f\u200d\U0001F525"
	res := ct(raw)
	if res.Text != raw {
		t.Fatalf("got %q", res.Text)
	}
}

func TestCleanStripsFloatingEmojiGlue(t *testing.T) {
	res := ct("a\u200db\ufe0f")
	if res.Text != "ab" {
		t.Fatalf("got %q", res.Text)
	}
	if it(res.Stats, "removed_count") != 2 {
		t.Fatalf("removed_count = %d", it(res.Stats, "removed_count"))
	}
}

func TestInspectEmojiGlueNotSuspiciousByDefault(t *testing.T) {
	raw := "Balance returns. \u2696\ufe0f Family time: \U0001F468\u200D\U0001F469\u200D\U0001F467"
	rep := InspectText(raw, false, false)
	if rep.SuspiciousTotal != 0 {
		t.Fatalf("suspicious_total = %d", rep.SuspiciousTotal)
	}
}

func TestInspectFloatingEmojiGlueIsSuspicious(t *testing.T) {
	rep := InspectText("a\u200d", false, false)
	if rep.SuspiciousTotal < 1 {
		t.Fatal("suspicious_total = 0")
	}
}

func TestCleanStripEmojiGlueFlag(t *testing.T) {
	res := ct("\u2696\ufe0f", false, false, true, true)
	if res.Text != "\u2696" {
		t.Fatalf("got %q", res.Text)
	}
	if it(res.Stats, "removed_count") != 1 {
		t.Fatalf("removed_count = %d", it(res.Stats, "removed_count"))
	}
}

func TestCleanPreservesScriptJoiners(t *testing.T) {
	for _, raw := range []string{"\u0645\u06cc\u200c\u0631\u0648\u0645", "\u0915\u094d\u200d\u0937"} {
		res := ct(raw)
		if res.Text != raw {
			t.Fatalf("got %q want %q", res.Text, raw)
		}
	}
}

func TestCleanPreservesFlagTagSequence(t *testing.T) {
	raw := "\U0001F3F4\U000E0067\U000E0062\U000E0073\U000E0063\U000E0074\U000E007F"
	res := ct(raw)
	if res.Text != raw {
		t.Fatalf("got %q", res.Text)
	}
}

func TestCleanPreservesOrthographicArabicCf(t *testing.T) {
	raw := "x\u0600y\u06ddz"
	res := ct(raw)
	if res.Text != raw {
		t.Fatalf("got %q", res.Text)
	}
}

func TestCleanStillStripsJoinersBetweenLatin(t *testing.T) {
	for _, raw := range []string{"a\u200db", "a\u200cb", "ab\u200c"} {
		res := ct(raw)
		if strings.ContainsRune(res.Text, 0x200C) || strings.ContainsRune(res.Text, 0x200D) {
			t.Fatalf("joiner survived in %q", res.Text)
		}
	}
}

func TestCleanPreservesMongolianFVS(t *testing.T) {
	for _, raw := range []string{"\u1820\u180b\u1821", "\u1820\u180c\u1821", "\u1820\u180d\u1821", "\u1820\u180b\u180c\u1821"} {
		res := ct(raw)
		if res.Text != raw {
			t.Fatalf("got %q want %q", res.Text, raw)
		}
	}
}

func TestCleanPreservesKhmerInherentVowels(t *testing.T) {
	for _, raw := range []string{"\u1780\u17b4\u1781", "\u1780\u17b5\u1781"} {
		res := ct(raw)
		if res.Text != raw {
			t.Fatalf("got %q", res.Text)
		}
	}
}

func TestCleanPreservesHangulFillers(t *testing.T) {
	for _, raw := range []string{"\u1100\u115f\u1161", "\u1100\u1160\u1161"} {
		res := ct(raw)
		if res.Text != raw {
			t.Fatalf("got %q", res.Text)
		}
	}
}

func TestCleanStripsPrivateUse(t *testing.T) {
	raw := "a\ue000b\U000f0000c\U0010fffd"
	res := ct(raw)
	if res.Text != "abc" {
		t.Fatalf("got %q", res.Text)
	}
	if it(res.Stats, "removed_count") < 3 {
		t.Fatalf("removed_count = %d", it(res.Stats, "removed_count"))
	}
}

func TestCleanStripsFloatingScriptGlue(t *testing.T) {
	for _, raw := range []string{"a\u180bb", "a\u17b4b", "a\u115fb", "\u180b", "\u1160"} {
		res := ct(raw)
		for _, r := range []rune{0x180B, 0x17B4, 0x115F, 0x1160} {
			if strings.ContainsRune(res.Text, r) {
				t.Fatalf("rune U+%04X survived in %q", r, res.Text)
			}
		}
	}
}

func TestInspectScriptGlueNotSuspiciousByDefault(t *testing.T) {
	raw := "\u1820\u180b\u1821\u1780\u17b4\u1781\u1100\u115f\u1161"
	rep := InspectText(raw, false, false)
	if rep.SuspiciousTotal != 0 {
		t.Fatalf("suspicious_total = %d", rep.SuspiciousTotal)
	}
}

func TestInspectPrivateUse(t *testing.T) {
	rep := InspectText("a\ue000b", false, false)
	found := false
	for _, h := range rep.Hits {
		if h.Kind == "private_use" {
			found = true
		}
	}
	if !found {
		t.Fatal("no private_use hit")
	}
}

func TestNFKCStats(t *testing.T) {
	res := ct("\uFF41\uFF42", true)
	if res.Text != "ab" {
		t.Fatalf("got %q", res.Text)
	}
	if n := it(res.Stats, "removed_count"); n != 0 {
		t.Fatalf("removed_count = %d", n)
	}
	if repl, ok := res.Stats["replaced"].(map[string]interface{}); ok {
		if repl["NFKC_normalize"] == nil {
			t.Fatal("NFKC_normalize not in replaced")
		}
	}
}

// TestCleanTextSurrogateescapeRoundTrip locks in the Python-parity behavior:
// invalid UTF-8 input bytes ride through CleanText byte-exactly
// (decode/encode with errors="surrogateescape"), they are not replaced with
// U+FFFD.
func TestCleanTextSurrogateescapeRoundTrip(t *testing.T) {
	in := "He\xcd\x90llo\u200bworld" // Latin-1 É byte + ZWSP
	res := CleanText(in, false, false, true, false)
	if res.Text != "He\xcd\x90llo world"[0:2]+"\xcd\x90"+"llo"+""+"world" {
		// exact expectation spelled out below for clarity
	}
	want := "He\xcd\x90lloworld"
	if res.Text != want {
		t.Fatalf("surrogateescape round-trip broken: got %q want %q", res.Text, want)
	}
	if it(res.Stats, "removed_count") != 1 {
		t.Fatalf("ZWSP not counted as removed: %v", res.Stats)
	}
}

// TestInspectTextLengthCountsRawBytesAsRunes mirrors Python len() on a
// surrogate-escaped string: each invalid byte is one codepoint.
func TestInspectTextLengthCountsRawBytesAsRunes(t *testing.T) {
	rep := InspectText("a\xef\xb7\xb0b", false, false) // private-use codepoint kept
	if rep.Length != 3 {
		t.Fatalf("length = %d, want 3", rep.Length)
	}
}
