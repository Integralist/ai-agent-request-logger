package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var markdownFilePath = regexp.MustCompile(`(?:@~|~|/)[A-Za-z0-9._~/-]+\.md`)

var redactedHeaders = map[string]bool{
	"authorization":  true,
	"x-api-key":      true,
	"api-key":        true,
	"x-goog-api-key": true,
}

// RenderMarkdown creates a scannable summary with the complete exchange available in structured sections.
func RenderMarkdown(capture Capture) string {
	requestText, decodeWarning := decodeRequestBody(capture.RequestBody, capture.RequestEncoding)
	var request any
	if err := json.Unmarshal([]byte(requestText), &request); err != nil {
		request = nil
	}
	model := requestModel(request, capture.Path)
	usage := responseTokenUsage(capture.Renderer, capture.ResponseBody)
	parts := []string{renderMeta(capture, object(request), model, usage)}
	if len(usage) > 0 {
		parts = append(parts, details("Token usage details", fenceJSON(usage)))
	}
	if headers := renderHeaders(capture.RequestHeaders); headers != "" {
		parts = append(parts, details(fmt.Sprintf("Request headers (%d)", len(capture.RequestHeaders)), headers))
	}
	parts = append(parts,
		"## Request\n\n"+renderRequest(capture.Renderer, request, requestText, decodeWarning),
		"## Response\n\n"+renderResponse(capture.Renderer, capture.ResponseBody),
	)
	return strings.Join(nonEmpty(parts), "\n\n") + "\n"
}

// decodeRequestBody decodes common content encodings without changing saved request bytes.
func decodeRequestBody(body []byte, encoding string) (requestText string, warning string) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "identity":
		return string(body), ""
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return string(body), fmt.Sprintf("Could not decode gzip request body: %v", err)
		}
		defer reader.Close()
		decoded, err := io.ReadAll(reader)
		if err != nil {
			return string(body), fmt.Sprintf("Could not decode gzip request body: %v", err)
		}
		return string(decoded), ""
	case "deflate":
		reader, err := zlib.NewReader(bytes.NewReader(body))
		if err != nil {
			return string(body), fmt.Sprintf("Could not decode deflate request body: %v", err)
		}
		defer reader.Close()
		decoded, err := io.ReadAll(reader)
		if err != nil {
			return string(body), fmt.Sprintf("Could not decode deflate request body: %v", err)
		}
		return string(decoded), ""
	case "br":
		return string(body), "This request body uses Brotli compression, which the standard-library build cannot decode. The exact bytes remain in the .request.txt file."
	case "zstd":
		return string(body), "This request body uses zstd compression, which the standard-library build cannot decode. The exact bytes remain in the .request.txt file."
	default:
		return string(body), fmt.Sprintf("Unsupported request content encoding %q; exact bytes remain in the .request.txt file.", encoding)
	}
}

// renderMeta gives each capture a useful title and a compact summary before the full request.
func renderMeta(capture Capture, request map[string]any, model string, usage map[string]any) string {
	lines := []string{
		fmt.Sprintf("# %s — `%s`", capture.AgentLabel, model),
		"",
		"## Summary",
		"",
		"- **Captured:** " + capture.Timestamp.UTC().Format("2006-01-02 15:04:05.000 UTC"),
		"- **Wire format:** " + string(capture.Renderer),
		fmt.Sprintf("- **Endpoint:** `%s %s`", capture.Method, capture.Path),
		fmt.Sprintf("- **Upstream status:** %d", capture.StatusCode),
	}
	messageCount, hasMessages, toolNames := requestSummary(capture.Renderer, request)
	if hasMessages {
		lines = append(lines, fmt.Sprintf("- **Messages:** %d", messageCount))
	}
	if len(toolNames) > 0 {
		quoted := make([]string, len(toolNames))
		for index, name := range toolNames {
			quoted[index] = "`" + name + "`"
		}
		lines = append(lines, fmt.Sprintf("- **Tools:** %d (%s)", len(toolNames), strings.Join(quoted, ", ")))
	}
	if summary := tokenUsageSummary(usage); summary != "" {
		lines = append(lines, "- **Token usage:** "+summary)
	}
	return strings.Join(lines, "\n")
}

// responseTokenUsage combines token usage fields from JSON and streamed responses.
func tokenUsageSummary(usage map[string]any) string {
	fields := []struct {
		label string
		keys  []string
	}{
		{label: "input", keys: []string{"input_tokens", "prompt_tokens", "promptTokenCount"}},
		{label: "output", keys: []string{"output_tokens", "completion_tokens", "candidatesTokenCount"}},
		{label: "total", keys: []string{"total_tokens", "totalTokenCount"}},
	}
	var summary []string
	for _, field := range fields {
		for _, key := range field.keys {
			if value, ok := usage[key]; ok {
				summary = append(summary, fmt.Sprintf("%s: %v", field.label, value))
				break
			}
		}
	}
	return strings.Join(summary, " · ")
}

