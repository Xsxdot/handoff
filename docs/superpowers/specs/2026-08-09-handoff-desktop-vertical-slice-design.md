# Handoff Desktop 第二阶段纵切 —— 设计

> 日期：2026-08-09
> 状态：设计已逐节确认，等待规格复核
> 目标代码基线：Orca Desktop + Handoff agentd
> 视觉基准：`prototypes/desktop-console/` 及本轮确认的三栏工作台

## 1. 背景与目标

Handoff 已具备任务派发、执行者 adapter、事件、工单、审批、回答、继续、停止等后端能力；
原型已确认桌面控制台的产品形态。第二阶段不再继续做静态原型，而是在 Orca 真实桌面代码和
Handoff agentd 之间跑通一条可验证的纵切。

产品本质是一个**常开的 AI 开发控制台**：用户在左侧看到所有项目，以及项目在本机和至多一台
远端开发机上的主目录、工作树与正在运行的 handoff 任务；选择目录后，中间工作台和右侧文件树
共同切换到该目录；用户可以打开普通终端、编辑文件、打开浏览器预览，也可以进入统一的结构化
任务 TUI，查看进度并完成审批、回答、继续和停止。

第二阶段的目标不是“把 Orca 全部改完”，而是证明以下架构在真实环境中成立：

1. 桌面端只连接本机 agentd；本机 agentd 是机器注册、项目目录和跨机器任务索引的控制面。
2. 本机和远端开发资源都通过 agentd 访问，不依赖 Orca SSH，也不走本地 Node 特殊捷径。
3. 所有执行者统一输出结构化任务现场；桌面端不提供 OpenCode 原生 TUI 产品路径。
4. 本机和一台真实远端开发机上的项目、文件、终端、浏览器和任务闭环可以端到端工作。
5. 纵切通过后，第三阶段才按依赖图删除 Orca 不需要的业务代码并极度瘦身。

## 2. 已确认的核心决策

| 决策点 | 结论 |
|---|---|
| 桌面连接拓扑 | 桌面端只连接本机 agentd，不直连任何远端 agentd |
| 远端连接所有者 | 本机 agentd 持有开发机注册表、凭据、连接和同步 cursor |
| 项目位置约束 | 每个项目：本机 Location `0..1`，远端 Location `0..1`，两者合计至少一个 |
| 远端选择 | 一个项目最多选择一台远端开发机；agentd 全局可以登记多台机器 |
| Orca SSH | 完全不接入：不读 SSH host、SFTP、SSH PTY、SSH Git、连接状态或远端 Worktree 模型 |
| 项目事实源 | Project、ProjectLocation 及工作区归属由本机 agentd 持久化 |
| 资源事实源 | 文件、Git、PTY、执行者会话、任务事件和完整输出由所属机器 agentd 负责 |
| 桌面持久化 | 只保存标签、分屏、选中项等纯 UI 状态，不保存项目/机器/任务业务真相 |
| 任务身份 | `machine_id + workspace_id`；`work_dir` 保留为执行时路径快照；不重复保存 project/location id |
| 未登记目录 | dispatch 允许执行；机器 agentd 创建 detached Workspace，添加项目后自动归并 |
| 任务 UI | 所有执行者统一使用桌面结构化 TUI；TaskTUI 不是 PTY |
| OpenCode 原生 TUI | 不进入桌面产品和设置页；`handoff attach` 仅保留为 CLI 诊断/应急入口 |
| Orca 集成方式 | 独立 `Handoff Workbench`，复用渲染能力，不接入 Orca 旧项目/SSH 状态树 |
| 断线语义 | 保留最后的列表元数据；文件、任务现场和所有操作均为不可用，不称为“只读” |
| 多桌面实例 | 任务生命周期属于 agentd；多个桌面可同时查看和操作，命令由服务端幂等与版本校验裁决 |

## 3. 本阶段范围

### 3.1 必须完成

1. 独立 Handoff Workbench：项目树、工作区级标签组、右侧文件树、统一 TaskTUI。
2. 本机 agentd 控制面：机器注册表读取、项目/Location/Workspace 持久化、远端 peer、
   durable outbox、全局投影与推送。
3. 最小项目创建：本机可选、至多一台远端可选、至少一个；每个 Location 支持已有路径或
   Git clone；只有本机已有路径支持 Finder，远端只能粘贴路径。
4. 工作区资源：主目录、worktree、分支发现与推送；文件浏览、读取、编辑、保存和冲突保护；
   Git 基础状态。
