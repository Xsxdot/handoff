# handoff 权限门判定精度 设计

> 合并 backlog B23（误升级：叙述性文本命中黑名单）与 B27（漏升级：写文件类工具不设门）。
> 两者是同一处的两面——**判据是「对一整串展示文本做正则」**——因此一并设计、一并落地。

日期：2026-08-09
状态：待实现

---

## 1. 背景

### 1.1 现状：判据只有一个字符串

三个 adapter 的权限请求最终都收敛到 `executor.AdapterEvent.Text` 一个字符串，形如
`"Bash: git commit -m ..."` / `"Write: /path"`。manager 拿到它之后：

```
handlePermission
  └─ shouldConsultApprover(Text)
       ├─ Text 含截断标记        → 升级人工
       ├─ Approver.Blacklisted(Text) 正则命中 → 升级人工
       └─ 未命中                 → consultApprover（廉价模型裁决）
```

而各 adapter 手里其实**有结构化数据**（claude 的 `tool_name` + `input`、grok 的
`toolCall.rawInput`、opencode 的 `metadata`），在拼成 `Text` 那一步被拍平丢掉了。
黑名单与廉价模型都只能对拍平后的字符串做判断，两个方向的偏差由此而来。

### 1.2 B23 实证：误升级

用 `internal/agentd/approver.go` 的 `builtinBlacklist` 原文逐条跑，9 条候选样本
**9 条全部误命中**（2026-08-09 实测，非推演）：

| 权限描述 | 命中的规则 |
|---|---|
| `Bash: git commit -m "fix: 清理逻辑不再误删，去掉 rm -rf 分支"` | `rm\s+-[a-z]*[rf][a-z]*[rf]` |
| `Bash: git commit -m "docs: 说明 production 部署流程"` | `\bproduction\b\|\bprod\b` |
| `Bash: go test ./internal/prod/...` | `\bproduction\b\|\bprod\b` |
| `Bash: grep -rn "sudo" internal/` | `\bsudo\b` |
| `Bash: cat docs/production-checklist.md` | `\bproduction\b\|\bprod\b` |
| `Bash: npm run build:prod` | `\bproduction\b\|\bprod\b` |
| `Write: /repo/docs/production.md` | `\bproduction\b\|\bprod\b` |
| `Bash: go run ./cmd --note "drop table 是危险操作"` | `drop\s+(table\|database)` |
| `Bash: echo "见 README：git reset --hard 会丢改动" >> notes.md` | `git\s+reset\s+--hard` |

误升级的方向是安全的（多叫醒审核者一次），但叫多了会训练出盲批习惯，反过来侵蚀
B6「黑名单对全文匹配」本该带来的收益。

### 1.3 B27 实证：漏升级，以及对 backlog 记载的更正

backlog B27 记「两个 adapter 形状同源，是两边共有的口子」。核对代码后**这条记载不准确**：

| adapter | 文件写入的静态规则 | 是否有路径维度 |
|---|---|---|
| claude | `allowRules = [Bash, Edit, Write, Read, Glob, Grep]` | 无 |
| grok | `allowRules = ["Edit", "Write"]` | 无 |
| opencode | `edit: "allow"`，另有 `external_directory: "ask"` | 名义上有，**实际是否拦得住绝对路径写入未验证** |

也就是说 opencode 有一条名义覆盖此场景的规则（有效性待验），另外两个完全没有。
后果不变：`Bash` 里的 `rm -rf` 会进审批链，`Write`/`Edit` 直接写
`~/.ssh/authorized_keys`、`~/.zshrc`、`~/Library/LaunchAgents/*.plist` 却不经过任何人，
**连事件都不会留**。同样是「把宿主机改坏」，一条要人批、一条静默通过。

### 1.4 载荷可得性：本设计最大的未知

