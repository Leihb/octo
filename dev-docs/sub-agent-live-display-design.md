# Sub-Agent 运行时实时显示 — 设计方案

## Status

Draft — pending implementation (M10+).

## 目标

让 octo-agent 的 sub-agent 运行时体验接近 Claude Code：

- **启动时立即显示**（不是完成后才出现）
- **实时显示内部 tool 调用链**
- **底部面板展示所有活跃 sub-agent**
- **可展开/折叠查看详情**

## 架构原则

沿用现有设计模式：

- `SubAgentManager` 已经管理 sub-agent 生命周期 → 扩展为同时推送运行时事件
- `AgentEvent` 已经定义了事件体系 → 复用，子 agent 的事件通过新通道透传
- `bubbletea Msg` 已经是 TUI 的通信方式 → 新增 `subAgentEventMsg`
- 底部面板已有 background processes 先例 → 新增 sub-agent 面板

## 核心改动

### 1. `internal/tools/subagent_manager.go` — 运行时事件通道

新增事件类型和回调：

```go
// SubAgentEvent 是 sub-agent 运行时的内部事件
type SubAgentEvent struct {
    AgentID     string        // agent_1
    Description string        // "Find Banner TUI code"
    Kind        SubAgentEventKind
    // 以下为可选字段，根据 Kind 填充
    ToolName   string        // 内部 tool 调用名
    ToolInput  map[string]any
    ToolOutput string
    Err        string
    TextDelta  string        // 子 agent 的 thinking 文本片段
    TokensIn   int           // 累计 token
    TokensOut  int
    Elapsed    time.Duration // 运行时长
}

type SubAgentEventKind string

const (
    SubAgentStarted   SubAgentEventKind = "started"    // 子 agent 启动
    SubAgentThinking  SubAgentEventKind = "thinking"   // 文本片段
    SubAgentToolStart SubAgentEventKind = "tool_start" // 内部开始调用 tool
    SubAgentToolDone  SubAgentEventKind = "tool_done"  // 内部 tool 完成
    SubAgentToolError SubAgentEventKind = "tool_error" // 内部 tool 出错
    SubAgentCompleted SubAgentEventKind = "completed"  // 子 agent 完成
    SubAgentExited    SubAgentEventKind = "exited"     // 子 agent 被 kill/出错退出
)
```

`SubAgentManager` 新增：

```go
type SubAgentManager struct {
    // ... 现有字段 ...
    onEvent func(SubAgentEvent)  // 运行时事件回调（可选）
}

func (m *SubAgentManager) SetOnEvent(fn func(SubAgentEvent))

// pushEvent 线程安全地推送事件到 onEvent 回调
func (m *SubAgentManager) pushEvent(ev SubAgentEvent)
```

### 2. `cmd/octo/sub_agent.go` — 事件透传

`agentSpawner.runChild` 中，子 agent 调用 `RunStream` 时传入一个 `EventHandler`，把子 agent 的事件翻译成 `SubAgentEvent` 通过 manager 的 `onEvent` 推送：

```go
func (s *agentSpawner) runChild(ctx context.Context, lc *liveChild, prompt string, agentID string) (reply string, in, out int, err error) {
    lc.mu.Lock()
    defer lc.mu.Unlock()

    // 推送启动事件
    s.reg.mgr.pushEvent(SubAgentEvent{AgentID: agentID, Kind: SubAgentStarted, Description: lc.description})

    childCtx := tools.WithSubAgentMarker(ctx)

    // 用 RunStream 替代 Run，透传事件
    r, err := lc.agent.RunStream(childCtx, prompt, lc.tools, lc.executor, func(ev agent.AgentEvent) {
        // 把 agent.AgentEvent 映射为 tools.SubAgentEvent
        se := mapAgentEventToSubAgentEvent(agentID, ev)
        s.reg.mgr.pushEvent(se)
    })

    // ... 后续不变
}
```

事件映射逻辑：

