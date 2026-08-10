---
name: handoff
description: 用 handoff CLI 把实现计划派发给独立 executor（opencode / claude / grok）执行，并以审核者身份驱动 dispatch → wait → reply → diff → continue/done 的完整回路。只要涉及「把这个 plan 交给远程开发机跑」「派发任务给 executor 执行」「盯 handoff 任务进度」「想写个轮询/sleep 循环等 handoff 任务」「任务卡在 running / waiting_review」「reply 返回 502 / continue 报 409 / done 报 404」「wait 返回了旧事件」「新会话接管一个已经在跑的 handoff 任务」，哪怕用户一个字没提「handoff」，也必须先读这份 skill——handoff 的状态机对操作顺序有硬约束，凭印象敲命令会撞 404/409，并把任务卡成没人收的孤儿。
---

<!--
职责：
  - 教会「审核者」角色如何用 handoff CLI 驱动一个派发任务的完整生命周期。
  - 固化那些一旦搞错就会卡住任务的硬约束：ID 形态、状态机前置条件、事件分诊、失败出口。

边界：
  - 不讲 agentd 的部署与配置（config.yaml 各段、approver 审批链、env 注入）——见仓库 README。
  - 不讲三个 executor 的内部差异与协议实现——见 README「执行者差异」与 docs/superpowers/specs/。
  - 不替审核者做审批判断：批不批、改不改由审核者（必要时升级给人）决定。
-->

# handoff：以审核者身份驱动派发任务

## 心智模型

handoff 把「写计划的人」和「干活的人」拆成两个进程：

- **agentd** 跑在 executor 所在机器上，持有**全部**状态——任务、事件、工单、executor 生命周期，落 SQLite。
- **你（审核者）**只是一个客户端。你不持有任何状态，随时可以崩溃、断网、换一台机器接管。
- 你和 agentd 之间只有一条通道：`handoff` CLI。

这条架构直接决定了三件事，后面所有纪律都是它的推论：

1. **你的会话不是权威**。「我记得这个任务已经批过了」不作数，`handoff show` 说了算。
2. **断网不丢事件**。事件在 agentd 侧持久化并带 cursor，`wait` 重连后从断点续拉。所以你没必要一直挂着。
3. **绕过 CLI 就会失配**。ssh 到执行机去 `tmux kill-session`、手删任务目录、直接进工作区改代码——这些 agentd 全都不知道，它记的运行态和真实存活性会当场对不上，任务卡成孤儿。

### 铁律：一切经 CLI

需要看 executor 在干什么，用 `handoff attach`（它会替你 ssh + tmux attach）。需要看代码，用 `handoff diff` / `fetch` / `run`。需要回收，用 `handoff done` / `stop`。

**唯一例外**：任务已经彻底死了、CLI 三条路（`resume` / `continue` / `done`）全被拒，此时手工 `tmux kill-session -t handoff-<id8>` 是兜底。但那是排障，不是日常。

## 任务 ID 必须是完整 UUID

所有接受 `<task>` 的子命令都是**精确匹配**，没有前缀补全。传 8 位短 id 一律 404「任务不存在」。

短 id（`id8`）只出现在两个地方：tmux 会话名 `handoff-<id8>`、`--notify` 的通知文案。它们不能当命令参数用。

拿完整 id 的办法：`dispatch` 的输出 JSON 里的 `.id`，或者 `handoff tasks | jq -r 'select(.name=="...") | .id'`。

## 状态机：先看状态，再敲命令

六个状态。**`continue` 和 `done` 都硬要求 `waiting_review`**，状态不符返回 409 `ErrBadTransit`——这是最常撞的一堵墙。

| 状态 | 含义 | 此时能做 | 此时会被拒 |
|------|------|----------|-----------|
| `pending` | 已建任务，executor 还没起来 | `show` / `stop` | `continue` / `done` |
| `running` | executor 正在干活 | `wait` / `attach` / `show` / `stop` | `continue` / `done` |
| `waiting_answer` | 有工单挂起，等你裁决 | `reply --ticket ...` / `attach` / `stop` | `continue` / `done` |
| `waiting_review` | 一轮干完了，等你审 | `diff` / `fetch` / `run` / **`continue`** / **`done`** / `stop` | — |
| `completed` | 已归档 | `show` / `diff`（只读） | 一切写操作，含 `stop` |
| `failed` | 已失败 | `show` / `diff` / `pull`（只读取证） | `continue` / `done` / `stop` |

> `failed` 是终态。想在失败后继续，路径是重新 `dispatch`，不是 `continue`。

**动手前先确认状态**：`handoff show <task>` 输出一行 JSON，含任务体 + `pending_tickets` + 最近事件。不确定就先 show，比吃一个 409 便宜。

