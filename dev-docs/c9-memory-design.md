# C9 — 跨会话记忆（typed auto-memory）

> octo 的第二层上下文:agent 自己决定记什么、自己写、在后续会话召回。第一层是手写的
> `octorules`(`~/.octo/octorules.md` + `.octorules`,`prompt.Compose` 加载);本文是自动的
> 第二层 `~/.octo/memory/`。骨架是 **write → manage → read**(捕获 → 整合 → 注入),检索不
> 进核心(留给可选的 hook 插件)。

## 1. 目标

会话中出现值得跨会话保留的信号(用户偏好、纠正、约束、外部资源)时,agent 经 `remember`
工具即时落地;启动时把累积条目整合成紧凑 summary;summary 注入后续会话的系统提示。整套
原生自足、无外部进程、无向量检索;检索能力由 hook 插件(Hindsight)可选叠加。

`--no-memory` 整层关闭。

## 2. 存储模型

`~/.octo/memory/` 自身是一个 git repo:每次 `Save` / `WriteSummary` / `ArchiveCwd` 自动
提交,git history 即 archive(rollback、deletion 信号、审计都天然)。`git init` 是 lazy 的:
PATH 里没有 git 时静默降级,记忆仍可用。

```
~/.octo/memory/
  .git/                  # lazy 自动初始化;每次写一个 commit
  .gitignore             # 运行期排除 .lock / .state
  MEMORY.md              # 索引/注册表:一行一条(slug + 钩子),整合时读它做去重/分类
  memory_summary.md      # 注入源:整合产出的紧凑摘要,首行 v1 协议标记(全局桶)
  <slug>.md              # 一事一文件:单条记忆 + frontmatter
  .lock                  # advisory 文件锁(跨进程互斥写)
  .state                 # 整合游标(gitignored)
```

单条 `<slug>.md` 的 frontmatter:

```markdown
---
name: <kebab-slug>
description: <一行摘要——整合/召回判断相关性用>
type: user | feedback | project | reference
created: <YYYY-MM-DD>
last_verified: <YYYY-MM-DD>
cwd: <项目根;全局记忆省略>
---

<事实正文;feedback/project 附 **Why:** / **How to apply:** 行>
```

- **type 语义**:`user`(用户是谁/偏好)、`feedback`(怎么做事的纠正与确认,含 why)、
  `project`(与代码/git 无关的在研工作与约束)、`reference`(外部资源指针)。
- **索引 ≠ 注入源**:`MEMORY.md` 是注册表(整合读它去重),注入进 prompt 的是
  `memory_summary.md`——整合过的紧凑摘要,避免「全量索引一多 adherence 就下降」。
- **per-project 分桶**:`Store.WriteSummary(cwd, …)` / `ReadSummary(cwd)` 按项目根存独立
  summary(`cwd=""` 为全局),所以一个项目的事实不会泄漏进别的项目的注入上下文。
- **summary 协议标记**:`memory_summary.md` 首行恒为 `<!-- octo-memory v1 -->`,HTML 注释
  (Markdown 不渲染),`ReadSummary` 自动剥;给未来 schema 升级留版本通道。无标记的旧文件
  透传。
- `.state` 只存一个游标 `last_consolidated`(YYYY-MM-DD),驱动整合的 24h 节流。

## 3. Write — `remember` 工具

写入只有一条路:会话中模型经 `remember` 工具即时落地。`RememberTool.Execute` 立即写
`<slug>.md`、重建 `MEMORY.md`、git 提交,全程持 `.lock`。

驱动它的是 **per-turn memory nudge**(`cmd/octo/memory_nudge.go`):memory 与 tools 都开时,
每回合在用户消息尾部附一段 `<system-reminder>`,要模型扫描四类 durable 信号——(a) 偏好/
角色/约束、(b) 纠正/反驳**及其 why**、(c) 对非显然选择的默许、(d) 指向的外部资源——命中
就调 `remember`,否则静默跳过。提醒贴在决策点(而非埋在长系统提示顶部),模型对工具调用
指令的遵循率显著更高。

显式触发也走同一条:用户说「记住…」或当场给反馈时,模型调 `remember`。

## 4. Manage — 整合(consolidation)

随会话累积,`<slug>.md` 会重复/过时。整合在 **`octo chat` 启动时**按需触发
(`cmd/octo/memory_extract.go` 的 `maybeProcessMemory` → `consolidateIfDue`):活跃条目
**≥5 条** 且 距上次整合 **≥24h**。

按项目分桶(`ActiveNotesByCwd`),每桶各折进自己的 summary,双轨执行:

- **sub-agent 优先**:`consolidateViaSubAgent` 经 `launch_agent` spawn 一个受限子 agent,
  只给只读三件套 `["read_file", "grep", "glob"]`(`consolidationToolAllowlist`)。子 agent
  自己读 `<slug>.md`、查 `MEMORY.md`,在隔离 context 里产出新 summary 文本,比单次调用多出
  「自主翻文件」的能力。子 agent **不写文件**(避免绕开 v1 标记 / git commit),只回文本。
