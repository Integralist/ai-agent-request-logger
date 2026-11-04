package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLFoldsMessagesIntoAClosedBoxLedByTheTurnNote keeps every request message closed until the reader opens it.
func TestRenderHTMLFoldsMessagesIntoAClosedBoxLedByTheTurnNote(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hello"},{"role":"assistant","content":"hi there"}]}`),
	}
	page := RenderHTML(capture)

	const box = "<details class=\"msgs\">\n<summary>Messages (2)</summary>"
	const intro = "resends all earlier messages, not only the last turn"
	const firstCard = "<details class=\"msg-user\">"
	for _, expected := range []string{box, intro, `href="https://www.aihero.dev/ai-coding-dictionary/turn"`, firstCard, "<details class=\"msg-assistant\">"} {
		if !strings.Contains(page, expected) {
			t.Errorf("HTML capture does not contain %q:\n%s", expected, page)
		}
	}
	for _, open := range []string{"<details class=\"msgs\" open>", "<details class=\"msg-user\" open>", "<details class=\"msg-assistant\" open>"} {
		if strings.Contains(page, open) {
			t.Errorf("message box or card starts open: %q", open)
		}
	}
	boxAt, introAt, cardAt := strings.Index(page, box), strings.Index(page, intro), strings.Index(page, firstCard)
	if boxAt < 0 || introAt < boxAt || cardAt < introAt {
		t.Errorf("turn note is not inside the box, above the first card (box %d, note %d, card %d):\n%s", boxAt, introAt, cardAt, page)
	}
}

// TestRenderHTMLOmitsMessagesBoxWhenRequestHasNoMessages keeps an empty request from showing an empty box.
func TestRenderHTMLOmitsMessagesBoxWhenRequestHasNoMessages(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5"}`),
	}
	page := RenderHTML(capture)

	if strings.Contains(page, "<details class=\"msgs\"") {
		t.Errorf("request without messages still shows a Messages box:\n%s", page)
	}
}
