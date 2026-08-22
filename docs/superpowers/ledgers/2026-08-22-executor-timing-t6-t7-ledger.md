# 需求 A T6/T7 执行 ledger

基线分支：`feat/timing-t6-t7`；基线提交：`2450d3a7`。

## 进度

- 2026-08-22 基线核验：`go build ./... && go test ./internal/store/ ./internal/proto/`、`cd web && npx tsc -b`、`npx vitest run src/app/task src/api` 均实跑通过；工作树干净。
- 2026-08-22 T6 RED：先落 `timing_agg_test.go`，`go test ./internal/store/ -run 'TestAggregateTiming|TestTaskTimingWiring'` 原始编译红为 `undefined: timingRow`、`undefined: aggregateTiming`。
- 2026-08-22 T6 完成：实现 `timing_agg.go` 的三分法、回合区间并集、Partial、排行/下钻与 `TaskTiming` 读路径；删除 store 空壳并接入 `GetTask`，保留 `ListTasks` 不填；补 Task/Frame 契约夹具并显式清空 TasksResp 的 timing。T6 双裁决通过：规格覆盖与代码质量均通过；`go test ./internal/store/ -run 'TestAggregateTiming|TestTaskTimingWiring' -count=1`、`go test ./internal/store/ -count=1`、`go test ./internal/proto/ -run TestContractFixtures -update -count=1` 实测通过，`gofmt -l internal/store internal/proto` 无输出。commit 范围：`internal/store/timing_agg.go`、`internal/store/timing_agg_test.go`、`internal/store/store.go`、`internal/proto/contract_fixture_test.go`、`web/src/api/testdata/{Task,Frame}.json`、本 ledger；提交信息按计划为 `feat(store): 耗时账本三分法聚合与 GetTask 接线（T6）`。
