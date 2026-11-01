package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
	"slices"
	"strings"
)

const (
	customProviderID = "custom"
	otherChoiceID    = "other"
	issueURL         = "https://github.com/ai-hero-dev/ai-coding-crash-course/issues/new"
)

// AgentSummary describes a harness option shown by the setup wizard.
type AgentSummary struct {
	// ID is the stable value stored in the user's configuration.
	ID string
	// Label is the name shown in the wizard.
	Label string
	// Supported reports whether requests from this harness can be logged.
	Supported bool
}

// environmentVariable is one shell assignment needed to route a harness through the proxy.
type environmentVariable struct {
	// name is the environment variable the harness reads.
	name string
	// value is a string that may contain the local base URL placeholder.
	value string
}

// providerEntry holds the routing and setup facts for one harness/provider pair.
type providerEntry struct {
	// id is the stable provider selection.
	id string
	// label is the name shown in the wizard.
	label string
	// host is the provider API hostname.
	host string
	// renderer describes the request protocol for readable captures.
	renderer Renderer
	// suffix is appended to the local proxy origin for this provider.
	suffix string
	// env lists the environment variables used to route the harness.
	env []environmentVariable
	// binary is the command that starts the harness.
	binary string
	// args are command-line arguments used for temporary routing overrides.
	args []string
	// setup contains configuration snippets required by the harness.
	setup []SetupFile
	// notes explains non-obvious setup behaviour.
	notes []string
	// warnings identifies limitations of this route.
	warnings []string
	// customFor lists wire formats this provider can represent for custom targets.
	customFor []Renderer
}

// agentEntry groups supported providers and known limitations by harness.
type agentEntry struct {
	// id is the stable harness selection.
	id string
	// label is the name shown in the wizard.
	label string
	// providers are tested ways to route this harness through the logger.
	providers []providerEntry
	// alwaysCustom means this harness has no fixed upstream provider.
	alwaysCustom bool
	// refusal explains why the harness cannot expose its model request.
	refusal string
}

// agentCatalogue defines each supported harness route in one place.
var agentCatalogue = buildAgentCatalogue()

