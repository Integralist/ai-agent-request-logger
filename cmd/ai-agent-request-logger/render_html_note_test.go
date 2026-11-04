package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLExplainsSystemCardsAndPromptsOnceEach keeps the explanatory note on system content and off user content.
func TestRenderHTMLExplainsSystemCardsAndPromptsOnceEach(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","system":"You are helpful.","messages":[{"role":"user","content":"hello"},{"role":"system","content":"Environment: /tmp"}]}`),
		ResponseBody: []byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n" +
			"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hi.\"}}\n" +
			"event: message_stop\ndata: {\"type\":\"message_stop\"}\n"),
	}

	page := RenderHTML(capture)
	const userCard = "Message 1 · User 🧑</summary>"
	const systemCard = "Message 2 · System ⚙️</summary>"
	const systemNote = `<p class="capture-note">Added by the coding agent, not you or the model.`
	const promptNote = `<p class="capture-note">Standing instructions the coding agent sends`

	if got := strings.Count(page, `class="capture-note"`); got != 3 {
		t.Fatalf("capture has %d notes, want 3 (system card, system prompt, and user card):\n%s", got, page)
	}
	cardAt := strings.Index(page, systemCard)
	noteAt := strings.Index(page, systemNote)
	contentAt := strings.Index(page, "Environment: /tmp")
	if cardAt < 0 || noteAt < cardAt || contentAt < noteAt {
		t.Errorf("system card note is not between its title and its content (card %d, note %d, content %d):\n%s", cardAt, noteAt, contentAt, page)
	}
	if !strings.Contains(page, promptNote) {
		t.Errorf("system prompt fold has no explanatory note:\n%s", page)
	}
	userAt := strings.Index(page, userCard)
	if userAt < 0 || userAt > cardAt || strings.Contains(page[userAt:cardAt], systemNote) {
		t.Errorf("user card carries a system note or is out of order:\n%s", page)
	}
	if !strings.Contains(page, ".capture-note {") {
		t.Errorf("notes have no styling rule")
	}
}

// TestRenderHTMLNotesCannotBeForgedIntoRawMarkup keeps captured text from injecting HTML through the note marker.
func TestRenderHTMLNotesCannotBeForgedIntoRawMarkup(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"\ue000system-prompt\ue001<img src=x onerror=alert(1)><script>alert(2)</script>"}]}`),
	}

	page := RenderHTML(capture)
	for _, forbidden := range []string{"<img src=x", "<script>alert"} {
		if strings.Contains(page, forbidden) {
			t.Errorf("captured text injected %q into the page:\n%s", forbidden, page)
		}
	}
}
