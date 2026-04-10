// fog-of-war-clearer CLI – analyse a GitHub repository for security and quality
// metrics from the command line.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ken-guru/fog-of-war-clearer/internal/checker"
	"github.com/ken-guru/fog-of-war-clearer/internal/planner"
	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

func main() {
	// Try to load .env file if it exists.
	_ = loadEnvFile(".env")

	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// loadEnvFile reads a simple key=value .env file and sets environment variables.
// Lines starting with # are treated as comments. Empty lines are ignored.
func loadEnvFile(filename string) error {
	f, err := os.Open(filename)
	if err != nil {
		return nil // File doesn't exist, which is fine
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip empty lines and comments.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Parse key=value.
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			key := strings.TrimSpace(parts[0])
			val := strings.TrimSpace(parts[1])
			// Don't override existing environment variables.
			if os.Getenv(key) == "" {
				os.Setenv(key, val)
			}
		}
	}
	return scanner.Err()
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "fog-of-war-clearer",
		Short: "Analyse a GitHub repository for security and quality metrics",
		Long: `fog-of-war-clearer clones a GitHub repository and runs a configurable
set of checks — such as test-coverage analysis — in a sandboxed Docker
container.  Results are emitted as structured JSON.`,
	}

	root.AddCommand(newAnalyzeCmd())
	return root
}

func newAnalyzeCmd() *cobra.Command {
	var (
		pat    string
		repo   string
		checks []string
		output string
	)

	cmd := &cobra.Command{
		Use:   "analyze",
		Short: "Run analysis checks against a GitHub repository",
		Example: `  # Run default test-coverage check and save to results/owner-name_<timestamp>.json
  fog-of-war-clearer analyze --pat <PAT> --repo owner/name

  # Run test-coverage and save results to a custom file
  fog-of-war-clearer analyze --pat <PAT> --repo owner/name --output result.json

  # Specify checks explicitly
  fog-of-war-clearer analyze --pat <PAT> --repo owner/name --checks test-coverage`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Load defaults from environment variables if flags are not provided.
			if pat == "" {
				pat = os.Getenv("FOG_PAT")
			}
			if repo == "" {
				repo = os.Getenv("FOG_REPO")
			}
			if len(checks) == 1 && checks[0] == "test-coverage" {
				// Default check was not explicitly overridden
				if envChecks := os.Getenv("FOG_CHECKS"); envChecks != "" {
					checks = strings.Split(envChecks, ",")
				}
			}

			// Validate required arguments after loading from environment.
			if pat == "" {
				return fmt.Errorf("--pat or FOG_PAT environment variable is required")
			}
			if repo == "" {
				return fmt.Errorf("--repo or FOG_REPO environment variable is required")
			}

			return runAnalyze(cmd, pat, repo, checks, output)
		},
	}

	cmd.Flags().StringVar(&pat, "pat", "", "GitHub Personal Access Token for cloning the repository (can also use FOG_PAT env var)")
	cmd.Flags().StringVar(&repo, "repo", "", "Repository to analyse in owner/name format (can also use FOG_REPO env var)")
	cmd.Flags().StringSliceVar(&checks, "checks", []string{"test-coverage"}, "Comma-separated list of checks to run (can also use FOG_CHECKS env var)")
	cmd.Flags().StringVar(&output, "output", "", "Write JSON output to this file (default: results/<owner>-<repo>_<timestamp>.json)")

	return cmd
}

func runAnalyze(cmd *cobra.Command, pat, repo string, checkNames []string, outputFile string) error {
	// Resolve check types.
	checkTypes, err := parseChecks(checkNames)
	if err != nil {
		return err
	}

	// Build LLM config from environment variables.  If FOG_LLM_MODEL is set
	// (or defaulted), enable the LLM planner; otherwise use static fallback.
	var llmCfg *planner.LLMConfig
	if model := os.Getenv("FOG_LLM_MODEL"); model != "" {
		cfg := planner.LLMConfig{Model: model}
		if img := os.Getenv("FOG_LLM_OLLAMA_IMAGE"); img != "" {
			cfg.OllamaImage = img
		}
		llmCfg = &cfg
	}

	c, err := checker.New(llmCfg)
	if err != nil {
		return fmt.Errorf("initialise checker: %w", err)
	}

	rpt, err := c.Run(context.Background(), checker.Request{
		PAT:    pat,
		Repo:   repo,
		Checks: checkTypes,
	})
	if err != nil {
		// Sanitize error in case it accidentally contains the PAT.
		return fmt.Errorf("analysis failed: %s", strings.ReplaceAll(err.Error(), pat, "***"))
	}

	data, err := json.MarshalIndent(rpt, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	if outputFile == "" {
		// Generate default filename from repo (owner/repo-name → owner-repo-name_2026-04-10T11-57-43Z.json)
		// Include timestamp for time-series analysis. Place in results/ directory (gitignored).
		timestamp := time.Now().UTC().Format("2006-01-02T15-04-05Z")
		filename := strings.ToLower(strings.ReplaceAll(repo, "/", "-")) + "_" + timestamp + ".json"
		outputFile = filepath.Join("results", filename)
	}

	// Create results directory if it doesn't exist.
	if dir := filepath.Dir(outputFile); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create output directory: %w", err)
		}
	}

	if err := os.WriteFile(outputFile, data, 0o600); err != nil {
		return fmt.Errorf("write output file: %w", err)
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Results written to %s\n", outputFile)
	return nil
}

// parseChecks converts string check names to CheckType values and validates them.
func parseChecks(names []string) ([]report.CheckType, error) {
	supported := map[string]report.CheckType{
		"test-coverage": report.CheckTestCoverage,
	}
	var types []report.CheckType
	for _, name := range names {
		name = strings.TrimSpace(name)
		ct, ok := supported[name]
		if !ok {
			return nil, fmt.Errorf("unsupported check %q (supported: %s)", name, strings.Join(keys(supported), ", "))
		}
		types = append(types, ct)
	}
	return types, nil
}

func keys(m map[string]report.CheckType) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