5. 普通终端：正确 cwd、输入输出、resize、左右分屏、重连和显式结束。
6. 浏览器：复用 Orca BrowserPane；远端 localhost 由 agentd preview-port 代理，不走 SSH 隧道。
7. 任务闭环：任务创建推送、结构化事件重放、工具动作、Diff、Todo、审批、回答、继续、停止、
   failed/stalled/completed 等终态。
8. 真实环境验收：macOS 桌面、本机 agentd、一台真实远端开发机 agentd、一次真实 OpenCode
   任务；Claude Code 与 Grok 至少完成协议 fixture 回放。

### 3.2 明确不做

- 独立任务看板页面。
- 完整设置页落地。
- 开发机、env、执行者、审批者的完整管理 UI。
- agentd 安装、配对和自动升级流程。
- 高级 Git、PR、Issue、代码托管平台集成。
- 移动端、云同步、自动化、插件与 Orca 账号体系。
- Windows/Linux 桌面端验收。
- 第二阶段内删除 Orca 旧业务代码。
- OpenCode 原生 TUI 的桌面入口或任务级/全局选择开关。

## 4. 产品信息架构

### 4.1 三栏工作台

主工作台由三栏构成：

1. **左侧项目树**：`Project → Machine/Location → main/worktree → handoff task`。
2. **中间标签工作区**：Terminal、Editor、Browser、TaskTUI 四类标签，支持左右分屏。
3. **右侧文件树**：根始终是左侧当前选中的 Workspace。

左侧项目与机器行右侧显示概览计数，例如工作区数、运行中任务数、待用户处理数；这些计数由
本机 agentd 的投影提供，不由 renderer 遍历列表临时推导。

### 4.2 当前工作区与标签组

- 单击主目录或 worktree 后，它成为唯一的“当前工作区”。
- 中间 breadcrumb、操作栏、标签组和右侧文件根同步切换。
- 每个 Workspace 拥有自己的标签组和 split tree；切换 Workspace 不关闭旧标签，返回时恢复。
- 新终端默认 cwd 是当前 Workspace 的真实路径。
- 点击文件会在当前 Workspace 的标签组打开或聚焦 Editor 标签。
- 点击任务会先切到任务所属 Workspace，再打开或聚焦该任务唯一的 TaskTUI 标签。
- 一个任务在同一桌面实例中只有一个 TaskTUI 标签；它可以被分屏查看，但不能产生两套任务状态。

### 4.3 断线界面

- 远端断开后，左侧仍显示最后已知的 Project、Workspace 和 Task 摘要，并标记“已断开/不可用”。
- 选中该 Workspace 时，中间与右侧展示统一的不可用遮罩，不继续读取缓存文件或任务详情。
- 已有标签和布局保留，以便重连后恢复；不把旧终端标签静默绑定到新 Shell。
- 本机 Location 继续正常工作。

## 5. 总体架构与事实边界

```text
┌─────────────────────┐       versioned IPC       ┌─────────────────────────┐
│ Electron Renderer   │  ⇄  Electron Main/Preload │ Local AgentdClient       │
│ Handoff Workbench   │                            │ token / REST / WS / retry│
└─────────────────────┘                            └────────────┬────────────┘
                                                               │ one authenticated
                                                               │ local connection
                                                    ┌──────────▼───────────┐
                                                    │ Local agentd         │
                                                    │ control plane        │
                                                    │ registry/projections │
                                                    └──────────┬───────────┘
                                                               │ peer protocol by
                                                               │ machine_id
                                                    ┌──────────▼───────────┐
                                                    │ Remote agentd        │
                                                    │ machine authority    │
                                                    │ fs/git/pty/task/port │
                                                    └──────────────────────┘
```

### 5.1 本机 agentd 的职责

- 持久化 Machine 注册表和受保护凭据引用。
- 持久化 Project、ProjectLocation 和 Workspace 的项目归属。
- 维护每台远端的连接、capability、`last_machine_seq` 与同步状态。
- 镜像跨机器 Task 摘要、待处理数量和最后已知状态。
- 把各机器资源事件归并为单调递增的 `control_revision`。
- 为桌面提供唯一的 bootstrap、全局流、资源代理和命令入口。
- 远端断开时拒绝文件、PTY、TaskTUI、artifact 和 preview-port 操作。

### 5.2 所属机器 agentd 的职责

- 对本机目录和相对路径进行授权、读写、watch、搜索和版本冲突校验。
- 扫描 Git worktree、refs、HEAD 和基础状态。
- 创建和持有普通 Shell PTY，会话重连与有序输出。
- 拉起执行者、持久化任务状态、Ticket、稀疏生命周期事件和结构化 TaskFrame。
- 保存完整命令输出、Diff 等 artifact，并按引用提供读取。
- 提供受限 preview-port，把指定本机 loopback 端口代理给本机 agentd。
- 生成 durable `machine_events`，供控制面断线续传。

