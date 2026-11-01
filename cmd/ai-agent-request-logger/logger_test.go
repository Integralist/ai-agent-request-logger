package main

import (
	"strings"
	"testing"
)

// TestShouldLogRequest keeps housekeeping calls out of model-turn captures.
func TestShouldLogRequest(t *testing.T) {
	testCases := []struct {
		name        string
		method      string
		requestPath string
		renderer    Renderer
		want        bool
	}{
		{name: "logs an Anthropic turn", method: "POST", requestPath: "/v1/messages", renderer: RendererAnthropic, want: true},
		{name: "skips Anthropic token counting", method: "POST", requestPath: "/v1/messages/count_tokens", renderer: RendererAnthropic, want: false},
		{name: "skips Gemini metadata requests", method: "POST", requestPath: "/v1/models/gemini-pro:countTokens", renderer: RendererGemini, want: false},
		{name: "logs Gemini content generation", method: "POST", requestPath: "/v1/models/gemini-pro:streamGenerateContent", renderer: RendererGemini, want: true},
		{name: "skips non-POST probes", method: "GET", requestPath: "/v1/messages", renderer: RendererAnthropic, want: false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldLogRequest(tc.method, tc.requestPath, tc.renderer); got != tc.want {
				t.Errorf("ShouldLogRequest() = %t, want %t", got, tc.want)
			}
		})
	}
}

// TestResolveChoiceBuildsClaudeCodeSSOCommand keeps the subscription-login route on Anthropic's API.
func TestResolveChoiceBuildsClaudeCodeSSOCommand(t *testing.T) {
	target, err := ResolveChoice(AgentChoice{Agent: "claude-code", Provider: "anthropic"}, 8787)
	if err != nil {
		t.Fatalf("ResolveChoice() error = %v", err)
	}
	if target.UpstreamHost != "api.anthropic.com" {
		t.Errorf("upstream host = %q, want api.anthropic.com", target.UpstreamHost)
	}
	if !strings.Contains(target.Command, "ANTHROPIC_BASE_URL='http://localhost:8787'") {
		t.Errorf("command %q does not route Claude Code through the local proxy", target.Command)
	}
	if !strings.Contains(target.Command, "ENABLE_TOOL_SEARCH='true'") {
		t.Errorf("command %q does not preserve Claude Code's tool-search behavior", target.Command)
	}
}

// TestResolveChoiceConfiguresPiCodexForHTTPLogging requires Pi's Codex route to use SSE.
func TestResolveChoiceConfiguresPiCodexForHTTPLogging(t *testing.T) {
	target, err := ResolveChoice(AgentChoice{Agent: "pi", Provider: "codex"}, 8787)
	if err != nil {
		t.Fatalf("ResolveChoice() error = %v", err)
	}
	if target.BaseURL != "http://localhost:8787/backend-api" {
		t.Errorf("base URL = %q, want http://localhost:8787/backend-api", target.BaseURL)
	}
	if len(target.Setup) != 2 {
		t.Fatalf("setup files = %d, want 2", len(target.Setup))
	}
	if !strings.Contains(target.Setup[1].Body, `"transport": "sse"`) {
		t.Errorf("Pi settings = %q, want SSE transport", target.Setup[1].Body)
	}
}
