# Handoff Desktop Control Plane and Workbench Shell Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 建立 agentd 唯一控制面、持久化 Project/Location/Workspace/Machine/TaskSummary，并让导入 Orca 的 Handoff Workbench 通过本机 agentd 实时展示项目、机器、主目录、worktree、任务和权威计数。

**Architecture:** 新建 `controlplane` 领域层、`store` SQLite adapter、`machineauthority` 本机资源层和 `/v1` HTTP/WS adapter。每台机器先写 durable `machine_events`，本机控制面再投影为单调 `control_revision`；renderer 只消费 bootstrap + control events。桌面 Handoff feature 独立于 Orca 的 SSH、Project、Worktree 和全局 Zustand store。

**Tech Stack:** Go 1.26、SQLite、coder/websocket、fsnotify、Electron 43、React、TypeScript、Zod、Zustand、Vitest、Playwright。

## Global Constraints

- 先阅读总索引和设计规格；本计划只完成控制面与工作台骨架，不实现文件内容、PTY、Preview 或完整 TaskTUI。
- Orca 固定导入 annotated tag `v1.4.177-rc.0`：tag object `ff48a6d33b7bde5d37ccc367dc5aa1103d2a8ee4`，源码 commit `9e948fbdf462ede3c0160c719474100fc5cbefb7`；下载目录只读。
- 保留现有 CLI REST/WS 线格式；`proto.Event` 和 16 槽 Hub 不承载 desktop 高频 catalog 流。
- Project 必须有 1–2 个 Location：本机 `0..1`、远端 `0..1`，合计至少一个；一个项目至多一台远端开发机。
- 项目创建只有“本机已有路径”可调 Finder；远端已有路径只收绝对 path；clone path 默认 `~/.handoff/<repo-name>` 且可编辑。
- 所有新 Go/TS 文件写职责和边界头注释；所有导出项写文档；复杂约束写“为什么”注释。
- 每个服务入口、peer 调用、Reconcile、Operation 状态变化、cursor 和成功结果写结构化日志；禁止记录 token、文件内容、env value 和回答全文。
- 每个任务遵循 red → green → refactor → 聚焦验证 → 回归验证 → commit；不得合并多个任务后一次性补测试或日志。

---

### Task 1: 导入可复现的 Orca 上游快照

**Files:**
- Create: `desktop/**`（来自官方 Orca commit 的完整源码快照，不含 `.git`）
- Create: `desktop/UPSTREAM.md`
- Verify: `desktop/LICENSE`
- Modify: `.gitignore`

**Interfaces:**
- Consumes: official Orca annotated tag `v1.4.177-rc.0` (`ff48a6d33b7bde5d37ccc367dc5aa1103d2a8ee4`) resolving to source commit `9e948fbdf462ede3c0160c719474100fc5cbefb7`.
- Produces: tracked `desktop/` source snapshot, `desktop/UPSTREAM.md`, and the upstream `desktop/package.json`/lockfile used by Tasks 8–10.

- [ ] 在隔离 worktree 中记录当前状态，确认只存在用户已有的未跟踪项，后续提交不得包含 `.superpowers/`、`CONTEXT.md`、`docs/adr/` 或 `prototypes/`：

  ```bash
  git status --short
  git worktree list --porcelain
  ```

- [ ] 先运行来源契约检查并确认失败，因为 `desktop/UPSTREAM.md` 尚不存在：

  ```bash
  test -f desktop/UPSTREAM.md && rg -n 'ff48a6d33b7bde5d37ccc367dc5aa1103d2a8ee4|9e948fbdf462ede3c0160c719474100fc5cbefb7|v1.4.177-rc.0|MIT' desktop/UPSTREAM.md desktop/LICENSE
  ```

  Expected: 非零退出，缺少 `desktop/UPSTREAM.md`。

- [ ] 从官方仓库校验 tag 指向，输出必须包含固定 commit：

  ```bash
  git ls-remote https://github.com/stablyai/orca refs/tags/v1.4.177-rc.0 'refs/tags/v1.4.177-rc.0^{}'
  ```

  Expected: tag object 为 `ff48a6d33b7bde5d37ccc367dc5aa1103d2a8ee4`，peeled commit 为 `9e948fbdf462ede3c0160c719474100fc5cbefb7`。

- [ ] 使用 `mktemp -d` 创建专用临时目录，clone 固定 tag，验证 HEAD 后把工作树内容复制到 `desktop/`，明确排除 `.git`；不要复制 `/Users/xushixin/Downloads/AnyTimeDelete/orca-main`：

  ```bash
  ORCA_IMPORT_DIR="$(mktemp -d /tmp/handoff-orca-import.XXXXXX)"
  git clone --depth 1 --branch v1.4.177-rc.0 https://github.com/stablyai/orca "$ORCA_IMPORT_DIR/orca"
  test "$(git -C "$ORCA_IMPORT_DIR/orca" rev-parse HEAD)" = 9e948fbdf462ede3c0160c719474100fc5cbefb7
  mkdir -p desktop
  rsync -a --exclude .git "$ORCA_IMPORT_DIR/orca/" desktop/
  ```

- [ ] 写 `desktop/UPSTREAM.md`，必须包含：官方 URL、annotated tag、tag object、peeled source commit、导入日期 `2026-08-09`、MIT、上述无嵌套 `.git` 的同步方法，以及“本地下载目录不是来源”的边界说明。

- [ ] 在根 `.gitignore` 追加 `desktop/node_modules/`、`desktop/out/`、`desktop/dist/`、`desktop/release/`；保留导入快照自身的 `.gitignore`。

- [ ] 运行来源和嵌套仓库检查：

  ```bash
  test ! -e desktop/.git
  rg -n 'ff48a6d33b7bde5d37ccc367dc5aa1103d2a8ee4|9e948fbdf462ede3c0160c719474100fc5cbefb7|v1.4.177-rc.0|MIT' desktop/UPSTREAM.md desktop/LICENSE
  git status --short desktop .gitignore
  ```

- [ ] 安装锁文件声明的依赖并跑无改动基线：

  ```bash
  (cd desktop && corepack pnpm install --frozen-lockfile)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/app-version.test.ts)
  ```

  Expected: typecheck 与聚焦测试通过；若上游基线本身失败，原样记录命令和错误，先判断是否为环境依赖，不能用改测试/关 lint 掩盖。

