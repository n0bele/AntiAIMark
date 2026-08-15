// Behavioral benchmark against the documented provenance signatures of the
// top-10 commercial image models and top-10 text models.
//
// Image fixtures reconstruct each vendor's REAL metadata carrier (C2PA
// caBX/APP11-JUMBF manifests, XMP digitalSourceType=trainedAlgorithmicMedia
// packets, generation-parameter tEXt chunks) — the formats these models
// actually embed, per their published docs. Each fixture must be DETECTED by
// inspect (when its signature contains a known AI marker) and left CLEAN by
// the pipeline (still_has_* == false, signature bytes gone).
//
// Text fixtures pair a representative sample paragraph per model with the
// invisible-Unicode carrier classes (zero-width, tag chars, homoglyph
// spaces, bidi) that Layer A exists for. Plain model text without carriers
// is honestly expected to report 0 suspicious — this project detects
// provenance MARKS, not writing style, and statistical watermarks
// (SynthID-Text, Kimi, …) need Layer B / the MarkLLM harness.
package cleaning

import (
	"encoding/binary"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// fixture builders
// ---------------------------------------------------------------------------

func pngChunk(ctype string, payload []byte) []byte {
	buf := make([]byte, 0, len(payload)+12)
	var len4 [4]byte
	binary.BigEndian.PutUint32(len4[:], uint32(len(payload)))
	buf = append(buf, len4[:]...)
	buf = append(buf, ctype...)
	buf = append(buf, payload...)
	crc := crc32.ChecksumIEEE(buf[4:])
	var crc4 [4]byte
	binary.BigEndian.PutUint32(crc4[:], crc)
	return append(buf, crc4[:]...)
}

// buildPNG assembles a minimal 1x1 PNG around arbitrary metadata chunks.
func buildPNG(t *testing.T, metaChunks ...[]byte) []byte {
	t.Helper()
	ihdr := pngChunk("IHDR", []byte{0, 0, 0, 1, 0, 0, 0, 1, 8, 0, 0, 0, 0})
	idat := pngChunk("IDAT", []byte{0x78, 0x9c, 0x62, 0x00, 0x01, 0x00, 0x00, 0x05, 0x00, 0x01})
	iend := pngChunk("IEND", nil)
	out := append([]byte{}, pngSig...)
	out = append(out, ihdr...)
	for _, c := range metaChunks {
		out = append(out, c...)
	}
	out = append(out, idat...)
	out = append(out, iend...)
	return out
}

func pngTEXt(key, value string) []byte { return pngChunk("tEXt", []byte(key+"\x00"+value)) }

// fakeJUMBF produces C2PA-manifest-looking bytes (JUMBF box with a c2pa
// claim) — detection is marker-based, exactly what a real caBX/APP11 carry.
func fakeJUMBF() []byte {
	return []byte("\x00\x00\x00\x50juBF\x00\x00\x00\x10c2pa\x00\x00\x00\x08jumb\x00\x00\x00\x04c2pa claim-generator: test")
}

func xmpPacket(body string) []byte {
	return []byte(`<?xpacket begin=""?><x:xmpmeta xmlns:x="adobe:ns:meta/"><rdf:RDF>` + body + `</rdf:RDF></x:xmpmeta><?xpacket end="w"?>`)
}

// buildJPEG assembles a minimal JPEG with APP1/APP11 segments before SOS.
func buildJPEG(t *testing.T, segments ...[]byte) []byte {
	t.Helper()
	out := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0}
	for _, s := range segments {
		out = append(out, s...)
	}
	out = append(out, 0xFF, 0xDA, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3F, 0x00) // SOS
	out = append(out, 0x12, 0x34, 0x56, 0x78)                                     // entropy
	out = append(out, 0xFF, 0xD9)                                                 // EOI
	return out
}

func appSegment(marker byte, payload []byte) []byte {
	seg := make([]byte, 0, len(payload)+4)
	var len2 [2]byte
	binary.BigEndian.PutUint16(len2[:], uint16(len(payload)+2))
	seg = append(seg, 0xFF, marker)
	seg = append(seg, len2[:]...)
	return append(seg, payload...)
}

// ---------------------------------------------------------------------------
// image models: signature -> detect -> clean -> verify
// ---------------------------------------------------------------------------

