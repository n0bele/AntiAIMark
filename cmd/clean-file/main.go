// clean-file: Go port of service/scripts/clean_file.py.
// Unified clean: text Layer A, PNG/JPEG/WebP metadata, and document containers.
package main

import (
	"flag"
	"os"
	"path/filepath"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/cliutil"
	"antiaimark/internal/i18n"
)

func main() {
	var output string
	var inPlace, jsonOut, nfkc, aggressive, keepNonAI, forceText bool
	var forceType string
	flag.StringVar(&output, "o", "", "Output path")
	flag.StringVar(&output, "output", "", "Output path")
	flag.BoolVar(&inPlace, "in-place", false, "Overwrite input (writes .bak backup first)")
	flag.BoolVar(&jsonOut, "json", false, "JSON result on stdout")
	flag.BoolVar(&nfkc, "nfkc", false, "Text: NFKC normalize")
	flag.BoolVar(&aggressive, "aggressive-homoglyphs", false, "Text: map confusable lookalikes to ASCII")
	flag.BoolVar(&keepNonAI, "keep-non-ai-metadata", false, "Images: only drop C2PA/AI-looking segments")
	flag.StringVar(&forceType, "as", "auto", "auto|text|image|container")
	flag.BoolVar(&forceText, "force-text", false, "Clean as text even when the bytes look like a binary container")
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
		cleaning.Eprint(i18n.T("cli.invalid_choice", forceType, "'auto', 'text', 'image', 'container'"))
		os.Exit(2)
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
		os.Exit(0)
	}

	if kind == cleaning.KindImage {
		result, err := cleaning.CleanImage(src, dest, cleaning.CleanImageOptions{
			StripAllMetadata: cleaning.BoolPtr(!keepNonAI),
		})
		if err != nil {
			cleaning.Eprint("error: " + err.Error())
			os.Exit(1)
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
			os.Exit(1)
		}
		os.Exit(0)
	}

	result, err := cleaning.CleanContainer(src, dest, cleaning.CleanContainerOptions{})
	if err != nil {
		cleaning.Eprint("error: " + err.Error())
		os.Exit(1)
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
		os.Exit(1)
	}
	os.Exit(0)
}

func reportBool(m map[string]interface{}, key string) bool {
	b, _ := m[key].(bool)
	return b
}

func reportStrs(m map[string]interface{}, key string) []string {
	switch t := m[key].(type) {
	case []string:
		return t
	case []interface{}:
		out := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
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
