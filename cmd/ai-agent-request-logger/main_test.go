package main

import (
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