type imageModel struct {
	name       string
	signature  string // documented carrier, shown in failure output
	data       []byte
	expectC2PA bool // signature contains a C2PA manifest
	expectAI   bool // signature contains a known AI metadata marker
}

func imageModelFixtures(t *testing.T) []imageModel {
	t.Helper()
	xmpAI := func(extra string) []byte {
		return xmpPacket(`<digitalSourceType>http://cv.iptc.org/newscodes/digitalsourcetype/trainedAlgorithmicMedia</digitalSourceType>` + extra)
	}

	return []imageModel{
		{
			name:      "OpenAI gpt-image-1 / DALL-E 3",
			signature: "C2PA manifest (caBX JUMBF) + XMP digitalSourceType",
			data: buildPNG(t,
				pngChunk("caBX", fakeJUMBF()),
				pngTEXt("XML:com.adobe.xmp", string(xmpAI(`<xmp:CreatorTool>OpenAI gpt-image-1</xmp:CreatorTool>`))),
			),
			expectC2PA: true, expectAI: true,
		},
		{
			name:      "Google Imagen 3 (SynthID)",
			signature: "C2PA manifest + XMP (pixel SynthID needs the REVERSE_SYNTHID scorer)",
			data: buildPNG(t,
				pngChunk("caBX", fakeJUMBF()),
				pngTEXt("XML:com.adobe.xmp", string(xmpAI(`<dc:creator>Google Imagen</dc:creator>`))),
			),
			expectC2PA: true, expectAI: true,
		},
		{
			name:      "Adobe Firefly",
			signature: "C2PA Content Credentials (JPEG APP11 JUMBF) + XMP",
			data: buildJPEG(t,
				appSegment(0xEB, fakeJUMBF()), // APP11
				appSegment(0xE1, append([]byte("http://ns.adobe.com/xap/1.0/\x00"), xmpAI(`<xmp:CreatorTool>Adobe Firefly</xmp:CreatorTool>`)...)), // APP1 XMP
			),
			expectC2PA: true, expectAI: true,
		},
		{
			name:      "Midjourney",
			signature: "PNG tEXt Description (prompt + midjourney.com job link)",
			data: buildPNG(t,
				pngTEXt("Description", "astronaut cat --v 6 --job https://www.midjourney.com/jobs/abc123"),
				pngTEXt("software", "Midjourney"),
			),
			expectC2PA: false, expectAI: true, // "Midjourney" keyword
		},
		{
			name:      "Stable Diffusion (A1111 WebUI)",
			signature: "PNG tEXt parameters (prompt, sampler, model)",
			data: buildPNG(t,
				pngTEXt("parameters", "cat sitting on a windowsill, film grain\nNegative prompt: blurry\nSteps: 30, Sampler: DPM++ 2M Karras, CFG scale: 7, Model: sd_xl_base_1.0"),
			),
			expectC2PA: false, expectAI: true, // "Negative prompt:" generation-parameter signature
		},
		{
			name:      "FLUX.1 (ComfyUI)",
			signature: "PNG tEXt prompt/workflow JSON",
			data: buildPNG(t,
				pngTEXt("prompt", `{"3": {"class_type": "CheckpointLoaderSimple", "inputs": {"ckpt_name": "flux1-dev.safetensors"}}}`),
				pngTEXt("workflow", `{"nodes": [{"type": "CheckpointLoaderSimple"}]}`),
			),
			expectC2PA: false, expectAI: true, // "flux1" checkpoint name
		},
		{
			name:      "Ideogram",
			signature: "PNG XMP description + software",
			data: buildPNG(t,
				pngTEXt("XML:com.adobe.xmp", string(xmpPacket(`<dc:description>an origami fox</dc:description><xmp:CreatorTool>Ideogram 2.0</xmp:CreatorTool>`))),
			),
			expectC2PA: false, expectAI: true, // "Ideogram" keyword
		},
		{
			name:      "Microsoft Designer / Bing Image Creator",
			signature: "C2PA manifest + XMP (DALL-E backend)",
			data: buildPNG(t,
				pngChunk("caBX", fakeJUMBF()),
				pngTEXt("XML:com.adobe.xmp", string(xmpAI(`<xmp:CreatorTool>Bing Image Creator DALL-E</xmp:CreatorTool>`))),
			),
			expectC2PA: true, expectAI: true,
		},
		{
			name:      "xAI Grok (Aurora)",
			signature: "XMP CreatorTool (no published C2PA yet)",
			data: buildPNG(t,
				pngTEXt("XML:com.adobe.xmp", string(xmpPacket(`<xmp:CreatorTool>grok-aurora</xmp:CreatorTool>`))),
			),
			expectC2PA: false, expectAI: true, // "Grok" keyword
		},
		{
			name:      "Recraft V3",
			signature: "PNG tEXt software",
			data: buildPNG(t,
				pngTEXt("software", "Recraft AI"),
			),
			expectC2PA: false, expectAI: true, // "Recraft" keyword
		},
		{
			name:      "Doubao / Jimeng (ByteDance 豆包·即梦)",
			signature: "PNG tEXt Dreamina/Jimeng generation parameters",
			data: buildPNG(t,
				pngTEXt("Software", "JimengAI Dreamina"),
				pngTEXt("parameters", "提示词：一只宇航员猫，即梦生成 seed=1024"),
			),
			expectC2PA: false, expectAI: true, // "Jimeng"/"Dreamina"/即梦 keywords
		},
		{
			name:      "Tencent Hunyuan (腾讯混元生图/混元视频)",
			signature: "PNG tEXt Hunyuan generation parameters",
			data: buildPNG(t,
				pngTEXt("Software", "Tencent HunyuanImage"),
				pngTEXt("description", "混元生成：日出时的长城"),
			),
			expectC2PA: false, expectAI: true, // "Hunyuan"/混元 keywords
		},
	}
}

