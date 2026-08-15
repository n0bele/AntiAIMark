// clean-text: Go port of service/scripts/clean_text.py (Layer A).
package main

import (
	"flag"
	"os"

	"watermarks-remover/internal/cleaning"
	"watermarks-remover/internal/cliutil"
	"watermarks-remover/internal/i18n"
)

func main() {
	var output string
	var nfkc, aggressive, noNormalizeSpaces, stripEmojiGlue, statsFlag, forceText, inPlace bool
	flag.StringVar(&output, "o", "", "Output path (default: stdout or *.cleaned.*)")
	flag.StringVar(&output, "output", "", "Output path (default: stdout or *.cleaned.*)")
	flag.BoolVar(&nfkc, "nfkc", false, "Apply Unicode NFKC after scrub")
	flag.BoolVar(&aggressive, "aggressive-homoglyphs", false, "Map Cyrillic/fullwidth Latin confusables to ASCII Latin")
	flag.BoolVar(&noNormalizeSpaces, "no-normalize-spaces", false, "Do not rewrite exotic spaces to U+0020")
	flag.BoolVar(&stripEmojiGlue, "strip-emoji-glue", false, "Paranoid: strip all load-bearing invisibles too (emoji glue, script joiners, flag tags, same-script fillers/selectors, orthographic Cf)")
	flag.BoolVar(&statsFlag, "stats", false, "Print stats JSON to stderr")
	flag.BoolVar(&forceText, "force-text", false, "Clean even when the input looks like a binary container (this rewrites the bytes and will corrupt the file)")
	flag.BoolVar(&inPlace, "in-place", false, "Overwrite input file (creates .bak backup)")
	var langFlag string
	cliutil.AddLangFlag(&langFlag)
	positional := cliutil.ParseAllowInterspersed()
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
			os.Exit(2)
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
	os.Exit(0)
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
