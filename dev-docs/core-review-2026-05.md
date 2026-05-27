# 核心评审 — 2026-05-27

> 对 octo agent harness 六个「重中之重」模块的代码评审：主循环、tool calling、
> session 管理、system prompt、cache 优化、memory 机制。其余皆细枝末节。
> 评审基于 main @ `fd8b8c0`（M6.5 全部落地后）。结论：三块机制健全有小疵，
> 三块完全缺失且互相耦合——后者才是「能过测试的工具循环」与「真正好用且省钱的
> agent」之间的差距。

---

## 总览：严重程度矩阵

| 模块 | 状态 | 最严重的问题 |
|------|------|-------------|
| 主循环 | 🟡 健全有疵 | `Run`/`runStreamLoop` 双份重复循环 |
| Tool calling | 🟡 健全有疵 | 多工具串行派发 |
| Session 管理 | 🟡 健全有疵 | ID 精确到秒 → 冲突覆盖 |
| **System prompt** | 🔴 缺失 | 默认空 prompt，零行为指引 |
| **Cache 优化** | 🔴 缺失 | 无 `cache_control`，输入成本数倍浪费 |
| **Memory 机制** | 🔴 缺失 | 无界 history → 上下文溢出哑雷 |

---

## 1. 主循环（`internal/agent/agent.go`）

**扎实**：buffered（`Run`）与 streaming（`RunStream`）两条路分得干净；首轮出错回滚
history（`agent.go:246`、`387`）；`maxToolIterations=20` 防死循环。

**问题**：

- **双份重复循环**（`Run` `agent.go:243-281` vs `runStreamLoop` `383-433`）。
  权限 gate 是手动加到**两处**的（`261`、`404`）——正是会引发分叉的结构。
  → 合并成单循环 + 可选事件输出（nil handler 即 buffered 行为）。
- **`dispatchTools` 的 `error` 返回是死代码**。工具失败、gate 拒绝都变成
  `IsError` block，函数实际永远返回 nil；`405` 行的 `if err != nil { return }`
  看着像处理真实分支，其实不可达。→ 要么去掉返回值，要么让它承载真正的致命错误
  （如 ctx 取消）。
- **撞迭代上限直接硬失败**（`432`）。第 20 次工具调用后返回 error，整个 turn 工作
  全丢。合理的 21 步任务就此报废。→ 做检查点 / 询问是否继续，而非 error 退出。

---

## 2. Tool calling（`dispatchTools` + `content.go`）

**扎实**：`ContentBlock` 联合类型干净；tool_use/tool_result 用 ID 配对；
OpenAI `tool_calls`→`tool_use` 归一化；gate + streaming 都接好。

**问题**：

- **多工具串行派发**（`agent.go:518` 循环）。模型一个 turn 发多个 tool_use block 时
  （Claude 常见），只读工具（`read_file`/`glob`/`grep`）只能逐个跑。纯延迟浪费，
  正确性无碍。→ 对已知无副作用的工具并发派发。
- **派发层无超时**，全靠每个工具自律（仅 `terminal` 有 30s）。→ 派发层加一个可配置
  的总超时兜底。

---

## 3. Session 管理（`internal/agent/session.go`）

**问题**：

- **ID 精确到秒的时间戳**（`session.go:30`）。同一秒起两个 session → 静默覆盖/损坏。
  注释自称「可接受」，但任何非交互流程一碰就是数据丢失 bug。→ 加短随机/计数后缀。
- **`TurnCount = len/2`**（`session.go:40`）对用工具的 session 算错——tool_use/
  tool_result 消息撑大计数，"N turns" 失真。→ 按 user-role 消息数或显式 turn 标记计。
- **每 turn 全文件重写**（自动保存）→ 整 session 写入字节 O(n²)。交互够用，规模化糙。
  → 追加写 / 增量持久化，或降低保存频率。

---

## 4. System prompt — 🔴 真实缺口

- **默认 system prompt 为空**（`cmd/octo/chat.go`：`fs.String("system", "", …)`）。
  一个能用工具的 agentic CLI 出厂**零**行为指引：模型拿到工具 schema，却完全不知道
  怎么用好工具、输出风格、read-before-write 纪律、权限态势，甚至不知自己叫 "octo"。
- **没告诉模型权限层存在** → 它会盲目尝试被拒操作，只能靠 error 反馈学到——我们刚建好的
  M6.5 机制反而在制造浪费的 turn。
- **无组合层**：没有「基础 octo prompt + 用户 `--system` + 项目上下文」的分层拼装。

→ **agent 质量上单点杠杆最高的缺失项。** 应建一个 system prompt 组合器：内置基础
prompt（身份、工具使用规范、权限说明、read-before-write 约定）+ 用户覆盖 + 项目上下文。

---

## 5. Cache 优化 — 🔴 完全没做

- 任何地方都无 Anthropic `cache_control: ephemeral`（`anthropic/client.go:86`、
  `anthropic/stream.go` 构造请求时无任何 cache 断点）。**Anthropic 是默认 provider，
  且不加断点就不缓存。**
- agentic 循环每轮按全价重发 system prompt + 工具定义 + **不断增长的整个 history**。
  一个 10 次工具调用的 turn，输入成本约是应有的 **10 倍**。
- 自然断点：(a) system + tools（session 内稳定），(b) history 前缀。Anthropic 最多 4 个
  断点。缓存前缀省约 **90% 输入成本**，改动极小。
