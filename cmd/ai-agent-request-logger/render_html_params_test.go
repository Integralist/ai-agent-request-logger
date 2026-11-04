package main

import (
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLKeepsParameterCodeBlocksInsideTheirListItems keeps each object or array parameter's code block inside its own item of the Parameters box.
func TestRenderHTMLKeepsParameterCodeBlocksInsideTheirListItems(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","max_tokens":128000,"stream":true,"thinking":{"type":"adaptive","display":"summarized"},"metadata":{"user_id":"abc"},"messages":[{"role":"user","content":"hi"}]}`),
	}
	page := RenderHTML(capture)

	for _, key := range []string{"thinking", "metadata"} {
		label := "<strong>" + key + "</strong>:"
		start := strings.Index(page, label)
		if start < 0 {
			t.Fatalf("parameters have no %q entry:\n%s", key, page)
		}
		rest := page[start:]
		before, _, ok := strings.Cut(rest, "</li>")
		if !ok {
			t.Fatalf("%q entry is not closed as a list item:\n%s", key, page)
		}
		item := before
		if !strings.Contains(item, "<pre><code") {
			t.Errorf("%q code block is outside its list item:\n%s", key, page)
		}
	}
	if !strings.Contains(page, "<strong>stream</strong>") {
		t.Errorf("scalar parameter stream is missing:\n%s", page)
	}
}