| adapter | 权限载荷携带的字段 | 文件类工具的目标路径 |
|---|---|---|
| claude | `tool_name` + `input`（`permText` 已在解 `command` / `file_path`） | **已知可靠** |
| grok | `toolCall.title` + `toolCall.rawInput`（已知含 `command`） | **未知**——ACP 另有 `kind` / `locations` 字段，handoff 未解析，无实测样本 |
| opencode | `permission` + `metadata.command` + `patterns` | **未知**——`edit` 现为 allow，从未产生过 edit 的 `permission.asked`，无样本 |

grok 的 `_x.ai/ask_user_question` 应答形态在本项目已经猜错过两次（先裸 `{}`，再照抄
`session/request_permission` 的内嵌形态），两次都要靠真机才发现。因此本设计**不猜载荷形态**：
实现的第一步是真机探针取样（见 §6.1），取样结果决定各 adapter 的改动能否落地。

---

## 2. 目标与非目标

### 目标

1. 权限判据从「整串文本正则」改为「先结构化解析请求，再对字段判」。
2. B23：叙述性文本（引号内的字面量）不再触发黑名单硬升级。
3. B27：`Write` / `Edit` 的目标路径越出任务范围时必须经过人，且必留事件。
4. 判据只有一份实现，三个 adapter 共用，可纯单测。

### 非目标

- **不做「本任务内已批准过同一条」的审批记忆**（B23 后半段）。结构化判据落地后，重复升级里
  绝大多数本来就是误判，会自然消失；剩下的重复是真危险命令重复，而「批准一次后同样命令
  自动放行」是实打实的安全削弱。此项不纳入本 spec，B23 收口时不计入。
- **不覆盖 Bash 的重定向越界**。`printf > ~/.ssh/authorized_keys` 走 Bash，有重定向目标但
  没有工具级路径字段，解析它需要真正的 shell 语法分析。它仍由黑名单 + 廉价模型 + `ask`
  规则兜。本 spec 不提供「越界写已全封死」的保证。
- 不改工单/事件的既有契约（工单存全文、事件 payload 截断至 200 字），B6 的结论原样保留。
- 不改审批者的 fail-closed 语义、nonce 防伪、连续失败停用逻辑。

---

## 3. 架构：判据下沉为纯函数包

### 3.1 新包 `internal/permgate`

职责：把一次权限请求判成三个出口之一。纯计算，无 I/O，不写 store、不碰 adapter。

```go
// Action 是一次权限裁决的出口。
type Action int

const (
    AutoAllow Action = iota // 立即放行，不建工单、不发事件
    Consult                 // 交廉价模型审批者裁决（今天的默认路径）
    Escalate                // 直接升级人工审核者（今天黑名单命中的路径）
)

// Request 是结构化后的权限请求。
type Request struct {
    Tool      string   // 归一化工具名：bash | write | edit | webfetch | other
    Text      string   // 权限描述全文（与工单同源）；非 bash 路由的黑名单扫描对象
    Command   string   // Tool=bash 时的完整命令串
    Paths     []string // Tool=write|edit 时的目标路径（可为相对路径）
    Truncated bool     // 描述含 executor.TruncationMarker
}

// Scope 是本任务的合法作用范围。
type Scope struct {
    Workdir string // task.Workdir()：仓库或 worktree
    TaskDir string // <DataDir>/tasks/<id>：agentd 给该任务的 0700 私有目录
}

// Verdict 是裁决结果。
type Verdict struct {
    Action Action
    Reason string // 日志与审计用的可读理由
    Rule   string // 因黑名单而 Escalate 时，命中的规则原文
}

type Gate struct { /* 编译后的黑名单 + log */ }

func New(patterns []string, log *slog.Logger) (*Gate, error)
func (g *Gate) Judge(req Request, scope Scope) Verdict
```

`builtinBlacklist` 与正则编译从 `internal/agentd/approver.go` **迁入** permgate：黑名单从此
只有一个属主。`Approver` 退化为只做「调廉价模型 + 解析裁决」，`Approver.Blacklisted` 删除。

### 3.2 数据流