// responseTokenUsage extracts structured token counters from JSON and streamed responses.
func responseTokenUsage(renderer Renderer, body []byte) map[string]any {
	usage := make(map[string]any)
	mergeUsage := func(raw any) {
		maps.Copy(usage, object(raw))
	}
	if looksLikeSSE(string(body)) {
		for _, raw := range sseEvents(string(body)) {
			event := object(raw)
			switch eventType := stringValue(event["type"], ""); renderer {
			case RendererAnthropic:
				switch eventType {
				case "message_start":
					mergeUsage(object(event["message"])["usage"])
				case "message_delta":
					mergeUsage(event["usage"])
				}
			case RendererOpenAI:
				mergeUsage(event["usage"])
				if event["type"] == "response.completed" {
					mergeUsage(object(event["response"])["usage"])
				}
			case RendererGemini:
				if response, ok := event["response"]; ok {
					event = object(response)
				}
				mergeUsage(event["usageMetadata"])
			}
		}
		return usage
	}
	var response any
	if err := json.Unmarshal(body, &response); err != nil {
		return usage
	}
	responseMap := object(response)
	switch renderer {
	case RendererAnthropic, RendererOpenAI:
		mergeUsage(responseMap["usage"])
		mergeUsage(object(responseMap["response"])["usage"])
	case RendererGemini:
		mergeUsage(responseMap["usageMetadata"])
		mergeUsage(object(responseMap["response"])["usageMetadata"])
	}
	return usage
}

// requestSummary reads message counts and declared tool names for the capture overview.
func requestSummary(renderer Renderer, request map[string]any) (int, bool, []string) {
	messages := array(request["messages"])
	tools := array(request["tools"])
	hasMessages := len(messages) > 0
	switch renderer {
	case RendererAnthropic:
		_, hasMessages = request["messages"]
	case RendererOpenAI:
		if len(messages) == 0 {
			if input, ok := request["input"].(string); ok {
				messages = []any{input}
			} else {
				for _, item := range array(request["input"]) {
					if stringValue(object(item)["type"], "message") == "message" {
						messages = append(messages, item)
					}
				}
			}
		}
		_, hasInput := request["input"]
		hasMessages = hasMessages || hasInput
	case RendererGemini:
		if nested, ok := request["request"]; ok {
			request = object(nested)
		}
		messages = array(request["contents"])
		tools = array(request["tools"])
		_, hasMessages = request["contents"]
	}
	toolNames := make([]string, 0, len(tools))
	for _, raw := range tools {
		tool := object(raw)
		if renderer == RendererGemini {
			for _, declaration := range array(tool["functionDeclarations"]) {
				toolNames = append(toolNames, stringValue(object(declaration)["name"], "(unnamed)"))
			}
			continue
		}
		function := object(tool["function"])
		if len(function) == 0 {
			function = tool
		}
		toolNames = append(toolNames, stringValue(function["name"], "(unnamed)"))
	}
	return len(messages), hasMessages, toolNames
}

// renderHeaders writes request headers while masking common credential fields.
func renderHeaders(headers map[string][]string) string {
	if len(headers) == 0 {
		return ""
	}
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	lines := []string{"```text"}
	for _, key := range keys {
		value := strings.Join(headers[key], ", ")
		if redactedHeaders[strings.ToLower(key)] {
			value = "[REDACTED]"
		}
		lines = append(lines, key+": "+value)
	}
	lines = append(lines, "```")
	return strings.Join(lines, "\n")
}

// renderRequest selects a protocol-specific request view or a safe raw JSON fallback.
func renderRequest(renderer Renderer, request any, requestText, decodeWarning string) string {
	if decodeWarning != "" {
		return "[!WARNING]\n" + decodeWarning + "\n\nThe request body is preserved byte-for-byte in `.request.txt`; it is not shown here because it could be binary."
	}
	if request == nil {
		return jsonOrText(request, requestText)
	}
	requestMap := object(request)
	switch renderer {
	case RendererAnthropic:
		return renderAnthropicRequest(requestMap)
	case RendererOpenAI:
		return renderOpenAIRequest(requestMap)
	case RendererGemini:
		return renderGeminiRequest(requestMap)
	case RendererRaw:
		return jsonOrText(request, requestText)
	default:
		return fence(requestText, "")
	}
}