- **side-call 兜底**:无 spawner 或子 agent 失败时,落回 `a.ConsolidateMemory(priorSummary,
  newNotes)` 一次性 LLM 调用。

整合是 **incremental** 的:两条路都吃 `(priorSummary, newNotes)`,把新增条目折进现有
summary,不每次重建(priorSummary 空 = 首次)。产出由父调用 `Store.WriteSummary(cwd, …)`
写盘(加 v1 标记 + commit),再 `ArchiveCwd(cwd)` 删掉已整合的 `<slug>.md`(git history
留存;`ListArchived` 走 `git log` 还原)。

失败非致命:子 agent 或 side-call 报错都保留现状,下次启动再试。

## 5. Read — 注入

`prompt.Compose` 有一层 memory,注入 `RenderInjection(ProjectRoot(cwd))` 的产出:全局
summary + 当前项目 summary + 尚未整合的活跃条目(按项目过滤)。caller(cmd/octo)读
`Store.RenderInjection()` 渲染好传入 `Compose`,prompt 包不做记忆 IO(保持单向依赖)。

层位置——记忆是「跨会话用户上下文」,落在 skills 之后、用户身份/规则之前,让用户显式规则
仍可覆盖:

```
base → soul → env → skills → memory → user.md → octorules(user) → octorules(project) → --system
```

base.md 的 Memory 段教模型三件事:

- **Citations**:用 `(from memory: <slug>)` 内联标注 load-bearing 的记忆引用,避免把旧记忆
  当成用户当下的话;轻量,仅当某条记忆物质性影响本次回答时才注明。
- **Verify-first**:记忆里命名的 path/function/flag 动手前 `grep` / `read_file` 验证;背景
  信息(身份、风格)不必每次验证。
- **User contradicts memory → user wins**:调 `remember` 写下新事实,下次整合与旧的对账。

### 5a. Live cross-session delta

注入落在 `prompt.Compose` 的**冻结 prefix** 里(session-start 组装、provider 缓存、整场
不变),所以前缀里是会话开始时定下的 summary。要让「A 终端写的记忆,B 终端正在跑的会话
也能用上」,又不让中途重算击穿缓存,加一条**缓存安全的尾部 delta**:

- **注入点**:每回合 `runTurn`(`cmd/octo/turncore.go`)在 memory nudge 之后调
  `cfg.memRefresh.delta()`,把**本会话启动后新增**的条目作为 `<memory-update>` 块追加到
  user 消息**尾部**。尾部是当回合的新消息、本就未缓存,所以 system / tools / 历史前缀
  照常命中(断点是 system + tools + 最后两条消息)。
- **变更检测**:`Store.Version()` —— `MEMORY.md` 的 fnv 指纹(`Save` 每次重建索引),**持锁
  读**,未变即快速 no-op,变了才 `List()` 取条目。
- **并发读安全**:`Store.List()` 持 `.lock`(与写同一把),避免读到半写的索引/条目;持锁的
  内部调用方(`rebuildIndex` / `ArchiveAll` / `ArchiveCwd`)走无锁 `listEntries()` 防自死锁。
- **去重 / 项目过滤**:`memoryRefresher`(`cmd/octo/memory_refresh.go`)启动时把已注入的
  条目标记为 seen,`delta()` 只吐之后新增、且匹配当前项目的条目;读失败不推进 version,
  下回合重试。
- **范围**:仅新增**活跃条目**(整合改写 summary 的变化不热刷)。生效在**下一回合**,不打断
  在途回合。

## 6. 检索 — 不进核心,经 hook 插件

原生层不含向量/BM25 检索。检索作为**可选叠加**,经 `internal/hooks` 的两个 env 钩子点接入
外部检索层(如 Hindsight,见 `c9-phase3-hindsight.md`):

- `OCTO_HOOK_PRE_TURN`:每回合前 shell out(用户输入 → stdin JSON;stdout 文本 /
  `{additional_context}` JSON → 拼到 user message 后)——recall 注入本轮。
- `OCTO_HOOK_POST_TURN`:回合成功后 fire-and-forget(user + reply → stdin)——retain。
- `OCTO_HOOK_TIMEOUT`:默认 5s / 上限 30s,`WaitDelay 1s` 兜底防 child 卡住 pipe。错误降级
  为一行 stderr,回合继续。

形态与 Claude Code 的 `UserPromptSubmit` hook 一致,检索层脚本可直接复用。不配钩子 → 退化
为纯原生 summary 注入,零外部依赖。

## 7. 测试(stdlib + httptest,无外部框架)

- `internal/memory`:Store 读写/round-trip、frontmatter 解析、`MEMORY.md` 索引一致性、
  `RenderInjection` 空/非空、整合去重、`.lock` 两 writer 竞争互斥、`Version()` 随 Save 变化、
  `List()` 持锁与无锁 `listEntries()` 不自死锁。
- `internal/agent`:`ConsolidateMemory` side-call(stub Sender)、整合触发条件、失败容错。
- `cmd/octo`:启动时整合接线、`Compose` memory 层位置、`/memory` 与 `--no-memory`、
  `memoryRefresher` 只吐新增且按项目过滤。