本机开发资源也必须经过同一套 machine-authority 服务。实现可以在同一 agentd 进程内走直接调用，
但不得在 Electron 主进程里另走 Node `fs`、本地 PTY 或 Git 捷径，否则本地与远端语义会再次分叉。

## 6. 数据模型

### 6.1 Machine

```text
Machine {
  id                 stable UUID
  display_name
  kind               local | remote
  endpoint
  secret_ref         renderer 永不可见
  protocol_version
  capabilities
  status             connecting | reconciling | connected | unavailable | incompatible
  last_seen_at
}
```

- 本机 agentd 启动时确保存在一个稳定的 local Machine。
- 全局可以登记多台 remote Machine；每个 ProjectLocation 只选择其中一台。
- 第二阶段桌面只读取机器列表并用于项目创建，不实现完整管理 UI。

### 6.2 Project 与 ProjectLocation

```text
Project {
  id
  name
  git_identity?      已知时保存规范化 remote identity
  created_at
  updated_at
}

ProjectLocation {
  id
  project_id
  machine_id
  main_workspace_id
  source             existing_path | git_clone
  git_url?
  created_at
  updated_at
}
```

约束：

- 每个 Project 至少一个 ProjectLocation。
- `(project_id, machine.kind=local)` 最多一个。
- `(project_id, machine.kind=remote)` 最多一个。
- 如果本机和远端目录都能识别出 Git remote，规范化 identity 不一致时拒绝把它们加入同一 Project。
- 一个 Location 的 main Workspace 唯一；其余 Workspace 来自 worktree 扫描。

### 6.3 Workspace

```text
Workspace {
  id                 机器 agentd 生成的全局唯一 UUID
  machine_id
  location_id?       null 表示 detached，归属由本机控制面维护
  kind               main | worktree | detached
  path
  repo_identity?
  git_common_dir?
  branch?
  head_oid?
  availability
  last_scanned_at
}
```

- 机器 agentd 为已见过的路径维护稳定 Workspace ID，任务永远引用 ID 而不是靠路径关联。
- CLI 对项目外路径 dispatch 时创建 detached Workspace；任务立即可见于“未登记工作区任务”。
- 添加项目或 Reconcile 后，本机控制面按 `machine_id + repo_identity/git_common_dir + path` 归并，
  只更新 `location_id/kind`，不重写 Workspace ID，也不改 Task。
- 路径仍是资源描述和执行快照，不是跨层主键。

### 6.4 GitRef

```text
GitRef {
  location_id
  name
  head_oid
  checked_out_workspace_ids[]
}
```

分支清单属于 ProjectLocation，因为本机 clone 与远端 clone 的 refs 可能不同。单独创建分支时只产生
GitRef，不在左侧新增目录；只有真实 worktree 出现时才新增 Workspace 行。

### 6.5 Task

现有 Task 增加：

```text
machine_id
workspace_id
```

并继续保留现有 `work_dir`，作为该任务执行时的路径快照。Task 不保存 `project_id` 或
`location_id`；展示归属始终由 Workspace 关联推导。

TaskState 增加 `stalled`：它表示机器仍可达，但 executor 失联、无心跳或恢复凭据不足，需要用户
选择 resume/stop；机器整体断开只改变 Machine availability，不把 Task 迁移到 stalled。

兼容迁移：旧 Task 在首次迁移时绑定 local Machine，并按 `Task.Workdir()` 创建或复用 detached
Workspace。现有 `repo_path`、`target` 等字段暂时保留，保证旧 CLI 和历史数据库可读，但桌面新逻辑
不以它们作为权威归属。

### 6.6 两类任务事件

现有稀疏 `proto.Event` 继续服务 CLI `wait/show/attach` 和任务生命周期，不把高频 UI 事件直接塞入
这条唤醒流，避免新 activity/message 让旧 `wait` 命令误唤醒。

新增结构化 `TaskFrame`：

```text
TaskFrame {
  event_id
  task_id
  task_seq          每任务单调递增
  kind              message | activity | file_change | todo_snapshot |
                    interaction | task_state
  payload_version
  payload
  artifact_ref?
  created_at
}
```

TaskFrame 是 TaskTUI 的唯一事实输入；renderer 通过 snapshot + frame reducer 构建界面。

## 7. 项目创建

