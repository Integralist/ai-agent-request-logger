package main

import (
	"encoding/json"
	"fmt"
	stdhtml "html"
	"regexp"
	"strings"

	"github.com/gomarkdown/markdown"
	markdownhtml "github.com/gomarkdown/markdown/html"
	"github.com/gomarkdown/markdown/parser"
)

const (
	// longBlockLines is the line count above which folded content starts closed.
	longBlockLines = 10
	// longBlockChars is the character count above which folded content starts closed.
	longBlockChars = 600
	// detailsCloseTag ends every folding section this renderer emits.
	detailsCloseTag = "</details>"
)

// detailsOpenPattern matches only the section openers this renderer emits, so captured text cannot add attributes.
var detailsOpenPattern = regexp.MustCompile(`^<details(?: class="(?:msgs|msg-(?:user|system|assistant|other))")?(?: open)?>\n<summary>`)

// validAnchorID accepts only the tool, message, and section identifiers this renderer generates.
var validAnchorID = regexp.MustCompile(`^(?:tool|message|section)-[a-z0-9-]+$`)

// RenderHTML creates a self-contained, styled HTML page for one captured exchange.
func RenderHTML(capture Capture) string {
	requestText, _ := decodeRequestBody(capture.RequestBody, capture.RequestEncoding)
	var request any
	_ = json.Unmarshal([]byte(requestText), &request)
	title := fmt.Sprintf("%s — %s", capture.AgentLabel, requestModel(request, capture.Path))
	body := markdownToHTML(RenderMarkdown(capture))
	var page strings.Builder
	page.WriteString("<!doctype html>\n<html lang=\"en\" data-design=\"anthropic\">\n<head>\n<meta charset=\"utf-8\">\n<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n<meta name=\"color-scheme\" content=\"light dark\">\n<title>")
	page.WriteString(stdhtml.EscapeString(title))
	page.WriteString("</title>\n<style>\n")
	page.WriteString(designAnthropicCSS)
	page.WriteString("\n")
	page.WriteString(designCSS)
	page.WriteString("\n")
	page.WriteString(captureStyles)
	page.WriteString("\n</style>\n</head>\n<body>\n<div class=\"capture-layout\">\n<header class=\"capture-toolbar\">\n<label class=\"design-appearance\" for=\"design-mode\">Appearance\n<select id=\"design-mode\">\n<option value=\"system\" selected>System</option>\n<option value=\"light\">Light</option>\n<option value=\"dark\">Dark</option>\n</select>\n</label>\n</header>\n<main class=\"capture design-prose\">\n")
	page.WriteString(body)
	page.WriteString("\n</main>\n<nav class=\"page-outline\" aria-label=\"On this page\">\n<p class=\"page-outline-title\">On this page</p>\n<ol></ol>\n</nav>\n</div>\n<script>\n")
	page.WriteString(designModeJS)
	page.WriteString("\n</script>\n<script>\n")
	page.WriteString(revealJS)
	page.WriteString("\n</script>\n<script>\n")
	page.WriteString(sidebarJS)
	page.WriteString("\n</script>\n</body>\n</html>\n")
	return page.String()
}

// collapsible wraps content in a details element; summaryHTML must already be escaped.
func collapsible(summaryHTML, content string, open bool) string {
	tag := "<details>"
	if open {
		tag = "<details open>"
	}
	return tag + "\n<summary>" + summaryHTML + "</summary>\n\n" + content + "\n\n" + detailsCloseTag
}

// details wraps metadata in a section that stays closed until the reader expands it.
func details(summary, content string) string {
	return collapsible(stdhtml.EscapeString(summary), content, false)
}

// foldable wraps prompt or response content; long content starts closed so the page stays scannable.
func foldable(summary, content string) string {
	return collapsible(stdhtml.EscapeString(summary), content, !isLongBlock(content))
}