## 主循环

```bash
# 1. 派发（仓库工作区必须干净，否则被拒——脏改动会被污染进任务分支）
handoff dispatch --repo /path/to/repo --new-worktree --executor opencode plan.md
# stdout 第一行是任务 JSON，取 .id 作为后续所有命令的 <task>

# 2. 挂等待（阻塞，一次只返回一个事件）
handoff wait <task> --notify --timeout 1h

# 3. 按事件类型分诊、处置（见下表）

# 4. 回到第 2 步重新挂 wait，直到进入审核
```

**`wait` 的三条契约**，记牢了省很多事：

- **一次只吐一个事件**。stdout 单行 JSON，然后退出。处理完必须**重新挂**，否则后面的事件没人接。
- **退出码有语义**。`0` = 事件到达；`124` = `--timeout` 到点（可以接着挂）；`1` = 真失败。
- **失败会立刻退出，不会闷等**。token 没同步（401）、task-id 不存在（1008）都是立即报错。`wait` 长时间不返回**只**意味着「还没有事件」，那是正常态——stderr 里的「WS 连接断开，等待后重连」也是正常态。

无人值守时务必带 `--timeout`：它是配置错误的最后一道防线，退出码 124 可以和真失败区分开。

`progress` / `approver_decision` / `approver_disabled` 三类事件**不会**唤醒 `wait`（只入库）。你只会在 `show` 的事件历史里见到它们，日常不用管。

## 在 agent 会话里挂 wait（Claude Code 等）

上面的主循环假设操作者能前台阻塞一小时。agent 的 Bash 工具做不到（前台超时上限通常只有几分钟到十分钟），于是最常见的走样就是自己发明 `show` + `sleep` 轮询循环，或把几百轮 wait 包进一条 shell 大循环。**两种都不要。** 正确形态只有一种：

**每一轮 = 一条后台命令，内容就是一条裸的 wait。**

```bash
# run_in_background 挂上，然后结束你的回合
handoff wait <task> --notify --timeout 1h
```

后台进程退出时 harness 会自动唤醒你——这就是循环的「下一圈」，不需要 sleep，不需要计数器。醒来后：

1. 按退出码分诊：`0` → 有事件；`124` → 超时无事，直接重挂；`1` → 看 stderr，按排障表办（别重挂）。
2. **退出码 0 时，事件只当唤醒信号用，先 `handoff show`，以它的 `state` + `pending_tickets` 为准**（为什么见下面的 cursor 语义）。
3. 处置完（reply / continue / 进审核）再挂下一条 wait。

循环体是「挂 → 醒 → show → 处置 → 重挂」，由多个回合天然构成，**不写任何 shell 循环**。

### cursor 语义：为什么 wait 可能吐出旧事件

`wait` 的「不重不丢」靠审核者**本机**的 `~/.handoff/cursor-<task>` 文件，且**只有 wait 成功交付事件时才推进**。两个直接后果：

- `show` / `reply` 不推进 cursor。走「show → reply」恢复流程之后再挂 wait，第一批返回的可能是**你早已处理过的历史事件**（答过的 question、continue 过的 completed）。
- 换一台机器接管时本机没有 cursor 文件，wait 从 seq 0 起把历史可动作事件重放一遍。

这不是 bug，是「事件即信号、show 即权威」分工的推论。所以纪律固定为：**醒来先 show**。历史 question 的 ticket 已被消耗，补 reply 会 404——正常，跳过即可；历史 completed 也不代表当前在 `waiting_review`，state 说了算。

## 远程派发：代码怎么过去，改动怎么回来

用 `--target <name>` 派发到远程执行机时，两台机器上是**两个独立的 git 仓库**。handoff 不传代码，只做校验和同步——所以「本地写的改动」要靠 git 自己走过去。

### 去程：先 push，再 dispatch

`dispatch --target` 会在**当前工作目录**跑 `git rev-parse HEAD`，把这个 sha 作为「基线」上送。agentd 收到后：查这个 commit 在不在任务仓库的对象库里 → 不在就 `git fetch --all --prune` 一次再查 → 还不在就 **400 拒发**，报文是「基线提交在任务仓库中不存在 …… 请先在本地 git push，或用 --no-sync-check 跳过校验」。

这条机制有三个必须记住的边界：

