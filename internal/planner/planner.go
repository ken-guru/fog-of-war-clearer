// Package planner determines Docker images and shell scripts for coverage
// analysis of a repository.  An LLM-backed planner inspects repo config files
// to produce a tailored RunPlan; a static planner provides deterministic
// fallback defaults.
package planner

import (
	"context"

	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

// RunPlan describes how to run coverage analysis for a single language.
type RunPlan struct {
	// Language being analysed.
	Language report.Language `json:"language"`

	// Image is the Docker image to use (e.g. "node:20-slim").
	Image string `json:"image"`

	// Script is the command passed to container.Config.Cmd.
	// Typically: ["sh", "-c", "<shell script>"].
	Script []string `json:"script"`

	// OutputFormat tells the analyser how to parse the container output.
	// Supported values: "jest-summary", "jacoco-xml".
	OutputFormat string `json:"output_format"`
}

// LLMConfig holds the settings for the containerised Ollama-based planner.
type LLMConfig struct {
	// Model is the Ollama model tag (default: "qwen2.5:1.5b").
	Model string

	// OllamaImage is the Docker image for Ollama (default: "ollama/ollama:latest").
	OllamaImage string
}

// DefaultLLMConfig returns the default LLM planner configuration.
func DefaultLLMConfig() LLMConfig {
	return LLMConfig{
		Model:       "qwen2.5:1.5b",
		OllamaImage: "ollama/ollama:latest",
	}
}

// Planner produces RunPlans for a set of detected languages by inspecting the
// repository contents.
type Planner interface {
	// Plan returns a RunPlan for each of the given languages.
	Plan(ctx context.Context, repoDir string, languages []report.Language) ([]RunPlan, error)
}