- [ ] 此任务没有新运行时代码；在提交前确认 provenance 文档和 LICENSE 完整，不添加日志或注释噪声。

- [ ] Commit:

  ```bash
  git add .gitignore desktop
  git commit -m "chore: import pinned Orca desktop source"
  ```

### Task 2: 定义控制面领域模型、桌面 v1 契约和幂等数据库迁移

**Files:**
- Create: `internal/controlplane/model.go`
- Create: `internal/controlplane/repository.go`
- Create: `internal/controlplane/model_test.go`
- Create: `internal/desktopapi/v1.go`
- Create: `internal/desktopapi/problem.go`
- Create: `internal/desktopapi/catalog_assembler.go`
- Create: `internal/desktopapi/catalog_assembler_test.go`
- Create: `internal/desktopapi/contract_test.go`
- Create: `internal/desktopapi/testdata/bootstrap.json`
- Create: `internal/desktopapi/testdata/control-event.json`
- Create: `internal/desktopapi/testdata/problem.json`
- Create: `internal/store/desktop_schema.go`
- Create: `internal/store/desktop_schema_test.go`
- Modify: `internal/store/store.go`
- Modify: `internal/proto/proto.go`
- Modify: `internal/proto/proto_test.go`

**Interfaces:**
- Consumes: existing `store.Open`, `proto.Task`, `proto.TaskState`, and legacy CLI JSON compatibility.
- Produces: `controlplane.Repository`, `Machine`, `Project`, `ProjectLocation`, `Workspace`, `GitRef`, `TaskSummary`, `Operation`, `MachineEvent`, `ControlEvent`; `desktopapi.BootstrapResponse`, `ControlEventEnvelope`, `Problem`, `CatalogAssembler`, and `WriteProblem(http.ResponseWriter, int, Problem)`.

- [ ] 先写 `model_test.go`，覆盖所有枚举 JSON 值和关键验证：Machine 状态五值；Workspace `main/worktree/detached`；Operation 五态；项目 Location 为 1–2 个、本地至多一个、远端至多一个；TaskState 新增 `stalled` 且仅允许 `running/waiting_answer/waiting_review -> stalled`、`stalled -> running/failed`。

- [ ] 先写 `desktop_schema_test.go`，打开全新库并断言以下表/索引存在，再重复 `Open` 两次验证迁移幂等：

  ```text
  control_metadata, machines, projects, project_locations, workspaces,
  git_refs, task_summaries, operations, machine_events, machine_cursors,
  control_events
  ```

  必须断言：`machines.kind=local` 唯一、`project_locations(project_id, role)` 唯一、`workspaces(machine_id, canonical_path)` 唯一、`machine_events(machine_id,machine_seq)` 唯一、`control_events(control_revision)` 唯一。

- [ ] 在 `proto_test.go` 先增加失败断言，要求 Task JSON 含 `machine_id`、`workspace_id`，并验证旧 JSON 缺字段仍可解码为空值。

- [ ] 运行红灯：

  ```bash
  go test ./internal/controlplane ./internal/desktopapi ./internal/store ./internal/proto
  ```

  Expected: 新类型、迁移和 `stalled` 尚不存在而失败。

- [ ] 在 `controlplane/model.go` 定义领域类型，至少采用以下稳定字段；不要把 token、secret value 放进任何公开结构：

  ```go
  type Machine struct {
      ID string
      DisplayName string
      Kind MachineKind
      Endpoint string
      SecretRef string
      ProtocolVersion int
      Capabilities map[string]int
      Status MachineStatus
      LastSeenAt *time.Time
  }

  type ProjectLocation struct {
      ID string
      ProjectID string
      MachineID string
      Role LocationRole
      MainWorkspaceID string
      Source LocationSource
      GitURL string
      CreatedAt time.Time
      UpdatedAt time.Time
  }

  type Workspace struct {
      ID string
      MachineID string
      LocationID *string
      Kind WorkspaceKind
      Path string
      CanonicalPath string
      RepoIdentity string
      GitCommonDir string
      Branch string
      HeadOID string
      Availability Availability
      LastScannedAt time.Time
  }
  ```

- [ ] 同文件定义 `Project`、`GitRef`、`TaskSummary`、`Operation`、`OperationTargetResult`、`MachineEvent`、`ControlEvent`；所有资源都用稳定 ID，事件 payload 用 `json.RawMessage`，不得用路径作跨层主键。

- [ ] 在 `repository.go` 定义领域层所需端口，不让领域层依赖 `database/sql`：

  ```go
  type Repository interface {
      EnsureLocalMachine(context.Context, string) (Machine, error)
      SyncConfiguredMachines(context.Context, []ConfiguredMachine) ([]Machine, error)
      Snapshot(context.Context) (Snapshot, error)
      UpsertWorkspaceWithMachineEvent(context.Context, Workspace, MachineEventKind) (MachineEvent, error)
      RemoveWorkspaceWithMachineEvent(context.Context, string, string) (MachineEvent, error)
      UpsertGitRefsWithMachineEvents(context.Context, string, []GitRef) ([]MachineEvent, error)
      AppendTaskSummaryEvent(context.Context, TaskSummary) (MachineEvent, error)
      ApplyMachineEvent(context.Context, MachineEvent) (ControlEvent, bool, error)
      MachineEventsAfter(context.Context, string, int64, int) ([]MachineEvent, error)
      ControlEventsAfter(context.Context, int64, int) ([]ControlEvent, error)
  }
  ```

  `bool` 表示重复 machine event 被幂等忽略；实现不得依赖错误文本判断重复。

- [ ] 在 `desktopapi/v1.go` 定义 wire DTO，而不是直接 JSON marshal 领域对象。`BootstrapResponse` 必须含 machines/projects/locations/workspaces/git_refs/active_task_summaries/operations/control_revision；`ControlEventEnvelope` 必须含 revision/kind/resource_id/payload/created_at；所有 JSON key 用 snake_case。

- [ ] 在 `problem.go` 定义 `Problem` 与这些稳定 code：`LOCAL_AGENTD_UNAVAILABLE`、`MACHINE_OFFLINE`、`CAPABILITY_UNSUPPORTED`、`RESOURCE_NOT_FOUND`、`PATH_OUTSIDE_WORKSPACE`、`VERSION_CONFLICT`、`COMMAND_CONFLICT`、`CURSOR_EXPIRED`、`OPERATION_IN_PROGRESS`、`AUTH_FAILED`。提供 `WriteProblem(http.ResponseWriter, int, Problem)`，不泄漏内部错误细节。

