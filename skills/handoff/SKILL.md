---
name: handoff
description: 用 handoff CLI 把实现计划派发给独立 executor（opencode / claude / grok）执行，并以审核者身份驱动 dispatch → wait → reply → diff → continue/done 的完整回路。只要涉及「把这个 plan 交给远程开发机跑」「派发任务给 executor 执行」「盯 handoff 任务进度」「任务卡在 running / waiting_review」「reply 返回 502 / continue 报 409 / done 报 404」「新会话接管一个已经在跑的 handoff 任务」，哪怕用户一个字没提「handoff」，也必须先读这份 skill——handoff 的状态机对操作顺序有硬约束，凭印象敲命令会撞 404/409，并把任务卡成没人收的孤儿。
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

`pending_tickets` 是关键——它是「我还欠哪些没答」的权威清单。把里面每张工单 `reply` 掉，然后按当前 state 决定：`running` → 重新挂 `wait`；`waiting_review` → 进审核。

会话崩溃、主动关掉重开、换一台机器接管，三种场景都是这一套。**不要**因为「我不记得这个任务了」就重新 dispatch 一个——先 `tasks` 看有没有。

## 排障

| 症状 | 根因 | 处置 |
|------|------|------|
| 任何命令 404「任务不存在」 | 传了 8 位短 id | 用 `handoff tasks` 取完整 UUID |
| `continue` / `done` 报 409 | 任务不在 `waiting_review` | `handoff show` 看真实状态，按状态机表办 |
| `reply` 返回 502，或收到 `delivery_failed` | 裁决已落库但没送到 executor（executor 半死） | `handoff resume <task>`：幂等重投；executor 还在就继续跑，确已不在则转交审核 |
| `resume` 之后 `reply` 404、`attach` 看不到挂起项 | 工单已被消耗 | 正常。按 `resume` 报告里的结论走 `continue` 或 `done` |
| `wait` 立刻报错退出 | 401（token 与 agentd 不一致）或 1008（task-id 错） | 看报错原文，修 `~/.handoff/config.yaml` 或核对 id。**别重挂**，它不会自己好 |
| `wait` 一直不返回 | 通常只是还没有事件 | 正常。stderr 的重连日志也正常。加 `--timeout` 兜底 |
| `dispatch` 报「工作区不干净」 | 任务仓库有未提交/未跟踪改动 | 提交或 stash 后重试 |
| `continue` 报 500 / 恢复失败 | executor 进程死了但 agentd 记的运行态是陈的 | 先 `handoff show` 确认状态；`agentd.log` 里搜「恢复结果」看四级恢复阶梯走到哪一级 |
| 任务归档后 tmux 会话还在 | executor 回收失败（事件里会带残留提示） | 按提示 `tmux kill-session -t handoff-<id8>` 手工兜底 |

**日志在哪**（在 executor 所在机器上）：

- `~/.handoff/agentd.log`：agentd 主日志。`HANDOFF_LOG_LEVEL=debug` 可调低级别。
- `~/.handoff/tasks/<完整 task-id>/render.log`：模型回合正文实况，`handoff attach` 的第二个窗口就是 `tail -f` 它。
- 同目录下按 executor 分：opencode 是 `serve.log` / `serve.json`，claude 是 `claude.log` / `claude.json`，grok 是 `serve.log` / `serve.json`。

## 远程用法

远程 executor 机全程加 `--target <name>`（地址与 token 从 `~/.handoff/config.yaml` 的 `targets` 段换算）：

```bash
handoff dispatch --target devbox --repo /remote/path --new-worktree plan.md
handoff wait <task> --target devbox --timeout 1h
handoff pull <task> --target devbox      # 把远程任务分支 fetch 到本地（不 checkout）
```

任务结束（`completed` / `failed`）时 `wait` 会自动 `pull`（配置 `sync.auto`，`--no-sync` 可关）。`--target` 的机器需要配 `user` 字段，否则 `attach` / `pull` 的 ssh 建不起来。

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
| 「这个权限请求看起来问题不大」 | 破坏性、不可逆、外部可见的操作一律升级给用户。 |
| 「任务好像不见了，重新 dispatch 一个」 | 先 `handoff tasks`。重复派发会开出第二个 executor 抢同一个仓库。 |

## 延伸阅读

这份 skill 只覆盖审核者回路。以下不在范围内，需要时读仓库文档：

- **agentd 部署、`config.yaml` 各段、分级审批链、env 注入**：仓库 `README.md`。
- **三个 executor 的传输形态与差异（SSE / stream-json / ACP）**：`README.md` 的「执行者差异」。
- **架构与协议设计**：`docs/superpowers/specs/2026-08-07-handoff-design.md`。
