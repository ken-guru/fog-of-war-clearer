package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

const (
	// ollamaReadyTimeout is how long we wait for the Ollama server to become healthy.
	ollamaReadyTimeout = 120 * time.Second

	// plannerTimeout is the overall timeout for the planning stage.
	plannerTimeout = 5 * time.Minute

	// maxConfigFileSize is the maximum bytes read from any single repo config file.
	maxConfigFileSize = 8 * 1024
)

// allowedImagePrefixes is the set of Docker image prefixes the LLM is permitted
// to recommend. Any image outside this list is rejected and the static fallback
// is used instead.
var allowedImagePrefixes = []string{
	"node:",
	"golang:",
	"rust:",
	"openjdk:",
	"eclipse-temurin:",
	"maven:",
	"gradle:",
	"php:",
	"alpine:",
	"python:",
	"ruby:",
}

// configFiles are the repo files we look for and send to the LLM.
var configFiles = []string{
	"package.json",
	".nvmrc",
	".node-version",
	"go.mod",
	"Cargo.toml",
	"composer.json",
	"build.gradle",
	"build.gradle.kts",
	"pom.xml",
	"Makefile",
	"Dockerfile",
	".tool-versions",
}

// LLMPlanner inspects a repository via a containerised Ollama instance and
// returns tailored RunPlans.
type LLMPlanner struct {
	cli      *client.Client
	config   LLMConfig
	fallback *StaticPlanner
}

// NewLLMPlanner creates a planner that uses a containerised Ollama model.
// It falls back to the StaticPlanner on any error.
func NewLLMPlanner(cli *client.Client, cfg LLMConfig) *LLMPlanner {
	if cfg.Model == "" {
		cfg.Model = DefaultLLMConfig().Model
	}
	if cfg.OllamaImage == "" {
		cfg.OllamaImage = DefaultLLMConfig().OllamaImage
	}
	return &LLMPlanner{
		cli:      cli,
		config:   cfg,
		fallback: &StaticPlanner{},
	}
}

// Plan attempts LLM-based planning.  On any failure it logs a warning and
// delegates to the StaticPlanner.
func (p *LLMPlanner) Plan(ctx context.Context, repoDir string, languages []report.Language) ([]RunPlan, error) {
	plans, err := p.planWithLLM(ctx, repoDir, languages)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[fog] LLM planner failed (%v), using static fallback\n", err)
		return p.fallback.Plan(ctx, repoDir, languages)
	}
	return plans, nil
}

func (p *LLMPlanner) planWithLLM(ctx context.Context, repoDir string, languages []report.Language) ([]RunPlan, error) {
	ctx, cancel := context.WithTimeout(ctx, plannerTimeout)
	defer cancel()

	// 1. Create an ephemeral network.
	networkName := fmt.Sprintf("fog-planner-%d", time.Now().UnixNano())
	netResp, err := p.cli.NetworkCreate(ctx, networkName, network.CreateOptions{
		Driver: "bridge",
	})
	if err != nil {
		return nil, fmt.Errorf("create planner network: %w", err)
	}
	defer func() {
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer rmCancel()
		_ = p.cli.NetworkRemove(rmCtx, netResp.ID)
	}()

	// 2. Pull Ollama image if needed.
	if err := p.pullIfMissing(ctx, p.config.OllamaImage); err != nil {
		return nil, fmt.Errorf("pull ollama image: %w", err)
	}

	// 3. Start Ollama container on the ephemeral network.
	ollamaID, err := p.startOllama(ctx, netResp.ID)
	if err != nil {
		return nil, fmt.Errorf("start ollama: %w", err)
	}
	defer func() {
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer rmCancel()
		_ = p.cli.ContainerRemove(rmCtx, ollamaID, container.RemoveOptions{Force: true})
	}()

	// 4. Wait for Ollama to be ready; capture the tags response for cache check.
	tagsJSON, err := p.waitForOllama(ctx, ollamaID, netResp.ID)
	if err != nil {
		return nil, fmt.Errorf("ollama not ready: %w", err)
	}

	// 5. Pull the model only if not already present in the volume.
	if err := p.pullModel(ctx, ollamaID, netResp.ID, tagsJSON); err != nil {
		return nil, fmt.Errorf("pull model: %w", err)
	}

	// 6. Read repo config files from the host.
	configs := p.readConfigFiles(repoDir)

	// 7. Build the prompt.
	prompt := p.buildPrompt(languages, configs)

	// 8. Run the script container to query Ollama.
	output, err := p.queryOllama(ctx, repoDir, netResp.ID, prompt)
	if err != nil {
		return nil, fmt.Errorf("query ollama: %w", err)
	}

	// 9. Parse and validate the response.
	plans, err := p.parsePlans(output, languages)
	if err != nil {
		return nil, fmt.Errorf("parse LLM response: %w", err)
	}

	return plans, nil
}