// renderAnthropicRequest extracts the system prompt, tools, parameters, and messages.
func renderAnthropicRequest(request map[string]any) string {
	parts := renderParams(request, []string{"max_tokens", "temperature", "top_p", "top_k", "stream", "stop_sequences", "tool_choice", thinkingContentType, "metadata"})
	if system, ok := request["system"]; ok {
		content := renderAnthropicContent(system)
		parts = append(parts, details(fmt.Sprintf("System prompt (%d characters)", len(content)), content))
	}
	if tools := array(request["tools"]); len(tools) > 0 {
		parts = append(parts, renderAnthropicTools(tools))
	}
	parts = append(parts, renderMessageList(array(request["messages"]), renderAnthropicMessage))
	return strings.Join(nonEmpty(parts), "\n\n")
}

// renderAnthropicTools formats Messages API tool schemas.
func renderAnthropicTools(tools []any) string {
	rendered := make([]string, 0, len(tools))
	for _, value := range tools {
		tool := object(value)
		name := stringValue(tool["name"], "(unnamed tool)")
		lines := []string{"### " + name, ""}
		if description := stringValue(tool["description"], ""); description != "" {
			lines = append(lines, description, "")
		}
		if schema, ok := tool["input_schema"]; ok {
			lines = append(lines, fenceJSON(schema))
		}
		rendered = append(rendered, strings.Join(lines, "\n"))
	}
	return details(fmt.Sprintf("Tool definitions (%d)", len(rendered)), strings.Join(rendered, "\n\n"))
}

// renderAnthropicMessage renders a message's text, tool use, tool results, and images.
func renderAnthropicMessage(message any) string {
	item := object(message)
	return renderAnthropicContent(item["content"])
}

// renderAnthropicContent formats content blocks while replacing embedded image bytes with a note.
func renderAnthropicContent(content any) string {
	if text, ok := content.(string); ok {
		return renderMarkdownText(text)
	}
	blocks, ok := content.([]any)
	if !ok {
		return fenceJSON(content)
	}
	var parts []string
	for _, raw := range blocks {
		block := object(raw)
		switch stringValue(block["type"], "") {
		case textContentType:
			parts = append(parts, renderMarkdownText(stringValue(block[textContentType], "")))
		case thinkingContentType:
			parts = append(parts, details("Thinking", stringValue(block[thinkingContentType], "")))
		case "tool_use":
			parts = append(parts, renderToolUse(stringValue(block["name"], ""), stringValue(block["id"], ""), fenceJSON(block["input"])))
		case "tool_result":
			label := "Tool result"
			if id := stringValue(block["tool_use_id"], ""); id != "" {
				label += " for " + id
			}
			parts = append(parts, "### "+label+"\n\n"+renderAnthropicContent(block["content"]))
		case "image":
			parts = append(parts, imageNote(object(block["source"])))
		default:
			parts = append(parts, fenceJSON(raw))
		}
	}
	return strings.Join(parts, "\n\n")
}

// renderOpenAIRequest extracts Responses and Chat Completions request fields.
func renderOpenAIRequest(request map[string]any) string {
	parts := renderParams(request, []string{"max_tokens", "max_output_tokens", "temperature", "top_p", "stream", "stop", "tool_choice", "reasoning", "metadata"})
	if instructions := stringValue(request["instructions"], ""); instructions != "" {
		instructions = renderMarkdownText(instructions)
		parts = append(parts, details(fmt.Sprintf("System prompt (%d characters)", len(instructions)), instructions))
	}
	if tools := array(request["tools"]); len(tools) > 0 {
		parts = append(parts, renderOpenAITools(tools))
	}
	if messages := array(request["messages"]); len(messages) > 0 {
		parts = append(parts, renderMessageList(messages, renderOpenAIChatMessage))
	} else if input, ok := request["input"].(string); ok {
		parts = append(parts, renderMessageList([]any{map[string]any{"role": "user", "content": input}}, renderOpenAIChatMessage))
	} else if input := array(request["input"]); len(input) > 0 {
		parts = append(parts, renderMessageList(input, renderOpenAIResponseItem))
	}
	return strings.Join(nonEmpty(parts), "\n\n")
}

