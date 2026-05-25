# Octo

[![Build](https://img.shields.io/github/actions/workflow/status/Leihb/octo/main.yml?label=build&style=flat-square)](https://github.com/Leihb/octo/actions)
[![Release](https://img.shields.io/gem/v/octo-agent?label=release&style=flat-square&color=blue)](https://rubygems.org/gems/octo-agent)
[![Website](https://img.shields.io/badge/website-octo--agent.dev-4f46e5?style=flat-square)](https://octo-agent.dev)
[![Ruby](https://img.shields.io/badge/ruby-%3E%3D%203.1.0-red?style=flat-square)](https://www.ruby-lang.org)
[![License](https://img.shields.io/badge/license-MIT-lightgrey?style=flat-square)](LICENSE.txt)

<p align="center">
  <a href="README.md">English</a> · <a href="README_CN.md">简体中文</a>
</p>

> ## 🚧 Octo is being rewritten in Go
>
> This Ruby implementation is **frozen** as of `v0.11.2-final-ruby`. The full source remains on the [`archive/ruby`](https://github.com/Leihb/octo/tree/archive/ruby) branch and you can still `gem install octo-agent` to use it, but no new features or bug fixes will land on this line.
>
> The next-generation Octo is being built in Go to ship as a single binary (zero install dependencies), with native Windows support and the same three-interface, three-protocol design. Track progress on `main` here, or watch the repo for releases. Estimated first Go alpha: 3-4 months.

> This project is a hard fork of [clacky-ai/openclacky](https://github.com/clacky-ai/openclacky). The original MIT copyright is preserved in [LICENSE.txt](LICENSE.txt); modifications are © 2026 Leihb (roy).

A **functionality-first** AI agent with three equal interfaces.

Octo is a Ruby tool for interacting with AI models. It speaks **Anthropic Messages**, **OpenAI** (Chat Completions + Responses), and **AWS Bedrock** natively, and works with any other provider that exposes one of those API shapes. It provides chat functionality and autonomous AI agent capabilities with tool use. Use it in the **terminal**, in a **web browser**, or through **instant messaging** — all three interfaces are first-class citizens with identical capabilities.

## Philosophy

- **Three faces, one agent** — CLI, Web, and IM are all first-class. No interface is secondary
- **Open skills** — Compatible with Claude Code skill format. Install any community skill without friction
- **Token-pragmatic** — Uses tokens wisely, but never at the expense of getting the job done right

## What Octo Is Not

- Not a token-minimization obsession — functionality comes first
- Not web-first — no master-worker architecture imposed on local CLI usage
- Not a marketplace — no encrypted skills, no commercial skill ecosystem

## Features

| Feature | Description |
|---|---|
| **Interactive CLI** | Start an agent session directly in your terminal |
| **Web UI** | Full chat interface with multi-session support at `localhost:8888` |
| **IM Integration** | Feishu, WeCom, WeChat, Discord, Telegram — all with full parity |
| **Skills** | Install, create, and evolve skills in standard Markdown format |
| **BYOK** | Bring your own API key — any Anthropic / OpenAI / Bedrock-compatible model |
| **Autonomous agent** | ReAct pattern with tool execution for complex tasks |

## Installation

### RubyGem

Requires Ruby >= 3.1.0

```bash
gem install octo-agent
```

### One-line installer (macOS / Ubuntu)

```bash
/bin/bash -c "$(curl -sSL https://raw.githubusercontent.com/Leihb/octo/main/scripts/install.sh)"
```

### Windows

```powershell
powershell -c "& ([scriptblock]::Create((irm 'https://raw.githubusercontent.com/Leihb/octo/main/scripts/install.ps1')))"
```

## Quick Start

### Terminal

```bash
octo            # start interactive agent in current directory
```

### Web UI

```bash
octo server     # default: http://localhost:8888
```

Options:

```bash
octo server --port 8080        # custom port
octo server --host 0.0.0.0     # listen on all interfaces
```

### Configuration

```bash
$ octo
> /config
```

Set your **API Key**, **Model**, and **Base URL**. Octo routes each model to its native protocol — Anthropic Messages, OpenAI (Chat Completions / Responses), or AWS Bedrock — so you keep features like Claude's `cache_control` byte-for-byte instead of going through a lossy OpenAI shim.

Supported out of the box: **Claude (Anthropic) · GPT (OpenAI) · DeepSeek · Kimi (Moonshot) · MiniMax · OpenRouter · AWS Bedrock · Qwen** — or any custom endpoint that speaks one of the three protocols.

## Skills

Skills are the primary way to extend Octo's capabilities. A skill is a Markdown instruction file that guides the agent to accomplish a specific task using existing tools.

- **Invoke with `/`** — fuzzy search and call any installed skill
- **Create in natural language** — describe what you want; the agent drafts `SKILL.md`
- **Self-evolving** — skills improve based on execution context and results
- **Open format** — compatible with Claude Code / Markdown Pack / custom formats

Skill directories:

- Built-in: `lib/octo/default_skills/`
- Project-level: `.octo/skills/`
- User-level: `~/.octo/skills/`

## Example Usage

```bash
$ octo
> /new my-app        # scaffold a new project
> Add user auth with email and password
> How does the payment module work?
```

## Install from Source

```bash
git clone https://github.com/Leihb/octo.git
cd octo
bundle install
bin/octo
```

## Contributing

Bug reports and pull requests are welcome on GitHub at https://github.com/Leihb/octo. Please read [CONTRIBUTING.md](./CONTRIBUTING.md) before opening a PR.

## License

Available as open source under the [MIT License](https://opensource.org/licenses/MIT).