// Note markers wrap explanatory text with a key; only keys in capturedNotes are honoured, so captured text cannot inject markup.
const (
	noteStart = "\uE000"
	noteEnd   = "\uE001"
	// noteSystemMessage keys the note on a system-role message card.
	noteSystemMessage = "system-message"
	// noteSystemPrompt keys the note on a system prompt fold.
	noteSystemPrompt = "system-prompt"
	// noteUserMessage keys the note on a user-role message card.
	noteUserMessage = "user-message"
	// noteAssistantMessage keys the note on an assistant-role message card.
	noteAssistantMessage = "assistant-message"
)

// capturedNotes maps each note key to the text shown beneath it.
var capturedNotes = map[string]string{
	noteSystemMessage:    "Added by the coding agent, not you or the model. It gives the model context, such as the working folder, git state, or extra tools. Not every request has one.",
	noteSystemPrompt:     "Standing instructions the coding agent sends with every request. They set the model's role and rules; you don't type them.",
	noteUserMessage:      "What you typed, plus anything the coding agent added to your turn, such as tool results, instruction files, or reminders.",
	noteAssistantMessage: "The model's earlier reply, sent back with this request so it can see what it already said. It can include text, thinking, and tool calls.",
}

// noted prefixes content with a note marker that markdownToHTML turns into a styled explanation.
func noted(key, content string) string {
	return noteStart + key + noteEnd + "\n\n" + content
}

// foldableWithNote folds prompt content closed or open by length and explains it with a note; summaryHTML must already be escaped.
func foldableWithNote(summaryHTML, key, content string) string {
	return collapsible(summaryHTML, noted(key, content), !isLongBlock(content))
}

// sectionSummary escapes a fold's label and prefixes the anchor the page outline links to.
func sectionSummary(id, label string) string {
	return `<a id="` + id + `"></a>` + stdhtml.EscapeString(label)
}

// toolDefinitionsFold folds every declared tool under the outline anchor and emoji shared by all renderers.
func toolDefinitionsFold(rendered []string) string {
	return collapsible(sectionSummary("section-tools", fmt.Sprintf("Tool definitions (%d) 🧰", len(rendered))), strings.Join(rendered, "\n\n"), false)
}

// isLongBlock reports whether content is long enough to start folded.
func isLongBlock(text string) bool {
	return lineCount(text) > longBlockLines || len(text) > longBlockChars
}

// lineCount counts visible lines, ignoring a trailing newline.
func lineCount(text string) int {
	return strings.Count(strings.TrimRight(text, "\n"), "\n") + 1
}

// messageBody folds a long message body inside its card; short bodies sit directly in the card.
// sourceLines is the line count of the message's own text, which is what the label reports.
func messageBody(content string, sourceLines int) string {
	if !isLongBlock(content) {
		return content
	}
	if sourceLines == 0 {
		sourceLines = lineCount(content)
	}
	return details(fmt.Sprintf("Message content (%d lines)", sourceLines), content)
}

// messageSourceLines counts lines of a message's own text blocks, ignoring the wrappers the renderer adds.
func messageSourceLines(raw any) int {
	var texts []string
	switch content := object(raw)["content"].(type) {
	case string:
		texts = append(texts, content)
	case []any:
		for _, block := range content {
			item := object(block)
			if stringValue(item["type"], "") == textContentType {
				texts = append(texts, stringValue(item[textContentType], ""))
			}
		}
	}
	lines := 0
	for _, text := range texts {
		if text != "" {
			lines += lineCount(text)
		}
	}
	return lines
}

// messageCard renders one conversation turn as an open, role-tinted card with a numbered title.
func messageCard(number int, role, body string) string {
	switch messageClass(role) {
	case "system":
		body = noted(noteSystemMessage, body)
	case "user":
		body = noted(noteUserMessage, body)
	case "assistant":
		body = noted(noteAssistantMessage, body)
	}
	summary := fmt.Sprintf(`<a id="%s"></a>Message %d · %s %s`, messageAnchorID(number), number, displayRole(role), roleEmoji(role))
	return fmt.Sprintf("<details class=\"msg-%s\">\n<summary>%s</summary>\n\n%s\n\n%s", messageClass(role), summary, body, detailsCloseTag)
}

