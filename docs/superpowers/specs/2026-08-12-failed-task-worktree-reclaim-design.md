# B77：终态任务 managed worktree 的回收入口（`handoff reclaim`）

> 日期：2026-08-12　　来源：backlog B77　　状态：设计定稿，待写实现计划

## 1. 问题

清理 devbox 上的死分支时，`git push --delete feat/b73-proc-fence-r2` 被拒：

```
! [remote rejected] (branch is currently checked out)
```

占用者是失败任务 `2c58bbb7` 的 managed worktree `/Users/sycm/.handoff/worktrees/2c58bbb7`。

### 1.1 原始判断与实际机制的偏差

B77 登记时记的机制是「`failed` 是终态但不回收资源，`done` 和 `stop` 都硬要求非终态，
于是没有任何一条 CLI 出口能回收失败任务的 worktree」。**通读代码后这条不成立**：

- `Stop` **会**清 managed worktree（`manager.go:1191`，B15/P2-2 补的），失败才降级。
- 派发期三处落 `failed`（写分支名 / 写 plan 摘要 / adapter start 失败）由
  `compensateWorkspace` 的 defer 兜底删树、切回原 ref、必要时删空分支（`manager.go:763`）。
- executor 死掉**根本不落 `failed` 状态**：`handleResult(!OK)` 与 `reconcileExecutorGone`
  追加的是 `failed` **事件**，状态迁的是 `waiting_review`（`manager.go:2473`、
  `reconcile.go:162`）。那类任务可审阅，走 `done` 就能正常回收。

也就是说，能走到 `failed` **状态**的只有 `stop` 和派发期三处，这两类都自带清理。

### 1.2 真正的洞

清理是**一次性 best-effort**：失败就降级成一条 progress 事件（文案「请手动
`git worktree remove`」），**此后没有任何入口能重试**。

而失败的大概率原因是 `RemoveManagedWorktree` 用的是裸 `git worktree remove`、
**不带 `--force`**（`workspace.go:589`），git 对有未提交改动或未跟踪文件的工作树
直接拒绝。任务失败时 executor 往往正干到一半，树里留着改动——这同时解释了
「为什么只漏了一部分任务而不是全部」：干净的树删得掉，脏的删不掉。

**这一条是推断，不是实证**：devbox 不可达，`stop` 当时的失败事件也没留存。
真机烟测（§8）的第一步就是对账验证它。

### 1.3 「40 个任务里 15 个 failed 且 `worktree_managed=true`」这个规模数字不可信

`worktree_managed` 不在 `SetTaskField` 白名单里（`store.go:388` 只有
branch / executor_session / plan_summary / done_note），**删成功从不回写该字段**。
stop 成功清完树的任务，记录里照样是 `worktree_managed=true`。

15 这个数里有多少是真残留无从判断；**被实证的只有 `2c58bbb7` 一个**（push 被拒是硬证据）。
本设计因此不依赖该字段，改问 git 要地面真相（§3）。

## 2. 目标与非目标

### 目标

给终态任务一条**可重复执行**的 managed worktree 回收入口，并让既有的清理失败提示
指向它。

### 非目标（本条明确不做）

| 不做 | 理由 |
|---|---|
| 删任务分支 | 分支是审核者的工作成果，`stop` 已确立「不删分支」。且删除判据是 `NewBranchTip`（`workspace.go:554`），而 B76 正在质疑该判据在 `--new-branch` + `--base <分支名>` 下是否可信——纳入即把 B77 绑死在 B76 的结论上 |
| 删任务目录 `<dataDir>/tasks/<id>/` | `out.jsonl` / `render.log` / `shim.log` / `proc.json` 是失败任务的排查素材，而失败任务恰恰最可能要回头查 |
| 回写 `worktree_managed` / `work_dir` 字段 | 地面真相改问 git（§3），字段不可信这件事被绕开而非修补。修它是独立的一条 |
| 改任务状态 | 见 §3.1 |
| 自动后台重试回收 | 对主要漏法（脏树）无效——重试撞的是同一堵 git 墙；后台无声删目录也是风险面 |

## 3. 设计

### 3.1 边界与不变量

`reclaim` 是**纯资源动作**，不参与任务生命周期：

1. **不改状态、不追加状态迁移事件**。回收前后 `handoff show` 看到的状态一致。
   这是它能与 `stop`/`done` 共存的前提——B63 的终态收口管工单，本条管磁盘，互不重叠。
2. **只认终态任务**（`completed` / `failed`）。非终态一律 409：删运行中任务的树
   等于抽它脚下。`pending` 任务被 agentd 崩溃遗弃不归它管——`stop` 本就能处理非终态。
3. **只认 `Managed=true` 的树**。用户自带工作树是审核者资产，与 `done` / `stop` /
   `compensateWorkspace` 三处已确立的纪律一致。
4. **幂等**。树已不在则报「无残留」并成功退出。一条重试入口如果重试第二次会报错，
   它就不是重试入口。

### 3.2 地面真相：问 git，不问库