```
adapter 权限载荷
  └─ 提取结构 → AdapterEvent{Type:"permission", Text:<全文>, Perm:*PermRequest}
       └─ manager.handlePermission
            └─ gate.Judge(req, scope)
                 ├─ AutoAllow → RespondPermission(taskID, permID, "once")
                 │              不建工单、不发事件、Debug 日志、计数 +1
                 ├─ Consult   → consultApprover（审批者不可用/已停用时退化为 Escalate）
                 └─ Escalate  → escalatePermission（原契约一字不改）
```

### 3.3 `AdapterEvent` 的扩展

```go
type AdapterEvent struct {
    Type         string
    PermissionID string
    SessionID    string
    Text         string        // 不变：工单与展示的唯一真相源
    Perm         *PermRequest  // 新增：Type=permission 时可选；nil = adapter 提取不出结构
    Result       *Result
}
```

`Text` 一个字节都不动——工单存全文、事件 payload 截断这两条 B6 契约因此无需重验。

**`Perm == nil` 一律 Escalate。** 这是整个设计的 fail-closed 支点：提取不出结构等于看不懂
请求，看不懂就交给人。（今天的行为是「看不懂也让廉价模型猜」。）

`PermRequest` 定义在 `internal/executor` 包（`AdapterEvent` 的所在地），permgate 的
`Request` 由 manager 从它加上 `Truncated` 组装——避免 executor 包反向依赖 permgate。

### 3.4 按工具路由

`Judge` 先按 `Tool` 分流，避免「哪条判据管哪种请求」留有解释空间：

| `Tool` | 判据 | 可能的出口 |
|---|---|---|
| `bash` | §4 对 `Command` 做黑名单判定 | Consult / Escalate |
| `write`、`edit` | §5 对 `Paths` 做归属判定；**并且**仍对 `Text` 跑一次 §4 的黑名单（路径本身可能命中，如 `Write: /etc/sudoers`） | AutoAllow / Escalate |
| 其余（`webfetch`、`other`…） | §4 对 `Text` 做黑名单判定 | Consult / Escalate |

三条硬规则：

- **AutoAllow 只可能出自 `write` / `edit`**，其余工具最宽也只到 Consult——本 spec 不放宽
  任何现有工具的裁决。
- `write` / `edit` 的两项判定是**与**关系：路径在范围内**且**黑名单不命中，才 AutoAllow；
  任一不满足即 Escalate。
- 无论哪条路由，`Truncated == true` 一律直接 Escalate，不再往下判。

---

## 4. Bash 判据：引号字面量不参与匹配

### 4.1 规则

黑名单改为对**剥离引号字面量后**的文本匹配。剥离规则：把成对的单引号 / 双引号包裹的内容
替换为空串（保留引号本身，使 `git commit -m ""` 仍是合法形态），不做完整 shell 解析。

单靠剥离会开一个绕过口：`sh -c "rm -rf /"` 剥完就干净了。因此判据是四条：

| 原文匹配 | 剥离后匹配 | 附加条件 | 裁决 |
|---|---|---|---|
| 命中 | 仍命中 | — | **Escalate** |
| 命中 | 不命中 | 命令含执行包装器 | **Escalate** |
| 命中 | 不命中 | 无执行包装器 | **Consult** |
| 不命中 | — | — | **Consult** |

执行包装器清单（大小写不敏感，作为独立词匹配）：`sh -c`、`bash -c`、`zsh -c`、`eval`、
`xargs`、`env ... -c` 形态的 `-c`。

表中的出口取值适用于 `bash` 与「其余」两条路由。走 `write` / `edit` 路由时，本表的
**Consult 读作「黑名单这一项通过」**，最终出口由 §5 的路径归属决定（§3.4 的与关系）。

第三行是本节的核心取舍：**误判的修法不是「直接放行」，而是「从硬拦降级为让模型看一眼」**。
这样未被枚举到的引号绕过形态最坏也只落到廉价模型手上，不会变成静默通过。

### 4.2 删除内置规则 `\bproduction\b|\bprod\b`

这是本 spec 唯一的实质性**削弱**，理由需要写清楚：

