package main

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Renderer identifies the API request shape used for readable captures.
type Renderer string

const (
	// RendererAnthropic reads Anthropic Messages API requests.
	RendererAnthropic Renderer = "anthropic"
	// RendererOpenAI reads OpenAI-compatible requests.
	RendererOpenAI Renderer = "openai"
	// RendererGemini reads Gemini API requests.
	RendererGemini Renderer = "gemini"
	// RendererRaw preserves unknown request shapes as JSON.
	RendererRaw Renderer = "raw"
)

// AgentChoice contains the harness and provider selected by the user.
type AgentChoice struct {
	// Agent names the coding harness.
	Agent string `json:"agent"`
	// Provider names the provider configured for the harness.
	Provider string `json:"provider,omitempty"`
	// CustomBaseURL is the selected upstream when Provider is "custom".
	CustomBaseURL string `json:"customBaseUrl,omitempty"`
	// CustomRenderer is the protocol used by a custom upstream.
	CustomRenderer Renderer `json:"customRenderer,omitempty"`
	// CustomModel is required when the harness needs a model declared in config.
	CustomModel string `json:"customModel,omitempty"`
}

// SetupFile is a configuration snippet the user must apply before starting a harness.
type SetupFile struct {
	// Path is the destination configuration file.
	Path string
	// Body is the snippet to merge into that file.
	Body string
	// Language names the snippet's format for display.
	Language string
}

// Target describes a selected harness route through the local proxy.
type Target struct {
	// AgentID is the stable harness identifier.
	AgentID string
	// AgentLabel is the name shown to the user.
	AgentLabel string
	// ProviderID is the stable provider identifier.
	ProviderID string
	// ProviderLabel is the provider name shown to the user.
	ProviderLabel string
	// UpstreamHost is the hostname of the provider API.
	UpstreamHost string
	// UpstreamBaseURL includes the upstream scheme and any custom path prefix.
	UpstreamBaseURL string
	// Renderer selects the request and response formatter.
	Renderer Renderer
	// BaseURL is the local proxy URL for this provider route.
	BaseURL string
	// Command is the harness launch command with proxy settings.
	Command string
	// Setup lists configuration snippets needed before launch.
	Setup []SetupFile
	// Notes explains details that affect the selected route.
	Notes []string
	// Warnings identifies known limitations of the selected route.
	Warnings []string
}

// Capture contains one completed API exchange and the details needed to render it.
type Capture struct {
	// AgentLabel identifies the harness in the Markdown document.
	AgentLabel string
	// Renderer selects the request and response formatter.
	Renderer Renderer
	// Timestamp is the time the request reached the proxy.
	Timestamp time.Time
	// Method is the HTTP method sent by the harness.
	Method string
	// Path is the request path and query sent by the harness.
	Path string
	// StatusCode is the upstream response status.
	StatusCode int
	// RequestHeaders are the headers received from the harness.
	RequestHeaders map[string][]string
	// RequestBody is the exact body received from the harness.
	RequestBody []byte
	// RequestEncoding names the request body's content encoding.
	RequestEncoding string
	// ResponseBody is the exact body received from the provider.
	ResponseBody []byte
}

// ShouldLogRequest excludes probes and provider housekeeping calls from captures.
func ShouldLogRequest(method, requestPath string, renderer Renderer) bool {
	if !strings.EqualFold(method, "POST") {
		return false
	}
	if renderer == RendererGemini {
		return strings.Contains(strings.ToLower(requestPath), "generatecontent")
	}
	return !tokenCountPath.MatchString(requestPath)
}

// tokenCountPath matches token-count endpoints across provider URL conventions.
var tokenCountPath = regexp.MustCompile(`(?i)count[_-]?tokens`)

// validatePort rejects ports that cannot be bound by the proxy.
func validatePort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("logger: port %d is outside 1..65535", port)
	}
	return nil
}
