package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLClosesInstructionFoldsByDefault keeps injected instruction blocks closed even when they are short.
func TestRenderHTMLClosesInstructionFoldsByDefault(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"Contents of /Users/mmcdonnell/.claude/CLAUDE.md:\n\n@~/.agents/AGENTS.md\n\nAs you answer the user's questions, you can use the following context:\n\n# currentDate\nToday's date is 2026-11-04."}]}`),
	}

	page := RenderHTML(capture)
	for _, expected := range []string{
		"<details>\n<summary>Contents of <code>/Users/mmcdonnell/.claude/CLAUDE.md</code>:</summary>",
		"<details>\n<summary>Additional context</summary>",
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("instruction fold is not closed by default, want %q:\n%s", expected, page)
		}
	}
}
