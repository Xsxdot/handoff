# 纪律配置与按执行者注入，附 codex 三处修复 —— 设计

> 日期：2026-08-18
> Backlog：**B129**（由 B114 + B116 + B126-A 合并而成）、**B117**、**B118**
> 基线：`claude/b128-windows-claude-executor`（前沿线，含 B37 Windows、B105）
> 来源：08-17 B93 基准评测与 08-18 B105 派发的连续实测

## 1. 问题与目标

本批四件事，一个新机制 + 三条独立修复。它们被放在一起，是因为**都在 codex 这条路径上取证**，
一次真机验收能同时验完；其中新机制的价值对四个 executor 通用。

| 来源条目 | 缺陷 | 归宿 |
|---|---|---|
| B114 | 执行纪律块写死为「用你的 subagent 机制派活」，codex/grok 没有 subagent，读到就转而扮演协调者 | §2 |
| B116 | codex adapter 未传 `developerInstructions`，协议纪律只活在第一条用户消息里 | §2.6 |
| B126-A | plan 与纪律块由同一个人在两个时刻分别写，无交叉检查；配置化后纪律块对写 plan 的人彻底隐形 | §2.7 |
| B117 | 审批裁决晚于回合边界时被静默丢弃，不回传 executor 也不产协调者可见事件 | §3 |
| B118 | codex 沙箱排除 TMPDIR，`go test` 直接失败且零工单无从求助 | §4 |

### 1.1 B114 的实测代价

08-17 B93 基准评测，同机同模型同 plan（6 个 task），**只换纪律块**：

| 纪律块 | 结果 |
|---|---|
| subagent 版（A 版） | 9 次人工推动仅到 3/6，最后卡死 |
| 单上下文版（B 版） | **0 次推动，26 分钟 6/6，六闸门全绿** |

7 组探针隔离变量确认：加「不许中途结束」条款救不回来，**去掉 subagent 指令才是充分条件**。
今天的解法是人工把 CLAUDE.md §4 的对应版本粘进 plan 文件头部——选错一次的代价如上。

### 1.2 非目标

- **不做纪律内容的语义校验**。handoff 不理解纪律块写了什么，只负责按执行者送对文件。
- **不做 plan 与纪律块的冲突机器检测**。理由见 §2.7。
- **不改 `writing-plans` skill 本体**。它在插件缓存 `superpowers/<版本>/skills/` 下，升级即被覆盖。

## 2. 纪律配置与按执行者注入（B129）

### 2.1 总体形态：逐字镜像 env 注入

B19 的 env 注入已经解决过同构的问题——「让用户显式声明某个 executor 启动时该带什么」。
本机制照搬它的全部形状，不发明新概念：

| 维度 | env（B19，现存） | discipline（本设计） |
|---|---|---|
| 文件位置 | `<DataDir>/env/` | `<DataDir>/discipline/` |
| 配置键 | `Env map[string]string` (`config.go:116`) | `Discipline map[string]string` |
| 键 → 值 | executor 名 → 文件名 | executor 名 → 文件名 |
| 文件名校验 | 纯文件名，含路径分隔符即拒 | 同 |
| 解析器 | `internal/envfile.Resolver` | `internal/discipline.Resolver` |
| 启动校验 | `Preflight()`，缺失 WARN 不阻塞 | 同 |
| 流转 | `m.env.For(execName)` → `StartReq.Env` | `m.discipline.For(execName)` → `StartReq.Discipline` |

新包 `internal/discipline`，导出面与 `internal/envfile/resolver.go` 一一对应：

```go
func NewResolver(dir string, m map[string]string, log *slog.Logger) *Resolver
func (r *Resolver) For(executor string) (Block, error)   // 返回纪律块文本 + 来源标注
func (r *Resolver) Preflight()
```

`For` 返回 `Block{Text string; Source string}`——`Source` 是「内置:single-context」或
「配置:my-rules.md」这类人可读标注，§2.7 的回显要用它。

### 2.2 内置两版随二进制走

`internal/discipline/builtin/` 下两个文件，`go:embed` 进二进制：

| 文件 | 适用 | 要点 |
|---|---|---|
| `subagent.md` | 有 subagent 机制的执行器 | 逐 task 派 subagent、独立审查 subagent 双裁决、修复回路 5 轮 |
| `single-context.md` | 无 subagent 机制的执行器 | 在本会话内自己逐 task 实现、不派发不调 handoff CLI、自做双裁决 |

