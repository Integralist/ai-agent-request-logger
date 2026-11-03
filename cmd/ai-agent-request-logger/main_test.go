package main

import (
	"net"
	"strconv"
	"strings"
	"testing"
)

// TestRunHelpDisplaysUsageWithoutStartingTheWizardMakesTheCLIDiscoverable.
func TestRunHelpDisplaysUsageWithoutStartingTheWizardMakesTheCLIDiscoverable(t *testing.T) {
	var output strings.Builder
	var errorOutput strings.Builder
	err := run(t.Context(), runParams{
		args: []string{"--help"}, in: strings.NewReader(""),
		out: &output, errOut: &errorOutput,
	})
	if err != nil {
		t.Fatalf("run(--help) error = %v, want nil", err)
	}
	if !strings.Contains(errorOutput.String(), "Usage of ai-agent-request-logger") {
		t.Errorf("help output = %q, want usage text", errorOutput.String())
	}
}

// TestRunStartsFirstTimePiChoiceUsesTheSelectionInsteadOfAnEmptyOuterValue.
func TestRunStartsFirstTimePiChoiceUsesTheSelectionInsteadOfAnEmptyOuterValue(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve listener port: %v", err)
	}
	defer listener.Close()
	_, portValue, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("parse listener port: %v", err)
	}
	port, err := strconv.Atoi(portValue)
	if err != nil {
		t.Fatalf("convert listener port: %v", err)
	}

	var output strings.Builder
	var errorOutput strings.Builder
	err = run(t.Context(), runParams{
		args: []string{"--force", "--port", strconv.Itoa(port)}, in: strings.NewReader("5\n2\ny\n"),
		out: &output, errOut: &errorOutput,
	})
	if !strings.Contains(output.String(), "Agent:     Pi (OpenAI API key)") {
		t.Fatalf("output = %q, want startup banner for selected Pi OpenAI route", output.String())
	}
	if err == nil || !strings.Contains(err.Error(), "proxy: listen:") {
		t.Errorf("run() error = %v, want the expected occupied-port listener error", err)
	}
}

// TestRunReasksAfterAStaleSavedChoiceAvoidsLeavingUsersWithAnObsoleteConfig.
func TestRunReasksAfterAStaleSavedChoiceAvoidsLeavingUsersWithAnObsoleteConfig(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path, err := choicePath()
	if err != nil {
		t.Fatalf("choicePath() error = %v", err)
	}
	if err := saveChoice(path, AgentChoice{Agent: "removed-harness"}); err != nil {
		t.Fatalf("saveChoice() error = %v", err)
	}
	var output strings.Builder
	var errorOutput strings.Builder
	err = run(t.Context(), runParams{
		args: []string{"--port", "8787"}, in: strings.NewReader("10\n"),
		out: &output, errOut: &errorOutput,
	})
	if err != nil {
		t.Fatalf("run() error = %v", err)
	}
	if !strings.Contains(output.String(), "Cursor CLI cannot be logged") {
		t.Errorf("output = %q, want wizard to replace the stale selection", output.String())
	}
}