// messagesIntro explains why every request repeats earlier messages; it leads the Messages box.
const messagesIntro = "On every [turn](https://www.aihero.dev/ai-coding-dictionary/turn), " +
	"the request resends all earlier messages, not only the last turn's. " +
	"An LLM keeps no memory between requests, so each one needs the full " +
	"context and history."

// messagesBox folds one request's message cards into a single closed box, led by messagesIntro.
func messagesBox(count int, cards string) string {
	return fmt.Sprintf("<details class=\"msgs\">\n<summary>Messages (%d)</summary>\n\n%s\n\n%s\n\n%s", count, messagesIntro, cards, detailsCloseTag)
}

// roleEmoji marks a message card's role at a glance, using the same grouping as its tint.
func roleEmoji(role string) string {
	switch messageClass(role) {
	case "user":
		return "🧑"
	case "assistant":
		return "🤖"
	case "system":
		return "⚙️"
	default:
		return "💬"
	}
}

// messageClass maps wire-format roles onto the three card tints plus a neutral fallback.
func messageClass(role string) string {
	switch role {
	case "user", "system", "assistant":
		return role
	case "model":
		return "assistant"
	default:
		return "other"
	}
}

// displayRole capitalises a wire-format role for a card title.
func displayRole(role string) string {
	label := strings.ReplaceAll(role, "_", " ")
	if label == "" {
		return "Unknown"
	}
	return strings.ToUpper(label[:1]) + label[1:]
}

// demoteHeadings moves captured Markdown headings below the card titles, leaving fenced code untouched.
func demoteHeadings(text string) string {
	lines := strings.Split(text, "\n")
	inFence := false
	for index, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}
		level := len(line) - len(strings.TrimLeft(line, "#"))
		if level == 0 || level > 6 || level >= len(line) || line[level] != ' ' {
			continue
		}
		lines[index] = strings.Repeat("#", min(level+3, 6)) + line[level:]
	}
	return strings.Join(lines, "\n")
}

// markdownToHTML converts captured Markdown, expanding only the folding sections this renderer emits.
func markdownToHTML(source string) string {
	var output strings.Builder
	for {
		start, openLength := findDetailsOpen(source)
		if start < 0 {
			output.Write(markdownFragment(source))
			break
		}
		output.Write(markdownFragment(source[:start]))
		opening := source[start : start+openLength]
		rest := source[start+openLength:]
		before, after, ok := strings.Cut(rest, "</summary>")
		if !ok {
			output.Write(markdownFragment(source[start:]))
			break
		}
		summary := before
		body := after
		if newline := strings.Index(body, "\n"); newline >= 0 {
			body = body[newline+1:]
		}
		noteText := ""
		if trimmed := strings.TrimLeft(body, "\n"); strings.HasPrefix(trimmed, noteStart) {
			if key, rest, found := strings.Cut(trimmed[len(noteStart):], noteEnd); found {
				if text, known := capturedNotes[key]; known {
					noteText = text
					body = rest
				}
			}
		}
		end := findDetailsClose(body)
		if end < 0 {
			output.Write(markdownFragment(source[start:]))
			break
		}
		output.WriteString(strings.TrimSuffix(opening, "\n<summary>"))
		output.WriteString("\n<summary>")
		output.WriteString(renderDetailsSummary(summary))
		output.WriteString("</summary>\n<div class=\"details-body\">\n")
		if noteText != "" {
			output.WriteString("<p class=\"capture-note\">")
			output.WriteString(stdhtml.EscapeString(noteText))
			output.WriteString("</p>\n")
		}
		output.WriteString(markdownToHTML(body[:end]))
		output.WriteString("\n</div>\n" + detailsCloseTag + "\n")
		source = body[end+len(detailsCloseTag):]
	}
	return output.String()
}

// findDetailsOpen locates the next recognised folding opener and returns its position and length.
func findDetailsOpen(source string) (int, int) {
	return findDetailsOpenMasked(maskFencedCode(source))
}

