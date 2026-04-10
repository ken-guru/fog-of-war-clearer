package planner

import (
	"context"
	"fmt"

	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

// staticImages maps each supported language to a default Docker image.
var staticImages = map[report.Language]string{
	report.LanguageTypeScript: "node:20-slim",
	report.LanguageJavaScript: "node:20-slim",
	report.LanguageJava:       "maven:3.9-eclipse-temurin-21",
	report.LanguageKotlin:     "gradle:8.5-jdk21",
	report.LanguageGo:         "golang:1.24-alpine",
	report.LanguageRust:       "rust:1-slim",
	report.LanguagePHP:        "php:8.3-cli",
}

// nodeScript is the shared shell script used for both TypeScript and JavaScript
// coverage analysis.  Both languages use the same Node.js toolchain and test
// runner detection logic, so they share a single definition to avoid drift.
const nodeScript = `set -e
cp -r /repo/. /workspace/
cd /workspace
if [ -f package-lock.json ] || [ -f npm-shrinkwrap.json ]; then
  npm ci --ignore-scripts --no-fund --no-audit 2>&1
else
  npm install --ignore-scripts --no-fund --no-audit 2>&1
fi
if npx --no -- jest --version > /dev/null 2>&1; then
  npx jest --coverage --coverageReporters=json-summary --passWithNoTests --maxWorkers=2 --forceExit 2>&1
  echo '---COVERAGE_JSON---'
  cat coverage/coverage-summary.json 2>/dev/null || echo '{}'
elif npx --no -- vitest --version > /dev/null 2>&1; then
  npm install --no-save --ignore-scripts @vitest/coverage-v8 2>&1
  npx vitest run --coverage --coverage.reporter=json-summary 2>&1
  echo '---COVERAGE_JSON---'
  cat coverage/coverage-summary.json 2>/dev/null || echo '{}'
else
  echo '---COVERAGE_JSON---'
  echo '{"total":{"lines":{"pct":0},"statements":{"pct":0},"branches":{"pct":0},"functions":{"pct":0}}}'
fi`

// staticScripts maps each supported language to the shell command run inside
// the analysis container.  Every script copies /repo → /workspace, installs
// deps, runs tests with coverage, and emits output that the corresponding
// parser can consume.
var staticScripts = map[report.Language][]string{
	report.LanguageTypeScript: {"sh", "-c", nodeScript},
	report.LanguageJavaScript: {"sh", "-c", nodeScript},
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
	report.LanguageGo: {
		"sh", "-c",
		`set -e
cp -r /repo/. /workspace/
cd /workspace
go test -coverprofile=cover.out ./... 2>&1
# Convert to jest-summary-compatible JSON.
total=$(go tool cover -func=cover.out | tail -1 | grep -oE '[0-9]+\.[0-9]+')
echo '---COVERAGE_JSON---'
printf '{"total":{"lines":{"pct":%s},"statements":{"pct":%s},"branches":{"pct":0},"functions":{"pct":0}}}' "$total" "$total"`,
	},
	report.LanguageRust: {
		"sh", "-c",
		`set -e
cp -r /repo/. /workspace/
cd /workspace
cargo install cargo-tarpaulin --locked 2>&1
cargo tarpaulin --out Json --output-dir coverage 2>&1
# Extract the line coverage percentage.
pct=$(grep -oE '"covered_percent":[0-9.]+' coverage/tarpaulin-report.json | head -1 | cut -d: -f2)
echo '---COVERAGE_JSON---'
printf '{"total":{"lines":{"pct":%s},"statements":{"pct":%s},"branches":{"pct":0},"functions":{"pct":0}}}' "$pct" "$pct"`,
	},
	report.LanguagePHP: {
		"sh", "-c",
		`set -e
cp -r /repo/. /workspace/
cd /workspace
apt-get update -qq && apt-get install -yqq git unzip libzip-dev > /dev/null 2>&1
docker-php-ext-install zip > /dev/null 2>&1
# Install Xdebug (required for PHPUnit coverage) and Composer.
pecl install xdebug > /dev/null 2>&1 && docker-php-ext-enable xdebug > /dev/null 2>&1
curl -sS https://getcomposer.org/installer | php -- --install-dir=/usr/local/bin --filename=composer > /dev/null 2>&1
composer install --no-interaction --no-scripts 2>&1
if [ -f "vendor/bin/phpunit" ]; then
  XDEBUG_MODE=coverage vendor/bin/phpunit --coverage-clover=coverage/clover.xml 2>&1
  # Convert clover to jest-summary-like JSON using awk (no bc required).
  covered=$(grep -oP 'elements.*?covered="\K[0-9]+' coverage/clover.xml | head -1)
  elements=$(grep -oP 'elements.*?count="\K[0-9]+' coverage/clover.xml | head -1)
  pct=$(awk -v t="${covered:-0}" -v e="${elements:-0}" 'BEGIN{if(e>0) printf "%.2f",t*100/e; else print 0}')
  echo '---COVERAGE_JSON---'
  printf '{"total":{"lines":{"pct":%s},"statements":{"pct":%s},"branches":{"pct":0},"functions":{"pct":0}}}' "$pct" "$pct"
else
  echo '---COVERAGE_JSON---'
  echo '{"total":{"lines":{"pct":0},"statements":{"pct":0},"branches":{"pct":0},"functions":{"pct":0}}}'
fi`,
	},
}

// staticOutputFormats maps each language to its output format identifier.
var staticOutputFormats = map[report.Language]string{
	report.LanguageTypeScript: "jest-summary",
	report.LanguageJavaScript: "jest-summary",
	report.LanguageJava:       "jacoco-xml",
	report.LanguageKotlin:     "jacoco-xml",
	report.LanguageGo:         "jest-summary",
	report.LanguageRust:       "jest-summary",
	report.LanguagePHP:        "jest-summary",
}

// StaticPlanner returns deterministic, hardcoded RunPlans.  It is used as the
// fallback when the LLM planner is unavailable or fails.
type StaticPlanner struct{}

// Plan returns a RunPlan for each language using the hardcoded defaults.
func (s *StaticPlanner) Plan(_ context.Context, _ string, languages []report.Language) ([]RunPlan, error) {
	var plans []RunPlan
	for _, lang := range languages {
		img, ok := staticImages[lang]
		if !ok {
			return nil, fmt.Errorf("no static plan for language %s", lang)
		}
		script, ok := staticScripts[lang]
		if !ok {
			return nil, fmt.Errorf("no static script for language %s", lang)
		}
		plans = append(plans, RunPlan{
			Language:     lang,
			Image:        img,
			Script:       script,
			OutputFormat: staticOutputFormats[lang],
		})
	}
	return plans, nil
}