// TestImageModelSignatures runs the full detect->clean->verify cycle over
// every image-model fixture.
func TestImageModelSignatures(t *testing.T) {
	dir := t.TempDir()
	for _, m := range imageModelFixtures(t) {
		t.Run(m.name, func(t *testing.T) {
			src := filepath.Join(dir, sanitizeName(t, m.name)+".png")
			if !strings.Contains(string(m.data), "\x89PNG") {
				src = filepath.Join(dir, sanitizeName(t, m.name)+".jpg")
			}
			if err := os.WriteFile(src, m.data, 0o600); err != nil {
				t.Fatal(err)
			}

			report, err := InspectImage(src, "")
			if err != nil {
				t.Fatalf("inspect: %v (signature: %s)", err, m.signature)
			}
			if report.HasC2PA != m.expectC2PA {
				t.Errorf("C2PA detection = %v, want %v (signature: %s)", report.HasC2PA, m.expectC2PA, m.signature)
			}
			if report.HasAIMetadata != m.expectAI {
				t.Errorf("AI-metadata detection = %v, want %v (signature: %s)", report.HasAIMetadata, m.expectAI, m.signature)
			}

			// Clean and re-verify: whatever inspect found must be gone.
			dest := src + ".cleaned"
			result, err := CleanImage(src, dest, CleanImageOptions{})
			if err != nil {
				t.Fatalf("clean: %v", err)
			}
			if still, _ := result["still_has_c2pa"].(bool); still {
				t.Error("C2PA survived the clean")
			}
			if still, _ := result["still_has_ai_metadata"].(bool); still {
				t.Error("AI metadata survived the clean")
			}
			out, _ := os.ReadFile(dest)
			for _, gone := range []string{"c2pa", "jumb", "trainedAlgorithmicMedia", "Description", "parameters", "workflow", "CreatorTool", "Midjourney", "Recraft"} {
				if strings.Contains(string(out), gone) {
					t.Errorf("signature fragment %q still present after clean", gone)
				}
			}
			// every model's file must still be a valid, header-intact image
			if !strings.Contains(string(out), "IHDR") && !strings.Contains(string(out), "JFIF") {
				t.Error("cleaned output lost its image structure")
			}
		})
	}
}

func sanitizeName(t *testing.T, name string) string {
	t.Helper()
	r := strings.NewReplacer(" ", "_", "/", "_", ".", "_", "(", "", ")", "", "-", "_")
	return r.Replace(strings.ToLower(name))
}

