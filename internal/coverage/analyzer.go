package coverage

import (
	"context"
	"fmt"
	"os"

	"github.com/ken-guru/fog-of-war-clearer/internal/runner"
	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

// dockerImages maps each supported language to the Docker image used for
// running its test suite.
var dockerImages = map[report.Language]string{
	report.LanguageTypeScript:  "node:20-slim",
	report.LanguageJavaScript:  "node:20-slim",
	report.LanguageJava:        "maven:3.9-eclipse-temurin-21",
	report.LanguageKotlin:      "gradle:8.5-jdk21",
}

// scripts contains the shell commands executed inside each container.
// They copy the repo to a writable workspace, install dependencies with
// lifecycle scripts suppressed (security), run the tests with coverage enabled,
// and emit the coverage summary to stdout.
var scripts = map[report.Language][]string{
	report.LanguageTypeScript: {
		"sh", "-c",
		// 1. Copy repo to writable workspace
		// 2. Install deps without running lifecycle scripts (prevents arbitrary code execution)
		// 3. Run jest with json-summary reporter; fall back to vitest
		// --maxWorkers=2 and --forceExit prevent OOM and hanging workers inside the resource-limited container
		`set -e
cp -r /repo/. /workspace/
cd /workspace
npm ci --ignore-scripts --no-fund --no-audit 2>&1
if npx --no -- jest --version > /dev/null 2>&1; then
  npx jest --coverage --coverageReporters=json-summary --passWithNoTests --maxWorkers=2 --forceExit 2>&1
  echo '---COVERAGE_JSON---'
  cat coverage/coverage-summary.json 2>/dev/null || echo '{}'
elif npx --no -- vitest --version > /dev/null 2>&1; then
  npx vitest run --coverage --pool=threads --poolOptions.threads.maxThreads=2 2>&1
  echo '---COVERAGE_JSON---'
  cat coverage/coverage-summary.json 2>/dev/null || echo '{}'
else
  echo '---COVERAGE_JSON---'
  echo '{"total":{"lines":{"pct":0},"statements":{"pct":0},"branches":{"pct":0},"functions":{"pct":0}}}'
fi`,
	},
	report.LanguageJavaScript: {
		"sh", "-c",
		`set -e
cp -r /repo/. /workspace/
cd /workspace
npm ci --ignore-scripts --no-fund --no-audit 2>&1
if npx --no -- jest --version > /dev/null 2>&1; then
  npx jest --coverage --coverageReporters=json-summary --passWithNoTests --maxWorkers=2 --forceExit 2>&1
  echo '---COVERAGE_JSON---'
  cat coverage/coverage-summary.json 2>/dev/null || echo '{}'
elif npx --no -- vitest --version > /dev/null 2>&1; then
  npx vitest run --coverage --pool=threads --poolOptions.threads.maxThreads=2 2>&1
  echo '---COVERAGE_JSON---'
  cat coverage/coverage-summary.json 2>/dev/null || echo '{}'
else
  echo '---COVERAGE_JSON---'
  echo '{"total":{"lines":{"pct":0},"statements":{"pct":0},"branches":{"pct":0},"functions":{"pct":0}}}'
fi`,
	},
	report.LanguageJava: {
		"sh", "-c",
		`set -e
cp -r /repo/. /workspace/
cd /workspace
if [ -f "pom.xml" ]; then
  mvn test jacoco:report -q --no-transfer-progress 2>&1
  find . -name "jacoco.xml" | head -1 | xargs cat 2>/dev/null || echo ''
else
  echo ''
fi`,
	},
	report.LanguageKotlin: {
		"sh", "-c",
		`set -e
cp -r /repo/. /workspace/
cd /workspace
if [ -f "build.gradle.kts" ] || [ -f "build.gradle" ]; then
  gradle test jacocoTestReport --no-daemon -q 2>&1
  find . -name "jacocoTestReport.xml" | head -1 | xargs cat 2>/dev/null || echo ''
else
  echo ''
fi`,
	},
}

// Analyzer runs coverage analysis for one or more detected languages.
type Analyzer struct {
	runner *runner.Runner
}

// NewAnalyzer creates an Analyzer backed by the given Docker runner.
func NewAnalyzer(r *runner.Runner) *Analyzer {
	return &Analyzer{runner: r}
}

// Analyze runs test-coverage analysis for each of the given languages against
// the repository located at repoDir.
func (a *Analyzer) Analyze(ctx context.Context, repoDir string, languages []report.Language) ([]report.CoverageMetrics, error) {
	var results []report.CoverageMetrics

	for _, lang := range languages {
		fmt.Fprintf(os.Stderr, "[fog] analysing %s test coverage...\n", lang)
		metrics, err := a.analyzeLanguage(ctx, repoDir, lang)
		if err != nil {
			return results, fmt.Errorf("coverage analysis for %s: %w", lang, err)
		}
		fmt.Fprintf(os.Stderr, "[fog] %s coverage analysis complete\n", lang)
		results = append(results, metrics)
	}
	return results, nil
}

func (a *Analyzer) analyzeLanguage(ctx context.Context, repoDir string, lang report.Language) (report.CoverageMetrics, error) {
	img, ok := dockerImages[lang]
	if !ok {
		return report.CoverageMetrics{}, fmt.Errorf("no Docker image configured for language %s", lang)
	}

	cmd, ok := scripts[lang]
	if !ok {
		return report.CoverageMetrics{}, fmt.Errorf("no analysis script configured for language %s", lang)
	}

	output, err := a.runner.Run(ctx, runner.RunOptions{
		Image:   img,
		Cmd:     cmd,
		RepoDir: repoDir,
	})
	if err != nil {
		return report.CoverageMetrics{}, fmt.Errorf("run container: %w", err)
	}

	switch lang {
	case report.LanguageTypeScript, report.LanguageJavaScript:
		return ParseJestSummary(lang, output)
	case report.LanguageJava, report.LanguageKotlin:
		return ParseJacocoXML(lang, output)
	default:
		return report.CoverageMetrics{}, fmt.Errorf("unsupported language: %s", lang)
	}
}
