package coverage

import (
	"context"
	"fmt"
	"os"

	"github.com/ken-guru/fog-of-war-clearer/internal/planner"
	"github.com/ken-guru/fog-of-war-clearer/internal/runner"
	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

// Analyzer runs coverage analysis for one or more detected languages.
type Analyzer struct {
	runner *runner.Runner
}

// NewAnalyzer creates an Analyzer backed by the given Docker runner.
func NewAnalyzer(r *runner.Runner) *Analyzer {
	return &Analyzer{runner: r}
}

// Analyze runs test-coverage analysis for each of the given languages using the
// StaticPlanner defaults.  Kept for backward compatibility.
func (a *Analyzer) Analyze(ctx context.Context, repoDir string, languages []report.Language) ([]report.CoverageMetrics, error) {
	sp := &planner.StaticPlanner{}
	plans, err := sp.Plan(ctx, repoDir, languages)
	if err != nil {
		return nil, err
	}
	return a.AnalyzeWithPlans(ctx, repoDir, plans)
}

// AnalyzeWithPlans runs test-coverage analysis for each RunPlan.
func (a *Analyzer) AnalyzeWithPlans(ctx context.Context, repoDir string, plans []planner.RunPlan) ([]report.CoverageMetrics, error) {
	var results []report.CoverageMetrics

	for _, plan := range plans {
		fmt.Fprintf(os.Stderr, "[fog] analysing %s test coverage...\n", plan.Language)
		metrics, err := a.executePlan(ctx, repoDir, plan)
		if err != nil {
			return results, fmt.Errorf("coverage analysis for %s: %w", plan.Language, err)
		}
		fmt.Fprintf(os.Stderr, "[fog] %s coverage analysis complete\n", plan.Language)
		results = append(results, metrics)
	}
	return results, nil
}

func (a *Analyzer) executePlan(ctx context.Context, repoDir string, plan planner.RunPlan) (report.CoverageMetrics, error) {
	output, err := a.runner.Run(ctx, runner.RunOptions{
		Image:   plan.Image,
		Cmd:     plan.Script,
		RepoDir: repoDir,
	})
	if err != nil {
		return report.CoverageMetrics{}, fmt.Errorf("run container: %w", err)
	}

	switch plan.OutputFormat {
	case "jest-summary":
		return ParseJestSummary(plan.Language, output)
	case "jacoco-xml":
		return ParseJacocoXML(plan.Language, output)
	default:
		// Default to jest-summary for unknown formats.
		return ParseJestSummary(plan.Language, output)
	}
}