### 7.1 表单语义

- 本机 Location 与远端 Location 至少选择一个。
- 远端下拉来自本机 agentd 的 Machine 注册表，只能选一台。
- 每个已选择 Location 单独选择：`已有目录` 或 `Git clone`。
- 本机已有目录：由 Electron Main 弹 Finder，renderer 只得到结果并交给本机 agentd 校验。
- 远端已有目录：只能粘贴绝对路径，不提供 Finder/远端目录选择器。
- Git clone：填写 Git URL，并可编辑 clone path；默认预填
  `~/.handoff/<repository-name>`。
- 创建前由目标机器 agentd 校验路径、目录可访问性、Git 状态和目标 path 冲突。

### 7.2 长操作与失败

项目创建和 clone 使用 durable Operation：

```text
Operation {
  operation_id       command_id，同时作为幂等键
  kind               create_project | clone | register_path | create_worktree
  state              pending | running | partial | succeeded | failed
  targets[]
  progress
  result/error
}
```

- HTTP/WS 断开不取消已提交 Operation；重连后按 operation_id 查询或继续接收进度。
- 重试同一 operation_id 不重复 clone、创建 worktree 或注册 Project。
- 多 Location 创建发生部分成功时，不自动删除已经 clone 的目录。Operation 标记 `partial`，保留
  精确结果，允许重试失败目标或显式接受成功的 Location；任何清理必须由用户明确触发。
- Project/ProjectLocation 投影更新与 `control_event` 追加在同一事务提交。

## 8. 工作树、分支和任务如何推送

### 8.1 机器事件 outbox

所属机器 agentd 在资源变化落库的同一事务中追加：

```text
MachineEvent {
  machine_seq        每机器单调递增
  event_id
  kind
  resource_id
  payload
  created_at
}
```

核心事件：

| 事件 | 触发 |
|---|---|
| `workspace.upsert` | 新 worktree、路径/分支/HEAD/availability 更新 |
| `workspace.remove` | 确认 worktree 已不存在 |
| `git_ref.upsert` / `git_ref.remove` | 分支新增、移动、删除 |
| `task.upsert` | Task 创建及 state/executor/attention 摘要变化 |
| `task.remove` | 明确归档策略要求从活跃投影移除；历史仍保留 |
| `pty.upsert` / `pty.exit` | Terminal session 生命周期 |
| `operation.upsert` | clone、worktree 创建等长操作进度 |

Task 创建必须先落所属机器数据库，再启动 adapter，因此 `task.upsert(pending)` 先于
`task.upsert(running/failed)`；即使执行者启动失败，桌面也能看到失败现场。

### 8.2 外部 Git 变化

Git watcher 只作为“尽快扫描”的提示，不能作为事实源。Reconciler 使用：

- `git worktree list --porcelain` 确认 worktree。
- `git for-each-ref refs/heads` 确认本地分支。
- 当前 HEAD、common dir 和规范化 repo identity 计算变化。

以下时机必须完整 Reconcile：

1. agentd 启动；
2. 远端 peer 重连并补完 machine events 后；
3. watcher 报告 Git 元数据变化；
4. 周期性兜底扫描。

### 8.3 控制面归并

- 本机 agentd 为每台远端保存 `last_machine_seq`。
- 重连从 cursor 后补拉，按 `(machine_id, machine_seq)` 幂等去重。
- 更新本机 Workspace/GitRef/Task 投影与追加 `ControlEvent` 在同一事务中完成。
- 本机资源走同一事件入口，不另做 renderer 特判。
- 每条 ControlEvent 获得全局单调 `control_revision`。

## 9. 桌面协议

现有 `/api/tasks`、`/ws/events` 等 CLI 契约保持兼容。桌面新增版本化 `/v1` 控制面契约；实现时可
按现有 Server 路由风格组织，但语义必须满足本节。

### 9.1 Bootstrap 与全局摘要流

```text
GET /v1/bootstrap
→ machines, projects, locations, workspaces, git_refs,
  active_task_summaries, operations, control_revision

WS /v1/stream?after=<control_revision>
→ MachineEvent/Project/Location/Workspace/GitRef/TaskSummary/Operation 增量
```

bootstrap 返回快照对应的 revision `R`；桌面随后订阅 `after=R`。快照生成期间发生的变化在 durable
control log 中补发，因此没有“先取列表还是先连 WS”的竞态。renderer 按稳定 ID 执行
upsert/remove，重复事件无副作用。

全局流只传任务摘要，不传所有任务的完整 TaskFrame，避免常开桌面被大量后台输出淹没。