- [ ] 写三份 golden JSON 并让 Go contract test round-trip；后续桌面 Zod 测试直接读取同一 fixture，防止 Go/TS 契约漂移。

- [ ] `CatalogAssembler` 是无状态纯转换层，先提供 `ToBootstrap(controlplane.Snapshot) BootstrapResponse`、`ToControlEvent(controlplane.ControlEvent) (ControlEventEnvelope, error)`、`ToOperation(controlplane.Operation) OperationDTO`；assembler 测试覆盖 nil/空集合/可选字段/枚举和错误 payload，禁止业务校验或 DB/I/O。项目创建 command 的转换在 Task 5 定义 command 后再加入。

- [ ] 在 `desktop_schema.go` 实现 `migrateDesktopV1(ctx, db)` 并从 `store.Open` 调用。所有 DDL 使用事务；失败必须 rollback 并让 `Open` 返回错误，不能半迁移继续服务。

- [ ] 修改 `proto.Task` 增加：

  ```go
  MachineID string `json:"machine_id"`
  WorkspaceID string `json:"workspace_id"`
  ```

  为 `tasks` 增加对应非空默认空串列；增加 `TaskStateStalled` 和状态迁移。现有 `repo_path`、`target`、CLI JSON key 均保留。

- [ ] 为新文件补齐职责/边界头注释、导出项文档和约束原因注释。迁移日志只记录 schema version 和 DB path；失败日志由 `store.Open` 调用方带上下文记录，不在 leaf store 重复打两遍。

- [ ] 运行绿灯与回归：

  ```bash
  gofmt -w internal/controlplane internal/desktopapi internal/store/desktop_schema.go internal/store/desktop_schema_test.go internal/proto/proto.go internal/proto/proto_test.go
  go test ./internal/controlplane ./internal/desktopapi ./internal/store ./internal/proto
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/controlplane internal/desktopapi internal/store internal/proto
  git commit -m "feat: add desktop control plane schema and contracts"
  ```

### Task 3: 建立稳定本机身份、配置机器投影和旧任务迁移

**Files:**
- Create: `internal/controlplane/bootstrap.go`
- Create: `internal/controlplane/bootstrap_test.go`
- Create: `internal/store/machines.go`
- Create: `internal/store/machines_test.go`
- Create: `internal/store/workspaces.go`
- Create: `internal/store/workspaces_test.go`
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `cmd/agentd.go`
- Modify: `cmd/agentd_test.go`

**Interfaces:**
- Consumes: Task 2 `controlplane.Repository`, `config.Config`, existing Tasks, and configured `targets`.
- Produces: `(*controlplane.BootstrapService).Initialize(context.Context, *config.Config) (controlplane.Machine, error)`, stable local/configured Machine rows, and migrated Task `machine_id/workspace_id`.

- [ ] 写失败测试覆盖：首次启动生成一个 local Machine UUID；同库重启保持 ID；配置 `targets` 只保存 `secret_ref=config.targets.<name>.token` 而不落 token；endpoint/display name 改变保留 machine ID；删掉 target 后保留 last-known Machine 但标 `unavailable`；旧 Task 按 `Task.Workdir()` 绑定 local Machine 和稳定 detached Workspace。

- [ ] 增加迁移原子性测试：两个旧 Task 指向同一 canonical path 时复用一个 Workspace；失败时 Task 不出现只写了 `machine_id` 没写 `workspace_id` 的半迁移状态。

- [ ] 运行红灯：

  ```bash
  go test ./internal/controlplane ./internal/store ./internal/config ./cmd -run 'Machine|LegacyTask|ConfiguredTarget'
  ```

- [ ] 在 `config.Target` 增加可选 `DisplayName`、`Transport`、`AllowInsecurePrivate`，保留 `Addr/Token/User` 兼容旧 CLI。严格解析错误提示同步更新；本计划不新增机器管理 UI。

- [ ] 实现 `controlplane.BootstrapService`：

  ```go
  type BootstrapService struct {
      repo Repository
      log  *slog.Logger
  }

  func (s *BootstrapService) Initialize(ctx context.Context, cfg *config.Config) (Machine, error)
  ```

  初始化顺序固定为：ensure local Machine → sync configured remote metadata → migrate legacy tasks → project local machine events into control log。任一步失败即拒绝启动 desktop `/v1` 写服务。

- [ ] `machines.go` 用 `control_metadata.local_machine_id` 保存稳定 ID；configured remote 按 target map key 保存稳定 `config_key`，token 只由运行时 credential resolver 从 `config.Config` 读取。

- [ ] `workspaces.go` 实现 canonical path 与 detached Workspace 复用。macOS 路径规范化须处理绝对路径、clean 和实际可解析的 symlink；保留展示 path，但唯一键只用 canonical path。

- [ ] 旧 Task 迁移必须在单事务里更新 Task 两个 ID 并 upsert `task_summaries`；TaskState 保持原值，不因迁移制造新生命周期事件。

- [ ] 在 `cmd/agentd.go` 中于 `store.Open` 后、`NewServer` 前运行 `BootstrapService.Initialize`；结构化记录开始、local machine id、target 数、迁移 Task 数和完成耗时。token/secret 不进日志。

- [ ] 为所有新文件补头注释、导出方法文档和“为何不把 token 落库”“为何旧 Task 只建 detached Workspace”的原因注释。

- [ ] 运行绿灯与回归：

  ```bash
  gofmt -w internal/controlplane/bootstrap.go internal/controlplane/bootstrap_test.go internal/store/machines.go internal/store/machines_test.go internal/store/workspaces.go internal/store/workspaces_test.go internal/config/config.go internal/config/config_test.go cmd/agentd.go cmd/agentd_test.go
  go test ./internal/controlplane ./internal/store ./internal/config ./cmd
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/controlplane internal/store internal/config cmd/agentd.go cmd/agentd_test.go
  git commit -m "feat: persist machine identity and migrate legacy tasks"
  ```

### Task 4: 实现本机 Machine Authority、Git inventory、durable outbox 与 Reconcile

