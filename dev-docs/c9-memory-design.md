# C9 — 跨会话记忆（typed auto-memory）design

> 路线图源：`competitive-parity-roadmap.md` C9（"跨会话记忆 · 第二层，类型化记忆"，原与 M7
> 合并规划；M7 已落地 #87，本文是 C9 单独成案）。
>
> 设计依据：内部调研《Agent Harness Auto-Memory 机制：Claude Code vs Codex 与第三方记忆层》
> （Obsidian vault `05-Learning/Agent 系统原理/`，2026-05-27）。下文凡引"调研"均指该文。

## 1. 目标与范围

给 octo 一层 **auto-memory**：agent 自己决定记什么、自己写、自己在后续会话召回。
区别于 octo 已有的 `octorules`（手写上下文文件，第一层）—— C9 是调研里说的**第二层**。

骨架统一是 **write → manage → read**（提取 → 整合 → 注入）。

本轮基调（已与用户对齐）：

- ✅ **贴 Codex 的写入质量**：独立提取（专门的 side-call + 独立 prompt，可配更便宜的
  model）判断 load-bearing vs noise。注入用整合过的 **summary**，不是全量索引。
- ✅ **即时写入（对标 CC 内联）**：会话中用户给反馈/偏好时，模型经 `remember` 工具
  **立即**落地，不必等会话结束的提取；边界提取补隐式信号 + 整合去重。
- ✅ **原生为主、自足**：不装任何插件，记忆也完整可用（提取 → typed 存储 → summary 注入）。
  **不含检索**。
- ✅ **检索不进 harness 核心**：做一个 **hook 插件机制**，让 **Hindsight** 作为**可选**的
  检索增强叠加（它本就是 hook 全自动 recall/retain + 本地 embedding）。不装插件 → 用
  原生 summary 注入；装了 Hindsight → 多一路检索召回。
- ✅ **和 compaction（C6）同一套 context-management 设计**：调研强调"真正决定 harness
  强弱的是 compaction 时机，auto-memory 跟它是一套系统"。提取复用 `a.summarize` 的
  side-call 模式（`internal/agent/compaction.go`）。

进程模型：原生层**自足**（无外部进程也能用），之上加一个**常驻 memory daemon**做真异步
——最贴 Codex 的「idle 检测 + 异步提取/整合」。daemon **离线**时记忆不失效，退回原生
fallback（退出标记 + 启动时提取/整合，对标 Claude Code 的 Auto Dream）；daemon **在线**时
接管，提取/整合常驻异步进行，不拖慢任何会话。两条路写同一套 `~/.octo/memory/`，经文件锁
互斥（对标 Codex 的全局锁）。

分三阶段，Phase 1 不依赖 daemon/插件，可独立交付：

- **Phase 1 — 原生 typed auto-memory（自足）**：提取 / typed 存储 / 整合 / summary 注入，
  触发用退出标记 + 启动时按需。无外部进程。
- **Phase 2 — 常驻 memory daemon**：接管异步提取/整合，idle 检测，生命周期管理；离线时
  fallback 到 Phase 1 路径。
- **Phase 3 — 插件机制 + Hindsight**：hook 扩展点 + 检索增强集成。

不做：向量/BM25 检索进核心（交给 Hindsight）；EEA 式地区限制。

## 2. 与现有系统的关系

```
第一层（手写）   octorules：~/.octo/octorules.md + .octorules   —— 已有，prompt.Compose 加载
第二层（自动）   C9 auto-memory：~/.octo/memory/                —— 本设计
context 管理     compaction（C6）：internal/agent/compaction.go —— 复用其 side-call 模式
```

C9 的注入层和 skills manifest 一样，落在 `prompt.Compose` 的**冻结 prefix** 里
（session-start 组装、provider 缓存、整场不动）——所以注入的是会话开始时就定下的
summary，**不**随对话中途新增的记忆刷新（那会让缓存失效）。新记忆在**下次会话**生效。

## 3. 存储模型（typed，一事一文件 + 注册表 + 注入摘要）

`~/.octo/memory/`，融合 CC 的「一事一文件 + 类型 frontmatter + MEMORY.md 索引」与 Codex 的
「注册表 + 注入摘要」分离：

