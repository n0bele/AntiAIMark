package i18n

import (
	"strings"
	"testing"
)

// TestCatalogCompleteness enforces that every language defines exactly the
// key set of the English reference catalog.
func TestCatalogCompleteness(t *testing.T) {
	for _, tag := range Tags {
		if tag == English {
			continue
		}
		cat := catalogs[tag]
		for key := range catalogs[English] {
			if _, ok := cat[key]; !ok {
				t.Errorf("%s catalog missing key %q", tag, key)
			}
		}
		for key := range cat {
			if _, ok := catalogs[English]; !ok {
				t.Errorf("%s catalog has extra key %q", tag, key)
			}
			if _, ok := catalogs[English][key]; !ok {
				t.Errorf("%s catalog has key %q not present in English reference", tag, key)
			}
		}
	}
}

// TestPlaceholdersMatch guards that translations keep the same fmt verbs as
// the English reference (a missing %s would corrupt localized output).
func TestPlaceholdersMatch(t *testing.T) {
	count := func(s string) map[string]int {
		out := map[string]int{}
		for _, verb := range []string{"%s", "%d", "%v", "%.3f", "%.1f"} {
			out[verb] = strings.Count(s, verb)
		}
		return out
	}
	for _, tag := range Tags {
		if tag == English {
			continue
		}
		for key, enMsg := range catalogs[English] {
			other, ok := catalogs[tag][key]
			if !ok {
				continue
			}
			for verb, n := range count(enMsg) {
				if strings.Count(other, verb) != n {
					t.Errorf("%s/%s: verb %s count mismatch (en=%d, %s=%d)", tag, key, verb, n, tag, strings.Count(other, verb))
				}
			}
		}
	}
}

func TestNormalize(t *testing.T) {
	cases := map[string]Tag{
		"en": English, "en_US": English, "en-US.UTF-8": English,
		"zh": Chinese, "zh_CN": Chinese, "zh-TW": Chinese, "zh_CN.UTF-8": Chinese,
		"es": Spanish, "es_ES@euro": Spanish, "es-MX": Spanish,
		"fr": French, "fr_FR.UTF-8": French,
		"ru": Russian, "ru_RU": Russian,
		"": Default, "xx": Default, "C": Default, "POSIX": Default,
	}
	for in, want := range cases {
		if got := Normalize(in); got != want {
			t.Errorf("Normalize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTForFallback(t *testing.T) {
	if got := TFor(English, "cli.not_a_file", "x.txt"); got != "not a file: x.txt" {
		t.Errorf("en rendering changed: %q", got)
	}
	if got := TFor(Chinese, "cli.not_a_file", "x.txt"); got != "不是文件：x.txt" {
		t.Errorf("zh rendering: %q", got)
	}
	// unknown key falls back to the key itself
	if got := TFor(Chinese, "no.such.key"); got != "no.such.key" {
		t.Errorf("unknown key: %q", got)
	}
	// unsupported locale falls back to English
	if got := TFor(Tag("de"), "cli.not_a_file", "x"); got != "not a file: x" {
		t.Errorf("de fallback: %q", got)
	}
}

func TestNegotiateAcceptLanguage(t *testing.T) {
	cases := map[string]Tag{
		"zh-CN,zh;q=0.9,en;q=0.8": Chinese,
		"fr-CH,fr;q=0.9,en;q=0.8": French,
		"ru":                      Russian,
		"es-ES,es;q=0.9":          Spanish,
		"en-GB,en;q=0.9":          English,
		"de-DE,de;q=0.9,en;q=0.5": English, // unsupported falls to q-ranked en
		"":                        Default,
		"de":                      Default,
		"en;q=0.3,zh-CN;q=0.9":    Chinese, // q values decide, not order
	}
	for header, want := range cases {
		if got := NegotiateAcceptLanguage(header); got != want {
			t.Errorf("Negotiate(%q) = %q, want %q", header, got, want)
		}
	}
}
