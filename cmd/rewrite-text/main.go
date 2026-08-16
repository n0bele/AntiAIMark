// rewrite-text: Go port of service/scripts/rewrite_text.py.
//
// Layer B optional rewrite hook for statistical (token-sampling) watermarks.
//
// Backends:
//
//	print-prompt       — emit prompt only (default; CI-safe, no model)
//	ollama             — POST to Ollama /api/chat
//	openai-compatible  — POST to OpenAI-style /v1/chat/completions
//
// Security notes mirror the Python original: only http(s) endpoints are
// accepted; redirects are refused outright so an Authorization header (API
// key) can never be re-sent to an unvalidated host; non-loopback endpoints
// are denied unless ANTIAIMARK_REWRITE_ALLOW_REMOTE=1 (or --allow-remote).
// The API key is read from ANTIAIMARK_REWRITE_API_KEY only — never argv.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"antiaimark/internal/cleaning"
	"antiaimark/internal/cliutil"
)

const defaultMarkllmModel = "facebook/opt-1.3b"

var prompts = map[string]string{
	"paraphrase": "Rewrite the following text so that it uses substantially different wording at " +
		"the token level. Change clause order, connectors, and transition words; vary " +
		"sentence boundaries and length; and replace both content words and function " +
		"words where meaning allows. Preserve all facts, numbers, names, and technical " +
		"identifiers. Do not add or remove claims. Output only the rewritten text.\n\n---\n{TEXT}",
	"humanize": "Rewrite the following text so it reads as if a human wrote it from scratch. " +
		"Vary sentence rhythm and length, replace formulaic AI-style transitions and " +
		"filler with concrete natural phrasing, and use plain, varied wording. Preserve " +
		"all facts, numbers, names, and technical identifiers. Do not add or remove " +
		"claims. Output only the rewritten text.\n\n---\n{TEXT}",
	"code": "Rewrite the natural-language parts of this code — comments, docstrings, and " +
		"string literals — using different wording. Rename local variables, function " +
		"parameters, and private helper names to semantically equivalent names. Preserve " +
		"program behavior, public API names, and all values that affect output. Output " +
		"only the rewritten code.\n\n---\n{TEXT}",
	"backtranslate_out": "Translate the following text to {LANG}. Output only the translation.\n\n---\n{TEXT}",
	"backtranslate_back": "Translate the following text to {ORIGINAL_LANG}. Preserve meaning; use natural " +
		"phrasing. Output only the translation.\n\n---\n{TEXT}",
	"structural_outline": "Extract a bullet outline of all claims and structure from the text " +
		"(no full sentences). Output only the outline.\n\n---\n{TEXT}",
	"structural_write": "Write a complete document from this outline in natural, varied human prose. " +
		"Avoid formulaic transitions. Do not omit any bullet. Output only the document." +
		"\n\n---\n{TEXT}",
}

var tokenRe = regexp.MustCompile(`[A-Za-z0-9]+`)

func tokens(text string) []string {
	return tokenRe.FindAllString(strings.ToLower(text), -1)
}

func bigrams(tokens []string) map[string]bool {
	set := map[string]bool{}
	for i := 0; i+1 < len(tokens); i++ {
		set[tokens[i]+"\x00"+tokens[i+1]] = true
	}
	return set
}

// lexicalDivergence is the bigram Jaccard distance: 0.0 identical, 1.0 fully
// different (mirrors _lexical_divergence).
func lexicalDivergence(original, candidate string) float64 {
	a := tokens(original)
	b := tokens(candidate)
	if len(a) == 0 && len(b) == 0 {
		return 0.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 1.0
	}
	ba := bigrams(a)
	bb := bigrams(b)
	inter := 0
	for k := range ba {
		if bb[k] {
			inter++
		}
	}
	union := len(ba) + len(bb) - inter
	if union == 0 {
		return 0.0
	}
	return 1.0 - float64(inter)/float64(union)
}

// selectCandidate picks the most lexically diverged rewrite, gently guarding
// extreme length drift (mirrors _select_candidate).
func selectCandidate(original string, candidates []string) (string, []float64) {
	scores := make([]float64, len(candidates))
	for i, cand := range candidates {
		score := lexicalDivergence(original, cand)
		if original != "" {
			ratio := float64(len(cand)) / float64(len(original))
			if ratio > 2.0 || ratio < 0.5 {
				score -= 0.15
			}
		}
		scores[i] = score
	}
	best := 0
	for i, s := range scores {
		if s > scores[best] {
			best = i
		}
	}
	return candidates[best], scores
}

func envOrEmpty(name string) string {
	return os.Getenv(name)
}

