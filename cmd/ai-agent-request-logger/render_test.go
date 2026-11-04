package main

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLBuildsStyledPageAndLinksToolSummaryProtectsCaptureNavigation.
func TestRenderHTMLBuildsStyledPageAndLinksToolSummaryProtectsCaptureNavigation(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","system":"Follow this policy","tools":[{"name":"read_file","description":"Read a file","input_schema":{"type":"object"}},{"name":"mcp__clean-view__plan_steps","description":"Plan steps","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"Read <script>alert('x')</script> and ![pixel](https://example.com/track)"},{"role":"assistant","content":"Prior turn."},{"role":"user","content":"As you answer the user’s questions, you can use the following context:\n\n## userEmail\n\nuser@example.com"}]}`),
		ResponseBody: []byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Here is the result.\"}}\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n"),
	}

	page := RenderHTML(capture)
	for _, expected := range []string{
		"<!doctype html>", "<html lang=\"en\" data-design=\"anthropic\">",
		"<meta name=\"color-scheme\" content=\"light dark\">", "<title>Claude Code — claude-haiku-5-5</title>",
		"--d-page: #faf9f5", "--d-page: #141413", "--d-focus: #185fa5",
		"--d-font-heading: \"Poppins\"", "--d-font-body: \"Lora\"",
		"<label class=\"design-appearance\" for=\"design-mode\">Appearance",
		"<option value=\"system\" selected>System</option>", "<option value=\"light\">Light</option>", "<option value=\"dark\">Dark</option>",
		"localStorage.getItem(key)", "matchMedia(\"(prefers-color-scheme: dark)\")", "@media print", "@media (prefers-reduced-motion: reduce)",
		"<a href=\"#tool-read-file\"><code>read_file</code></a>",
		"<summary><a id=\"tool-read-file\"></a><code>read_file</code></summary>",
		"<a href=\"#tool-mcp-clean-view-plan-steps\"><code>mcp__clean-view__plan_steps</code></a>",
		"<summary><a id=\"tool-mcp-clean-view-plan-steps\"></a><code>mcp__clean-view__plan_steps</code></summary>",
		"a[href^=\"#tool-\"]", "border-radius: 999px", "a[href^=\"#tool-\"]:hover", "a[href^=\"#tool-\"]:focus-visible",
		"<details>", "<summary><a id=\"section-system-prompt\"></a>System prompt (18 characters) 📋</summary>",
		"<summary><a id=\"section-tools\"></a>Tool definitions (2) 🧰</summary>",
		"<details class=\"msg-user\">", "<summary><a id=\"message-1\"></a>Message 1 · User 🧑</summary>",
		"<summary><a id=\"message-2\"></a>Message 2 · Assistant 🤖</summary>",
		"<summary><a id=\"message-3\"></a>Message 3 · User 🧑</summary>",
		"<a href=\"#message-1\">1</a>", "<a href=\"#message-3\">3</a>",
		"<summary>Additional context</summary>", "<summary>Assistant response</summary>", "Here is the result.",
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("HTML capture does not contain %q:\n%s", expected, page)
		}
	}
	if strings.Contains(page, "<script>alert") || strings.Contains(page, `<img src=\"https://example.com/track\"`) {
		t.Errorf("HTML capture rendered untrusted prompt HTML:\n%s", page)
	}
	head, _, _ := strings.Cut(page, "</head>")
	if strings.Contains(head, "<link") || strings.Contains(head, "@import url") {
		t.Errorf("HTML capture depends on external styles or assets:\n%s", head)
	}
	for _, closed := range []string{"<details>\n<summary><a id=\"tool-read-file\"></a>", "<details>\n<summary><a id=\"tool-mcp-clean-view-plan-steps\"></a>"} {
		if !strings.Contains(page, closed) {
			t.Errorf("tool definition is not closed by default: %q", closed)
		}
	}
}