### 9.2 当前任务详情流

```text
GET /v1/tasks/{task_id}/session
→ task projection, pending interactions, render snapshot, through_task_seq

WS /v1/tasks/{task_id}/frames?after=<task_seq>
→ TaskFrame
```

- 只有被打开的 TaskTUI 订阅详情流。
- TaskFrame 断线后从最后 task_seq 重放。
- 如果 cursor 已超出保留窗口，服务端返回 `CURSOR_EXPIRED`；客户端重新取 session snapshot，
  用 `through_task_seq` 原子替换 reducer，再订阅后续 frame，不拼残缺历史。
- assistant delta 在 adapter/recorder 侧按时间或大小批量落盘，避免一个 token 一行。
- 完整命令输出、Diff 和大附件不进入 frame payload；frame 保存摘要与 `artifact_ref`，展开时按需代理。

### 9.3 任务命令

```text
POST /v1/tasks/{task_id}/commands
{
  command_id,
  kind: approve | reject | answer | continue | stop | resume,
  ticket_id?,
  expected_version?,
  payload?
}
```

- `command_id` 是幂等键；网络重试或多个桌面不会重复调用 executor。
- Ticket 操作携带 expected version；已被其他窗口处理时返回 `409 COMMAND_CONFLICT` 和权威结果。
- renderer 不乐观改变 Task/Ticket 状态；命令接受后等待 TaskFrame/TaskSummary 回流。
- answer/continue 的文本原样传递，不在桌面端加工。

### 9.4 文件、Git、PTY 和 Preview

所有 API 使用 `workspace_id + relative_path`，renderer 不向远端提交任意绝对路径。所属机器 agentd
解析 Workspace 根、realpath 并保证目标位于授权边界内。

文件读取返回 `version`；保存携带 `if_match=version`，不一致返回 `409 VERSION_CONFLICT`。写入采用
同目录临时文件 + 原子 rename；冲突时 UI 提供重载、Diff、另存或人工合并，不静默覆盖。

PTY 以 `terminal_session_id + incarnation` 标识，输出有单调 seq。重连时：

- 会话仍存活：按 seq/快照恢复。
- 会话已不存在：旧标签显示“会话已结束”，只能显式新建终端。
- 不允许把旧 session ID 偷偷指向新 Shell。

Preview 由本机 agentd 创建短期本机 loopback URL，绑定 `machine_id + remote_port + session_id`；
远端 agentd 只允许代理目标机器的 loopback 端口。机器断开时代理关闭，Browser 标签保留但显示不可用。

## 10. 统一结构化 TaskTUI

### 10.1 六类 frame

| kind | 内容 |
|---|---|
| `message` | 面向用户公开的 assistant 文本增量；不包含隐藏思维链 |
| `activity` | search/read/edit/command/test 的 started/updated/completed/failed，含稳定 activity id |
| `file_change` | 路径、create/modify/delete、增删统计、diff_ref |
| `todo_snapshot` | 当前步骤与 todo/running/done/blocked 状态的权威快照 |
| `interaction` | permission/question 的 requested/resolved 生命周期及 ticket version |
| `task_state` | pending/running/waiting/completed/failed/stalled 及可行动原因 |

Activity 的完整输出通过 artifact_ref 读取；列表先显示命令、摘要、耗时和结果，用户展开时再取全文。

### 10.2 Adapter 边界

OpenCode、Claude Code、Grok 的原始事件在 machine agentd 内由各自 adapter 转成统一 ExecutorEvent，
再由 Task Recorder 持久化 TaskFrame。桌面端禁止根据 executor 名做事件解析或 UI 分支。

现有 permission/question/result 控制语义继续经过 Manager、Ticket 与分级审批链；TaskFrame 是这些
权威事实的显示投影，不另建第二套审批状态机。

第二阶段：

- OpenCode 用真实任务验证全部主路径。
- Claude Code、Grok 使用捕获的 fixture 验证六类 frame 归一化与 replay。
- 不展示 `thinking_delta`、reasoning part 或其他隐藏思维链。
- `handoff attach` 仍可作为 CLI 诊断旧任务/原始进程，但桌面没有入口、开关或自动 fallback。

## 11. Orca 改造边界

### 11.1 采用独立 Handoff Workbench

不把 agentd 做成 Orca `local | ssh` 旁边的第三种 ExecutionHost。该做法短期看似复用更多，但会让
agentd 判断扩散进 Worktree、PTY、文件、Git、tab hydration 和巨型 sidebar 组件，第三阶段很难拆除。

新增有边界的功能模块：