// startOllama creates and starts the Ollama container.
func (p *LLMPlanner) startOllama(ctx context.Context, networkID string) (string, error) {
	cfg := &container.Config{
		Image: p.config.OllamaImage,
	}
	hostCfg := &container.HostConfig{
		Mounts: []mount.Mount{
			{
				Type:   mount.TypeVolume,
				Source: "fog-ollama-models",
				Target: "/root/.ollama",
			},
		},
		Resources: container.Resources{
			Memory:    4 * 1024 * 1024 * 1024, // 4 GiB for model inference
			CPUPeriod: 100_000,
			CPUQuota:  200_000, // 2 vCPUs
		},
	}

	resp, err := p.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("create ollama container: %w", err)
	}

	if err := p.cli.NetworkConnect(ctx, networkID, resp.ID, &network.EndpointSettings{
		Aliases: []string{"fog-ollama"},
	}); err != nil {
		_ = p.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("connect ollama container to network: %w", err)
	}

	if err := p.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = p.cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("start ollama container: %w", err)
	}

	fmt.Fprintf(os.Stderr, "[fog] started ollama container\n")
	return resp.ID, nil
}

// waitForOllama polls the Ollama API via a helper container until it responds.
// It returns the raw JSON body of the /api/tags response once Ollama is ready.
func (p *LLMPlanner) waitForOllama(ctx context.Context, ollamaID, networkID string) (string, error) {
	fmt.Fprintf(os.Stderr, "[fog] waiting for ollama to be ready...\n")

	deadline := time.Now().Add(ollamaReadyTimeout)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		output, err := p.runHelperContainer(ctx, networkID, []string{
			"sh", "-c",
			`wget -q -O- http://fog-ollama:11434/api/tags 2>/dev/null`,
		})
		if err == nil && strings.Contains(output, `"models"`) {
			fmt.Fprintf(os.Stderr, "[fog] ollama is ready\n")
			return output, nil
		}
		time.Sleep(2 * time.Second)
	}
	return "", fmt.Errorf("ollama did not become ready within %s", ollamaReadyTimeout)
}

// modelCached reports whether the named model is already present in the Ollama
// instance, based on the JSON body returned by /api/tags.
func modelCached(tagsJSON, model string) bool {
	var resp struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(tagsJSON), &resp); err != nil {
		return false
	}
	// Ollama may store the name with or without a tag suffix (e.g. "qwen2.5:1.5b").
	for _, m := range resp.Models {
		if m.Name == model || strings.HasPrefix(m.Name, model+":") {
			return true
		}
	}
	return false
}

// pullModel ensures the configured model is available in this Ollama instance.
// It checks /api/tags first; if the model is already cached it skips the pull
// entirely so that parallel fog runs can proceed without queuing.
func (p *LLMPlanner) pullModel(ctx context.Context, ollamaID, networkID, tagsJSON string) error {
	fmt.Fprintf(os.Stderr, "[fog] ensuring model %s is available...\n", p.config.Model)

	if modelCached(tagsJSON, p.config.Model) {
		fmt.Fprintf(os.Stderr, "[fog] model %s ready (cached)\n", p.config.Model)
		return nil
	}

	payload := fmt.Sprintf(`{"name":"%s"}`, p.config.Model)
	script := fmt.Sprintf(`wget -q -O- --post-data='%s' --header='Content-Type: application/json' http://fog-ollama:11434/api/pull 2>&1; echo`, payload)

	output, err := p.runHelperContainer(ctx, networkID, []string{"sh", "-c", script})
	if err != nil {
		return fmt.Errorf("pull model request: %w", err)
	}

	// Check for error in the streamed response.
	if strings.Contains(output, `"error"`) {
		return fmt.Errorf("model pull error: %s", output)
	}

	fmt.Fprintf(os.Stderr, "[fog] model %s ready\n", p.config.Model)
	return nil
}

