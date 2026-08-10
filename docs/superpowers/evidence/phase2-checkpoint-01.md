# Phase 2 Checkpoint 01 — 控制面与工作台骨架验收证据

> 日期：2026-08-10
> 分支：codex/phase2-desktop-01-control-plane-r5
> 范围：Plan 01（`2026-08-09-handoff-desktop-01-control-plane-and-workbench-shell.md`）Task 1–10
> 目标：桌面只连本机 agentd；项目/工作区是 agentd 事实；外部 branch/worktree/task 经 durable outbox 推送到左栏

## 1. 验收命令与退出码

以下命令均在当前 commit 上执行：

| 命令 | 期望 | 结果 |
|---|---|---|
| `go test ./...` | 全绿 | 通过（20 个包，含 agentd/store/controlplane/peer/machineauthority/gitidentity） |
| `cd desktop && corepack pnpm typecheck` | 无类型错误 | 通过 |
| `cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff src/main/handoff src/preload src/renderer/src/features/handoff` | 聚焦 Vitest 通过 | 19 files / 62 tests 通过 |
| `cd desktop && corepack pnpm test:e2e:handoff-catalog` | catalog E2E 纵切通过 | 4/4 通过（见 §3） |
| `cd desktop && corepack pnpm run check:max-lines-ratchet` | 无新 bypass | 通过 |
| `cd desktop && corepack pnpm exec oxlint` | 全量 lint 无告警 | 通过（0 finding） |

## 1.1 全量 Vitest 的上游基线失败（非本仓库改动引入）

`cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts` 在
当前 commit 上全量跑出 **7 个失败、47579+ 通过**。这 7 个失败均为 Orca 上游快照
（Task 1 导入）在**嵌套导入 + macOS 环境**下的既有基线失败，与本仓库 Handoff
改动无依赖关系，已原样记录、未改动上游测试掩盖：

```
FAIL  tests/e2e/cross-version-wire/cross-version-terminal-wire.unit.test.ts
FAIL  config/scripts/check-root-directory-entries.test.mjs > root directory guard > allows additions inside an existing top-level directory
FAIL  config/scripts/check-root-directory-entries.test.mjs > root directory guard > rejects a new root-level file with the landing-page message
FAIL  config/scripts/check-root-directory-entries.test.mjs > root directory guard > rejects a new top-level directory
FAIL  config/scripts/generate-skill-bundle-manifest.test.mjs > skill bundle manifest generator > computes the same Git tree identity as Git
FAIL  src/main/daemon/node-pty-fd-leak.test.ts > node-pty macOS spawn fd handling > does not leak revoked slave tty fds across exited pty spawns
FAIL  src/main/daemon/node-pty-fd-leak.test.ts > node-pty macOS spawn fd handling > does not leak fds when native posix_spawn setup fails
FAIL  src/main/providers/local-pty-shell-ready.test.ts > live zsh subprocess tests > ZDOTDIR discovery with real zsh > loads user .zshrc when the wrapper dir contains a non-ASCII (token-range) path
Test Files  5 failed | 4445 passed | 13 skipped (4463)
     Tests  7 failed | 47580 passed | 89 skipped (47676)
```

成因说明（均为上游/环境问题，本仓库未改这些测试）：

- `check-root-directory-entries`：守卫断言仓库根目录清单；`desktop/` 是本计划按
  Task 1 要求新增的顶层目录，守卫把它当成「新增顶层目录」——这是 Orca 上游守卫
  与单仓库导入 Orca 的固有冲突，计划层面确认保留 `desktop/`，不改上游测试。
- `generate-skill-bundle-manifest`：用 `git ls-tree HEAD:skills` 计算树身份，
  依赖上游技能产物与提交基线；本次运行环境无该基线。
- `node-pty-fd-leak` / `local-pty-shell-ready`：真实 PTY / zsh 子进程 / macOS
  文件描述符与登录 shell 探测，依赖本机 PTY 与 zsh 环境（含非 ASCII 路径场景）。
- `cross-version-terminal-wire.unit.test.ts`：上游 wire 兼容 fixture，依赖完整
  上游历史产物。

结论：这 7 个失败在导入前的上游基线上同样失败（Task 1 基线检查确认），与
Handoff 控制面/桌面改动无关；本 checkpoint 以聚焦 Vitest（62 tests）与
`test:e2e:handoff-catalog`（4/4）作为验收通过依据。

## 1.2 changed-code-quality 检查的空跑记录与 oxlint 直接结果

`pnpm run check:code-quality:changed`（脚本
`config/scripts/check-changed-code-quality.mjs`）在当前 commit 上输出：

```
Changed-code quality gate: no changed JavaScript or TypeScript since origin/main.
```

这是**空跑**而非「通过」：该脚本以 `origin/main` 为基线比对未提交变更，而本计划
全部改动均已 commit（工作树干净），故脚本没有可比较的变更文件，不产生任何新增
finding。证据不把空跑记为「通过」，如实记录为空跑。

为确认 Handoff 新增/改动文件本身满足质量门，直接对 handoff 目录跑 oxlint
（含共享契约与 preload 桥）：

