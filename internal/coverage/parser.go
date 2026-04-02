package coverage

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

// ParseJestSummary parses the JSON summary produced by jest with
// --coverageReporters=json-summary.  The top-level "total" key is used.
//
// Example format:
//
//	{
//	  "total": {
//	    "lines":     {"pct": 85.5},
//	    "statements":{"pct": 86.0},
//	    "branches":  {"pct": 72.0},
//	    "functions": {"pct": 90.0}
//	  }
//	}
func ParseJestSummary(lang report.Language, raw string) (report.CoverageMetrics, error) {
	// Jest may print log output before the JSON; find the first '{'.
	idx := strings.Index(raw, "{")
	if idx < 0 {
		return report.CoverageMetrics{}, fmt.Errorf("no JSON object found in jest output")
	}
	raw = raw[idx:]

	var outer map[string]map[string]struct {
		Pct json.Number `json:"pct"`
	}
	if err := json.Unmarshal([]byte(raw), &outer); err != nil {
		return report.CoverageMetrics{}, fmt.Errorf("parse jest summary: %w", err)
	}

	total, ok := outer["total"]
	if !ok {
		return report.CoverageMetrics{}, fmt.Errorf("jest summary missing 'total' key")
	}

	metrics := report.CoverageMetrics{Language: lang}
	if v, ok := total["lines"]; ok {
		metrics.Lines, _ = v.Pct.Float64()
	}
	if v, ok := total["statements"]; ok {
		metrics.Statements, _ = v.Pct.Float64()
	}
	if v, ok := total["branches"]; ok {
		metrics.Branches, _ = v.Pct.Float64()
	}
	if v, ok := total["functions"]; ok {
		metrics.Functions, _ = v.Pct.Float64()
	}
	return metrics, nil
}

// jacocoReport matches a subset of the JaCoCo XML report structure used by
// ParseJacocoXML.
type jacocoCounter struct {
	Type    string `json:"type"`
	Covered int    `json:"covered"`
	Missed  int    `json:"missed"`
}

// ParseJacocoXML parses XML produced by `mvn jacoco:report` or
// `gradle jacocoTestReport`.  It accepts the raw output from the container,
// which may include log lines before the XML.
//
// The parser is intentionally lenient and extracts only the top-level
// <report> counters.
func ParseJacocoXML(lang report.Language, raw string) (report.CoverageMetrics, error) {
	// Find the XML declaration or <report tag.
	xmlStart := strings.Index(raw, "<?xml")
	if xmlStart < 0 {
		xmlStart = strings.Index(raw, "<report")
	}
	if xmlStart < 0 {
		return report.CoverageMetrics{}, fmt.Errorf("no XML found in jacoco output")
	}
	xml := raw[xmlStart:]

	metrics := report.CoverageMetrics{Language: lang}

	// Simple attribute extraction without importing xml package to keep the
	// parser lightweight and dependency-free.  JaCoCo counter elements look like:
	//   <counter type="LINE" missed="5" covered="95"/>
	//
	// We read the LAST occurrence of each counter type in the document (which
	// corresponds to the report-level totals that appear after per-class entries).
	counters := extractJacocoCounters(xml)
	for _, c := range counters {
		total := float64(c.Covered + c.Missed)
		if total == 0 {
			continue
		}
		pct := float64(c.Covered) / total * 100
		switch c.Type {
		case "LINE":
			metrics.Lines = pct
		case "INSTRUCTION":
			metrics.Statements = pct
		case "BRANCH":
			metrics.Branches = pct
		case "METHOD":
			metrics.Functions = pct
		}
	}
	return metrics, nil
}

// extractJacocoCounters extracts all <counter ...> elements from the xml fragment.
func extractJacocoCounters(xml string) []jacocoCounter {
	var counters []jacocoCounter
	// Look for patterns like: <counter type="LINE" missed="5" covered="95"/>
	remaining := xml
	for {
		start := strings.Index(remaining, "<counter ")
		if start < 0 {
			break
		}
		end := strings.Index(remaining[start:], "/>")
		if end < 0 {
			break
		}
		tag := remaining[start : start+end+2]
		remaining = remaining[start+end+2:]

		ctype := extractAttr(tag, "type")
		missed := extractAttrInt(tag, "missed")
		covered := extractAttrInt(tag, "covered")
		if ctype != "" {
			counters = append(counters, jacocoCounter{Type: ctype, Covered: covered, Missed: missed})
		}
	}
	return counters
}

// extractAttr extracts the value of a named XML attribute from a tag string.
func extractAttr(tag, name string) string {
	search := name + `="`
	idx := strings.Index(tag, search)
	if idx < 0 {
		return ""
	}
	rest := tag[idx+len(search):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// extractAttrInt extracts a named XML attribute as an integer.
func extractAttrInt(tag, name string) int {
	v := extractAttr(tag, name)
	if v == "" {
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return n
}
