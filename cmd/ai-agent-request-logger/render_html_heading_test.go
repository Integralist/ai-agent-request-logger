package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLSizesHeadingsInsideFoldedBoxesToBodyText keeps headings from tool descriptions from looking like page titles.
func TestRenderHTMLSizesHeadingsInsideFoldedBoxesToBodyText(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","tools":[{"name":"Bash","description":"# Git\n\nAvoid interactive flags.","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hi"}]}`),
	}

	page := RenderHTML(capture)
	if !strings.Contains(page, "<h1>Git</h1>") {
		t.Fatalf("tool description heading was not rendered inside the tool box:\n%s", page)
	}
	rule := "details h1, details h2, details h3, details h4, details h5, details h6 {"
	start := strings.Index(page, rule)
	if start < 0 {
		t.Fatalf("page has no heading override scoped to folded boxes:\n%s", page)
	}
	block := page[start : start+strings.Index(page[start:], "}")]
	if !strings.Contains(block, "font-size: 16px") {
		t.Errorf("heading override does not set body-sized text: %q", block)
	}
}