- **只 commit 不够，必须 push。** 校验的是「远端能不能 fetch 到这个 commit」。没推上去的提交，远程永远拿不到；未提交的改动更是完全不可见——校验会拿你的 HEAD 去比，而 HEAD 不含工作区的脏改动，所以它会**静默通过**，然后 executor 基于一份没有你最新改动的代码开工。
- **基线取自 cwd，不是 `--repo`。** 必须在本地那份同仓库的 checkout 里发 `dispatch`。cwd 不是 git 仓库时只打一行提示就跳过校验（「远程仓库可能落后于你的本地代码」），不报错——这是最容易悄悄踩空的一格。
- **新分支的起点是你派发时的本地 HEAD，不是执行机仓库的 HEAD。** agentd 收到基线后，既拿它做存在性校验，也拿它做新分支的起点——两件事出自同一次决议，不会再分叉（B35 之前会：校验的是你的基线，开分支用的是执行机 HEAD，中间可以差出几十个提交而毫无痕迹）。派发成功后 stderr 会打一行 `基线 <短号>`；执行机仓库比这个起点新时还会补上「领先 N 个提交，新分支不含它们」。
- **`--no-sync-check` 关掉的不止是校验。** 它同时关掉起点决议——没有基线可用时，新分支的起点退回执行机仓库当前的 HEAD（很可能是旧的）。只在 cwd 和 `--repo` 根本不是同一个仓库时用它。

稳妥的远程派发姿势：

```bash
git push                                                  # 缺这步必被拒
handoff dispatch --target devbox --repo /remote/path \
  --new-worktree --new-branch feat/x plan.md              # 起点自动取你当前的 HEAD
```

`--base` 仍然可用，用于**刻意**从别处开分支（比如从某个 tag 或更早的提交起）；给了它就以它为准，也不会再提示分叉。

### 回程：wait 自动 fetch，合并是你的决定

任务结束（`completed` / `failed`）时 `wait` 会自动把远程任务分支同步回来（配置 `sync.auto`，`--no-sync` 可关）；也可以随时手动：

```bash
handoff pull <task> --target devbox
```

`pull` 经 ssh 从执行机 fetch 任务分支到**当前工作目录**的仓库。**只 fetch，不 checkout、不合并**——合并进你的主线是审核决定，handoff 不替你做。

去程回程都以 cwd 为准，所以 `dispatch` / `wait` / `pull` 最好都在同一个本地仓库目录里发。`--target` 的机器还需要在配置里配 `user` 字段，否则 `attach` / `pull` 的 ssh 建不起来。

本机派发（不带 `--target`）完全不走这一套：代码本来就在同一台机器上，基线校验直接跳过，`pull` 也会告诉你「本机任务，无需同步」。

## 事件分诊表

`wait` 返回的 JSON 形如 `{"seq":N,"task_id":"...","type":"...","payload":{...}}`。按 `type` 分诊：

| type | 含义 | 你要做的 |
|------|------|---------|
| `permission_request` | executor 要执行一个需授权的操作 | 判断后 `reply <task> --ticket <id> --approve` 或 `--deny --reason "..."` |
| `question` | executor 卡在一个需求取舍上 | `reply <task> --ticket <id> --answer "..."` |
| `completed` | 一轮干完了，任务进 `waiting_review` | 进入审核：`diff` → 决定 `continue` 还是 `done` |
| `failed` | 任务失败落 `failed` | `diff` 看做到哪、`attach` 看现场；要接着干就重新 `dispatch` |
| `delivery_failed` | 裁决落库了但没送到 executor | **`handoff resume <task>`**（详见排障） |
| `stalled` | 看门狗：长时间无产出 | `attach` 或 `show` 判断 executor 是真死还是在长跑；死了就 `stop` |

`ticket_id` 在 payload 里，**一次性消耗**。同一个 ticket 回答两次，第二次 404。

## 审批：批什么，不批什么

`--approve` 批的是**这一次这一条**操作，不是一类操作的长期授权。

**`--deny` 一定要带 `--reason`**。理由会随应答回到模型手里；不给理由，模型只知道「被拒了」，下一步大概率原地再试一次同样的操作，白烧一轮。

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

**`handoff run` 的参数顺序有坑**：handoff 自己的 flag 必须写在 `<task>` **之前**，任务名之后的一切（含 `-v`、`--race`）都原样透传给被执行的命令。

```bash
handoff run --target devbox T1 go test -race ./...   # ✅ --target 在任务名之前
handoff run T1 --target devbox go test ./...         # ❌ --target 会被当成 go test 的参数
```

## 改与收

```bash
handoff continue <task> "把重试次数改成 3，并给这个分支补一条失败用例"
handoff done <task>
```

- `continue` 是**同一会话续接**，executor 的上下文完整保留——不需要在指令里重述前情。
- `continue` 之后任务回到 `running`，要**重新挂 `wait`**。
- `done` 归档任务并回收 executor（停进程、删 managed worktree、清任务目录）。

