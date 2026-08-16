// Package cliapp hosts the implementations behind both the standalone CLI
// binaries (cmd/*) and the merged antiaimark binary: one core library, many
// thin facades. Every Run* takes the remaining argument slice and returns the
// process exit code, mirroring the Python originals' exit semantics
// (0 clean, 1 findings/residual, 2 bad input).
package cliapp

import (
	"flag"
	"fmt"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/cliutil"
	"antiaimark/internal/i18n"
)

// newFlagSet builds a ContinueOnError FlagSet for one subcommand: on a parse
// error the flag package prints the message + usage itself, and the caller
// maps it to exit code 2 like the standalone binaries did.
func newFlagSet(name string) *flag.FlagSet {
	return flag.NewFlagSet(name, flag.ContinueOnError)
}

// --- shared helpers (formerly duplicated across cmd/*) ---

func dictBool(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

func reportBool(m map[string]interface{}, key string) bool {
	return dictBool(m, key)
}

func dictFloat(m map[string]interface{}, key string) float64 {
	if m == nil {
		return 0
	}
	switch t := m[key].(type) {
	case float64:
		return t
	case int:
		return float64(t)
	}
	return 0
}

func strList(v interface{}) []string {
	switch t := v.(type) {
	case []string:
		return t
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, s := range t {
			if str, ok := s.(string); ok {
				out = append(out, str)
			}
		}
		return out
	}
	return nil
}

func reportStrs(m map[string]interface{}, key string) []string {
	return strList(m[key])
}

func intStat(m map[string]interface{}, key string) int {
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

func yesNo(b bool) string {
	if b {
		return i18n.T("cli.yes")
	}
	return i18n.T("cli.no")
}

// intOpt is a flag.Value for optional ints: unset stays unset (Python default
// None), so --ctrlregen-seed 0 is distinguishable from omitting the flag.
type intOpt struct {
	p   *int
	set *bool
}

func (o *intOpt) String() string {
	if o == nil || !*o.set {
		return ""
	}
	return fmt.Sprintf("%d", *o.p)
}

func (o *intOpt) Set(v string) error {
	var n int
	if _, err := fmt.Sscanf(v, "%d", &n); err != nil {
		return err
	}
	*o.p = n
	*o.set = true
	return nil
}

// --- text commands ---

// CleanText implements the clean-text CLI (Layer A scrub).
func CleanText(args []string) int {
	fs := newFlagSet("clean-text")
	var output string
	var nfkc, aggressive, noNormalizeSpaces, stripEmojiGlue, statsFlag, forceText, inPlace bool
	fs.StringVar(&output, "o", "", "Output path (default: stdout or *.cleaned.*)")
	fs.StringVar(&output, "output", "", "Output path (default: stdout or *.cleaned.*)")
	fs.BoolVar(&nfkc, "nfkc", false, "Apply Unicode NFKC after scrub")
	fs.BoolVar(&aggressive, "aggressive-homoglyphs", false, "Map Cyrillic/fullwidth Latin confusables to ASCII Latin")
	fs.BoolVar(&noNormalizeSpaces, "no-normalize-spaces", false, "Do not rewrite exotic spaces to U+0020")
	fs.BoolVar(&stripEmojiGlue, "strip-emoji-glue", false, "Paranoid: strip all load-bearing invisibles too (emoji glue, script joiners, flag tags, same-script fillers/selectors, orthographic Cf)")
	fs.BoolVar(&statsFlag, "stats", false, "Print stats JSON to stderr")
	fs.BoolVar(&forceText, "force-text", false, "Clean even when the input looks like a binary container (this rewrites the bytes and will corrupt the file)")
	fs.BoolVar(&inPlace, "in-place", false, "Overwrite input file (creates .bak backup)")
	var langFlag string
	cliutil.AddLangFlagFS(fs, &langFlag)
	positional := cliutil.ParseAllowInterspersedFS(fs, args)
	cliutil.Init(langFlag)

	path := "-"
	if len(positional) > 0 {
		path = positional[0]
	}

	text, err := cleaning.ReadTextInput(path, forceText, nil)
	if err != nil {
		cliutil.FatalErr(err)
	}
	res := cleaning.CleanText(text, nfkc, aggressive, !noNormalizeSpaces, stripEmojiGlue)

	out := output
	if inPlace {
		if path == "-" {
			cleaning.Eprint(i18n.T("cli.in_place_requires"))
			return 2
		}
		if _, err := cleaning.BackupPath(path); err != nil {
			cliutil.FatalErr(err)
		}
		out = path
	} else if out == "" && path != "-" {
		out = cleaning.CleanedPath(path, ".cleaned")
	}

	if err := cleaning.WriteTextOutput(res.Text, out); err != nil {
		cliutil.FatalErr(err)
	}

	if statsFlag {
		if b, err := cleaning.MarshalIndentNoEscape(res.Stats); err == nil {
			cleaning.Eprint(string(b))
		}
	} else {
		cleaning.Eprint(i18n.T("cli.summary_text",
			intStat(res.Stats, "removed_count"),
			intStat(res.Stats, "replaced_count"),
			intStat(res.Stats, "input_length"),
			intStat(res.Stats, "output_length")))
	}
	return 0
}

// InspectText implements the inspect-text CLI.
func InspectText(args []string) int {
	fs := newFlagSet("inspect-text")
	var jsonOut, aggressive, stripEmojiGlue, forceText bool
	fs.BoolVar(&jsonOut, "json", false, "JSON report")
	fs.BoolVar(&aggressive, "aggressive", false, "Also flag Latin confusable / fullwidth lookalikes")
	fs.BoolVar(&stripEmojiGlue, "strip-emoji-glue", false, "Paranoid: flag all load-bearing invisibles too")
	fs.BoolVar(&forceText, "force-text", false, "Scan even when the input looks like a binary container")
	var langFlag string
	cliutil.AddLangFlagFS(fs, &langFlag)
	positional := cliutil.ParseAllowInterspersedFS(fs, args)
	cliutil.Init(langFlag)

	path := "-"
	if len(positional) > 0 {
		path = positional[0]
	}

	text, err := cleaning.ReadTextInput(path, forceText, nil)
	if err != nil {
		cliutil.FatalErr(err)
	}
	report := cleaning.InspectText(text, aggressive, stripEmojiGlue)
	if jsonOut {
		if err := cleaning.EmitJSON(report.ToDict()); err != nil {
			cliutil.FatalErr(err)
		}
	} else {
		fmt.Println(cleaning.HumanReport(report))
	}
	if report.SuspiciousTotal == 0 {
		return 0
	}
	return 1
}