```
~/.octo/memory/
  MEMORY.md            # 注册表/索引：一行一条（标题 + 钩子），管理与去重用
  memory_summary.md    # 注入用：整合产出的 summary，进 system prompt（read 层读它）
  <slug>.md            # 一事一文件：单条记忆 + frontmatter
```

单条 `<slug>.md` frontmatter（type 沿用调研里 CC 的语义分类，也是本仓库 auto-memory
正在用的形态）：

```markdown
---
name: <kebab-slug>
description: <一行摘要——整合/召回时判断相关性用>
type: user | feedback | project | reference
created: <YYYY-MM-DD>
last_verified: <YYYY-MM-DD>
---

<事实正文；feedback/project 附 **Why:** / **How to apply:** 行>
```

- **type 语义**：`user`（用户是谁/偏好）、`feedback`（怎么做事的纠正与确认，含 why）、
  `project`（与代码/git 无关的在研工作与约束）、`reference`（外部资源指针）。
- **MEMORY.md ≠ 注入源**：MEMORY.md 是注册表（整合时读它做去重/分类），注入进 prompt 的
  是 `memory_summary.md`（贴 Codex：整合过的紧凑摘要，避免 CC「全量索引一多就退化」）。
- 二阶性约束：`memory_summary.md` 是整合产物，不作权威事实源；需要细节时回查
  `<slug>.md`。

## 4. Write — 即时 `remember` 工具 + 边界提取

写入有两个来源（对标 Claude Code：主模型内联即时写 + 事后整合补全）：

### 4a. 即时：`remember` 工具（会话内，捕捉显式信号）

会话进行中用户给出**反馈 / 偏好 / 纠正 / 跨会话约束**时（"我希望你下次运行完测试再
提交"、"以后提交信息用中文"），**立即**落地，而不是等会话结束才提取。

- `remember` 工具加进 tools（像 skill 工具）：参数 `content` + 可选 `type`（模型判断
  user/feedback/project/reference）+ `description`。Execute 立即写 `<slug>.md` + 追加
  MEMORY.md，写前查 MEMORY.md 去重。
- **触发**：(a) 模型自主 —— base prompt 引导它识别到 load-bearing 用户反馈时调用；
  (b) 用户显式 —— `/remember <text>` 或自然语言"记住…"。
- **不刷新当前会话注入**：注入走冻结 prefix（§2/§6），即时写入只落盘、不重注入当前会话；
  但该反馈已在对话上下文里，当前会话本就遵守，记下来是为**下次**会话持久生效。
- 即时工具吃显式信号（低延迟、高精度，用户当场说的）；隐式信号 + 去重整合交给 4b 与 §5。

### 4b. 边界：提取 side-call（事后补全）

提取是一次专门的 side-call，与 `compaction.go` 的 `a.summarize` 同构：
`Sender.SendMessages(ctx, extractModel, extractSystem, msgs, maxTokens)`，独立 system
prompt，token 计入会话预算。

- **extractSystem**：独立提取 prompt，明确「记什么 / 不记什么」。对标 Codex 的 stage-one：
  只记 load-bearing（用户是谁、确认过的偏好/纠正、跨会话的项目约束、外部资源指针）；
  **不记** 仓库/octorules/CLAUDE.md 已有的、单任务琐碎纠正、实时状态、下次大概率翻盘的。
  输出结构化条目（每条带建议的 type + slug + description + body）。
- **extractModel**：默认主 model；可经配置覆盖为更便宜的 model（对标 Codex 的
  `extract_model` 钩子，但 octo 只暴露一个可选覆盖，不强求）。
- **触发点（双模式，都不阻塞退出）**：
  - **daemon 在线**：daemon 经 idle 检测发现会话停止活动（见 §7），异步跑提取，完全不碰
    会话进程。
  - **daemon 离线（fallback）**：`/exit`/EOF 时把本会话标记为"待提取"（session 元数据
    加 flag）；下次 `octo chat` 启动时检查待提取会话 → 跑提取。对标 Auto Dream 的
    "启动时做"，不拖慢退出。
  两条路写入同一套 `<slug>.md` + MEMORY.md，经文件锁互斥（§7）。`--no-memory` 全关。
