package main

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

// TestRenderHTMLNumbersAndLinksEveryMessageToItsCard keeps the message count and its links in step.
func TestRenderHTMLNumbersAndLinksEveryMessageToItsCard(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","messages":[` +
			`{"role":"user","content":"hello"},` +
			`{"role":"system","content":"system note"},` +
			`{"role":"assistant","content":"reply"},` +
			`{"role":"user","content":"two"},` +
			`{"role":"system","content":"three"}]}`),
	}

	page := RenderHTML(capture)
	for number := 1; number <= 5; number++ {
		link := fmt.Sprintf(`<a href="#message-%d">%d</a>`, number, number)
		card := fmt.Sprintf(`<a id="message-%d"></a>Message %d `, number, number)
		if !strings.Contains(page, link) {
			t.Errorf("message count does not link to message %d:\n%s", number, page)
		}
		if !strings.Contains(page, card) {
			t.Errorf("message %d has no numbered card title:\n%s", number, page)
		}
	}
}

// TestRenderHTMLFoldsLongBlocksClosedAndShortBlocksOpen keeps long content out of the way by default.
func TestRenderHTMLFoldsLongBlocksClosedAndShortBlocksOpen(t *testing.T) {
	long := strings.Repeat("A full sentence of response text.\n", 14)
	longResponse := fmt.Sprintf("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":%q}}\n", long)
	shortResponse := "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Short answer.\"}}\n"

	base := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"` + strings.Repeat("line one\\n", 13) + `end"}]}`),
	}

	longCapture := base
	longCapture.ResponseBody = []byte(longResponse)
	longPage := RenderHTML(longCapture)
	if !strings.Contains(longPage, "<details>\n<summary>Assistant response</summary>") {
		t.Errorf("long assistant response is not closed by default:\n%s", longPage)
	}
	if !strings.Contains(longPage, "<details>\n<summary>Message content (14 lines)</summary>") {
		t.Errorf("long user message is not folded closed:\n%s", longPage)
	}

	shortCapture := base
	shortCapture.RequestBody = []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hi"}]}`)
	shortCapture.ResponseBody = []byte(shortResponse)
	shortPage := RenderHTML(shortCapture)
	if !strings.Contains(shortPage, "<details open>\n<summary>Assistant response</summary>") {
		t.Errorf("short assistant response is not open by default:\n%s", shortPage)
	}
	if strings.Contains(shortPage, "Message content (") {
		t.Errorf("short user message was wrapped in a folding section:\n%s", shortPage)
	}
}

// TestRenderHTMLGivesUserMessagesTheirOwnTintedCard keeps user text from blending into the page.
func TestRenderHTMLGivesUserMessagesTheirOwnTintedCard(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"hello"}]}`),
	}

	page := RenderHTML(capture)
	card := strings.Index(page, `<details class="msg-user">`)
	greeting := strings.Index(page, "hello")
	if card < 0 {
		t.Fatalf("user message has no tinted card:\n%s", page)
	}
	if greeting < card || !strings.Contains(page[card:], "</details>") {
		t.Errorf("user text is not inside its card:\n%s", page)
	}
}

// TestRenderMarkdownDemotesHeadingsInsideMessages keeps captured headings below the message titles.
func TestRenderMarkdownDemotesHeadingsInsideMessages(t *testing.T) {
	capture := Capture{
		AgentLabel: "Claude Code", Renderer: RendererAnthropic, Timestamp: time.Now().UTC(),
		Method: http.MethodPost, Path: anthropicMessagesPath, StatusCode: 200,
		RequestBody: []byte(`{"model":"claude-haiku-5-5","messages":[{"role":"user","content":"# Communication & Tone\n\n## Chat\n\n` + "```" + `\n# not a heading\n` + "```" + `"}]}`),
	}

	page := RenderHTML(capture)
	if !strings.Contains(page, ">Communication &amp; Tone</h4>") {
		t.Errorf("top-level message heading was not demoted to h4:\n%s", page)
	}
	if strings.Contains(page, ">Communication &amp; Tone</h1>") {
		t.Errorf("message heading kept the page title level:\n%s", page)
	}
	if strings.Contains(page, "<h4>not a heading</h4>") || strings.Contains(page, "<h1>not a heading</h1>") {
		t.Errorf("heading inside a code fence was demoted:\n%s", page)
	}
}