// queryOllama runs the script container that sends the prompt to Ollama and
// returns the model's response.
func (p *LLMPlanner) queryOllama(ctx context.Context, repoDir, networkID, prompt string) (string, error) {
	fmt.Fprintf(os.Stderr, "[fog] querying LLM for analysis plan...\n")

	// Build the request payload as a Go struct and marshal to JSON so that
	// special characters (newlines, quotes, etc.) are properly escaped.
	type generateRequest struct {
		Model  string `json:"model"`
		Prompt string `json:"prompt"`
		Stream bool   `json:"stream"`
		Format string `json:"format"`
	}
	payloadBytes, err := json.Marshal(generateRequest{
		Model:  p.config.Model,
		Prompt: prompt,
		Stream: false,
		Format: "json",
	})
	if err != nil {
		return "", fmt.Errorf("marshal ollama request: %w", err)
	}

	// The JSON payload is embedded in a shell single-quoted string, so the
	// only character that needs escaping for the shell is a single quote.
	shellPayload := strings.ReplaceAll(string(payloadBytes), "'", `'\''`)

	// -T 300: allow up to 5 minutes for the model to generate a response.
	// Without an explicit timeout BusyBox wget uses a short default that fires
	// before qwen2.5:1.5b finishes generating, causing an empty response body.
	script := fmt.Sprintf(
		`wget -q -T 300 -O- --post-data='%s' --header='Content-Type: application/json' http://fog-ollama:11434/api/generate 2>/dev/null`,
		shellPayload,
	)

	output, err := p.runHelperContainer(ctx, networkID, []string{"sh", "-c", script})
	if err != nil {
		return "", fmt.Errorf("LLM query failed: %w", err)
	}

	return output, nil
}

// runHelperContainer runs a short-lived alpine container on the planner network.
func (p *LLMPlanner) runHelperContainer(ctx context.Context, networkID string, cmd []string) (string, error) {
	if err := p.pullIfMissing(ctx, "alpine:latest"); err != nil {
		return "", err
	}

	cfg := &container.Config{
		Image: "alpine:latest",
		Cmd:   cmd,
	}
	hostCfg := &container.HostConfig{
		Resources: container.Resources{
			Memory:    256 * 1024 * 1024, // 256 MiB
			CPUPeriod: 100_000,
			CPUQuota:  100_000, // 1 vCPU
		},
	}

	resp, err := p.cli.ContainerCreate(ctx, cfg, hostCfg, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("create helper container: %w", err)
	}
	defer func() {
		rmCtx, rmCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer rmCancel()
		_ = p.cli.ContainerRemove(rmCtx, resp.ID, container.RemoveOptions{Force: true})
	}()

	if err := p.cli.NetworkConnect(ctx, networkID, resp.ID, &network.EndpointSettings{
		Aliases: []string{"fog-helper"},
	}); err != nil {
		return "", fmt.Errorf("connect helper container to network: %w", err)
	}

	if err := p.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("start helper container: %w", err)
	}

	statusCh, errCh := p.cli.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		if err != nil {
			return "", fmt.Errorf("wait for helper: %w", err)
		}
	case status := <-statusCh:
		_ = status // we read output regardless of exit code
	case <-ctx.Done():
		return "", ctx.Err()
	}

	logReader, err := p.cli.ContainerLogs(ctx, resp.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("read helper logs: %w", err)
	}
	defer logReader.Close()

	var buf strings.Builder
	_, _ = stdcopy.StdCopy(&buf, &buf, logReader)
	return buf.String(), nil
}

// readConfigFiles reads well-known config files from the repo directory.
func (p *LLMPlanner) readConfigFiles(repoDir string) map[string]string {
	configs := make(map[string]string)
	for _, name := range configFiles {
		path := filepath.Join(repoDir, name)
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		content := string(data)
		if len(content) > maxConfigFileSize {
			content = content[:maxConfigFileSize]
		}
		configs[name] = content
	}
	return configs
}

// buildPrompt constructs the system + user prompt for the LLM.
func (p *LLMPlanner) buildPrompt(languages []report.Language, configs map[string]string) string {
	var b strings.Builder

	b.WriteString("You are a build system analyst. Given the detected languages and configuration files from a code repository, ")
	b.WriteString("determine the best Docker image and shell script to run test coverage for each language.\n\n")
	b.WriteString("RULES:\n")
	b.WriteString("1. Return ONLY valid JSON, no explanation.\n")
	b.WriteString("2. The JSON must be an array of objects with keys: language, image, script, output_format.\n")
	b.WriteString("3. 'image' must be an official Docker Hub image (e.g. node:20-slim, golang:1.24-alpine).\n")
	b.WriteString("4. 'script' must be a JSON array of command and arguments, e.g. [\"sh\",\"-c\",\"...\"].\n")
	b.WriteString("   The command must:\n")
	b.WriteString("   a. Copy /repo to /workspace: cp -r /repo/. /workspace/ && cd /workspace\n")
	b.WriteString("   b. Install dependencies.\n")
	b.WriteString("   c. Run tests with coverage.\n")
	b.WriteString("   d. Output '---COVERAGE_JSON---' followed by a JSON object with format: ")
	b.WriteString(`{"total":{"lines":{"pct":N},"statements":{"pct":N},"branches":{"pct":N},"functions":{"pct":N}}}`)
	b.WriteString("\n")
	b.WriteString("5. 'output_format' must be 'jest-summary' for JS/TS/Go/Rust/PHP or 'jacoco-xml' for Java/Kotlin.\n")
	b.WriteString("6. For npm projects, use --ignore-scripts for security.\n")
	b.WriteString("7. Detect the correct test runner from package.json (jest, vitest, mocha, etc.) and use version-appropriate flags.\n")
	b.WriteString("8. If vitest is detected, do NOT use --poolOptions flag — use 'npx vitest run --coverage --coverage.reporter=json-summary'.\n\n")

	b.WriteString("DETECTED LANGUAGES: ")
	for i, lang := range languages {
		if i > 0 {
			b.WriteString(", ")
		}
		b.WriteString(string(lang))
	}
	b.WriteString("\n\n")

	b.WriteString("REPOSITORY CONFIG FILES:\n")
	for name, content := range configs {
		b.WriteString(fmt.Sprintf("--- %s ---\n%s\n\n", name, content))
	}

	return b.String()
}