剥离引号救不了它——`go test ./internal/prod/...`、`Write: /repo/docs/production.md`、
`npm run build:prod`、`cat docs/production-checklist.md` 全都仍然命中（§1.2 实测 5 条误判
里有 4 条出自这一条规则）。

它想拦的是「操作生产环境」，实现成的却是「文本里出现 prod 字样」。而 handoff 的 agent 跑在
worktree 里，接触生产的途径是 `kubectl -n prod` / `ssh prod-01` / `terraform apply` 这类
**命令形态**，不是关键词。正则分不出 `go test ./internal/prod/...` 与
`kubectl -n prod delete deploy/api`，廉价模型分得出。

处置：从 `builtinBlacklist` 移除，同时在 `approverPromptTemplate` 增补一句——
「涉及生产环境、部署、运维目标机的操作必须升级」。模糊语义判断归模型，确定性模式归正则。

用户仍可通过 `config.ApproverConfig.Blacklist` 自定义补回，这条通路不变。

其余内置规则（rm 四条、git push --force、sudo、git reset --hard、drop table/database）
全部保留，形态不变。

---

## 5. Write/Edit 判据：路径归属

### 5.1 归一化与判定

```
目标路径
  → 相对路径按 Scope.Workdir 解析
  → filepath.Abs
  → 对「已存在的最长前缀」求 filepath.EvalSymlinks，再接回剩余部分
  → filepath.Rel(base, p) 对 Workdir 与 TaskDir 各判一次：
       err == nil 且结果不以 ".." 开头且不等于 ".." → 在该 base 内
```

两个细节不是可选项：

- **用 `filepath.Rel` 而非字符串前缀**——`strings.HasPrefix("/repo-evil/x", "/repo")` 为真，
  前缀匹配会把仓库外的路径判成内部。
- **`EvalSymlinks` 对已存在前缀求值**——否则 `ln -s ~ /repo/link` 之后写
  `/repo/link/.ssh/authorized_keys` 直接绕过。目标文件本身常常尚不存在（Write 新建），
  所以只能对存在的最长前缀求值。

多路径时**任一越界即整条越界**。

### 5.2 裁决

- **全部路径在 Workdir 或 TaskDir 子树内 → AutoAllow。** 不建工单、不发事件、Debug 日志，
  manager 侧累计计数器，回合收尾 Info 一条汇总（`自动放行工作区内写入 task=... n=37`）。
  不能完全静默：出问题时要有第一现场。
- **任一路径越界 → Escalate，直接叫人，不经廉价模型。** B27 的判断是触发面窄但后果最坏，
  这种形态不该由模型代人拍板。日志级别 **Warn**，带完整路径与 Scope 两个基准目录。
- **路径提取不出（`Paths` 为空但 `Tool` 是 write/edit）→ Escalate。** 同 `Perm == nil` 的
  fail-closed 逻辑。

### 5.3 AutoAllow 与审批者启用状态解耦

**审批者未配置（`m.approver == nil`）或已被停用时，AutoAllow 仍然生效。**

这条必须显式写死：AutoAllow 是第 0 层静态判据，不是审批者的职权。今天 `approver == nil`
时所有权限请求都升级人工，而 `Write`/`Edit` 在规则表里是 allow、根本到不了权限门；本 spec
把它们改成 ask 之后，若 AutoAllow 依赖审批者存在，未配置审批者的部署会立刻被工作区内的
每一次写入淹没。

### 5.4 残余风险

TOCTOU：判定通过后、executor 实际写入前，软链可能被换掉。接受此风险——闭合它需要在
executor 侧持有文件句柄，超出 handoff 的可控范围（写入动作在 agent 进程里）。

---

## 6. adapter 改动

### 6.1 前置：真机载荷探针（必须先做）

在改任何规则表之前，对**每个 adapter** 各跑一次真机探针，捕获文件写入触发权限门时的
**原始载荷全文**，落进各自的 `testdata/`：

