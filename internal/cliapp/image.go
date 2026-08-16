package cliapp

import (
	"fmt"
	"os"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/cliutil"
	"antiaimark/internal/i18n"
)

// CleanImage implements the clean-image CLI (strip C2PA/AI metadata from
// PNG/JPEG/WebP; pixels are untouched).
func CleanImage(args []string) int {
	fs := newFlagSet("clean-image")
	var output string
	var inPlace, keepNonAI, jsonOut bool
	var synthidDir, removePixel, ctrlregenDir, ctrlregenDevice string
	var ctrlregenStrength float64
	var ctrlregenSteps, ctrlregenTimeout int
	var ctrlregenSeedSet bool
	var ctrlregenSeed int
	var markdiffusionDir, markdiffusionModel, markdiffusionDevice string
	var markdiffusionStrength float64
	var markdiffusionSize, markdiffusionSteps, markdiffusionTimeout int

	fs.StringVar(&output, "o", "", "Output path (default: *.cleaned.*)")
	fs.StringVar(&output, "output", "", "Output path (default: *.cleaned.*)")
	fs.BoolVar(&inPlace, "in-place", false, "Overwrite input (writes .bak backup first)")
	fs.BoolVar(&keepNonAI, "keep-non-ai-metadata", false, "Only drop segments/chunks that look like C2PA/AI (less aggressive)")
	fs.BoolVar(&jsonOut, "json", false, "JSON result on stdout")
	fs.StringVar(&synthidDir, "synthid-dir", "", "reverse-SynthID checkout root for optional pixel SynthID scoring")
	fs.StringVar(&removePixel, "remove-pixel", "", "Run optional pixel-watermark removal after metadata cleaning (ctrlregen = CtrlRegen regeneration; diffusion = MarkDiffusion DiffusionPurification regeneration)")
	fs.StringVar(&ctrlregenDir, "ctrlregen-dir", "", "noai-watermark checkout root (default: $NOAI_WATERMARK_DIR)")
	fs.Float64Var(&ctrlregenStrength, "ctrlregen-strength", 0.25, "CtrlRegen strength in (0, 1] (default: 0.25, conservative)")
	fs.IntVar(&ctrlregenSteps, "ctrlregen-steps", 50, "CtrlRegen diffusion steps (default: 50)")
	fs.StringVar(&ctrlregenDevice, "ctrlregen-device", "", "CtrlRegen device: auto|cpu|cuda|mps (default: auto)")
	fs.Var(&intOpt{p: &ctrlregenSeed, set: &ctrlregenSeedSet}, "ctrlregen-seed", "Optional CtrlRegen RNG seed")
	fs.IntVar(&ctrlregenTimeout, "ctrlregen-timeout", 3600, "CtrlRegen subprocess timeout in seconds (default: 3600)")
	fs.StringVar(&markdiffusionDir, "markdiffusion-dir", "", "MarkDiffusion bootstrap dir (default: $MARKDIFFUSION_DIR)")
	fs.Float64Var(&markdiffusionStrength, "markdiffusion-strength", 0.3, "DiffusionPurification strength in (0, 1] (default: 0.3)")
	fs.StringVar(&markdiffusionModel, "markdiffusion-model", "", "Stable Diffusion model for purification (default: SD 2.1 base)")
	fs.IntVar(&markdiffusionSize, "markdiffusion-size", 512, "Purification working size in px (default: 512)")
	fs.IntVar(&markdiffusionSteps, "markdiffusion-steps", 50, "Purification diffusion steps (default: 50)")
	fs.StringVar(&markdiffusionDevice, "markdiffusion-device", "", "Purification device: auto|cpu|cuda|mps (default: auto)")
	fs.IntVar(&markdiffusionTimeout, "markdiffusion-timeout", 3600, "Purification subprocess timeout in seconds (default: 3600)")
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
	if removePixel != "" && removePixel != "ctrlregen" && removePixel != "diffusion" {
		cleaning.Eprint(i18n.T("cli.invalid_choice", removePixel, "'ctrlregen', 'diffusion'"))
		return 2
	}

	// Python: src = args.path; in-place cleans FROM the .bak backup so a
	// second run re-cleans the original bytes, not the already-cleaned file.
	src := path
	var dest string
	if inPlace {
		if _, err := cleaning.BackupPath(path); err != nil {
			cliutil.FatalErr(err)
		}
		src = path + ".bak"
		dest = path
	} else {
		if output != "" {
			dest = output
		} else {
			dest = cleaning.CleanedPath(path, ".cleaned")
		}
	}

	var seed *int
	if ctrlregenSeedSet {
		s := ctrlregenSeed
		seed = &s
	}

	result, err := cleaning.CleanImage(src, dest, cleaning.CleanImageOptions{
		StripAllMetadata:      cleaning.BoolPtr(!keepNonAI),
		SynthidDir:            synthidDir,
		RemovePixel:           removePixel,
		CtrlregenDir:          ctrlregenDir,
		CtrlregenStrength:     cleaning.F64Ptr(ctrlregenStrength),
		CtrlregenSteps:        cleaning.IntPtr(ctrlregenSteps),
		CtrlregenDevice:       ctrlregenDevice,
		CtrlregenSeed:         seed,
		CtrlregenTimeout:      cleaning.IntPtr(ctrlregenTimeout),
		MarkdiffusionDir:      markdiffusionDir,
		MarkdiffusionStrength: cleaning.F64Ptr(markdiffusionStrength),
		MarkdiffusionModel:    markdiffusionModel,
		MarkdiffusionSize:     cleaning.IntPtr(markdiffusionSize),
		MarkdiffusionSteps:    cleaning.IntPtr(markdiffusionSteps),
		MarkdiffusionDevice:   markdiffusionDevice,
		MarkdiffusionTimeout:  cleaning.IntPtr(markdiffusionTimeout),
	})
	if err != nil {
		cleaning.Eprint("error: " + err.Error())
		return 1
	}

	pr, _ := result["pixel_removal"].(map[string]interface{})
	residual := reportBool(result, "still_has_c2pa") || reportBool(result, "still_has_ai_metadata")
	failed := residual || (pr != nil && !dictBool(pr, "available"))

	if jsonOut {
		if err := cleaning.EmitJSON(result); err != nil {
			cliutil.FatalErr(err)
		}
	} else {
		cleaning.Eprint(i18n.T("cli.wrote_image", result["output"], result["bytes_in"], result["bytes_out"]))
		for _, a := range strList(result["actions"]) {
			cleaning.Eprint("  - " + a)
		}
		if sb, ok := result["synthid_before"].(map[string]interface{}); ok && dictBool(sb, "available") {
			cleaning.Eprint(i18n.T("cli.synthid_before", dictFloat(sb, "confidence"), yesNo(dictBool(sb, "is_watermarked"))))
		}
		if sa, ok := result["synthid_after"].(map[string]interface{}); ok && dictBool(sa, "available") {
			cleaning.Eprint(i18n.T("cli.synthid_after", dictFloat(sa, "confidence"), yesNo(dictBool(sa, "is_watermarked"))))
		}
		if pr != nil {
			engine := "DiffusionPurification"
			if removePixel == "ctrlregen" {
				engine = "CtrlRegen"
			}
			if dictBool(pr, "available") {
				device, _ := pr["device"].(string)
				if device == "" {
					device = i18n.T("cli.unknown_device")
				}
				cleaning.Eprint(i18n.T("cli.pixel_removed", engine, device))
			} else {
				errMsg := i18n.T("cli.unknown_error")
				if e, ok := pr["error"].(string); ok {
					errMsg = e
				}
				cleaning.Eprint(i18n.T("cli.pixel_unavail", engine, errMsg))
			}
		}
		if residual {
			cleaning.Eprint(i18n.T("cli.residual_warning"))
			for _, f := range strList(result["post_findings"]) {
				cleaning.Eprint("  ! " + f)
			}
		}
	}
	if failed {
		return 1
	}
	return 0
}

// InspectImage implements the inspect-image CLI.
func InspectImage(args []string) int {
	fs := newFlagSet("inspect-image")
	var jsonOut bool
	var synthidDir string
	fs.BoolVar(&jsonOut, "json", false, "JSON report")
	fs.StringVar(&synthidDir, "synthid-dir", "", "reverse-SynthID checkout root for optional pixel SynthID scoring")
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

	report, err := cleaning.InspectImage(path, synthidDir)
	if err != nil {
		cleaning.Eprint("error: " + err.Error())
		return 1
	}
	if jsonOut {
		if err := cleaning.EmitJSON(report.ToDict()); err != nil {
			cleaning.Eprint("error: " + err.Error())
			return 1
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
		return 1
	}
	return 0
}
