# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased — 0.1.0-dev]

### Added
- **Go module scaffold** at `github.com/Leihb/octo-agent` (`cmd/octo` entry, `internal/{agent,provider,tools,version}`, Makefile, Go 1.22 CI matrix on Linux / macOS / Windows).
- **`octo chat` CLI** — single-turn and interactive REPL modes. Streams by default; `--stream=false` opts back into buffered output.
- **Anthropic Messages provider** — `x-api-key` auth, `anthropic-version` header, `content[].text` block parsing. `ANTHROPIC_BASE_URL` env override targets compatible third parties (DeepSeek, Kimi, OpenRouter Anthropic shim, etc.).
- **OpenAI Chat Completions provider** — Bearer auth, `system` carried as `messages[0]`, `choices[0].message.content` parsing. `OPENAI_BASE_URL` env override targets compatible third parties (DeepSeek, Bailian/Qwen, vLLM, etc.).
- **Streaming SSE** — native aggregators for both protocols (`content_block_delta`/`message_delta` for Anthropic, `chat.completion.chunk` for OpenAI with `[DONE]` sentinel tolerated). `Provider.SendStream` + agent-level `StreamingSender` / `Agent.TurnStream`.
- **Tool calling (agentic loop)** — `Agent.Run` / `Agent.RunStream`, normalized `ContentBlock` (`text` / `tool_use` / `tool_result`), provider-side tool-call decoding (OpenAI `finish_reason:"tool_calls"` normalised to `"tool_use"`).
- **`terminal` tool** — first concrete tool; runs `sh -c <command>` with a 30s timeout, returns combined stdout+stderr, surfaces non-zero exits as `[exit: ...]` annotations rather than Go errors so the LLM can read and adapt.
- **Session persistence** — JSON sessions under `~/.octo/sessions/<YYYYMMDD-HHMMSS>.json`, resume via `octo chat -c <id>`, list via `--list-sessions`, opt out with `--no-save`.
- **REPL slash commands** — `/help`, `/cost` (token + USD estimate, per-model pricing), `/save`, `/sessions`, `/exit`, `/quit`.
- **AgentEvent structured event stream** — `Agent.RunStream` takes an `EventHandler` that receives typed events (`text_delta`, `tool_started`, `tool_done`, `tool_error`, `turn_done`). Tool events carry `ToolID` + `ToolName` + `Input`; `Output` is truncated to 512 bytes for UI/IM previews while the agent's conversation history keeps the full result. The REPL wraps the handler as a text-only printer so its behaviour is unchanged.
- **Core tool suite** — seven new built-in tools available under `octo chat --tools`:
  - `read_file` — cat-style line-numbered read with offset/limit, capped at 2000 lines per call.
  - `write_file` — write/overwrite with `mkdir -p` semantics on the parent path.
  - `edit_file` — exact substring replacement; requires a unique match unless `replace_all=true`.
  - `glob` — file pattern matching with `**` support, sorted by mtime descending, skips `.git`/`node_modules`/`vendor`/`.venv`, capped at 200 results.
  - `grep` — `ripgrep` wrapper with `content` / `files_with_matches` / `count` modes, supports `-A`/`-B`/`-C` context and include-glob filtering.
  - `web_fetch` — URL → Markdown via the Jina Reader proxy, capped at ~200 KB with a clear truncation marker.
  - `web_search` — five-backend chain with priority `BRAVE > TAVILY > SERPER > DuckDuckGo HTML > Bing HTML`. Default is **zero-key**; paid backends opt in via env vars. The response `Provider` field tells the LLM which backend produced the results so it can reason about result quality. Inherits the hard-won HTML-scraping rules (no `Accept-Encoding` on Bing, browser-shaped header set, Bing `ck/a?u=a1<base64>` link decoding, DDG 10-minute cooldown on failure).
- **Tool registry** — new `internal/tools/registry.go` houses `DefaultRegistry` and `DefaultTools()` as a slice-driven dispatcher. Adding a new built-in tool means one entry in `allTools`.
