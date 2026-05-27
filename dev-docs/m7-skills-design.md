# M7 — Skill 加载器（design）

> 路线图源：`go-rewrite-roadmap.md` §M7、`competitive-parity-roadmap.md` C9（C9
> 已并入 M7 track，但**本轮不实现** typed memory，只做 Skills）。

## 1. 目标与范围

让用户在 `~/.octo/skills/` 和项目 `.octo/skills/` 下放置 **Claude Code 格式**的
SKILL.md，octo 在会话里发现它们、按需把指令喂给模型，并支持 `/<name>` 显式触发。

**100% 兼容 Claude Code skill 格式**是硬目标：用户应能把现有
`~/.claude/skills/` 软链到 `~/.octo/skills/` 直接复用，无需改动任何 SKILL.md。

本轮范围（已与用户对齐）：

- ✅ **指令型 skill**：skill = 纯 SKILL.md，正文是给模型的 markdown 指令。模型读到
  指令后用**现有工具**执行。
- ✅ **两级来源**：用户级 `~/.octo/skills/` + 项目级 `<cwd>/.octo/skills/`，项目优先。
- ✅ **渐进式披露**：清单进 system prompt，正文经 `skill` 工具按需加载。
- ✅ **显式触发** `/<name>`、`--list-skills`、REPL `/skills`。
- ❌ **bundled 可执行脚本**：skill 目录里的脚本不由 loader 执行（frontmatter 的
  `allowed-tools` 等字段解析但不强制）。模型仍可经 `terminal` 自行运行 skill 目录里
  的脚本——但那是模型的工具调用，不是 loader 的职责。
- ❌ **C9 typed cross-session memory**：单独一轮。
- ❌ **内置 default_skills**：留待 future（roadmap 的 `~/.octo/default_skills/`）。

## 2. Skill 格式（Claude Code 兼容）

```
<root>/skills/<name>/
  SKILL.md          # YAML frontmatter + markdown 正文
  (其它文件)         # 模板/脚本/参考——loader 不解释，正文可引用让模型 read_file
```

SKILL.md：

```markdown
---
name: my-skill
description: 一句话说明「何时该用这个 skill」——这是模型自主触发的唯一依据
---

# 指令正文

按需写给模型的步骤、约束、示例。可以引用同目录文件
（让模型用 read_file 打开），可以要求模型用 terminal 跑命令。
```

解析规则（兼容优先，宽容为主）：

- **必需**：`name`、`description`。缺任一 → 跳过该 skill，向 stderr 记一行告警，不中断。
- **宽容忽略**：`allowed-tools`、`license`、`metadata` 等 CC 字段一律解析不报错、不强制
  ——保证 `~/.claude/skills/` 的 skill 原样可用。
- **name 一致性**：以**目录名**为权威 skill 名（CC 行为）；frontmatter 的 `name`
  仅作展示。`/<目录名>` 触发。

frontmatter 用 `gopkg.in/yaml.v3`（已是 go.mod 直接依赖，零新增成本）解析为 struct：
切出首尾 `---` 之间的块，`yaml.Unmarshal` 到 `struct{ Name, Description string }`。
未映射字段（`allowed-tools`、嵌套的 `metadata:` 块等）被自动忽略——这正是处理
CC frontmatter 的正确方式：CC 的 `metadata:` 是**嵌套块**，手写"顶层 key:value"解析
会解坏它，违反 100% 兼容目标。

## 3. 发现与来源层级

新包 `internal/skills/`：

```go
type Skill struct {
    Name        string // 目录名（触发名）
    Description string // frontmatter description（L1 清单 + 触发依据）
    Body        string // SKILL.md frontmatter 之后的正文
    Dir         string // skill 目录绝对路径（正文引用相对文件时的基准）
    Source      string // "project" | "user"，仅用于 --list-skills 展示
}

type Registry struct{ skills map[string]Skill } // 按 name 索引

// Discover 扫描项目级 + 用户级目录，项目级同名覆盖用户级。
// 任一目录缺失都不是错误（多数环境不会两个都有）。
func Discover(cwd string) *Registry

func (r *Registry) Get(name string) (Skill, bool)
func (r *Registry) List() []Skill   // 排序稳定，project 在前
func (r *Registry) Len() int
```

来源顺序（后者覆盖前者，所以项目级最后扫）：

