# Octo

[![Build](https://img.shields.io/github/actions/workflow/status/Leihb/octo/main.yml?label=build&style=flat-square)](https://github.com/Leihb/octo/actions)
[![Release](https://img.shields.io/gem/v/octo?label=release&style=flat-square&color=blue)](https://rubygems.org/gems/octo)
[![Ruby](https://img.shields.io/badge/ruby-%3E%3D%203.1.0-red?style=flat-square)](https://www.ruby-lang.org)
[![License](https://img.shields.io/badge/license-MIT-lightgrey?style=flat-square)](LICENSE.txt)

<p align="center">
  <a href="README.md">English</a> · <a href="README_CN.md">简体中文</a>
</p>

> 本项目硬分叉自 [clacky-ai/openclacky](https://github.com/clacky-ai/openclacky)。上游 MIT 协议及原作者版权完整保留于 [LICENSE.txt](LICENSE.txt)；后续修改 © 2026 Leihb (roy)。

一个**功能优先**的 AI Agent，三种界面一视同仁。

Octo 是一个 Ruby 工具，用于与 AI 模型交互。原生支持 **Anthropic Messages**、**OpenAI**（Chat Completions + Responses）和 **AWS Bedrock** 三套 API 协议，也兼容任何复用这些协议的第三方端点。提供聊天功能和具备工具执行能力的自主 AI Agent。你可以在**终端**、**浏览器**或**即时通讯**中使用它 —— 三种界面都是一等公民，能力完全相同。

## 理念

- **三面一体** — CLI、Web、IM 都是一等公民，没有次要界面
- **开放技能** — 兼容 Claude Code 技能格式，无缝安装社区技能
- **Token 务实** — 合理使用 token，但绝不为了省钱而牺牲功能正确性

## Octo 不是什么

- 不是 token 最小化执念 — 功能优先
- 不是 web 优先 — 本地 CLI 使用不受主从架构约束
- 不是应用市场 — 没有加密技能，没有商业化技能生态

## 特性

| 特性 | 说明 |
|---|---|
| **交互式 CLI** | 直接在终端启动 Agent 会话 |
| **Web UI** | 完整的聊天界面，支持多 Session，`localhost:7070` |
| **IM 集成** | 飞书、企微、微信、Discord、Telegram —— 全部能力对等 |
| **Skills** | 以标准 Markdown 格式安装、创建和进化技能 |
| **BYOK** | 自带 API Key —— 任意 Anthropic / OpenAI / Bedrock 协议模型 |
| **自主 Agent** | ReAct 模式配合工具执行，处理复杂任务 |

## 安装

### RubyGem

需要 Ruby >= 3.1.0

```bash
gem install octo
```

### 一键安装（macOS / Ubuntu）

```bash
/bin/bash -c "$(curl -sSL https://raw.githubusercontent.com/Leihb/octo/main/scripts/install.sh)"
```

### Windows

```powershell
powershell -c "& ([scriptblock]::Create((irm 'https://raw.githubusercontent.com/Leihb/octo/main/scripts/install.ps1')))"
```

## 快速开始

### 终端

```bash
octo            # 在当前目录启动交互式 Agent
```

### Web UI

```bash
octo server     # 默认地址：http://localhost:7070
```

选项：

```bash
octo server --port 8080        # 自定义端口
octo server --host 0.0.0.0     # 监听所有接口
```

### 配置

```bash
$ octo
> /config
```

设置你的 **API Key**、**模型**和 **Base URL**。Octo 会按模型分发到对应的原生协议 —— Anthropic Messages、OpenAI（Chat Completions / Responses）或 AWS Bedrock，避免走 OpenAI 兼容 shim 时丢失 Claude `cache_control` 等特性。

开箱即支持：**Claude (Anthropic) · GPT (OpenAI) · DeepSeek · Kimi (Moonshot) · MiniMax · OpenRouter · AWS Bedrock · Qwen**，或任意走上述三种协议之一的自定义端点。

## Skills

Skills 是扩展 Octo 能力的主要方式。一个 skill 是一份 Markdown 指令文件，指导 Agent 使用现有工具完成特定任务。

- **`/` 唤起** — 模糊搜索并调用已安装 skill
- **自然语言创建** — 描述你想要什么，Agent 自动起草 `SKILL.md`
- **自我进化** — 根据执行上下文和结果持续改进
- **开放格式** — 兼容 Claude Code / Markdown Pack / 自定义格式

Skill 目录：

- 内置：`lib/octo/default_skills/`
- 项目级：`.octo/skills/`
- 用户级：`~/.octo/skills/`

## 使用示例

```bash
$ octo
> /new my-app        # 创建新项目脚手架
> 添加邮箱密码登录功能
> 支付模块是怎么实现的？
```

## 从源码安装

```bash
git clone https://github.com/Leihb/octo.git
cd octo
bundle install
bin/octo
```

## 参与贡献

欢迎在 GitHub 提交 Bug 报告和 Pull Request：https://github.com/Leihb/octo 。提 PR 前请先阅读 [CONTRIBUTING.md](./CONTRIBUTING.md)。

## 许可证

基于 [MIT 协议](https://opensource.org/licenses/MIT) 开源发布。
