// clean-image: Go port of service/scripts/clean_image.py.
// Strip C2PA and AI-related metadata from PNG/JPEG/WebP.
package main

import (
	"flag"
	"fmt"
	"os"

	"watermarks-remover/internal/cleaning"
	"watermarks-remover/internal/cliutil"
	"watermarks-remover/internal/i18n"
)

func main() {
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

	flag.StringVar(&output, "o", "", "Output path (default: *.cleaned.*)")
	flag.StringVar(&output, "output", "", "Output path (default: *.cleaned.*)")
	flag.BoolVar(&inPlace, "in-place", false, "Overwrite input (writes .bak backup first)")
	flag.BoolVar(&keepNonAI, "keep-non-ai-metadata", false, "Only drop segments/chunks that look like C2PA/AI (less aggressive)")
	flag.BoolVar(&jsonOut, "json", false, "JSON result on stdout")
	flag.StringVar(&synthidDir, "synthid-dir", "", "reverse-SynthID checkout root for optional pixel SynthID scoring")
	flag.StringVar(&removePixel, "remove-pixel", "", "Run optional pixel-watermark removal after metadata cleaning (ctrlregen = CtrlRegen regeneration; diffusion = MarkDiffusion DiffusionPurification regeneration)")
	flag.StringVar(&ctrlregenDir, "ctrlregen-dir", "", "noai-watermark checkout root (default: $NOAI_WATERMARK_DIR)")
	flag.Float64Var(&ctrlregenStrength, "ctrlregen-strength", 0.25, "CtrlRegen strength in (0, 1] (default: 0.25, conservative)")
	flag.IntVar(&ctrlregenSteps, "ctrlregen-steps", 50, "CtrlRegen diffusion steps (default: 50)")
	flag.StringVar(&ctrlregenDevice, "ctrlregen-device", "", "CtrlRegen device: auto|cpu|cuda|mps (default: auto)")
	flag.Var(&intOpt{p: &ctrlregenSeed, set: &ctrlregenSeedSet}, "ctrlregen-seed", "Optional CtrlRegen RNG seed")
	flag.IntVar(&ctrlregenTimeout, "ctrlregen-timeout", 3600, "CtrlRegen subprocess timeout in seconds (default: 3600)")
	flag.StringVar(&markdiffusionDir, "markdiffusion-dir", "", "MarkDiffusion bootstrap dir (default: $MARKDIFFUSION_DIR)")
	flag.Float64Var(&markdiffusionStrength, "markdiffusion-strength", 0.3, "DiffusionPurification strength in (0, 1] (default: 0.3)")
	flag.StringVar(&markdiffusionModel, "markdiffusion-model", "", "Stable Diffusion model for purification (default: SD 2.1 base)")
	flag.IntVar(&markdiffusionSize, "markdiffusion-size", 512, "Purification working size in px (default: 512)")
	flag.IntVar(&markdiffusionSteps, "markdiffusion-steps", 50, "Purification diffusion steps (default: 50)")
	flag.StringVar(&markdiffusionDevice, "markdiffusion-device", "", "Purification device: auto|cpu|cuda|mps (default: auto)")
	flag.IntVar(&markdiffusionTimeout, "markdiffusion-timeout", 3600, "Purification subprocess timeout in seconds (default: 3600)")
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
	if removePixel != "" && removePixel != "ctrlregen" && removePixel != "diffusion" {
		cleaning.Eprint(i18n.T("cli.invalid_choice", removePixel, "'ctrlregen', 'diffusion'"))
		os.Exit(2)
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
		os.Exit(1)
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
		os.Exit(1)
	}
	os.Exit(0)
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

func reportBool(m map[string]interface{}, key string) bool {
	b, _ := m[key].(bool)
	return b
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

func yesNo(b bool) string {
	if b {
		return i18n.T("cli.yes")
	}
	return i18n.T("cli.no")
}
