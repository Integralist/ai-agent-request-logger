package main

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

var redactedHeaders = map[string]bool{
	"authorization":  true,
	"x-api-key":      true,
	"api-key":        true,
	"x-goog-api-key": true,
}

// RenderMarkdown creates a readable, complete record of one captured exchange.
func RenderMarkdown(capture Capture) string {
	requestText, decodeWarning := decodeRequestBody(capture.RequestBody, capture.RequestEncoding)
	var request any
	if err := json.Unmarshal([]byte(requestText), &request); err != nil {
		request = nil
	}
	model := requestModel(request, capture.Path)
	parts := []string{
		renderMeta(capture, model),
		renderHeaders(capture.RequestHeaders),
		"<request>\n\n" + renderRequest(capture.Renderer, request, requestText, decodeWarning) + "\n\n</request>",
		"<response>\n\n" + renderResponse(capture.Renderer, capture.ResponseBody) + "\n\n</response>",
	}
	return strings.Join(parts, "\n\n") + "\n"
}

// decodeRequestBody decodes common content encodings without changing saved request bytes.
func decodeRequestBody(body []byte, encoding string) (string, string) {
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

// renderMeta writes stable request details before the captured content.
func renderMeta(capture Capture, model string) string {
	return fmt.Sprintf("<meta>\n\n- **timestamp**: %s\n- **agent**: %s\n- **wire format**: %s\n- **model**: %s\n- **endpoint**: %s %s\n- **upstream status**: %d\n\n</meta>", capture.Timestamp.Format("2006-01-02T15:04:05.000Z"), capture.AgentLabel, capture.Renderer, model, capture.Method, capture.Path, capture.StatusCode)
}

// renderHeaders writes request headers while masking common credential fields.
func renderHeaders(headers map[string][]string) string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	lines := []string{"<headers>", "", "```"}
	for _, key := range keys {
		value := strings.Join(headers[key], ", ")
		if redactedHeaders[strings.ToLower(key)] {
			value = "[REDACTED]"
		}
		lines = append(lines, key+": "+value)
	}
	lines = append(lines, "```", "", "</headers>")
	return strings.Join(lines, "\n")
}

// renderRequest selects a protocol-specific request view or a safe raw JSON fallback.
func renderRequest(renderer Renderer, request any, requestText, decodeWarning string) string {
	if decodeWarning != "" {
		return "[!WARNING]\n" + decodeWarning + "\n\nThe request body is preserved byte-for-byte in `.request.txt`; it is not shown here because it could be binary."
	}
	if renderer == RendererRaw || request == nil {
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
	default:
		return jsonOrText(request, requestText)
	}
}

// renderAnthropicRequest extracts the system prompt, tools, parameters, and messages.
func renderAnthropicRequest(request map[string]any) string {
	parts := renderParams(request, []string{"max_tokens", "temperature", "top_p", "top_k", "stream", "stop_sequences", "tool_choice", "thinking", "metadata"})
	if system, ok := request["system"]; ok {
		parts = append(parts, tag("system-prompt", renderAnthropicContent(system)))
	}
	if tools := array(request["tools"]); len(tools) > 0 {
		parts = append(parts, renderAnthropicTools(tools))
	}
	parts = append(parts, renderMessageList(array(request["messages"]), renderAnthropicMessage))
	return strings.Join(nonEmpty(parts), "\n\n")
}

// renderAnthropicTools formats Messages API tool schemas.
func renderAnthropicTools(tools []any) string {
	var rendered []string
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
	return tag("tools", strings.Join(rendered, "\n\n"))
}

// renderAnthropicMessage renders a message's text, tool use, tool results, and images.
func renderAnthropicMessage(message any) string {
	item := object(message)
	return renderAnthropicContent(item["content"])
}

