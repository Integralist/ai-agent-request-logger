package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const modelDiscoveryTimeout = 3 * time.Second

// choicePath locates the remembered harness choice in the user's config directory.
func choicePath() (string, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: find user config directory: %w", err)
	}
	return filepath.Join(configDir, "ai-agent-request-logger", "choice.json"), nil
}

// loadChoice reads a valid saved harness selection, ignoring a missing or malformed file.
func loadChoice(path string) (AgentChoice, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return AgentChoice{}, false, nil
	}
	if err != nil {
		return AgentChoice{}, false, fmt.Errorf("config: read choice: %w", err)
	}
	var choice AgentChoice
	if err := json.Unmarshal(data, &choice); err != nil || choice.Agent == "" {
		return AgentChoice{}, false, nil
	}
	return choice, true, nil
}

// saveChoice stores only the selected harness and custom-target details with private permissions.
func saveChoice(path string, choice AgentChoice) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("config: create choice directory: %w", err)
	}
	data, err := json.MarshalIndent(choice, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode choice: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("config: write choice: %w", err)
	}
	return nil
}

// clearChoice removes a saved selection before the user chooses another route.
func clearChoice(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("config: remove choice: %w", err)
	}
	return nil
}

// askChoice collects a harness/provider selection from a numbered terminal menu.
func askChoice(ctx context.Context, reader *bufio.Reader, out io.Writer) (AgentChoice, bool, error) {
	fmt.Fprintln(out, "AI Agent Request Logger")
	fmt.Fprintln(out, "Choose your coding harness:")
	agents := ListAgents()
	options := make([]string, len(agents)+1)
	for index, agent := range agents {
		options[index] = agent.Label
		if !agent.Supported {
			options[index] += " (cannot be logged)"
		}
	}
	options[len(agents)] = "Other, or not sure"
	agentIndex, err := promptSelection(ctx, reader, out, options)
	if err != nil {
		return AgentChoice{}, false, err
	}
	if agentIndex == len(agents) {
		return AgentChoice{Agent: otherChoiceID}, false, nil
	}
	agent, ok := findAgent(agents[agentIndex].ID)
	if !ok {
		return AgentChoice{}, false, fmt.Errorf("config: selected unknown harness %q", agents[agentIndex].ID)
	}
	choice := AgentChoice{Agent: agent.id}
	if agent.refusal != "" {
		return choice, false, nil
	}
	if agent.alwaysCustom {
		return askCustomChoice(ctx, reader, out, choice)
	}

	providerOptions := make([]string, 0, len(agent.providers)+2)
	for _, provider := range agent.providers {
		providerOptions = append(providerOptions, provider.label)
	}
	providerOptions = append(providerOptions, "Custom base URL", "Other, or not sure")
	fmt.Fprintf(out, "Which model provider do you use with %s?\n", agent.label)
	providerIndex, err := promptSelection(ctx, reader, out, providerOptions)
	if err != nil {
		return AgentChoice{}, false, err
	}
	if providerIndex == len(providerOptions)-1 {
		choice.Provider = otherChoiceID
		return choice, false, nil
	}
	if providerIndex == len(agent.providers) {
		return askCustomChoice(ctx, reader, out, choice)
	}
	choice.Provider = agent.providers[providerIndex].id
	remember, err := askRemember(ctx, reader, out)
	return choice, remember, err
}

