---
name: handoff
description: 用 handoff CLI 以协调者身份把实现计划派发给独立 executor（opencode / claude / grok / codex / agy）执行并盯完全程。只要涉及「把这个 plan 交给远程开发机跑」「派发任务给 executor 执行」「盯 handoff 任务进度」「想写个轮询/sleep 循环等 handoff 任务」「任务卡在 running / waiting_review」「reply 返回 502 / continue 报 409 / done 报 404」「wait 返回了旧事件」「新会话接管一个已经在跑的 handoff 任务」，哪怕用户一个字没提「handoff」，也必须先读这份 skill——handoff 的状态机对操作顺序有硬约束，凭印象敲命令会撞 404/409，并把任务卡成没人收的孤儿。
---

<!--
职责：
  - 教会「协调者」角色如何用 handoff CLI 驱动一个派发任务的完整生命周期。
    不叫审核者：用户把审核者理解成 code review，协调者才是派发与盯任务的那一端。
  - 固化那些一旦搞错就会卡住任务的硬约束：ID 形态、状态机前置条件、事件分诊、失败出口。

边界：
  - 不讲 agentd 的部署与配置（config.yaml 各段、approver 审批链、env 注入）——见仓库 README。
  - 不讲各 executor 的内部差异与协议实现——见 README「各 executor 须知」与 docs/superpowers/specs/。
  - 不替协调者做审批判断：批不批、改不改由协调者（必要时升级给人）决定。
-->

# handoff：以协调者身份驱动派发任务

## 心智模型

handoff 把「写计划的人」和「干活的人」拆成两个进程：

- **agentd** 跑在 executor 所在机器上，持有**全部**状态——任务、事件、工单、executor 生命周期，落 SQLite。
- **你（协调者）**只是一个客户端。你不持有任何状态，随时可以崩溃、断网、换一台机器接管。
- 你和 agentd 之间只有一条通道：`handoff` CLI。

这条架构直接决定了三件事，后面所有纪律都是它的推论：

1. **你的会话不是权威**。「我记得这个任务已经批过了」不作数，`handoff show` 说了算。
2. **断网不丢事件**。事件在 agentd 侧持久化并带 cursor，`wait` 重连后从断点续拉。所以你没必要一直挂着。
3. **绕过 CLI 就会失配**。ssh 到执行机去杀 executor 进程、手删任务目录、直接进工作区改代码——这些 agentd 全都不知道，它记的运行态和真实存活性会当场对不上，任务卡成孤儿。

### 铁律：一切经 CLI

需要看 executor 在干什么，用 `handoff attach`（经 agentd 的 render 流，远程也不需要 ssh）。需要看代码，用 `handoff diff` / `fetch` / `run`。需要回收，用 `handoff done` / `stop`；归档后残留的 managed worktree 用 `handoff reclaim` 清；终态任务的 tmp/gocache 叶子和残留 managed 树用 `handoff gc`（默认预览，`--yes` 才删）。

**唯一例外**：任务已经彻底死了、CLI 三条路（`resume` / `continue` / `done`）全被拒，此时按任务目录 `proc.json` 里的 `handle.pid` 手工 kill shim 进程是兜底。但那是排障，不是日常。

## 任务 ID 必须是完整 UUID

所有接受 `<task>` 的子命令都是**精确匹配**，没有前缀补全。传 8 位短 id 一律 404「任务不存在」。

短 id（`id8`）只出现在 `--notify` 的通知文案里，不能当命令参数用。

拿完整 id 的办法：`dispatch` 的输出 JSON 里的 `.id`，或者 `handoff tasks | jq -r 'select(.name=="...") | .id'`。

## 状态机：先看状态，再敲命令

六个状态。**`continue` 和 `done` 都硬要求 `waiting_review`**，状态不符返回 409 `ErrBadTransit`——这是最常撞的一堵墙。

| 状态 | 含义 | 此时能做 | 此时会被拒 |
|------|------|----------|-----------|
| `pending` | 已建任务，executor 还没起来 | `show` / `stop` | `continue` / `done` |
| `running` | executor 正在干活 | `wait` / `attach` / `show` / `diff` / `stop` | `continue` / `done` |
| `waiting_answer` | 有工单挂起，等你裁决 | `reply --ticket ...` / `attach` / `stop` | `continue` / `done` |
| `waiting_review` | 一轮干完了，等你审 | `diff` / `fetch` / `run` / **`continue`** / **`done`** / `stop` | — |
| `completed` | 已归档 | `show` / `diff`（只读） | 一切写操作，含 `stop` |
| `failed` | 已失败 | `show` / `diff` / `pull`（只读取证） | `continue` / `done` / `stop` |

> **回合失败发的是 `turn_failed` 事件，不是 `failed`**（B100 拆开的两个类型）：`turn_failed` 时任务进 `waiting_review`——executor 会话与上下文都在，`continue` 就能续接重试。`failed` **事件**如今只在真终态出现：`stop`、`resume --force`、dispatch 期启动失败、看门狗补正——此时任务落 `failed` **状态**，想继续只能重新 `dispatch`。
>
> `diff` / `fetch` / `run` 无状态门禁：`running` 中也能看实时进度。但 `completed`（已归档）的 `--new-worktree` 任务其 worktree 已被回收：`run` 会给出真因并返回 400（「managed worktree 可能已被 done/stop 回收」），`diff` 则是普通 git 失败（500），没有专门归因。

**动手前先确认状态**：`handoff show <task>` 输出一行 JSON，含任务体 + `pending_tickets` + 最近事件。不确定就先 show，比吃一个 409 便宜。

## 主循环

> 本项目启用了账本（`ledger.enabled: true`）时，**外层**循环见下面的
> 「账本模式」一节；本节仍然完整适用于内层——醒来之后怎么处置一个具体
> task 的事件，两种模式一模一样。

```bash
# 1. 派发（仓库工作区必须干净，否则被拒——脏改动会被污染进任务分支）
handoff dispatch --new-worktree --executor opencode plan.md
# stdout 第一行是任务 JSON，取 .id 作为后续所有命令的 <task>

# 2. 挂等待（阻塞，一次只返回一个事件）
handoff wait <task> --notify --timeout 1h

# 3. 按事件类型分诊、处置（见下表）

# 4. 回到第 2 步继续 wait，直到进入审核
```

**`wait` 的三条契约**，记牢了省很多事：

- **默认一次只吐一个事件**。stdout 单行 JSON，然后退出；`--follow` 持续订阅，事件逐条流入，退出即任务终结或超时。处理完**不用重挂**——follow 订阅活到会话结束。
- **退出码有语义**。`0` = 事件到达；`124` = `--timeout` 到点（可以接着挂）；`1` = 真失败。`--timeout` 在三种模式下三种含义：一次性 wait 是**等不到事件的总时长**，`--follow` 是**空闲上限**（见下节），`--until-done` 是**总时限**（中间帧不续命）。
- **失败会立刻退出，不会闷等**。token 没同步（401）、task-id 不存在（1008）都是立即报错。`wait` 长时间不返回**只**意味着「还没有事件」，那是正常态——stderr 里的「WS 连接断开，等待后重连」也是正常态。

