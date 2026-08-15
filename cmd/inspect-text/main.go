// inspect-text: Go port of service/scripts/inspect_text.py.
package main

import (
	"flag"
	"fmt"
	"os"

	"watermarks-remover/internal/cleaning"
	"watermarks-remover/internal/cliutil"
)

func main() {
	var jsonOut, aggressive, stripEmojiGlue, forceText bool
	flag.BoolVar(&jsonOut, "json", false, "JSON report")
	flag.BoolVar(&aggressive, "aggressive", false, "Also flag Latin confusable / fullwidth lookalikes")
	flag.BoolVar(&stripEmojiGlue, "strip-emoji-glue", false, "Paranoid: flag all load-bearing invisibles too")
	flag.BoolVar(&forceText, "force-text", false, "Scan even when the input looks like a binary container")
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
	report := cleaning.InspectText(text, aggressive, stripEmojiGlue)
	if jsonOut {
		if err := cleaning.EmitJSON(report.ToDict()); err != nil {
			cliutil.FatalErr(err)
		}
	} else {
		fmt.Println(cleaning.HumanReport(report))
	}
	if report.SuspiciousTotal == 0 {
		os.Exit(0)
	}
	os.Exit(1)
}
