package main

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderMarkdownShowsAnthropicPromptAndMasksCredentialsProtectsUsefulCaptures.
func TestRenderMarkdownShowsAnthropicPromptAndMasksCredentialsProtectsUsefulCaptures(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Date(2026, 8, 6, 12, 0, 0, 0, time.UTC),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestHeaders: map[string][]string{"Authorization": {"Bearer secret-token"}, "anthropic-beta": {"oauth-capability"}},
		RequestBody:    []byte(`{"model":"claude-sonnet","system":"Follow this policy","tools":[{"name":"read_file","description":"Read a file","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"Explain this code"}]}`),
		ResponseBody:   []byte(`{"content":[{"type":"text","text":"I can explain it."}]}`),
	}
	markdown := RenderMarkdown(capture)
	for _, expected := range []string{"claude-sonnet", "<summary>System prompt (18 characters)</summary>", "Follow this policy", "### read_file", "Explain this code", "I can explain it.", "[REDACTED]"} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Markdown does not contain %q", expected)
		}
	}
	if strings.Contains(markdown, "secret-token") {
		t.Error("Markdown exposed an authorization credential")
	}
	if !strings.Contains(markdown, "oauth-capability") {
		t.Error("Markdown omitted the anthropic-beta OAuth capability header")
	}
}

// TestRenderMarkdownSummarizesAndFoldsLargeSectionsMakesCapturesScannable.
func TestRenderMarkdownSummarizesAndFoldsLargeSectionsMakesCapturesScannable(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic,
		Timestamp: time.Date(2026, 11, 3, 18, 59, 43, 0, time.UTC),
		Method:    http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestHeaders: map[string][]string{"content-type": {"application/json"}},
		RequestBody:    []byte(`{"model":"claude-haiku-5-5","system":"Follow this policy","tools":[{"name":"read_file","description":"Read a file","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"Explain this code"}]}`),
		ResponseBody: []byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":12}}}\n" +
			"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Here is the answer.\"}}\n" +
			"event: message_delta\ndata: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":4}}\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n"),
	}

	markdown := RenderMarkdown(capture)
	for _, expected := range []string{
		"# Claude Code — `claude-haiku-5-5`", "## Summary", "- **Upstream status:** 200",
		"- **Messages:** 1", "- **Tools:** 1 (`read_file`)",
		"- **Token usage:** {\"input_tokens\":12,\"output_tokens\":4}",
		"<summary>Request headers (1)</summary>", "## Request",
		"<summary>System prompt (18 characters)</summary>",
		"<summary>Tool definitions (1)</summary>", "### User message 1", "Explain this code",
		"## Response", "### Assistant response", "Here is the answer.",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Markdown does not contain %q", expected)
		}
	}
	if strings.Contains(markdown, "<meta>") || strings.Contains(markdown, "<message index=") {
		t.Errorf("Markdown still uses XML-like wrappers:\n%s", markdown)
	}
}

// TestDecodeRequestBodyReadsGzipAndWarnsForZstdPreservesTheNoDependencyBoundary.
func TestDecodeRequestBodyReadsGzipAndWarnsForZstdPreservesTheNoDependencyBoundary(t *testing.T) {
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write([]byte(`{"model":"compressed"}`)); err != nil {
		t.Fatalf("compress request: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close gzip stream: %v", err)
	}
	decoded, warning := decodeRequestBody(compressed.Bytes(), "gzip")
	if warning != "" || decoded != `{"model":"compressed"}` {
		t.Errorf("gzip decode = %q, warning %q; want original JSON and no warning", decoded, warning)
	}
	_, warning = decodeRequestBody([]byte{0x28, 0xb5, 0x2f, 0xfd}, "zstd")
	if !strings.Contains(warning, "zstd") || !strings.Contains(warning, ".request.txt") {
		t.Errorf("zstd warning = %q, want raw-capture guidance", warning)
	}
}