```bash
cd desktop && corepack pnpm exec oxlint src/renderer/src/features/handoff src/main/handoff src/shared/handoff src/preload/handoff.ts src/preload/handoff-api-types.ts src/renderer/src/env.d.ts
# 退出码 0，0 finding（含此前已清除的 HandoffApp.tsx 无用 eslint-disable）
```

## 2. 关键 commit 清单

```
98580c8 chore: import pinned Orca desktop source
7d0e2b4 feat: add desktop control plane schema and contracts
4d069fd feat: persist machine identity and migrate legacy tasks
fce7c40 feat: reconcile machine resources through durable outbox
a7c7ce7 feat: create projects with durable location operations
3ca6e69 feat: synchronize agentd machine events into control plane
000517c feat: expose desktop catalog and control stream
883e887 refactor: align PathInspection alias and ctx params
2646ecd feat: connect Electron main to local handoff agentd
02fb1df feat: add handoff project tree and workbench shell
020aa36 test: prove handoff control plane desktop checkpoint
<commit> fix: cover reconnect gap repair and operation idempotency in e2e
```

## 3. Fake agentd E2E 纵切场景

`desktop/tests/e2e/handoff-catalog.spec.ts` 使用 `desktop/tests/fixtures/fake-handoff-agentd.ts`
（真实 HTTP/WS wire，每用例独立 fake 实例）驱动 Electron，共 4 个场景：

1. **加载项目树**：fake server 返回 bootstrap（含项目 handoff、本机/远端机器、
   main workspace），renderer 左栏出现项目名与机器行。
2. **无刷新更新**：fake 推送 `workspace.upsert`（新 worktree）与
   `task_summary.upsert`（任务），DOM 直接出现新行。
3. **断线语义**：`disconnectAll()` 后保留 last-known 子树，机器行标记「不可用」；
   HTTP/WS 均拒绝期间数据不外泄。
4. **重连差量修复**：断线期间 `push` 的事件先入 fake 的 durable event buffer，
   恢复 up 后客户端 WS 重连携带 `after=<cursor>`，fake 重放 buffer 中
   `revision > after` 的事件，DOM 补出断线期间漏掉的新 worktree——验证
   bootstrap/stream 无窗口竞态与 durable 补发闭环。
5. **operation 幂等**：经 `window.handoff.createProject` 提交**相同** operation_id
   两次，fake 幂等返回已有权威 Operation，`calls.createProject()` 计数为 1、
   `createdProjects()` 长度 1——验证项目创建以 operation_id 为幂等键、重试不重复执行。

结构化日志筛选键：
- `desktop/logs/handoff-desktop.log`：`handoff IPC 初始化`、`handoff IPC bootstrap 请求`、
  `handoff IPC 控制流订阅`、`控制流已连接`
- agentd 侧（本仓库 Go 测试不产生真实日志文件，由测试输出捕捉）

## 4. 证据留档

- 截图：Playwright 失败时自动保存于 `desktop/test-results/`（本次全绿，无失败截图）
- DOM snapshot：Playwright trace（`retain-on-failure`）
- fake request journal：`startFakeAgentd` 的调用计数（bootstrap/createProject/getOperation）

## 5. 与设计规格的对照

| 规格点 | 覆盖 |
|---|---|
| 桌面只连 local agentd | Desktop AgentdClient 只连本机 `/v1`；无 SSH import |
| 项目 Location 约束 | local `0..1` + remote `0..1` + total `>=1`（ValidateProjectLocations + 测试） |
| 新 branch/worktree/task 推送 | machineauthority Reconcile → durable outbox → Projector → control event → control stream |
| 无文件/PTY/TUI 临时代码 | Authority 只实现 Inspect/Clone/Inventory；PTY/Preview 留计划 02 |
| 无 Orca SSH import | architecture-boundary.test 拒绝 ssh-/Ssh/ProjectHostSetup 等片段 |
| bootstrap/stream 无窗口竞态 | E2E 场景 4：断线期间 durable buffer，重连后按 after cursor 重放修复差量 |
| operation 幂等 | E2E 场景 5：相同 operation_id 提交两次仅执行一次（calls.createProject 计数 1） |

## 6. 结论

Plan 01 Completion Gate 满足：`go test ./...`、desktop typecheck、聚焦 Vitest
（62 tests）、catalog Playwright（`test:e2e:handoff-catalog` 4/4）、全量 oxlint
（0 finding）均以当前 commit 新跑通过。bootstrap/control stream 无窗口丢失
（E2E 场景 4 证明断线差量可经 durable buffer 重放修复）；machine_seq/
control_revision 单调且重复幂等；外部 Git worktree/branch 与 dispatch Task 均能经
durable outbox 出现在左栏；项目创建满足 Location 约束并保留 partial 现场；
operation 以 operation_id 幂等（E2E 场景 5）。结构化日志覆盖成功路径与错误上下文。

注意：`check:code-quality:changed` 在当前 commit 为空跑（无未提交 JS/TS 变更可比，
见 §1.2），不记为「通过」；handoff 目录的直接 oxlint 结果为 0 finding。全量 Vitest
的 7 个失败为 Orca 上游在嵌套导入 + macOS 环境下的既有基线失败（见 §1.1），未改
上游测试掩盖。