// buildAgentCatalogue constructs the data-driven harness and provider list.
func buildAgentCatalogue() []agentEntry {
	return []agentEntry{
		{
			id: "claude-code", label: "Claude Code",
			providers: []providerEntry{
				{
					id: "anthropic", label: "Anthropic", host: "api.anthropic.com", renderer: RendererAnthropic,
					binary: "claude", env: []environmentVariable{{"ANTHROPIC_BASE_URL", "{baseUrl}"}, {"ENABLE_TOOL_SEARCH", "true"}},
					customFor: []Renderer{RendererAnthropic, RendererOpenAI, RendererRaw},
					notes: []string{
						"This route keeps your Claude subscription login; only model traffic goes through the local proxy.",
						"ENABLE_TOOL_SEARCH=true keeps Claude Code's tool definitions close to the normal request shape.",
					},
				},
				{
					id: "vertex", label: "Google Vertex AI", host: "aiplatform.googleapis.com", renderer: RendererAnthropic,
					binary: "claude", env: []environmentVariable{{"ANTHROPIC_VERTEX_BASE_URL", "{baseUrl}"}, {"ENABLE_TOOL_SEARCH", "true"}},
					notes:    []string{"Requires CLAUDE_CODE_USE_VERTEX=1 and the existing Vertex project and region environment variables. This route uses the global Vertex endpoint."},
					warnings: []string{"Regional Vertex endpoints are not handled by this route."},
				},
			},
		},
		{
			id: "codex", label: "Codex",
			providers: []providerEntry{
				{id: "chatgpt", label: "ChatGPT subscription", host: "chatgpt.com", renderer: RendererOpenAI, suffix: "/backend-api/codex", binary: "codex", args: []string{"-c", `openai_base_url="{baseUrl}"`}, notes: []string{"The -c flag changes this run only; it does not edit ~/.codex/config.toml."}},
				{id: "openai", label: "OpenAI API key", host: "api.openai.com", renderer: RendererOpenAI, suffix: "/v1", binary: "codex", args: []string{"-c", `openai_base_url="{baseUrl}"`}},
			},
		},
		{
			id: "copilot", label: "GitHub Copilot CLI",
			providers: []providerEntry{{id: "github", label: "GitHub subscription", host: "api.githubcopilot.com", renderer: RendererOpenAI, binary: "copilot", env: []environmentVariable{{"COPILOT_API_URL", "{baseUrl}"}}, warnings: []string{"Some models use WebSockets, which this HTTP proxy cannot capture."}}},
		},
		{
			id: "cursor", label: "Cursor CLI",
			refusal: "Cursor builds its system prompt and tool list on its servers. Those details never leave your machine in the model request, so a local proxy cannot capture them.",
		},
		{
			id: "opencode", label: "OpenCode",
			providers: []providerEntry{
				{
					id: "anthropic", label: "Anthropic", host: "api.anthropic.com", renderer: RendererAnthropic, suffix: "/v1", binary: "opencode", env: []environmentVariable{{"ANTHROPIC_BASE_URL", "{baseUrl}"}}, customFor: []Renderer{RendererAnthropic},
					setup: []SetupFile{{Path: "~/.config/opencode/opencode.json", Language: "json", Body: `{
  "$schema": "https://opencode.ai/config.json",
  "model": "anthropic/claude-sonnet-4-5",
  "small_model": "anthropic/claude-sonnet-4-5",
  "provider": {"anthropic": {"options": {"apiKey": "{env:ANTHROPIC_API_KEY}", "baseURL": "{baseUrl}"}}}
}`}},
					notes: []string{"OpenCode's durable provider configuration is in opencode.json; the environment variable is a fallback."},
				},
				{
					id: "openai", label: "OpenAI API key", host: "api.openai.com", renderer: RendererOpenAI, suffix: "/v1", binary: "opencode", env: []environmentVariable{{"OPENAI_BASE_URL", "{baseUrl}"}}, customFor: []Renderer{RendererOpenAI, RendererRaw},
					setup: []SetupFile{{Path: "~/.config/opencode/opencode.json", Language: "json", Body: `{
  "$schema": "https://opencode.ai/config.json",
  "model": "openai/gpt-5.1",
  "small_model": "openai/gpt-5.1",
  "provider": {"openai": {"options": {"apiKey": "{env:OPENAI_API_KEY}", "baseURL": "{baseUrl}"}}}
}`}},
					warnings: []string{"A ChatGPT login uses a different host and does not go through this route. Use an OpenAI API key."},
				},
			},
		},
		{
			id: "pi", label: "Pi",
			providers: []providerEntry{
				{id: "anthropic", label: "Anthropic", host: "api.anthropic.com", renderer: RendererAnthropic, binary: "pi", setup: []SetupFile{piModels("anthropic", "{baseUrl}", "")}, customFor: []Renderer{RendererAnthropic}, notes: []string{"Pi has no base-URL flag, so ~/.pi/agent/models.json points its Anthropic provider at the proxy."}},
				{id: "openai", label: "OpenAI API key", host: "api.openai.com", renderer: RendererOpenAI, suffix: "/v1", binary: "pi", setup: []SetupFile{piModels("openai", "{baseUrl}", "")}, customFor: []Renderer{RendererOpenAI, RendererRaw}},
				{id: "codex", label: "ChatGPT subscription (Codex)", host: "chatgpt.com", renderer: RendererOpenAI, suffix: "/backend-api", binary: "pi", setup: []SetupFile{piModels("openai-codex", "{baseUrl}", ""), {Path: "~/.pi/agent/settings.json", Language: "json", Body: `{
  "transport": "sse"
}`}}, notes: []string{"SSE is required because the logger only handles HTTP, not Pi's default WebSocket transport."}, warnings: []string{"Pi compresses this route with zstd. The exact request bytes are saved, but the standard-library build cannot decode zstd for the readable Markdown capture."}},
			},
		},
		{
			id: "omp", label: "OMP (Oh My Pi)", alwaysCustom: true,
			providers: []providerEntry{{id: customProviderID, label: "Custom base URL", binary: "omp", setup: []SetupFile{{Path: "~/.omp/agent/models.yml", Language: "yaml", Body: "providers:\n  custom:\n    baseUrl: \"{baseUrl}\""}}}},
		},
		{
			id: "gemini", label: "Gemini CLI",
			providers: []providerEntry{
				{id: "google-login", label: "Free Google account login", host: "cloudcode-pa.googleapis.com", renderer: RendererGemini, binary: "gemini", env: []environmentVariable{{"CODE_ASSIST_ENDPOINT", "{baseUrl}"}}, notes: []string{"The Google-login route forwards housekeeping calls but only captures content-generation requests."}},
				{id: "api-key", label: "Gemini API key", host: "generativelanguage.googleapis.com", renderer: RendererGemini, binary: "gemini", env: []environmentVariable{{"GOOGLE_GEMINI_BASE_URL", "{baseUrl}"}}, warnings: []string{"CODE_ASSIST_ENDPOINT is for Google login; GOOGLE_GEMINI_BASE_URL is for API-key login. They are not interchangeable."}, customFor: []Renderer{RendererOpenAI, RendererAnthropic, RendererRaw}},
			},
		},
		{
			id: "antigravity", label: "Antigravity CLI",
			providers: []providerEntry{{id: "api-key", label: "Gemini API key", host: "generativelanguage.googleapis.com", renderer: RendererGemini, binary: "agy", env: []environmentVariable{{"GOOGLE_GEMINI_BASE_URL", "{baseUrl}"}}, setup: []SetupFile{{Path: "~/.gemini/antigravity-cli/settings.json", Language: "json", Body: `{
  "modelProvider": "gemini"
}`}}, notes: []string{"This is the Gemini API-key route; GEMINI_API_KEY must already be set."}, warnings: []string{"The default Antigravity account-sign-in route is not covered."}, customFor: []Renderer{RendererOpenAI, RendererAnthropic, RendererRaw}}},
		},
		{
			id: "junie", label: "Junie", alwaysCustom: true,
			providers: []providerEntry{
				{id: "anthropic", label: "Anthropic-compatible", binary: "junie", customFor: []Renderer{RendererAnthropic}},
				{id: "openai", label: "OpenAI-compatible", binary: "junie", customFor: []Renderer{RendererOpenAI, RendererRaw}},
			},
		},
		{
			id: "amp", label: "Amp",
			refusal: "Amp builds its system prompt on its servers and its URL setting points to Amp, not to the model API. A local proxy cannot capture the hidden prompt or tool list.",
		},
	}
}

