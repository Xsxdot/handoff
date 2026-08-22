# 需求 A T6/T7 执行 ledger

基线分支：`feat/timing-t6-t7`；基线提交：`2450d3a7`。

## 进度

- 2026-08-22 基线核验：`go build ./... && go test ./internal/store/ ./internal/proto/`、`cd web && npx tsc -b`、`npx vitest run src/app/task src/api` 均实跑通过；工作树干净。
- 2026-08-22 T6 RED：先落 `timing_agg_test.go`，`go test ./internal/store/ -run 'TestAggregateTiming|TestTaskTimingWiring'` 原始编译红为 `undefined: timingRow`、`undefined: aggregateTiming`。
- 2026-08-22 T6 完成：实现 `timing_agg.go` 的三分法、回合区间并集、Partial、排行/下钻与 `TaskTiming` 读路径；删除 store 空壳并接入 `GetTask`，保留 `ListTasks` 不填；补 Task/Frame 契约夹具并显式清空 TasksResp 的 timing。T6 双裁决通过：规格覆盖与代码质量均通过；`go test ./internal/store/ -run 'TestAggregateTiming|TestTaskTimingWiring' -count=1`、`go test ./internal/store/ -count=1`、`go test ./internal/proto/ -run TestContractFixtures -update -count=1` 实测通过，`gofmt -l internal/store internal/proto` 无输出。commit 范围：`internal/store/timing_agg.go`、`internal/store/timing_agg_test.go`、`internal/store/store.go`、`internal/proto/contract_fixture_test.go`、`web/src/api/testdata/{Task,Frame}.json`、本 ledger；提交信息按计划为 `feat(store): 耗时账本三分法聚合与 GetTask 接线（T6）`。
- 2026-08-22 T7-1 RED：依赖安装后运行 `npx vitest run src/app/lib/format.test.ts src/app/task/frames.test.ts`，原始失败为 `TypeError: formatDuration is not a function` 与 `durMS` 期望 1500/42 实得 `undefined`。
- 2026-08-22 T7-1 完成：新增 `formatDuration` 四档格式化，`ToolBlock.durMS?` 只从 `tool_result.dur_ms` 接线并保持缺席为 undefined。T7-1 双裁决通过：规格覆盖与代码质量均通过；`npx tsc -b`、`npx vitest run src/app/lib/format.test.ts src/app/task/frames.test.ts` 实测通过（2 files / 50 tests）。commit 范围：`web/src/app/lib/format.{ts,test.ts}`、`web/src/app/task/frames.{ts,test.ts}`、本 ledger；提交信息按计划为 `feat(web): tool_result 的 dur_ms 进 ToolBlock，新增 formatDuration（T7-1）`。
- 2026-08-22 T7-2 RED：先加组件/契约断言，`npx vitest run src/app/task/TimingChip.test.tsx src/app/task/TuiHeader.test.tsx src/api/contract.test.ts` 原始失败为 `Failed to resolve import "./TimingChip"`，以及页头找不到 `/耗时 3m4s/` 和文本缺少 `耗时`。
- 2026-08-22 T7-2 完成：新增 `TimingChip` 三分法面板与单层排行，ToolCard 显示可选单次耗时，TuiHeader 挂载耗时 chip 并同步分隔点，补齐 Task/TasksResp/Frame TS 契约断言。T7-2 双裁决通过：规格覆盖与代码质量均通过；`npx tsc -b`、`npx vitest run src/app/task src/api` 实测通过（24 files / 233 tests）。commit 范围：`web/src/app/task/TimingChip.{tsx,test.tsx}`、`web/src/app/task/{ToolCard,TuiHeader}.{tsx,test.tsx}`、`web/src/api/contract.test.ts`、本 ledger；提交信息按计划为 `feat(web): 工具卡单次耗时与页头耗时面板（T7-2）`。

## Task 4 收口

