// fog-of-war-clearer CLI – analyse a GitHub repository for security and quality
// metrics from the command line.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ken-guru/fog-of-war-clearer/internal/checker"
	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
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
		Example: `  # Run default test-coverage check and print JSON to stdout
  fog-of-war-clearer analyze --pat <PAT> --repo owner/name

  # Run test-coverage and save results to a file
  fog-of-war-clearer analyze --pat <PAT> --repo owner/name --output result.json

  # Specify checks explicitly
  fog-of-war-clearer analyze --pat <PAT> --repo owner/name --checks test-coverage`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runAnalyze(cmd, pat, repo, checks, output)
		},
	}

	cmd.Flags().StringVar(&pat, "pat", "", "GitHub Personal Access Token for cloning the repository (required)")
	cmd.Flags().StringVar(&repo, "repo", "", "Repository to analyse in owner/name format (required)")
	cmd.Flags().StringSliceVar(&checks, "checks", []string{"test-coverage"}, "Comma-separated list of checks to run")
	cmd.Flags().StringVar(&output, "output", "", "Write JSON output to this file (default: stdout)")

	_ = cmd.MarkFlagRequired("pat")
	_ = cmd.MarkFlagRequired("repo")

	return cmd
}

func runAnalyze(cmd *cobra.Command, pat, repo string, checkNames []string, outputFile string) error {
	// Resolve check types.
	checkTypes, err := parseChecks(checkNames)
	if err != nil {
		return err
	}

	c, err := checker.New()
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
		fmt.Fprintln(cmd.OutOrStdout(), string(data))
		return nil
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
