# AI Agent Request Logger

A small local proxy that records the requests a coding agent sends to its model
provider. It saves the complete request and response, plus a readable Markdown
view of the system prompt, tools, messages, and model output.

## Install

From the source directory, run:

```bash
go install ./cmd/ai-agent-request-logger
```

Go installs the `ai-agent-request-logger` binary in `GOBIN`, or in
`GOPATH/bin` when `GOBIN` is unset. Add that directory to `PATH`, then run the
command from any project directory:

```bash
ai-agent-request-logger
```

The first run asks which harness and provider you use. It prints the needed
configuration and launch command, then waits for requests. Run the harness in
a second terminal. The selected route is remembered under your operating
system's user config directory. Use `--force` to choose again.

```bash
ai-agent-request-logger --force
ai-agent-request-logger --port 9000
ai-agent-request-logger --log-dir /tmp/agent-captures
```

The default listener is `127.0.0.1:8787`. By default, captures go under
`.ai-agent-request-logger/logs` in the directory where you start the logger.
Add this to that project's `.gitignore` so captured prompts and code are not
committed:

```gitignore
.ai-agent-request-logger/
```

## How it works

```text
Any supported harness
  -> local HTTP proxy on 127.0.0.1
  -> selected model provider over HTTPS
  -> response streamed back to the harness
```

The proxy uses the selected harness/provider pair to choose one upstream API
endpoint and a renderer for its format. It preserves authentication and other
end-to-end headers, forwards the request, and streams the provider's response
back. It records model-generation requests after the response completes;
token-counting and other housekeeping calls are forwarded but not saved.

Each capture has three files:

- `.md`: readable request, headers, and response. Common credential headers are
  masked.
- `.request.txt`: exact request body bytes, including compression.
- `.response.txt`: exact response body bytes.

These files can include source code, prompts, tool definitions, and model
output. Header masking does not remove secrets embedded in request or response
bodies. Keep the capture directory private.

## Pi

Pi has no base-URL environment variable. The logger prints a snippet for
`~/.pi/agent/models.json`; merge it into the file rather than replacing your
existing provider settings. Pi's provider configuration uses the exact key
`baseUrl`.

### Anthropic

Choose **Pi**, then **Anthropic**. The generated `anthropic` provider points
Pi's existing Anthropic model list at the local proxy. Your Pi authentication
continues to supply the request credential.

```json
{
  "providers": {
    "anthropic": {
      "baseUrl": "http://localhost:8787"
    }
  }
}
```

### OpenAI API key

Choose **Pi**, then **OpenAI API key**. The generated `openai` provider uses
`http://localhost:8787/v1`. Keep your API key in Pi's usual auth setup; do not
put it in this file or in the README.

### GitHub Copilot models

Pi's GitHub Copilot OAuth provider derives its upstream URL from the Copilot
token. Pi uses that OAuth-provided URL for requests, overriding the
`providers.github-copilot.baseUrl` setting in `~/.pi/agent/models.json`. A local
proxy URL in that setting therefore does not route Pi's Copilot requests through
the logger. The logger does not offer this Pi provider. This limitation is
specific to Pi; the separate GitHub Copilot CLI route below is unchanged.

### ChatGPT subscription (Codex)

Choose **Pi**, then **ChatGPT subscription (Codex)**. The generated provider
uses `http://localhost:8787/backend-api`. Pi appends its Codex endpoint to that
prefix. This route also needs the following setting in
`~/.pi/agent/settings.json`:

```json
{
  "transport": "sse"
}
```

The proxy handles HTTP, not WebSockets. Pi's automatic transport can choose a
WebSocket for this route, so the logger explicitly switches it to SSE. Pi also
sends this route's request body compressed with zstd. The raw `.request.txt`
file preserves those bytes exactly, but this standard-library build cannot
zstd-decompress them for the readable Markdown view; that view displays a
warning instead.

For a custom Pi model server, choose **Custom base URL**, select its wire
format, and enter a model ID when prompted. Model discovery checks the
unauthenticated OpenAI-style `/v1/models` endpoint. If an Anthropic-compatible
server does not provide that endpoint, enter its model ID manually.

Pi's [model configuration](https://pi.dev/docs/latest/models) and
[transport settings](https://pi.dev/docs/latest/settings) documentation
covers these fields. Adding a custom `models` list replaces the selected
provider's built-in model list, so the generated custom-target snippet names
the model you selected.

## Claude Code with Enterprise SSO

Complete your normal Claude Code SSO sign-in first. In the logger, choose
**Claude Code**, then **Anthropic**. In another terminal, run the command it
prints; on macOS and Linux it looks like this:

```bash
ANTHROPIC_BASE_URL='http://localhost:8787' ENABLE_TOOL_SEARCH='true' claude
```

`ANTHROPIC_BASE_URL` sends model API traffic through the local proxy. When it
is the only gateway setting, Claude Code keeps the saved `claude.ai` login as
the credential, so your subscription's usage limits and billing still apply.
The proxy passes the authorization and `anthropic-beta` headers through rather
than replacing them. `ENABLE_TOOL_SEARCH=true` avoids changing Claude Code's
tool-search behavior solely because the API endpoint is overridden.

This route is intended for a Claude Code subscription authenticated through
SSO, including an Enterprise account. It has not been tested against a
specific company tenant. If your organization requires a separate LLM gateway
credential, a custom upstream, or prohibits local proxies, check that setup
with your administrator; it may not use the subscription-login route described
above. If Claude Code is configured for Vertex AI, choose **Google Vertex AI**
instead of **Anthropic**.

See Anthropic's [Claude Code gateway guidance][claude-gateway] and [request
header reference][claude-headers].

## GitHub Copilot CLI with GPT models

This route is for the terminal `copilot` command, not the VS Code Copilot
extension. Choose **GitHub Copilot CLI**, then **GitHub subscription**. Start
the command the logger prints; it sets `COPILOT_API_URL` to the local proxy.
Choose a GPT model in Copilot with `copilot --model gpt-5.4` or `/model gpt-5.4`.

There is a compatibility caveat: the current public Copilot CLI configuration
docs do not list `COPILOT_API_URL`. The logger's route depends on that setting,
so confirm that requests appear in the logs before relying on this setup. Some
Copilot model/transport combinations use WebSockets, which this HTTP-only proxy
cannot capture.

See the [Copilot CLI configuration][copilot-config] and
[CLI model-selection reference][copilot-cli].

## Other supported harnesses

The setup wizard also includes Codex, OpenCode, OMP, Gemini CLI, Antigravity
CLI, and Junie. **Custom base URL** supports compatible third-party and local
model servers. Cursor CLI and Amp cannot be logged because their system prompts
and tool definitions are built on their servers, not sent in requests from your
machine.

The proxy is HTTP-only. If a harness or model negotiates a WebSocket, that
request cannot be captured. The logger responds with `426 Upgrade Required` so
clients that support fallback can retry over HTTP.

[claude-gateway]: https://code.claude.com/docs/en/llm-gateway#subscriptions-and-gateways
[claude-headers]: https://code.claude.com/docs/en/llm-gateway-protocol.md#request-headers
[copilot-cli]: https://github.com/github/copilot-cli/blob/e1928292498bd60d8b8730381ed3818e82453711/_autodocs/api-reference/cli-interface.md
[copilot-config]: https://github.com/github/copilot-cli/blob/e1928292498bd60d8b8730381ed3818e82453711/_autodocs/configuration.md
