package main

import (
	"strings"
	"testing"

	"github.com/ken-guru/fog-of-war-clearer/pkg/report"
)

func TestParseChecks_Defaults(t *testing.T) {
	types, err := parseChecks([]string{"test-coverage"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 1 || types[0] != report.CheckTestCoverage {
		t.Errorf("got %v, want [test-coverage]", types)
	}
}

func TestParseChecks_InvalidCheck(t *testing.T) {
	_, err := parseChecks([]string{"unknown-check"})
	if err == nil {
		t.Fatal("expected error for unsupported check")
	}
	if !strings.Contains(err.Error(), "unsupported check") {
		t.Errorf("error message should mention 'unsupported check': %v", err)
	}
}

func TestParseChecks_Whitespace(t *testing.T) {
	types, err := parseChecks([]string{"  test-coverage  "})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(types) != 1 || types[0] != report.CheckTestCoverage {
		t.Errorf("got %v, want [test-coverage]", types)
	}
}

func TestRootCmd_Help(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetArgs([]string{"--help"})
	// Help should not return a non-zero exit code when executed via Execute.
	// We just confirm the command was created without panicking.
	if cmd.Use != "fog-of-war-clearer" {
		t.Errorf("unexpected command name: %s", cmd.Use)
	}
}

func TestAnalyzeCmd_RequiresFlags(t *testing.T) {
	cmd := newRootCmd()
	// Run without required flags – should fail
	cmd.SetArgs([]string{"analyze"})
	err := cmd.Execute()
	if err == nil {
		t.Fatal("expected error when required flags are missing")
	}
}