原文逐字取自 `~/.claude/CLAUDE.md` §4 的 A 版 / B 版。跟 B59「skill 随二进制分发」同一思路：
纪律块是 handoff 自己的协议知识，必须跟着版本走，不能是用户手上一份会静默变旧的副本。

### 2.3 默认映射：一张能力表

```go
// 无配置时按执行器有没有 subagent 机制选内置版本。加新 executor 时加一行。
var defaultTier = map[string]string{
    "opencode": tierSubagent,
    "claude":   tierSubagent,
    "codex":    tierSingleContext,
    "grok":     tierSingleContext,
}
```

未登记的 executor 名 → `tierSingleContext`。**保守方向是刻意的**：单上下文版对有 subagent 的
执行器只是没用上能力（B93 实测仍 6/6），而 subagent 版对没有 subagent 的执行器是灾难性的。

### 2.4 覆盖语义三档

| config `discipline` 里该 executor | 行为 |
|---|---|
| 有非空值 | 读 `<DataDir>/discipline/<值>` |
| 值为空串 `""` | **显式关闭**，不注入 |
| 未出现 | **用内置默认**（按 §2.3 映射） |

第三档是**与 env 刻意的偏离**（env 未配置 = 不注入）。理由：env 的内容是机器特有的
（代理地址、私有 registry），猜错不如不猜；纪律块的内容是 handoff 通用的，不给默认
等于 B114 的价值只兑现一半——用户不配就退回今天的人工粘贴。

### 2.5 注入位置：四家同构

`turn.RenderPrompt` 加一个参数（`protocol.go:69`），模板（`protocol.go:39`）在三条铁律之后、
实现计划之前插入一段：

```
（三条铁律原样）

{{if .Discipline}}--- 执行纪律（先读这段，再读计划）---
{{.Discipline}}

{{end}}--- 实现计划 ---
{{.PlanContent}}
```

四个 adapter 的调用点各加一个实参，别无改动。

铁律段落同时以常量形式从 `turn` 包导出（如 `turn.ProtocolRules`），供 §2.6 的
`developerInstructions` 复用——模板与常量必须是同一份文本，避免两处各自漂移。

**协议层不可配置**：三条铁律（提问纪律 / 收尾纪律 / 不切分支）留在模板里写死，
纪律块只能追加、不能覆盖。配没了 `turn.ParseTrailer` 直接崩、`turn.NoTrailerResult`
全盘误判——B74 那条「假完成」防线的上游就断在这里。

### 2.6 codex 的常驻加固（原 B116）

`developerInstructions` 是 codex 协议直收的持久指令通道（见
`specs/2026-08-09-handoff-codex-adapter-design.md` §5.1 与能力表），今天全仓 0 命中。

- `openThread`（`adapter.go:289`）的 `thread/start` params 加 `developerInstructions`
- `resume.go:150` 的 `thread/resume` params 同样加——恢复路径是最容易漏钉的地方（B18 的教训）
- 取值 = **三条铁律原文 + 纪律块**

**首条用户消息仍与另三家逐字同构**，即纪律块在 codex 上两处都有。冗余是刻意的：
B93 那类跨 executor 评测（同一份 plan 比不同模型）要求首条消息可比，
首条消息一旦分叉就少了一个控制变量。

**已知限制**：opencode / claude / grok 的 resume 不重发首条消息，纪律只在首回合有效；
只有 codex 因 `thread/resume` 重传而全程常驻。另两家的常驻通道（claude 的 `--settings`
/ `--append-system-prompt`、opencode 的会话 instructions）未经实证，留待探针，不在本批。

### 2.7 消除隐形（原 B126-A）

配置化把纪律块从 plan 文件里拿走，**写 plan 的人从此看不到它**——B105 那次冲突时
纪律块至少还躺在 plan 头部。因此必须在派发那一刻把它变回可见：

- `dispatch` 的 **stderr** 回显一行：`纪律块: single-context（内置）`
  （stdout 有「单行任务 JSON」契约，人读信息一律走 stderr）
- agentd 侧 Info 日志带 executor 名与 `Block.Source`
- `ad.Start` 成功后经现成的 `m.appendProgress(taskID, text)` 落一条 progress 事件
  （文案 `纪律块: <Block.Source>`），使 `handoff show` 可见。
  **不新增事件类型、不改 payload 结构**——`progressPayload` 只有一个 `Text` 字段且被
  五处共用，为这件事加字段是无谓的侵入