// TestRenderHTMLRendersCollapsibleAgentFilesAsMarkdownKeepsLongPromptReadable.
func TestRenderHTMLRendersCollapsibleAgentFilesAsMarkdownKeepsLongPromptReadable(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","system":"Contents of /Users/mmcdonnell/.agents/AGENTS.md (user's private global instructions for all projects):\n\n# Communication & Tone\n\n- Keep replies short.\n\nContents of /Users/mmcdonnell/.claude/CLAUDE.md (user’s private global instructions for all projects):\n\n# Claude instructions\n\n- Follow the same guidance.\n\nContents of /Users/mmcdonnell/.claude/rules/LINTER-DIRECTIVES.md (user’s private global instructions for all projects):\n\n# Linter Suppression Directives\n\nUse exact syntax."}`),
	}

	page := RenderHTML(capture)
	sections := []struct {
		summary string
		content string
	}{
		{
			summary: "<summary>Contents of <code>/Users/mmcdonnell/.agents/AGENTS.md</code> (user&#39;s private global instructions for all projects):</summary>",
			content: "Communication &amp; Tone</h4>",
		},
		{
			summary: "<summary>Contents of <code>/Users/mmcdonnell/.claude/CLAUDE.md</code> (user’s private global instructions for all projects):</summary>",
			content: "Claude instructions</h4>",
		},
		{
			summary: "<summary>Contents of <code>/Users/mmcdonnell/.claude/rules/LINTER-DIRECTIVES.md</code> (user’s private global instructions for all projects):</summary>",
			content: "Linter Suppression Directives</h4>",
		},
	}
	if !strings.Contains(page, "<summary><a id=\"section-system-prompt\"></a>System prompt (") {
		t.Errorf("HTML capture lacks the collapsible system prompt:\n%s", page)
	}
	for _, section := range sections {
		summaryIndex := strings.Index(page, section.summary)
		contentIndex := strings.Index(page, section.content)
		if summaryIndex < 0 || contentIndex <= summaryIndex {
			t.Errorf("HTML capture does not render embedded file %q after its summary:\n%s", section.summary, page)
			continue
		}
		closeIndex := strings.Index(page[contentIndex:], "</details>")
		if closeIndex < 0 {
			t.Errorf("HTML capture does not close embedded file section %q:\n%s", section.summary, page)
		}
	}
}

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
	for _, expected := range []string{"claude-sonnet", "<summary><a id=\"section-system-prompt\"></a>System prompt (18 characters) 📋</summary>", "Follow this policy", "<summary><a id=\"tool-read-file\"></a><code>read_file</code></summary>", "Explain this code", "I can explain it.", "[REDACTED]"} {
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
		"- **Messages:** 1 ([1](#message-1))", "- **Tools:** 1 ([`read_file`](#tool-read-file))",
		"- **Token usage:** input: 12 · output: 4",
		"<summary>Token usage details</summary>",
		"```json\n{\n  \"input_tokens\": 12,\n  \"output_tokens\": 4\n}\n```",
		"<summary>Request headers (1)</summary>", "## Request 📨 {#section-request}",
		"<summary><a id=\"section-system-prompt\"></a>System prompt (18 characters) 📋</summary>",
		"<summary><a id=\"section-tools\"></a>Tool definitions (1) 🧰</summary>", "Message 1 · User", "Explain this code",
		"## Response 📬 {#section-response}", "<summary>Assistant response</summary>", "Here is the answer.",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Markdown does not contain %q", expected)
		}
	}
	if strings.Contains(markdown, "<meta>") || strings.Contains(markdown, "<message index=") {
		t.Errorf("Markdown still uses XML-like wrappers:\n%s", markdown)
	}
}

// TestRenderMarkdownFencesCompositeParametersAndJSONTextKeepsJSONReadable.
func TestRenderMarkdownFencesCompositeParametersAndJSONTextKeepsJSONReadable(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","max_tokens":128000,"stream":true,"thinking":{"display":"summarized","type":"adaptive"},"metadata":{"user_id":{"device_id":"device-1","session_id":"session-1"}},"messages":[{"role":"user","content":"Return JSON"}]}`),
		ResponseBody: []byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"{\\\"ok\\\":true}\"}}\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n"),
	}

	markdown := RenderMarkdown(capture)
	for _, expected := range []string{
		"- **max_tokens**: `128000`", "- **stream**: `true`",
		"- **thinking**:\n\n    ```json\n    {\n      \"display\": \"summarized\",\n      \"type\": \"adaptive\"\n    }\n    ```",
		"- **metadata**:\n\n    ```json\n    {\n      \"user_id\": {\n        \"device_id\": \"device-1\",\n        \"session_id\": \"session-1\"\n      }\n    }\n    ```",
		"```json\n{\n  \"ok\": true\n}\n```",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Markdown does not contain structured JSON output %q:\n%s", expected, markdown)
		}
	}
	if strings.Contains(markdown, `**thinking**: {"display"`) || strings.Contains(markdown, `**metadata**: {"user_id"`) {
		t.Errorf("composite parameters were rendered as inline JSON:\n%s", markdown)
	}
}

