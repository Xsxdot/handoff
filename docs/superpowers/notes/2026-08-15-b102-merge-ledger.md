# B102 并线合并 执行 ledger

职责与边界：这是 B102 并线（把 w4 控制台线合回 main）的执行 ledger，记录合并过程、取舍解法、验证证据。恢复现场以本 ledger + git log 为准。

分支：`integration/w4-main-r2`
范围：B102 五个 task 的合并、改名、冲突解法与收口；恢复现场请以本 ledger + `git log` 为准。

## 纪律

- 每个 task 完成即 commit，提交信息说清做了什么。不 push、不合并进 main、不动 tag、不切分支。
- 每处「取舍型」冲突记录文件、两边原状、取了哪边、理由。
- 审核者裁决的删除项必须单列记录授权链。
- 冒烟用临时 datadir + 临时端口，不碰 `~/.handoff`（服役中 agentd 数据目录）。

## 进度

- [x] Task 1: 合入 w4-delivery、改名 import 路径、解冲突（commit 3202e9ff）
- [x] Task 2: 修 reclaim hostGuard 403 + client 拷贝锁；config update.* 取 main 废弃语义（commit 1474a6fd + c9b1dee2）
- [x] Task 3: backlog 合表（commit 4d663888）
- [x] Task 4: 零改动，三件套初始即全绿（无 commit）
- [x] Task 5: 端点存在性冒烟 + ledger 收口（本提交）

## 各 task 完成时间与 commit 范围

- **Task 1**: commit `3202e9ff`（合并 + 改名 + 解 21 处冲突）。计划预判 14 冲突文件，实测 21 个；试合并时用了陈旧 w4 引用 de1173c06，审核者裁决按 21 继续。7 个多余文件在 B92/B93/B99 区域，口径收紧为「main 侧为准，w4 纯新增才叠加」。
- **Task 2**: commit `1474a6fd`（修 reclaim hostGuard 403 + client 拷贝锁；点名七条用例 PASS）+ commit `c9b1dee2`（config update.* 取 main 废弃语义，审核者裁决）。
- **Task 3**: commit `4d663888`（backlog 合表——两线原 ID 保留，新增「线」列，新号从 B103 起）。
- **Task 4**: 无 commit（零改动，三件套初始即全绿）。
- **Task 5**: 本提交（冒烟通过 + ledger 收口）。

## 取舍型冲突解法

- **server.go**（byTask 包裹取舍）：w4 侧按 task 包裹的 byTask 结构 + 补 main 独有 reclaim 路由。
- **config.go**（proc_fence 全留 + w4 EnvForward/Web/Update）：proc_fence 全留；EnvForward/Web/Update 取 w4；Update 字段经工单取 main 废弃语义（见下节）。
- **workspace.go**（ReadFile 截断取 w4 FileRead 签名）：取 w4 的 FileRead 签名（含截断语义）。
- **client.go**（cursorTempTTL 去重取 main，PtySessions 留 w4）：cursorTempTTL 去重后取 main；PtySessions 留 w4。
- **footprint.go**（main 的 rosterKill/B93 语义 + w4 CountGroup 都留）：main 的 rosterKill/B93 语义与 w4 CountGroup 均保留。
- **cmd/tasks.go**（proto import 取 w4）：proto import 路径取 w4。
- **7 个 B92/B93/B99 区域文件**：
  - grok/codex `adapter.go` 取 main 侧（emitFailed → emitTurnFailed/emitFatal/evClosed 拆分）。
  - `export_test.go` 取 main + 叠 w4 导出。
  - `manager.go`/`manager_test.go` 取 main 为底，叠 w4 纯新增（usageMu/lastUsage）。
  - `workspace_minor_test.go` 两边用例都留。

## config update.* 授权删除一节

审核者裁决：以下 5 条用例授权删除：

- TestUpdateDefaults
- TestUpdateExplicit
- TestUpdateIntervalMustBePositiveWhenAuto
- TestUpdateIntervalNotCheckedWhenAutoOff
- TestUnknownFieldMessageListsUpdate

删除理由：**「断言的行为已在 main 线被明确废除」**。

决策链 commit：
- `f7c04b6d9`（加 update 段）
- `57e50119a`（删自动更新循环，字段标废弃）
- `bbf16034e`（删除 update.*，旧文件剥键后仍能启动）
- `2b22f8c7f`（docs 去 update 说明）

处置：Config 删 Update 字段与 interval 校验，剥键回写（removeMapKey("update")）保留。

## Task 2 Step 4 七条点名用例实际输出（PASS 行原文）

```
--- PASS: TestTurnFailureKeepsEventChannelOpen (grok) 与 (codex) 各一
--- PASS: TestSendRefusesOnClosedChannel (grok) 与 (codex) 各一
--- PASS: TestHandleResultSweepsProcsOnFail
--- PASS: TestHandleResultSweepsProcsOnSuccess
--- PASS: TestScanTaskProcsWarnsOnceAtBudget
--- PASS: TestScanTaskProcsRearmsAfterFallback
--- PASS: TestDoneIsIdempotentOnCompleted
```

## Task 5 Step 2 三个端点实际状态码（实测）

```
/api/projects/tree?scope=all -> 200
/api/machines -> 200
/api/tasks -> 200
```

三个端点均非 405（200），路由注册完整，无 BLOCKED。

## 过程中发现但没有修的问题（只记账）

- `internal/proto/contract_fixture_test.go:204/221/314` 有 3 处 "xushixin" 样本值（SSH clone URL 与设备名），属 w4 契约 fixture 有意保留，不改。
- Task 1 取舍注释未全覆盖（manager.go 指纹、adapter.go emit 拆分的取舍点无内联「合并 B102」注释）。
- Task 3 审查 minor 3 条（B86-B90 排版扶正、编号说明措辞、待验证空白取 main 版）。
