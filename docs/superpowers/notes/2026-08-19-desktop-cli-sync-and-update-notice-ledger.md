# 桌面端与 CLI 版本同步 + 新版通知执行 ledger

起点：`67cc7a74`

范围：Task 2–Task 11；Task 1 与 Task 12 由审核者负责。

- Task 2 完成；双裁决第 1 轮通过（spec 符合、代码质量通过）；字典序变异按预期击穿，已恢复。commit 范围：`internal/selfupdate/`、`desktop/internal/shell/release.go`、`desktop/internal/shell/release_test.go`、本 ledger。
- Task 3 完成；双裁决第 1 轮通过（改名、四态判据、保守 busy 判据均符合）；两条变异均按预期击穿，已恢复。`desktop` 全量测试与 gofmt 通过；根全量测试未通过，原始失败为既有 `internal/executor/claudecode` 的 Unix socket 路径超长（`路径过长（120/122 字节，上限 107）`）。commit 范围：`desktop/internal/shell/`、`desktop/main.go`、本 ledger。
- Task 4 完成；双裁决第 1 轮通过（TAG 去 v 注入两个 Info.plist 版本键且先于实际 `package` 构建）；顺序变异与删除 `CFBundleVersion` 变异均按预期击穿，已恢复。根包测试与 gofmt 通过。commit 范围：`.github/workflows/release.yml`、`release_workflow_test.go`、本 ledger。
- Task 5 完成；双裁决第 1 轮通过（DoSync 四步顺序、skill 非致命、Activate 失败停止、临时文件清理均符合）；四条变异均按预期击穿，已恢复。`desktop` 全量测试与 gofmt 通过；根全量测试仍受既有 `internal/executor/claudecode` Unix socket 路径超长失败（原始为 `路径过长（119/121/122 字节，上限 107）`）。commit 范围：`desktop/internal/shell/sync.go`、`desktop/internal/shell/sync_test.go`、本 ledger。
- Task 6 完成；双裁决第 1 轮通过（版本相等、探测后只催一次、取消/90 秒 deadline 错误均符合）；四个原计划变异均被击穿，其中两个原测试假门已分别改写为三次探测与可注入时钟后重新验证，已恢复。`desktop` 全量测试与 gofmt 通过。GUI/真机未涉及。commit 范围：`desktop/internal/shell/waitback.go`、`desktop/internal/shell/waitback_test.go`、`desktop/internal/shell/waitback_internal_test.go`、本 ledger。
- Task 7 完成；双裁决第 1 轮通过（`EnsureRunning → Busy → PlanSync → DoSync → WaitAgentdBack` 顺序、D8 错误折返与 `openConsole` 装配均符合）；四条计划变异均按预期击穿，其中 Busy 探测失败的假门通过补断言 `SyncBlocked` 修正后重新验证，已恢复。`desktop` 构建、全量测试与 gofmt 通过。GUI 步骤未验（需图形会话，已划归审核者）。commit 范围：`desktop/internal/shell/open_sync.go`、`desktop/internal/shell/open_sync_test.go`、`desktop/main.go`、本 ledger。
- Task 8 完成；双裁决第 1 轮通过（未知版本与网络失败静默、24h 缓存复用/回写、方向比较统一走 `CompareVersion`）；四条变异均击穿并已恢复（方向变异在编译期即失败，其他三条由对应测试断言击穿）。`desktop` 全量测试与 gofmt 通过。commit 范围：`desktop/internal/shell/latest.go`、`desktop/internal/shell/latest_test.go`、本 ledger。
- Task 9 完成；双裁决第 1 轮通过（Vite 多页入口、升级面板事件通道、独立 `WindowRuntimeReady` 与一次性就绪等待均符合）；`npm run build`、`ls dist/upgrade.html`、桌面构建与 gofmt 通过，并补足面板顶部拖动区与主体禁拖样式。GUI 肉眼步骤未验（需图形会话，已划归审核者）。commit 范围：`desktop/frontend/vite.config.ts`、`desktop/frontend/upgrade.html`、`desktop/frontend/src/upgrade.ts`、`desktop/frontend/public/style.css`、`desktop/panel.go`、本 ledger。
- Task 10 完成；双裁决第 1 轮通过（托盘状态集中重建、阻塞/失败/新版三类条目、强制同步面板与后台新版检查均符合）；桌面构建、vet、全量测试与 gofmt 通过。为解决计划中 Task 10 引用 Task 11 尚未提供的 `runRemoteUpgrade`，本 task 暂留可编译占位接线，Task 11 将在同名实现落地时删除；配置文件不存在时新版检查只读跳过，避免 `config.Load` 的首次运行写盘副作用。托盘 GUI 未验（需图形会话，已划归审核者）。commit 范围：`desktop/main.go`、本 ledger。
- Task 11 完成；双裁决第 1 轮通过（CLI 编排不重建、stdout/stderr 逐行合流、非零退出码与无法启动错误分离）；三条计划变异均按预期击穿，并补加了时序断言杀掉「最后一次性回调」假门后恢复。桌面构建、vet、全量测试与 gofmt 通过。真实托盘点击及 `--force` GUI 步骤未验（需图形会话，已划归审核者）。commit 范围：`desktop/remote_upgrade.go`、`desktop/remote_upgrade_test.go`、`desktop/main.go`、本 ledger。
- 终审第 1 轮完成；发现并修复生产路径把同步等待误受 30 秒 webview 上下文限制的问题，现以独立 90 秒同步上下文保留 `WaitAgentdBack` 契约；同时补齐 `sync_test.go` 文件头注释。修复后桌面构建、vet、全量测试与 gofmt 再次通过。commit 范围：`desktop/main.go`、`desktop/internal/shell/sync_test.go`、本 ledger。
