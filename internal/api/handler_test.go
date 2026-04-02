package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ken-guru/fog-of-war-clearer/internal/checker"
	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)


func TestHandleAnalyze_MethodNotAllowed(t *testing.T) {
	h := &Handler{} // checker not needed for this path
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/analyze", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}

func TestHandleAnalyze_MissingPAT(t *testing.T) {
	h := &Handler{}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"repo":"owner/repo"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	assertBodyContains(t, rec.Body.String(), "pat is required")
}

func TestHandleAnalyze_MissingRepo(t *testing.T) {
	h := &Handler{}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body := `{"pat":"token123"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
	assertBodyContains(t, rec.Body.String(), "repo is required")
}

func TestHandleAnalyze_InvalidJSON(t *testing.T) {
	h := &Handler{}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", strings.NewReader("not json"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleHealth(t *testing.T) {
	h := &Handler{}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}
	assertBodyContains(t, rec.Body.String(), "ok")
}

func TestHandleAnalyze_PATNotLeakedInErrorResponse(t *testing.T) {
	// Simulate a checker that returns an error message containing the PAT.
	secretPAT := "supersecretpat12345"

	h := &Handler{
		checker: &fakeChecker{
			err: &errWithPAT{pat: secretPAT},
		},
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body, _ := json.Marshal(analyzeRequest{
		PAT:  secretPAT,
		Repo: "owner/repo",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusInternalServerError)
	}

	respBody := rec.Body.String()
	if strings.Contains(respBody, secretPAT) {
		t.Errorf("response body contains the PAT – must be sanitised: %s", respBody)
	}
}

func TestHandleAnalyze_SuccessResponseStructure(t *testing.T) {
	expectedReport := &report.Report{
		Repo:  "owner/repo",
		RunAt: time.Now().UTC(),
		Checks: []report.CheckResult{
			{
				Type:   report.CheckTestCoverage,
				Status: report.CheckStatusSuccess,
				Coverage: []report.CoverageMetrics{
					{Language: report.LanguageTypeScript, Lines: 90.0},
				},
			},
		},
	}

	h := &Handler{
		checker: &fakeChecker{report: expectedReport},
	}
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)

	body, _ := json.Marshal(analyzeRequest{
		PAT:  "token",
		Repo: "owner/repo",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/analyze", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("status: got %d, want %d", rec.Code, http.StatusOK)
	}

	var got report.Report
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got.Repo != expectedReport.Repo {
		t.Errorf("repo: got %q, want %q", got.Repo, expectedReport.Repo)
	}
	if len(got.Checks) != 1 {
		t.Errorf("checks length: got %d, want 1", len(got.Checks))
	}
}

func TestSanitizeErrorMessage(t *testing.T) {
	msg := "error: some\x00null\x01byte\x1finjection"
	got := sanitizeErrorMessage(msg)
	for _, r := range got {
		if r < 0x20 || r == 0x7F {
			t.Errorf("sanitizeErrorMessage left control char 0x%02X in output", r)
		}
	}
}

// helpers

func assertBodyContains(t *testing.T, body, substr string) {
	t.Helper()
	if !strings.Contains(body, substr) {
		t.Errorf("response body %q does not contain %q", body, substr)
	}
}

// fakeChecker is a test double for *checker.Checker.
type fakeChecker struct {
	report *report.Report
	err    error
}

func (f *fakeChecker) Run(_ context.Context, _ checker.Request) (*report.Report, error) {
	return f.report, f.err
}

// errWithPAT is an error whose message contains the PAT to test sanitisation.
type errWithPAT struct {
	pat string
}

func (e *errWithPAT) Error() string {
	return "clone failed: https://" + e.pat + "@github.com/owner/repo.git: 401"
}