**Files:**
- Create: `internal/gitidentity/identity.go`
- Create: `internal/gitidentity/identity_test.go`
- Create: `internal/machineauthority/authority.go`
- Create: `internal/machineauthority/inventory.go`
- Create: `internal/machineauthority/inventory_test.go`
- Create: `internal/machineauthority/reconciler.go`
- Create: `internal/machineauthority/reconciler_test.go`
- Create: `internal/machineauthority/git_watch.go`
- Create: `internal/machineauthority/git_watch_test.go`
- Create: `internal/store/machine_events.go`
- Create: `internal/store/machine_events_test.go`
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `internal/store/store.go`
- Modify: `internal/agentd/manager.go`
- Modify: `internal/agentd/manager_test.go`
- Modify: `cmd/agentd.go`

**Interfaces:**
- Consumes: Task 2 repository transaction ports, Task 3 stable Machine identity, existing Git CLI and `Manager.Dispatch`.
- Produces: `gitidentity.CanonicalRepoIdentity(rawURL string) (string, error)`, `machineauthority.Authority`, `ReconcileResult`, durable `(machine_id, machine_seq)` events, and `CreateTaskWithMachineEvent`/task-summary transactional store methods.

- [ ] 写失败测试：解析 GitHub HTTPS/SSH/scp-like URL 得到同一 repo identity；Git 2.25 的 `worktree list --porcelain` 能发现 main/worktree；新增、移动 HEAD、删除 worktree 和新增/删除 branch 产生准确 upsert/remove；重复 Reconcile 不产生事件；同一事务中资源更新与 machine event 同生同灭；`machine_seq` 每机器单调。

- [ ] 写 Manager 失败测试：dispatch 先创建完整 pending Task（含 branch/summary/machine/workspace）和 `task.upsert`，再调用 adapter；adapter 启动失败仍有 failed 摘要；状态更新与 task outbox 同事务；旧 CLI 返回结构不变。

- [ ] 运行红灯：

  ```bash
  go test ./internal/gitidentity ./internal/machineauthority ./internal/store ./internal/agentd -run 'Identity|Inventory|Reconcile|MachineEvent|DispatchPublishes'
  ```

- [ ] 添加 `github.com/fsnotify/fsnotify`；watcher 只触发 debounce 后的完整 Reconcile，不直接把文件系统事件当事实。

- [ ] `gitidentity.CanonicalRepoIdentity(rawURL string) (string, error)` 统一 HTTPS、SSH URL 和 scp-like remote；只返回 `host/owner/repo` 规范值，不保留 userinfo、token、scheme 或 `.git` 后缀。

- [ ] `machineauthority.Authority` 明确本机资源边界：

  ```go
  type Authority interface {
      InspectPath(context.Context, string) (PathInspection, error)
      Clone(context.Context, CloneCommand) (PathInspection, error)
      ReconcileLocation(context.Context, controlplane.ProjectLocation) (ReconcileResult, error)
      Snapshot(context.Context) (MachineSnapshot, error)
  }
  ```

  本计划先实现 Inspect/Clone/Inventory；文件内容、PTY、Preview 方法留到计划 02，禁止用 Electron Node `fs` 作为临时替代。

- [ ] Git 命令仅使用 2.25 基线：`worktree list --porcelain`、`for-each-ref`、`rev-parse`、`remote get-url`。不得直接使用 2.36 才有的 `worktree list -z`；若引入更高版本选项，必须行为探测、缓存和 fallback 测试。

- [ ] `machine_events.go` 提供事务 helper，使 Workspace/GitRef/TaskSummary 更新和 outbox append 共用一个 `sql.Tx`。事件 payload 是完整可幂等 upsert 的公开投影，不含本地 secret 或文件内容。

- [ ] 修改 `Manager.Dispatch`：在 `PrepareWorkspace` 后同步 resolve/create Workspace；登记路径命中已有 Workspace，项目外路径创建/复用 detached Workspace；构造 Task 时一次填好 Branch、PlanSummary、MachineID、WorkspaceID；用 `CreateTaskWithMachineEvent` 代替 `CreateTask + SetTaskField`。修改 `UpdateTaskState` 与会影响摘要的字段更新，使 Task 更新和 `task.upsert` 同事务。

- [ ] agentd 启动时先 Reconcile；之后启动 watcher 和周期兜底（默认 30s，可测试注入）。日志包含原因 `startup|watch|periodic|peer_reconnect`、扫描数、upsert/remove 数、耗时、machine cursor；无变化也记录 Debug 摘要，变化成功记录 Info。

- [ ] 为新文件补职责/边界头注释、导出方法文档和 watcher 仅作提示的“为什么”注释。错误日志带 machine/location/path 摘要，不打完整 remote URL 凭据。

- [ ] 运行绿灯与回归：

  ```bash
  gofmt -w internal/gitidentity internal/machineauthority internal/store/machine_events.go internal/store/machine_events_test.go internal/agentd/manager.go internal/agentd/manager_test.go cmd/agentd.go
  go test ./internal/gitidentity ./internal/machineauthority ./internal/store ./internal/agentd ./cmd
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add go.mod go.sum internal/gitidentity internal/machineauthority internal/store internal/agentd/manager.go internal/agentd/manager_test.go cmd/agentd.go
  git commit -m "feat: reconcile machine resources through durable outbox"
  ```

### Task 5: 实现 durable Project Operation 与本机/远端 Location 创建语义

**Files:**
- Create: `internal/controlplane/project_service.go`
- Create: `internal/controlplane/project_service_test.go`
- Create: `internal/controlplane/project_command.go`
- Create: `internal/store/projects.go`
- Create: `internal/store/projects_test.go`
- Create: `internal/store/operations.go`
- Create: `internal/store/operations_test.go`
- Modify: `internal/controlplane/repository.go`
- Modify: `internal/desktopapi/catalog_assembler.go`
- Modify: `internal/desktopapi/catalog_assembler_test.go`

**Interfaces:**
- Consumes: Task 4 `machineauthority.PathInspection`, canonical repo identity, and Task 2 Project/Location/Operation repositories.
- Produces: `CreateProjectCommand`, `InspectPathCommand`, `CloneLocationCommand`, `MachineCommander`, and `(*ProjectService).Create(context.Context, CreateProjectCommand) (Operation, error)`.

- [ ] 写失败测试覆盖全部表单不变量：无 Location 拒绝；两个 local 拒绝；两个 remote 拒绝；Location role 与 Machine kind 不一致拒绝；远端已有路径必须绝对；本机已有路径允许绝对 Finder 结果；clone 必须有 Git URL；空 clone path 自动变成 `~/.handoff/<repo-name>`；本机和远端 identity 不同拒绝组成一个 Project。

