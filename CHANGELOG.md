# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

> **History note.** This repository began life as the Ruby gem `octo` (a hard fork of [clacky-ai/openclacky](https://github.com/clacky-ai/openclacky) at upstream 1.1.6, numbered 0.10.0 here). The Ruby line ended at `v0.11.2-final-ruby`, preserved on the `archive/ruby` branch. From `0.1.0-dev` onward this is a clean Go reimplementation; the Ruby changelog is no longer carried forward.

## [Unreleased — 0.1.0-dev] — Go rewrite

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

### Notes
- The Ruby implementation under `lib/`, `spec/`, `bin/octo`, `Gemfile`, `Rakefile`, `octo-agent.gemspec`, `.rubocop.yml`, `Dockerfile` (Ruby base image), `homebrew/octo-agent.rb`, `scripts/`, `benchmark/`, `sig/`, and `POSITIONING.md` (Ruby-era) has been removed from `main`. The frozen Ruby tree remains accessible at `archive/ruby`.
- Repository was renamed `Leihb/octo` → `Leihb/octo-agent` on GitHub; old URLs continue to 301 to the new ones.