```text
Electron Main / Preload
  handoff/AgentdClient
  handoff/CatalogClient
  handoff/FilesystemAdapter
  handoff/GitAdapter
  handoff/PtyAdapter
  handoff/TaskClient
  handoff/PreviewClient
  window.handoff (typed narrow IPC)

Renderer
  features/handoff/catalog-store
  features/handoff/workspace-session-store
  features/handoff/task-session-store
  features/handoff/components/ProjectTree
  features/handoff/components/Workbench
  features/handoff/components/FileTree
  features/handoff/components/TaskTUI
```

具体路径由实施计划结合 Orca 现有目录约定最终落定，但模块依赖方向不得改变。

### 11.2 复用什么

- xterm 渲染、输入、键盘/剪贴板、尺寸适配与可提取的 split tree 算法。
- Monaco、语言识别、Editor/Diff 的纯渲染与编辑交互。
- BrowserPane 的 webview、地址栏、查找与标签渲染。
- 主题、图标、弹窗、菜单、快捷键、错误边界和通用基础组件。
- Orca provider contract 中已经验证的文件、Git、PTY 语义可以作为接口参考。

### 11.3 不复用什么

- Orca Project、Repo、ProjectHostSetup、Worktree 的业务持久化。
- SSH host、SFTP、SSH PTY、SSH Git、SSH port-forward 和连接 generation。
- Orca agent 检测、原生 TUI 启动、terminal agent rows 与 agent status 推断。
- 把新逻辑继续追加到 `WorktreeList.tsx`、`WorktreeCard.tsx`、`App.tsx` 等巨型文件。

复用组件若直接依赖 Orca 全局 store 或 SSH 类型，应先提取纯 surface/contract，再由 Handoff Workbench
注入 transport；不得为了“少改文件”把 agentd DTO 映射成虚假的 Orca Worktree。

### 11.4 状态所有权

- `CatalogStore`：bootstrap + control stream 的纯投影，可随时重建。
- `WorkspaceSessionStore`：每 Workspace 标签组、split tree、active tab；允许桌面本地持久化。
- `TaskSessionStore`：Task snapshot + TaskFrame reducer；关闭标签可丢弃，重开从 agentd 恢复。
- Token、endpoint secret、peer credentials 只在 Electron Main/本机 agentd；renderer 永不可见。

### 11.5 仓库拓扑

Handoff Desktop 与 agentd 是同一产品、同一协议版本和同一套端到端验收，采用单仓库更容易保证
原子变更。实施时将 Orca 作为上游源码快照导入本仓库的 `desktop/`：

- 来源固定为官方 `https://github.com/stablyai/orca` 的明确 commit/tag，不以无版本的下载目录为基线。
- 使用 squash/subtree 形态导入，不保留嵌套 `.git`；`desktop/UPSTREAM.md` 记录 URL、commit、版本、
  导入时间和后续同步方法。
- 保留 Orca MIT LICENSE 与必要 attribution。
- 当前 `/Users/xushixin/Downloads/AnyTimeDelete/orca-main` 只用于设计期阅读，不直接修改。
- Go agentd 继续位于仓库根目录；Electron 工程位于 `desktop/`，二者各自保持构建入口。
- 导入和后续开发必须在隔离 worktree 完成；不能污染当前主 checkout 或下载目录。

若规格复核时用户明确要求双仓库，则 writing-plans 必须先重写代码组织、版本联动和端到端 CI；
不得在实施中途临时改成双仓库。

## 12. 失败与恢复契约

### 12.1 机器状态

| 状态 | 语义 |
|---|---|
| `connecting` | 建立连接中，保留树结构但不开放资源操作 |
| `reconciling` | 已认证，正在 capability、事件补拉和资源校准；写操作仍关闭 |
| `connected` | cursor 已追平、Reconcile 完成、核心 capability 可用 |
| `unavailable` | 网络、认证或进程不可达，资源现场不可用 |
| `incompatible` | 已连接但缺少本阶段核心 capability，明确提示升级 agentd |

连接状态不改写 TaskState。机器断开时 Task 保留最后已知状态，不自动变 failed/stalled；恢复后由
所属机器的权威事件决定。

### 12.2 远端恢复顺序

1. Authenticate：验证机器身份和 secret。
2. Negotiate：交换 protocol version 与 files/git/pty/tasks/preview capability。
3. Catch up：从 `last_machine_seq` 补拉 durable machine events。
4. Reconcile：扫描 worktree、GitRef、Task、PTY，修复极端漏差。
5. Reattach：恢复文件 watch、PTY、已打开 TaskFrame 流与 preview。
6. 以上完成后才转 `connected` 并开放写操作。

