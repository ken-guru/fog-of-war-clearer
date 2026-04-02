// Package coverage provides language detection, coverage analysis orchestration,
// and result parsing for the fog-of-war-clearer tool.
package coverage

import (
	"os"
	"path/filepath"

	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

// DetectLanguages inspects the directory tree rooted at dir and returns the set
// of supported languages that appear to be present.
func DetectLanguages(dir string) []report.Language {
	var found []report.Language

	if hasFile(dir, "package.json") {
		if hasExtension(dir, ".ts", ".tsx") {
			found = append(found, report.LanguageTypeScript)
		}
		if hasExtension(dir, ".js", ".jsx", ".mjs", ".cjs") {
			found = append(found, report.LanguageJavaScript)
		}
	}

	if hasKotlinProject(dir) {
		found = append(found, report.LanguageKotlin)
	} else if hasJavaProject(dir) {
		found = append(found, report.LanguageJava)
	}

	return found
}

// hasFile reports whether any file with the given base name exists anywhere
// under dir (non-recursive for performance on top-level project files).
func hasFile(dir, name string) bool {
	info, err := os.Stat(filepath.Join(dir, name))
	return err == nil && !info.IsDir()
}

// hasExtension reports whether any file with one of the given extensions exists
// anywhere in the directory tree under dir.
func hasExtension(dir string, exts ...string) bool {
	extSet := make(map[string]bool, len(exts))
	for _, e := range exts {
		extSet[e] = true
	}
	found := false
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if found || err != nil {
			return nil
		}
		if d.IsDir() && isSkippedDir(d.Name()) {
			return filepath.SkipDir
		}
		if extSet[filepath.Ext(path)] {
			found = true
		}
		return nil
	})
	return found
}

// hasJavaProject reports whether dir contains a Maven or Gradle Java project.
func hasJavaProject(dir string) bool {
	if hasFile(dir, "pom.xml") {
		return true
	}
	if hasFile(dir, "build.gradle") && !hasFile(dir, "build.gradle.kts") {
		// A plain build.gradle without Kotlin DSL – check for .java sources
		return hasExtension(dir, ".java")
	}
	return false
}

// hasKotlinProject reports whether dir contains a Kotlin project.
func hasKotlinProject(dir string) bool {
	if hasFile(dir, "build.gradle.kts") {
		return true
	}
	// Gradle project with .kt source files
	if hasFile(dir, "build.gradle") && hasExtension(dir, ".kt", ".kts") {
		return true
	}
	return false
}

// isSkippedDir returns true for well-known dependency/cache directories that
// should not be scanned for source files.
func isSkippedDir(name string) bool {
	switch name {
	case "node_modules", ".git", "vendor", "dist", "build", "target", ".gradle":
		return true
	}
	return false
}