- 写入：把提取出的条目写成 `<slug>.md` + 追加 MEMORY.md 一行。与已有 session
  持久化（`internal/agent/session.go`，`~/.octo/sessions/`）同目录体系。

## 5. Manage — 整合（daemon idle / 启动时按需）

随会话累积，`<slug>.md` 会重复/过时。整合是另一次 side-call，读 MEMORY.md + 相关条目，
做合并去重、淘汰过期、刷新 `memory_summary.md`。

- **触发（双模式）**：daemon 在线 → idle 时异步整合；daemon 离线 → 启动时按需，距上次
  整合 > N 天 **且** 累积 ≥ M 条新记忆（默认值评审定，起点 N=1、M=5，对标 CC Auto Dream
  的 24h+5session 量级）。
- **产物**：更新 `memory_summary.md`（注入源）+ 清理 `<slug>.md`/MEMORY.md。
- 失败非致命：整合 side-call 失败则保留现状，下次再试（与 maybeCompact 的容错一致）。

## 6. Read — 注入（summary 进冻结 prefix）

`prompt.Compose` 新增 memory 层，注入 `memory_summary.md` 的内容。

- 层位置（skills 之后、用户身份/规则之前——记忆是"跨会话用户上下文"，让用户显式
  规则仍可覆盖）：
  `base → soul → env → skills → memory → user.md → octorules(user) → octorules(project) → --system`
  其中 `soul` / `user.md` 由独立的**身份文件特性**提供（见 `identity-files-design.md`）；
  本设计只负责 `memory` 层。
- 与 skills 一致：caller（cmd/octo）读 `memory_summary.md` 渲染好传入 `Compose`，
  保持 prompt 包不做记忆 IO（单向依赖）。空则跳过该层。
- 冻结约束同 §2：注入的是会话开始时的 summary，整场不变。

## 7. Phase 2 — 常驻 memory daemon

`octo memoryd`：长期运行的守护进程，接管异步提取（§4）与整合（§5）；离线时一切退回
Phase 1 的启动时路径，记忆功能不丢。

- **生命周期**：`octo memoryd start|stop|status`。PID 文件 `~/.octo/memoryd.pid`；
  SIGTERM 优雅关闭（跑完手头的 side-call 再退）；start 时检测已在跑则报错退出。
- **感知会话结束（松耦合，已定 2026-05-28）**：daemon watch `~/.octo/sessions/` 的 mtime，
  两类信号：打了"待提取"标记的会话 daemon **立即**处理；无标记但 mtime 静默 ≥ **idle
  阈值（默认 15 分钟，可配）** 的视为异常退出/挂起，兜底提取。daemon 与 `octo chat`
  **互不依赖**：chat 不必通知 daemon，daemon 不在也不影响 chat。（否决 Unix socket IPC：
  更及时但把 chat 与 daemon 耦合，不值。）
- **并发与锁**：daemon 是 `~/.octo/memory/` 的主写者；fallback 路径下 chat 启动时也可能写。
  统一一把文件锁（`~/.octo/memory/.lock`，flock）互斥，对标 Codex 的全局锁。
- **provider 配置**：daemon 跑提取/整合 side-call 需要 key + provider，启动时从 env /
  `~/.octo/config`（若有）读取，与 chat 同源；缺配置则拒绝启动并提示。
- **跨平台**：macOS 经 launchd plist、Linux 经 systemd user service 托管（可选
  `octo memoryd install` 生成单元文件）；**Windows 降级** —— 不做 daemon，强制走 Phase 1
  启动时路径（与 sandbox 的 Windows 降级一致，fail-soft）。
- **自启动（已定：手动）**：MVP `octo memoryd start` 手动启动。自动 spawn / launchd 托管
  留待后续，不在首版。

## 8. Phase 3 — 插件机制 + Hindsight（检索增强）

原生层（§3–6）自足后，加一个**记忆插件机制**让外部检索层（Hindsight）叠加。Hindsight
在 Claude Code 里靠 **UserPromptSubmit hook**（prompt 前 recall，结果作 `additionalContext`
注入）+ **post-response hook**（自动 retain）+ `agent_knowledge_*` MCP tools。