**明确不做：机器检测 plan 与纪律块的冲突。** 这是 handoff 自己的仓库，plan 正文里
`handoff` 一词遍地都是（B105 那份尤甚），naive grep 噪音必然爆炸；而「这行是验收步骤
还是背景描述」机器判不了。**只做回显，不做判定**——把纪律块从隐形变成可见，判断留给人。

B126 的另一半（写 plan 时把需驱动 agentd 的验收步骤显式归审核者）落在
`~/.claude/CLAUDE.md` §4「派发硬纪律」，属仓库外改动，见 §5。

## 3. 回合边界后到达的裁决必须应答（B117）

### 3.1 现场

`manager.go:1768`：审批链异步化后 `Decide` 最长 60s，裁决回来时重读任务状态，
已离开 `running`/`waiting_answer` 就只打一条 Warn 然后 return——不回传 executor、
不落任何协调者可见事件。

这段守卫（P1-1）本身是对的：窗口内 executor 可能死亡或被 `done` 归档，照旧建工单/唤醒/
回传会重现 U-1/U-3 修掉的矛盾形态。但它**误伤了「回合正常结束、executor 还活着」**这种情况。

实测两次同型（08-17，B93 评测任务，mac-02 codex，都发生在 `go test` 这类稍慢的命令上）：

```
14:51:39.746 权限判定：交审批者   perm=exec-c572cb11 tool=bash（黑名单未命中）
14:51:49.447 WARN 审批者裁决期间任务已离开 running/waiting_answer，仅留审计事件
             decision=approve state=waiting_review
             → codex 侧：exec_command failed … Rejected("approval request aborted")
             → turn_failed「回合被中断（非 handoff 发起）」
```

codex 侧那条 ServerRequest 悬了 8.5 分钟后自行放弃，**打断的是下一个回合**。

### 3.2 修法

在 return 之前把门关上，三条守卫原意全部保留：

1. **一律回 `reject`**，无论裁决是 approve 还是 escalate。
   伤害的本质是「回合被 abort 打死」而非「命令没跑成」——给一个干净拒绝，
   当前回合正常收尾，`continue` 重跑即可补上。approve 方向不放行，是因为任务此时
   已在 `waiting_review`（语义是「等协调者」），放行等于让 executor 在无人看管时继续改工作区。
2. **落一条协调者可见事件**：新增 `EventTypeApprovalDropped`（`internal/proto/proto.go`），
   payload 带 `ticket_id` / `decision`（原裁决）/ `state`（当时任务状态）。
   形态仿 `deny_guidance_dropped`——同一根因（回合结束即无下发通道）的 approve 方向。
   协调者 `show` 能看见「有一条裁决因回合已结束被拒发」，而不是只在 agentd.log 里留条 Warn。
3. **不建工单、不 Publish 唤醒、不消耗答案守卫**——P1-1 的原意一字不改。

executor 已死时这次 `RespondPermission` 会失败：按现有错误路径记日志吞掉，
不因此改变任务状态。

同文件的 `autoAllowPermission` 已经是「必须应答」的形态，codex adapter 的 `reqUserInput`
分支也明写「不应答等于让回合永久挂起」——同一条原则这里补齐。

## 4. codex 沙箱给任务专属 tmp（B118）

### 4.1 现场

`adapter.go:109` 的 `sandboxPolicy()` 把 `excludeSlashTmp` 与 `excludeTmpdirEnvVar`
都设 `true` 且 `writableRoots` 为空。Go 的 build cache 与临时目录都在 `/var/folders` 下，
`go test` 报 `operation not permitted`，**且不产生任何 `permission_request`**——
模型撞上时连问都问不了，只能自己在工作区造 `.gocache-*` / `.gotmp-*` 绕，
绕不对就反复失败并把原因误归给「平台限制 / 既有测试问题」。B93 评测里两个回合基本耗在这件事上。

### 4.2 修法：开门与走门配套

1. `writableRoots` 加 `<TaskDir>/tmp`（`TaskDir` = `<DataDir>/tasks/<id>`，见 `StartReq`）
2. 经**已有 env 通道**注入 `TMPDIR` / `GOTMPDIR` / `GOCACHE` 指向该目录
3. 两个 `exclude` **保持 `true`**，不碰 `/tmp`

**两者缺一不可**：`writableRoots` 只开门，env 才让 go 真走这扇门。只做 1，
go 仍用系统默认 `/var/folders`，照旧被拒；只做 2，目录不在可写域，照旧被拒。

### 4.3 为什么目录必须在工作区之外