// renderOpenAITools formats function tools from either supported OpenAI request shape.
func renderOpenAITools(tools []any) string {
	rendered := make([]string, 0, len(tools))
	for _, raw := range tools {
		tool := object(raw)
		function := object(tool["function"])
		if len(function) == 0 {
			function = tool
		}
		name := stringValue(function["name"], stringValue(tool["type"], "(unnamed tool)"))
		lines := []string{"### " + name, ""}
		if description := stringValue(function["description"], ""); description != "" {
			lines = append(lines, description, "")
		}
		if schema, ok := function["parameters"]; ok {
			lines = append(lines, fenceJSON(schema))
		}
		rendered = append(rendered, strings.Join(lines, "\n"))
	}
	return details(fmt.Sprintf("Tool definitions (%d)", len(rendered)), strings.Join(rendered, "\n\n"))
}

// renderOpenAIChatMessage formats Chat Completions message content and tool calls.
func renderOpenAIChatMessage(raw any) string {
	message := object(raw)
	var parts []string
	content := message["content"]
	if text, ok := content.(string); ok {
		parts = append(parts, renderMarkdownText(text))
	} else if blocks, ok := content.([]any); ok {
		for _, block := range blocks {
			item := object(block)
			if item["type"] == "image_url" {
				parts = append(parts, "`[image_url — full data remains in .request.txt]`")
			} else {
				parts = append(parts, contentText(block))
			}
		}
	}
	for _, call := range array(message["tool_calls"]) {
		toolCall := object(call)
		function := object(toolCall["function"])
		parts = append(parts, renderToolUse(stringValue(function["name"], ""), stringValue(toolCall["id"], ""), fenceJSONText(stringValue(function["arguments"], "{}"))))
	}
	if stringValue(message["role"], "") == "tool" {
		parts = append(parts, "### Tool result for "+stringValue(message["tool_call_id"], "unknown")+"\n\n"+contentText(content))
	}
	return strings.Join(nonEmpty(parts), "\n\n")
}

// renderOpenAIResponseItem formats Responses API messages, calls, and results.
func renderOpenAIResponseItem(raw any) string {
	item := object(raw)
	switch stringValue(item["type"], "message") {
	case "function_call":
		return renderToolUse(stringValue(item["name"], ""), stringValue(item["call_id"], stringValue(item["id"], "")), fenceJSONText(stringValue(item["arguments"], "{}")))
	case "function_call_output":
		return "### Tool result for " + stringValue(item["call_id"], "unknown") + "\n\n" + contentText(item["output"])
	default:
		return contentText(item["content"])
	}
}

// renderGeminiRequest extracts the system instruction, tools, generation settings, and contents.
func renderGeminiRequest(raw any) string {
	request := object(raw)
	if nested, ok := request["request"]; ok {
		request = object(nested)
	}
	parts := renderParams(object(request["generationConfig"]), []string{"temperature", "topP", "topK", "maxOutputTokens", "candidateCount", "stopSequences", "responseMimeType", "thinkingConfig"})
	if system, ok := request["systemInstruction"]; ok {
		var content string
		if text, isText := system.(string); isText {
			content = renderMarkdownText(text)
		} else {
			content = renderGeminiParts(object(system)["parts"])
		}
		parts = append(parts, details(fmt.Sprintf("System prompt (%d characters)", len(content)), content))
	}
	if tools := array(request["tools"]); len(tools) > 0 {
		parts = append(parts, renderGeminiTools(tools))
	}
	parts = append(parts, renderMessageList(array(request["contents"]), renderGeminiMessage))
	return strings.Join(nonEmpty(parts), "\n\n")
}

// renderGeminiTools formats declared functions and built-in tool settings.
func renderGeminiTools(tools []any) string {
	var rendered []string
	for _, raw := range tools {
		tool := object(raw)
		declarations := array(tool["functionDeclarations"])
		for _, declaration := range declarations {
			function := object(declaration)
			text := "### " + stringValue(function["name"], "(unnamed tool)") + "\n\n"
			if description := stringValue(function["description"], ""); description != "" {
				text += description + "\n\n"
			}
			schema := function["parametersJsonSchema"]
			if schema == nil {
				schema = function["parameters"]
			}
			if schema != nil {
				text += fenceJSON(schema)
			}
			rendered = append(rendered, text)
		}
		if len(declarations) == 0 {
			for name, value := range tool {
				rendered = append(rendered, "### "+name+"\n\n"+fenceJSON(value))
			}
		}
	}
	return details(fmt.Sprintf("Tool definitions (%d)", len(rendered)), strings.Join(rendered, "\n\n"))
}

// renderGeminiMessage formats one Gemini content item.
func renderGeminiMessage(raw any) string {
	return renderGeminiParts(object(raw)["parts"])
}