octo 引入它的两条路径（**开放决策，Phase 3 评审定**）：

- **(倾向) 事件 hook（shell-out，CC 风格）**：octo 在 turn 边界暴露两个 hook 点——
  `pre-turn`（把 user input 交给外部命令，stdout 作 additionalContext 注入本轮）、
  `post-turn`（把本轮交给外部命令做 retain）。轻，落在现有 `runLoop`，Hindsight 的
  hook 模式直接适配。代价：octo 要新增一个通用 hook 配置/执行机制（roadmap 未规划）。
- (备选) MCP client：octo 实现 MCP client 调 Hindsight 的 `agent_knowledge_*` tools。
  通用性强（顺带支持其他 MCP server），但 MCP client 是更大的独立工作。

无论哪条，原生层都不动；插件只是多一路 recall 注入 + retain。不装插件 → 退化为纯原生
summary 注入。

## 9. 决策记录

- **D1（贴 Codex 写入 + 常驻 daemon 真异步）**：独立提取 + summary 注入取 Codex 的写入
  质量；并做常驻 memory daemon 实现 idle 异步提取/整合（最贴 Codex）。daemon 离线时退回
  退出标记 + 启动时整合，所以**原生层始终自足**，记忆不硬绑 daemon。
- **D2（原生为主，Hindsight 可选增强）**：原生层自足、无检索；检索经插件机制外包 Hindsight。
  不装插件零外部依赖仍可用。
- **D3（注册表与注入源分离）**：`MEMORY.md`（管理/去重）vs `memory_summary.md`（注入），
  对标 Codex；避免 CC「全量索引一多 adherence 下降」。
- **D4（type 沿用 CC 语义四类）**：user/feedback/project/reference，与本仓库 auto-memory
  现用形态一致，有现成活样本。
- **D5（注入走冻结 prefix）**：与 skills manifest 同约束，新记忆下次会话生效，不中途刷新
  以保 provider 缓存。
- **D6（提取/整合复用 compaction side-call 模式）**：与 `a.summarize` 同构，不另起机制。
- **D7（daemon 经 sessions mtime 感知会话结束，与 chat 松耦合）**：daemon 不依赖 chat
  通知、chat 不依赖 daemon 在线；Windows 降级到无 daemon。锁用 flock 单点互斥。
- **D8（即时 remember 工具 + 边界提取双路径，对标 CC）**：显式用户反馈经 `remember`
  工具会话内即时落地；边界提取补隐式信号 + 整合去重。即时写入因 prefix 冻结不刷新当前
  会话，下次会话生效。

## 10. 分阶段与切片

**Phase 1（原生自足，可独立合并）**

1. `internal/memory/` 包：`Store`（读写 `~/.octo/memory/` 的 `<slug>.md` + MEMORY.md +
   memory_summary.md）、`Entry`/type、`RenderInjection()`、flock 互斥。+ 测试。
2. `remember` 工具：即时写入（去重后落 `<slug>.md` + MEMORY.md）+ base prompt 引导模型
   在识别用户反馈时调用 + REPL `/remember`。+ 测试。
3. 边界提取：`agent` 层加 `extractMemory` side-call（复用 summarize 模式）+ extractSystem
   + extractModel 可选覆盖。+ 测试。
4. 触发接线：会话退出打"待提取"标记（session.go）；启动时检查待提取 + 按需整合
   （cmd/octo）。+ 测试。
5. 注入：`prompt.Compose` 加 memory 层；chat.go 两处调用点接线。+ 测试。
6. CLI/REPL：`/memory`（列出/开关）、`--no-memory`、`octo memory list`。+ 测试。

**Phase 2（常驻 daemon，单独 PR）**

7. `octo memoryd`：start/stop/status + PID 文件 + SIGTERM 优雅关闭；sessions mtime idle
   检测 → 异步提取/整合；fallback 协调（daemon 在线时 chat 跳过启动时路径）。+ 测试。
8. 跨平台托管：launchd / systemd 单元（可选 `octo memoryd install`）；Windows 降级。

**Phase 3（插件 + Hindsight，单独 PR/里程碑）**

