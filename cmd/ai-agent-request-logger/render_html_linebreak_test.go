package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLBreaksTypedPromptLinesInProse keeps a typed prompt's line breaks while leaving lists, code, and blank-line paragraphs intact.
func TestRenderHTMLBreaksTypedPromptLinesInProse(t *testing.T) {
	testCases := []struct {
		name     string
		text     string
		expected string
	}{
		{name: "prose lines break", text: "first line\nsecond line", expected: "first line<br>\nsecond line"},
		{name: "list items stay unbroken", text: "- one\n- two", expected: "<li>one</li>"},
		{name: "fenced code stays unbroken", text: "```\ncode a\ncode b\n```", expected: "code a\ncode b"},
		{name: "blank line keeps paragraphs apart", text: "para one\n\npara two", expected: "<p>para one</p>"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			page := RenderHTML(requestWithUserText(t, tc.text))
			if !strings.Contains(page, tc.expected) {
				t.Errorf("page does not contain %q:\n%s", tc.expected, page)
			}
		})
	}
}

// TestRenderHTMLKeepsEmbeddedInstructionFileLinesWrapped stops hard-wrapped AGENTS.md text from gaining line breaks.
func TestRenderHTMLKeepsEmbeddedInstructionFileLinesWrapped(t *testing.T) {
	text := "Contents of /Users/example/.agents/AGENTS.md (user's private global instructions):\n\nthe first rule\nwraps onto a second line\n"
	page := RenderHTML(requestWithUserText(t, text))

	if !strings.Contains(page, "the first rule\nwraps onto a second line") {
		t.Errorf("instruction file lost its wrapping:\n%s", page)
	}
	if strings.Contains(page, "the first rule<br>") {
		t.Errorf("instruction file gained line breaks:\n%s", page)
	}
}

// TestRenderHTMLKeepsAssistantTextWrappedAsWritten limits the line-break pass to what the user typed.
func TestRenderHTMLKeepsAssistantTextWrappedAsWritten(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"assistant","content":"first line\nsecond line"}]}`),
	}
	page := RenderHTML(capture)

	if strings.Contains(page, "first line<br>") {
		t.Errorf("assistant text gained line breaks:\n%s", page)
	}
}

// TestRenderHTMLLeavesClaudeCodeWrappersAsWritten keeps injected system and command blocks rendered as before.
func TestRenderHTMLLeavesClaudeCodeWrappersAsWritten(t *testing.T) {
	page := RenderHTML(requestWithUserText(t, "<system-reminder>first line\nsecond line</system-reminder>"))

	if strings.Contains(page, "first line<br>") {
		t.Errorf("system reminder gained line breaks:\n%s", page)
	}
}