// renderGeminiParts formats Gemini text, thought, function, and image parts.
func renderGeminiParts(raw any) string {
	if text, ok := raw.(string); ok {
		return renderMarkdownText(text)
	}
	var parts []string
	for _, value := range array(raw) {
		part := object(value)
		if text, ok := part[textContentType].(string); ok {
			if thought, ok := part["thought"].(bool); ok && thought {
				parts = append(parts, details("Thinking", renderMarkdownText(text)))
			} else {
				parts = append(parts, renderMarkdownText(text))
			}
		} else if call, ok := part["functionCall"]; ok {
			function := object(call)
			parts = append(parts, renderToolUse(stringValue(function["name"], ""), stringValue(function["id"], ""), fenceJSON(function["args"])))
		} else if result, ok := part["functionResponse"]; ok {
			function := object(result)
			label := "Tool result"
			if name := stringValue(function["name"], ""); name != "" {
				label += ": " + name
			}
			parts = append(parts, "### "+label+"\n\n"+contentText(function["response"]))
		} else if data, ok := part["inlineData"]; ok {
			parts = append(parts, imageNote(object(data)))
		} else {
			parts = append(parts, fenceJSON(value))
		}
	}
	return strings.Join(parts, "\n\n")
}

// renderResponse shows JSON responses directly and reconstructs common SSE streams.
func renderResponse(renderer Renderer, body []byte) string {
	raw := string(body)
	if strings.TrimSpace(raw) == "" {
		return "_(empty response)_"
	}
	if !looksLikeSSE(raw) {
		var value any
		if err := json.Unmarshal(body, &value); err == nil {
			return fenceJSON(value)
		}
		return fence(raw, "")
	}
	events := sseEvents(raw)
	switch renderer {
	case RendererAnthropic:
		return renderAnthropicEvents(events, raw)
	case RendererOpenAI:
		return renderOpenAIEvents(events, raw)
	case RendererGemini:
		return renderGeminiEvents(events, raw)
	case RendererRaw:
		return renderRawSSEEvents(events, raw)
	default:
		return fence(raw, "")
	}
}

// renderRawSSEEvents shows one JSON block for every parsed event without guessing at its protocol.
func renderRawSSEEvents(events []any, raw string) string {
	blocks := make([]string, 0, len(events))
	for _, event := range events {
		blocks = append(blocks, fenceJSON(event))
	}
	if len(blocks) == 0 {
		return fence(raw, "")
	}
	return strings.Join(blocks, "\n\n")
}

// renderAnthropicEvents rejoins text and tool input deltas from a Messages API stream.
func renderAnthropicEvents(events []any, raw string) string {
	blocks := make(map[int]map[string]any)
	var stopReason string
	for _, rawEvent := range events {
		event := object(rawEvent)
		switch stringValue(event["type"], "") {
		case "content_block_start":
			index := intValue(event["index"])
			block := object(event["content_block"])
			blocks[index] = map[string]any{"type": block["type"], textContentType: "", "name": block["name"], "id": block["id"]}
		case "content_block_delta":
			index := intValue(event["index"])
			block := blocks[index]
			if block == nil {
				continue
			}
			delta := object(event["delta"])
			for _, key := range []string{textContentType, "partial_json", thinkingContentType} {
				if text := stringValue(delta[key], ""); text != "" {
					block[textContentType] = stringValue(block[textContentType], "") + text
				}
			}
		case "message_delta":
			delta := object(event["delta"])
			stopReason = stringValue(delta["stop_reason"], stopReason)
		}
	}
	var parts []string
	if stopReason != "" {
		parts = append(parts, "- **stop reason**: "+stopReason)
	}
	keys := make([]int, 0, len(blocks))
	for index := range blocks {
		keys = append(keys, index)
	}
	slices.Sort(keys)
	for _, index := range keys {
		block := blocks[index]
		text := stringValue(block[textContentType], "")
		switch stringValue(block["type"], textContentType) {
		case textContentType:
			parts = append(parts, "### Assistant response\n\n"+renderMarkdownText(text))
		case thinkingContentType:
			parts = append(parts, details("Thinking", renderMarkdownText(text)))
		case "tool_use":
			parts = append(parts, renderToolUse(stringValue(block["name"], ""), stringValue(block["id"], ""), fenceJSONText(text)))
		}
	}
	if len(parts) == 0 {
		return fence(raw, "")
	}
	return strings.Join(parts, "\n\n")
}

