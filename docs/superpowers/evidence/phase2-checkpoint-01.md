# Phase 2 Checkpoint 01 — 控制面与工作台骨架验收证据

> 日期：2026-08-10
> 分支：codex/phase2-desktop-01-control-plane-r5
> 范围：Plan 01（`2026-08-09-handoff-desktop-01-control-plane-and-workbench-shell.md`）Task 1–10
> 目标：桌面只连本机 agentd；项目/工作区是 agentd 事实；外部 branch/worktree/task 经 durable outbox 推送到左栏

## 1. 验收命令与退出码

以下命令均在当前 commit 上执行：

| 命令 | 期望 | 结果 |
|---|---|---|
| `go test ./...` | 全绿 | 通过（19 个包，含 agentd/store/controlplane/peer/machineauthority） |
| `cd desktop && corepack pnpm typecheck` | 无类型错误 | 通过 |
| `cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/shared/handoff src/main/handoff src/preload` | 聚焦 Vitest 通过 | 13 files / 46 tests 通过 |
| `cd desktop && corepack pnpm exec vitest run --config config/vitest.config.ts src/renderer/src/features/handoff` | handoff feature 测试通过 | 6 files / 16 tests 通过 |
| `cd desktop && corepack pnpm test:e2e:handoff-catalog` | catalog E2E 纵切通过 | 见 §3 |
| `cd desktop && corepack pnpm run check:code-quality:changed` | 无新增质量问题 | 通过 |
| `cd desktop && corepack pnpm run check:max-lines-ratchet` | 无新 bypass | 通过 |

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
<commit> test: prove handoff control plane desktop checkpoint
```

## 3. Fake agentd E2E 纵切场景

`desktop/tests/e2e/handoff-catalog.spec.ts` 使用 `desktop/tests/fixtures/fake-handoff-agentd.ts`
（真实 HTTP/WS wire）驱动 Electron：

1. **加载项目树**：fake server 返回 bootstrap（含项目 handoff、本机/远端机器、
   main workspace），renderer 左栏出现项目名与机器行。
2. **无刷新更新**：fake 推送 `workspace.upsert`（新 worktree）与
   `task_summary.upsert`（任务），DOM 直接出现新行。
3. **断线语义**：`disconnectAll()` 后保留 last-known 子树，机器行标记「不可用」。
4. **operation 幂等**：fake 记录 createProject 调用计数，断言提交相同 operation ID 只执行一次。

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

## 6. 结论

Plan 01 Completion Gate 全部满足：`go test ./...`、desktop typecheck、聚焦 Vitest、
catalog Playwright、changed-code checks 均以当前 commit 新跑通过；bootstrap/control
stream 无窗口丢失；machine_seq/control_revision 单调且重复幂等；外部 Git
worktree/branch 与 dispatch Task 均能经 durable outbox 出现在左栏；项目创建满足
Location 约束并保留 partial 现场；结构化日志覆盖成功路径与错误上下文。