// piModels creates the Pi provider override required to route a model through the proxy.
func piModels(providerID, baseURL, modelID string) SetupFile {
	provider := map[string]any{"baseUrl": baseURL}
	if modelID != "" {
		provider["models"] = []map[string]string{{"id": modelID}}
	}
	body, _ := json.MarshalIndent(map[string]any{"providers": map[string]any{providerID: provider}}, "", "  ")
	return SetupFile{Path: "~/.pi/agent/models.json", Language: "json", Body: string(body)}
}

// ListAgents returns supported harnesses first, followed by harnesses that cannot be logged.
func ListAgents() []AgentSummary {
	supported := make([]AgentSummary, 0, len(agentCatalogue))
	unsupported := make([]AgentSummary, 0, len(agentCatalogue))
	for _, agent := range agentCatalogue {
		summary := AgentSummary{ID: agent.id, Label: agent.label, Supported: len(agent.providers) > 0}
		if summary.Supported {
			supported = append(supported, summary)
		} else {
			unsupported = append(unsupported, summary)
		}
	}
	return append(supported, unsupported...)
}

// ResolveChoice builds the target, launch command, and setup snippets for a harness selection.
func ResolveChoice(choice AgentChoice, port int) (Target, error) {
	if err := validatePort(port); err != nil {
		return Target{}, err
	}
	agent, ok := findAgent(choice.Agent)
	if choice.Agent == otherChoiceID || choice.Provider == otherChoiceID {
		return Target{}, fmt.Errorf("logger: setup not catalogued; request support at %s", issueURL)
	}
	if !ok {
		return Target{}, fmt.Errorf("logger: unknown agent %q", choice.Agent)
	}
	if agent.refusal != "" {
		return Target{}, fmt.Errorf("logger: %s cannot be logged: %s", agent.label, agent.refusal)
	}
	if agent.alwaysCustom || choice.Provider == customProviderID {
		return resolveCustomTarget(agent, choice, port)
	}
	provider, ok := findProvider(agent, choice.Provider)
	if !ok {
		if choice.Provider == "" && len(agent.providers) == 1 {
			provider = agent.providers[0]
		} else {
			return Target{}, fmt.Errorf("logger: unknown or missing provider %q for %s", choice.Provider, agent.label)
		}
	}
	baseURL := fmt.Sprintf("http://localhost:%d%s", port, provider.suffix)
	return Target{
		AgentID: agent.id, AgentLabel: agent.label, ProviderID: provider.id, ProviderLabel: provider.label,
		UpstreamHost: provider.host, UpstreamBaseURL: "https://" + provider.host,
		Renderer: provider.renderer, BaseURL: baseURL, Command: buildCommand(provider, baseURL),
		Setup: fillSetup(provider.setup, baseURL), Notes: append([]string(nil), provider.notes...), Warnings: append([]string(nil), provider.warnings...),
	}, nil
}

