// Package report defines the output types for fog-of-war-clearer analysis results.
// All output structures are serializable to JSON and are designed to never expose
// sensitive information such as Personal Access Tokens (PATs).
package report

import "time"

// CheckType identifies the kind of analysis that was performed.
type CheckType string

const (
	// CheckTestCoverage measures the percentage of code covered by tests.
	CheckTestCoverage CheckType = "test-coverage"
)

// Language represents a supported programming language.
type Language string

const (
	LanguageTypeScript Language = "typescript"
	LanguageJavaScript Language = "javascript"
	LanguageJava       Language = "java"
	LanguageKotlin     Language = "kotlin"
	LanguageGo         Language = "go"
	LanguageRust       Language = "rust"
	LanguagePHP        Language = "php"
)

// CheckStatus indicates whether a check succeeded or failed.
type CheckStatus string

const (
	CheckStatusSuccess CheckStatus = "success"
	CheckStatusFailure CheckStatus = "failure"
	CheckStatusSkipped CheckStatus = "skipped"
)

// CoverageMetrics holds coverage percentages for a single language detected in the
// repository.  All fields are percentages in the range [0, 100].
type CoverageMetrics struct {
	Language   Language `json:"language"`
	Lines      float64  `json:"lines"`
	Statements float64  `json:"statements,omitempty"`
	Branches   float64  `json:"branches,omitempty"`
	Functions  float64  `json:"functions,omitempty"`
}

// CheckResult holds the result of a single check type.
type CheckResult struct {
	Type     CheckType         `json:"type"`
	Status   CheckStatus       `json:"status"`
	Coverage []CoverageMetrics `json:"coverage,omitempty"`
	Error    string            `json:"error,omitempty"`
}

// Report is the top-level output structure returned by both the CLI and API.
// It is safe to serialise directly to JSON: it contains no secrets.
type Report struct {
	Repo   string        `json:"repo"`
	RunAt  time.Time     `json:"run_at"`
	Checks []CheckResult `json:"checks"`
}