无人值守时务必带 `--timeout`：它是配置错误的最后一道防线，退出码 124 可以和真失败区分开。

`progress` / `approver_decision` / `approver_disabled` / `tickets_voided` 四类事件**不会**唤醒 `wait`（只入库）。你只会在 `show` 的事件历史里见到它们，日常不用管。

## 执行器选型与模型

- **codex**：缺省执行者，稳重不跑偏——整份 plan 的执行、大规模机械改动
  （codemod、跨文件重命名、依赖升级修编译）。
- **grok**：快、挖 bug 能挖到根——根因排查（复现、加日志、二分定位）、真机烟测、
  tool 调用多的纯执行。根因后分流：一两行小修顺手修掉，架构级修复回本地走流程。
- **claude**：重构、细粒度设计、复杂推理或多文件协同排查。
- **agy**（Antigravity CLI）：适用于基于 Google Antigravity / Gemini 的工程任务与复杂子代理编排，权限门通过 PreToolUse 钩子挂载。
- **opencode**：备选，codex 不可用时顶上。
- **一律不传 `--model`**，除非要点名某个具体模型：缺省执行者用机器默认模型；
  改派非缺省执行器时留空、由其自身默认接管。模型名**按机器不同**，跨机/跨执行器
  复用模型名，第一个事件就是 400。

## 在 agent 会话里挂 wait

上面的主循环假设操作者能前台阻塞一小时。agent 的 Bash 工具做不到（前台超时上限通常只有几分钟到十分钟），于是最常见的走样就是自己发明 `show` + `sleep` 轮询循环，或把几百轮 wait 包进一条 shell 大循环。**两种都不要。** 正确形态按你所在 harness 的能力二选一：

- **有后台任务/监控机制的 harness（Claude Code 的 Monitor、grok 的 background task）**：挂一条后台 `wait --follow` 长订阅，见下节。
- **没有后台唤醒机制的 harness（opencode、codex）**：挂不了 `--follow` 订阅，退回**前台一次性 wait 逐轮挂**：`handoff wait <完整 task-id> --timeout <小于前台超时上限，如 5m>`，阻塞到返回一个事件就处置，处置完再挂下一条；退出码 124 表示这轮没等到，直接再挂即可。每轮一条独立命令，事件 JSON 完整落在命令输出里——这不是被禁止的轮询循环，禁的是拿不到事件的 `show`+`sleep` 和吞掉输出的 shell 大循环。

### 订阅：开一次，活到会话结束（Claude Code / grok）

    Monitor({
      command: "handoff wait --follow <完整 task-id> --timeout 3h",
      description: "handoff <任务名> 事件流",
      persistent: true
    })

事件作为通知逐条流入本会话，**没有「重挂」这个动作**。

**命令后面不要接任何过滤器**（`grep` / `sed` / `awk`）。INFO 噪声在 **stderr**，
stdout 上本来就只有 JSON 事件——要静音用 `2>/dev/null`，一个过滤器都不需要。
接了会**静默掐断唤醒**：2026-08-25 实测，裸跑 `card wait` 从账本落账到事件出流
是 274ms；同一条流接上 `2>&1 | grep -vE "INFO"` 后一条不出，直到进程超时退出才
一次性吐出来（3/3 复现，加 `--line-buffered` 也救不回来）。C1.5 那轮工单因此在
无人应答里躺了 23 分钟，而会话把它误判成「镜像没过来」，多挂了一层 task 级订阅。
判据：卡上/任务上明明有新事件，Monitor 却一声不吭——先看自己的命令里有没有管道。

- `--timeout` 是**空闲**上限（距上一次收到任何帧，含不唤醒的 progress），
  必须**大于**对端 agentd 的 stalltimeout（默认 2h），故取 3h。设小了，
  客户端的超时会抢在 agentd 的 stalled 诊断前面退出——把一条带 last_seq 的
  诊断换成一句「我没收到东西」。
- **follow 进程退出本身就是信号**，必须看退出码：
  - `0`：收到真终结事件（`failed` 或 `archived`）→ 先 `show` 确认。两个都是终态：
    `failed` 来自 stop / `resume --force` / 启动失败，`archived` 来自 `done`。
    **`turn_failed`（回合失败）不会让 follow 退出**——任务进 `waiting_review`，
    订阅还活着，照常审核即可
  - `124`：空闲 3 小时一帧都没收到 → **可疑**。正常情况下 agentd 的 stalled
    会先到；先 `handoff show`，再怀疑 agentd 失联
  - 其他非 0：鉴权失败 / 任务不存在 / 连接永久失败 → 看 stderr 按排障表办，
    **不要盲目重开**（401、404 重开一百次还是同样的结果）

醒来 → `handoff show <完整 task-id>` → 处置。**没有重挂这一步。**

`show` 是权威，事件只是唤醒信号——`--follow` 下这条比以前更要紧：事件可能在
你正忙时流入，cursor 已经推进而你还没看。**任何处置前先 show，以 `state` +
`pending_tickets` 为准。**

### 另一个会话只等本任务归档

后续会话的实现依赖当前任务**真正审核归档**时，那个会话挂一条门闩就够了：

    handoff wait <完整 task-id> --until-done --timeout 3h

它不消费协调者的游标，也不把 question / permission_request / completed 送进后续
会话；只在 `handoff done` 产生 `archived` 后输出一行原始事件。退出 `0` 才能开工，
`124` 表示本轮等待到期（任务还没归档），其他非 0 是依赖失败或配置错误。

**它只负责唤醒，不自动 dispatch，也不触发分支自动同步**——下游会话拿到 `archived`
后要自己 `handoff pull`。它也不能替代本任务自己的 `wait --follow` 审核订阅——
工单、completed、`done` 仍由本任务的协调者照常处理。`--timeout` 在这个模式
下是**总时限**，中间帧不续命：否则一个永远没人 `done` 的任务能把门闩拖到天荒地老。

### cursor 语义：为什么 wait 可能吐出旧事件

`wait` 的「不重不丢」靠协调者**本机**的游标文件（`~/.handoff/cursors/` 下按 agentd 地址分命名空间，每任务一个），且**只有 wait 成功交付事件时才推进**。两个直接后果：

- `show` / `reply` 不推进 cursor。走「show → reply」恢复流程之后再挂 wait，第一批返回的可能是**你早已处理过的历史事件**（答过的 question、continue 过的 completed）。
- 换一台机器接管时本机没有 cursor 文件。**`wait --follow` 会在建连前先对账**，
  把水位之前的一切折成一行 `backlog_summary`（带 `missed` / `stale` / `actionable`），
  而不是逐条重放；**对账同时把磁盘游标直接推到水位**——被折叠的积压从此不会再逐条
  交付，要看历史只能 `handoff show`。一次性 `wait`（不带 `--follow`）没有对账机制，
  会从本机游标（换机接管时即 seq 0）起逐条重放。