// findDetailsOpenMasked searches text whose fenced code has already been masked.
func findDetailsOpenMasked(masked string) (int, int) {
	for offset := 0; offset < len(masked); {
		index := strings.Index(masked[offset:], "<details")
		if index < 0 {
			break
		}
		index += offset
		if match := detailsOpenPattern.FindString(masked[index:]); match != "" {
			return index, len(match)
		}
		offset = index + len("<details")
	}
	return -1, 0
}

// findDetailsClose returns the index of the closing tag that matches the section whose body starts at body[0].
func findDetailsClose(body string) int {
	masked := maskFencedCode(body)
	depth := 0
	for cursor := 0; cursor < len(masked); {
		nextClose := strings.Index(masked[cursor:], detailsCloseTag)
		if nextClose < 0 {
			return -1
		}
		nextOpen, openLength := findDetailsOpenMasked(masked[cursor:])
		if nextOpen >= 0 && nextOpen < nextClose {
			depth++
			cursor += nextOpen + openLength
			continue
		}
		if depth == 0 {
			return cursor + nextClose
		}
		depth--
		cursor += nextClose + len(detailsCloseTag)
	}
	return -1
}

// maskFencedCode blanks the contents of closed fenced code blocks so markup inside code is never read as structure.
// Offsets are preserved, so positions found in the masked text index the original. An unclosed fence is left alone.
func maskFencedCode(source string) string {
	type span struct{ start, end int }
	var spans []span
	var fence string
	var fenceStart, offset int
	for _, line := range strings.SplitAfter(source, "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case fence == "":
			if marker := fenceRun(trimmed); marker != "" {
				fence, fenceStart = marker, offset+len(line)
			}
		case isClosingFence(trimmed, fence):
			spans = append(spans, span{fenceStart, offset})
			fence = ""
		}
		offset += len(line)
	}
	if len(spans) == 0 {
		return source
	}
	masked := []byte(source)
	for _, s := range spans {
		for i := s.start; i < s.end; i++ {
			if masked[i] != '\n' {
				masked[i] = ' '
			}
		}
	}
	return string(masked)
}

// fenceRun returns the backtick or tilde run that opens a fence, or "" when the line does not open one.
func fenceRun(trimmed string) string {
	if len(trimmed) < 3 || (trimmed[0] != '`' && trimmed[0] != '~') {
		return ""
	}
	n := 0
	for n < len(trimmed) && trimmed[n] == trimmed[0] {
		n++
	}
	if n < 3 {
		return ""
	}
	return trimmed[:n]
}

// isClosingFence reports whether a line is only the fence characters, at least as long as the opener.
func isClosingFence(trimmed, fence string) bool {
	return len(trimmed) >= len(fence) && strings.Trim(trimmed, fence[:1]) == ""
}

// markdownFragment renders Markdown with raw HTML and remote images disabled.
func markdownFragment(source string) []byte {
	parserInstance := parser.NewWithExtensions(parser.CommonExtensions)
	renderer := markdownhtml.NewRenderer(markdownhtml.RendererOptions{Flags: markdownhtml.CommonFlags | markdownhtml.SkipHTML | markdownhtml.SkipImages})
	return markdown.ToHTML([]byte(source), parserInstance, renderer)
}

// renderDetailsSummary escapes summary text, keeping only the code spans and anchors this renderer generates.
func renderDetailsSummary(summary string) string {
	var output strings.Builder
	for summary != "" {
		if anchor, rest, ok := cutAnchor(summary); ok {
			output.WriteString(anchor)
			summary = rest
			continue
		}
		if code, rest, ok := cutCode(summary); ok {
			output.WriteString(code)
			summary = rest
			continue
		}
		next := len(summary)
		for _, marker := range []string{`<a id="`, "<code>"} {
			if index := strings.Index(summary, marker); index > 0 && index < next {
				next = index
			}
		}
		output.WriteString(stdhtml.EscapeString(stdhtml.UnescapeString(summary[:next])))
		summary = summary[next:]
	}
	return output.String()
}