- 全量门禁：`gofmt -l . | grep -v '^web/'` 无输出；`go build ./...` 通过；`go test ./...` 失败仅为既有 `internal/executor/claudecode` 的 `TestPermServerAskThenRespond`/`TestPermServerRespondUnknownID`/`TestPermServerReRegisterSameID` Unix socket 路径 114/115/116 字节超过 107，以及 `TestResumeContinuesFromOffset` 同类 115 字节路径错误；Web `npx tsc -b && npx vitest run && npm run lint` 通过（114 files / 1100 tests，lint 0 errors / 17 existing warnings）。契约二次确认 `go test ./internal/proto/ -run TestContractFixtures -count=1` 通过，`git status --short web/src/api/testdata/` 无输出。
- 变异 1（并集改时长求和）：`go test ./internal/store/ -run TestAggregateTimingConcurrentTools -count=1` 失败首行 `--- FAIL: TestAggregateTimingConcurrentTools (0.00s)`；已恢复。
- 变异 2（turn 赋值改累加）：首次按计划运行 `TestTaskTimingWiring` 为 `ok`，暴露重复 turn 行未被覆盖；补断言后同命令失败首行 `--- FAIL: TestTaskTimingWiring (0.12s)`，诊断为 `同回合重复 turn 行应取最终值 2000，实际 3000`；已恢复。
- 变异 3（未知 kind 不置 Partial）：`go test ./internal/store/ -run TestAggregateTimingPartialUnknownKind -count=1` 失败首行 `--- FAIL: TestAggregateTimingPartialUnknownKind (0.00s)`；已恢复。
- 变异 4（同耗时排序不按 Label）：`go test ./internal/store/ -run TestAggregateTimingDeterministicOrder -count=1` 失败首行 `--- FAIL: TestAggregateTimingDeterministicOrder (0.00s)`；已恢复。
- 变异 5（删除 bucket 截断）：`go test ./internal/store/ -run TestAggregateTimingBucketCap -count=1` 失败首行 `--- FAIL: TestAggregateTimingBucketCap (0.00s)`；已恢复。
- 变异 6（删除 GetTask 的 Timing 接线）：`go test ./internal/store/ -run TestTaskTimingWiring -count=1` 编译失败首行 `# github.com/Xsxdot/handoff/internal/store`，随后报 `declared and not used: tm`；已恢复。
- 变异 7（ListTasks 填 Timing）：`go test ./internal/store/ -run TestTaskTimingWiring -count=1` 失败首行 `--- FAIL: TestTaskTimingWiring (0.05s)`，诊断为 `ListTasks 不得填 Timing`；已恢复。
- 变异 8（缺失 dur_ms 兜底为 0）：`npx vitest run src/app/task/frames.test.ts` 失败首行 `❯ src/app/task/frames.test.ts (29 tests | 1 failed)`，诊断为 `expected +0 to be undefined`；已恢复。
- 变异 9（tool_call 读取 dur_ms）：同一命令失败首行 `❯ src/app/task/frames.test.ts (29 tests | 1 failed)`，诊断为 `expected 999999 to be undefined`；已恢复。
- 变异 10（删除 ToolCard 耗时块）：`npx vitest run src/app/task/blocks.test.tsx` 失败首行 `❯ src/app/task/blocks.test.tsx (20 tests | 1 failed)`，缺少 `1.5s`；已恢复。
- 变异 11（ToolCard 无条件显示并用 0 兜底）：同一命令失败首行 `❯ src/app/task/blocks.test.tsx (20 tests | 1 failed)`，诊断为缺席耗时出现 `0ms`；已恢复。
- 变异 12（TimingChip 删除工具时长合计行）：`npx vitest run src/app/task/TimingChip.test.tsx` 失败首行 `❯ src/app/task/TimingChip.test.tsx (10 tests | 1 failed)`，缺少 `1m11s`；已恢复。
- 变异 13（partial 提示改 false）：同一命令失败首行 `❯ src/app/task/TimingChip.test.tsx (10 tests | 1 failed)`，缺少 `/账目不全/`；已恢复。
- 变异 14（缺席 timing 不早退）：同一命令失败首行 `❯ src/app/task/TimingChip.test.tsx (10 tests | 1 failed)`，原始异常为 `Cannot read properties of undefined (reading 'total_ms')`；已恢复。
- 变异 15（taskSample 删除 Timing）：先跑 `go test ./internal/proto/` 失败首行 `--- FAIL: TestContractFixtures (0.00s)`；临时 `-update` 生成缺 timing 的 Task.json 后，`npx vitest run src/api/contract.test.ts` 失败首行 `❯ src/api/contract.test.ts (34 tests | 2 failed)`，两端均红；已恢复源码并重生成原夹具。
- 变异 16（TasksResp 清空 Timing 的两行删除）：先跑 `go test ./internal/proto/ -count=1` 失败首行 `--- FAIL: TestContractFixtures (0.00s)`；临时 `-update` 生成带 timing 的 TasksResp.json 后，`npx vitest run src/api/contract.test.ts` 失败首行 `❯ src/api/contract.test.ts (34 tests | 1 failed)`，两端均红；已恢复源码并重生成原夹具。
- 收口修复轮 1：为变异 2 补同回合重复 turn 行覆盖断言；变异重跑红后恢复生产实现。修复范围：`internal/store/timing_agg_test.go`；commit 范围待收口提交。
- 整分支终审（相对 `2450d3a7`）：`git diff --check` 通过，文件范围仅含本计划的 ledger、T6 store/proto/fixtures、T7 web 纯函数/组件/契约测试与 fixtures；无残留变异（`false &&`、`if (false)`、`dur_ms ?? 0` 均无输出）。终审复跑 `gofmt -l internal/store internal/proto` 无输出、`go test ./internal/store/ -count=1`、`go test ./internal/proto/ -count=1`、`npx tsc -b`、`npx vitest run src/app/task src/api`（24 files / 233 tests）与 `npm run lint`（0 errors）均通过。特殊项：三 task 均无新增日志（读路径高频、前端不打 console）；`TaskTiming` 用 `s.db.QueryContext`，与 `TaskCumulative` 一致，本包无 DAO/`mvc.ExtractDB` 分层。收口 commit 范围：相对基线的完整实现、测试、夹具与本 ledger；提交信息为 `test(timing): 收口耗时聚合与 TUI 展示`。
