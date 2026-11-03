package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveAndLoadChoicePreservesASelectedCustomModel protects remembered custom routing.
func TestSaveAndLoadChoicePreservesASelectedCustomModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings", "choice.json")
	want := AgentChoice{Agent: "pi", Provider: customProviderID, CustomBaseURL: "http://localhost:11434", CustomRenderer: RendererOpenAI, CustomModel: "qwen-coder"}
	if err := saveChoice(path, want); err != nil {
		t.Fatalf("saveChoice() error = %v", err)
	}
	got, found, err := loadChoice(path)
	if err != nil {
		t.Fatalf("loadChoice() error = %v", err)
	}
	if !found {
		t.Fatal("loadChoice() found = false, want true")
	}
	if got.Agent != want.Agent || got.Provider != want.Provider || got.CustomModel != want.CustomModel {
		t.Errorf("loaded choice = %#v, want agent/provider/model %q/%q/%q", got, want.Agent, want.Provider, want.CustomModel)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat choice: %v", err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Errorf("choice permissions = %o, want no group or world access", info.Mode().Perm())
	}
}

// TestLoadChoiceIgnoresMissingAndMalformedFiles lets the wizard recover from stale state.
func TestLoadChoiceIgnoresMissingAndMalformedFiles(t *testing.T) {
	for _, path := range []string{filepath.Join(t.TempDir(), "missing.json")} {
		if _, found, err := loadChoice(path); err != nil || found {
			t.Errorf("loadChoice(missing) = found %t, error %v; want false, nil", found, err)
		}
	}
	malformed := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(malformed, []byte("{"), 0o600); err != nil {
		t.Fatalf("write malformed choice: %v", err)
	}
	if _, found, err := loadChoice(malformed); err != nil || found {
		t.Errorf("loadChoice(malformed) = found %t, error %v; want false, nil", found, err)
	}
}

// TestAskChoiceBuildsClaudeCodeSubscriptionRouteUsesTheDocumentedSSOSetup.
func TestAskChoiceBuildsClaudeCodeSubscriptionRouteUsesTheDocumentedSSOSetup(t *testing.T) {
	input := bufio.NewReader(strings.NewReader("1\n1\ny\n"))
	var output strings.Builder
	choice, remember, err := askChoice(t.Context(), input, &output)
	if err != nil {
		t.Fatalf("askChoice() error = %v", err)
	}
	if choice.Agent != agentClaudeCodeID || choice.Provider != string(RendererAnthropic) || !remember {
		t.Errorf("askChoice() = %#v, remember %t; want Claude Code / Anthropic / remembered", choice, remember)
	}
}

// TestAskChoiceOffersPiGitHubCopilotMakesTheProviderSelectableInTheWizard.
func TestAskChoiceOffersPiGitHubCopilotMakesTheProviderSelectableInTheWizard(t *testing.T) {
	input := bufio.NewReader(strings.NewReader("5\n2\ny\n"))
	var output strings.Builder
	choice, remember, err := askChoice(t.Context(), input, &output)
	if err != nil {
		t.Fatalf("askChoice() error = %v", err)
	}
	if choice.Agent != agentPiID || choice.Provider != "github-copilot" || !remember {
		t.Errorf("askChoice() = %#v, remember %t; want Pi / GitHub Copilot / remembered", choice, remember)
	}
	if !strings.Contains(output.String(), "GitHub Copilot subscription") {
		t.Errorf("wizard output = %q, want GitHub Copilot subscription option", output.String())
	}
}

// TestAskChoiceDiscoversTheOnlyCustomPiModelAvoidsAnUnnecessarySelectionPrompt.
func TestAskChoiceDiscoversTheOnlyCustomPiModelAvoidsAnUnnecessarySelectionPrompt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/models" {
			t.Errorf("model discovery path = %q, want /v1/models", req.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "local-qwen"}}})
	}))
	defer server.Close()
	input := bufio.NewReader(strings.NewReader("5\n5\n" + server.URL + "\n1\ny\n"))
	var output strings.Builder
	choice, remember, err := askChoice(t.Context(), input, &output)
	if err != nil {
		t.Fatalf("askChoice() error = %v", err)
	}
	if choice.Agent != "pi" || choice.Provider != customProviderID || choice.CustomModel != "local-qwen" || !remember {
		t.Errorf("askChoice() = %#v, remember %t; want custom Pi route with discovered model", choice, remember)
	}
}

// TestClearChoiceRemovesRememberedStateLetsForceStartFresh.
func TestClearChoiceRemovesRememberedStateLetsForceStartFresh(t *testing.T) {
	path := filepath.Join(t.TempDir(), "choice.json")
	if err := saveChoice(path, AgentChoice{Agent: "pi"}); err != nil {
		t.Fatalf("saveChoice() error = %v", err)
	}
	if err := clearChoice(path); err != nil {
		t.Fatalf("clearChoice() error = %v", err)
	}
	if _, found, err := loadChoice(path); err != nil || found {
		t.Errorf("loadChoice after clear = found %t, error %v; want false, nil", found, err)
	}
}

// TestPromptSelectionStopsWhenContextIsCancelledAvoidsBlockingAfterShutdown.
func TestPromptSelectionStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := promptSelection(ctx, bufio.NewReader(strings.NewReader("1\n")), &strings.Builder{}, []string{"only"})
	if err == nil {
		t.Fatal("promptSelection() error = nil, want canceled context error")
	}
}