这不是 bug，是「事件即信号、show 即权威」分工的推论。所以纪律固定为：醒来先 show。发现 reply 返回 404 后，必须用任务实际所在机器执行 `handoff show <task> --target <机器>`，读取 `pending_tickets`：ticket 仍在列表就原样重发 reply；不在列表才按已消耗处理。历史 completed 是否代表当前状态仍由 state 决定。

## 远程派发：代码怎么过去，改动怎么回来

用 `--target <name>` 派发到远程执行机时，两台机器上是**两个独立的 git 仓库**。handoff 不传代码，只做校验和同步——所以「本地写的改动」要靠 git 自己走过去。

### 去程：先 push，再 dispatch

`dispatch --target` 会在**当前工作目录**跑 `git rev-parse HEAD`，把这个 sha 作为「基线」上送。agentd 收到后：查这个 commit 在不在任务仓库的对象库里 → 不在就 `git fetch --all --prune` 一次再查 → 还不在就 **400 拒发**，报文是「基线提交在任务仓库中不存在 …… 请先在本地 git push，或用 --no-sync-check 跳过校验」。

这条机制有三个必须记住的边界：

- **只 commit 不够，必须 push。** 校验的是「远端能不能 fetch 到这个 commit」。没推上去的提交，远程永远拿不到；未提交的改动更是完全不可见——校验会拿你的 HEAD 去比，而 HEAD 不含工作区的脏改动，所以它会**静默通过**，然后 executor 基于一份没有你最新改动的代码开工。
- **项目本身就取自 cwd，所以必须在项目目录里发 `dispatch`。** 项目由当前工作目录的 origin 识别，基线同样取自 cwd。未给 `--project` 时，cwd 不是 git 仓库**直接被拒**。**注意：`--project` 不会跳过基线校验**——跨项目派发时它照样拿 cwd 的 HEAD 去校验目标仓库，会假拒绝；cwd 与目标项目不是同一个仓库时，必须自己加 `--no-sync-check`。
- **新分支的起点是你派发时的本地 HEAD，不是执行机仓库的 HEAD。** agentd 收到基线后，既拿它做存在性校验，也拿它做新分支的起点——两件事出自同一次决议，不会再分叉（B35 之前会：校验的是你的基线，开分支用的是执行机 HEAD，中间可以差出几十个提交而毫无痕迹）。派发成功后 stderr 会打一行 `分支 <名>，起点 <短号>`（B76 起的三件套文案）；执行机仓库比这个起点新时还会补上「领先 N 个提交，新分支不含它们」。
- **`--no-sync-check` 关掉的不止是校验。** 它同时关掉起点决议——没有基线可用时，新分支的起点退回执行机仓库当前的 HEAD（很可能是旧的）。只在 cwd 与 `--project` 指定的项目不是同一个仓库时用它。

稳妥的远程派发姿势：

```bash
git push                                                  # 缺这步必被拒
handoff dispatch --target devbox \
  --new-worktree --new-branch feat/x plan.md              # 起点自动取你当前的 HEAD
```

`--base` 仍然可用，用于**刻意**从别处开分支（比如从某个 tag 或更早的提交起）；给了它就以它为准，也不会再提示分叉。

### 纪律块：agentd 自动注入，别手工拼

派发时 agentd 会按 executor **自动**把执行纪律块注入首回合 prompt（B129）：内置
两版——subagent 版（opencode / claude）与 single-context 版（codex / grok，
未登记的 executor 也走这版，保守方向）。**不要再手工把纪律块拼到 plan 文件头部**：
模板会再注入一份，纪律在 prompt 里出现两遍（codex 因常驻 developer instructions
会有三遍）。

- 按机器覆盖：映射与正文在 Web 控制台改（设置页编正文、开发机详情配
  executor→纪律映射），落盘即生效，不必重启 agentd。CLI 没有开关。
- 派发成功后 stderr 回显 `纪律块: <来源>`（如 `内置:single-context` /
  `配置:my-rules.md`）——这是你确认「注入了哪版」的唯一入口，派发后瞄一眼。
- 纪律块文件不可用时 agentd 直接拒发（500 带真因），不会静默不注入。

### 回程：wait 自动 fetch，合并是你的决定

回合结束（`completed` / `turn_failed`）时 `wait` 会自动把远程任务分支同步回来（配置 `sync.auto` 默认开，`--no-sync` 可关；`--follow` 下**每个回合结束都同步一次**，`archived` 与 `--until-done` 不触发）；也可以随时手动：

```bash
handoff pull <task> --target devbox
```

`pull` 主路径走 agentd 的 **HTTP bundle**（复用已有连接与鉴权，不需要 ssh）；只有对端 agentd 太旧不支持（404）才回落老的 ssh fetch，其他任何错误如实报错、不回落。落到**当前工作目录**的仓库，**只 fetch，不 checkout、不合并**——合并进你的主线是审核决定，handoff 不替你做。

去程回程都以 cwd 为准，所以 `dispatch` / `wait` / `pull` 最好都在同一个本地仓库目录里发。`--target` 机器配置里的 `user` 字段（ssh 用户名）**只在回落 ssh 老路时**才用得上——对端 agentd 够新时它完全不参与；ssh 老路在 Windows 执行机上不可用。`attach` 走 agentd 的 render 流，从来不需要 ssh。

本机派发（不带 `--target`）完全不走这一套：代码本来就在同一台机器上，基线校验直接跳过，`pull` 也会告诉你「本机任务，无需同步」。

**项目由 cwd 识别，第一次派到某台开发机会自动登记。** 你不需要（也无法）告诉
handoff「代码在那台机器的哪个目录」——那是它自己的事。首次派发会多一次往返，
远程可能含一次全量 clone（落点已存在时会直接认领、不重复 clone），stderr 会打出
「正在让 <机器> 落地项目 …」。

**在 worktree 里派发会归并到主仓。** 项目位置永远是主工作树，不是你当前所在的
那个 worktree。想接着某个分支干，用 `--base <分支>` 显式表达。

### 重连/补挂后的第一行：`backlog_summary`

`wait --follow` 每次建立连接前都会对账一次。本机 cursor 之后有积压时（断网重连、
忘挂之后补挂、换机接管），它先吐**一行**摘要再转入实时流：

    {"type":"backlog_summary","task_id":"…","from_seq":2489,"to_seq":2537,
     "state":"waiting_answer","missed":14,"missed_truncated":false,"stale":11,
     "actionable":[{"id":"…","kind":"gate","request":{…}}]}

怎么读：

- **`actionable` 是权威的「你还欠什么」**，每张带完整请求原文，可直接
  `reply --ticket <id>`。它**不限于间隙内**——断网前你就看见过、一直没答的也在里面。