- [ ] 写幂等/部分成功测试：相同 `operation_id` 只调用每个目标一次；一个 Location 成功、另一个失败时 Operation=`partial`，成功目录不删除，Project 保存成功 Location；同 ID 重试只补失败目标；两个目标都失败时 Operation=`failed` 且不创建空 Project。

- [ ] 写 detached 归并测试：目标 path 已有 detached Workspace/Task 时，注册 ProjectLocation 后只更新该 Workspace 的 `location_id/kind`，保留 Workspace ID 与所有 Task `workspace_id`；按 `machine_id + repo_identity/git_common_dir + canonical_path` 不匹配时不得误归并。

- [ ] 运行红灯：

  ```bash
  go test ./internal/controlplane ./internal/store -run 'CreateProject|Operation|Location'
  ```

- [ ] 定义明确命令：

  ```go
  type CreateProjectCommand struct {
      OperationID string
      Name string
      Locations []CreateLocationCommand
  }

  type CreateLocationCommand struct {
      MachineID string
      Role LocationRole
      Source LocationSource
      Path string
      GitURL string
      ClonePath string
  }

  type InspectPathCommand struct {
      OperationID string
      TargetID string
      MachineID string
      Path string
  }

  type CloneLocationCommand struct {
      OperationID string
      TargetID string
      MachineID string
      GitURL string
      ClonePath string
  }

  type MachineCommander interface {
      InspectPath(context.Context, InspectPathCommand) (PathInspection, error)
      Clone(context.Context, CloneLocationCommand) (PathInspection, error)
  }
  ```

  `OperationID + TargetID` 组成单个目录副作用的幂等键；所有调用使用具名 command，避免 machine/Git/path 位置错乱。

- [ ] `ProjectService.Create` 先持久化 pending Operation，再逐目标更新 running/result；HTTP 断开不取消后台 operation。每个目标状态和最终 Project/Location/Workspace + `control_event` 在同一事务提交。

- [ ] 注册已有目录或 clone 完成后先查相同机器的稳定 Workspace：命中 detached 时原位 adoption；未命中才创建 main Workspace。adoption 与 Project/Location/control event 同事务，禁止复制 Workspace 或批量重写 Task。

- [ ] 在 command 类型稳定后给 `CatalogAssembler` 增加 `ToCreateProjectCommand(CreateProjectRequest) (controlplane.CreateProjectCommand, error)`；assembler 只做字段/枚举转换，Location 数量、role/Machine 匹配、Git identity 等业务规则仍由 `ProjectService` 校验。用 table test 锁定 local-only、remote-only、双 Location 和 clone/default-path 输入。

- [ ] clone 目标已存在时只能在“同 operation 已成功且 repo identity 一致”时幂等复用；其他情况返回 path conflict，不能覆盖、删除或 `git reset` 用户目录。

- [ ] Operation 日志包含 operation ID、目标 machine、source、阶段、结果和耗时；Git URL 仅记录 host/repo identity，不能记录 URL userinfo；成功和 partial 路径均不静默。

- [ ] 为新文件补职责/边界头注释、导出方法文档和部分成功不回滚目录的“为什么”注释。

- [ ] 运行绿灯与回归：

  ```bash
  gofmt -w internal/controlplane/project_service.go internal/controlplane/project_service_test.go internal/controlplane/project_command.go internal/controlplane/repository.go internal/store/projects.go internal/store/projects_test.go internal/store/operations.go internal/store/operations_test.go internal/desktopapi/catalog_assembler.go internal/desktopapi/catalog_assembler_test.go
  go test ./internal/controlplane ./internal/store ./internal/desktopapi
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/controlplane internal/store internal/desktopapi/catalog_assembler.go internal/desktopapi/catalog_assembler_test.go
  git commit -m "feat: create projects with durable location operations"
  ```

### Task 6: 实现 agentd peer 协议、远端同步和全局 control projection

**Files:**
- Create: `internal/peer/protocol.go`
- Create: `internal/peer/client.go`
- Create: `internal/peer/client_test.go`
- Create: `internal/peer/supervisor.go`
- Create: `internal/peer/supervisor_test.go`
- Create: `internal/controlplane/projector.go`
- Create: `internal/controlplane/projector_test.go`
- Create: `internal/store/control_events.go`
- Create: `internal/store/control_events_test.go`
- Create: `internal/agentd/peer_server.go`
- Create: `internal/agentd/peer_server_test.go`
- Modify: `internal/agentd/server.go`
- Modify: `cmd/agentd.go`

**Interfaces:**
- Consumes: Task 4 durable machine-event log and Task 5 target-side Location operations.
- Produces: peer `Hello`, `MachineSnapshot`, `EventsAfter`, stream routes; `(*controlplane.Projector).Apply(context.Context, MachineEvent) (ControlEvent, bool, error)`; and per-Machine `Supervisor` catch-up/reconcile lifecycle.

- [ ] 先写协议测试：hello 返回 protocol version 和 capability map；snapshot 带 through_machine_seq；events after cursor 单调；重复 `(machine_id,machine_seq)` 被忽略；远端断线保留投影但 Machine=`unavailable`；catch-up 完成并 Reconcile 后才=`connected`。

- [ ] 写 bootstrap/stream 竞态底层测试：投影更新与 `control_event` 在同一事务；snapshot revision R 之后发生的事件可从 `after=R` 补齐；`control_revision` 全局单调。

- [ ] 运行红灯：

  ```bash
  go test ./internal/peer ./internal/controlplane ./internal/store ./internal/agentd -run 'Peer|Projector|ControlEvent|CatchUp'
  ```

- [ ] 定义 peer v1 路由：

  ```text
  GET /v1/peer/hello
  GET /v1/machine/snapshot
  GET /v1/machine/events?after=<machine_seq>&limit=<n>
  GET /v1/machine/stream?after=<machine_seq>
  ```

  `Hello` 至少协商 `catalog=1`、`machine_events=1`，后续计划再增加 files/git/pty/tasks/preview。未知 capability 被忽略；依赖 capability 的行为只有双方确认后才启用。

- [ ] `peer.Client` 只能由本机 agentd 构造；credential resolver 通过 Machine.SecretRef 取 config token。所有请求设 connect/header/read deadline；401/403 标 `AUTH_FAILED`，协议不兼容标 `incompatible`，网络错误标 `unavailable`。