```go
func mapAgentEventToSubAgentEvent(agentID string, ev agent.AgentEvent) SubAgentEvent {
    se := SubAgentEvent{AgentID: agentID}
    switch ev.Kind {
    case agent.EventTextDelta:
        se.Kind = SubAgentThinking
        se.TextDelta = ev.Text
    case agent.EventToolStarted:
        se.Kind = SubAgentToolStart
        se.ToolName = ev.ToolName
        se.ToolInput = ev.Input
    case agent.EventToolDone:
        se.Kind = SubAgentToolDone
        se.ToolName = ev.ToolName
        se.ToolOutput = ev.Output
    case agent.EventToolError:
        se.Kind = SubAgentToolError
        se.ToolName = ev.ToolName
        se.ToolOutput = ev.Output
        se.Err = ev.Err
    case agent.EventTurnDone:
        if ev.Reply != nil {
            se.TokensIn = ev.Reply.InputTokens
            se.TokensOut = ev.Reply.OutputTokens
        }
        se.Kind = SubAgentCompleted
    }
    return se
}
```

### 3. `cmd/octo/tuirepl.go` — 新增消息类型和 Update 处理

新增消息类型：

```go
type subAgentEventMsg struct {
    ev tools.SubAgentEvent
}
```

`Update` 中新增 case：

```go
case subAgentEventMsg:
    return m.handleSubAgentEvent(msg.ev)
```

`handleSubAgentEvent` 更新 `m.subAgents` 状态映射：

```go
func (m *tuiModel) handleSubAgentEvent(ev tools.SubAgentEvent) (tea.Model, tea.Cmd) {
    sa := m.subAgents[ev.AgentID]
    if sa == nil {
        sa = &subAgentState{ID: ev.AgentID, Description: ev.Description, Start: time.Now()}
        m.subAgents[ev.AgentID] = sa
    }

    switch ev.Kind {
    case tools.SubAgentStarted:
        sa.Status = "running"
        sa.Start = time.Now()
    case tools.SubAgentThinking:
        sa.LatestText += ev.TextDelta
    case tools.SubAgentToolStart:
        sa.CurrentTool = &toolState{Name: ev.ToolName, Input: ev.ToolInput, Start: time.Now()}
        sa.ToolHistory = append(sa.ToolHistory, sa.CurrentTool)
    case tools.SubAgentToolDone:
        if sa.CurrentTool != nil {
            sa.CurrentTool.Done = true
            sa.CurrentTool.Output = ev.ToolOutput
        }
    case tools.SubAgentToolError:
        if sa.CurrentTool != nil {
            sa.CurrentTool.Done = true
            sa.CurrentTool.Output = ev.ToolOutput
            sa.CurrentTool.Err = ev.Err
        }
    case tools.SubAgentCompleted, tools.SubAgentExited:
        sa.Status = "done"
        sa.CurrentTool = nil
    }

    sa.TokensIn = ev.TokensIn
    sa.TokensOut = ev.TokensOut

    return m, nil
}
```

### 4. `cmd/octo/tuirepl_view.go` — 底部面板和实时显示

新增 sub-agent 面板（类比 background processes 面板）：

```go
// View() 中，在 background panel 之后新增：
if sa := runningSubAgents(m.subAgents); len(sa) > 0 {
    var lines strings.Builder
    for i, a := range sa {
        if i > 0 { lines.WriteByte('\n') }
        frame := spinnerFrames[m.spinnerFrame%len(spinnerFrames)]
        if a.Status == "running" {
            elapsed := time.Since(a.Start).Round(time.Second)
            tokStr := ""
            if a.TokensOut > 0 {
                tokStr = fmt.Sprintf(" · ↓ %s", formatTokens(a.TokensOut))
            }
            fmt.Fprintf(&lines, "%c %s (%s)%s", frame, a.Description, elapsed, tokStr)
            if a.CurrentTool != nil {
                fmt.Fprintf(&lines, "\n  └ %s: %s", a.CurrentTool.Name, summariseInput(a.CurrentTool.Input))
            }
        } else {
            fmt.Fprintf(&lines, "  %s ✓", a.Description)
        }
    }
    b.WriteString(tui.Panel(fmt.Sprintf("sub-agents (%d)", len(sa)), lines.String()))
    b.WriteByte('\n')
}
```

展开/折叠交互：

```go
// tuiModel 新增
expandedSubAgent string // 当前展开的 sub-agent ID，"" = 无

// handleKey 中新增
case tea.KeyCtrlO:
    // 循环展开下一个 sub-agent
    // ...
```