// resolveCustomTarget combines a user-entered upstream with the closest harness configuration template.
func resolveCustomTarget(agent agentEntry, choice AgentChoice, port int) (Target, error) {
	upstream, err := parseUpstreamURL(choice.CustomBaseURL)
	if err != nil {
		return Target{}, err
	}
	renderer := choice.CustomRenderer
	if renderer == "" {
		renderer = RendererRaw
	}
	if renderer != RendererAnthropic && renderer != RendererOpenAI && renderer != RendererRaw {
		return Target{}, fmt.Errorf("logger: unsupported custom wire format %q", renderer)
	}
	provider, hasTemplate := customTemplate(agent, renderer)
	baseURL := fmt.Sprintf("http://localhost:%d%s", port, provider.suffix)
	modelNeeded := agent.id == "pi" || (agent.id == "opencode" && renderer == RendererOpenAI)
	if modelNeeded && strings.TrimSpace(choice.CustomModel) == "" {
		return Target{}, fmt.Errorf("logger: %s custom target needs a model ID", agent.label)
	}
	setup := fillSetup(provider.setup, baseURL)
	command := buildCommand(provider, baseURL)
	if agent.id == "pi" && modelNeeded && hasTemplate {
		setup = []SetupFile{piModels(provider.id, baseURL, choice.CustomModel)}
	}
	if agent.id == "opencode" && modelNeeded {
		command, setup = openCodeCustomCommand(baseURL, choice.CustomModel), nil
	}
	if agent.id == "junie" {
		setup = []SetupFile{junieConfig(baseURL, renderer)}
	}
	if !hasTemplate {
		command = agent.id
		setup = nil
	}
	notes := append([]string(nil), provider.notes...)
	if renderer == RendererRaw {
		notes = append(notes, "The wire format is unknown, so captures use a raw JSON rendering rather than guessing at the request structure.")
	}
	return Target{
		AgentID: agent.id, AgentLabel: agent.label, ProviderID: customProviderID, ProviderLabel: "Custom base URL",
		UpstreamHost: upstream.Hostname(), UpstreamBaseURL: upstream.String(), Renderer: renderer,
		BaseURL: baseURL, Command: command, Setup: setup, Notes: notes, Warnings: append([]string(nil), provider.warnings...),
	}, nil
}

// customTemplate selects the provider configuration that best matches the requested wire format.
func customTemplate(agent agentEntry, renderer Renderer) (providerEntry, bool) {
	if len(agent.providers) == 1 {
		return agent.providers[0], true
	}
	for _, provider := range agent.providers {
		if slices.Contains(provider.customFor, renderer) {
			return provider, true
		}
	}
	return providerEntry{binary: agent.id}, false
}