「谁还占着树」不读 `worktree_managed`（§1.3）。判据是两段实测：

1. **在不在**：按任务 `repo_path` 分组，每仓库跑一次 `git worktree list --porcelain`，
   用任务 `work_dir` 匹配。
2. **脏不脏**：对还在的树跑 `git status --porcelain`，输出非空即脏。
   **未跟踪文件也算脏**——判据必须与 `git worktree remove` 自身的拒绝条件对齐，
   否则会出现「我说是净的，删的时候被拒了」。

### 3.3 第三态：`prunable`

目录被手工 `rm -rf` 掉、git 元数据还挂着的工作树，`git worktree list --porcelain`
会打 `prunable` 标记，对它跑 `remove` 同样失败。

这一态占的不是磁盘而是**分支占用**——它照样能让 `push --delete` 被拒，与 §1 撞上的
症状完全一致。因此必须纳入：列表要认它，回收时走 `git worktree prune` 而非 `remove`。

### 3.4 判不出要如实说

仓库路径不存在、或不是 git 仓库时，相关任务报**判不出**，不得从列表中消失。
与 B70「不支持 footprinter 的 adapter 必须留 nil，不猜 0」同一条纪律。

### 3.5 状态判定表

| 态 | 判据 | 回收动作 |
|---|---|---|
| 净 | 在 worktree list 中，`git status --porcelain` 为空 | `git worktree remove` |
| 脏 | 在 worktree list 中，`git status --porcelain` 非空 | 默认拒绝；`--force` 时 `git worktree remove --force` |
| 元数据残留 | worktree list 标 `prunable` | `git worktree prune` |
| 无残留 | 不在 worktree list 中 | 无动作，幂等成功 |
| 判不出 | 仓库不可达 / 非 git 仓库 | 无动作。**列表**里标出该行；**单任务回收**按 409 拒绝，错误文本点名仓库路径与真因 |

「判不出」在两条路径上处置不同，是因为它们的失败代价不同：列表少一行结论仍然有用，
而回收若把「判不出」当成「无残留」静默退 0，就会让人以为已经清干净了。

## 4. CLI 契约

```bash
handoff reclaim [--target <名字>] [--json]          # 列
handoff reclaim <task-id> [--target <名字>] [--force]  # 收
```

`--json` 只服务列表形态（供脚本消费清单）；回收是单动作，人读输出足够，不加 `--json`。

### 4.1 无参 = 列

```
残留     3 个终态任务仍占着 managed worktree（共体检 40 个）
  2c58bbb7  b73 围栏 r2  failed     脏（4 项改动）  ~/.handoff/worktrees/2c58bbb7
  a1b2c3d4  w4 时间线    completed  净              ~/.handoff/worktrees/a1b2c3d4
  ef012345  b69 足迹     failed     元数据残留      目录已不存在，需 prune
  9a8b7c6d  b52 子会话   failed     ⚠ 判不出        仓库不可达 /Users/sycm/gone
```

无残留时一行收口：`残留     无（共体检 40 个任务）`。

**「判不出」的行永远显示**，不因「看起来没占」被过滤——直接沿用
`renderFootprint` 已想清楚的规矩（`cmd/footprint.go:70`）。

列表**恒退 0**：它是一份报告，「有残留」是它的正常结论而非失败。只有拿不到列表
（连不上、401、5xx）才退非零。

### 4.2 带 id = 收

净树成功（退 0）：

```
已回收   2c58bbb7 的 managed worktree
工作树   ~/.handoff/worktrees/2c58bbb7（已删除）
提示     任务分支 feat/b73-proc-fence-r2 保留——reclaim 不删分支
```

脏树拒绝（退 1）：

```
拒绝     工作树有未提交改动，未回收
工作树   ~/.handoff/worktrees/2c58bbb7
改动     M  internal/prochost/fence.go
         M  internal/agentd/manager.go
         ?? scratch/probe.log
         （共 4 项）
处置     确认可丢弃后重跑：handoff reclaim 2c58bbb7 --force
```

已不存在（退 0，幂等）：

```
无残留   2c58bbb7 的 managed worktree 已不在，无需回收
```

`--force` 强删时**仍打印被丢弃的文件清单**（删后打印），让日志留下「丢了什么」的记录。

## 5. agentd 端点

| 方法 | 路径 | 用途 |
|---|---|---|
| GET | `/api/reclaim` | 列，纯查询，与 `/api/footprint` 同构 |
| POST | `/api/tasks/{id}/reclaim` | 收，body `{"force": bool}`，遵循 `/api/tasks/{id}/<动词>` 惯例 |

状态码：

- **404** 任务不存在
- **409** 非终态，错误文本点名当前状态
- **409** 脏树未带 `force`，响应体带**结构化**脏文件清单 `[{status, path}]`——
  不是预渲染文本，渲染是 CLI 的事
- **409** 仓库不可达 / 非 git 仓库（判不出，见 §3.5）
- **409** 工作区非 managed（`Managed=false`，见 §3.1 第 3 条）
- **200** 成功，体带 `{"removed": bool, "action": "removed|pruned|already_absent"}`

