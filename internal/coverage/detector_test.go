package coverage

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

// helpers

// makeTempRepo creates a temporary directory representing a minimal repository.
func makeTempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	return dir
}

func writeFile(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", relPath, err)
	}
}

// ---

func TestDetectLanguages_TypeScript(t *testing.T) {
	dir := makeTempRepo(t)
	writeFile(t, dir, "package.json", `{"name":"test"}`)
	writeFile(t, dir, "src/index.ts", `export const x = 1;`)

	langs := DetectLanguages(dir)
	assertContains(t, langs, report.LanguageTypeScript)
	assertNotContains(t, langs, report.LanguageJavaScript)
}

func TestDetectLanguages_JavaScript(t *testing.T) {
	dir := makeTempRepo(t)
	writeFile(t, dir, "package.json", `{"name":"test"}`)
	writeFile(t, dir, "src/index.js", `module.exports = {};`)

	langs := DetectLanguages(dir)
	assertContains(t, langs, report.LanguageJavaScript)
}

func TestDetectLanguages_Mixed_TS_JS(t *testing.T) {
	dir := makeTempRepo(t)
	writeFile(t, dir, "package.json", `{"name":"test"}`)
	writeFile(t, dir, "src/index.ts", `export const x = 1;`)
	writeFile(t, dir, "src/helper.js", `module.exports = {};`)

	langs := DetectLanguages(dir)
	assertContains(t, langs, report.LanguageTypeScript)
	assertContains(t, langs, report.LanguageJavaScript)
}

func TestDetectLanguages_Java_Maven(t *testing.T) {
	dir := makeTempRepo(t)
	writeFile(t, dir, "pom.xml", `<project/>`)
	writeFile(t, dir, "src/main/java/App.java", `class App {}`)

	langs := DetectLanguages(dir)
	assertContains(t, langs, report.LanguageJava)
	assertNotContains(t, langs, report.LanguageKotlin)
}

func TestDetectLanguages_Kotlin(t *testing.T) {
	dir := makeTempRepo(t)
	writeFile(t, dir, "build.gradle.kts", `plugins { kotlin("jvm") }`)
	writeFile(t, dir, "src/main/kotlin/App.kt", `fun main() {}`)

	langs := DetectLanguages(dir)
	assertContains(t, langs, report.LanguageKotlin)
	assertNotContains(t, langs, report.LanguageJava)
}

func TestDetectLanguages_EmptyRepo(t *testing.T) {
	dir := makeTempRepo(t)
	langs := DetectLanguages(dir)
	if len(langs) != 0 {
		t.Errorf("expected no languages for empty repo, got %v", langs)
	}
}

func TestDetectLanguages_SkipsNodeModules(t *testing.T) {
	dir := makeTempRepo(t)
	writeFile(t, dir, "package.json", `{"name":"test"}`)
	// Only TypeScript in node_modules – should not influence detection
	writeFile(t, dir, "node_modules/dep/index.ts", `export {};`)

	langs := DetectLanguages(dir)
	// TypeScript should not be detected because only source is inside node_modules
	assertNotContains(t, langs, report.LanguageTypeScript)
}

// assertions

func assertContains(t *testing.T, langs []report.Language, lang report.Language) {
	t.Helper()
	for _, l := range langs {
		if l == lang {
			return
		}
	}
	t.Errorf("expected %q in %v", lang, langs)
}

func assertNotContains(t *testing.T, langs []report.Language, lang report.Language) {
	t.Helper()
	for _, l := range langs {
		if l == lang {
			t.Errorf("did not expect %q in %v", lang, langs)
			return
		}
	}
}