- [ ] `Supervisor` 恢复顺序实现为 authenticate → negotiate → catch up → reconcile → connected。每台机器一个串行 worker，按 machine ID 隔离 cursor 和 backoff；不能让一台坏机器阻塞本机或其他远端。

- [ ] `Projector.Apply` 在一个事务内：幂等记录 machine event → 更新 Workspace/GitRef/TaskSummary/Operation 投影 → 追加 ControlEvent → 更新 last_machine_seq。重复事件返回 `applied=false` 且不分配新 revision。

- [ ] 本机 machine event 使用同一 Projector 入口；不得在 desktop handler 中为 local 分支直接查原始表。

- [ ] 非 loopback 明文 HTTP 先按配置 `allow_insecure_private` 显式门控；默认 fail-closed。真正的 TLS/capability hardening 在计划 04 完成，但本计划不得默认接受公网明文 endpoint。

- [ ] 结构化日志覆盖 connect/auth/negotiate/catch-up/reconcile/connected/unavailable，带 machine ID、from/to seq、批次、重复数和耗时，不打 token。

- [ ] 为新文件补职责/边界头注释、导出项文档、mixed-version 和 duplicate-event 的“为什么”注释。

- [ ] 运行绿灯与回归：

  ```bash
  gofmt -w internal/peer internal/controlplane/projector.go internal/controlplane/projector_test.go internal/store/control_events.go internal/store/control_events_test.go internal/agentd/peer_server.go internal/agentd/peer_server_test.go internal/agentd/server.go cmd/agentd.go
  go test ./internal/peer ./internal/controlplane ./internal/store ./internal/agentd ./cmd
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/peer internal/controlplane internal/store internal/agentd cmd/agentd.go
  git commit -m "feat: synchronize agentd machine events into control plane"
  ```

### Task 7: 提供 desktop bootstrap、control stream 和项目 Operation API

**Files:**
- Create: `internal/agentd/desktop_server.go`
- Create: `internal/agentd/desktop_server_test.go`
- Create: `internal/agentd/control_stream.go`
- Create: `internal/agentd/control_stream_test.go`
- Modify: `internal/agentd/server.go`
- Modify: `internal/agentd/server_test.go`
- Modify: `internal/desktopapi/v1.go`
- Modify: `internal/desktopapi/catalog_assembler.go`
- Modify: `internal/desktopapi/catalog_assembler_test.go`

**Interfaces:**
- Consumes: Task 5 `ProjectService`, Task 6 `ControlEvent` projection, and local Bearer authentication.
- Produces: `GET /v1/bootstrap`, `GET /v1/control/events`, `GET /v1/control/stream`, `POST /v1/projects/operations`, and `GET /v1/operations/{operation_id}` with `desktopapi.Problem` failures.

- [ ] 写失败测试覆盖路由、Bearer 鉴权、Problem 线格式、项目创建 202、Operation 查询、bootstrap revision、先订阅后重放、重复去重、慢客户端有界断开和 cursor 参数校验。

- [ ] 运行红灯：

  ```bash
  go test ./internal/agentd -run 'Desktop|Bootstrap|ControlStream|ProjectOperation'
  ```

- [ ] 注册以下本机 desktop 路由；现有 `/api` 与 `/ws` 不改：

  ```text
  GET  /v1/bootstrap
  GET  /v1/control/events?after=<revision>&limit=<n>
  GET  /v1/control/stream?after=<revision>
  POST /v1/projects/operations
  GET  /v1/operations/{operation_id}
  ```

- [ ] `GET /v1/bootstrap` 在一致性读事务里返回快照与 revision R。stream 使用与旧 WS 同样的“先订阅、再补发、按 revision 去重”原则，但使用独立 `ControlHub` 和 durable `control_events`，不能复用 task Hub。

- [ ] `POST /v1/projects/operations` 请求体精确为：

  ```json
  {
    "operation_id": "uuid",
    "name": "super-debug",
    "locations": [
      {"machine_id":"uuid","role":"local","source":"existing_path","path":"/repo"},
      {"machine_id":"uuid","role":"remote","source":"git_clone","git_url":"git@github.com:o/r.git","clone_path":"~/.handoff/r"}
    ]
  }
  ```

  返回 `202` + Operation；同 operation ID 返回现有权威 Operation，不重复执行。

- [ ] handler 只做 decode → `CatalogAssembler` → application service → `CatalogAssembler` → encode；不直接写 store、不手拼 DTO、不承载项目业务校验。错误按 Problem code 映射，用户可修复错误保留具体 message，内部错误只返回安全摘要并在日志记录 cause。

- [ ] 日志覆盖 bootstrap revision、stream after/current、replay 数、慢客户端断开、operation accepted/conflict/completed；所有成功路径可追踪。

- [ ] 为新文件补职责/边界头注释、导出方法文档和 bootstrap/stream 无窗口竞态的“为什么”注释。

- [ ] 运行绿灯与回归：

  ```bash
  gofmt -w internal/agentd/desktop_server.go internal/agentd/desktop_server_test.go internal/agentd/control_stream.go internal/agentd/control_stream_test.go internal/agentd/server.go internal/agentd/server_test.go internal/desktopapi/v1.go internal/desktopapi/catalog_assembler.go internal/desktopapi/catalog_assembler_test.go
  go test ./internal/agentd ./internal/desktopapi
  go test ./...
  ```

- [ ] Commit:

  ```bash
  git add internal/agentd internal/desktopapi
  git commit -m "feat: expose desktop catalog and control stream"
  ```

### Task 8: 建立 Electron Main AgentdClient、窄 preload 和跨语言契约测试

**Files:**
- Create: `desktop/src/shared/handoff/contracts.ts`
- Create: `desktop/src/shared/handoff/contracts.test.ts`
- Create: `desktop/src/main/handoff/agentd-config.ts`
- Create: `desktop/src/main/handoff/agentd-config.test.ts`
- Create: `desktop/src/main/handoff/logger.ts`
- Create: `desktop/src/main/handoff/agentd-client.ts`
- Create: `desktop/src/main/handoff/agentd-client.test.ts`
- Create: `desktop/src/main/handoff/register-handoff-ipc.ts`
- Create: `desktop/src/main/handoff/register-handoff-ipc.test.ts`
- Create: `desktop/src/preload/handoff-api-types.ts`
- Create: `desktop/src/preload/handoff.ts`
- Modify: `desktop/src/main/ipc/register-core-handlers.ts`
- Modify: `desktop/src/preload/index.ts`
- Modify: `desktop/src/renderer/src/env.d.ts`
- Modify: `desktop/config/tsconfig.node.json`
- Modify: `desktop/config/tsconfig.web.json`

