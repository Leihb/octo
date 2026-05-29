# TUI UX 升级（design）

> 把交互式 bubbletea TUI 从「比 plain 路径还朴素」升级到对标 Claude Code（Ink）的体验：
> 富工具卡片、底部状态栏 + 边框输入框、富 spinner、glamour markdown、面板化 + 统一主题。
> 保持 **inline 模式**（不进 alt-screen），用 octo 现有的 bubbletea / lipgloss / chroma 栈实现，不引入 Ink（那是 JS）。

---

## 1. 背景与目标

### 现状（grounded）

#149 落地的交互式 TUI（`cmd/octo/tuirepl.go` + `tuirepl_view.go`）是 inline 模式：committed 输出靠 `tea.Println` 进终端 scrollback，底部 live 区（`View()`）= partial 助手行 + `thinking…` + 队列 + `you> ▏` 输入行 + 一行键位提示。它能用，但视觉上很朴素。

一个**已存在但未被 TUI 复用的渲染器**值得先讲清楚（避免高估现状）：`internal/tui/diffcard.go` 的 `RenderEditCard`（true-color、行号、±标记、真·chroma 高亮——`highlightLine` 用 `terminal16m` formatter + `github-dark` 主题）渲染一张精致 diff 卡片。它**只接在 `plainView`（非-TTY / `--no-tui` 回退视图）的 `replToolEventHandler` 上**，而且：

- **仅 `edit_file`** 一种工具走卡片（`rendersAsCard`，`repl.go:643-652` 只对 edit_file 返回 true）；terminal/grep/read 在回退视图里也只是 `↳ tool ✓` 一行。
- **`--plain` 会把卡片关掉**（`rendersAsCard(name, true)` 返回 false）——所以「plain」恰恰是无卡片的那一档。
- **bubbletea TUI 完全没接它**：`handleEvent`（`tuirepl.go` ~228）连 edit_file 都是一行 `↳ tool ✓`。

| 维度 | TUI（`tuirepl.go`） | 非-TTY 回退视图（`plainView`，非 `--plain`） |
|---|---|---|
| edit_file | 一行 `↳ tool ✓` | **chroma 高亮 diff 卡片**（`RenderEditCard`） |
| terminal / grep / read | 一行 `↳ tool ✓` | 一行 `↳ tool ✓`（两边都无卡片） |
| 助手文本 | 原始 text delta | 原始 text delta（两边都无 markdown） |
| 输入 / 状态 | `you> ▏` + 一行 hint | n/a（非交互） |

结论：现成的 chroma diff 卡片**只覆盖 edit_file、只在回退视图、且 `--plain` 关闭**，其实是放错了层——卡片属于交互式 TTY，不属于 headless/管道输出（决策 #8）。`chroma` 已是依赖。所以本设计要做的是 **(a)** 把 edit_file 卡片**从 plainView 下沉到 TUI**（plainView 退回纯文本一行），**(b)** 在 TUI 为 terminal/grep/read 等新建输出预览卡片（**两侧目前都没有**），**(c)** 富化 live 区与主题。卡片渲染统一为 **TUI-only**。

### 目标

对标 CC 的 Ink TUI 体验，在 bubbletea 里实现：

1. **富工具卡片**：diff 卡片（edit_file）、输出预览框（terminal/grep/read）、带 spinner→✓/✗ 的状态头。
2. **底部状态栏 + 边框输入框**：model · cwd · context% · cost · 权限模式 · 计时。
3. **富 spinner**：计时、轮转提示词、token/cost 跳动。
4. **glamour markdown**：助手文本的标题/粗体/列表/引用/代码块渲染。
5. **面板化 + 统一主题**：队列/后台/模态做成边框面板；自适应（亮/暗）主题色板，收拢现在散落的 style。

### 非目标（见 §9）

alt-screen / 内部 scrollback viewport（决策保留 inline）；Web/IM 端渲染；鼠标 / 内联图形；mswe-eval 等 headless 路径（纯文本不变）。

---

## 2. 决策记录（grill 已定）