// renderOpenAIEvents reconstructs Chat Completions and Responses API output streams.
func renderOpenAIEvents(events []any, raw string) string {
	responsesAPI := false
	for _, value := range events {
		if strings.HasPrefix(stringValue(object(value)["type"], ""), "response.") {
			responsesAPI = true
			break
		}
	}
	if responsesAPI {
		return renderOpenAIResponsesEvents(events, raw)
	}
	var text strings.Builder
	tools := make(map[int]map[string]string)
	var finish string
	for _, value := range events {
		event := object(value)
		choices := array(event["choices"])
		if len(choices) == 0 {
			continue
		}
		choice := object(choices[0])
		delta := object(choice["delta"])
		text.WriteString(stringValue(delta["content"], ""))
		for _, rawCall := range array(delta["tool_calls"]) {
			call := object(rawCall)
			index := intValue(call["index"])
			if tools[index] == nil {
				tools[index] = map[string]string{}
			}
			function := object(call["function"])
			tools[index]["name"] += stringValue(function["name"], "")
			tools[index]["arguments"] += stringValue(function["arguments"], "")
			tools[index]["id"] = stringValue(call["id"], tools[index]["id"])
		}
		finish = stringValue(choice["finish_reason"], finish)
	}
	var parts []string
	if finish != "" {
		parts = append(parts, "- **finish reason**: "+finish)
	}
	if text.Len() > 0 {
		parts = append(parts, "### Assistant response\n\n"+renderMarkdownText(text.String()))
	}
	keys := make([]int, 0, len(tools))
	for index := range tools {
		keys = append(keys, index)
	}
	slices.Sort(keys)
	for _, index := range keys {
		tool := tools[index]
		parts = append(parts, renderToolUse(tool["name"], tool["id"], fenceJSONText(defaultString(tool["arguments"], "{}"))))
	}
	if len(parts) == 0 {
		return fence(raw, "")
	}
	return strings.Join(parts, "\n\n")
}

// renderOpenAIResponsesEvents reconstructs the Responses API event stream.
func renderOpenAIResponsesEvents(events []any, raw string) string {
	var text strings.Builder
	tools := make(map[string]map[string]string)
	var status string
	for _, value := range events {
		event := object(value)
		key := stringValue(event["item_id"], "0")
		switch stringValue(event["type"], "") {
		case "response.output_text.delta":
			text.WriteString(stringValue(event["delta"], ""))
		case "response.function_call_arguments.delta":
			if tools[key] == nil {
				tools[key] = map[string]string{}
			}
			tools[key]["arguments"] += stringValue(event["delta"], "")
		case "response.output_item.added":
			item := object(event["item"])
			if stringValue(item["type"], "") == "function_call" {
				key = stringValue(item["id"], key)
				if tools[key] == nil {
					tools[key] = map[string]string{}
				}
				tools[key]["name"] = stringValue(item["name"], "")
			}
		case "response.completed":
			response := object(event["response"])
			status = stringValue(response["status"], status)
		}
	}
	var parts []string
	if status != "" {
		parts = append(parts, "- **status**: "+status)
	}
	if text.Len() > 0 {
		parts = append(parts, "### Assistant response\n\n"+renderMarkdownText(text.String()))
	}
	for key, tool := range tools {
		parts = append(parts, renderToolUse(tool["name"], key, fenceJSONText(defaultString(tool["arguments"], "{}"))))
	}
	if len(parts) == 0 {
		return fence(raw, "")
	}
	return strings.Join(parts, "\n\n")
}

// renderGeminiEvents reconstructs text and function calls from Gemini SSE data.
func renderGeminiEvents(events []any, raw string) string {
	var text strings.Builder
	var thinking strings.Builder
	var calls []string
	var finish string
	for _, value := range events {
		event := object(value)
		if nested, ok := event["response"]; ok {
			event = object(nested)
		}
		candidates := array(event["candidates"])
		if len(candidates) > 0 {
			candidate := object(candidates[0])
			content := object(candidate["content"])
			for _, partValue := range array(content["parts"]) {
				part := object(partValue)
				if value, ok := part[textContentType].(string); ok {
					if thought, hasThought := part["thought"].(bool); hasThought && thought {
						thinking.WriteString(value)
					} else {
						text.WriteString(value)
					}
				}
				if call, ok := part["functionCall"]; ok {
					function := object(call)
					calls = append(calls, renderToolUse(stringValue(function["name"], ""), "", fenceJSON(function["args"])))
				}
			}
			finish = stringValue(candidate["finishReason"], finish)
		}
	}
	var parts []string
	if finish != "" {
		parts = append(parts, "- **finish reason**: "+finish)
	}
	if thinking.Len() > 0 {
		parts = append(parts, details("Thinking", renderMarkdownText(thinking.String())))
	}
	if text.Len() > 0 {
		parts = append(parts, "### Assistant response\n\n"+renderMarkdownText(text.String()))
	}
	parts = append(parts, calls...)
	if len(parts) == 0 {
		return fence(raw, "")
	}
	return strings.Join(parts, "\n\n")
}

