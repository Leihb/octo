# 竞品对标 — 落地路线（2026-05-27）

> 对照 Claude Code（`claude-code/src`，TS）与 Codex（`codex/codex-rs`，Rust）的实现，
> 排查 octo 在六个核心点上的差距，并排出落地路线。
> 前置：`core-review-2026-05.md`（六块核心评审）。A+B 加固已完成（PR #60–#65）。

---

## 现状：已对齐 vs 仍有差距

A+B 之后，octo 在以下点已与两家**基本对齐**：主循环预算闸门（`max_turns`/`max_cost`
优雅停，比 Codex 还全）、只读工具并行派发、权限 allow/deny/ask、摘要式压缩、分层
system prompt。

真正的差距集中在六个方向（竞品都有、octo 缺）：

| 核心点 | Claude Code | Codex | octo 差距 |
|--------|-------------|-------|-----------|
| 主循环 | AbortController 中断 + 错误恢复链 + 流式执行 | CancellationToken | 无优雅中断、无错误恢复、工具不与流式重叠 |
| Tool calling | sandbox-runtime（FS+网络白名单） | Seatbelt/Landlock+seccomp/bwrap | **无 OS 级沙箱**（只有正则 deny） |
| 持久化 | JSONL 追加 + parentUuid 链 | JSONL rollout 追加 + UUIDv7 | **单 JSON 每轮全量重写（O(n²)）** |
| system prompt | env 上下文（git/date）+ dynamic boundary | EnvironmentContext 片段 | **无 env 上下文**、无 boundary marker |
| 缓存 | tools+system+messages 多断点 + sliding | `prompt_cache_key`=会话ID + `previous_response_id` | **history 不缓存**；OpenAI 侧零措施 |
| 记忆/压缩 | autocompact + micro-compact + CLAUDE.md 层级 + memdir | 截断 + AGENTS.md 层级 + memories crate | **无 micro-compact**、压缩默认关、跨会话记忆仅单个 .octorules |

---

## 落地路线（按 价值 ÷ 成本 + 依赖 排序）

### 阶段 1 — 快赢（合计 ~1d，零风险）

| # | 项 | 谁有 | 成本 | 说明 |
|---|----|------|------|------|
| C1 | env 上下文进 prompt | 两家 | 0.5d | 注入 cwd / git 分支+status / 日期 / OS。**走 CC 路线**：作前置 user 消息或 system「动态段」+ boundary，别破坏 system 缓存前缀。 |
| C2 | OpenAI `prompt_cache_key` | Codex | 0.5h | 请求带 `prompt_cache_key = 会话ID`，OpenAI 自动前缀缓存。白送，独立。 |
| C3 | 结构化压缩摘要 prompt | CC | 0.5h | 把一行摘要指令换成 5 段模板（Primary Request / Key Concepts / Files & Code / Errors & fixes / Problem Solving）。改 `compaction.go` 一个常量。 |

> C1+C2+C3 可一个 PR 打包。

### 阶段 2 — 成本 + 上下文卫生（~2–3d）

| # | 项 | 谁有 | 成本 | 依赖 |
|---|----|------|------|------|
| C4 | Anthropic history 缓存（message 断点） | CC | 1d | double-marker：最后 1–2 条 message 打 `cache_control`，缓存 history 前缀。长 loop 里 history 是输入大头。 |
| C5 | micro-compact（单条 tool_result 截断） | 两家 | 1d | 按 token 中间截断超大 tool_result，不动整段历史。补 Ruby CATCHUP P2-2。 |
| C6 | 压缩默认开 + 窗口相对阈值 | 两家 | 0.5d | `--compact-threshold` 默认从 0 改成「窗口 − 余量」。需先拿真模型校验摘要质量（C3 后）。 |

### 阶段 3 — 持久化 + 记忆（~4–6d，可拆）

| # | 项 | 谁有 | 成本 | 说明 |
|---|----|------|------|------|
| C7 | append-only JSONL 持久化 | 两家 | 1.5d | 干掉每轮全量重写（O(n²)）：每条 entry 追加一行 + 后台 writer。顺带换 UUIDv7 会话 id。兼容旧 JSON。 |
| C8 | 跨会话记忆 · 第一层 | 两家 | 1.5d | user 级记忆文件 + 层级加载（managed < user < project）+ `@include`。扩展 `internal/prompt`。 |
| C9 | 跨会话记忆 · 第二层（靠后） | CC memdir / Codex memories | 3d+ | 类型化持久记忆（MEMORY.md + 相关记忆预取）。大，与 M7 skill 重叠，建议合并规划。 |

### 阶段 4 — 健壮性（~1.5d）

| # | 项 | 谁有 | 成本 | 说明 |
|---|----|------|------|------|
| C10 | 优雅中断（Ctrl-C） | 两家 | 1.5d | REPL 捕获中断 → 取消 ctx → 给在途/未执行工具合成 tool_result（历史保持 well-formed）→ 干净返回。 |
| C12 | 后台/并发命令执行 | 两家 | 1.5d | `terminal` 加 `background` 参数：脱离 30s 超时、不阻塞，返回后台进程 id；新增 `terminal_output` 工具读取输出+状态（类比 CC `BashOutput`）。后台进程天然并发，绕开只读并行闸门。会话退出杀掉所有后台进程。 |

> 错误恢复链（模型 fallback / output-token 升级 / prompt 超长自动压缩）优先级低、CC 专属，延后。

### 阶段 5 — OS 级沙箱（~5–7d，独立里程碑）

| # | 项 | 谁有 | 成本 | 说明 |
|---|----|------|------|------|
| C11 | OS 级命令沙箱 | 两家 | 5–7d | 最大安全差距，= M6.5 P1-2 遗留。macOS Seatbelt(sbpl) + Linux landlock+seccomp（或 bwrap），FS 读写根 + 网络白名单。`terminal` 执行前套沙箱。**不阻塞 M8**（strict 权限模式兜底），故压阵。 |

---

## 排序逻辑

阶段 1 几乎零成本的质量/成本提升，先做攒动量；阶段 2 攻「输入成本 + 上下文」两轴；
阶段 3 解决可扩展性和记忆能力；阶段 4 补 UX 健壮性；沙箱单独压阵——价值最高但成本/
风险最大，且有权限层兜底不阻塞 M8。

## 与现有 roadmap 的关系

- **C11 = M6.5 P1-2**（沙箱），把它正式纳入。
- **C9 与 M7（Skill 加载器）重叠**，建议合并规划。
- 其余（C1–C8、C10）是正交的 parity 硬化项，可与 M7/M8 并行推进。

## 备注

- Claude Code 那棵树有不少 feature-gated / 公开版缺失的代码；上表只取确属真实逻辑的部分。
- 调研依据：`claude-code/src`（`QueryEngine.ts` / `query.ts` / `services/compact/*` /
  `services/tools/*` / `utils/{sandbox,permissions,claudemd,api}.ts` / `memdir/`）；
  `codex/codex-rs`（`core/src/session/turn.rs` / `tools/*` / `compact.rs` / `client.rs` /
  `agents_md.rs` / `sandboxing/`）。