- `stale` 只是间隙里已经被审批链答掉的工单数，不能单独决定某个 404 是否可跳过。遇到 404，先在任务实际所在机器执行 `handoff show <task> --target <机器>`，检查 `pending_tickets`；仍在列表就原样重发，列表没有才跳过。不要把 backlog_summary 的计数当成当前欠办清单。
- `missed_truncated` 为 `true` 时，`missed` / `stale` 的语义是「**至少**这么多」
  ——快照的事件窗口没覆盖到 cursor。此时 `actionable` 仍然精确。
- 摘要行**不是**事件，`agentd` 不存这个类型；它只在客户端合成。

积压事件不会再逐条推给你——那会让一次重连变成 N 次会话唤醒。要看被折叠掉的历史，
用 `handoff show`。

## 事件分诊表

`wait` 返回的 JSON 形如 `{"seq":N,"task_id":"...","type":"...","payload":{...}}`。按 `type` 分诊：

| type | 含义 | 你要做的 |
|------|------|---------|
| `permission_request` | executor 要执行一个需授权的操作 | 判断后 `reply <task> --ticket <id> --approve` 或 `--deny --reason "..."` |
| `question` | executor 卡在一个需求取舍上 | `reply <task> --ticket <id> --answer "..."` |
| `completed` | 一轮干完了，任务进 `waiting_review` | 进入审核：`diff` → 决定 `continue` 还是 `done` |
| `turn_failed` | 一轮以失败收尾，任务进 `waiting_review`（executor 会话还在） | 与 `completed` 同路：`diff` 取证后 `continue` 续接重试或 `done` 归档。follow 订阅**不会退出**，不用重挂。**别急着重新 dispatch** |
| `failed` | 任务真终结：`stop`、`resume --force`、启动失败、看门狗补正 | follow 随之退出（码 0）。先 `show` 取证；想继续只能重新 `dispatch` |
| `approval_dropped` | 你**批准**的裁决没送到 executor（回合已结束），agentd 已代回一个 reject | 后果比 deny 丢失重：那一步被打断了。`continue` 让 executor 重跑该步 |
| `resource_pressure` / `task_proc_pressure` | 执行机进程余量告警 / 单任务进程数越预算 | 看 payload 的 `used/limit`（或 `used/budget`）；连续告警时 `attach` 查 executor 是否在泄漏进程 |
| `archived` | 任务被 `done` 归档，`payload.note` 是协调者留的完成说明 | 这是任务真正结束的信号。等这个任务的下游会话据此开工；自己是协调者时无需动作。只有 `wait --until-done` 把它当成功信号；`wait --follow` 收到它后随连接正常结束 |
| `delivery_failed` | 裁决落库了但没送到 executor | **`handoff resume <task>`**（详见排障） |
| `stalled` | 看门狗：长时间无产出 | `attach` 或 `show` 判断 executor 是真死还是在长跑：真死就 `stop`；若模型其实已干完（如 `attach` 能看到结果、`git log` 有新提交）而事件流停在 `question`/无终态，那是 agentd 断连窗口丢了终态事件——**先 `handoff resume <task>` 对账补回**（自动补发后任务会自然迁移），判不出再 `handoff resume <task> --force` 收口，`stop` 是最后手段 |

`ticket_id` 在 payload 里，**一次性消耗**。同一个 ticket 回答两次，第二次 404。

## 审批：批什么，不批什么

`--approve` 批的是**这一条**操作，不是一类操作的长期授权。两个自动化例外要心里有数：同一任务内**等价**的权限请求会自动复用你先前的 allow——判等不是逐字比对，而是三域指纹（命令域 / 路径域 / 全文域，B91），同一条命令换个包装也会命中（`permission_reuse` 事件留痕，跨任务不复用）；还有一档**静态规则自动放行**，根本不会来问你，B249 起覆盖三类：**落在任务范围内的写入**（任务工作区、任务私有目录、任务临时目录三个根；共享的 `/tmp/<executor>` **不在**范围内，写它照旧升级）、**已知安全命令**（`go build|test|vet`、`gofmt`、`npm test|run`、`make`、`ls|cat|grep`、`git status|diff|log`，以及 charter 台账纪律的法定动作 `git add <范围内路径> && git commit --amend --no-edit`）、**handoff 自身的只读子命令**。白名单匹配命令主体形态而非子串，`echo "go test"` 不会被放行；未登记的 `handoff` 子命令一律 fail-closed。命令白名单的每次放行都**补一条事件**，所以「这一段静默放行了什么」能从事件流查到，不必开 Debug 日志。

**`--deny` 一定要带 `--reason`**。理由会随应答回到模型手里；不给理由，模型只知道「被拒了」，下一步大概率原地再试一次同样的操作，白烧一轮。理由是否送达的留痕分执行器：claude **与 agy** 的理由与裁决**同帧送达**，事件历史里**不会**有留痕事件——没有留痕不等于没送达，反而是送得更早；其余 executor（opencode / grok / codex）走带外注入，事件历史里有 `deny_guidance_relayed` / `deny_guidance_dropped`。

```bash
handoff reply <task> --ticket <id> --deny --reason "别装全局包，加到 go.mod 里"
```

**这些别自己批，升级给用户**：删除数据、`git push` / 改写历史、往外部服务写入或发布、装全局依赖、动 CI/密钥/生产配置。你是替用户看着这个 executor 的，不是替它签字的。判断不了就把 `permission_request` 的原文贴给用户问。

## 审阅取证

任务进 `waiting_review` 后，三条只读命令帮你判断改得对不对：

```bash
handoff diff <task>                      # git diff + 提交列表（主要素材）
handoff diff <task> --base main          # 指定比较基线
handoff fetch <task> internal/foo/bar.go # 读任务仓库里的单个文件
handoff run <task> go test ./...         # 在任务仓库执行命令（sh -c，10min 超时）
```

`handoff diff` 默认用任务自己的基线提交，没有才按仓库默认分支推导。所以默认 diff
就是这个任务的改动，不再含 base 分支与任务分支之间的历史。

**`handoff run` 的参数顺序有坑**：handoff 自己的 flag 必须写在 `<task>` **之前**，任务名之后的一切（含 `-v`、`--race`）都原样透传给被执行的命令。

**`handoff run` 的参数按个数分两档**：

- **只给一个参数** = 一条 shell 命令原文，原样交给远端 `sh -c` 解析：
  `handoff run T1 "cd web && npm test"`
- **给多个参数** = argv，逐个做 shell 转义后再拼接。你敲的引号、空格、元字符
  原样到达远端：`handoff run T1 grep -rn 'foo bar' .`

B66 之前多参数形态是直接空格重拼的，`'foo bar'` 到远端会变成两个参数——静默失真，
不报错。

```bash
handoff run --target devbox T1 go test -race ./...   # ✅ --target 在任务名之前
handoff run T1 --target devbox go test ./...         # ❌ --target 会被当成 go test 的参数
```

## 改与收

```bash
handoff continue <task> "把重试次数改成 3，并给这个分支补一条失败用例"
handoff done <task> --note "已验收：重试与失败用例都符合预期"
```