9. 记忆插件机制（pre-turn / post-turn hook 点；形态 hook vs MCP 评审定）。
10. Hindsight 参考集成 + 文档。

每步 `make vet && make test`（race）+ gofmt；跨 OS `GOOS=linux/windows go build ./...`。

## 11. 开放决策点（评审拍板）

1. ✓ **已定（2026-05-28）daemon 感知会话结束**：监控 sessions mtime（松耦合）；否决 socket IPC。
2. ✓ **已定 idle 阈值**：默认 15 分钟（可配）；有退出标记的会话立即处理。
3. ✓ **已定 daemon 自启动**：MVP 手动 `octo memoryd start`；自动 spawn / launchd 留后续。
4. **整合阈值**：fallback 路径下 N 天 / M 条默认值（起点 N=1、M=5）。
5. **extractModel**：是否暴露独立（更便宜）model 覆盖，还是固定用主 model。
6. **注入层位置**：`memory` 放在 skills 之后 / user 之前（本文采用），还是紧跟 env。
7. **Phase 3 插件形态**：事件 hook（shell-out，倾向）vs MCP client。
8. **type 体系**：沿用 CC 四类是否够，要不要加 Codex 的时效分层（durable/recent）。

## 12. 测试（stdlib + httptest，无外部框架）

- `internal/memory`：Store 读写/round-trip、frontmatter 解析、MEMORY.md 索引一致性、
  RenderInjection 空/非空、整合去重、flock 两 writer 竞争互斥。
- `internal/agent`：extractMemory side-call（用 stub Sender，仿 compaction 测试）、
  整合触发条件、失败容错。
- `cmd/octo`：待提取标记 round-trip、启动时提取/整合接线、`Compose` memory 层位置、
  `/memory` 与 `--no-memory`。
- **daemon**：idle 检测（伪造 sessions mtime）、生命周期（start 重复检测 / stop /
  status）、fallback 协调（daemon 在线时 chat 跳过启动时提取）。用临时 `~/.octo`，
  stub provider，不起真网络。

---

## Appendix A — Codex 实测对照（2026-05-28）

读 `~/Projects/github/codex/codex-rs/memories/` 源码后整理。原 §1–§12 的基调和分阶段
仍然成立（Codex 风格 + 原生 fallback + 三阶段），但**真实实现比调研描述精细一个数量
级**，几个具体细节调研也漏了或不准；记录在此供 Phase 2/3 设计时参照，避免凭印象。

### A.1 真实架构（不是单一 store）

Codex memories 是**三个 crate + state DB + git baseline**：

| Crate | 职责 |
|---|---|
| `memories-read` | read path 注入 + **citations**（条目带 source 引用）+ usage 遥测 |
| `memories-write` | Phase 1 + Phase 2 + storage + workspace + extensions |
| `memories-mcp` | 把 memories 目录暴露成**只读 MCP server** 给 sub-agents |

存储两层：
- **State DB (SQLite)** — Phase 1 输出落这里，含 claim / lease / backoff / 并发上限，
  防止多个进程或 worker 重复处理同一 rollout。
- **文件系统 `~/.codex/memories/`** — **本身是 git repo**（`.git/` 在 root 下），用
  git workspace diff 决定 Phase 2 是否需要跑、得到 deletion 信号、回滚。
  - `memory_summary.md`（首行 `v1`，**always loaded into system prompt**）
  - `MEMORY.md`（handbook，按 keyword grep）
  - `raw_memories.md`（Phase 1 merged 临时输入）
  - `skills/<name>/SKILL.md`（**整合可以生成 skill！**）
  - `rollout_summaries/<slug>.md`（每个 rollout 一份详细 recap）
  - `extensions/<name>/` — 可插拔记忆源（`ad_hoc`、`prune`，外部产物可加入）

### A.2 两阶段（不是单 side-call）