// sseEvents returns decoded JSON data fields from a server-sent event stream.
func sseEvents(raw string) []any {
	var events []any
	for line := range strings.SplitSeq(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "" || data == "[DONE]" {
			continue
		}
		var event any
		if err := json.Unmarshal([]byte(data), &event); err == nil {
			events = append(events, event)
		}
	}
	return events
}

// looksLikeSSE reports whether the response contains server-sent event lines.
func looksLikeSSE(raw string) bool {
	return strings.Contains(raw, "data:") || strings.Contains(raw, "event:")
}

// renderMessageList separates each conversation turn under a role-labelled heading.
func renderMessageList(messages []any, renderContent func(any) string) string {
	rendered := make([]string, 0, len(messages))
	for index, raw := range messages {
		message := object(raw)
		role := stringValue(message["role"], stringValue(message["type"], "unknown"))
		if role != "" {
			role = strings.ToUpper(role[:1]) + role[1:]
		}
		rendered = append(rendered, fmt.Sprintf("### %s message %d\n\n%s", role, index+1, renderContent(raw)))
	}
	return strings.Join(rendered, "\n\n")
}

// renderParams lists only parameters present in the request object.
func renderParams(values map[string]any, keys []string) []string {
	var entries []string
	for _, key := range keys {
		value, ok := values[key]
		if !ok {
			continue
		}
		switch value.(type) {
		case map[string]any, []any:
			block := strings.ReplaceAll(fenceJSON(value), "\n", "\n  ")
			entries = append(entries, "- **"+key+"**:\n\n  "+block)
		default:
			entries = append(entries, "- **"+key+"**: `"+compactJSON(value)+"`")
		}
	}
	if len(entries) == 0 {
		return nil
	}
	return []string{"### Parameters\n\n" + strings.Join(entries, "\n")}
}

// requestModel reads the model from a request body or a Gemini endpoint path.
func requestModel(request any, path string) string {
	if model := stringValue(object(request)["model"], ""); model != "" {
		return model
	}
	if _, after, ok := strings.Cut(path, "/models/"); ok {
		model := after
		for _, separator := range []string{":", "/", "?"} {
			if end := strings.Index(model, separator); end >= 0 {
				model = model[:end]
			}
		}
		if model != "" {
			return model
		}
	}
	return "unknown"
}

// contentText keeps strings readable and formats structured values as JSON.
func contentText(value any) string {
	if text, ok := value.(string); ok {
		return renderMarkdownText(text)
	}
	if blocks, ok := value.([]any); ok {
		var parts []string
		for _, block := range blocks {
			item := object(block)
			if text, ok := item[textContentType].(string); ok {
				parts = append(parts, renderMarkdownText(text))
			} else {
				parts = append(parts, fenceJSON(block))
			}
		}
		return strings.Join(parts, "\n\n")
	}
	return fenceJSON(value)
}

// imageNote describes image payloads without duplicating base64 into Markdown.
func imageNote(source map[string]any) string {
	data := stringValue(source["data"], "")
	kind := stringValue(source["media_type"], stringValue(source["mimeType"], stringValue(source["type"], "unknown")))
	return fmt.Sprintf("`[image: %s, %d base64 chars — full data remains in .request.txt]`", kind, len(data))
}

// object returns a map value or an empty map for a different JSON shape.
func object(value any) map[string]any {
	result, ok := value.(map[string]any)
	if !ok || result == nil {
		return map[string]any{}
	}
	return result
}

// array returns a JSON array or nil for another value type.
func array(value any) []any {
	result, ok := value.([]any)
	if !ok {
		return nil
	}
	return result
}

// stringValue returns a string value or its caller-provided fallback.
func stringValue(value any, fallback string) string {
	result, ok := value.(string)
	if !ok {
		return fallback
	}
	return result
}

// intValue converts a JSON number to an integer index.
func intValue(value any) int {
	number, ok := value.(float64)
	if !ok {
		return 0
	}
	return int(number)
}

// compactJSON serializes a value on one line for parameter summaries.
func compactJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(encoded)
}

// fenceJSONText parses and pretty-prints JSON text when possible.
func fenceJSONText(text string) string {
	var value any
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &value); err == nil {
		return fenceJSON(value)
	}
	return fence(text, jsonLanguage)
}

// fenceJSON renders a value in a fenced JSON block.
func fenceJSON(value any) string {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "```json\nnull\n```"
	}
	return "```json\n" + string(encoded) + "\n```"
}