- `continue` 是**同一会话续接**，executor 的上下文完整保留——不需要在指令里重述前情。
- `continue` 之后任务回到 `running`；follow 订阅还活着时会继续收到新一轮事件，**不需要重挂**。回合失败（`turn_failed`）同样不断订阅——只有真终结的 `failed` / `archived` 才让 follow 退出，而那之后也没有 `continue` 可言。
- `done` 归档任务并回收 executor（停进程、删 managed worktree；**任务目录不删**，留作排查素材）；`--note` 的说明会写进任务记录与 `archived` 事件，等这个任务的下游会话靠它知道结果。对已是 `completed` 的任务重发 `done` 幂等返回 200，不算错。

**`done` 返回成功之前，什么都不要删。** `done` 会因状态不符被拒（409）；如果你已经先手删了任务目录或杀了 executor 进程，就会留下一个 agentd 记着、但资源已经被你拆掉的孤儿，只能手工补清。顺序永远是：先 `done`，看到 `{"ok":true}` 再谈清理。

`handoff stop <task>` 是另一条出口：主动中止，停 executor、作废挂起工单、任务落 `failed`。任务跑偏了不想再等，用它。

## 账本模式：把任务回路包在卡回路里

> **前置条件：本项目的 agentd 配了 `ledger.enabled: true`。**账本是可选功能，
> 默认关闭——没开就跳过整节，按上面的任务回路做即可（`card` 命令会直接报
> 「账本未启用」）。

账本模式**不是第二条主循环**，是把上面那条包了一层：

- **外层（本节）**管「卡」的调度——哪张卡该开工、派给谁、做完推到哪个状态。
- **内层**就是上面的任务回路，一字不变——醒来处置 `permission_request` /
  `question` / `turn_failed` 用的还是 `reply` / `approve` / `continue`，
  事件分诊表、审批硬纪律、审阅取证、排障各节**原样适用**。

一句话记法：**`card` 族管卡，执行域动词管 task，两者分层不混用。**

### 卡命令族速查

| 动作 | 命令 |
|---|---|
| 建卡 | `handoff card add "<标题>" --project <项目> [--workflow <流>] [--priority 高\|中\|低] [--parent <父卡>]`（不指定流时按账本内流集合解析：零条先建流、唯一条自动使用、多条要求显式指定） |
| 按原号导入 | `handoff card import <B号> "<标题>" --project <项目> --source <来源>`（撞号即拒） |
| 看板 / 单卡 | `handoff card list [--status <列>] [--needs] [--all] [--json]` / `handoff card show <id>` |
| 挂附件 / 改卡 | `handoff card update <id> --attach <kind>:<仓内相对路径> / --title / --priority / --accept` |
| 移列 | `handoff card move <id> <状态>`（CAS；`--expect` 钉前值；gate 拒绝会说清缺什么） |
| 跨流迁移 | `handoff workflow migrate <id> --workflow <流> --column <落点列> --yes` |
| 记一笔 | `handoff card note <id> "<正文>" [--correction] [--reset-node <节点>]` |
| 记验收 | `handoff card accept <id> --evidence "<命令+结果>"` / `--unverified` |
| 拆子卡 / 阻塞边 | `handoff card split <id> "<子卡标题>"` / `card link <阻塞者> <被阻塞>` |
| 等人标记 | `handoff card needs <id> "<原因>"` / `--clear` |
| 搁置 / 复活 / 终止 | `handoff card close <id> --reason 搁置\|取消\|废弃` / `handoff card revive <id>` |
| 工作流形状 | `handoff workflow show <流>`——**列序与门以账本为准，任何文档都不复制** |

### 状态不会自己流转

账本里每一次状态转移都必须有 actor 落进事件流（取证要能回答「这步是谁推的」），
所以没有「自动变态」这回事。区别只在**谁**去推：

| 谁 | 推什么 |
|---|---|
| **代码自动** | `--step` 认领卡的驱动权（只写 `driver_session`，**不改状态**）；裁决 pass 自动进下一列、fail 退回上一节点再来一轮；声明了 `produces` 的节点在 pass 时自动挂附件再路由；一切失败出口打「等人」标记 |
| **你（主会话）** | 逐节点点火 `card dispatch --step`（**连点就是跳过协调者检查点**）；人工列（spec / acceptance / finish）做完后自己 `card move`；定级跳边 |
| **人** | 答裁决、批工单、合 main、推「已完成」 |

- 节点声明的法定产出路径必须逐字使用；不要在 basename 前加 `YYYY-MM-DD-` 日期前缀。带日期前缀的是历史文件，不是本节点的法定产出；写错时按 prompt 给出的法定路径改名。

一期你就是那台发动机。三期规则引擎接手「按按钮」的活，调的是同一个环节执行体。

### 1. 唤醒先查账，不信会话记忆

被唤醒（新会话接管、压缩后续跑、隔了一夜）后**先重建现场再动作**：

```bash
handoff card list --needs          # 需要你的：等人标记 + 挂卡裁决
handoff decision list              # 未答复的裁决（含项目级、无卡可挂的那些）
handoff card show <id>             # 在飞的卡：字段 + 关系 + 挂账 task + 事件流
```

修复回合计数、环节走到第几轮这类推进状态**一律从事件流推导**，不存会话记忆——
和「`show` 是权威、事件只是信号」是同一条纪律。

### 2. 派发前查账，防重复开工

```bash
handoff card list --project <项目>     # 落在执行列（既非「待办」也非「已完成」）的就是在飞的
```

这条是「重复开会话把同一件事又做一遍」的正解。**别按 `--status 进行中` 筛**——
现役 charter 流没有这一列，那条查询稳定返回**空表**，看起来像「没人在做」
（2026-08-24 实测；这是文档腐烂里最阴的一种，它不报错）。

真撞上了通常不会出事：`--step` 派发前先认领卡的驱动权，第二个会话干净失败并报出持有者。
但**认领失败在 `--step` 下是静默的**——见排障表最后一行。

> **B239 已修，等部署**（2026-08-25 合入 main）：节点入口的失败（认领被拒、运行锁被
> 他方持有、派发失败）现在一律落到卡的事件流上——一条带原因原文的 comment ＋ 一条
> `needs_human`，`card wait` 实时收得到，不再只进 `agentd.log`。**但这要各机重新构建
> 并重启 agentd 才生效**；在那之前上面这条「静默」仍然成立。判断手上是新是旧：造一次
> 入口失败，卡上有没有那两条事件。

### 3. 外层派发与等待

```bash
handoff card dispatch <id> --step <节点名>   # 走工作流节点（节点名 = 看板列名）
handoff card wait <id> [--subtree] [--timeout 3h]
```