| adapter | 探针做法 | 要确认的事 |
|---|---|---|
| claude | 临时把 `Write` 移入 `askRules`，派发一个「写一个文件」的任务 | `input` 里的字段名确为 `file_path` |
| grok | 临时把 `Write`/`Edit` 移入 `askRules` | `toolCall.rawInput` 的字段名；是否另有 `kind` / `locations` |
| opencode | 临时把 `edit` 置为 `ask` | `permission.asked` 的 `metadata` / `patterns` 里有没有路径 |

同时验证 opencode 的 `external_directory: "ask"` 对绝对路径写入是否真的生效（§1.3 的待验项）。

**条件性回退（不是占位符，是明确规则）**：若某 adapter 的探针显示无法从载荷可靠提取目标
路径，则**该 adapter 的 `Write`/`Edit` 保持 `allow` 不变**，并向 backlog 追加一条独立条目
记录该 adapter 的载荷缺口；本 spec 的其余部分（permgate、Bash 判据、其他 adapter 的改动）
照常落地。理由：把提取不出路径的 adapter 改成 ask，等于让它的每一次编辑都 Escalate 到人，
那正是 opencode 一期 dogfooding 修掉的连环唤醒形态——用一个更严重的问题换一个较轻的问题。

### 6.2 规则表改动（探针通过后）

| adapter | 文件 | 改动 |
|---|---|---|
| claude | `internal/executor/claudecode/taskenv.go` | `allowRules` 移除 `Edit`/`Write`；`askRules` 增加 `"Write"`、`"Edit"` |
| grok | `internal/executor/grok/taskenv.go` | `allowRules` 移除 `Edit`/`Write`；`askRules` 增加 `"Edit(*)"`、`"Write(*)"` |
| opencode | `internal/executor/opencode/taskenv.go` | `Edit: "allow"` → `"ask"`；`external_directory: "ask"` 保留 |

三处的 `taskenv_test` 都有「逐条断言规则表」的既有约定（各文件注释写明「少一条就是静默
放行」），改表必须同步改断言。

### 6.3 结构提取

每个 adapter 在产出 permission 事件时同时填 `Perm`：

- **claude**：`permText` 已经在 switch `ToolName` 解 `command` / `file_path`，重构为同时返回
  `Text` 与 `*PermRequest`，两者共用一次解析。
- **grok**：`OnPermission` 现有 `rawCommand(rawInput)`，按探针结果补文件类字段的提取。
  工具名从 `toolCall` 里取（探针确认字段），取不到则 `Tool="other"`。
- **opencode**：`mapPermissionAsked` 已解 `permission` / `metadata.command` / `patterns`，
  按探针结果补路径提取。`permission` 字段（`bash` / `edit` / …）直接作归一化工具名的来源。

归一化工具名的映射表放在 `internal/executor`（与 `PermRequest` 同处），三个 adapter 共用，
避免各写各的字符串。

---

## 7. fail-closed 汇总

一处集中列出，实现与测试都以此为准：

| 情形 | 裁决 |
|---|---|
| `Perm == nil`（adapter 提取不出结构） | Escalate |
| `Tool` 是 write/edit 但 `Paths` 为空 | Escalate |
| 路径归一化失败（`Abs`/`EvalSymlinks` 报错） | Escalate |
| `Truncated == true`（描述含截断标记） | Escalate（同今天） |
| 黑名单命中且剥离后仍命中 | Escalate |
| 黑名单命中、剥离后不命中、含执行包装器 | Escalate |
| 任一路径越界 | Escalate |
| `Judge` 返回 Consult 但审批者不可用/已停用 | Escalate（同今天） |

没有任何一条新增路径的失败会导向 AutoAllow。

---

## 8. 可观测性

按 `instrumenting-code` 的要求，关键节点逐条落实：

| 节点 | 级别 | 字段 |
|---|---|---|
| `Judge` 判出 AutoAllow | Debug | task、tool、paths、命中的 base（workdir/taskdir） |
| 回合收尾的 AutoAllow 汇总 | Info | task、n |
| `Judge` 判出 Consult | Info | task、tool、reason（含「原文命中但剥离后不命中」这一子因） |
| `Judge` 判出 Escalate（黑名单） | Info | task、rule、permission 截断至 80 字 |
| `Judge` 判出 Escalate（**路径越界**） | **Warn** | task、path 全文、workdir、taskdir |
| `Judge` 判出 Escalate（结构缺失/归一化失败） | **Warn** | task、tool、cause |
| adapter 提取结构失败 | Warn | task、perm id、原始载荷截断 |