func flagEnv(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

var loopbackHosts = map[string]bool{"localhost": true, "127.0.0.1": true, "::1": true}

// checkRemote enforces the rewrite-endpoint allowlist: default-deny, only
// loopback hosts, non-http(s) schemes always refused (mirrors _check_remote).
func checkRemote(baseURL string, allowRemote bool) error {
	u, err := url.Parse(baseURL)
	if err != nil || u.Scheme == "" {
		return fmt.Errorf("error: rewrite base URL must be http(s), got scheme '': %s", baseURL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("error: rewrite base URL must be http(s), got scheme '%s': %s", u.Scheme, baseURL)
	}
	host := u.Hostname()
	if loopbackHosts[host] {
		return nil
	}
	if !allowRemote {
		return fmt.Errorf("error: rewrite base URL host is not loopback ('%s'); refusing to send content off-machine. Set ANTIAIMARK_REWRITE_ALLOW_REMOTE=1 or pass --allow-remote to override.", host)
	}
	cleaning.Eprint(fmt.Sprintf("warning: rewrite base URL host is '%s' (not localhost); content will leave this machine", host))
	return nil
}

// noRedirectClient refuses HTTP redirects so the Authorization header can
// never be re-sent to an unvalidated host (mirrors _NoRedirect).
var noRedirectClient = &http.Client{
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return fmt.Errorf("refused redirect to %s", req.URL.String())
	},
}

func httpJSON(urlStr string, payload interface{}, headers map[string]string, timeout float64) (map[string]interface{}, error) {
	u, err := url.Parse(urlStr)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, fmt.Errorf("refusing non-http(s) rewrite endpoint: %s", urlStr)
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", urlStr, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncateStr(string(data), 500))
	}
	var out map[string]interface{}
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func callOllama(baseURL, model, prompt string, timeout, temperature float64) (string, error) {
	data, err := httpJSON(strings.TrimRight(baseURL, "/")+"/api/chat", map[string]interface{}{
		"model":    model,
		"stream":   false,
		"messages": []map[string]interface{}{{"role": "user", "content": prompt}},
		"options":  map[string]interface{}{"temperature": temperature},
	}, nil, timeout)
	if err != nil {
		return "", err
	}
	msg, _ := data["message"].(map[string]interface{})
	content, _ := msg["content"].(string)
	if content == "" {
		rep, _ := json.Marshal(data)
		return "", fmt.Errorf("ollama empty response: %s", truncateStr(string(rep), 500))
	}
	return strings.TrimSpace(content), nil
}

func callOpenAICompatible(baseURL, model, prompt, apiKey string, timeout, temperature float64, reasoningEffort string) (string, error) {
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	payload := map[string]interface{}{
		"model":       model,
		"messages":    []map[string]interface{}{{"role": "user", "content": prompt}},
		"temperature": temperature,
	}
	if reasoningEffort != "" {
		payload["reasoning_effort"] = reasoningEffort
	}
	data, err := httpJSON(strings.TrimRight(baseURL, "/")+"/v1/chat/completions", payload, headers, timeout)
	if err != nil {
		return "", err
	}
	choices, _ := data["choices"].([]interface{})
	if len(choices) == 0 {
		rep, _ := json.Marshal(data)
		return "", fmt.Errorf("openai-compatible empty choices: %s", truncateStr(string(rep), 500))
	}
	choice, _ := choices[0].(map[string]interface{})
	msg, _ := choice["message"].(map[string]interface{})
	content, _ := msg["content"].(string)
	if content == "" {
		rep, _ := json.Marshal(data)
		return "", fmt.Errorf("openai-compatible empty content: %s", truncateStr(string(rep), 500))
	}
	return strings.TrimSpace(content), nil
}

// markllmDetect runs the MarkLLM adapter on text; never fails the rewrite
// (mirrors _markllm_detect).
func markllmDetect(text, scheme, upstreamDir, model string, timeout float64) map[string]interface{} {
	if upstreamDir == "" {
		return map[string]interface{}{"available": false, "error": "no MARKLLM_DIR set"}
	}
	upstream := resolvePath(expandUserHome(upstreamDir))
	if fi, err := os.Stat(upstream); err != nil || !fi.IsDir() {
		return map[string]interface{}{"available": false, "error": "MarkLLM checkout missing: " + upstream}
	}
	if fi, err := os.Stat(filepath.Join(upstream, "watermark")); err != nil || !fi.IsDir() {
		return map[string]interface{}{"available": false, "error": "MarkLLM checkout missing: " + upstream}
	}
	venvPython := venvInterpreter(upstream)
	if venvPython == "" {
		return map[string]interface{}{"available": false, "error": "MarkLLM venv missing: " + upstream}
	}
	script := filepath.Join(scriptsDir(), "detect_text_watermark.py")
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout*float64(time.Second)))
	defer cancel()
	cmd := exec.CommandContext(ctx, venvPython, script, "detect", "-", "--scheme", scheme,
		"--upstream-dir", upstream, "--model", model, "--json")
	cmd.Stdin = strings.NewReader(text)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return map[string]interface{}{"available": false, "error": msg}
	}
	var payload map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		return map[string]interface{}{"available": false, "error": "adapter JSON parse error: " + err.Error()}
	}
	return payload
}

