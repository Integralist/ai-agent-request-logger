# AI Agent Request Logger

A local proxy that saves each model request a coding agent sends, and the
response that comes back. Each capture also gets an HTML page you can read in
a browser.

## Quick start

1. Install the logger:

   ```bash
   go install ./cmd/ai-agent-request-logger
   ```

   Go puts the `ai-agent-request-logger` binary in `GOBIN`, or in `GOPATH/bin`
   when `GOBIN` is unset. Add that directory to your `PATH`.

2. Start the logger from the project you want to capture:

   ```bash
   ai-agent-request-logger
   ```

3. Answer the setup questions: your agent, then its provider. The logger
   prints the configuration and the command that launches your agent, then
   waits for requests.

4. In a second terminal, run the printed command and send a prompt.

5. Open the newest `.html` file in `.ai-agent-request-logger/logs/`, under the
   directory where you started the logger.

> [!TIP]
> `open $(/bin/ls *.html)`

The logger remembers your choice under your operating system's user config
directory. Run `ai-agent-request-logger --force` to choose again.

Add the capture directory to the project's `.gitignore` so captures are never
committed:

```gitignore
.ai-agent-request-logger/
```

## Choose your route

| Agent                                                    | Provider options                                                         | Setup                                     |
| -------------------------------------------------------- | ------------------------------------------------------------------------ | ----------------------------------------- |
| Claude Code                                              | Anthropic, Google Vertex AI                                              | [Claude Code](#claude-code)               |
| Pi                                                       | Anthropic, OpenAI API key, ChatGPT subscription (Codex), Custom base URL | [Pi](#pi)                                 |
| GitHub Copilot CLI                                       | GitHub subscription                                                      | [GitHub Copilot CLI](#github-copilot-cli) |
| Codex, OpenCode, OMP, Gemini CLI, Antigravity CLI, Junie | Shown by the wizard                                                      | [Other agents](#other-agents)             |
| Cursor CLI, Amp                                          | Cannot be captured                                                       | [Limits](#limits)                         |

## Set up each agent

### Claude Code

Use this route for a Claude Code subscription signed in with SSO.

1. Sign in to Claude Code as usual.

2. In the logger, choose **Claude Code**, then **Anthropic**. If Claude Code
   uses Google Vertex AI, choose **Google Vertex AI** instead.

3. In a second terminal, run the printed command. On macOS and Linux it looks
   like this:

   ```bash
   ANTHROPIC_BASE_URL='http://localhost:8787' ENABLE_TOOL_SEARCH='true' claude
   ```

- `ANTHROPIC_BASE_URL` sends model traffic through the proxy. It is the only
  gateway setting, so Claude Code keeps your saved `claude.ai` login, and your
  usage limits and billing still apply.
- The proxy passes the `authorization` and `anthropic-beta` headers through
  unchanged.
- `ENABLE_TOOL_SEARCH=true` keeps tool-search behaviour the same when the API
  endpoint is overridden.

> [!NOTE]
> This route has not been tested against a specific company tenant. If your
> organization requires a separate LLM gateway credential, a custom upstream,
> or bans local proxies, ask your administrator first. This route may not work
> for you.

See Anthropic's [Claude Code gateway guidance][claude-gateway] and
[request header reference][claude-headers].

### Pi

Pi has no base-URL option. The logger prints a snippet for
`~/.pi/agent/models.json`. Merge it into your existing file rather than
replacing it. The key is `baseUrl`.

| Route in the wizard          | Extra setup                                   |
| ---------------------------- | --------------------------------------------- |
| Anthropic                    | None; Pi's sign-in supplies the credential    |
| OpenAI API key               | Keep the key in Pi's auth setup               |
| ChatGPT subscription (Codex) | Set SSE transport, see below                  |
| Custom base URL              | Choose the wire format, then enter a model ID |

The Codex route needs `"transport": "sse"` in `~/.pi/agent/settings.json`.
The logger handles only HTTP, and Pi's default transport can choose WebSockets.

For a custom server, model discovery checks the unauthenticated OpenAI-style
`/v1/models` endpoint. If an Anthropic-compatible server does not provide
that endpoint, enter its model ID manually. A custom `models` list replaces
Pi's built-in list for that provider, so the snippet names the model you
selected.

GitHub Copilot is not available through Pi. See [Limits](#limits).

See Pi's [model configuration][pi-models] and [transport settings][pi-settings]
documentation.

### GitHub Copilot CLI

This route is for the terminal `copilot` command, not the VS Code Copilot
extension.

1. In the logger, choose **GitHub Copilot CLI**, then **GitHub subscription**.
2. Run the printed command. It sets `COPILOT_API_URL` to the proxy.
3. Choose a GPT model in Copilot, for example `copilot --model gpt-5.4` or
   `/model gpt-5.4`.

> [!NOTE]
> `COPILOT_API_URL` is not in the public Copilot CLI configuration docs.
> Confirm that requests appear in the logs before you rely on this route. Some
> model and transport combinations use WebSockets, which the logger cannot
> capture.

See the [Copilot CLI configuration][copilot-config] and the
[model-selection reference][copilot-cli].

### Other agents

The wizard also offers Codex, OpenCode, OMP, Gemini CLI, Antigravity CLI, and
Junie. Pick the agent in the wizard and follow the printed setup. **Custom
base URL** works with compatible third-party and local model servers.

## Captures

By default, captures go under `.ai-agent-request-logger/logs` in the directory
where you start the logger. Each capture has three files:

- `.html`: a styled, self-contained view of the request, headers, and response,
  including linked tool definitions. It has a System, Light, and Dark
  appearance control and local font fallbacks, and it loads no external assets.
  Messages are numbered, sit in one box, and start closed. Long responses start
  folded.
- `.request.txt`: the exact request body bytes, including any compression.
- `.response.txt`: the exact response body bytes.

The HTML view masks common credential headers.

> [!WARNING]
> Captures can include source code, prompts, tool definitions, and model
> output. Header masking does not remove secrets embedded in request or
> response bodies. Keep the capture directory private.

## How it works

```text
Your agent
  -> local proxy on 127.0.0.1
  -> your model provider over HTTPS
  -> the response streams back to your agent
```

The route you choose decides which provider API the proxy calls and how the
capture is formatted. The proxy keeps your authentication and other
end-to-end headers, forwards the request, and streams the response back. It
saves model-generation requests once the response completes. Housekeeping
calls, such as token counting, are forwarded but not saved.

## Command options

| Option      | Default                         | Purpose                                      |
| ----------- | ------------------------------- | -------------------------------------------- |
| `--force`   | Off                             | Forget the saved choice and run setup again. |
| `--port`    | `8787`, or `PORT` when set      | Local port on `127.0.0.1`.                   |
| `--log-dir` | `.ai-agent-request-logger/logs` | Where captures are written.                  |

## Limits

- **HTTP only.** The proxy cannot capture WebSockets. It replies
  `426 Upgrade Required`, so clients that support fallback retry over HTTP.
- **GitHub Copilot is not offered through Pi.** Pi's GitHub Copilot sign-in
  sends requests to its own Copilot API URL. Setting `baseUrl` in
  `~/.pi/agent/models.json` does not route those requests through the logger,
  so the logger does not offer that route. Use the
  [GitHub Copilot CLI](#github-copilot-cli) route instead.
- **Pi compresses the Codex route with zstd.** The `.request.txt` file keeps
  the exact bytes. This standard-library build cannot decompress zstd, so the
  HTML view shows a warning instead of the request.
- **Cursor CLI and Amp cannot be captured.** Their system prompts and tool
  definitions are built on their servers, not sent from your machine.

[claude-gateway]: https://code.claude.com/docs/en/llm-gateway#subscriptions-and-gateways
[claude-headers]: https://code.claude.com/docs/en/llm-gateway-protocol.md#request-headers
[copilot-cli]: https://github.com/github/copilot-cli/blob/e1928292498bd60d8b8730381ed3818e82453711/_autodocs/api-reference/cli-interface.md
[copilot-config]: https://github.com/github/copilot-cli/blob/e1928292498bd60d8b8730381ed3818e82453711/_autodocs/configuration.md
[pi-models]: https://pi.dev/docs/latest/models
[pi-settings]: https://pi.dev/docs/latest/settings
