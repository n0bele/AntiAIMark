// SVG inspect/clean — port of the SVG section of container_meta.py.
package cleaning

import (
	"regexp"
)

func inspectSVG(data []byte) (bool, bool, []string, map[string]interface{}) {
	var findings []string
	hasC2pa, hasAI, hits := blobHits(data)
	findings = append(findings, hits...)
	text := string(bytesToValidUTF8(data)) // errors="replace" per maximal subpart
	if svgMetadataRe.MatchString(text) {
		findings = append(findings, "svg <metadata> present")
		hasAI = true // often XMP; treat as inspect signal
	}
	if svgXmpRe.MatchString(text) {
		hasAI = true
		findings = append(findings, "XMP/RDF-like content in SVG")
	}
	if svgC2paRe.MatchString(text) {
		hasC2pa = true
	}
	return hasC2pa, hasAI || hasC2pa, findings, map[string]interface{}{}
}

func cleanSVG(data []byte) ([]byte, []string) {
	var actions []string
	text := string(data) // errors="surrogateescape" -> byte-preserving in Go
	// Drop metadata blocks
	if n := len(svgMetadataBlockRe.FindAllString(text, -1)); n > 0 {
		actions = append(actions, "drop <metadata> x"+itoa(n))
		text = svgMetadataBlockRe.ReplaceAllString(text, "")
	}
	// Drop adobe xmp packets
	if n := len(svgXmpmetaBlockRe.FindAllString(text, -1)); n > 0 {
		actions = append(actions, "drop xmpmeta x"+itoa(n))
		text = svgXmpmetaBlockRe.ReplaceAllString(text, "")
	}
	// Drop comments that look like provenance
	text = svgCommentRe.ReplaceAllStringFunc(text, func(body string) string {
		if aiMetaNameRe.MatchString(body) {
			actions = append(actions, "drop SVG comment with AI markers")
			return ""
		}
		return body
	})
	if len(actions) == 0 {
		// still strip generator attribute on root if present
		if n := len(svgGenAttrRe.FindAllString(text, -1)); n > 0 {
			actions = append(actions, "drop generator-like attrs x"+itoa(n))
			text = svgGenAttrRe.ReplaceAllString(text, "")
		}
	}
	if len(actions) == 0 {
		actions = append(actions, "no SVG metadata removed")
	}
	return []byte(text), actions
}

var svgMetadataRe = regexp.MustCompile(`(?i)<metadata[\s>]`)
var svgXmpRe = regexp.MustCompile(`(?i)xmpmeta|rdf:RDF|contentcredentials`)
var svgC2paRe = regexp.MustCompile(`(?i)c2pa|jumbf`)
var svgMetadataBlockRe = regexp.MustCompile(`(?is)<metadata\b[^>]*>.*?</metadata\s*>`)
var svgXmpmetaBlockRe = regexp.MustCompile(`(?is)<x:xmpmeta\b[^>]*>.*?</x:xmpmeta\s*>`)
var svgCommentRe = regexp.MustCompile(`(?s)<!--.*?-->`)
var svgGenAttrRe = regexp.MustCompile(`(?i)\s(inkscape:version|sodipodi:docname|generator)\s*=\s*"[^"]*"`)