**Interfaces:**
- Consumes: Task 7 local desktop HTTP/WS routes and Task 2 Go golden JSON fixtures.
- Produces: `AgentdClient.bootstrap/createProject/getOperation/subscribeControl`, `HandoffRendererAPI.pickLocalDirectory`, and narrow `window.handoff` IPC methods without endpoint/token exposure.

- [ ] 写 Zod 红灯测试，直接读取根仓库 `internal/desktopapi/testdata/*.json`，要求 Bootstrap、ControlEvent、Problem 全部解析；未知字段 strip；缺关键 version/revision 拒绝。

- [ ] 写 AgentdClient 红灯测试：只从 `~/.handoff/config.yaml` 读取 local Listen/Token；renderer 返回值中无 token；HTTP 401 映射 AUTH_FAILED；WS 重连携带最后 revision；unsubscribe 后停止向该 renderer 推送；Main 销毁时释放 socket/timer。

- [ ] 写 Finder IPC 红灯测试：`pickLocalDirectory` 固定调用 `dialog.showOpenDialog({ properties: ['openDirectory'] })`；取消返回 `{ canceled: true }`；成功只返回规范化本机绝对路径；该 IPC 不接收 machine ID、remote path 或任意 dialog options。

- [ ] 运行红灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff src/main/handoff)
  ```

- [ ] `contracts.ts` 定义与 Go golden 一致的 Zod schemas 和推导类型。不要手写第二套未校验 interface；所有 Main 网络响应必须先 parse 再进入 IPC。

- [ ] `agentd-config.ts` 只读取本机配置；不得解析/暴露 `targets.*.token` 给 renderer。测试用注入路径，不直接污染真实 `~/.handoff`。

- [ ] `logger.ts` 提供 `HandoffLogger`，Main 结构化 JSONL 写到 Electron userData 下 `logs/handoff-desktop.log`；字段值先 redaction。新 Handoff 代码不得用 `console.log` 作为日志。

- [ ] `AgentdClient` API 至少包含：

  ```ts
  interface AgentdClient {
    bootstrap(signal?: AbortSignal): Promise<BootstrapResponse>
    createProject(command: CreateProjectCommand): Promise<OperationDto>
    getOperation(operationId: string): Promise<OperationDto>
    subscribeControl(
      after: number,
      onEvent: (event: ControlEventEnvelope) => void,
      onStatus: (status: LocalAgentdConnectionStatus) => void
    ): () => void
  }

  interface HandoffRendererAPI {
    bootstrap(): Promise<BootstrapResponse>
    createProject(command: CreateProjectCommand): Promise<OperationDto>
    getOperation(operationId: string): Promise<OperationDto>
    pickLocalDirectory(): Promise<{ canceled: boolean; path?: string }>
  }
  ```

- [ ] `window.handoff` 只暴露 catalog 方法、本机目录选择和订阅：`bootstrap`、`createProject`、`getOperation`、`pickLocalDirectory`、`onControlEvent`、`onConnectionStatus`。preload 不返回 endpoint/token，不接受任意 URL、远端机器或远端 path。

- [ ] IPC 注册按 sender scope 管理订阅；窗口 destroyed 时清理。Main 记录 request/operation/revision/status，不记录项目表单里的完整 Git URL 或 path 内容。

- [ ] 新文件补职责/边界头注释、导出项文档和 token 只留 Main 的“为什么”注释。

- [ ] 运行绿灯、类型检查和 changed-code checks：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff src/main/handoff src/preload)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  ```

- [ ] Commit:

  ```bash
  git add desktop/src/shared/handoff desktop/src/main/handoff desktop/src/main/ipc/register-core-handlers.ts desktop/src/preload desktop/src/renderer/src/env.d.ts desktop/config
  git commit -m "feat: connect Electron main to local handoff agentd"
  ```

### Task 9: 实现独立 CatalogStore、三栏工作台骨架、项目树与创建项目对话框

**Files:**
- Create: `desktop/src/renderer/src/features/handoff/HandoffApp.tsx`
- Create: `desktop/src/renderer/src/features/handoff/HandoffApp.test.tsx`
- Create: `desktop/src/renderer/src/features/handoff/catalog/catalog-store.ts`
- Create: `desktop/src/renderer/src/features/handoff/catalog/catalog-store.test.ts`
- Create: `desktop/src/renderer/src/features/handoff/catalog/catalog-selectors.ts`
- Create: `desktop/src/renderer/src/features/handoff/catalog/catalog-selectors.test.ts`
- Create: `desktop/src/renderer/src/features/handoff/components/ProjectTree.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/ProjectTree.test.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/WorkbenchShell.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/ProjectCreateDialog.tsx`
- Create: `desktop/src/renderer/src/features/handoff/components/ProjectCreateDialog.test.tsx`
- Create: `desktop/src/renderer/src/features/handoff/testing/handoff-api-fixture.ts`
- Create: `desktop/src/renderer/src/features/handoff/architecture-boundary.test.ts`
- Modify: `desktop/src/renderer/src/main.tsx`
- Modify: `desktop/src/renderer/src/assets/main.css`

**Interfaces:**
- Consumes: Task 8 `window.handoff`, `BootstrapResponse`, `ControlEventEnvelope`, and local Finder result.
- Produces: CatalogStore actions `hydrate/apply/setConnection/selectWorkspace/resetFromGap`, `HandoffApp`, selected `workspace_id`, and the Project → Machine → Workspace → Task tree consumed by Plan 02/03.

- [ ] 写 CatalogStore 红灯测试：bootstrap 原子替换；event 按 revision 严格递增；重复 event 幂等；gap 触发重新 bootstrap 而非猜补；project/machine trailing counts 直接读 TaskSummary/Workspace 投影；Machine unavailable 不删除最后已知子树。

- [ ] 写 UI 红灯测试：层级固定为 Project → Machine/Location → main/worktree → handoff task；点击 Workspace 同时更新 selected workspace、breadcrumb 占位和右栏 root label；点击 task 先选所属 Workspace；project 与 machine 行显示 workspace/running/attention 右侧标识。

- [ ] 写项目创建红灯测试：local/remote 至少一个；remote 单选；Finder 按钮只在 local existing path 出现；remote existing path 只能粘贴；clone URL 必填；clone path 自动预填并可改；提交后显示 Operation 状态而非伪造“已创建”。

