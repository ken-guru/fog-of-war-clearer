package planner

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

func TestStaticPlanner_AllLanguages(t *testing.T) {
	sp := &StaticPlanner{}
	languages := []report.Language{
		report.LanguageTypeScript,
		report.LanguageJavaScript,
		report.LanguageJava,
		report.LanguageKotlin,
		report.LanguageGo,
		report.LanguageRust,
		report.LanguagePHP,
	}

	plans, err := sp.Plan(context.Background(), "/tmp/test", languages)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plans) != len(languages) {
		t.Fatalf("got %d plans, want %d", len(plans), len(languages))
	}

	for i, plan := range plans {
		if plan.Language != languages[i] {
			t.Errorf("plan[%d].Language = %q, want %q", i, plan.Language, languages[i])
		}
		if plan.Image == "" {
			t.Errorf("plan[%d].Image is empty for %s", i, plan.Language)
		}
		if len(plan.Script) == 0 {
			t.Errorf("plan[%d].Script is empty for %s", i, plan.Language)
		}
		if plan.OutputFormat == "" {
			t.Errorf("plan[%d].OutputFormat is empty for %s", i, plan.Language)
		}
	}
}

func TestStaticPlanner_UnsupportedLanguage(t *testing.T) {
	sp := &StaticPlanner{}
	_, err := sp.Plan(context.Background(), "/tmp/test", []report.Language{"cobol"})
	if err == nil {
		t.Fatal("expected error for unsupported language")
	}
}

func TestIsAllowedImage(t *testing.T) {
	tests := []struct {
		image   string
		allowed bool
	}{
		{"node:20-slim", true},
		{"golang:1.24-alpine", true},
		{"rust:1.85-slim", true},
		{"maven:3.9-eclipse-temurin-21", true},
		{"gradle:8.5-jdk21", true},
		{"php:8.3-cli", true},
		{"alpine:latest", true},
		{"python:3.12", true},
		{"ruby:3.3", true},
		{"ubuntu:latest", false},
		{"my-custom-image:latest", false},
		{"", false},
		{"malicious/node:latest", false},
	}

	for _, tt := range tests {
		got := isAllowedImage(tt.image)
		if got != tt.allowed {
			t.Errorf("isAllowedImage(%q) = %v, want %v", tt.image, got, tt.allowed)
		}
	}
}

func TestParsePlans_ValidResponse(t *testing.T) {
	p := &LLMPlanner{}
	raw := `{"response":"[{\"language\":\"typescript\",\"image\":\"node:20-slim\",\"script\":[\"sh\",\"-c\",\"echo test\"],\"output_format\":\"jest-summary\"}]"}`

	plans, err := p.parsePlans(raw, []report.Language{report.LanguageTypeScript})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("got %d plans, want 1", len(plans))
	}
	if plans[0].Image != "node:20-slim" {
		t.Errorf("image = %q, want %q", plans[0].Image, "node:20-slim")
	}
}

func TestParsePlans_DisallowedImage(t *testing.T) {
	p := &LLMPlanner{}
	raw := `{"response":"[{\"language\":\"typescript\",\"image\":\"evil:latest\",\"script\":[\"sh\",\"-c\",\"echo pwned\"],\"output_format\":\"jest-summary\"}]"}`

	_, err := p.parsePlans(raw, []report.Language{report.LanguageTypeScript})
	if err == nil {
		t.Fatal("expected error for disallowed image")
	}
}

func TestParsePlans_EmptyResponse(t *testing.T) {
	p := &LLMPlanner{}
	raw := `{"response":""}`
	_, err := p.parsePlans(raw, []report.Language{report.LanguageTypeScript})
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestParsePlans_SingleStringScript(t *testing.T) {
	p := &LLMPlanner{}
	raw := `{"response":"[{\"language\":\"go\",\"image\":\"golang:1.24-alpine\",\"script\":[\"echo test\"],\"output_format\":\"jest-summary\"}]"}`

	plans, err := p.parsePlans(raw, []report.Language{report.LanguageGo})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Single-element script should be wrapped in ["sh", "-c", ...]
	if len(plans[0].Script) != 3 || plans[0].Script[0] != "sh" {
		t.Errorf("script not wrapped: %v", plans[0].Script)
	}
}

func TestParsePlans_WrappedInPlansKey(t *testing.T) {
	p := &LLMPlanner{}
	raw := `{"response":"{\"plans\":[{\"language\":\"typescript\",\"image\":\"node:20-slim\",\"script\":[\"sh\",\"-c\",\"echo hi\"],\"output_format\":\"jest-summary\"}]}"}`

	plans, err := p.parsePlans(raw, []report.Language{report.LanguageTypeScript})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plans) != 1 {
		t.Fatalf("got %d plans, want 1", len(plans))
	}
}

func TestDefaultLLMConfig(t *testing.T) {
	cfg := DefaultLLMConfig()
	if cfg.Model != "qwen2.5:1.5b" {
		t.Errorf("Model = %q, want %q", cfg.Model, "qwen2.5:1.5b")
	}
	if cfg.OllamaImage != "ollama/ollama:latest" {
		t.Errorf("OllamaImage = %q, want %q", cfg.OllamaImage, "ollama/ollama:latest")
	}
}

func TestReadConfigFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/test\n\ngo 1.24\n"), 0600); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	p := &LLMPlanner{}
	configs := p.readConfigFiles(dir)
	if _, ok := configs["go.mod"]; !ok {
		t.Error("expected go.mod to be present in configs")
	}
}