展开时显示完整 tool 调用历史（类似 Claude Code的 `+7 tool uses (ctrl+o to expand)`）。

### 5. `internal/tui/panel.go` — 复用现有 Panel 组件

已有 `tui.Panel(title, content string)` 用于 background/queue 面板，sub-agent 面板直接复用，保持视觉一致。

## 数据流图

```
┌─────────────────┐     RunStream     ┌──────────────────┐
│  child Agent    │ ──AgentEvent────→ │ agentSpawner     │
│  (sub-agent)    │                   │ .runChild        │
└─────────────────┘                   └────────┬─────────┘
                                               │
                                               │ map to SubAgentEvent
                                               ▼
                                        ┌──────────────┐
                                        │ SubAgentMgr  │
                                        │ .pushEvent   │
                                        └──────┬───────┘
                                               │
                                               ▼
                                        ┌──────────────┐
                                        │ onEvent hook │
                                        │ (registered  │
                                        │  in runTUI)  │
                                        └──────┬───────┘
                                               │
                                               ▼
                                        ┌──────────────┐
                                        │ p.Send(...)  │
                                        │ subAgentEventMsg
                                        └──────┬───────┘
                                               │
                                               ▼
                                        ┌──────────────┐
                                        │ tuiModel     │
                                        │ .Update()    │
                                        │ handleSubAgentEvent
                                        └──────┬───────┘
                                               │
                                               ▼
                                        ┌──────────────┐
                                        │ subAgents    │
                                        │ map[string]* │
                                        │ subAgentState│
                                        └──────────────┘
```

## 状态模型

```go
type subAgentState struct {
    ID          string
    Description string
    Status      string // "running" | "done" | "exited"
    Start       time.Time

    LatestText  string // 最近 thinking 文本（截断显示）

    CurrentTool *toolState
    ToolHistory []*toolState // 完整历史，用于展开

    TokensIn    int
    TokensOut   int
}

type toolState struct {
    Name   string
    Input  map[string]any
    Output string
    Done   bool
    Err    string
    Start  time.Time
}
```

## Plain REPL 路径

Plain REPL（非 TUI）保持最小改动：在 `repl.go` 的 `onExit` 回调旁边增加一个 `onEvent` 回调，但只打印极简行：

```go
cfg.subAgentMgr.SetOnEvent(func(ev tools.SubAgentEvent) {
    // plain path: 只打印启动/完成，不打印内部细节
    if ev.Kind == tools.SubAgentStarted {
        fmt.Fprintf(cfg.stdout, "↳ sub-agent %s: %s\n", ev.AgentID, ev.Description)
    }
})
```

## 改动文件清单

| 文件 | 改动 |
|---|---|
| `internal/tools/subagent_manager.go` | 新增 `SubAgentEvent` / `SubAgentEventKind`，`SetOnEvent`，`pushEvent` |
| `internal/tools/launch_agent.go` | 无改动（`LaunchAgentTool` 只关心启动） |
| `cmd/octo/sub_agent.go` | `runChild` 改用 `RunStream`，透传事件；新增 `mapAgentEventToSubAgentEvent` |
| `cmd/octo/tuirepl.go` | 新增 `subAgentEventMsg`，`handleSubAgentEvent`，`subAgents` map |
| `cmd/octo/tuirepl_view.go` | 新增 sub-agent 面板渲染，展开/折叠交互；`liveHeight` 计入面板高度 |
| `cmd/octo/repl.go` | Plain REPL 的 `onEvent` 回调（极简） |

## 非目标（保持简单）

- ❌ 不在 sub-agent 面板中支持 kill/send_message 操作（保持通过 tool 调用）
- ❌ 不持久化 sub-agent 事件（内存-only，会话结束即丢）
- ❌ 不在 plain REPL 中显示内部 tool 链（只有 TUI 才有）
- ❌ 不改动 `AgentEvent` 定义（复用现有事件体系）

## 相关文档

- `dev-docs/async-subagent-design.md` — 异步 sub-agent 基础设计
- `dev-docs/tui-ux-upgrade-design.md` — TUI inline 模式和面板系统
