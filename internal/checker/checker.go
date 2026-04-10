// Package checker orchestrates the full analysis pipeline: cloning the repo,
// running the requested checks, and building the final Report.
package checker

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/ken-guru/fog-of-war-clearer/internal/coverage"
	"github.com/ken-guru/fog-of-war-clearer/internal/fetcher"
	"github.com/ken-guru/fog-of-war-clearer/internal/planner"
	"github.com/ken-guru/fog-of-war-clearer/internal/runner"
	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

// Request carries the inputs required to run an analysis.
// PAT is intentionally not present in the output Report to prevent leakage.
type Request struct {
	// PAT is the GitHub Personal Access Token used only for cloning.
	PAT string

	// Repo is the repository to analyse in "owner/name" format.
	Repo string

	// Checks is the list of checks to run.  If empty, CheckTestCoverage is used.
	Checks []report.CheckType
}

// Checker orchestrates the full analysis pipeline.
type Checker struct {
	fetcher  *fetcher.Fetcher
	runner   *runner.Runner
	analyzer *coverage.Analyzer
	planner  planner.Planner
}

// New creates a Checker.  If llmCfg is non-nil an LLM-backed planner is used;
// otherwise analysis falls back to deterministic static plans.
func New(llmCfg *planner.LLMConfig) (*Checker, error) {
	r, err := runner.New()
	if err != nil {
		return nil, fmt.Errorf("initialise docker runner: %w", err)
	}

	var p planner.Planner
	if llmCfg != nil {
		p = planner.NewLLMPlanner(r.Client(), *llmCfg)
	} else {
		p = &planner.StaticPlanner{}
	}

	return &Checker{
		fetcher:  fetcher.New(),
		runner:   r,
		analyzer: coverage.NewAnalyzer(r),
		planner:  p,
	}, nil
}

// Run executes the analysis described by req and returns a Report.  The Report
// never contains the PAT.
func (c *Checker) Run(ctx context.Context, req Request) (*report.Report, error) {
	checks := req.Checks
	if len(checks) == 0 {
		checks = []report.CheckType{report.CheckTestCoverage}
	}

	// Clone the repository.  The PAT is used here only; it is never forwarded
	// to the Docker containers.
	fmt.Fprintf(os.Stderr, "[fog] cloning %s...\n", req.Repo)
	repoDir, err := c.fetcher.Clone(req.PAT, req.Repo)
	if err != nil {
		return nil, fmt.Errorf("clone repo: %w", err)
	}
	defer os.RemoveAll(repoDir)
	fmt.Fprintf(os.Stderr, "[fog] clone complete\n")

	rpt := &report.Report{
		Repo:  req.Repo,
		RunAt: time.Now().UTC(),
	}

	for _, checkType := range checks {
		fmt.Fprintf(os.Stderr, "[fog] running check: %s\n", checkType)
		result := c.runCheck(ctx, checkType, repoDir)
		rpt.Checks = append(rpt.Checks, result)
	}

	return rpt, nil
}

func (c *Checker) runCheck(ctx context.Context, checkType report.CheckType, repoDir string) report.CheckResult {
	switch checkType {
	case report.CheckTestCoverage:
		return c.runCoverageCheck(ctx, repoDir)
	default:
		return report.CheckResult{
			Type:   checkType,
			Status: report.CheckStatusSkipped,
			Error:  fmt.Sprintf("unsupported check type: %s", checkType),
		}
	}
}

func (c *Checker) runCoverageCheck(ctx context.Context, repoDir string) report.CheckResult {
	result := report.CheckResult{Type: report.CheckTestCoverage}

	fmt.Fprintf(os.Stderr, "[fog] detecting languages...\n")
	langs := coverage.DetectLanguages(repoDir)
	if len(langs) == 0 {
		fmt.Fprintf(os.Stderr, "[fog] no supported languages detected\n")
		result.Status = report.CheckStatusSkipped
		result.Error = "no supported languages detected in repository"
		return result
	}
	fmt.Fprintf(os.Stderr, "[fog] detected languages: %v\n", langs)

	fmt.Fprintf(os.Stderr, "[fog] planning container configuration...\n")
	plans, err := c.planner.Plan(ctx, repoDir, langs)
	if err != nil {
		result.Status = report.CheckStatusFailure
		result.Error = fmt.Sprintf("planning failed: %s", err)
		return result
	}

	metrics, err := c.analyzer.AnalyzeWithPlans(ctx, repoDir, plans)
	if err != nil {
		result.Status = report.CheckStatusFailure
		result.Error = err.Error()
		return result
	}

	result.Status = report.CheckStatusSuccess
	result.Coverage = metrics
	return result
}
