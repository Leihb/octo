# Skills — 加载器与默认集

用户在 `.octo/skills/` 下放 **Claude Code 格式**的 SKILL.md,octo 会话里发现它们、按需把
指令喂给模型,并支持 `/<name>` 显式触发。一组精选 skill 嵌在二进制里、安装即用。

100% 兼容 Claude Code 格式是硬约束:把 `~/.claude/skills/` 软链到 `~/.octo/skills/` 应能
直接复用,无需改任何 SKILL.md。

skill 是**纯指令**:正文是给模型的 markdown,模型读后用现有工具(`terminal` / `read_file`
/ `edit_file` …)执行。loader 不执行 skill 目录里的脚本——frontmatter 的 `allowed-tools`
等字段解析但不强制(模型仍可经 `terminal` 自行运行那些脚本,但那是模型的工具调用)。

## 1. SKILL.md 格式

```
<root>/skills/<name>/
  SKILL.md          # YAML frontmatter + markdown 正文
  (其它文件)         # 模板/脚本/参考——loader 不解释,正文可引用让模型 read_file
```

```markdown
---
name: my-skill
description: 一句话说明「何时该用这个 skill」——模型自主触发的唯一依据
---

# 指令正文

写给模型的步骤、约束、示例。可引用同目录文件(让模型 read_file),可要模型用 terminal 跑命令。
```

解析规则(兼容优先、宽容为主):

- **必需** `name`、`description`,缺任一则跳过该 skill,向 stderr 记一行告警、不中断。
- **目录名是权威 skill 名**(CC 行为);frontmatter 的 `name` 仅作展示。`/<目录名>` 触发。
- 其余 CC 字段(`allowed-tools`、`license`、嵌套 `metadata:` 块)一律解析不报错、不强制。
- frontmatter 用 `gopkg.in/yaml.v3` 解析到只含 `Name`/`Description` 的 struct,其余字段
  自动忽略——这是处理 CC 嵌套 `metadata:` 块的正确方式(手写「顶层 key:value」会解坏它)。

## 2. 三层发现与优先级

`internal/skills` 的 `Discover(cwd)` 扫三个 root,**后扫覆盖同名**:

```
default   ~/.octo/skills-default/   随二进制 ship、首次运行落地(见 §4)
   ↓ 被覆盖
user      ~/.octo/skills/           跨项目,用户级
   ↓ 被覆盖
project   <cwd>/.octo/skills/        项目级,最高优先
```

任一 root 缺失都不是错误。用户覆盖某个默认 skill,只要在 `~/.octo/skills/` 放同名目录。

```go
type Skill struct {
    Name        string // 目录名(触发名)
    Description string // frontmatter description(L1 清单 + 触发依据)
    Body        string // SKILL.md frontmatter 之后的正文
    Dir         string // skill 目录绝对路径(正文引用相对文件时的基准)
    Source      string // "default" | "user" | "project",用于 list 展示
}

type Registry struct{ skills map[string]Skill } // 按 name 索引

func Discover(cwd string) *Registry
func (r *Registry) Get(name string) (Skill, bool)
func (r *Registry) List() []Skill
func (r *Registry) Len() int
```

## 3. 渐进式披露——两个集成点

完整正文不进 system prompt(`Compose` 的 prefix 被 provider 缓存、session-start 冻结;
mid-session 重注入会失效缓存,全量注入又吃满上下文)。所以分两级,与真实 Claude Code 的
Skill 工具一致:

### L1:清单进 system prompt(session start,冻结)

只把**清单**(每个 skill 的 name + description)放进 system。渲染逻辑在
`internal/skills.RenderManifest(r)`(不放 prompt 包,保持 prompt 不依赖 skills)。caller
(chat.go)session start 调 `Discover` → `RenderManifest` → 传入 `Compose` 的 skills 层。
resume 路径同样重新发现 skills——清单随当前磁盘状态重算,不随会话固化。