- **裸 `card dispatch`（不带 `--step`）在 charter 流上必然失败，不要用**
  （B237，2026-08-24 实测）：它的「派发即认领」把卡 CAS 到硬编码的「进行中」
  （`internal/ledger/types.go`），而现役唯一的流没有这一列。报文是
  `认领失败（可能被并发抢先）: 状态 "进行中" 不在工作流 charter v9 中`
  ——**前半句是错误归因**（当时并没有并发），真因在后半句，排查时别往 CAS 冲突方向找。
  卡驱动一律走 `--step`。

  > **B239 已修，等部署**（2026-08-25 合入 main）：认领已一分为二——归属锁（人尺度，
  > 写 `driver_session`，**不再改卡的状态**）与运行锁（运行尺度，带租期）。裸
  > `card dispatch` 因此不再把卡挪去「进行中」，在 charter 流上能正常派发。
  > 同样**要重新构建并重启 agentd 才生效**，部署前上面这条仍然成立。
  > 即便修好之后，卡驱动仍推荐走 `--step`——它才带节点语义（自动挂卡、模板与纪律块快照、
  > 裁决路由）。
- `--step` 会自动做三件事：**认领卡的驱动权**（只写 `driver_session`，**不改卡的状态**；
  纯人工节点直接跳过认领）、把 task 回链到卡、把模板版本与纪律块 hash 快照进派发事件。
  **「挂卡」不是一个你要单独做的动作。**
- **`--step` 提交后先短等首态**：CLI 只把请求交给本机 agentd，HTTP 仍是 202，编排仍在
  agentd 里异步运行。CLI 在 POST 前记下本机账本 seq 水位，最多短等约 20 秒，只看这次水位
  之后的卡事件：看到 `dispatched` 就在 stdout 打出目标机、新分支 `branch`、起点分支
  `base`、起点短号和纪律块名；看到 reason=`派发失败` 的 `needs_human` 就把卡上
  `haltForHuman` 的 comment 正文写到 stderr 并以非零退出。HTTP 202 之前的 404/400/409、
  纪律探活/拒发闸 400 仍当场失败。短等窗口内没有这两类首态时，stdout 打「已受理，首态未到；
  进展见 handoff card wait」，退出 0；命令返回值不带回合结论。不要用 task WS 或卡上历史
  `dispatched` 推断这一次派发。CLI 与 agentd 必须同批升级。
- 本机卡派发省略 `--target`；不要在 `targets` 登记指向本机 loopback 的自机。`--target 本机`
  不是合法键；版本不一致时的「目标机未定」仍是版本 skew 文案，不表示本机 target 缺失。
- 裁决落在卡的事件流（`review_verdict`）：pass 自动进下一列，fail 退回上一节点
  再来一轮；裁决解析失败或超轮打 `needs_human`，人工裁决后把结论 `note` 落卡，
  重派前 `card needs <id> --clear`。
- 一次性覆盖：`--executor` / `--model`（B203）、`--extra "<本轮补充>"`（进 prompt
  的「本次补充」小节，不落卡、不影响后续轮次）、`--discipline-override <角色>`（应急）。
- `card wait` 跟的是**账本单流**（卡或整棵子树的事件，含镜像进来的 task 事件），
  不是 task 集合——所以挂起期间新拆的子卡、新派的任务天然进流，没有动态成员问题。
- **一次工作流只挂一次 `card wait`，不必再叠 task 级 `wait --follow`**。唤醒语义
  与 `wait --follow` 同款：逐条事件即时流出、命令不退出、不用重挂；工单
  （`question` / `permission_request`）由镜像子系统转成 `task_mirrored` 进卡流，
  只跳过 `progress` / `approver_decision` / `approver_disabled`
  （`internal/ledgermirror/mirror.go` 的 `mirrorSkip`）。**卡流该有的事件却没动静时，
  先查自己的命令有没有接管道**（见上文「订阅」一节的过滤器禁令），别先怀疑镜像。
- 醒来之后**处置方式与任务回路完全相同**：先 `handoff show <task>` 以 state
  为准，再按事件分诊表办。别在这里另发明一套。

### 4. task 完成之后的推进

```bash
handoff card dispatch <id> --step <节点名>   # 触发下一个派发列（节点名 = 看板列名）
handoff card accept <id> --evidence "go test ./... 全绿"
handoff card move <id> <下一列>              # 人工列的跳转（如 acceptance → finish）
```

四条要点：

- **各流的列序与逐节点卡操作对照表在 `product-backlog` skill 的「推进 charter 流」**
  ——那边是驾驶手册。工作流定义由用户或上层方法论安装，handoff 出厂不预设任何流；
  本节不复制某条流的列序，只说明流无关的通用机制。建卡未指定流时，账本按流数量作
  唯一解析：空账本指向先建流，恰好一条自动使用，多条必须显式指定。
- **审阅类节点的 fail 会自动 `continue`**（带发现项原文），**3 轮封顶**，超限自动
  打「等人」。要人工重置计数用 `handoff card note <id> --reset-node <节点名>`
  （注意这个 flag 仍叫 `--reset-node`：「节点→环节」改名只动了
  `card dispatch` 的 `--step`，没顺带改它）。
- **`card accept` 的「已验」必须带 `--evidence`**——已验是一个断言，无证据的
  断言不许落账。还没验就用 `--unverified`。
- **合并主线永远人工**：现役各流都没有自动合并节点——charter 流的 finish 是
  人工列，合并归人（`charter:finish`）。账本里不存在「跑完自动合 main」这回事。

`card move` 的每一步都拿卡钉的那版工作流当法律——状态名合不合法、gate（如
「进 `implement` 需 plan 或 breakdown 附件」）过不过，全按配置判。被拒了先 `handoff workflow show
<name>` 看形状，别硬推。

### 5. 回合末四分法落账

聊天里的长报告照旧写，但那四类信息**必须同时落进账本**——不然并行几个会话时，
真正需要用户的两三行会被战报淹没（这正是账本要解决的痛点）：

| 报告里的 | 落账动作 |
|---|---|
| 完成项（带证据） | `card move` 推状态 ＋ `card accept --evidence` 记验收 |
| 更正 | `card note <id> --correction "<更正内容>"` |
| 请示裁决 | `decision open "<正文>" [--card <id>] [--option A --option B]` |
| 阻断需人工 | `card needs <id> "<原因>"`（原因必填，解除用 `--clear`） |

`decision open` 不挂 `--card` 就是项目级裁决（如「推不推汇流线」），照样进
「需要你」。**open 的裁决不答复不消失**——用户从看板清账，不再从聊天记录里捞。

### 6. 验收后发现 bug：开新卡，不 reopen

```bash
handoff card add "<标题>" --project <项目>          # 缺省按账本流集合解析；多流时显式加 --workflow
handoff card note <新卡> "发现自 <原卡 id> 的验收"
# 定性后按级别走 charter：L1 挂 spec+plan 合体页跳 implement（见 product-backlog）
```

账本历史不改写，与 task 机的「归档了就是归档了」对齐。

**别用 `card link` 挂血缘**：它加的是**阻塞边**（`link <blocker> <blocked>`，
前者阻塞后者），语义完全不同——把新 bug 卡和原卡 link 起来，等于声明其中一张
挡着另一张开工。血缘关系（`discovered_from` / `relates`）在数据模型里有，但
**今天 CLI 与 API 都造不出来**，所以先用 `card note` 把出处记进 timeline。
真正需要挂边时，用 `card link` 表达的必须确实是「A 没完成 B 不能开工」。