// renderAnthropicContent formats content blocks while replacing embedded image bytes with a note.
func renderAnthropicContent(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	blocks, ok := content.([]any)
	if !ok {
		return fenceJSON(content)
	}
	var parts []string
	for _, raw := range blocks {
		block := object(raw)
		switch stringValue(block["type"], "") {
		case "text":
			parts = append(parts, stringValue(block["text"], ""))
		case "thinking":
			parts = append(parts, tag("thinking", stringValue(block["thinking"], "")))
		case "tool_use":
			parts = append(parts, fmt.Sprintf("<tool-use name=%q id=%q>\n\n%s\n\n</tool-use>", stringValue(block["name"], ""), stringValue(block["id"], ""), fenceJSON(block["input"])))
		case "tool_result":
			parts = append(parts, fmt.Sprintf("<tool-result tool-use-id=%q is-error=%q>\n\n%s\n\n</tool-result>", stringValue(block["tool_use_id"], ""), fmt.Sprint(block["is_error"]), renderAnthropicContent(block["content"])))
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
		parts = append(parts, tag("system-prompt", instructions))
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
	var rendered []string
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
	return tag("tools", strings.Join(rendered, "\n\n"))
}

// renderOpenAIChatMessage formats Chat Completions message content and tool calls.
func renderOpenAIChatMessage(raw any) string {
	message := object(raw)
	var parts []string
	content := message["content"]
	if text, ok := content.(string); ok {
		parts = append(parts, text)
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
		parts = append(parts, fmt.Sprintf("<tool-use name=%q id=%q>\n\n%s\n\n</tool-use>", stringValue(function["name"], ""), stringValue(toolCall["id"], ""), fence(stringValue(function["arguments"], "{}"), "json")))
	}
	if stringValue(message["role"], "") == "tool" {
		parts = append(parts, fmt.Sprintf("<tool-result tool-call-id=%q>\n\n%s\n\n</tool-result>", stringValue(message["tool_call_id"], ""), contentText(content)))
	}
	return strings.Join(nonEmpty(parts), "\n\n")
}

// renderOpenAIResponseItem formats Responses API messages, calls, and results.
func renderOpenAIResponseItem(raw any) string {
	item := object(raw)
	switch stringValue(item["type"], "message") {
	case "function_call":
		return fmt.Sprintf("<tool-use name=%q id=%q>\n\n%s\n\n</tool-use>", stringValue(item["name"], ""), stringValue(item["call_id"], stringValue(item["id"], "")), fence(stringValue(item["arguments"], "{}"), "json"))
	case "function_call_output":
		return fmt.Sprintf("<tool-result call-id=%q>\n\n%s\n\n</tool-result>", stringValue(item["call_id"], ""), contentText(item["output"]))
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
		if text, isText := system.(string); isText {
			parts = append(parts, tag("system-prompt", text))
		} else {
			parts = append(parts, tag("system-prompt", renderGeminiParts(object(system)["parts"])))
		}
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
	return tag("tools", strings.Join(rendered, "\n\n"))
}

// renderGeminiMessage formats one Gemini content item.
func renderGeminiMessage(raw any) string {
	return renderGeminiParts(object(raw)["parts"])
}

// renderGeminiParts formats Gemini text, thought, function, and image parts.
func renderGeminiParts(raw any) string {
	if text, ok := raw.(string); ok {
		return text
	}
	var parts []string
	for _, value := range array(raw) {
		part := object(value)
		if text, ok := part["text"].(string); ok {
			if part["thought"] == true {
				parts = append(parts, tag("thinking", text))
			} else {
				parts = append(parts, text)
			}
		} else if call, ok := part["functionCall"]; ok {
			function := object(call)
			parts = append(parts, fmt.Sprintf("<tool-use name=%q id=%q>\n\n%s\n\n</tool-use>", stringValue(function["name"], ""), stringValue(function["id"], ""), fenceJSON(function["args"])))
		} else if result, ok := part["functionResponse"]; ok {
			function := object(result)
			parts = append(parts, fmt.Sprintf("<tool-result name=%q id=%q>\n\n%s\n\n</tool-result>", stringValue(function["name"], ""), stringValue(function["id"], ""), contentText(function["response"])))
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
	default:
		var blocks []string
		for _, event := range events {
			blocks = append(blocks, fenceJSON(event))
		}
		if len(blocks) > 0 {
			return strings.Join(blocks, "\n\n")
		}
		return fence(raw, "")
	}
}

// renderAnthropicEvents rejoins text and tool input deltas from a Messages API stream.
func renderAnthropicEvents(events []any, raw string) string {
	blocks := make(map[int]map[string]any)
	var stopReason string
	var usage any
	for _, rawEvent := range events {
		event := object(rawEvent)
		switch stringValue(event["type"], "") {
		case "content_block_start":
			index := intValue(event["index"])
			block := object(event["content_block"])
			blocks[index] = map[string]any{"type": block["type"], "text": "", "name": block["name"], "id": block["id"]}
		case "content_block_delta":
			index := intValue(event["index"])
			block := blocks[index]
			if block == nil {
				continue
			}
			delta := object(event["delta"])
			for _, key := range []string{"text", "partial_json", "thinking"} {
				if text := stringValue(delta[key], ""); text != "" {
					block["text"] = stringValue(block["text"], "") + text
				}
			}
		case "message_delta":
			delta := object(event["delta"])
			stopReason = stringValue(delta["stop_reason"], stopReason)
			if event["usage"] != nil {
				usage = event["usage"]
			}
		case "message_start":
			message := object(event["message"])
			if message["usage"] != nil {
				usage = message["usage"]
			}
		}
	}
	var parts []string
	if stopReason != "" {
		parts = append(parts, "- **stop reason**: "+stopReason)
	}
	if usage != nil {
		parts = append(parts, "- **usage**: "+compactJSON(usage))
	}
	keys := make([]int, 0, len(blocks))
	for index := range blocks {
		keys = append(keys, index)
	}
	sort.Ints(keys)
	for _, index := range keys {
		block := blocks[index]
		text := stringValue(block["text"], "")
		switch stringValue(block["type"], "text") {
		case "text":
			parts = append(parts, tag("assistant-text", text))
		case "thinking":
			parts = append(parts, tag("thinking", text))
		case "tool_use":
			parts = append(parts, fmt.Sprintf("<tool-use name=%q id=%q>\n\n%s\n\n</tool-use>", stringValue(block["name"], ""), stringValue(block["id"], ""), fence(text, "json")))
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
	var usage any
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
		if event["usage"] != nil {
			usage = event["usage"]
		}
	}
	var parts []string
	if finish != "" {
		parts = append(parts, "- **finish reason**: "+finish)
	}
	if usage != nil {
		parts = append(parts, "- **usage**: "+compactJSON(usage))
	}
	if text.Len() > 0 {
		parts = append(parts, tag("assistant-text", text.String()))
	}
	keys := make([]int, 0, len(tools))
	for index := range tools {
		keys = append(keys, index)
	}
	sort.Ints(keys)
	for _, index := range keys {
		tool := tools[index]
		parts = append(parts, fmt.Sprintf("<tool-use name=%q id=%q>\n\n%s\n\n</tool-use>", tool["name"], tool["id"], fence(defaultString(tool["arguments"], "{}"), "json")))
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
	var usage any
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
			usage = response["usage"]
		}
	}
	var parts []string
	if status != "" {
		parts = append(parts, "- **status**: "+status)
	}
	if usage != nil {
		parts = append(parts, "- **usage**: "+compactJSON(usage))
	}
	if text.Len() > 0 {
		parts = append(parts, tag("assistant-text", text.String()))
	}
	for key, tool := range tools {
		parts = append(parts, fmt.Sprintf("<tool-use name=%q id=%q>\n\n%s\n\n</tool-use>", tool["name"], key, fence(defaultString(tool["arguments"], "{}"), "json")))
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
	var usage any
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
				if value, ok := part["text"].(string); ok {
					if part["thought"] == true {
						thinking.WriteString(value)
					} else {
						text.WriteString(value)
					}
				}
				if call, ok := part["functionCall"]; ok {
					function := object(call)
					calls = append(calls, fmt.Sprintf("<tool-use name=%q>\n\n%s\n\n</tool-use>", stringValue(function["name"], ""), fenceJSON(function["args"])))
				}
			}
			finish = stringValue(candidate["finishReason"], finish)
		}
		if event["usageMetadata"] != nil {
			usage = event["usageMetadata"]
		}
	}
	var parts []string
	if finish != "" {
		parts = append(parts, "- **finish reason**: "+finish)
	}
	if usage != nil {
		parts = append(parts, "- **usage**: "+compactJSON(usage))
	}
	if thinking.Len() > 0 {
		parts = append(parts, tag("thinking", thinking.String()))
	}
	if text.Len() > 0 {
		parts = append(parts, tag("assistant-text", text.String()))
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

// renderMessageList wraps content in role-labelled XML tags.
func renderMessageList(messages []any, renderContent func(any) string) string {
	var rendered []string
	for index, raw := range messages {
		message := object(raw)
		role := stringValue(message["role"], stringValue(message["type"], "unknown"))
		rendered = append(rendered, fmt.Sprintf("<message index=%q role=%q>\n\n%s\n\n</message>", fmt.Sprint(index+1), role, renderContent(raw)))
	}
	return tag("messages", strings.Join(rendered, "\n\n"))
}

// renderParams lists only parameters present in the request object.
func renderParams(values map[string]any, keys []string) []string {
	var entries []string
	for _, key := range keys {
		if value, ok := values[key]; ok {
			entries = append(entries, "- **"+key+"**: "+compactJSON(value))
		}
	}
	if len(entries) == 0 {
		return nil
	}
	return []string{tag("params", strings.Join(entries, "\n"))}
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
		return text
	}
	if blocks, ok := value.([]any); ok {
		var parts []string
		for _, block := range blocks {
			item := object(block)
			if text, ok := item["text"].(string); ok {
				parts = append(parts, text)
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
	result, _ := value.(map[string]any)
	if result == nil {
		return map[string]any{}
	}
	return result
}

// array returns a JSON array or nil for another value type.
func array(value any) []any {
	result, _ := value.([]any)
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
	number, _ := value.(float64)
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

// tag wraps content in an XML-like section tag to avoid Markdown heading collisions.
func tag(name, content string) string {
	if content == "" {
		return "<" + name + "></" + name + ">"
	}
	return "<" + name + ">\n\n" + content + "\n\n</" + name + ">"
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
