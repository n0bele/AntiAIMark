// inspect-image: Go port of service/scripts/inspect_image.py.
package main

import (
	"flag"
	"fmt"
	"os"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/cliutil"
	"antiaimark/internal/i18n"
)

func main() {
	var jsonOut bool
	var synthidDir string
	flag.BoolVar(&jsonOut, "json", false, "JSON report")
	flag.StringVar(&synthidDir, "synthid-dir", "", "reverse-SynthID checkout root for optional pixel SynthID scoring")
	var langFlag string
	cliutil.AddLangFlag(&langFlag)
	positional := cliutil.ParseAllowInterspersed()
	cliutil.Init(langFlag)
	if len(positional) < 1 {
		flag.Usage()
		os.Exit(2)
	}
	path := positional[0]
	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		cleaning.Eprint(i18n.T("cli.not_a_file", path))
		os.Exit(2)
	}

	report, err := cleaning.InspectImage(path, synthidDir)
	if err != nil {
		cleaning.Eprint("error: " + err.Error())
		os.Exit(1)
	}
	if jsonOut {
		if err := cleaning.EmitJSON(report.ToDict()); err != nil {
			cleaning.Eprint("error: " + err.Error())
			os.Exit(1)
		}
	} else {
		fmt.Println(i18n.T("cli.path", report.Path))
		fmt.Println(i18n.T("cli.format", report.Format))
		fmt.Println(i18n.T("cli.c2pa", report.HasC2PA))
		fmt.Println(i18n.T("cli.ai_metadata", report.HasAIMetadata))
		if len(report.Findings) > 0 {
			fmt.Println(i18n.T("cli.findings"))
			for _, f := range report.Findings {
				fmt.Printf("  - [%s] %s\n", cleaning.ClassifyFindingConfidence(f), f)
			}
		}
		ct, _ := report.Tools["c2patool"].(map[string]interface{})
		fmt.Printf("c2patool: %s\n", yesNo(dictBool(ct, "available")))
		et, _ := report.Tools["exiftool"].(map[string]interface{})
		fmt.Printf("exiftool: %s\n", yesNo(dictBool(et, "available")))
		if lines, ok := et["interesting_lines"].([]string); ok && len(lines) > 0 {
			fmt.Println("exiftool highlights:")
			highlights := lines
			if len(highlights) > 20 {
				highlights = highlights[:20]
			}
			for _, line := range highlights {
				fmt.Println("  " + line)
			}
		}
		if synthid, ok := report.Synthid.(map[string]interface{}); ok && dictBool(synthid, "available") {
			fmt.Printf("SynthID score: confidence %.3f (watermarked: %s)\n",
				dictFloat(synthid, "confidence"), yesNo(dictBool(synthid, "is_watermarked")))
			if dictBool(synthid, "is_watermarked") {
				fmt.Println("Hint: optional pixel removal is available via " +
					"clean_image.py IMG --remove-pixel ctrlregen " +
					"--ctrlregen-dir $NOAI_WATERMARK_DIR")
			}
		} else if synthid, ok := report.Synthid.(map[string]interface{}); ok && synthid["error"] != nil {
			fmt.Printf("SynthID score: error: %v\n", synthid["error"])
		}
	}

	if report.HasC2PA || report.HasAIMetadata {
		os.Exit(1)
	}
	os.Exit(0)
}

func dictBool(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
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

func yesNo(b bool) string {
	if b {
		return i18n.T("cli.yes")
	}
	return i18n.T("cli.no")
}