08-17 B119 验收实测：把 `TMPDIR` 指进任务工作区（仓库内部），会让一族
「造一个非 git 目录、断言 git 操作报错」的用例**假性失败**——临时目录落在 git 仓库里，
git 命令正常成功，断言全挂。handoff 仓库上命中 6 条：
`TestMainWorktreeRootRejectsNonRepo` / `TestRegisterProjectClaimRejectsNonRepoDest` /
`TestRepoWorktreesFailsOnNonRepo` / `TestReclaimListDegradesPerRepo` /
`TestEnsureRepoUsableRejectsNonGitPath` / `TestProjectAPIRejectsNonRepoWithReadableReason`。
识别判据：失败形态高度一致的「实得 nil」，且路径带 `.gotmp/`。

`<DataDir>/tasks/<id>/tmp` 在 DataDir 下、工作区之外，天然避开。

### 4.4 安全论证（补 codex spec §2.1）

新增的可写域是**任务专属**（每任务一个目录）、**随任务目录一起回收**、
**不跨任务共享**。对比「把两个 exclude 翻成 false」的方案：那会让 `/tmp` 这个
跨任务共享目录对所有 codex 任务可写，两个并发任务能互相看见与覆盖对方的临时文件，
与 handoff 一路在收的任务隔离方向相反。顺带收益：`GOCACHE` 也随之任务隔离，
消除跨任务的构建缓存污染。

## 5. 仓库外改动（B126-B）

`~/.claude/CLAUDE.md` §4「派发硬纪律」新增一条：

> 派 plan 前自审：plan 里凡有需要驱动 handoff 自身的验收步骤（起 agentd、派子任务、
> 调 `handoff` CLI），必须标注「本 task 由审核者执行，不派发」，或不写进派发的 plan、
> 留在本地验收清单里。**注意别过度推广**：并非所有真机验收都要留本地，
> 只有需要驱动 handoff 自身的那些才与纪律块冲突。

来源：08-18 B105 派发实测。B105 plan 的 Task 8 要求执行者起 agentd、派子任务、读回收日志，
而拼在 plan 头部的 B 版纪律块明令「不要派发、不要调用 handoff CLI、不要起任何新的 executor 进程」。
codex 的处置是对的（如实记「未验证」而不是硬跑或编结论），代价是 plan 里最承重的那个判据
（`mark_only ≥ 1`）派出去等于没验。

## 6. 测试与验收

### 6.1 单测

| 范围 | 要点 |
|---|---|
| `internal/discipline` | 三档覆盖语义、文件名含分隔符被拒、缺文件的 `Preflight` 行为、默认映射含未登记 executor |
| `turn.RenderPrompt` | 带纪律块与不带（空串）两种渲染；三条铁律位置不变 |
| codex adapter | `thread/start` 与 `thread/resume` 的 params 都含 `developerInstructions`；`sandboxPolicy()` 含 `<TaskDir>/tmp` 且两个 exclude 仍为 true |
| `manager` B117 | 表驱动：裁决 approve/escalate × 任务状态 completed/failed/waiting_review，均须回 `reject` 且落可见事件；executor 已死路径不改任务状态 |
| 回显 | `Block.Source` 在内置与配置两种来源下的文案 |

### 6.2 真机（codex，mac-02，一次跑完）

1. 首条消息含单上下文版纪律块原文（判据：出现「在本会话内自己逐 task 实现」）
2. `developerInstructions` 在 `thread/start` 与 `thread/resume` 都到位
3. **沙箱内直接 `go test ./...`**，不带任何 `.gocache-*` / `.gotmp-*` / `.codex-tmp` 绕行配方
   —— 这是 B118 的核心判据，绕行配方出现即判不过
4. `dispatch` stderr 回显了实际注入的档位，`handoff show` 里能看到对应事件
5. B117：需构造慢裁决触发（审批链耗时 > 回合边界），单测兜底，真机机会性验证

### 6.3 回归

另三个 executor（opencode / claude / grok）各派一次小任务，确认首条消息注入了正确档位
且行为无变化。opencode / claude 应拿到 subagent 版，grok 应拿到单上下文版。

**协调者自己跑门时不要带绕行配方**：`handoff run` 不经 codex 沙箱，
直接 `go test ./... -count=1` 用默认 TMPDIR 即可。

## 7. 已知限制

1. 纪律块对 opencode / claude / grok 只在**首回合**有效（resume 不重发首条消息）。
   只有 codex 全程常驻。补齐另两家需要先做常驻通道探针。
2. 纪律块内容不做任何校验——用户配一份自相矛盾的纪律块，handoff 照送不误。
3. plan 与纪律块的冲突不做机器检测，只做回显（理由见 §2.7）。
