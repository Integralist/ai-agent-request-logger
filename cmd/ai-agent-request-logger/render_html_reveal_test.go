package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLRevealsToolBoxWhenItsPillIsFollowed keeps a tool pill from landing on a closed box.
func TestRenderHTMLRevealsToolBoxWhenItsPillIsFollowed(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","tools":[{"name":"Bash","description":"Run a shell command","input_schema":{"type":"object"}}],"messages":[{"role":"user","content":"hello"}]}`),
	}

	page := RenderHTML(capture)
	if !strings.Contains(page, `href="#tool-bash"`) {
		t.Fatalf("tool pill does not link to the Bash box:\n%s", page)
	}
	for _, expected := range []string{`a[href^="#tool-"]`, "hashchange", "box.open = true"} {
		if !strings.Contains(page, expected) {
			t.Errorf("page does not reveal the tool box on follow; missing %q", expected)
		}
	}
}
