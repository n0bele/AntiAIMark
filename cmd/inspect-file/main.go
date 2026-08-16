// inspect-file: Go port of service/scripts/inspect_file.py.
// Unified inspect: text, images (PNG/JPEG/WebP), and document containers.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/cliutil"
	"antiaimark/internal/i18n"
)

func main() {
	var jsonOut, aggressive, forceText bool
	var forceType string
	flag.BoolVar(&jsonOut, "json", false, "JSON report")
	flag.BoolVar(&aggressive, "aggressive", false, "Text: flag confusables")
	flag.StringVar(&forceType, "as", "auto", "text|image|container|auto")
	flag.BoolVar(&forceText, "force-text", false, "Scan as text even when the bytes look like a binary container")
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
	if fi.Size() > int64(cleaning.MaxInputBytes) {
		cleaning.Eprint(i18n.T("cli.over_cap_file", cleaning.MaxInputBytes, path))
		os.Exit(2)
	}

	var kind cleaning.Kind
	switch forceType {
	case "auto":
		k, err := cleaning.Classify(path)
		if err != nil {
			cliutil.FatalErr(err)
		}
		kind = k
	case "text":
		kind = cleaning.KindText
	case "image":
		kind = cleaning.KindImage
	case "container":
		kind = cleaning.KindContainer
	default:
		cleaning.Eprint(i18n.T("cli.invalid_choice", forceType, "'text', 'image', 'container', 'auto'"))
		os.Exit(2)
	}
	fileLabel := absPath(path)

	if kind == cleaning.KindText {
		text, err := cleaning.ReadTextInput(path, forceText, cleaning.RouterAdvice)
		if err != nil {
			cliutil.FatalErr(err)
		}
		report := cleaning.InspectText(text, aggressive, false)
		if jsonOut {
			payload := map[string]interface{}{"kind": "text", "path": fileLabel}
			for k, v := range report.ToDict() {
				payload[k] = v
			}
			if err := cleaning.EmitJSON(payload); err != nil {
				cliutil.FatalErr(err)
			}
		} else {
			fmt.Println(i18n.T("cli.file_label", fileLabel))
			fmt.Println(i18n.T("cli.kind", "text"))
			fmt.Println(cleaning.HumanReport(report))
		}
		if report.SuspiciousTotal == 0 {
			os.Exit(0)
		}
		os.Exit(1)
	}

	if kind == cleaning.KindImage {
		report, err := cleaning.InspectImage(path, "")
		if err != nil {
			cliutil.FatalErr(err)
		}
		if jsonOut {
			payload := map[string]interface{}{"kind": "image", "path": fileLabel}
			for k, v := range report.ToDict() {
				payload[k] = v
			}
			if err := cleaning.EmitJSON(payload); err != nil {
				cliutil.FatalErr(err)
			}
		} else {
			fmt.Println(i18n.T("cli.file_label", fileLabel))
			fmt.Println(i18n.T("cli.kind", "image"))
			fmt.Println(i18n.T("cli.path", report.Path))
			fmt.Println(i18n.T("cli.format", report.Format))
			fmt.Println(i18n.T("cli.c2pa", report.HasC2PA))
			fmt.Println(i18n.T("cli.ai_metadata", report.HasAIMetadata))
			for _, f := range report.Findings {
				fmt.Printf("  - [%s] %s\n", cleaning.ClassifyFindingConfidence(f), f)
			}
		}
		if report.HasC2PA || report.HasAIMetadata {
			os.Exit(1)
		}
		os.Exit(0)
	}

	report, err := cleaning.InspectContainer(path)
	if err != nil {
		cliutil.FatalErr(err)
	}
	if jsonOut {
		payload := map[string]interface{}{"kind": "container", "path": fileLabel}
		for k, v := range report.ToDict() {
			payload[k] = v
		}
		if err := cleaning.EmitJSON(payload); err != nil {
			cliutil.FatalErr(err)
		}
	} else {
		fmt.Println(i18n.T("cli.file_label", fileLabel))
		fmt.Println(i18n.T("cli.kind", "container"))
		fmt.Println(i18n.T("cli.path", report.Path))
		fmt.Println(i18n.T("cli.format", report.Format))
		fmt.Println(i18n.T("cli.c2pa", report.HasC2PA))
		fmt.Println(i18n.T("cli.ai_metadata", report.HasAIMetadata))
		for _, f := range report.Findings {
			fmt.Printf("  - [%s] %s\n", cleaning.ClassifyFindingConfidence(f), f)
		}
	}
	if report.HasC2PA || report.HasAIMetadata {
		os.Exit(1)
	}
	os.Exit(0)
}

// absPath mirrors Python's Path.resolve() for the file label.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
