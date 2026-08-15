// Shared helpers for aggregate directory and website audits — Go port of
// service/scripts/audit_lib.py. Both audits normalize every file/URL into the
// same per-item map so a single aggregate summary can be computed and
// rendered consistently.
package cleaning

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"watermarks-remover/internal/i18n"
)

// TextHitConfidence mirrors text_hit_confidence: Layer A space homoglyphs are
// weaker context than invisible carriers.
func TextHitConfidence(kind string) string {
	if kind == "space" {
		return "informational"
	}
	return "probable"
}

// TextFindings flattens a TextInspectReport into finding strings +
// confidence lists (mirrors text_findings).
func TextFindings(report TextInspectReport) (findings []string, confidences []string, suspicious int) {
	for _, h := range report.Hits {
		conf := TextHitConfidence(h.Kind)
		findings = append(findings, fmt.Sprintf("layer-a [%s] %s x%d", h.Kind, h.Label, h.Count))
		confidences = append(confidences, conf)
	}
	return findings, confidences, report.SuspiciousTotal
}

// AuditItem is the normalized per-file audit record from scan_file.
type AuditItem struct {
	Path            string
	Kind            string
	HasC2PA         bool
	HasAIMetadata   bool
	SuspiciousTotal int
	Findings        []string
	Confidence      []string
	Notes           []string
	Err             string // set when the file could not be read
}

// ToDict renders the item as the Python scan_file dict shape.
func (i AuditItem) ToDict() map[string]interface{} {
	if i.Err != "" {
		return map[string]interface{}{"path": i.Path, "kind": i.Kind, "error": i.Err}
	}
	return map[string]interface{}{
		"path":             i.Path,
		"kind":             i.Kind,
		"has_c2pa":         i.HasC2PA,
		"has_ai_metadata":  i.HasAIMetadata,
		"suspicious_total": i.SuspiciousTotal,
		"findings":         strSliceOrEmpty(i.Findings),
		"confidence":       strSliceOrEmpty(i.Confidence),
		"notes":            strSliceOrEmpty(i.Notes),
	}
}

func strSliceOrEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// ScanFile inspects one local file and returns a normalized audit item
// (mirrors scan_file).
func ScanFile(path string, displayName string) AuditItem {
	name := displayName
	if name == "" {
		name = path
	}
	kind, err := Classify(path)
	if err != nil {
		return AuditItem{Path: name, Kind: "text", Err: err.Error()}
	}

	if kind == KindText {
		data, err := os.ReadFile(path)
		if err != nil {
			return AuditItem{Path: name, Kind: "text", Err: err.Error()}
		}
		report := InspectText(string(data), false, false)
		findings, confidences, suspicious := TextFindings(report)
		return AuditItem{
			Path:            name,
			Kind:            "text",
			SuspiciousTotal: suspicious,
			Findings:        findings,
			Confidence:      confidences,
			Notes:           report.Notes,
		}
	}

	if kind == KindImage {
		report, err := InspectImage(path, "")
		if err != nil {
			return AuditItem{Path: name, Kind: "image", Err: err.Error()}
		}
		confidences := make([]string, 0, len(report.Findings))
		for _, f := range report.Findings {
			confidences = append(confidences, ClassifyFindingConfidence(f))
		}
		return AuditItem{
			Path:          name,
			Kind:          report.Format,
			HasC2PA:       report.HasC2PA,
			HasAIMetadata: report.HasAIMetadata,
			Findings:      report.Findings,
			Confidence:    confidences,
			Notes:         report.Notes,
		}
	}

	report, err := InspectContainer(path)
	if err != nil {
		return AuditItem{Path: name, Kind: "container", Err: err.Error()}
	}
	findings := append([]string{}, report.Findings...)
	confidences := make([]string, 0, len(report.Findings))
	for _, f := range report.Findings {
		confidences = append(confidences, ClassifyFindingConfidence(f))
	}
	suspicious := 0

	// Text-bearing containers also get a Layer A scan of their visible text,
	// mirroring the skill's "container + Layer A" workflow.
	if report.Format == "html" || report.Format == "markdown" {
		if data, err := os.ReadFile(path); err == nil {
			text := string(data)
			if text != "" {
				tReport := InspectText(text, false, false)
				tFindings, tConfidences, tSuspicious := TextFindings(tReport)
				findings = append(findings, tFindings...)
				confidences = append(confidences, tConfidences...)
				suspicious = tSuspicious
			}
		}
	}

	return AuditItem{
		Path:            name,
		Kind:            report.Format,
		HasC2PA:         report.HasC2PA,
		HasAIMetadata:   report.HasAIMetadata,
		SuspiciousTotal: suspicious,
		Findings:        findings,
		Confidence:      confidences,
		Notes:           report.Notes,
	}
}

