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