清单层在 Compose 里的位置(skills 是能力说明,紧跟 base/env、在用户身份与规则之前):

```
base → soul → env → skills → memory → user.md → octorules(user) → octorules(project) → --system
```

清单文本形如:

```
# Available skills

When a task matches a skill's description, call the `skill` tool with its name
to load the full instructions before acting. The user can also trigger one
directly by typing /<name>.

- code-review: Review the current diff for correctness and cleanups.
- worktree-isolate: Do risky work in an isolated git worktree.
```

### L2:正文经 `skill` 工具加载(按需,进 history)

`internal/tools/skill.go` 的 `SkillTool`:`Definition` 名为 `skill`、参数 `{name}`;
`Execute` 返回该 skill 的 Body 作 **tool_result 进 history**——位于缓存 prefix 之后,不污染
冻结的 system,且天然纳入压缩 / 会话持久化。

registry 经包级 `tools.SetSkills(*skills.Registry)` 注入,`SkillTool{}` 零值从单例取数
(同 `TerminalTool` 的 `defaultBg`)。`DefaultTools()` **仅在发现 ≥1 skill 时** append
skill 工具——没有 skill 时模型不该看到空工具。

一次 skill 使用的时序:

```
session start
  skills.Discover(cwd) → Registry;tools.SetSkills(reg)
  manifest = RenderManifest(reg);a.System = Compose(..., manifest, ...)   // L1
  DefaultTools() 含 skill 工具(reg 非空)
turn
  模型据清单 description 匹配 → 调 skill{name:"code-review"}              // L2
  tool_result = SKILL.md 正文 → 进 history → 模型据正文用工具执行
```

## 4. 默认集(嵌入 + 首次落地)

随二进制 ship 一组精选 skill,安装即用,详见 `default-skills-design.md`。要点:

- 源在 `internal/skills/defaults/<name>/SKILL.md`,`//go:embed` 烤进二进制。
- 启动时 `MaterializeDefaults(version)` 把它们写到 `~/.octo/skills-default/`,版本戳 no-op,
  版本升级整目录重写。独立目录,刷新不碰用户 skill。
- 嵌入(非远程下载)→ 离线可用、版本锁定二进制。

## 5. 显式触发与 CLI

- **`/<name> [args]`**(REPL):命中 skill registry 则 `line = skill.Body`(args 非空追加为
  用户附加输入)、fall through 到 turn——直接把正文喂模型,省一次 skill 工具往返。与模型
  自主调 skill 工具并存,语义一致。未命中保持 `Unknown command`。
- **`octo skills list|update|path`**:`list` 按来源(default → user → project)列出;
  `update` 强制重新落地默认集;`path` 打印三个 root。
- **`octo chat --list-skills`**:发现并打印后退出,不需 provider/key。
- **REPL `/skills`**:列出本会话可用 skill。

`base.md` 有一小节教模型:看到 system 里的 "Available skills" 清单后,当且仅当任务匹配某条
description 时,先调 `skill` 工具加载完整指令再行动,不要凭一句描述揣测正文。

## 6. 测试(stdlib,无外部框架)

- `internal/skills`:`Discover` 三层覆盖(project > user > default)、缺失目录不报错、坏/缺
  frontmatter 跳过、目录名作 name;`RenderManifest` 稳定有序、空 registry → 空串;
  `MaterializeDefaults` 写嵌入 + 版本戳、同版本 no-op、版本变重写;默认 skill 被同名 user
  skill 覆盖。
- `internal/tools`:`SkillTool.Execute` 命中返回正文 / 未命中报错;`DefaultTools` 按 registry
  空非空决定 skill 工具有无。
- `cmd/octo`:`/<name>` 命中 inline / 未命中 Unknown command / `/skills` 输出;`--list-skills`
  与退出码;`Compose` 的 skills 层位置;`octo skills` 子命令。