// IsActionable reports a confirmed/probable finding or C2PA
// (mirrors is_actionable).
func IsActionable(item map[string]interface{}) bool {
	if b, _ := item["has_c2pa"].(bool); b {
		return true
	}
	for _, c := range item["confidence"].([]string) {
		if c == "confirmed" || c == "probable" {
			return true
		}
	}
	return false
}

// Aggregate builds the summary block shared by directory and website audits
// (mirrors aggregate).
func Aggregate(files []map[string]interface{}) map[string]interface{} {
	byKind := map[string]int{}
	findingsByConfidence := map[string]int{}
	for _, c := range ConfidenceLevels {
		findingsByConfidence[c] = 0
	}
	withC2PA, withAI, withSuspicious, actionable := 0, 0, 0, 0
	for _, item := range files {
		kind := "error"
		if k, ok := item["kind"].(string); ok && k != "" {
			kind = k
		}
		byKind[kind]++
		if b, _ := item["has_c2pa"].(bool); b {
			withC2PA++
		}
		if b, _ := item["has_ai_metadata"].(bool); b {
			withAI++
		}
		if n, _ := item["suspicious_total"].(int); n > 0 {
			withSuspicious++
		}
		if confs, ok := item["confidence"].([]string); ok {
			for _, c := range confs {
				if _, exists := findingsByConfidence[c]; exists {
					findingsByConfidence[c]++
				}
			}
		}
		if IsActionable(item) {
			actionable++
		}
	}
	return map[string]interface{}{
		"total":                  len(files),
		"by_kind":                byKind,
		"with_c2pa":              withC2PA,
		"with_ai_metadata":       withAI,
		"with_suspicious_text":   withSuspicious,
		"actionable_files":       actionable,
		"findings_by_confidence": findingsByConfidence,
	}
}

// PrintHumanReport renders the shared plain-text audit report in the active
// locale. extraHeader is an ordered list of (i18n key, value) pairs rendered
// before the summary.
func PrintHumanReport(files []map[string]interface{}, summary map[string]interface{}, extraHeader [][2]string) {
	for _, pair := range extraHeader {
		fmt.Println(i18n.T(pair[0], pair[1]))
	}
	fmt.Println(i18n.T("audit.files_scanned", summary["total"]))
	fmt.Println(i18n.T("audit.by_kind", pyDictInt(summary["by_kind"].(map[string]int))))
	fmt.Println(i18n.T("audit.with_c2pa", summary["with_c2pa"]))
	fmt.Println(i18n.T("audit.with_ai", summary["with_ai_metadata"]))
	fmt.Println(i18n.T("audit.with_suspicious", summary["with_suspicious_text"]))
	fmt.Println(i18n.T("audit.actionable", summary["actionable_files"]))
	fmt.Println(i18n.T("audit.by_confidence", pyConfidenceDict(summary["findings_by_confidence"].(map[string]int))))
	for _, item := range files {
		findings, _ := item["findings"].([]string)
		confidence, _ := item["confidence"].([]string)
		for i := 0; i < len(findings) && i < len(confidence); i++ {
			fmt.Printf("  [%s] %s: %s\n", confidence[i], item["path"], findings[i])
		}
	}
}

// pyDictInt renders a map like Python's dict repr, insertion order by
// first-seen kind (audit_lib builds by_kind in scan order; Go maps are
// unordered, so alphabetical order is used deterministically).
func pyDictInt(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = fmt.Sprintf("'%s': %d", k, m[k])
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// pyConfidenceDict renders findings_by_confidence in CONFIDENCE_LEVELS order,
// matching the Python dict built from that tuple.
func pyConfidenceDict(m map[string]int) string {
	parts := make([]string, 0, len(ConfidenceLevels))
	for _, k := range ConfidenceLevels {
		parts = append(parts, fmt.Sprintf("'%s': %d", k, m[k]))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// WalkAuditFiles mirrors audit_dir.walk_files: sorted recursive walk skipping
// DEFAULT_SKIP_DIRS and dot-directories.
func WalkAuditFiles(root string, skipDirs map[string]bool) []string {
	var out []string
	filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // keep the audit going
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (skipDirs[name] || strings.HasPrefix(name, ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() {
			out = append(out, path)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// DefaultSkipDirs mirrors audit_dir.DEFAULT_SKIP_DIRS.
var DefaultSkipDirs = map[string]bool{
	".git": true, ".hg": true, ".svn": true, "node_modules": true,
	"__pycache__": true, ".venv": true, "venv": true, ".tox": true,
	".mypy_cache": true, ".pytest_cache": true, "dist": true, "build": true,
	".next": true, "target": true, ".cache": true,
}
