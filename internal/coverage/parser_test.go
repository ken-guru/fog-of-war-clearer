package coverage

import (
	"math"
	"testing"

	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

const floatEps = 0.01

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < floatEps
}

// --- ParseJestSummary ---

func TestParseJestSummary_FullSummary(t *testing.T) {
	raw := `
Some log line before JSON
{"total":{"lines":{"pct":85.5},"statements":{"pct":86.0},"branches":{"pct":72.0},"functions":{"pct":90.0}}}
`
	m, err := ParseJestSummary(report.LanguageTypeScript, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Language != report.LanguageTypeScript {
		t.Errorf("language: got %q, want %q", m.Language, report.LanguageTypeScript)
	}
	if !approxEqual(m.Lines, 85.5) {
		t.Errorf("lines: got %f, want 85.5", m.Lines)
	}
	if !approxEqual(m.Statements, 86.0) {
		t.Errorf("statements: got %f, want 86.0", m.Statements)
	}
	if !approxEqual(m.Branches, 72.0) {
		t.Errorf("branches: got %f, want 72.0", m.Branches)
	}
	if !approxEqual(m.Functions, 90.0) {
		t.Errorf("functions: got %f, want 90.0", m.Functions)
	}
}

func TestParseJestSummary_NoJSONReturnsError(t *testing.T) {
	_, err := ParseJestSummary(report.LanguageJavaScript, "no json here")
	if err == nil {
		t.Fatal("expected error for missing JSON")
	}
}

func TestParseJestSummary_MissingTotalKeyReturnsError(t *testing.T) {
	raw := `{"other":{"lines":{"pct":50}}}`
	_, err := ParseJestSummary(report.LanguageJavaScript, raw)
	if err == nil {
		t.Fatal("expected error for missing 'total' key")
	}
}

func TestParseJestSummary_ZeroValues(t *testing.T) {
	raw := `{"total":{"lines":{"pct":0},"statements":{"pct":0},"branches":{"pct":0},"functions":{"pct":0}}}`
	m, err := ParseJestSummary(report.LanguageJavaScript, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Lines != 0 || m.Statements != 0 || m.Branches != 0 || m.Functions != 0 {
		t.Errorf("expected all zeros, got %+v", m)
	}
}

// --- ParseJacocoXML ---

func TestParseJacocoXML_FullReport(t *testing.T) {
	raw := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<!DOCTYPE report PUBLIC "-//JACOCO//DTD Report 1.1//EN" "report.dtd">
<report name="myapp">
  <counter type="INSTRUCTION" missed="100" covered="900"/>
  <counter type="BRANCH" missed="20" covered="80"/>
  <counter type="LINE" missed="50" covered="450"/>
  <counter type="COMPLEXITY" missed="10" covered="90"/>
  <counter type="METHOD" missed="5" covered="95"/>
  <counter type="CLASS" missed="0" covered="20"/>
</report>`

	m, err := ParseJacocoXML(report.LanguageJava, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Language != report.LanguageJava {
		t.Errorf("language: got %q, want %q", m.Language, report.LanguageJava)
	}
	// LINE: 450/(450+50) = 90%
	if !approxEqual(m.Lines, 90.0) {
		t.Errorf("lines: got %f, want 90.0", m.Lines)
	}
	// INSTRUCTION: 900/(900+100) = 90%
	if !approxEqual(m.Statements, 90.0) {
		t.Errorf("statements: got %f, want 90.0", m.Statements)
	}
	// BRANCH: 80/(80+20) = 80%
	if !approxEqual(m.Branches, 80.0) {
		t.Errorf("branches: got %f, want 80.0", m.Branches)
	}
	// METHOD: 95/(95+5) = 95%
	if !approxEqual(m.Functions, 95.0) {
		t.Errorf("functions: got %f, want 95.0", m.Functions)
	}
}

func TestParseJacocoXML_NoXMLReturnsError(t *testing.T) {
	_, err := ParseJacocoXML(report.LanguageJava, "no xml here")
	if err == nil {
		t.Fatal("expected error for missing XML")
	}
}

func TestParseJacocoXML_PrefixedWithBuildOutput(t *testing.T) {
	raw := `[INFO] BUILD SUCCESS
[INFO] Total time: 3.421 s
<?xml version="1.0"?>
<report name="test">
  <counter type="LINE" missed="10" covered="90"/>
</report>`
	m, err := ParseJacocoXML(report.LanguageKotlin, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// 90/(90+10) = 90%
	if !approxEqual(m.Lines, 90.0) {
		t.Errorf("lines: got %f, want 90.0", m.Lines)
	}
}

func TestParseJacocoXML_ZeroCoveredAndMissed(t *testing.T) {
	raw := `<report name="empty">
  <counter type="LINE" missed="0" covered="0"/>
</report>`
	m, err := ParseJacocoXML(report.LanguageJava, raw)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if m.Lines != 0 {
		t.Errorf("lines should be 0 when total=0, got %f", m.Lines)
	}
}

func TestExtractAttr(t *testing.T) {
	tag := `<counter type="LINE" missed="5" covered="95"/>`
	if v := extractAttr(tag, "type"); v != "LINE" {
		t.Errorf("type: got %q, want LINE", v)
	}
	if v := extractAttr(tag, "missed"); v != "5" {
		t.Errorf("missed: got %q, want 5", v)
	}
	if v := extractAttr(tag, "covered"); v != "95" {
		t.Errorf("covered: got %q, want 95", v)
	}
	if v := extractAttr(tag, "nonexistent"); v != "" {
		t.Errorf("nonexistent: got %q, want empty", v)
	}
}