// parsePlans parses the LLM JSON response and validates the plans.
func (p *LLMPlanner) parsePlans(raw string, languages []report.Language) ([]RunPlan, error) {
	// The Ollama /api/generate response wraps the model output in a JSON envelope.
	var envelope struct {
		Response string `json:"response"`
	}
	if err := json.Unmarshal([]byte(raw), &envelope); err != nil {
		return nil, fmt.Errorf("parse ollama envelope: %w (raw: %.200s)", err, raw)
	}

	responseBody := strings.TrimSpace(envelope.Response)
	if responseBody == "" {
		return nil, fmt.Errorf("empty LLM response")
	}

	var plans []RunPlan
	if err := json.Unmarshal([]byte(responseBody), &plans); err != nil {
		// Try unwrapping if the LLM returned an object with a "plans" key.
		var wrapper struct {
			Plans []RunPlan `json:"plans"`
		}
		if err2 := json.Unmarshal([]byte(responseBody), &wrapper); err2 != nil {
			return nil, fmt.Errorf("parse LLM plans: %w (response: %.300s)", err, responseBody)
		}
		plans = wrapper.Plans
	}

	if len(plans) == 0 {
		return nil, fmt.Errorf("LLM returned no plans")
	}

	// Build a map of requested languages for O(1) lookup.
	langRequested := make(map[report.Language]bool, len(languages))
	for _, lang := range languages {
		langRequested[lang] = false // false = not yet seen
	}

	// Validate each plan: allowed image, non-empty script, and valid output_format.
	validFormats := map[string]bool{"jest-summary": true, "jacoco-xml": true}
	for i := range plans {
		if !isAllowedImage(plans[i].Image) {
			return nil, fmt.Errorf("LLM suggested disallowed image %q for %s", plans[i].Image, plans[i].Language)
		}
		if len(plans[i].Script) == 0 {
			return nil, fmt.Errorf("LLM returned empty script for %s", plans[i].Language)
		}
		// If the LLM returned the script as a single string, wrap it.
		if len(plans[i].Script) == 1 {
			plans[i].Script = []string{"sh", "-c", plans[i].Script[0]}
		}
		if !validFormats[plans[i].OutputFormat] {
			return nil, fmt.Errorf("LLM returned invalid output_format %q for %s", plans[i].OutputFormat, plans[i].Language)
		}
		// Check for unexpected or duplicate languages.
		seen, exists := langRequested[plans[i].Language]
		if !exists {
			return nil, fmt.Errorf("LLM returned plan for unexpected language %s", plans[i].Language)
		}
		if seen {
			return nil, fmt.Errorf("LLM returned duplicate plan for language %s", plans[i].Language)
		}
		langRequested[plans[i].Language] = true
	}

	// Ensure every requested language has a plan.
	for lang, seen := range langRequested {
		if !seen {
			return nil, fmt.Errorf("LLM returned no plan for language %s", lang)
		}
	}

	return plans, nil
}

// isAllowedImage checks an image name against the allowlist.
func isAllowedImage(img string) bool {
	for _, prefix := range allowedImagePrefixes {
		if strings.HasPrefix(img, prefix) {
			return true
		}
	}
	return false
}

// pullIfMissing pulls an image if it is not present locally.
func (p *LLMPlanner) pullIfMissing(ctx context.Context, imageName string) error {
	images, err := p.cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return fmt.Errorf("list images: %w", err)
	}
	for _, img := range images {
		for _, tag := range img.RepoTags {
			if tag == imageName {
				return nil
			}
		}
	}

	fmt.Fprintf(os.Stderr, "[fog] pulling image %s...\n", imageName)
	out, err := p.cli.ImagePull(ctx, imageName, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %s: %w", imageName, err)
	}
	defer out.Close()
	_, _ = io.Copy(io.Discard, out)
	fmt.Fprintf(os.Stderr, "[fog] image %s ready\n", imageName)
	return nil
}