| # | 决策 | 选择 | 理由 |
|---|---|---|---|
| 1 | 布局架构 | **inline + 富化 live 区**，不进 alt-screen | 保留终端原生滚动 / 鼠标选择复制；`tea.Println` 提交卡片到 scrollback，`View()` 做 pinned 的状态栏 + 输入框 + 瞬态面板。与 CC 新版一致，改动可控 |
| 2 | markdown | **glamour 全量渲染** | 最接近 CC；新增依赖 `charmbracelet/glamour`（其内部也用 chroma，与现有依赖同源） |
| 3 | 工具卡片 | 复用并扩展 `internal/tui` | 已有 `RenderEditCard`；新增 output-preview 卡片 + 状态头卡片。TUI 接进来，先抹平与 plain 的差距再超越 |
| 4 | 运行中 vs 已完成的卡片 | 运行中（spinner）放 `View()` live 区；`EventToolDone/Error` 时定版成卡片用 `tea.Println` 提交 | inline 下 committed 行不可再变；动态状态只能活在 live 区，终态才落 scrollback |
| 5 | markdown 流式 | 流式期间 partial 区出**原始**文本；到 **block 边界**（空行 / 闭合 ``` ）才把该 block 经 glamour 渲染后 `tea.Println` 提交 | glamour 要完整 block 才渲染对；逐 delta 渲染会抖。块级提交兼顾流式手感与正确渲染 |
| 6 | 主题 | 统一 `internal/tui` theme 包，`lipgloss.AdaptiveColor` 亮/暗自适应，收拢 `tuirepl_view.go` + `diffcard.go` 散落 style | 一处定义、两处（TUI/plain 卡片）共享 |
| 7 | 落地策略 | **分期多 PR**（P1–P5，§8），每期独立可发 | UX 改动面大；分期便于 review、回归、按你反馈调方向。区别于 tui-input-modes 的单大 PR |
| 8 | 卡片归属 | **卡片是 TUI-only**；`plainView`（非-TTY / `--no-tui`）一律纯文本，**移除现有接在 plainView 上的 edit_file 卡片**（下沉到 TUI 层） | 卡片是 ANSI true-color 框，在管道 / CI / mswe / 重定向输出里是噪声；交互式 TTY 才是它的归宿。现状把卡片接在 plainView 是放错层，本设计纠正 |
| 9 | headless 行为 | `plainView` 的工具事件退回 `↳ tool ✓/✗` 一行（含 edit_file）；`--no-tui` / 非-TTY / mswe 不变地走纯文本 | 简化 `replToolEventHandler`（去掉 `rendersAsCard`/`renderToolCard` 分支）；headless 输出更干净可解析 |

---

## 3. 架构

inline 模型不变，富化两侧：**committed scrollback**（`tea.Println` 富字符串）与 **live 区**（`View()`）。

```
  ┌─ 终端原生 scrollback（tea.Println 提交，向上滚）──────────────┐
  │  you> 给 booking 详情加缓存                                  │
  │  ● Edit  internal/cache/booking.go  (+12 -3)   ← diff 卡片   │
  │  ┌ terminal  go test ./...  ────────────────┐  ← 输出预览框   │
  │  │ ok  internal/cache  0.4s                  │                │
  │  └───────────────────────── +6 more lines ──┘                │
  │  好的，缓存层已加上，**关键点**：…            ← glamour 渲染   │
  ├─ live 区（View()，pinned 在底部）────────────────────────────┤
  │  ⟳ terminal: npm run build  (3.2s)            ← 运行中卡片     │
  │  ┌ 队列 (1) ─┐  ┌ 后台 (2 running) ─┐         ← 边框面板       │
  │  ┌────────────────────────────────────────┐                  │
  │  │ ▸ 继续…                                  │  ← 边框输入框     │
  │  └────────────────────────────────────────┘                  │
  │  claude-sonnet · ~/proj · ctx 42% · $0.03 · interactive · 12s │  ← 状态栏
  └──────────────────────────────────────────────────────────────┘