### 账本模式的红旗

| 念头 | 事实 |
|------|------|
| 「状态应该会自动流转吧」 | 每次转移都要 actor。一期的发动机是你，不是数据库。 |
| 「我记得这张卡已经推到 review 了」 | 和 task 一样：`card show` 是权威，会话记忆不是。 |
| 「审阅没过，我再 continue 一轮」 | 环节自己会 continue，3 轮封顶。手工绕开封顶就是绕开防死循环的安全阀。 |
| 「验过了，accept 一下，证据就不写了」 | 已验必须带证据，命令会直接拒。这是取证文化不是输入校验。 |
| 「节点跑完就合进 main 了吧」 | 主线永远人工。合并发生在 finish 人工列，由你本地做。 |
| 「先 `handoff dispatch` 派了，回头再挂卡」 | 那样出来的是「未挂账」task，重复开工检测看不见它。要挂卡就用 `card dispatch`。 |
| 「卡的事件流里没有，那就是没发生」 | 镜像可能滞后。看板会显式标「事件流滞后」，`card show` 的挂账 task 也能对账。 |

## 会话恢复：从零接管

一个完全没有前文的新会话，先重建现场：

1. `handoff tasks` —— 每行任务 JSON 现在带 `watchers`（有几个连接在听）
2. 给每个 `watchers == 0` 的**活跃**任务（`pending` / `running` /
   `waiting_answer`）补开一条 follow 订阅（无后台机制的 harness 改为前台
   逐轮 wait，见「在 agent 会话里挂 wait」）。`waiting_review` 不用补：
   它在等你裁决，挂几天都正常。补订阅与播报分开：补订阅照旧对上述所有
   `watchers == 0` 的任务做；向用户播报需处置事项时，只报无挂账卡或卡上
   没有 `DriverSession` 的真孤儿。有驱动归属的任务不打扰用户，恢复报告里
   最多列一行事实，例如「卡 B177 由 <session> 驱动，已补挂订阅」
3. `handoff show` 逐个清 `pending_tickets`

`handoff status` 会把归属结论直接标在活跃任务行上：真孤儿是 `⚠ 无人值守`；
有卡驱动但暂时无人订阅的是 `⚠ 无人订阅（卡 <id> 驱动 <session>，心跳 <时长>）`。

`pending_tickets` 是关键——它是「我还欠哪些没答」的权威清单。把里面每张工单
`reply` 掉，然后按当前 state 决定：`running` → 订阅已在（follow 不需要重挂）；
`waiting_review` → 进审核。接管后第一次 wait 可能重放历史事件（见「cursor
语义」）——照旧以 `show` 为准即可。

会话崩溃、主动关掉重开、换一台机器接管，三种场景都是这一套。**不要**因为「我不记得这个任务了」就重新 dispatch 一个——先 `tasks` 看有没有。

## 确认 agentd 在不在

    handoff status --target <名字>

**不要 ssh 上去查进程、查端口、查二进制。** 那是在验证零件，而问题是「这个服务
现在能不能用」；零件检查有无数种失败方式（PATH、平台差异、引号嵌套），每一种都
长得像「没有」。

| 输出 | 结论 | 处置 |
|---|---|---|
| 正常一屏（版本/数据/任务） | 能用 | 直接派发 |
| `可用（版本过旧）` | 能用，但远端 agentd 不支持 status | 想看详情就升级远端；不看也不影响派发 |
| `target "x" 未在配置 … 中定义` | 你的本机配置问题，不是远端问题 | 补 target 配置 |
| `dial tcp …: connect: connection refused` | 真的没有 agentd 在跑 | 见下面的红线 |
| `状态码 401`（通用报错，无专门提示） | agentd 在，但 token 对不上 | 同步两边的 token |

退出码：**0 = 能用**（含版本过旧）；**1 = 够不着**。

**红线：查到有 agentd 在跑就复用它，不要起第二个。**
两个 agentd 抢同一份数据目录、同一批 worktree 与 executor 进程，正是状态机最怕的
失配。这条现在由代码兜底——同一个 DataDir 起第二个 agentd 会直接被文件锁挡下
并报错，什么都不会被改动。别把它当逃生口：它挡的是事故，不是流程。

**升级 agentd 要先停旧的再起新的。** 以前是新进程起来撞端口失败（但那时破坏
已经造成了），现在是新进程被锁挡在门外。好处是安全，代价是不能再指望「起个新
的把旧的顶掉」。

活跃任务行末尾的存活结论有三态：`executor 存活` / `executor 已不在（理由）` /
`存活性未知（理由）`。**`未知` 不等于 `已不在`**——探不出结论时不要按「死了」
处置，先看理由。

## 排障

| 症状 | 根因 | 处置 |
|------|------|------|
| 任何命令 404「任务不存在」 | 传了 8 位短 id | 用 `handoff tasks` 取完整 UUID |
| `continue` / `done` 报 409 | 任务不在 `waiting_review` | `handoff show` 看真实状态，按状态机表办 |
| `reply` 返回 502，或收到 `delivery_failed` | 裁决已落库但没送到 executor（executor 半死） | `handoff resume <task>`：幂等重投；executor 还在就继续跑，确已不在则转交审核 |
| `resume` 之后 `reply` 404、`attach` 看不到挂起项 | 工单已被消耗 | 正常。按 `resume` 报告里的结论走 `continue` 或 `done` |
| `wait` 立刻报错退出 | 401（token 与 agentd 不一致）或 1008（task-id 错） | 看报错原文，修 `~/.handoff/config.yaml` 或核对 id。**别重开**，它不会自己好 |
| 远程任务的 `wait` / `show` / `reply` 报 `task not found`（1008 / StatusPolicyViolation），id 明明是刚 dispatch 出来的 | **漏了 `--target`**，命令打到了本机 agentd——任务在执行机上，本机当然没有 | 看 stderr 里的 `addr=`：是 `127.0.0.1` 就是漏了 `--target`。补上重发即可，任务本身没事 |
| `wait` 一直不返回 | 通常只是还没有事件 | 正常。stderr 的重连日志也正常。加 `--timeout` 兜底 |
| 重开 follow 后吐出旧事件 | cursor 只在 wait 交付时推进；show/reply 不推进，换机接管从 0 起 | 以 show 为准；若 reply 404，先在任务实际所在机器执行 `handoff show <task> --target <机器>` 并检查 `pending_tickets`；仍在列表就原样重发，不在列表才跳过 |
| `dispatch` 报「工作区不干净」 | **执行机上**的任务仓库有未提交/未跟踪改动 | 在执行机上提交或 stash 后重试（`--new-worktree` 可绕开主工作区的脏检查，但主仓库仍需可用） |
| `dispatch` 报「本地工作区有 N 处未提交的已跟踪改动」 | **你本地**（不是执行机）有改动没提交，远程派发的基线不含它们，executor 会基于旧代码开工 | `git commit` 或 `git stash` 后重试；确认这些改动与本次任务无关时加 `--allow-dirty`（放行仍会打印被忽略的文件） |
| `dispatch` 报 400「基线提交在任务仓库中不存在」 | 本地 HEAD 没 push，或执行机 fetch 不到（无凭证/网络不通） | `git push` 后重试；报文里的 fetch stderr 是根因原文。确实是不同仓库才用 `--no-sync-check` |
| 远程派发成功，但 executor 基于旧代码开工 | 改动只 commit 没 push——校验拿 HEAD 比，HEAD 不含未提交改动，会静默通过 | 派发前先 `git push`。起点本身不用管：新分支自动落在你派发时的 HEAD 上，stderr 的「分支 …，起点 …」行就是实际起点 |
| `continue` 报 500 / 恢复失败 | executor 进程死了但 agentd 记的运行态是陈的 | 先 `handoff show` 确认状态；`agentd.log` 里搜「恢复阶梯」看走到哪一级 |
| 任务归档后有残留（worktree / executor 进程） | 回收失败（事件里会带残留提示） | worktree 用 `handoff reclaim` 回收；进程按事件提示处置，彻底死透按 `proc.json` 的 `handle.pid` 手工 kill shim |
| `card dispatch --step` 已受理后短等超时、卡上仍无 `dispatched`/`派发失败` 首态 | 202 只代表请求已受理；编排仍在 agentd 异步运行，正常首态可能在约 20 秒窗口外；入口认领拒绝/运行锁占用会在卡上 comment + `needs_human` 留痕，ViaTemplate 派发失败也会落卡 | stdout 的「已受理，首态未到；进展见 card wait」是正常短等超时，跟 `card wait`；若短等捕获 reason=`派发失败`，stderr 会有卡上 comment 正文且命令非 0。认领/运行锁问题先读卡上 comment 的 holder/reason；需要接管时用 `card takeover`，不要把 agentd.log 当成唯一证据。|