- 与 #6 的无界 history 成对：缓存让重发 history 便宜，但仍会撞上下文窗口上限。
- OpenAI 自动缓存（无需改 API）；Anthropic 必须显式 `cache_control`——所以这是
  默认 provider 上一笔具体、高价值的 TODO。

---

## 6. Memory 机制 — 🔴 没做

- 无跨 session 记忆；无项目上下文加载（`.octorules` / `AGENTS.md` 从不读进 prompt）；
  无旧 turn 压缩。
- **无界 history 是颗哑雷**：`runStreamLoop` 每轮快照**完整** history（`agent.go:384`），
  零截断。测试里没事；真实长 session 会撑爆上下文窗口、成本二次方增长。
- 压缩/摘要是结构性解法，且与 memory 是同一套机器：旧 turn 摘要成「记忆」，既控上下文
  又可跨 session 持久化。

→ 最小可行第一步：自动把 `.octorules` / 项目文档读进 system prompt（与 #4 共用组合器）。

---

## 优先级建议

机制健全的三块（主循环、tool calling、session）只是有可修小疵；缺失的三块
（system prompt、cache、memory）互相缠绕，是真正的差距。建议顺序：

1. **System prompt + 组合层**（含项目上下文加载）——质量杠杆最大，顺带把权限态势告诉模型。
2. **Anthropic prompt caching**——成本杠杆最大，改动小，正好默认 provider。
3. **History 压缩 / memory**——拆掉上下文溢出哑雷；cache 让它更便宜，memory 让它持久。
4. 收尾：主循环去重、session ID 后缀、并行派发。

> #1 与 #2 耦合且价值最高（system prompt 是 cache 的天然第一个断点），建议捆在一起做。

---

## 附录：Ruby 版的现成解法（`archive/ruby` 分支）

评审完才发现：本文点出的三大缺口，正是 Ruby 版创始人文章里称的「真正的架构问题」，
而 Ruby 版当时**已有实战解法（90%+ cache 命中）**，go 重写时整批没跟过来。
源文档在 `archive/ruby` 分支：`CATCHUP_PLAN.md`、`engineering-article.md`。

### A. `CATCHUP_PLAN.md` —— Ruby 版已实现、go 版丢了

| 项 | Ruby | go 版 | 对应本文 |
|----|------|-------|---------|
| **P0-4 `max_turns` / `max_cost_usd` 主循环闸门** | ✅ 已做 | ❌ 仅硬编码 `maxToolIterations=20`，无成本闸门，撞限硬报错丢工作 | 主循环 #3 |
| P0-1 Agent/Task 子代理工具 | ✅ | ❌（= go M10） | — |
| P0-3 `config.yml` 用户 hook | ✅ | ❌ | — |
| P1-1 子代理预设 explore/plan/verify | ✅ | ❌ | — |
| P2-2 micro-compact（按 token 截单条 tool result） | ⬜ todo | ❌ | memory #6 |
| P2-3 项目记忆 `.octo/memories/` + 分层 merge | ⬜ todo | ❌ | memory #6 / sysprompt #4 |

### B. `engineering-article.md` —— Ruby 版**已 ship** 的 cache 七决策（取相关 4 个）

核心论点：**「每个 agent 功能都是一个缓存失效面」**，架构应围绕单 agent 的 cache 命中率设计。

| 决策 | 内容 | go 版 | 对应本文 |
|------|------|-------|---------|
| **D1 双 cache 标记** | 每轮打**两个** `cache_control` 断点（非一个），扛住 history 增长 / 工具重试 / 中途换模型三种失效 | ❌ 零 `cache_control` | #5 |
| **D2 冻结 system prompt** | session 启动组装一次后冻结；skill 中途装不重渲染（重渲染 = 全员每轮 miss） | ❌ 默认空 prompt，无 builder | #4 |
| **D4 稳定工具 schema** | schema 紧跟 system prompt，一变则其后全失效；Ruby 刻意锁在 16 工具 | 🟡 8 工具，但无「schema 稳定性=cache」意识 | #2 |
| **D5 insert-then-compress** | 压缩不重写历史（=必然 miss），而是**追加**摘要复用已缓存前缀 → 压缩调用本身 95% 命中 | ❌ 无压缩，history 无界 | #6 |

### 捡回清单（具体模式，非「做个 cache」）

1. **System prompt** → 端口 Ruby `SystemPromptBuilder`：**启动组装一次、冻结**，分层
   `built-in < user < project`（D2 + P2-3）。同时是 cache 的天然第一个断点。
2. **Cache** → 端口**双 cache 标记**（D1）+ 保持工具 schema 稳定（D4）。要按两标记法放断点，
   不是简单加一个 `cache_control`。
3. **压缩/memory** → 端口 **insert-then-compress**（D5）：压缩追加摘要而非重写历史，
   让压缩调用复用缓存前缀。
4. **主循环闸门** → 捡回 P0-4：`max_turns` + `max_cost_usd`，撞限友好退出而非硬报错。

> 这批不用重新设计——Ruby 版踩过两代失败（RAG / 多代理）才收敛出来，文章与 CATCHUP 写得很细。
> 取用前仍需对照 go 版当前结构验证（如 `cache_control` 在 `anthropic/client.go` 请求体里的落点）。