```

关键约束：`tea.Println` 输出落在 live 区**之上**；live 区每帧重绘。所以 `View()` 要克制高度（状态栏 + 输入框 + 必要时面板/运行中卡片），太高会吃掉可见 scrollback。

事件 → 渲染映射（`handleEvent`，`tuirepl.go:222`）：

| AgentEvent | 现状 | 升级后 |
|---|---|---|
| `EventTextDelta` | 逐行 `tea.Println` | 累积；block 边界经 glamour 渲染后提交（决策#5） |
| `EventToolStarted` | `↳ tool: input` 一行 | 进 live 区「运行中卡片」（spinner + verb + target） |
| `EventToolProgress` | `│ chunk` 一行 | 更新运行中卡片的尾部预览（live 区） |
| `EventToolDone/Error` | `↳ tool ✓/✗` 一行 | 定版：edit→`RenderEditCard`；其他→输出预览卡片；`tea.Println` 提交，清 live 区运行态 |

---

## 4. 富工具卡片（P1）

扩展 `internal/tui`，TUI 与 plain 共享：

- **状态头卡片**（新）：`● <verb> <target>  (<summary>)`，verb 来自工具名（Edit/Run/Search/Read…），运行中前缀换成 spinner 帧。已有 `renderHeader`/`renderSummary`（`diffcard.go:131/138`）可提取复用。
- **diff 卡片**：直接用 `RenderEditCard(path, old, new)`（`diffcard.go:67`）——TUI 的 `EventToolDone`（当 `ToolName=="edit_file"`）调它，与 plain 路径同一函数。
- **输出预览卡片**（新）：terminal/grep/read 的 `EventToolDone.Output`，边框包裹、按行 cap（如 10 行）+ `+N more lines` 折叠标记；超长复用 `EventToolOutputCap`（`event.go:60`）的思路。
- **运行中卡片**（live 区）：决策#4——`EventToolStarted` 起一个带 spinner 的活动卡片放 `View()`，`EventToolProgress` 滚动其尾部，`EventToolDone/Error` 时定版成上面的终态卡片 `tea.Println` 出去、清 live 态。

卡片渲染函数留在 `internal/tui`，但**只由 TUI（`tuirepl.handleEvent`）调用**。`plainView` 的 `replToolEventHandler` 去掉 `rendersAsCard`/`renderToolCard` 分支，edit_file 退回 `↳ tool ✓/✗` 一行（决策 #8/#9）——卡片不再出现在 headless/管道输出里。本期 TUI 接入 edit_file 卡片，**并为 terminal/grep/read 新建输出预览卡片**（两侧目前都没有）。

---

## 5. 状态栏 + 边框输入框（P2）

都在 `View()` 里（pinned）：

- **边框输入框**：`lipgloss` rounded border 包住输入行，光标用反色块；多行输入（`\` 续行，对齐 plain 的 `readPromptLine`）。
- **状态栏**（一行，右对齐次要信息）：
  - `model`：`a.Model`。
  - `cwd`：缩写 home（`~/…`）。
  - `context%`：本回合 input tokens / 模型上下文窗（compaction 已有窗口概念，`CompactThreshold` 逻辑里）。
  - `cost`：累计 USD（`a` 已 `accrueUsage`，`MaxCostUSD` 逻辑在用）。
  - 权限模式：`cfg.permEngine.GetMode()`。
  - 计时：本回合 elapsed（见 §6）。
- 键位提示并入状态栏第二行（或 hover 态），不再单占一行。

---

## 6. 富 spinner + 活动指示（P3）

- `tea.Tick`（~120ms）驱动 spinner 帧 + 回合 elapsed 计时。
- thinking 态：`⟳ thinking… (3.2s)` + 轮转提示词（"planning…/reading…/writing…" 按最近事件类型选词）。
- 工具运行中：复用同一 ticker 更新 §4 的运行中卡片。
- 可选：状态栏的 token/cost 在回合内按 `EventToolDone`/usage 跳动（流式 usage 有限，见 CLAUDE.md 的 OpenAI usage 限制——OpenAI 流式可能 0，需降级显示）。

---

## 7. glamour markdown（P4）

- 新增 `charmbracelet/glamour`，用一个固定深/浅主题的 `glamour.TermRenderer`（宽度跟 `m.width`）。
- 流式（决策#5）：`EventTextDelta` 累积到 `m.mdBuf`；检测到 block 边界（连续 `\n\n`、或 ``` 闭合）时，把完整 block 交 glamour 渲染、`tea.Println` 提交，保留未完成的尾巴在 partial 区（原始）。回合末 flush 剩余 block。
- 代码块：glamour 内部用 chroma 高亮，与 diff 卡片同源主题，观感统一。
- 风险：glamour 对半截 markdown 渲染不稳——故严格按 block 边界提交，绝不渲染半个 block。

---

## 8. 分期（决策#7：多 PR）

| 期 | 内容 | 价值 |
|---|---|---|
| **P1** | `internal/tui` theme 包雏形 + 工具富卡片接进 TUI（§4）：edit_file 卡片**从 plainView 下沉到 TUI**（plainView 退回纯文本）+ 新建 terminal/grep/read 输出预览卡片 | 卡片归位到 TUI（headless 输出变干净），并补齐两侧都缺的输出卡片，收益最大 |
| **P2** | 状态栏 + 边框输入框（§5） | 最像 CC 的视觉跃迁 |
| **P3** | 富 spinner + 活动指示（§6） | 等待期的反馈质感 |
| **P4** | glamour markdown（§7） | 助手文本可读性 |
| **P5** | 面板化（队列/后台/模态边框）+ 主题收口（§6 决策#6） | 一致性打磨 |

每期独立 PR、独立可发。唯一触及 headless 的是 P1 把 edit_file 卡片从 `plainView` 下沉到 TUI（plainView 该工具退回一行文本，决策 #8/#9）；P2–P5 全在 TUI 层，`plain`/headless 不受影响。

---

## 9. 测试

- `teatest`（charmbracelet 官方）驱动 Model：键位、modal 状态机、`View()` 快照（状态栏/输入框/面板）。
- 卡片渲染：复用 `cmd/tui-preview`（已有，dev-only 肉眼校验）+ 对 `internal/tui` 渲染函数加字符串断言（含/不含语法高亮、行号、cap 折叠）。
- markdown block 边界：断言半截 block 不提交、完整 block 经 glamour 提交、回合末 flush。
- 回归：现有 `tuirepl_test.go` / `repl_test.go` 全绿。P1 改 `plainView` 的 edit_file 渲染（卡片→一行），更新对应断言；其余 headless 行为不变。

---

## 10. 新增依赖

- `github.com/charmbracelet/glamour`（markdown 渲染；内部依赖 chroma，与现有同源）。
- 已有：`bubbletea` / `lipgloss` / `chroma`（diff 卡片 + glamour 共用）。

---

## 11. 不做 / 未来

- **alt-screen / 内部 scrollback viewport**（决策#1 保留 inline）；若将来要分屏/可滚动历史再单独评估。
- **Web / IM 端富渲染**：各端自有渲染模型，另案（`AgentEvent` 已带 JSON tag，便于 Web/SSE 复用同一事件流）。
- **鼠标交互 / 内联图形 / 图片**。
- **mswe-eval / headless** 始终纯文本。