### 12.3 统一错误线格式

```text
Problem {
  code
  message
  retryable
  machine_id?
  workspace_id?
  task_id?
  operation_id?
  details?          不含 secret、env value 或完整敏感输出
}
```

至少定义：`LOCAL_AGENTD_UNAVAILABLE`、`MACHINE_OFFLINE`、`CAPABILITY_UNSUPPORTED`、
`RESOURCE_NOT_FOUND`、`PATH_OUTSIDE_WORKSPACE`、`VERSION_CONFLICT`、`COMMAND_CONFLICT`、
`CURSOR_EXPIRED`、`OPERATION_IN_PROGRESS`、`AUTH_FAILED`。

### 12.4 关键不变量

- 不可用不是只读：远端断开时不开放文件读取、TaskTUI 或命令。
- 桌面关闭、刷新或多开不改变 executor 生命周期。
- 文件冲突不静默覆盖。
- PTY 丢失不静默创建替代 Shell。
- 协议不兼容不偷偷降级到 Orca SSH。
- 所有长操作和用户命令都以 command/operation ID 幂等。

## 13. 安全边界

- Electron renderer 只使用稳定资源 ID 与 Workspace 相对路径。
- Electron Main 持有桌面连接本机 agentd 的 token；不通过 preload 暴露。
- 本机 agentd 持有远端 secret_ref；桌面永远拿不到远端 token。
- 非 loopback 远端连接必须运行在加密 transport 上；`http/ws` 只允许 loopback 或明确配置的受保护
  私有网络开发模式，默认 fail-closed。
- 文件 realpath 必须位于 Workspace 根内；symlink 越界、路径穿越和 TOCTOU 需在 machine agentd
  授权边界防护。
- preview-port 只允许目标机器 loopback，绑定 session 与允许端口，不形成任意 TCP 代理。
- TaskFrame 不保存或显示隐藏思维链。
- 日志不记录 token、env value、用户回答全文或完整文件内容。

## 14. 可观测性与性能

### 14.1 结构化日志

关键日志都必须携带适用的 `machine_id`、`workspace_id`、`task_id`、`operation_id`、cursor/seq 和阶段：

| 节点 | 日志 |
|---|---|
| peer 连接 | connect/auth/negotiate 成功与失败、协议和 capability |
| 事件同步 | from/to machine_seq、批次数、重复数、延迟、断点恢复 |
| Reconcile | 原因、扫描数量、upsert/remove 数、耗时、失败资源 |
| 控制面流 | bootstrap revision、订阅 after revision、cursor expired、慢客户端断开 |
| Project Operation | started/progress/partial/succeeded/failed，目标机器和路径摘要 |
| 文件保存 | workspace、relative path、expected/actual version；不打内容 |
| PTY | spawn/attach/resize/exit/replay，session/incarnation/seq |
| Task 命令 | command id、kind、ticket version、accepted/conflict/delivery result |
| TaskFrame | adapter、kind、批量大小、task_seq；不打隐藏内容和完整输出 |

成功路径不能静默；错误必须带可行动原因，UI 提供打开本机 agentd 日志入口。

### 14.2 流量与存储边界

- 全局流只含资源和任务摘要；完整 TaskFrame 仅对打开任务订阅。
- message delta 按时间/大小批量写入，避免 token 级记录。
- 大输出使用 artifact_ref，按需读取并设置尺寸上限、分页或 range。
- Task session 定期生成 render snapshot，TaskFrame 可按 retention 压缩；cursor 过期走 snapshot reset。
- 慢 WS 客户端超过有界缓冲后主动断开，数据由 durable cursor 重连，不允许内存无限增长。
- 桌面 reducer 必须能在重复、乱序拒绝和 snapshot reset 下保持确定性。

## 15. 兼容与迁移

- 保留现有 CLI REST/WS 线格式和命令行为；新桌面协议使用独立版本前缀。
- 旧 Task 自动绑定 local Machine 与 detached Workspace，保证历史任务可显示。
- 每个 peer 握手显式协商 protocol/capabilities；核心能力缺失时标记 incompatible，不走猜测。
- 新数据库表和列使用幂等迁移；迁移失败时 agentd 拒绝以半迁移状态提供写服务。
- 本阶段不删除 Orca 旧持久化和 SSH 代码；Handoff Workbench 通过模块边界与它们共存。
- 第三阶段删除前先用依赖图和本阶段九个验收场景做回归，防止误删渲染能力。

## 16. 测试策略