越界写与结构缺失用 Warn 而非 Info：这两类是「本该被静默通过、现在被拦下」的事件，
是这次改动的全部价值所在，必须在日志里一眼可见。

---

## 9. 测试策略

### 9.1 permgate 单测（纯函数，表驱动，不起进程）

- **B23 回归**：§1.2 的 9 条实证误判样本逐条断言 **不是 Escalate**。这 9 条是本 spec 的
  验收基线，一条都不能少。
- **真危险仍拦**：`rm -rf /tmp/x`、`sudo systemctl ...`、`git push --force`、
  `git reset --hard`、`drop table x` 逐条断言 Escalate。
- **绕过防线**：`sh -c "rm -rf /"`、`bash -c 'sudo x'`、`eval "$DANGER"`、
  `xargs rm -rf` 逐条断言 Escalate。
- **路径归属**：工作区内相对路径 / 绝对路径 → AutoAllow；任务目录内 → AutoAllow；
  `/repo-evil/x`（前缀陷阱）→ Escalate；软链逃逸（测试内真建软链）→ Escalate；
  `~/.ssh/authorized_keys` → Escalate；多路径中任一越界 → Escalate。
- **fail-closed 表**：§7 每一行一条用例。

### 9.2 adapter 层单测

用 §6.1 探针取回的**真实载荷样本**作 testdata，断言 `Perm` 提取正确（工具名、命令、路径）。
不用手写的假样本——这个项目已经吃过两次「照着想象写载荷」的亏。

### 9.3 manager 层单测

- AutoAllow 路径：不建工单、不发事件、直接调用 `RespondPermission(..., "once")`。
- `Perm == nil` → 走 `escalatePermission` 原路径。
- `approver == nil` 时 AutoAllow 仍生效（§5.3 的显式断言）。
- Consult → 审批者不可用时退化为 Escalate。

### 9.4 真机 e2e

每个改动生效的 adapter 各跑一次，两项：

1. 任务写工作区内的文件 → 无工单、无 permission 事件、日志有 AutoAllow 汇总行。
2. 任务写工作区外的绝对路径 → 产生工单、日志有 Warn 级越界行、`reply --reject` 后
   模型收到拒绝。

---

## 10. 影响面与风险

| 风险 | 缓解 |
|---|---|
| 判据有 bug 会同时影响三个 adapter | 判据是纯函数、无 I/O，可穷举测试；且失败方向全部指向 Escalate（多叫醒一次），不指向放行 |
| `Write`/`Edit` 改 ask 后每次写入多一次本地往返 | unix socket / 本地 HTTP / WS 的往返，微秒级；AutoAllow 不写库不发事件，无 I/O |
| AutoAllow 判定失误会淹没审核者 | 失误方向是 Escalate（噪音）而非放行（危险）；§9.4 的 e2e 第 1 项专门验「工作区内不叫人」 |
| 删除 `prod` 规则是安全削弱 | §4.2 论证；同时增补审批者 prompt 指令，且用户可经 config 自定义补回 |
| 探针发现某 adapter 拿不到路径 | §6.1 的条件性回退：该 adapter 保持 allow 并记 backlog，不阻塞其余部分 |

## 11. 对 backlog 的影响

- B23：本 spec 覆盖「误判」部分；「审批记忆」明确不做（§2 非目标），收口时按此口径。
- B27：本 spec 覆盖 `Write`/`Edit` 工具级路径判据；Bash 重定向越界明确不在范围（§2 非目标）。
- 可能新增：§6.1 探针若发现某 adapter 载荷缺路径，追加一条该 adapter 的载荷缺口条目。
- §1.3 对 B27「两边共有的口子」这一记载的更正，需回写 backlog 的「变更痕迹」列。