// cutAnchor consumes a generated anchor element from the start of summary.
func cutAnchor(summary string) (string, string, bool) {
	const prefix = `<a id="`
	const suffix = `"></a>`
	if !strings.HasPrefix(summary, prefix) {
		return "", "", false
	}
	end := strings.Index(summary, suffix)
	if end < 0 {
		return "", "", false
	}
	id := summary[len(prefix):end]
	if !validAnchorID.MatchString(id) {
		return "", "", false
	}
	return prefix + id + suffix, summary[end+len(suffix):], true
}

// cutCode consumes a code span from the start of summary.
func cutCode(summary string) (string, string, bool) {
	if !strings.HasPrefix(summary, "<code>") {
		return "", "", false
	}
	end := strings.Index(summary, "</code>")
	if end < 0 {
		return "", "", false
	}
	inner := stdhtml.EscapeString(stdhtml.UnescapeString(summary[len("<code>"):end]))
	return "<code>" + inner + "</code>", summary[end+len("</code>"):], true
}

// captureStyles lays out capture content: message cards, folding sections, and navigation pills.
const captureStyles = `
:root { --pill: var(--d-soft); --pill-hover: var(--d-hover); --pill-border: var(--d-border); }
* { box-sizing: border-box; }
body { min-width: 320px; margin: 0; background: var(--d-page); color: var(--d-text); font-size: 17px; line-height: 1.7; }
.capture-toolbar, .capture { width: min(calc(100% - 48px), 980px); margin-left: auto; margin-right: auto; }
.capture-toolbar { display: flex; justify-content: flex-end; margin-top: 24px; }
.capture { margin-top: 24px; margin-bottom: 96px; }
.capture p, .capture ul, .capture ol, .capture pre, .capture blockquote { margin: 0 0 16px; }
.capture-note { margin: 0 0 14px; color: var(--d-muted); font-size: 14px; line-height: 1.55; }
.capture li + li { margin-top: 6px; }
h1, h2, h3, h4, h5, h6, summary { font-weight: 600; line-height: 1.3; }
h1 { margin: 0 0 24px; padding-bottom: 18px; border-bottom: 2px solid var(--d-border); font-size: clamp(28px, 4vw, 40px); }
h2 { margin: 56px 0 20px; padding-bottom: 10px; border-bottom: 1px solid var(--d-border); font-size: 26px; }
h3 { margin: 32px 0 12px; font-size: 21px; }
h4 { margin: 26px 0 10px; font-size: 18px; }
h5, h6 { margin: 20px 0 8px; font-size: 16px; }
.capture > ul { margin: 0 0 24px; padding: 20px 26px; border: 1px solid var(--d-soft); border-radius: 14px; background: var(--d-surface); list-style: none; }
a { color: var(--d-accent-text); text-decoration-thickness: 1px; text-underline-offset: 3px; }
a[href^="#message-"] { font-weight: 600; }
a[href^="#tool-"] { display: inline-flex; align-items: center; margin: 0 .2rem .35rem 0; padding: .22rem .7rem; border: 1px solid var(--pill-border); border-radius: 999px; background: var(--pill); color: var(--d-text); font: 500 13px/1.5 var(--d-font-ui); text-decoration: none; transition: background-color .15s ease, border-color .15s ease, transform .15s ease; }
a[href^="#tool-"]:hover, a[href^="#tool-"]:focus-visible { border-color: var(--d-accent); background: var(--pill-hover); transform: translateY(-1px); }
details { margin: 16px 0; border: 1px solid var(--d-border); border-radius: 12px; background: var(--d-surface); overflow: hidden; }
details > summary { display: flex; align-items: center; gap: 10px; padding: 12px 16px; cursor: pointer; font-family: var(--d-font-ui); font-size: 15px; }
details > summary::-webkit-details-marker { display: none; }
details > summary::before { content: ""; flex: 0 0 auto; border-top: 5px solid transparent; border-bottom: 5px solid transparent; border-left: 7px solid currentColor; transition: transform .15s ease; }
details[open] > summary::before { transform: rotate(90deg); }
details[open] > summary { border-bottom: 1px solid var(--d-soft); }
details > .details-body { padding: 16px 18px 18px; }
/* Headings inside folded content are body-sized; page-level sizes stay on the page. */
details h1, details h2, details h3, details h4, details h5, details h6 {
  margin: 18px 0 8px;
  padding: 0;
  border: 0;
  font-size: 16px;
  font-weight: 600;
}
details details { margin: 14px 0; background: var(--d-surface); }
details.msg { margin: 28px 0; border-radius: 16px; }
details.msg > summary { padding: 18px 22px; font-family: var(--d-font-heading); font-size: 19px; }
details.msg > .details-body { padding: 18px 22px 22px; }
details.msg-user { border-left: 6px solid var(--d-accent); background: var(--d-accent-soft); }
details.msg-user > summary { background: transparent; }
details.msg-system { background: var(--d-soft); }
details.msg-system > summary { background: transparent; }
details.msg-assistant { background: var(--d-page); }
details.msg-other { background: var(--d-soft); }
details.msgs > summary { font-family: var(--d-font-heading); font-size: 19px; }
details.msgs > .details-body > p:first-child { color: var(--d-muted); font-size: 15px; }
details.msg > .details-body > details { background: var(--d-surface); }
pre { overflow-x: auto; margin: 14px 0 16px; padding: 14px 16px; border: 1px solid var(--d-soft); border-radius: 10px; background: var(--d-page); color: var(--d-text); font: 13.5px/1.6 var(--d-font-code); }
code { padding: .1em .35em; border-radius: 5px; background: var(--d-soft); font: .9em var(--d-font-code); }
pre code { padding: 0; background: transparent; }
blockquote { margin: 18px 0; padding: 4px 18px; border-left: 3px solid var(--d-border); color: var(--d-text); font-style: italic; }
/* The "On this page" outline sits beside the reading column and sticks while the page scrolls. */
.capture-layout { width: min(calc(100% - 48px), 1240px); margin: 24px auto 96px; display: grid; grid-template-columns: minmax(0, 1fr) 220px; column-gap: 40px; align-items: start; }
.capture-layout > .capture-toolbar, .capture-layout > .capture { grid-column: 1; width: auto; min-width: 0; margin: 0; }
.capture-layout > .capture-toolbar { grid-row: 1; margin-bottom: 24px; }
.capture-layout > .capture { grid-row: 2; }
.page-outline { grid-column: 2; grid-row: 1 / span 2; position: sticky; top: 24px; max-height: calc(100vh - 48px); overflow-y: auto; font: 14px/1.4 var(--d-font-ui); color: var(--d-muted); }
.page-outline[hidden] { display: none; }
.page-outline-title { margin: 0 0 12px; font-size: 12px; font-weight: 600; letter-spacing: .08em; text-transform: uppercase; }
.page-outline ol { margin: 0; padding: 0; list-style: none; border-left: 1px solid var(--d-border); }
.page-outline a { display: block; margin-left: -1px; padding: 6px 0 6px 14px; border-left: 2px solid transparent; color: var(--d-muted); text-decoration: none; }
.page-outline a:hover { color: var(--d-text); }
.page-outline a[aria-current="true"] { color: var(--d-accent-text); border-left-color: var(--d-accent); }
.page-outline li.nested a { padding-left: 26px; font-size: 13px; }
@media (max-width: 900px) { .capture-layout { width: min(calc(100% - 48px), 980px); grid-template-columns: minmax(0, 1fr); } .page-outline { display: none; } }
@media (max-width: 600px) { .capture-layout { width: calc(100% - 24px); } }
@media print { .page-outline { display: none; } }
@media (max-width: 600px) { .capture-toolbar, .capture { width: calc(100% - 24px); } h2 { font-size: 22px; } details.msg > summary { font-size: 17px; padding: 14px 16px; } details.msg > .details-body { padding: 14px 16px 16px; } }
`