- **Phase 1 — Rollout Extraction (per-thread)**
  - 触发：**root session 启动时**，异步后台跑（不是 idle 6h；6h 是 Phase 1 *选 rollout*
    时的过滤阈值，避免动 still-active 的 rollout）。
  - 用 state DB 做 claim/lease，并行处理多个 rollouts（concurrency cap）。
  - 模型：独立小模型（`stage_one.MODEL = "gpt-5.4-mini"` + 配置的 reasoning effort）。
  - **stage-one prompt = 569 行**（调研写"570 行"几乎精确）。
  - 输出 **JSON**：`{rollout_summary, rollout_slug, raw_memory}`，三者都可能为空（no-op gate）。
- **Phase 2 — Global Consolidation**
  - 全局锁（确实存在 — 调研里的"全局锁 sub-agent"指的是这个）。
  - 加载 Phase 1 outputs（按 `usage_count` + `last_usage`/`generated_at` 排序，过滤 `max_unused_days`）。
  - sync `raw_memories.md` + `rollout_summaries/`，**git diff** 决定 dirty。
  - 若 dirty → **spawn 内部 consolidation sub-agent**（不是 inline side-call），
    限制：no approvals / no network / local write only / no collab（防递归）。
  - sub-agent 拿 `phase2_workspace_diff.md` 作上下文，自己决定怎么改
    `memory_summary.md` / `MEMORY.md` / `skills/`。
  - 完成后 reset git baseline。
  - **consolidation prompt = 880 行**，有 INIT / INCREMENTAL UPDATE 两个 mode。

### A.3 调研对/错对照

| 调研说 | 实际 | 评 |
|---|---|---|
| stage-one prompt 570 行 | 569 行 | ✅ 精确 |
| idle 6h 触发整合 | 触发是 **session start**；6h idle 是 Phase 1 *选 rollout* 时的过滤 | ⚠️ 调研口径不准 |
| 全局锁 sub-agent | Phase 2 真的 spawn sub-agent 跑整合 | ✅ 存在 |
| 独立提取小模型 | gpt-5.4-mini + reasoning | ✅ 印证 |
| `MEMORY.md` 注册表 + `memory_summary.md` 注入 | ✅，且 summary 首行 `v1` 协议字段 | ✅ 完整 |
| Codex 没有检索 | 真的没（**memories-mcp** 只暴露 fs，没向量索引） | ✅ 印证 |
| — | git baseline tracking | 漏 |
| — | memory extensions（可插拔 sources：`ad_hoc`/`prune`） | 漏 |
| — | citations（条目带引用） | 漏 |
| — | 整合可生成 skill（`skills/<name>/SKILL.md`） | 漏 |
| — | read_path.md 130 行专门 prompt 教模型用 memory | 漏 |

### A.4 stage_one prompt 设计精华

我那 ~20 行 extractSystem 是玩具,Codex 569 行里值得借鉴的核心 pattern：

- **No-op gate**：在产出前显式问"未来 agent 会因为我写的东西做得更好吗?"。如果是
  one-off 查询 / 临时事实 / 平凡知识 / 没有偏好信号 → 返回**全空字段**。我们也说了
  "没东西输出 []"，但没有把它做成一个正式的 "minimum signal gate"。
- **Outcome triage**：每个 task 标 `success | partial | fail | uncertain`，fail/partial
  时 prompt 强调 prevention rules。我们只有 type 没有 outcome。
- **Evidence → Implication shape**：
  `当 <situation>，用户 said "<quote>" → <suggests future default>`。
  保留 user 原话 + 推断,而非压缩成结论。这是 prompt 整篇都在强调的。
- **User > Tool output > Assistant**：读 rollout 的优先级。**用户文本是 preference 的
  最强证据**；assistant brainstorming 不该被当 durable memory 除非用户采纳。
- **rollout_summary vs raw_memory 双产物**：summary 可以详细（作 reference），
  raw_memory 严格（durable preference 为主）。我们一条 fact = 一份 entry，没这个分层。
- **Anti-patterns 显式列出**：avoid generic advice / 大段 raw output / 长 procedural
  recap / 把 assistant 提议当 durable / "should use X" 推荐语言。

frontmatter 字段也更丰富：`description / task / task_group / task_outcome / cwd / keywords`。
我们：`name / description / type / created / last_verified`。

### A.5 consolidation prompt 设计精华