**日志在哪**（在 executor 所在机器上）：

- `~/.handoff/agentd.log`：agentd 主日志。`HANDOFF_LOG_LEVEL=debug` 可调低级别。
- `~/.handoff/tasks/<完整 task-id>/render.log`：模型回合正文实况，`handoff attach` 流式读取的就是它。
- 同目录下按 executor 分：opencode / grok / codex 是 `serve.log` + `proc.json`（连接凭据与探活依据）；claude 是 `claude.log`（stderr）+ `out.jsonl`（stdout 事件流）+ `perm.sock` + `proc.json`；agy 是 `agy.log`（stderr）+ `out.jsonl`（stdout 事件流）+ `perm.sock` + `proc.json`。`shim.log` 是进程承载层日志，`proc.lock` 是存活锁。

## 红旗——想到这些说明你在偷懒

| 念头 | 事实 |
|------|------|
| 「我记得这个任务的状态是……」 | 你的会话不是权威。`handoff show` 是。 |
| 「短 id 应该也能认吧」 | 精确匹配，没有前缀补全。一定 404。 |
| 「先删掉任务目录再 done」 | 顺序反了。`done` 可能被拒，先删就留孤儿。 |
| 「ssh 上执行机手动杀进程/改工作区更快」 | agentd 不知道你改了什么，运行态当场失配。走 CLI。 |
| 「收到 turn_failed，只能重新 dispatch 了」 | 回合失败进 `waiting_review`，`continue` 能续接，重派才是浪费。真要重派的只有 `failed`（stop / 启动失败 / force-reclaim）。 |
| 「派 plan 前先把纪律块拼到文件头」 | B129 后 agentd 自动注入，手工拼会让纪律出现两遍。看 stderr 的「纪律块: <来源>」确认即可。 |
| 「拒了就拒了，不用写理由」 | 理由是给模型看的。不给，它就原地重试同样的操作。 |
| 「reply 报 502，我再 reply 一次」 | 工单已被消耗，第二次必 404。要的是 `resume`。 |
| 「wait 没动静，是不是挂了？」 | 没有事件就是没有事件。看退出码和 stderr，别瞎重启。 |
| 「Bash 工具会超时，我写个 show+sleep 轮询循环」 | wait 本身就是轮询的替代品。一条后台 wait，退出即唤醒你。见「在 agent 会话里挂 wait」。 |
| 「把几百轮 wait 包进一条 shell 大循环省事」 | 大循环吞掉事件 JSON，且循环期间你无法处置任何工单。每轮一条后台 wait。 |
| 「wait 返回了 completed，直接 continue」 | 可能是 cursor 重放的历史事件。醒来先 `show`，state 说了算。 |
| 「这个权限请求看起来问题不大」 | 破坏性、不可逆、外部可见的操作一律升级给用户。 |
| 「任务好像不见了，重新 dispatch 一个」 | 先 `handoff tasks`。重复派发会开出第二个 executor 抢同一个仓库。 |
| 「代码 commit 完了，可以远程派发了」 | 校验的是远端能否 fetch 到这个 commit。没 `git push` 等于没有。 |
| 「工作区里改了几行还没提交，先派了再说」 | 校验拿 HEAD 比对，看不见脏改动，会**静默放行**——executor 拿到的是没有你改动的代码。 |
| 「stderr 说领先 3 个提交，应该问题不大」 | 那 3 个提交不在任务分支里。执行者会找不到刚加的文件、目录、backlog 行——先想清楚它们是不是这次任务要用的东西。 |
| 「`pull` 完了改动就在我本地分支上了」 | `pull` 只 fetch，不 checkout 不合并。合并是你自己要做的事。 |
| 「Monitor 退出了，再开一个就行」 | 先看退出码。401 / 404 重开一百次也是同样的结果 |
| 「事件流进来了，直接按它处置」 | 事件是唤醒信号，`show` 是权威。`--follow` 下 cursor 会跑在「已读」前面，这条比以前更要紧 |
| 「重连后没收到那 14 条 permission_request，是不是丢了？」 | 没丢。它们被折进了一行 `backlog_summary`，其中仍需处置的在 `actionable` 里，其余是已被审批链答掉的。 |

## 延伸阅读

这份 skill 只覆盖协调者回路。以下不在范围内，需要时读仓库文档：

- **agentd 部署、`config.yaml` 各段、分级审批链、env 注入**：仓库 `README.md`。
- **各 executor 的差异与就绪判据（opencode / claude / grok / codex / agy）**：`README.md` 的「各 executor 须知」。
- **架构与协议设计**：`docs/superpowers/specs/2026-08-07-handoff-design.md`。
- **协调者回路之外的子命令**（`frames` 结构化回合帧、`footprint` 进程足迹体检、
  `machines` / `project` 机器与项目登记、`console` / `sessions` Web 控制台、
  `upgrade` / `service` 换版与托管、`skill` 同步本 skill）：各命令 `--help`。
