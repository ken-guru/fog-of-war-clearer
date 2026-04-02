// Package api provides the HTTP handlers for the fog-of-war-clearer REST API.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/ken-guru/fog-of-war-clearer/internal/checker"
	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

// analyzeRequest is the JSON body accepted by POST /api/v1/analyze.
type analyzeRequest struct {
	PAT    string             `json:"pat"`
	Repo   string             `json:"repo"`
	Checks []report.CheckType `json:"checks,omitempty"`
}

// errorResponse is the JSON body returned on error.
type errorResponse struct {
	Error string `json:"error"`
}

// checkerRunner is satisfied by *checker.Checker and by test doubles.
type checkerRunner interface {
	Run(ctx context.Context, req checker.Request) (*report.Report, error)
}

// Handler holds the dependencies required by the API handlers.
type Handler struct {
	checker checkerRunner
}

// NewHandler creates a Handler backed by the given Checker.
func NewHandler(c *checker.Checker) *Handler {
	return &Handler{checker: c}
}

// RegisterRoutes attaches the API routes to mux.
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/api/v1/analyze", h.handleAnalyze)
	mux.HandleFunc("/healthz", handleHealth)
}

// handleAnalyze handles POST /api/v1/analyze.
//
// Security notes:
//   - Only POST requests are accepted.
//   - The request body is limited in size to prevent DoS attacks.
//   - The PAT from the request is never written to response bodies or logs.
func (h *Handler) handleAnalyze(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	// Limit request body size to 1 MiB.
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)

	var req analyzeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+sanitizeErrorMessage(err.Error()))
		return
	}

	if req.PAT == "" {
		writeError(w, http.StatusBadRequest, "pat is required")
		return
	}
	if req.Repo == "" {
		writeError(w, http.StatusBadRequest, "repo is required")
		return
	}

	rpt, err := h.checker.Run(r.Context(), checker.Request{
		PAT:    req.PAT,
		Repo:   req.Repo,
		Checks: req.Checks,
	})
	if err != nil {
		// Sanitize error messages to ensure the PAT is not echoed back.
		msg := strings.ReplaceAll(err.Error(), req.PAT, "***")
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("analysis failed: %s", msg))
		return
	}

	writeJSON(w, http.StatusOK, rpt)
}

// handleHealth handles GET /healthz.
func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// writeJSON encodes v as JSON and writes it to w with the given status code.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError writes a JSON error response.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// sanitizeErrorMessage removes non-printable or control characters from error
// messages that will be included in API responses, to guard against log injection.
func sanitizeErrorMessage(msg string) string {
	var b strings.Builder
	for _, r := range msg {
		if r >= 0x20 && r != 0x7F {
			b.WriteRune(r)
		}
	}
	return b.String()
}