- **Progressive disclosure 是显式目标**：summary 始终注入 / `MEMORY.md` grep / rollout
  summaries 按需读 / skills 模板化复用。和我们的 `RenderInjection`（summary 优先 fallback
  到 entries）一致，但 Codex 多了 skill 生成层。
- **INIT vs INCREMENTAL UPDATE 两 mode**：第一次 vs 后续。INIT 时还要建最小骨架文件。
  我们 PR #96 后实际只有 INCREMENTAL（首次时 priorSummary 空,自然 INIT 化）—— 但没显式
  分 mode，prompt 措辞也没区分。
- **Phase 2 决策给 sub-agent 写文件，不是 side-call 拿字符串**。sub-agent 拿 diff，自己
  决定改哪些文件。我们 ConsolidateMemory 返回 string，cmd 写 `memory_summary.md`。

### A.6 我们 C9 vs Codex 差异表

| 维度 | 我们 C9 Phase 1 | Codex |
|---|---|---|
| 触发 | 启动时同步 / Phase 2 daemon idle | Session start 异步后台 |
| 提取模型 | 主 model（无 override） | 独立小模型 + reasoning effort |
| 提取 prompt | ~20 行 JSON 输出 | 569 行 + no-op gate + outcome triage |
| 写入产物 | 单条 `<slug>.md`(typed) | **rollout_summary + raw_memory + slug** 三件 |
| 整合执行 | side-call 返 string | spawn sub-agent，限权（no net/no approvals/no collab） |
| 整合 prompt | ~10 行 | 880 行，INIT/INCREMENTAL 两 mode |
| 整合产物 | `memory_summary.md` | summary + MEMORY.md + skills/ + 修 rollout_summaries |
| 状态管理 | `.state` JSON（last_extracted_session + last_consolidated） | SQLite state DB（claim/lease/backoff/usage_count/last_usage） |
| 增长控制 | PR #96 后归档到 `archive/` | git baseline + workspace diff |
| 检索 | 无（依赖 Phase 3 Hindsight） | 无（read 端纯 fs，sub-agent 经 MCP 访问） |
| 注入 prompt | 几行 base.md 提示 | 130 行 `read_path.md` + citations |

### A.7 推荐改进（按优先级）

基于上面的对照，C9 后续值得做的改进，按 ROI 排序：

1. **升级 stage_one prompt（高 ROI / 低风险）**——加 no-op gate + outcome triage +
   evidence→implication shape + user/tool/assistant 优先级。预期：显著提升 extract 质量，
   减少 like-Pre-commit-workflow-preference 那种空泛重复。
2. **升级 read_path 注入 prompt**——base.md 的 Memory 段从几行扩到 ~50–100 行，
   教模型怎么读 memory + citations 概念。预期：模型回答时能 ground 在 memory 上。
3. **`memory_summary.md` 首行加协议字段**（如 `v1`），便于将来格式升级 + 解析判定。
4. **git baseline 替换 archive 机制（中 ROI / 中改动）**——`~/.octo/memory/` init 成
   git repo，整合用 workspace diff 决定 dirty，archive 改为 git history。回滚天然，
   deletion 信号自然。**这能合并 PR #96 的 archive 逻辑**到 git 模型，更可审计。
5. **三件套产物**（per-rollout）：rollout_summary + raw_memory + slug。当前是单条 entry，
   失去了"reference vs durable"的层次。这会涉及 ConsolidateMemory 改签名/重写。
6. **Memory extensions（低 ROI / 中改动）**——可插拔 sources。本轮可不做，留作 future。
7. **整合改为 sub-agent 执行**——依赖 M10（sub-agent tool）。Phase 2 daemon 落地时
   一并考虑。

### A.8 验证来源

读源码片段：
- `~/Projects/github/codex/codex-rs/memories/README.md`（pipeline 文档）
- `~/Projects/github/codex/codex-rs/memories/write/lib.rs`（write 入口）
- `~/Projects/github/codex/codex-rs/memories/write/templates/memories/stage_one_system.md`（569 行）
- `~/Projects/github/codex/codex-rs/memories/write/templates/memories/consolidation.md`（880 行）
- `~/Projects/github/codex/codex-rs/memories/read/templates/memories/read_path.md`（130 行）
