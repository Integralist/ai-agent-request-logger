package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// requestWithUserText builds an Anthropic request whose single user message carries text.
func requestWithUserText(t *testing.T, text string) Capture {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"model":    "claude-haiku-5-5",
		"messages": []any{map[string]any{"role": "user", "content": text}},
	})
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	return Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200, RequestBody: body,
	}
}

// TestRenderHTMLKeepsFencedDiffIntactWhenItContainsFoldingMarkers stops captured markup from breaking the page structure.
func TestRenderHTMLKeepsFencedDiffIntactWhenItContainsFoldingMarkers(t *testing.T) {
	text := "Review this diff:\n\n```html\n@@ -0,0 +1,3 @@\n+<details class=\"msg-user\" open>\n+<summary>Message 1</summary>\n+</details>\n```\n"
	page := RenderHTML(requestWithUserText(t, text))

	if opened, closed := strings.Count(page, "<details"), strings.Count(page, "</details>"); opened != closed {
		t.Errorf("page has %d folding openers but %d closers; captured markup changed the structure", opened, closed)
	}
	start := strings.Index(page, "<pre><code")
	if start < 0 {
		t.Fatalf("diff was not rendered as a code block:\n%s", page)
	}
	block := page[start:]
	end := strings.Index(block, "</code></pre>")
	if end < 0 {
		t.Fatalf("code block is not closed:\n%s", page)
	}
	block = block[:end]
	for _, expected := range []string{"@@ -0,0 +1,3 @@", "&lt;details class="} {
		if !strings.Contains(block, expected) {
			t.Errorf("code block is missing %q:\n%s", expected, block)
		}
	}
}

// TestRenderHTMLCountsLinesOfMessageSourceNotOfItsRenderedWrappers keeps the folded-length label honest.
func TestRenderHTMLCountsLinesOfMessageSourceNotOfItsRenderedWrappers(t *testing.T) {
	lines := []string{
		"Contents of /Users/example/.agents/AGENTS.md (user's private global instructions):",
		"",
		"# Communication & Tone",
		"",
		"- one", "- two", "- three", "- four", "- five", "- six", "- seven", "- eight",
	}
	text := strings.Join(lines, "\n")
	page := RenderHTML(requestWithUserText(t, text))

	want := fmt.Sprintf("Message content (%d lines)", len(lines))
	if !strings.Contains(page, want) {
		t.Errorf("folded message label does not match its source; want %q:\n%s", want, page)
	}
}