**`done` 返回成功之前，什么都不要删。** `done` 会因状态不符被拒（409）；如果你已经先手删了任务目录或杀了 tmux，就会留下一个 agentd 记着、但资源已经被你拆掉的孤儿，只能手工补清。顺序永远是：先 `done`，看到 `{"ok":true}` 再谈清理。

`handoff stop <task>` 是另一条出口：主动中止，停 executor、作废挂起工单、任务落 `failed`。任务跑偏了不想再等，用它。

## 会话恢复：从零接管

一个完全没有前文的新会话，两条命令重建现场：

```bash
handoff tasks              # 每行一个任务 JSON：id / name / state / branch
handoff show <task>        # 任务体 + pending_tickets + 最近事件
```

`pending_tickets` 是关键——它是「我还欠哪些没答」的权威清单。把里面每张工单 `reply` 掉，然后按当前 state 决定：`running` → 重新挂 `wait`；`waiting_review` → 进审核。接管后第一次 `wait` 可能重放历史事件（见「cursor 语义」）——照旧以 `show` 为准即可。

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
| `状态码 401` | agentd 在，但 token 对不上 | 同步两边的 token |

退出码：**0 = 能用**（含版本过旧）；**1 = 够不着**。

**红线：查到有 agentd 在跑就复用它，不要起第二个。**
两个 agentd 抢同一份数据目录、同一批 worktree 与 tmux 会话，正是状态机最怕的
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
| `wait` 立刻报错退出 | 401（token 与 agentd 不一致）或 1008（task-id 错） | 看报错原文，修 `~/.handoff/config.yaml` 或核对 id。**别重挂**，它不会自己好 |
| `wait` 一直不返回 | 通常只是还没有事件 | 正常。stderr 的重连日志也正常。加 `--timeout` 兜底 |
| 重挂 `wait` 后立刻吐出旧事件 | cursor 只在 wait 交付时推进；show/reply 不推进，换机接管从 0 起 | 正常。以 `show` 为准处置；历史 ticket 补 reply 404 也正常，跳过 |
| `dispatch` 报「工作区不干净」 | **执行机上**的任务仓库有未提交/未跟踪改动 | 在执行机上提交或 stash 后重试（`--new-worktree` 可绕开主工作区的脏检查，但主仓库仍需可用） |
| `dispatch` 报 400「基线提交在任务仓库中不存在」 | 本地 HEAD 没 push，或执行机 fetch 不到（无凭证/网络不通） | `git push` 后重试；报文里的 fetch stderr 是根因原文。确实是不同仓库才用 `--no-sync-check` |
| 远程派发成功，但 executor 基于旧代码开工 | 改动只 commit 没 push——校验拿 HEAD 比，HEAD 不含未提交改动，会静默通过 | 派发前先 `git push`。起点本身不用管：新分支自动落在你派发时的 HEAD 上，stderr 的「基线」行就是实际起点 |
| `continue` 报 500 / 恢复失败 | executor 进程死了但 agentd 记的运行态是陈的 | 先 `handoff show` 确认状态；`agentd.log` 里搜「恢复结果」看四级恢复阶梯走到哪一级 |
| 任务归档后 tmux 会话还在 | executor 回收失败（事件里会带残留提示） | 按提示 `tmux kill-session -t handoff-<id8>` 手工兜底 |

**日志在哪**（在 executor 所在机器上）：

- `~/.handoff/agentd.log`：agentd 主日志。`HANDOFF_LOG_LEVEL=debug` 可调低级别。
- `~/.handoff/tasks/<完整 task-id>/render.log`：模型回合正文实况，`handoff attach` 的第二个窗口就是 `tail -f` 它。
- 同目录下按 executor 分：opencode 是 `serve.log` / `serve.json`，claude 是 `claude.log` / `claude.json`，grok 是 `serve.log` / `serve.json`。

## 红旗——想到这些说明你在偷懒

| 念头 | 事实 |
|------|------|
| 「我记得这个任务的状态是……」 | 你的会话不是权威。`handoff show` 是。 |
| 「短 id 应该也能认吧」 | 精确匹配，没有前缀补全。一定 404。 |
| 「先删掉任务目录再 done」 | 顺序反了。`done` 可能被拒，先删就留孤儿。 |
| 「ssh 上去 tmux 里手动改一下更快」 | agentd 不知道你改了什么，运行态当场失配。走 CLI。 |
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

## 延伸阅读

这份 skill 只覆盖审核者回路。以下不在范围内，需要时读仓库文档：

- **agentd 部署、`config.yaml` 各段、分级审批链、env 注入**：仓库 `README.md`。
- **三个 executor 的传输形态与差异（SSE / stream-json / ACP）**：`README.md` 的「执行者差异」。
- **架构与协议设计**：`docs/superpowers/specs/2026-08-07-handoff-design.md`。