// fence wraps text in a Markdown code fence with an optional language label.
func fence(text, language string) string {
	return "```" + language + "\n" + text + "\n```"
}

// details folds verbose content under a readable summary while preserving the original text.
func details(summary, content string) string {
	return detailsWithHTMLSummary(html.EscapeString(summary), content)
}

// detailsWithHTMLSummary wraps content when its summary already contains safe inline HTML.
func detailsWithHTMLSummary(summary, content string) string {
	return "<details>\n<summary>" + summary + "</summary>\n\n" + content + "\n\n</details>"
}

// renderMarkdownText preserves prose Markdown, formats standalone JSON, paths, and large AGENTS files.
func renderMarkdownText(text string) string {
	trimmed := strings.TrimSpace(text)
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		var value any
		if json.Unmarshal([]byte(trimmed), &value) == nil {
			return fenceJSON(value)
		}
	}
	return renderAgentMarkdown(text)
}

// renderAgentMarkdown collapses injected AGENTS.md content while leaving its Markdown rendered.
func renderAgentMarkdown(text string) string {
	lines := strings.Split(text, "\n")
	var sections []string
	var plain []string
	flushPlain := func() {
		if len(plain) == 0 {
			return
		}
		sections = append(sections, formatMarkdownPaths(strings.Join(plain, "\n")))
		plain = nil
	}
	for index := 0; index < len(lines); {
		path, suffix, ok := agentsMarkdownHeader(lines[index])
		if !ok {
			plain = append(plain, lines[index])
			index++
			continue
		}
		flushPlain()
		end := index + 1
		for end < len(lines) {
			line := strings.TrimSpace(lines[end])
			if strings.HasPrefix(line, "Contents of ") && strings.Contains(line, ".md") {
				break
			}
			end++
		}
		summary := "Contents of <code>" + html.EscapeString(path) + "</code>" + html.EscapeString(suffix)
		content := strings.Trim(formatMarkdownPaths(strings.Join(lines[index+1:end], "\n")), "\n")
		sections = append(sections, detailsWithHTMLSummary(summary, content))
		index = end
	}
	flushPlain()
	return strings.Join(nonEmpty(sections), "\n\n")
}

// agentsMarkdownHeader identifies a prompt section that embeds an AGENTS.md file.
func agentsMarkdownHeader(line string) (path, suffix string, ok bool) {
	content, found := strings.CutPrefix(strings.TrimSpace(line), "Contents of ")
	if !found {
		return "", "", false
	}
	end := strings.Index(content, ".md")
	if end < 0 {
		return "", "", false
	}
	path = content[:end+3]
	if !strings.HasSuffix(path, "/AGENTS.md") && path != "AGENTS.md" {
		return "", "", false
	}
	return path, content[end+3:], true
}

// formatMarkdownPaths marks Markdown file paths as inline code outside fenced code blocks.
func formatMarkdownPaths(text string) string {
	lines := strings.Split(text, "\n")
	inFence := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		matches := markdownFilePath.FindAllStringIndex(line, -1)
		if len(matches) == 0 {
			continue
		}
		var formatted strings.Builder
		last := 0
		for _, match := range matches {
			formatted.WriteString(line[last:match[0]])
			path := line[match[0]:match[1]]
			alreadyCode := match[0] > 0 && line[match[0]-1] == '`' || match[1] < len(line) && line[match[1]] == '`'
			urlPath := match[0] > 0 && (line[match[0]-1] == ':' || line[match[0]-1] == '/')
			if alreadyCode || urlPath {
				formatted.WriteString(path)
			} else {
				formatted.WriteByte('`')
				formatted.WriteString(path)
				formatted.WriteByte('`')
			}
			last = match[1]
		}
		formatted.WriteString(line[last:])
		lines[index] = formatted.String()
	}
	return strings.Join(lines, "\n")
}

// renderToolUse labels a tool call and folds its argument payload out of the conversation.
func renderToolUse(name, id, arguments string) string {
	heading := "### Tool call: " + name
	if id != "" {
		heading += " (`" + id + "`)"
	}
	return heading + "\n\n" + details("Arguments", arguments)
}

// jsonOrText returns parsed JSON when available, otherwise the original text.
func jsonOrText(value any, text string) string {
	if value == nil {
		return fence(text, "")
	}
	return fenceJSON(value)
}

// nonEmpty removes empty strings while keeping the order of rendered sections.
func nonEmpty(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result = append(result, value)
		}
	}
	return result
}

// defaultString substitutes fallback when a string has no content.
func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