func venvInterpreter(upstream string) string {
	rel := filepath.Join(".venv", "bin", "python")
	if runtime.GOOS == "windows" {
		rel = filepath.Join(".venv", "Scripts", "python.exe")
	}
	venv := filepath.Join(upstream, rel)
	if fi, err := os.Stat(venv); err == nil && !fi.IsDir() {
		return venv
	}
	return ""
}

func expandUserHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") || strings.HasPrefix(p, `~\`) {
		if h, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return h
			}
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
}

// scriptsDir locates the repo's service/scripts directory (the Python harness
// adapters live there).
func scriptsDir() string {
	for _, start := range []string{os.Getenv("ANTIAIMARK_SCRIPTS_DIR"), "."} {
		if start == "" {
			continue
		}
		dir := start
		for {
			cand := filepath.Join(dir, "service", "scripts")
			if fi, err := os.Stat(cand); err == nil && fi.IsDir() {
				return cand
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return filepath.Join("service", "scripts")
}

func buildPrompt(strength, text, lang, originalLang string) string {
	switch strength {
	case "paraphrase", "humanize", "code":
		return strings.ReplaceAll(prompts[strength], "{TEXT}", text)
	case "backtranslate":
		return fmt.Sprintf("Translate the text to %s, then translate that result back to %s. "+
			"Preserve all facts, numbers, and names. Output only the final %s text.\n\n---\n%s",
			lang, originalLang, originalLang, text)
	case "structural":
		return "First extract a bullet outline of all claims (no full sentences). " +
			"Then write a complete document from that outline in natural, varied human " +
			"prose without omitting any bullet. Output only the final document.\n\n---\n" + text
	}
	return ""
}

type rewriteOptions struct {
	backend         string
	model           string
	baseURL         string
	apiKey          string
	strength        string
	lang            string
	originalLang    string
	timeout         float64
	layerAAfter     bool
	temperature     float64
	candidates      int
	allowRemote     bool
	reasoningEffort string
	markllmScheme   string
	markllmDir      string
	markllmModel    string
	markllmTimeout  float64
}

func rewriteText(text string, opts rewriteOptions) (string, map[string]interface{}, error) {
	prompt := buildPrompt(opts.strength, text, opts.lang, opts.originalLang)
	info := map[string]interface{}{
		"backend":      opts.backend,
		"strength":     opts.strength,
		"model":        nilIfEmpty(opts.model),
		"base_url":     nilIfEmpty(opts.baseURL),
		"temperature":  opts.temperature,
		"prompt_chars": len([]rune(prompt)),
		"input_chars":  len([]rune(text)),
	}
	if opts.reasoningEffort != "" {
		info["reasoning_effort"] = opts.reasoningEffort
	}

	var markllm map[string]interface{}
	if opts.markllmScheme != "" {
		before := markllmDetect(text, opts.markllmScheme, opts.markllmDir, opts.markllmModel, opts.markllmTimeout)
		markllm = map[string]interface{}{"scheme": opts.markllmScheme, "before": before}
		if avail, _ := before["available"].(bool); !avail {
			cleaning.Eprint(fmt.Sprintf("markllm verification unavailable: %v", before["error"]))
		}
		info["markllm"] = markllm
	}

	if opts.backend == "print-prompt" {
		info["mode"] = "print-prompt"
		if opts.candidates > 1 {
			cleaning.Eprint("note: --candidates ignored in print-prompt mode")
		}
		return prompt, info, nil
	}

	if opts.model == "" {
		return "", nil, fmt.Errorf("error: --model required for ollama/openai-compatible backends")
	}
	if opts.baseURL == "" {
		return "", nil, fmt.Errorf("error: --base-url required for ollama/openai-compatible backends")
	}
	if err := checkRemote(opts.baseURL, opts.allowRemote); err != nil {
		return "", nil, err
	}

	n := opts.candidates
	if n < 1 {
		n = 1
	}
	outs := make([]string, 0, n)
	for i := 0; i < n; i++ {
		var out string
		var err error
		switch opts.backend {
		case "ollama":
			out, err = callOllama(opts.baseURL, opts.model, prompt, opts.timeout, opts.temperature)
		case "openai-compatible":
			out, err = callOpenAICompatible(opts.baseURL, opts.model, prompt, opts.apiKey, opts.timeout, opts.temperature, opts.reasoningEffort)
		default:
			return "", nil, fmt.Errorf("unknown backend: %s", opts.backend)
		}
		if err != nil {
			return "", nil, err
		}
		outs = append(outs, out)
	}

	var out string
	if len(outs) == 1 {
		out = outs[0]
	} else {
		info["candidates"] = n
		var scores []float64
		out, scores = selectCandidate(text, outs)
		info["candidate_scores"] = scores
	}

	if opts.layerAAfter {
		res := cleaning.CleanText(out, false, false, true, false)
		out = res.Text
		info["layer_a_after"] = res.Stats
	}

	info["output_chars"] = len([]rune(out))
	info["mode"] = "rewritten"
	info["note"] = "Layer B is best-effort against statistical token-sampling watermarks; " +
		"cannot certify removal against a vendor detector."

	if markllm != nil {
		after := markllmDetect(out, opts.markllmScheme, opts.markllmDir, opts.markllmModel, opts.markllmTimeout)
		markllm["after"] = after
		before, _ := markllm["before"].(map[string]interface{})
		if boolDict(before, "available") && boolDict(after, "available") {
			markllm["cleared"] = boolDict(before, "is_watermarked") && !boolDict(after, "is_watermarked")
		}
		markllm["note"] = "MarkLLM detection is only valid against the SAME scheme config + " +
			"keys used at generation; it does not certify a vendor detector."
	}

	return out, info, nil
}

func nilIfEmpty(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func boolDict(m map[string]interface{}, key string) bool {
	if m == nil {
		return false
	}
	b, _ := m[key].(bool)
	return b
}

// parseArgsAllowInterspersed mirrors argparse's tolerance of options after
// positionals; Go's flag package rejects that natively.
func parseArgsAllowInterspersed() (positional []string) {
	args := os.Args[1:]
	for {
		if err := flag.CommandLine.Parse(args); err != nil {
			os.Exit(2)
		}
		if flag.CommandLine.NArg() == 0 {
			return positional
		}
		positional = append(positional, flag.CommandLine.Arg(0))
		args = flag.CommandLine.Args()[1:]
	}
}

func validateChoice(name, value string, allowed []string) {
	for _, a := range allowed {
		if value == a {
			return
		}
	}
	cleaning.Eprint(fmt.Sprintf("invalid choice: '%s' for --%s (choose from %s)", value, name, "'"+strings.Join(allowed, "', '")+"'"))
	os.Exit(2)
}

func main() {
	var output string
	var backend, model, baseURL, reasoningEffort, strength, lang, originalLang string
	var markllmScheme, markllmDir, markllmModel string
	var timeout, temperature, markllmTimeout float64
	var candidates int
	var allowRemoteFlag string // "", "true", "false" (tri-state like argparse default=None)
	var noLayerAAfter, jsonStats, forceText bool

	flag.StringVar(&output, "o", "", "Output path (default: stdout or *.rewritten.*)")
	flag.StringVar(&output, "output", "", "Output path (default: stdout or *.rewritten.*)")
	choiceEnv := func(name, def string) string {
		if v := envOrEmpty(name); v != "" {
			return v
		}
		return def
	}
	flag.StringVar(&backend, "backend", choiceEnv("ANTIAIMARK_REWRITE_BACKEND", "print-prompt"), "rewrite backend")
	flag.StringVar(&model, "model", envOrEmpty("ANTIAIMARK_REWRITE_MODEL"), "model name")
	flag.StringVar(&baseURL, "base-url", choiceEnv("ANTIAIMARK_REWRITE_BASE_URL", "http://127.0.0.1:11434"), "backend base URL")
	flag.StringVar(&allowRemoteFlag, "allow-remote", "", "Allow non-loopback rewrite endpoints (default: deny; ANTIAIMARK_REWRITE_ALLOW_REMOTE=1 has the same effect)")
	flag.StringVar(&reasoningEffort, "reasoning-effort", choiceEnv("ANTIAIMARK_REWRITE_REASONING_EFFORT", "none"), "OpenAI-compatible reasoning_effort; 'none' skips chain-of-thought; 'off' omits the parameter entirely")
	// NOTE: no -api-key flag on purpose — keys on argv are visible in `ps`
	// and shell history. Set ANTIAIMARK_REWRITE_API_KEY instead.
	flag.StringVar(&strength, "strength", "paraphrase", "rewrite strength")
	flag.StringVar(&lang, "lang", "French", "Pivot language for backtranslate")
	flag.StringVar(&originalLang, "original-lang", "English", "Original language")
	flag.Float64Var(&timeout, "timeout", 120.0, "HTTP timeout for the rewrite backend")
	flag.Float64Var(&temperature, "temperature", 0.9, "Sampling temperature for the rewrite backend")
	flag.IntVar(&candidates, "candidates", 1, "Number of rewrite candidates to generate and score")
	flag.BoolVar(&noLayerAAfter, "no-layer-a-after", false, "Skip Layer A scrub on model output")
	flag.BoolVar(&jsonStats, "json-stats", false, "Stats JSON on stderr")
	flag.StringVar(&markllmScheme, "markllm-scheme", "", "Optional: run MarkLLM before/after detection around the rewrite")
	flag.StringVar(&markllmDir, "markllm-dir", envOrEmpty("MARKLLM_DIR"), "MarkLLM checkout root (default: $MARKLLM_DIR)")
	flag.StringVar(&markllmModel, "markllm-model", choiceEnv("ANTIAIMARK_MARKLLM_MODEL", defaultMarkllmModel), "Scoring model for MarkLLM detection")
	flag.Float64Var(&markllmTimeout, "markllm-timeout", 180.0, "Timeout per MarkLLM detection call (default: 180.0)")
	flag.BoolVar(&forceText, "force-text", false, "Rewrite even when the input looks like a binary container")
	var langFlag string
	cliutil.AddLangFlag(&langFlag)
	positional := cliutil.ParseAllowInterspersed()
	cliutil.Init(langFlag)

	validateChoice("backend", backend, []string{"print-prompt", "ollama", "openai-compatible"})
	validateChoice("strength", strength, []string{"paraphrase", "backtranslate", "structural", "humanize", "code"})
	validateChoice("reasoning-effort", reasoningEffort, []string{"none", "low", "medium", "high", "off"})
	if markllmScheme != "" {
		validateChoice("markllm-scheme", markllmScheme, []string{"kgw", "synthid", "synthid-text"})
	}

	path := "-"
	if len(positional) > 0 {
		path = positional[0]
	}

	text, err := cleaning.ReadTextInput(path, forceText, nil)
	if err != nil {
		cliutil.FatalErr(err)
	}

	allowRemote := flagEnv("ANTIAIMARK_REWRITE_ALLOW_REMOTE")
	if allowRemoteFlag != "" {
		allowRemote = allowRemoteFlag == "true" || allowRemoteFlag == "1"
	}

	reasoning := reasoningEffort
	if reasoning == "off" {
		reasoning = ""
	}

	result, info, err := rewriteText(text, rewriteOptions{
		backend:         backend,
		model:           model,
		baseURL:         baseURL,
		apiKey:          envOrEmpty("ANTIAIMARK_REWRITE_API_KEY"),
		strength:        strength,
		lang:            lang,
		originalLang:    originalLang,
		timeout:         timeout,
		layerAAfter:     !noLayerAAfter,
		temperature:     temperature,
		candidates:      candidates,
		allowRemote:     allowRemote,
		reasoningEffort: reasoning,
		markllmScheme:   markllmScheme,
		markllmDir:      markllmDir,
		markllmModel:    markllmModel,
		markllmTimeout:  markllmTimeout,
	})
	if err != nil {
		msg := err.Error()
		if strings.HasPrefix(msg, "error: ") {
			// Python SystemExit("error: ...") prints the message as-is.
			cleaning.Eprint(msg)
		} else {
			cleaning.Eprint("rewrite failed: " + msg)
		}
		os.Exit(1)
	}

	out := output
	if out == "" && path != "-" && backend != "print-prompt" {
		out = cleaning.CleanedPath(path, ".rewritten")
	} else if out == "" && backend == "print-prompt" {
		out = "-"
	}

	if err := cleaning.WriteTextOutput(result, out); err != nil {
		cleaning.Eprint("error: " + err.Error())
		os.Exit(1)
	}

	if jsonStats {
		if b, err := cleaning.MarshalIndentNoEscape(info); err == nil {
			cleaning.Eprint(string(b))
		}
	} else {
		outChars, ok := info["output_chars"]
		if !ok {
			outChars = len([]rune(result))
		}
		mode, _ := info["mode"]
		cleaning.Eprint(fmt.Sprintf("backend=%s strength=%s mode=%v chars %v->%v",
			info["backend"], info["strength"], mode, info["input_chars"], outChars))
	}
	os.Exit(0)
}
