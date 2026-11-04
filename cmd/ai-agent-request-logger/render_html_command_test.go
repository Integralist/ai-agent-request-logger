package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLFoldsLocalCommandBlocksClosedBeforeTheUserMessage keeps a slash command out of the user's own words.
func TestRenderHTMLFoldsLocalCommandBlocksClosedBeforeTheUserMessage(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":[` +
			`{"type":"text","text":"<system-reminder>Codebase instructions.</system-reminder>"},` +
			`{"type":"text","text":"<local-command-caveat>The command below was run directly in Claude Code.</local-command-caveat>"},` +
			`{"type":"text","text":"<command-name>/model</command-name>\n<command-message>model</command-message>\n<command-args></command-args>"},` +
			`{"type":"text","text":"<local-command-stdout>Set model to ` + "`Haiku 5.5`" + ` and saved as your default.</local-command-stdout>"},` +
			`{"type":"text","text":"hello"}]}]}`),
		ResponseBody: []byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi.\"}}\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n"),
	}

	page := RenderHTML(capture)
	const summary = "<details>\n<summary>Local command: /model</summary>"
	start := strings.Index(page, summary)
	if start < 0 {
		t.Fatalf("local command is not a closed box named after the command:\n%s", page)
	}
	end := strings.Index(page[start:], "</details>")
	if end < 0 {
		t.Fatalf("local command box is not closed:\n%s", page)
	}
	box := page[start : start+end]
	if !strings.Contains(box, "Set model to <code>Haiku 5.5</code>") {
		t.Errorf("command output is not inside the command box:\n%s", box)
	}
	if !strings.Contains(box, "The command below was run directly") {
		t.Errorf("command caveat is not inside the command box:\n%s", box)
	}
	if hello := strings.Index(page, ">hello</p>"); hello < start+end {
		t.Errorf("user message appears inside or before the command box:\n%s", page)
	}
	for _, raw := range []string{"<command-name", "<command-args", "<local-command"} {
		if strings.Contains(page, raw) {
			t.Errorf("raw wrapper %q leaked into the page:\n%s", raw, page)
		}
	}
}