- [ ] 写架构守卫测试，扫描 `features/handoff` imports，拒绝包含以下片段：

  ```text
  /ssh, ssh-, Ssh, ProjectHostSetup, store/slices/repos,
  store/slices/worktrees, launch-agent, native-chat
  ```

- [ ] 运行红灯：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff)
  ```

- [ ] `catalog-store.ts` 使用独立 vanilla Zustand store；不要挂进 `useAppStore` 巨型 slice。公开动作只有 `hydrate(snapshot)`、`apply(event)`、`setConnection(status)`、`selectWorkspace(id)`、`resetFromGap()`。

- [ ] `HandoffApp` 作为默认 renderer root；保留上游 Orca `App` 的显式开发 fallback，但 Handoff feature 不导入它。三栏尺寸初始为左 336px、中自适应、右 288px，支持拖拽调整并遵循 Orca token/style guide。

- [ ] `ProjectTree` 使用稳定 ID key；工作区行展示 kind、branch/path 摘要、availability；Task 行展示 executor、状态、attention 与时间。远端 unavailable 时仍展示最后元数据，但行和中/右占位明确写“不可用”，不能写“只读”。

- [ ] `ProjectCreateDialog` 用 shadcn Dialog/Select/Input/Button。Finder 调用 Electron `dialog.showOpenDialog` 的专用 `window.handoff.pickLocalDirectory()`；该方法只返回用户选择结果，最终仍由 agentd InspectPath 校验。

- [ ] control stream 生命周期：先 bootstrap 得到 R，再 subscribe after R；断线状态显示在应用壳；gap 或 CURSOR_EXPIRED 重新 bootstrap 原子替换。renderer 不做网络重试日志，Main 记录，UI 只显示可行动状态。

- [ ] 新文件补职责/边界头注释、导出项文档和“为何不用 Orca 全局 store/Worktree”的原因注释。所有异步失败提供 inline retry 或 toast；不得用 `console.log`。

- [ ] 运行绿灯、类型检查、样式与架构检查：

  ```bash
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff)
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  (cd desktop && corepack pnpm run check:max-lines-ratchet)
  ```

- [ ] Commit:

  ```bash
  git add desktop/src/renderer/src/features/handoff desktop/src/renderer/src/main.tsx desktop/src/renderer/src/assets/main.css desktop/src/main/handoff desktop/src/preload
  git commit -m "feat: add handoff project tree and workbench shell"
  ```

### Task 10: 建立 fake agentd Electron checkpoint 并验收控制面纵切

**Files:**
- Create: `desktop/tests/fixtures/fake-handoff-agentd.ts`
- Create: `desktop/tests/e2e/handoff-catalog.spec.ts`
- Create: `desktop/config/scripts/run-handoff-catalog-e2e.mjs`
- Modify: `desktop/package.json`
- Create: `docs/superpowers/evidence/phase2-checkpoint-01.md`

**Interfaces:**
- Consumes: Task 7 wire contract and Task 8/9 packaged Electron IPC/UI boundary.
- Produces: `pnpm test:e2e:handoff-catalog`, a programmable fake agentd HTTP/WS fixture, and checkpoint-01 evidence with revision/request journals.

- [ ] 先写失败 E2E：fake server 启动后桌面加载项目树；server 推送 workspace.upsert、git_ref.upsert、task.upsert 后 DOM 无刷新更新；断开后保留行但显示不可用；重连 snapshot 修复断线期间漏差；项目对话框提交相同 operation ID 只执行一次。

- [ ] `fake-handoff-agentd.ts` 实现真实 HTTP/WS wire，不直接调用 renderer store。fixture 支持脚本化 revision、gap、disconnect、operation partial，且暴露调用计数供断言。

- [ ] 添加 `pnpm test:e2e:handoff-catalog`，只运行该纵切，固定单 worker，测试结束关闭 fake server 和 Electron。

- [ ] 在 Main/E2E fixture 关键启动、连接、推送、断线和关停处使用结构化测试日志；测试失败保存 screenshot、DOM snapshot 和 fake request journal，不记录 token。

- [ ] 新文件补职责/边界头注释、导出项文档和“必须走真实 wire 而非直接改 store”的原因注释。

- [ ] 运行完整 checkpoint：

  ```bash
  go test ./...
  (cd desktop && corepack pnpm typecheck)
  (cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts)
  (cd desktop && corepack pnpm test:e2e:handoff-catalog)
  (cd desktop && corepack pnpm run check:code-quality:changed)
  (cd desktop && corepack pnpm run check:max-lines-ratchet)
  ```

- [ ] 在 `phase2-checkpoint-01.md` 记录 commit、命令、退出码、fake 场景、截图路径和结构化日志筛选键。不得只写“通过”。

- [ ] 对照设计规格确认：桌面只连 local agentd；项目 Location 约束正确；新 branch/worktree/task 能推送；没有实现文件/PTY/TUI 临时代码；无 Orca SSH import。

- [ ] Commit:

  ```bash
  git add desktop/tests/fixtures/fake-handoff-agentd.ts desktop/tests/e2e/handoff-catalog.spec.ts desktop/config/scripts/run-handoff-catalog-e2e.mjs desktop/package.json docs/superpowers/evidence/phase2-checkpoint-01.md
  git commit -m "test: prove handoff control plane desktop checkpoint"
  ```

## Plan 01 Completion Gate

- [ ] `git status --short` 只显示明确保留的用户未跟踪项，没有测试产物或真实 token。
- [ ] `go test ./...`、desktop typecheck、聚焦 Vitest、catalog Playwright 和 changed-code checks 均以当前 commit 新跑并通过。
- [ ] bootstrap/control stream 没有窗口丢失；machine_seq/control_revision 单调且重复幂等。
- [ ] 外部 Git worktree/branch 与 dispatch Task 都能经 durable outbox 出现在左栏。
- [ ] 项目创建满足 local `0..1` + remote `0..1` + total `>=1`，并保留 partial 现场。
- [ ] 结构化日志和代码注释按 instrumenting-code 清单复核；成功路径不静默，错误有 machine/workspace/operation/cursor 上下文。
- [ ] 独立规格审阅确认本 checkpoint 没有把资源 API 或 TaskTUI 偷塞进来，也没有把 agentd 映射成 Orca Worktree/SSH。