### 16.1 Handoff Go 单元与存储测试

- Machine/Project/Location/Workspace/TaskFrame/ControlEvent/Operation 表迁移和约束。
- 每 Project 至少一 Location、本机最多一、远端最多一。
- 旧 Task → local Machine + detached Workspace 的迁移。
- machine outbox 与资源更新同事务；control projection 与 control event 同事务。
- machine_seq/control_revision/task_seq 的单调、去重、断点补发和 cursor expired。
- Reconciler 对 worktree/ref 新增、更新、删除、重启和重连的行为。
- detached Workspace 归并保持 Workspace/Task ID 不变。
- command_id/operation_id 幂等，Ticket version 冲突只调用 executor 一次。
- 文件相对路径授权、symlink 越界、if_match 冲突和原子写。
- PTY session/incarnation/seq、重连和会话丢失。
- preview-port 的 loopback/端口/session 限制。

### 16.2 协议与集成测试

- 本机控制面 ↔ fake/real remote agentd 的认证、capability、补拉、Reconcile。
- bootstrap revision 与全局 WS 无窗口丢失。
- Task snapshot + frame replay + snapshot reset。
- OpenCode/Claude Code/Grok fixture 到六类 TaskFrame 的归一化；reasoning 不进入 frame。
- artifact 按需读取、大小边界和远端断开。
- 远端文件、Git、PTY 和 preview 通过本机 agentd 代理，桌面不直连远端。

### 16.3 Electron 自动化

- 使用可控 fake agentd 验证 CatalogStore、WorkspaceSessionStore 和 TaskSessionStore reducer。
- ProjectTree 层级、右侧计数和 upsert/remove。
- Workspace 切换恢复各自 tab/split，breadcrumb 和文件根一致。
- Project 创建表单的本机/远端约束、Finder 仅本机、clone path 默认值。
- 文件保存冲突 UI、终端结束 UI、远端不可用遮罩、incompatible 提示。
- TaskTUI 六类 frame、artifact 展开、审批/回答/继续/停止和命令 conflict。
- 多桌面实例同时处理同一 Ticket，executor 只收到一次。
- 架构守卫：handoff feature 不导入 Orca SSH/旧 project persistence，TaskTUI 不创建 PTY。

### 16.4 真实机器验收

使用 macOS 桌面、本机 agentd 和一台真实远端开发机 agentd，完成 §17 的九个场景。真实验收必须
保存命令、日志、截图/录屏和实际数据库/进程证据，不能以 mock E2E 代替。

## 17. 九个完成门槛

1. **项目持久化**：用 Finder 添加本机已有目录，再添加一个远端粘贴路径；重启桌面后完整恢复。
2. **Clone 两端一致**：本机或远端 clone，默认目录可改；失败重试不重复 clone。
3. **外部 Git 推送**：终端创建 branch/worktree 后无需刷新；断线漏差在重连 Reconcile 后修复。
4. **目录联动**：主目录与 worktree 切换时标签组、breadcrumb 和文件根一致；其他 Workspace 标签不丢。
5. **文件与终端**：两端均可编辑保存；外部修改触发 conflict；终端 cwd 正确、可分屏和重连。
6. **远端浏览器**：远端 localhost 服务经 agentd preview 打开；机器断开后明确不可用。
7. **任务自动归属**：dispatch 到登记 worktree 后自动出现；项目外目录 detached，添加项目后归并。
8. **结构化任务闭环**：真实 OpenCode 任务展示六类 frame，审批、回答、继续、停止均由权威事件确认。
9. **断线与并发**：远端断开时文件/TUI/操作不可用，重连恢复；两个桌面审批只投递一次。

第二阶段只有在九个场景全部有可复现证据后才算完成，之后才进入 Orca 代码瘦身。

## 18. 实施计划的硬边界

后续 writing-plans 必须遵守：

1. 先建立 agentd control-plane/domain/protocol 和可独立测试的 fake server，再接桌面。
2. 在 Orca 中新建 Handoff Workbench 边界，不往 SSH 或旧 Worktree 状态树追加 agentd 分支。
3. 先提取可复用渲染 surface，再注入 handoff transport；不复制一份 xterm/Monaco/browser。
4. 每个实现任务包含关键节点结构化日志、错误上下文、文件职责注释和导出方法注释。
5. 每个纵切 checkpoint 同时有自动测试和真实运行证据。
6. 不在第二阶段顺手删除 Orca 旧代码；删除工作单独进入第三阶段计划。
7. 不实现或恢复 OpenCode 原生 TUI 桌面路径。