// buildCommand returns shell syntax that starts the harness with its local proxy settings.
func buildCommand(provider providerEntry, baseURL string) string {
	args := []string{provider.binary}
	for _, argument := range provider.args {
		argument = strings.ReplaceAll(argument, "{baseUrl}", baseURL)
		if runtime.GOOS == "windows" {
			args = append(args, powerShellQuote(argument))
		} else {
			args = append(args, shellQuote(argument))
		}
	}
	binary := strings.Join(args, " ")
	if len(provider.env) == 0 {
		return binary
	}
	if runtime.GOOS == "windows" {
		assignments := make([]string, 0, len(provider.env)+1)
		for _, item := range provider.env {
			assignments = append(assignments, "$env:"+item.name+" = "+powerShellQuote(strings.ReplaceAll(item.value, "{baseUrl}", baseURL)))
		}
		return strings.Join(append(assignments, binary), "; ")
	}
	assignments := make([]string, 0, len(provider.env)+1)
	for _, item := range provider.env {
		assignments = append(assignments, item.name+"="+shellQuote(strings.ReplaceAll(item.value, "{baseUrl}", baseURL)))
	}
	return strings.Join(append(assignments, binary), " ")
}

// fillSetup replaces the local proxy placeholder in each generated configuration snippet.
func fillSetup(files []SetupFile, baseURL string) []SetupFile {
	filled := make([]SetupFile, len(files))
	for i, file := range files {
		file.Body = strings.ReplaceAll(file.Body, "{baseUrl}", baseURL)
		filled[i] = file
	}
	return filled
}

// parseUpstreamURL accepts only absolute HTTP URLs with a hostname.
func parseUpstreamURL(raw string) (*url.URL, error) {
	upstream, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || (upstream.Scheme != "http" && upstream.Scheme != "https") || upstream.Hostname() == "" {
		return nil, fmt.Errorf("logger: %q is not a valid http:// or https:// base URL", raw)
	}
	if upstream.User != nil {
		return nil, fmt.Errorf("logger: credentials must not be embedded in a custom base URL; use the harness's normal authentication")
	}
	upstream.Path = strings.TrimRight(upstream.Path, "/")
	upstream.RawQuery = ""
	upstream.Fragment = ""
	return upstream, nil
}

// findAgent looks up one harness by its stable identifier.
func findAgent(id string) (agentEntry, bool) {
	for _, agent := range agentCatalogue {
		if agent.id == id {
			return agent, true
		}
	}
	return agentEntry{}, false
}

// findProvider looks up one configured provider within a harness.
func findProvider(agent agentEntry, id string) (providerEntry, bool) {
	for _, provider := range agent.providers {
		if provider.id == id {
			return provider, true
		}
	}
	return providerEntry{}, false
}

// shellQuote makes a POSIX single-quoted shell argument safe from embedded quotes.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

// powerShellQuote makes a PowerShell single-quoted string safe from embedded quotes.
func powerShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

// junieConfig renders a Junie proxy configuration with the selected wire format.
func junieConfig(baseURL string, renderer Renderer) SetupFile {
	kind, auth := "OpenAI", "Authorization: Bearer YOUR_OPENAI_API_KEY"
	if renderer == RendererAnthropic {
		kind, auth = "Anthropic", "x-api-key: YOUR_ANTHROPIC_API_KEY"
	}
	body := fmt.Sprintf(`{
  "proxies": [{"name": "request-logger", "kind": %q, "api-url": %q, "headers": [%q]}],
  "provider": "request-logger"
}`, kind, baseURL, auth)
	return SetupFile{Path: "~/.junie/config.json", Language: "json", Body: body}
}

// openCodeCustomCommand creates a one-run provider configuration for a custom OpenAI-compatible endpoint.
func openCodeCustomCommand(baseURL, model string) string {
	provider := "request-logger"
	config := map[string]any{
		"model":       provider + "/" + model,
		"small_model": provider + "/" + model,
		"provider": map[string]any{provider: map[string]any{
			"npm": "@ai-sdk/openai-compatible", "name": "Request Logger",
			"options": map[string]string{"baseURL": baseURL},
			"models":  map[string]map[string]string{model: {"name": model}},
		}},
	}
	encoded, _ := json.Marshal(config)
	if runtime.GOOS == "windows" {
		return "$env:OPENCODE_CONFIG_CONTENT = " + powerShellQuote(string(encoded)) + "; opencode"
	}
	return "OPENCODE_CONFIG_CONTENT=" + shellQuote(string(encoded)) + " opencode"
}
