package main

import (
	"strings"
	"testing"
)

// TestListAgentsPutsUnsupportedHarnessesLastKeepsUsableRoutesEasyToFind.
func TestListAgentsPutsUnsupportedHarnessesLastKeepsUsableRoutesEasyToFind(t *testing.T) {
	agents := ListAgents()
	if len(agents) < 2 {
		t.Fatalf("agent choices = %d, want supported and unsupported choices", len(agents))
	}
	firstUnsupported := -1
	for index, agent := range agents {
		if !agent.Supported && firstUnsupported == -1 {
			firstUnsupported = index
		}
		if agent.Supported && firstUnsupported != -1 {
			t.Errorf("supported harness %q appears after an unsupported harness", agent.ID)
		}
	}
	if firstUnsupported == -1 {
		t.Fatal("no unsupported harnesses appear in the choices")
	}
}

// TestResolveChoiceQuotesCodexBaseURLKeepsTheTOMLOverrideValid.
func TestResolveChoiceQuotesCodexBaseURLKeepsTheTOMLOverrideValid(t *testing.T) {
	target, err := ResolveChoice(AgentChoice{Agent: "codex", Provider: "chatgpt"}, 8787)
	if err != nil {
		t.Fatalf("ResolveChoice() error = %v", err)
	}
	if !strings.Contains(target.Command, `'openai_base_url="http://localhost:8787/backend-api/codex"'`) {
		t.Errorf("command %q does not preserve the TOML string value", target.Command)
	}
}

// TestResolveCustomPiKeepsTheTypedPathAndModelProvesCustomRoutesTargetTheIntendedServer.
func TestResolveCustomPiKeepsTheTypedPathAndModelProvesCustomRoutesTargetTheIntendedServer(t *testing.T) {
	target, err := ResolveChoice(AgentChoice{
		Agent: "pi", Provider: customProviderID, CustomBaseURL: "https://models.example/v2/",
		CustomRenderer: RendererAnthropic, CustomModel: "claude-local",
	}, 9000)
	if err != nil {
		t.Fatalf("ResolveChoice() error = %v", err)
	}
	if target.UpstreamBaseURL != "https://models.example/v2" {
		t.Errorf("upstream base URL = %q, want path prefix preserved without trailing slash", target.UpstreamBaseURL)
	}
	if target.BaseURL != "http://localhost:9000" {
		t.Errorf("local base URL = %q, want http://localhost:9000", target.BaseURL)
	}
	if len(target.Setup) != 1 || !strings.Contains(target.Setup[0].Body, `"id": "claude-local"`) {
		t.Errorf("Pi setup does not declare the selected model: %#v", target.Setup)
	}
}

// TestResolveCustomPiRequiresAModelPreventsBuiltInModelNamesFromLeakingToCustomServers.
func TestResolveCustomPiRequiresAModelPreventsBuiltInModelNamesFromLeakingToCustomServers(t *testing.T) {
	_, err := ResolveChoice(AgentChoice{
		Agent: "pi", Provider: customProviderID, CustomBaseURL: "http://localhost:11434",
		CustomRenderer: RendererOpenAI,
	}, 8787)
	if err == nil {
		t.Fatal("ResolveChoice() error = nil, want missing custom model error")
	}
	if !strings.Contains(err.Error(), "model ID") {
		t.Errorf("error = %q, want missing model ID detail", err)
	}
}

// TestResolveChoiceRejectsInvalidPortPreventsAnUnbindableProxyConfiguration.
func TestResolveChoiceRejectsInvalidPortPreventsAnUnbindableProxyConfiguration(t *testing.T) {
	_, err := ResolveChoice(AgentChoice{Agent: "pi", Provider: "anthropic"}, 70000)
	if err == nil {
		t.Fatal("ResolveChoice() error = nil, want invalid port error")
	}
}

// TestResolveChoiceRejectsUnknownAndUnloggableHarnessesKeepsTheWizardHonest.
func TestResolveChoiceRejectsUnknownAndUnloggableHarnessesKeepsTheWizardHonest(t *testing.T) {
	testCases := []AgentChoice{
		{Agent: "made-up-agent"},
		{Agent: "cursor"},
	}
	for _, choice := range testCases {
		if _, err := ResolveChoice(choice, 8787); err == nil {
			t.Errorf("ResolveChoice(%q) error = nil, want an unsupported choice error", choice.Agent)
		}
	}
}

// TestResolveCustomChoiceRejectsEmbeddedCredentialsPreventsAccidentalSecretStorage.
func TestResolveCustomChoiceRejectsEmbeddedCredentialsPreventsAccidentalSecretStorage(t *testing.T) {
	_, err := ResolveChoice(AgentChoice{
		Agent: "claude-code", Provider: customProviderID,
		CustomBaseURL: "https://user:secret@models.example/v1", CustomRenderer: RendererAnthropic,
	}, 8787)
	if err == nil {
		t.Fatal("ResolveChoice() error = nil, want credentials-in-URL error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("error %q exposed the URL credential", err)
	}
}