1. `~/.octo/skills/*/SKILL.md` — 跨项目，用户级
2. `<cwd>/.octo/skills/*/SKILL.md` — 项目级，优先

> roadmap 原列的 `~/.octo/default_skills/`（内置）本轮不做；将来作为最低优先级的
> 第 0 层加入即可，不影响这里的接口。

## 4. 渐进式披露——两个集成点

这是与 roadmap 早期草图（"读取 SKILL.md → 注入 system prompt"）的**关键偏离**，理由见 §8 D1。

### L1：清单进 system prompt（session start，冻结）

`internal/prompt/Compose` 的 prefix 被 provider 缓存、且**必须 session-start 冻结**
（见 `prompt.go` 包注释）。所以只把**清单**（每个 skill 的 name + description，几十字）
放进 system，正文不放。

- `Compose` 新增一个参数 `skills string`（已渲染好的清单文本），空串则跳过该层。
- 渲染逻辑放在 `internal/skills`（`RenderManifest(r *Registry) string`），**不**放
  prompt 包——保持 prompt 包不依赖 skills 包（单向依赖）。caller（chat.go）在 session
  start 调 `skills.Discover` → `RenderManifest` → 传入 `Compose`。
- 层位置：`base → env → **skills 清单** → user octorules → project .octorules → --system`。
  清单是"能力说明"，性质近工具说明，紧跟 base/env、在用户规则之前。

清单文本形如：

```
# Available skills

When a task matches a skill's description, call the `skill` tool with its name
to load the full instructions before acting. The user can also trigger one
directly by typing /<name>.

- code-review: Review the current diff for correctness and cleanups.
- deploy: Push a service branch to a Klook test env via Lark bot.
```

Resume 路径（chat.go:184 的 `prompt.Compose(sess.System, cwd, env)`）同样要带上当前
发现的 skills——skill 清单随当前磁盘状态重算，不随会话固化。

### L2：正文经 `skill` 工具加载（按需，进 history）

`internal/tools/skill.go`：

```go
type SkillTool struct{} // 零值；经包级 registry 取数（见下）

func (SkillTool) Definition() agent.ToolDefinition // name="skill", 参数 {name: string}
func (SkillTool) Execute(ctx, _, input) (string, error) // 返回该 skill 的 Body
```

- 工具返回 SKILL.md 正文，作为 **tool_result 进 history**——位于缓存 prefix 之后，
  不污染冻结的 system，且天然纳入压缩/会话持久化。
- registry 注入沿用 `sandbox.go` 的 `SetSandbox` 模式：包级
  `tools.SetSkills(*skills.Registry)`，`SkillTool{}` 零值从包级单例取数。与
  `TerminalTool` 的 `defaultBg` 一致。
- **仅在发现 ≥1 skill 时暴露**：`DefaultTools()` 末尾在 registry 非空时 append
  skill 工具定义——没有 skill 时模型不该看到一个空工具。`allTools` 静态列表保持不变。

### 一次 skill 使用的时序

```
session start
  skills.Discover(cwd) → Registry
  tools.SetSkills(reg)
  manifest = skills.RenderManifest(reg)
  a.System = prompt.Compose(--system, cwd, env, manifest)   // L1
  DefaultTools() 含 skill 工具（reg 非空）

turn
  模型看到清单，判断 description 匹配 → 调 skill{name:"code-review"}   // L2
  tool_result = SKILL.md 正文 → 进 history
  模型据正文用 read_file/terminal/edit_file 执行
```

## 5. 显式触发 `/<name>`

`repl.go` 现有 slash 派发是硬编码 switch（`/help`、`/cost`…），`/init` 是特例：改写
`line` 为 `initInstruction` 后 fall through 到 turn machinery。

`/<name>` 复用同一模式：在 switch 的 `default` 分支（"Unknown command"之前）先查 skill
registry：

- 命中 `/foo [args]` → `line = skill.Body`（args 非空则追加为 `\n\n用户附加输入：<args>`），
  fall through 到 turn——**直接把正文喂给模型，省掉一次 skill 工具往返**。
- 未命中 → 保持现有 `Unknown command %q` 行为。

两条路（模型自主调 skill 工具 / 用户 `/foo` inline）最终都把正文交给模型，语义一致。

## 6. CLI / REPL 表面

- **`octo chat --list-skills`**：发现并打印（name、source、description 一行一个）后退出，
  不需要 provider/key。与 `--list-sessions` 同形。