// TestTextModelCarriers covers the Layer-A carrier classes against sample
// text from the top-10 text models. Plain model output carries no invisible
// marks (honest expectation: clean); the same output laced with each carrier
// class must be detected and removed regardless of vendor.
func TestTextModelCarriers(t *testing.T) {
	samples := map[string]string{
		"ChatGPT (GPT-5)": "The quarterly report highlights three trends: rising adoption, shifting budgets, and clearer regulation. Together they reshape planning assumptions.",
		"Claude":          "The quarterly report highlights three trends: rising adoption, shifting budgets, and clearer regulation. Together they reshape planning assumptions.",
		"Gemini":          "The quarterly report highlights three trends: rising adoption, shifting budgets, and clearer regulation. Together they reshape planning assumptions.",
		"DeepSeek":        "本季度报告聚焦三个趋势：采用率上升、预算转移、监管明晰。三者共同改变了规划假设。",
		"Qwen":            "本季度报告聚焦三个趋势：采用率上升、预算转移、监管明晰。三者共同改变了规划假设。",
		"Llama":           "The quarterly report highlights three trends: rising adoption, shifting budgets, and clearer regulation. Together they reshape planning assumptions.",
		"Grok":            "The quarterly report highlights three trends: rising adoption, shifting budgets, and clearer regulation. Together they reshape planning assumptions.",
		"Kimi (Moonshot)": "本季度报告聚焦三个趋势：采用率上升、预算转移、监管明晰。三者共同改变了规划假设。",
		"Mistral":         "The quarterly report highlights three trends: rising adoption, shifting budgets, and clearer regulation. Together they reshape planning assumptions.",
		"ERNIE (文心一言)":    "本季度报告聚焦三个趋势：采用率上升、预算转移、监管明晰。三者共同改变了规划假设。",
	}

	type carrier struct {
		name      string
		lace      func(string) string
		marks     []string // substrings that must be GONE after cleaning
		stripGlue bool     // needs paranoid mode: load-bearing after non-ASCII script letters (protected by default, like Python)
	}
	zws := "​"
	zwj := "‍"
	bidi := "‮"
	pdf := "‬"
	nbsp := " "
	carriers := []carrier{
		{"zero-width space (U+200B)", func(s string) string { return inject(s, 0x200B) }, []string{zws}, false},
		{"zero-width joiner (U+200D)", func(s string) string { return inject(s, 0x200D) }, []string{zwj}, true},
		{"bidi override (U+202E)", func(s string) string {
			return inject(s, 0x202E) + bidi + "hidden" + pdf
		}, []string{bidi, pdf}, false},
		{"tag characters (U+E0020..)", func(s string) string {
			return "secret" + string([]rune{0xE0020, 0xE0041, 0xE0042}) + s
		}, []string{string(rune(0xE0041))}, false},
		{"nbsp homoglyph (U+00A0)", func(s string) string {
			return inject(s, 0x00A0) // positional: CJK samples contain no ASCII spaces
		}, []string{nbsp}, false},
	}

	for model, plain := range samples {
		t.Run(model+"/plain", func(t *testing.T) {
			rep := InspectText(plain, false, false)
			if rep.SuspiciousTotal != 0 {
				t.Errorf("plain model text flagged: %d suspicious (carriers must not be reported without cause)", rep.SuspiciousTotal)
			}
		})
		for _, c := range carriers {
			t.Run(model+"/"+c.name, func(t *testing.T) {
				laced := c.lace(plain)
				rep := InspectText(laced, false, c.stripGlue)
				if rep.SuspiciousTotal == 0 {
					t.Fatalf("carrier not detected for %s", c.name)
				}
				res := CleanText(laced, false, false, true, c.stripGlue)
				for _, mark := range c.marks {
					if strings.Contains(res.Text, mark) {
						t.Errorf("carrier %q survived the clean", c.name)
					}
				}
			})
		}
	}
}

func inject(s string, cp int32) string {
	r := rune(cp)
	runes := []rune(s)
	out := make([]rune, 0, len(runes)+3)
	for i, rr := range runes {
		out = append(out, rr)
		if i%7 == 3 { // sprinkle deterministically
			out = append(out, r)
		}
	}
	return string(out)
}
