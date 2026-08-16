package cliapp

import (
	"fmt"
	"os"
	"path/filepath"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/cliutil"
	"antiaimark/internal/i18n"
)

// CleanFile implements the clean-file CLI: unified clean that routes to the
// text / image / container pipeline by content type.
func CleanFile(args []string) int {
	fs := newFlagSet("clean-file")
	var output string
	var inPlace, jsonOut, nfkc, aggressive, keepNonAI, forceText bool
	var forceType string
	fs.StringVar(&output, "o", "", "Output path")
	fs.StringVar(&output, "output", "", "Output path")
	fs.BoolVar(&inPlace, "in-place", false, "Overwrite input (writes .bak backup first)")
	fs.BoolVar(&jsonOut, "json", false, "JSON result on stdout")
	fs.BoolVar(&nfkc, "nfkc", false, "Text: NFKC normalize")
	fs.BoolVar(&aggressive, "aggressive-homoglyphs", false, "Text: map confusable lookalikes to ASCII")
	fs.BoolVar(&keepNonAI, "keep-non-ai-metadata", false, "Images: only drop C2PA/AI-looking segments")
	fs.StringVar(&forceType, "as", "auto", "auto|text|image|container")
	fs.BoolVar(&forceText, "force-text", false, "Clean as text even when the bytes look like a binary container")
	var langFlag string
	cliutil.AddLangFlagFS(fs, &langFlag)
	positional := cliutil.ParseAllowInterspersedFS(fs, args)
	cliutil.Init(langFlag)
	if len(positional) < 1 {
		fs.Usage()
		return 2
	}
	path := positional[0]

	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		cleaning.Eprint(i18n.T("cli.not_a_file", path))
		return 2
	}
	if fi.Size() > int64(cleaning.MaxInputBytes) {
		cleaning.Eprint(i18n.T("cli.over_cap_file", cleaning.MaxInputBytes, path))
		return 2
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
		cleaning.Eprint(i18n.T("cli.invalid_choice", forceType, "'auto', 'text', 'image', 'container'"))
		return 2
	}

	// classify() falls back to "text" for unrecognised bytes, so an unknown
	// binary would otherwise be decoded, scrubbed and written back mangled.
	// Sniff before --in-place takes a backup: refusing afterwards would leave a
	// .bak sidecar behind for a file this run never touches.
	var raw []byte
	if kind == cleaning.KindText {
		raw, err = os.ReadFile(path)
		if err != nil {
			cliutil.FatalErr(err)
		}
		if err := cleaning.GuardBinary(raw, path, forceText, cleaning.RouterAdvice); err != nil {
			cliutil.FatalErr(err)
		}
	}

	var src, dest string
	if inPlace {
		if _, err := cleaning.BackupPath(path); err != nil {
			cliutil.FatalErr(err)
		}
		dest = path
		src = path + ".bak" // clean from the backup, like Python (src = bak)
	} else {
		src = path
		if output != "" {
			dest = output
		} else {
			dest = cleaning.CleanedPath(path, ".cleaned")
		}
	}

	if kind == cleaning.KindText {
		res := cleaning.CleanText(string(raw), nfkc, aggressive, true, false)
		if err := os.MkdirAll(filepath.Dir(dest), 0o777); err != nil {
			cliutil.FatalErr(err)
		}
		if err := cleaning.SafeWriteText(dest, res.Text); err != nil {
			cliutil.FatalErr(err)
		}
		result := map[string]interface{}{
			"kind":   "text",
			"input":  path,
			"output": dest,
			"stats":  res.Stats,
		}
		if jsonOut {
			if err := cleaning.EmitJSON(result); err != nil {
				cliutil.FatalErr(err)
			}
		} else {
			cleaning.Eprint(i18n.T("cli.wrote_text", dest, intStat(res.Stats, "removed_count"), intStat(res.Stats, "replaced_count")))
		}
		return 0
	}

	if kind == cleaning.KindImage {
		result, err := cleaning.CleanImage(src, dest, cleaning.CleanImageOptions{
			StripAllMetadata: cleaning.BoolPtr(!keepNonAI),
		})
		if err != nil {
			cleaning.Eprint("error: " + err.Error())
			return 1
		}
		report := map[string]interface{}{"kind": "image"}
		for k, v := range result {
			report[k] = v
		}
		residual := reportBool(report, "still_has_c2pa") || reportBool(report, "still_has_ai_metadata")
		if jsonOut {
			if err := cleaning.EmitJSON(report); err != nil {
				cliutil.FatalErr(err)
			}
		} else {
			cleaning.Eprint(i18n.T("cli.wrote_image", report["output"], report["bytes_in"], report["bytes_out"]))
			for _, a := range reportStrs(report, "actions") {
				cleaning.Eprint("  - " + a)
			}
			if residual {
				cleaning.Eprint(i18n.T("cli.residual_warning"))
			}
		}
		if residual {
			return 1
		}
		return 0
	}

	result, err := cleaning.CleanContainer(src, dest, cleaning.CleanContainerOptions{})
	if err != nil {
		cleaning.Eprint("error: " + err.Error())
		return 1
	}
	report := map[string]interface{}{"kind": "container"}
	for k, v := range result {
		report[k] = v
	}
	residual := reportBool(report, "still_has_c2pa") || reportBool(report, "still_has_ai_metadata")
	degraded := false
	if meta, ok := report["meta"].(map[string]interface{}); ok {
		degraded, _ = meta["degraded"].(bool)
	}
	if jsonOut {
		if err := cleaning.EmitJSON(report); err != nil {
			cliutil.FatalErr(err)
		}
	} else {
		cleaning.Eprint(i18n.T("cli.wrote_container", report["output"], report["format"]))
		for _, a := range reportStrs(report, "actions") {
			cleaning.Eprint("  - " + a)
		}
		if residual {
			cleaning.Eprint(i18n.T("cli.residual_warning"))
			for _, f := range reportStrs(report, "post_findings") {
				cleaning.Eprint("  ! " + f)
			}
		}
	}
	// A degraded (best-effort) PDF copy warns but is not a hard failure.
	if residual && !degraded {
		return 1
	}
	return 0
}

// InspectFile implements the inspect-file CLI: unified inspect that routes to
// the text / image / container scanner by content type.
func InspectFile(args []string) int {
	fs := newFlagSet("inspect-file")
	var jsonOut, aggressive, forceText bool
	var forceType string
	fs.BoolVar(&jsonOut, "json", false, "JSON report")
	fs.BoolVar(&aggressive, "aggressive", false, "Text: flag confusables")
	fs.StringVar(&forceType, "as", "auto", "text|image|container|auto")
	fs.BoolVar(&forceText, "force-text", false, "Scan as text even when the bytes look like a binary container")
	var langFlag string
	cliutil.AddLangFlagFS(fs, &langFlag)
	positional := cliutil.ParseAllowInterspersedFS(fs, args)
	cliutil.Init(langFlag)
	if len(positional) < 1 {
		fs.Usage()
		return 2
	}
	path := positional[0]

	fi, err := os.Stat(path)
	if err != nil || fi.IsDir() {
		cleaning.Eprint(i18n.T("cli.not_a_file", path))
		return 2
	}
	if fi.Size() > int64(cleaning.MaxInputBytes) {
		cleaning.Eprint(i18n.T("cli.over_cap_file", cleaning.MaxInputBytes, path))
		return 2
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
		return 2
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
			return 0
		}
		return 1
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
			return 1
		}
		return 0
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
		return 1
	}
	return 0
}

// absPath mirrors Python's Path.resolve() for the file label.
func absPath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	return abs
}
