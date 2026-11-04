package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLGivesSectionsEmojisAndOutlineAnchors keeps every outline entry emoji-labelled and linkable.
func TestRenderHTMLGivesSectionsEmojisAndOutlineAnchors(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","system":"Be brief.","tools":[{"name":"Bash","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hi"},{"role":"assistant","content":"hello"}]}`),
	}
	page := RenderHTML(capture)

	for _, expected := range []string{
		`<h2 id="section-request">Request 📨</h2>`,
		`<h2 id="section-response">Response 📬</h2>`,
		`<summary><a id="section-system-prompt"></a>System prompt (9 characters) 📋</summary>`,
		`<summary><a id="section-tools"></a>Tool definitions (1) 🧰</summary>`,
		`<summary><a id="message-1"></a>Message 1 · User 🧑</summary>`,
		`<summary><a id="message-2"></a>Message 2 · Assistant 🤖</summary>`,
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("HTML capture does not contain %q:\n%s", expected, page)
		}
	}
}

// TestRenderHTMLAddsStickyOutlineBesideTheCapture keeps the outline floating beside the reading column.
func TestRenderHTMLAddsStickyOutlineBesideTheCapture(t *testing.T) {
	page := RenderHTML(requestWithUserText(t, "hi"))

	for _, expected := range []string{`<div class="capture-layout">`, `<nav class="page-outline" aria-label="On this page">`} {
		if !strings.Contains(page, expected) {
			t.Errorf("HTML capture does not contain %q:\n%s", expected, page)
		}
	}
	start := strings.Index(page, ".page-outline {")
	if start < 0 {
		t.Fatalf("page has no outline style:\n%s", page)
	}
	rule := page[start : start+strings.Index(page[start:], "}")]
	if !strings.Contains(rule, "position: sticky") {
		t.Errorf("outline style is not sticky: %q", rule)
	}
	if !strings.Contains(page, sidebarJS) {
		t.Errorf("page does not include the outline script")
	}
}
