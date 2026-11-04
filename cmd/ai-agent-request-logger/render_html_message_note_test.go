package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLExplainsUserAndAssistantCardsBelowTheirTitles keeps a note under every user and assistant card title.
func TestRenderHTMLExplainsUserAndAssistantCardsBelowTheirTitles(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hello there"},{"role":"assistant","content":"hi back"}]}`),
	}
	page := RenderHTML(capture)

	const userTitle = "Message 1 · User 🧑</summary>"
	const userNote = `<p class="capture-note">What you typed, plus anything the coding agent added`
	const assistantTitle = "Message 2 · Assistant 🤖</summary>"
	const assistantNote = `<p class="capture-note">The model`
	userAt, userNoteAt := strings.Index(page, userTitle), strings.Index(page, userNote)
	assistantAt, assistantNoteAt := strings.Index(page, assistantTitle), strings.Index(page, assistantNote)
	if userAt < 0 || userNoteAt < userAt || assistantAt < userNoteAt || assistantNoteAt < assistantAt {
		t.Errorf("user or assistant card has no note under its title (user %d, note %d, assistant %d, note %d):\n%s", userAt, userNoteAt, assistantAt, assistantNoteAt, page)
	}
	if got := strings.Count(page, `class="capture-note"`); got != 2 {
		t.Errorf("capture has %d notes, want 2 (one user card and one assistant card):\n%s", got, page)
	}
}

// TestMessageCardNotesOnlyTheRolesThatNeedExplaining keeps notes on user, assistant, and system turns and off other roles.
func TestMessageCardNotesOnlyTheRolesThatNeedExplaining(t *testing.T) {
	testCases := []struct {
		name     string
		role     string
		wantNote bool
	}{
		{name: "user turn", role: "user", wantNote: true},
		{name: "assistant turn", role: "assistant", wantNote: true},
		{name: "Gemini model turn", role: "model", wantNote: true},
		{name: "system turn", role: "system", wantNote: true},
		{name: "other role", role: "tool", wantNote: false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			card := markdownToHTML(messageCard(1, tc.role, "body"))
			if got := strings.Contains(card, `class="capture-note"`); got != tc.wantNote {
				t.Errorf("messageCard(%q) has note = %t, want %t:\n%s", tc.role, got, tc.wantNote, card)
			}
		})
	}
}