// TestRequestModelReadsGeminiModelFromPathFindsTheModelForURLShapedRequests.
func TestRequestModelReadsGeminiModelFromPathFindsTheModelForURLShapedRequests(t *testing.T) {
	if got := requestModel(map[string]any{}, "/v1/models/gemini-pro:streamGenerateContent"); got != "gemini-pro" {
		t.Errorf("requestModel() = %q, want gemini-pro", got)
	}
}

// TestRawRendererDoesNotGuessAtProtocolPreservesUnknownRequestShapes.
func TestRawRendererDoesNotGuessAtProtocolPreservesUnknownRequestShapes(t *testing.T) {
	capture := Capture{Renderer: RendererRaw, RequestBody: []byte(`{"unknown":{"field":true}}`), Timestamp: time.Now().UTC()}
	markdown := RenderMarkdown(capture)
	if !strings.Contains(markdown, `"unknown"`) {
		t.Errorf("raw Markdown = %q, want unknown JSON shape", markdown)
	}
}

// TestRenderMarkdownFormatsOpenAIResponsesRequestsAndStreamsKeepsPromptsAndToolsReadable.
func TestRenderMarkdownFormatsOpenAIResponsesRequestsAndStreamsKeepsPromptsAndToolsReadable(t *testing.T) {
	capture := Capture{
		AgentLabel: "Pi", Renderer: RendererOpenAI, Timestamp: time.Now().UTC(), Method: http.MethodPost,
		Path: "/v1/responses", StatusCode: 200,
		RequestBody: []byte(`{"model":"gpt-local","instructions":"Use tools carefully","tools":[{"type":"function","name":"read_file","description":"Read a file","parameters":{"type":"object"}}],"input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"read this"}]},{"type":"function_call","call_id":"call-1","name":"read_file","arguments":"{\"path\":\"a.go\"}"}]}`),
		ResponseBody: []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Answer\"}\n" +
			"data: {\"type\":\"response.output_item.added\",\"item\":{\"type\":\"function_call\",\"id\":\"item-1\",\"name\":\"write_file\"}}\n" +
			"data: {\"type\":\"response.function_call_arguments.delta\",\"item_id\":\"item-1\",\"delta\":\"{\\\"path\\\":\\\"b.go\\\"}\"}\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":5}}}\n"),
	}
	markdown := RenderMarkdown(capture)
	for _, expected := range []string{"gpt-local", "Use tools carefully", "### read_file", "read this", "### Tool call: read_file", "Answer", "write_file", "completed"} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("OpenAI Markdown does not contain %q", expected)
		}
	}
}

// TestRenderMarkdownFormatsGeminiRequestsAndStreamsKeepsItsDistinctWireShapeReadable.
func TestRenderMarkdownFormatsGeminiRequestsAndStreamsKeepsItsDistinctWireShapeReadable(t *testing.T) {
	capture := Capture{
		AgentLabel: "Gemini CLI", Renderer: RendererGemini, Timestamp: time.Now().UTC(), Method: http.MethodPost,
		Path: "/v1/models/gemini-pro:streamGenerateContent", StatusCode: 200,
		RequestBody:  []byte(`{"systemInstruction":{"parts":[{"text":"Follow policy"}]},"tools":[{"functionDeclarations":[{"name":"search","description":"Search docs","parameters":{"type":"object"}}]}],"contents":[{"role":"user","parts":[{"text":"find this"}]}]}`),
		ResponseBody: []byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"thinking\",\"thought\":true},{\"text\":\"answer\"},{\"functionCall\":{\"name\":\"search\",\"args\":{\"query\":\"docs\"}}}]},\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":2}}\n"),
	}
	markdown := RenderMarkdown(capture)
	for _, expected := range []string{"gemini-pro", "Follow policy", "### search", "find this", "<summary>Thinking</summary>", "answer", "STOP", "promptTokenCount"} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Gemini Markdown does not contain %q", expected)
		}
	}
}
