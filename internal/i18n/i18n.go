// Package i18n provides the human-facing message catalog for every facade
// (CLIs, HTTP API, web UI, MCP server). Machine-readable JSON reports stay in
// English for cross-tool parity; only human-facing strings are localized.
//
// Supported languages: English (en, default and fallback), Chinese (zh),
// Spanish (es), French (fr), Russian (ru).
//
// Locale resolution order: explicit SetLocale / --lang flag, then the
// WATERMARKS_LANG environment variable, then the system locale (LANG /
// LC_ALL / LC_MESSAGES, POSIX and Windows forms), then "en". HTTP callers use
// NegotiateAcceptLanguage on the Accept-Language header.
package i18n

import (
	"fmt"
	"os"
	"strings"
)

// Tag is a supported language tag (primary subtag only).
type Tag string

const (
	English Tag = "en"
	Chinese Tag = "zh"
	Spanish Tag = "es"
	French  Tag = "fr"
	Russian Tag = "ru"
	Default     = English
)

// Tags lists the supported languages in stable order.
var Tags = []Tag{English, Chinese, Spanish, French, Russian}

// Names maps each tag to its endonym, for UI language pickers.
var Names = map[Tag]string{
	English: "English",
	Chinese: "中文",
	Spanish: "Español",
	French:  "Français",
	Russian: "Русский",
}

var current = Default

var catalogs = map[Tag]map[string]string{
	English: en,
	Chinese: zh,
	Spanish: es,
	French:  fr,
	Russian: ru,
}

// SetLocale sets the active locale for T. Unknown tags fall back to English.
func SetLocale(tag Tag) {
	current = Normalize(string(tag))
}

// Locale returns the active locale.
func Locale() Tag { return current }

// Normalize reduces any locale string ("zh-CN", "fr_FR.UTF-8", "es") to a
// supported primary subtag, defaulting to English.
func Normalize(any string) Tag {
	s := strings.TrimSpace(any)
	if s == "" {
		return Default
	}
	// strip encoding / modifier suffixes: fr_FR.UTF-8 -> fr_FR
	if i := strings.IndexAny(s, ".@"); i >= 0 {
		s = s[:i]
	}
	s = strings.ReplaceAll(s, "\\", "-")
	s = strings.ReplaceAll(s, "_", "-")
	parts := strings.Split(s, "-")
	primary := strings.ToLower(parts[0])
	switch primary {
	case "zh":
		return Chinese
	case "es":
		return Spanish
	case "fr":
		return French
	case "ru":
		return Russian
	case "en":
		return English
	}
	return Default
}

// Detect resolves the locale from the environment (WATERMARKS_LANG, then the
// system locale variables), mirroring the resolution order documented in the
// package comment. The optional override (usually a --lang flag value) wins
// when non-empty.
func Detect(override string) Tag {
	if override != "" {
		return Normalize(override)
	}
	if v := os.Getenv("WATERMARKS_LANG"); v != "" {
		return Normalize(v)
	}
	for _, key := range []string{"LC_ALL", "LC_MESSAGES", "LANG"} {
		if v := os.Getenv(key); v != "" {
			return Normalize(v)
		}
	}
	return Default
}

// NegotiateAcceptLanguage picks the best supported locale from an
// Accept-Language header value ("zh-CN,zh;q=0.9,en;q=0.8").
func NegotiateAcceptLanguage(header string) Tag {
	best := Tag("")
	bestQ := -1.0
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lang, q := part, 1.0
		if i := strings.Index(part, ";"); i >= 0 {
			lang = strings.TrimSpace(part[:i])
			for _, p := range strings.Split(part[i+1:], ";") {
				p = strings.TrimSpace(p)
				if strings.HasPrefix(p, "q=") {
					fmt.Sscanf(p[2:], "%f", &q)
				}
			}
		}
		tag := Normalize(lang)
		if tag == Default && !strings.HasPrefix(strings.ToLower(lang), "en") {
			// Normalize() fell back to English for an unsupported language —
			// only treat it as a real match when the input actually named
			// some form of English.
			continue
		}
		if q > bestQ {
			best, bestQ = tag, q
		}
	}
	if best == "" {
		return Default
	}
	return best
}

// T renders the message for the active locale, falling back to English, then
// to the key itself. args are fmt verbs inside the message.
func T(key string, args ...interface{}) string {
	return TFor(current, key, args...)
}

// TFor renders the message for an explicit locale with the same fallbacks.
func TFor(tag Tag, key string, args ...interface{}) string {
	msg, ok := catalogs[tag][key]
	if !ok {
		msg, ok = catalogs[English][key]
	}
	if !ok {
		return key
	}
	if len(args) == 0 {
		return msg
	}
	return fmt.Sprintf(msg, args...)
}

// Catalog exposes the flat key->message map of a locale (used by the web UI
// via GET /api/i18n?lang=zh).
func Catalog(tag Tag) map[string]string {
	tag = Normalize(string(tag))
	cat, ok := catalogs[tag]
	if !ok {
		cat = catalogs[English]
	}
	out := make(map[string]string, len(cat))
	for k, v := range cat {
		out[k] = v
	}
	return out
}