- **REPL `/skills`**：列出本会话可用 skill；`printReplHelp` 增一行。
- **`/help`**：保持，但补一句"`/<skill>` 运行某个 skill"。

## 7. base prompt 增补

`internal/prompt/base.md` 增一小节，告诉模型 skill 机制怎么用：看到 system 里的
"Available skills"清单后，**当且仅当**任务匹配某条 description 时，先调 `skill` 工具
加载完整指令再行动；不要凭清单里的一句话描述就揣测正文。无 skill 时该节不出现
（清单层为空），所以这段文字要在"若有 skills 清单"的语气下写，避免无 skill 时显得突兀。

## 8. 决策记录

- **D1（渐进式披露，非全量注入）**：roadmap 早期写"读取 SKILL.md → 注入 system
  prompt"。本设计改为 L1 清单进 system + L2 正文进 history（经 skill 工具）。理由：
  (a) `Compose` 的 prefix 被 provider 缓存且 session-start 冻结，mid-session 重注入会
  对每个后续 turn 失效缓存；(b) 全量注入所有 skill 正文会无谓吃满上下文。这也正是真实
  Claude Code 的做法（Skill 工具），所以"参考 CC"在此与 cache 约束一致，不冲突。
- **D2（仅指令型）**：不执行 bundled 脚本；`allowed-tools` 等字段解析但不强制。保证
  CC 格式兼容的同时，把权限模型/沙箱交互留在范围外。
- **D3（两级来源，项目优先）**：用户级 + 项目级，无内置 default_skills（future）。
- **D4（skill 工具按需暴露）**：发现 0 个 skill 时不注入清单层、不暴露 skill 工具。
- **D5（`/name` inline）**：显式触发直接 inline 正文，省一次工具往返；与模型自主调
  skill 工具并存。
- **D6（目录名为权威触发名）**：与 CC 一致；frontmatter `name` 仅展示。
- **D7（用 yaml.v3 解析 frontmatter）**：`gopkg.in/yaml.v3` 已是 go.mod 直接依赖，
  零新增成本；且能正确跳过 CC 的嵌套 `metadata:` 块——手写"顶层 key:value"解析会解坏
  它，违反 100% CC 兼容。Unmarshal 到只含 `Name`/`Description` 的 struct，其余字段忽略。

## 9. 测试（stdlib + httptest，无外部框架）

- `internal/skills`：
  - `Discover`：临时目录构造两级来源；验证项目覆盖用户、缺失目录不报错、坏/缺
    frontmatter 被跳过且不中断、目录名作 name。
  - `RenderManifest`：稳定有序输出；空 registry → 空串。
- `internal/tools`：
  - `SkillTool.Execute`：命中返回正文、未命中报错。
  - `DefaultTools`：`SetSkills` 空/非空时 skill 工具的有无。
- `cmd/octo`：
  - `repl` slash：`/foo` 命中 inline、未命中 Unknown command、`/skills` 输出。
  - `--list-skills` 输出与退出码。
  - `Compose` 第四参数（manifest）的层位置。

## 10. 任务拆分

垂直切，依赖序：

1. **`internal/skills` 包**：`Skill`/`Registry`/`Discover`/`Get`/`List`/`RenderManifest`
   + frontmatter 解析 + 测试。（无下游依赖，先落）
2. **`Compose` 扩参**：加 `skills string` 层 + 测试；更新 chat.go 两处调用点
   （新建会话 + resume）。
3. **`skill` 工具 + `SetSkills`**：`internal/tools/skill.go` + `DefaultTools` 按需 append
   + 测试。
4. **CLI/REPL 表面**：`--list-skills`、`/skills`、`/<name>` 派发、chat.go session-start
   接线（Discover → SetSkills → manifest）+ 测试。
5. **base.md 增补** + 文档（docs/ 用户向，可并入或留到文档轮）。

每步 `make vet && make test`（race）+ gofmt；跨 OS `GOOS=linux/windows go build ./...`。

## 11. 不做 / 未来

- C9 typed cross-session memory（独立轮）。
- 内置 default_skills 第 0 层。
- bundled 脚本执行 / `allowed-tools` 强制（需先有沙箱-权限的 skill 交互模型）。
- Web/IM 界面的 skill 列举端点（roadmap §M8 `GET /api/skills`）。