4xx 走 WARN 不走 ERROR（B11 已定）。

**四种 409 共用一个状态码，因此响应体必须带机器可判的 `reason`**：
`not_terminal` / `dirty` / `repo_unreachable` / `not_managed`。CLI 按 `reason` 分派渲染，
**不解析中文文案**——文案是给人看的，会改；`reason` 是契约，不会。

### 5.1 404 消歧（必须专门处理）

老 agentd 没有该端点，`POST` 打过去也是 404——与「任务不存在」撞码。照直翻译会对着
一台好机器报「任务 2c58bbb7 不存在」，把人引向完全错误的方向，正是 B64 那类缺陷。

**处置**：收到 404 时补打一次 `GET /api/reclaim`。

- 它也 404 → 对端过旧，返回 `ErrReclaimUnsupported`，CLI 打印降级提示并**退 0**
  （与 `ErrStatusUnsupported` / `ErrFootprintUnsupported` 同款，`client.go:259`）
- 它 200 → 任务是真不存在，如实报错

只在错误路径上多一次往返，换一个不靠猜的结论。

> 备选方案：把动作端点挪到 `POST /api/reclaim`（body 带 task_id）可根治歧义，
> 代价是破坏 `/api/tasks/{id}/<动词>` 的既有惯例（done/stop/reply/continue/resume/run
> 六个端点都在用）。取舍结论是保惯例 + 补一次探测。

## 6. 与既有降级路径接线

三个清理降级点的提示文案统一改为指向新入口：

| 位置 | 现文案 | 新文案 |
|---|---|---|
| `Done`（`manager.go:1102`） | `worktree 清理失败：<真因>，请手动 git worktree remove` | `worktree 清理失败：<真因>，可重试：handoff reclaim <id8>` |
| `Stop`（`manager.go:1191`） | 同上 | 同上 |
| `compensateWorkspace`（`manager.go:775`，仅日志） | `补偿清理 managed worktree 失败，保留分支待查` | 追加 `retry` 字段给出同一条命令 |

`2c58bbb7` 无声漏掉的直接原因就是这条提示没给出真出路——手动 `remove` 撞的是同一堵墙。
**入口存在但没人知道，与入口不存在等价**，这是本条目性价比最高的一处改动。

## 7. 错误处理纪律

- **所有 git 调用带超时**，走既有 `WorkspaceGitTimeout`，不允许裸 `context.Background()`（B10）。
- **列表容忍局部失败**：单个仓库不可达不拖垮整张表，该行标「判不出」继续。
  列表的核心价值正是在环境已不健康时还能用。
- **回收不降级**：与列表相反，单任务回收失败就是失败，如实报真因并退非零。
  它是人主动发起的一次性动作，吞错误没有好处。
- **动手前重读状态**：`failed → running` 是合法迁移，任务可能在列表之后被重新派发。
  终态判定不能只在列表那一刻做，`remove` 前必须按最新快照再确认一次。

## 8. 验证策略

### 8.1 单测（真 git 仓库集成，不 mock git；B8 已有先例）

| 用例 | 期望 |
|---|---|
| 净树回收 | `action=removed`，树确实不在了 |
| 脏树无 `--force` | 409 `reason=dirty`，清单里确有那几个文件 |
| 脏树带 `--force` | `action=removed` |
| `prunable` 条目 | 走 `prune` 而非 `remove` |
| 树已不在 | `action=already_absent` 且退 0（幂等的定义，必须钉死） |
| 非终态任务 | 409 `reason=not_terminal`，错误文本点名当前状态 |
| `Managed=false` | 409 `reason=not_managed` |
| 仓库不可达（单任务回收） | 409 `reason=repo_unreachable`，**不得**被当成 `already_absent` |
| 仓库不可达（列表） | 该行标「判不出」，其余行照常返回 |

### 8.2 CLI 侧：404 消歧两条路

| 场景 | 期望 |
|---|---|
| 老 agentd（两个端点都 404） | 降级提示，退 0 |
| 新 agentd + 不存在的 id | 报「任务不存在」，退非零 |

两条走同一个 HTTP 码，用例分不开就等于没修。

### 8.3 真机烟测（devbox）

1. 跑 `handoff reclaim` 列表，与实际 `git worktree list --porcelain` 逐条对账
   ——**这一步顺带结掉「那 15 个到底漏没漏」的悬案，并验证 §1.2 的脏树推断**
2. 挑一个净的回收，验 `removed`
3. 挑一个脏的，验拒绝路径与清单
4. `--force` 收掉 `2c58bbb7`，验证 `push --delete` 随即放行

### 8.4 日志与注释自检（`instrumenting-code`）

关键节点日志：进入/退出、每个任务的判定结论（在/不在/脏/净/prunable/判不出）、
git 调用及其 stderr、拒绝原因。新文件写职责与边界头注释，导出方法写参数/返回/注意。