// TestRenderMarkdownCollapsesAGENTSContentAndFormatsItsPathsKeepsLongInstructionsReadable.
func TestRenderMarkdownCollapsesAGENTSContentAndFormatsItsPathsKeepsLongInstructionsReadable(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody:  []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"Contents of /Users/mmcdonnell/.agents/AGENTS.md (user's private global instructions):\n\n# Communication & Tone\n\n## Chat Execution\n\n- Keep replies short.\n\nContents of /Users/mmcdonnell/.claude/CLAUDE.md:\n\n@~/.agents/AGENTS.md\nSee https://example.com/README.md"}]}`),
		ResponseBody: []byte(`{"content":[{"type":"text","text":"Done."}]}`),
	}

	markdown := RenderMarkdown(capture)
	for _, expected := range []string{
		"<summary>Contents of <code>/Users/mmcdonnell/.agents/AGENTS.md</code> (user&#39;s private global instructions):</summary>",
		"# Communication & Tone", "## Chat Execution", "- Keep replies short.",
		"<summary>Contents of <code>/Users/mmcdonnell/.claude/CLAUDE.md</code>:</summary>",
		"`@~/.agents/AGENTS.md`", "https://example.com/README.md",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Markdown does not contain %q:\n%s", expected, markdown)
		}
	}
	if strings.Contains(markdown, "```markdown") || !strings.Contains(markdown, "\n\n#### Communication & Tone") {
		t.Errorf("AGENTS.md content was escaped or rendered as a code block:\n%s", markdown)
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
	for _, expected := range []string{"gpt-local", "Use tools carefully", "<summary><a id=\"tool-read-file\"></a><code>read_file</code></summary>", "read this", "### Tool call: read_file", "<summary>Arguments</summary>", "```json\n{\n  \"path\": \"a.go\"\n}\n```", "Answer", "write_file", "completed"} {
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
	for _, expected := range []string{"gemini-pro", "Follow policy", "<summary><a id=\"tool-search\"></a><code>search</code></summary>", "find this", "<summary>Thinking</summary>", "answer", "STOP", "promptTokenCount"} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Gemini Markdown does not contain %q", expected)
		}
	}
}

// TestRenderMarkdownFormatsCopilotResponsesEventsWithReasoningAndObfuscatedItemIDs.
func TestRenderMarkdownFormatsCopilotResponsesEventsWithReasoningAndObfuscatedItemIDs(t *testing.T) {
	capture := Capture{
		AgentLabel: "OpenCode", Renderer: RendererOpenAI, Timestamp: time.Now().UTC(), Method: http.MethodPost,
		Path: "/chat/completions", StatusCode: 200,
		RequestBody: []byte(`{"model":"gpt-5.6-terra","input":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hello"}]}]}`),
		ResponseBody: []byte("data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"Thinking about this...\",\"output_index\":0}\n" +
			"data: {\"type\":\"response.output_item.added\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"id\":\"opaque/base64/id\",\"call_id\":\"call_skill123\",\"name\":\"skill\"}}\n" +
			"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"item_id\":\"another/opaque/id1\",\"delta\":\"{\\\"id\\\":\\\"ex\"}\n" +
			"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":1,\"item_id\":\"another/opaque/id2\",\"delta\":\"plain-code\\\"}\"}\n" +
			"data: {\"type\":\"response.output_item.done\",\"output_index\":1,\"item\":{\"type\":\"function_call\",\"call_id\":\"call_skill123\",\"name\":\"skill\",\"arguments\":\"{\\\"id\\\":\\\"explain-code\\\"}\"}}\n" +
			"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n"),
	}
	markdown := RenderMarkdown(capture)
	for _, expected := range []string{
		"<summary>Thinking</summary>",
		"Thinking about this...",
		"### Tool call: skill (`call_skill123`)",
		"<summary>Arguments</summary>",
		"\"id\": \"explain-code\"",
		"- **status**: completed",
	} {
		if !strings.Contains(markdown, expected) {
			t.Errorf("Copilot markdown does not contain %q", expected)
		}
	}
	// Verify that the obfuscated base64 IDs do not leak as tool call headings
	for _, bad := range []string{"opaque/base64/id", "another/opaque/id1", "another/opaque/id2"} {
		if strings.Contains(markdown, bad) {
			t.Errorf("Copilot markdown leaked opaque base64 item ID %q", bad)
		}
	}
}

// TestRenderMarkdownFormatsToolResultsWithDedicatedBoxAndStrippedANSI.
func TestRenderMarkdownFormatsToolResultsWithDedicatedBoxAndStrippedANSI(t *testing.T) {
	capture := Capture{
		AgentLabel: "OpenCode", Renderer: RendererOpenAI, Timestamp: time.Now().UTC(), Method: http.MethodPost,
		Path: "/chat/completions", StatusCode: 200,
		RequestBody:  []byte(`{"model":"gpt-5.6-terra","input":[{"type":"function_call_output","call_id":"call_abc","output":"\u001b[33mcolored\u001b[m output"}]}`),
		ResponseBody: []byte(`{"choices":[{"message":{"content":"done"}}]}`),
	}
	page := RenderHTML(capture)
	for _, expected := range []string{
		"<h3>Tool result for <code>call_abc</code></h3>",
		"<summary>Result</summary>",
		"colored output",
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("HTML does not contain %q", expected)
		}
	}
	if strings.Contains(page, "\x1b[33m") || strings.Contains(page, "[33m") {
		t.Errorf("HTML contains raw ANSI codes")
	}
}