// askCustomChoice collects a custom upstream, wire format, and required model ID.
func askCustomChoice(ctx context.Context, reader *bufio.Reader, out io.Writer, choice AgentChoice) (AgentChoice, bool, error) {
	baseURL, err := promptText(ctx, reader, out, "Model server base URL (http:// or https://): ")
	if err != nil {
		return AgentChoice{}, false, err
	}
	if _, err := parseUpstreamURL(baseURL); err != nil {
		fmt.Fprintln(out, err)
		return askCustomChoice(ctx, reader, out, choice)
	}
	choice.Provider = customProviderID
	choice.CustomBaseURL = strings.TrimSpace(baseURL)
	formatOptions := []string{"OpenAI-compatible", "Anthropic-compatible", "Not sure — show raw JSON"}
	formatIndex, err := promptSelection(ctx, reader, out, formatOptions)
	if err != nil {
		return AgentChoice{}, false, err
	}
	choice.CustomRenderer = []Renderer{RendererOpenAI, RendererAnthropic, RendererRaw}[formatIndex]
	if agentNeedsModel(choice.Agent, choice.CustomRenderer) {
		models := discoverModelIDs(ctx, choice.CustomBaseURL)
		switch len(models) {
		case 0:
			fmt.Fprintln(out, "Could not discover models; enter a model ID manually.")
			choice.CustomModel, err = promptText(ctx, reader, out, "Model ID: ")
		case 1:
			choice.CustomModel = models[0]
			fmt.Fprintf(out, "Using the only model found: %s\n", models[0])
		default:
			models = append(models, "Enter a model ID manually")
			modelIndex, pickErr := promptSelection(ctx, reader, out, models)
			if pickErr != nil {
				return AgentChoice{}, false, pickErr
			}
			if modelIndex == len(models)-1 {
				choice.CustomModel, err = promptText(ctx, reader, out, "Model ID: ")
			} else {
				choice.CustomModel = models[modelIndex]
			}
		}
		if err != nil {
			return AgentChoice{}, false, err
		}
		if strings.TrimSpace(choice.CustomModel) == "" {
			return AgentChoice{}, false, errors.New("config: model ID cannot be empty")
		}
	}
	remember, err := askRemember(ctx, reader, out)
	return choice, remember, err
}

// promptSelection displays numbered options and returns the selected zero-based index.
func promptSelection(ctx context.Context, reader *bufio.Reader, out io.Writer, options []string) (int, error) {
	for index, option := range options {
		fmt.Fprintf(out, "  %d) %s\n", index+1, option)
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, fmt.Errorf("config: read selection: %w", err)
		}
		value, err := promptText(ctx, reader, out, "Selection: ")
		if err != nil {
			return 0, err
		}
		var selected int
		if _, err := fmt.Sscanf(value, "%d", &selected); err == nil && selected >= 1 && selected <= len(options) {
			return selected - 1, nil
		}
		fmt.Fprintf(out, "Enter a number from 1 to %d.\n", len(options))
	}
}

// promptText reads one non-empty terminal line for a setup answer.
func promptText(ctx context.Context, reader *bufio.Reader, out io.Writer, prompt string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", fmt.Errorf("config: read answer: %w", err)
	}
	fmt.Fprint(out, prompt)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("config: read answer: %w", err)
	}
	value = strings.TrimSpace(value)
	if errors.Is(err, io.EOF) && value == "" {
		return "", fmt.Errorf("config: read answer: %w", io.EOF)
	}
	return value, nil
}

// askRemember asks whether to keep the selection for later runs.
func askRemember(ctx context.Context, reader *bufio.Reader, out io.Writer) (bool, error) {
	answer, err := promptText(ctx, reader, out, "Remember this choice? [Y/n] ")
	if err != nil {
		return false, err
	}
	return answer == "" || strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes"), nil
}

// discoverModelIDs asks an unauthenticated OpenAI-compatible endpoint for model names.
func discoverModelIDs(ctx context.Context, baseURL string) []string {
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil
	}
	endpoint := parsed.Scheme + "://" + parsed.Host + "/v1/models"
	requestCtx, cancel := context.WithTimeout(ctx, modelDiscoveryTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestCtx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil
	}
	response, err := (&http.Client{}).Do(request)
	if err != nil {
		return nil
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil
	}
	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil
	}
	seen := make(map[string]bool)
	models := make([]string, 0, len(payload.Data))
	for _, model := range payload.Data {
		id := strings.TrimSpace(model.ID)
		if id != "" && !seen[id] {
			seen[id] = true
			models = append(models, id)
		}
	}
	return models
}

// agentNeedsModel reports which custom providers require an explicit model entry.
func agentNeedsModel(agentID string, renderer Renderer) bool {
	return agentID == "pi" || (agentID == "opencode" && renderer == RendererOpenAI)
}
