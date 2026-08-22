# 2026-08-22 lifecycle-backfill ledger

## 扫描口径

- 基线：`codegraph/baseline.json`，其中 `kind == "model"` 共 707 个，既有 lifecycle 2 条不重复产出。
- 法律：`docs/codegraph-scan-recipe.md` 的 creator/writer 纪律；只能把源码中能确认的返回类型、明确构造字面量或状态字段写入记为关系。
- 产物：`codegraph/diffs/lifecycle-backfill.json` 与本 ledger；不改源码、配方、声明、baseline 或 target。
- 进度恢复：每个领域完成后追加一行，提交范围用 `HEAD^..HEAD` 表示该领域提交。

## 覆盖统计

| 领域 | model 数 | 有生命周期的 model | lifecycle 条目 | 处置/跳过理由 |
|---|---:|---:|---:|---|
| d_coordination | 0 | 待扫 | 待扫 | 父领域，无直接容器 |
| d_coordination_api | 0 | 待扫 | 待扫 | 入口容器，无直接 model |
| d_coordination_cli | 13 | 待扫 | 待扫 | 待逐符号核查 |
| d_coordination_graph | 0 | 待扫 | 待扫 | 无直接 model 容器 |
| d_coordination_task | 194 | 10 | 27 | 184：wire/配置/展示投影/枚举/无状态服务结构跳过 |
| d_execution | 0 | 待扫 | 待扫 | 父领域，无直接容器 |
| d_execution_adapters | 0 | 待扫 | 待扫 | 无直接 model 容器 |
| d_execution_host | 14 | 3 | 3 | 11：进程凭据/判定快照/roster 结构无可证生命周期，跳过 |
| d_executor | 97 | 10 | 14 | 87：结果/事件/协议投影及纯业务辅助结构无稳定生命周期，跳过 |
| d_ledger | 48 | 4 | 10 | 44：查询/镜像/关系投影及配置快照跳过；仅保留卡片、事件、裁决和迁移审计的真实构造/状态写入 |
| d_runtime | 0 | 待扫 | 待扫 | 父领域，无直接容器 |
| d_runtime_config | 26 | 待扫 | 待扫 | 待逐符号核查 |
| d_runtime_maintenance | 18 | 待扫 | 待扫 | 待逐符号核查 |
| d_sessions | 20 | 4 | 7 | 13：hostproc/连接与快照结构缺少可证生命周期，跳过 |
| d_transport | 0 | 待扫 | 待扫 | 父领域，无直接容器 |
| d_transport_channel | 12 | 待扫 | 待扫 | 待逐符号核查 |
| d_transport_tunnel | 8 | 待扫 | 待扫 | 待逐符号核查 |
| d_web | 254 | 待扫 | 待扫 | wire/展示类型优先跳过，仍核查真实构造点 |
| d_workspace | 3 | 待扫 | 待扫 | 待逐符号核查 |
| **合计** | **707** | **待扫** | **待扫** | |

## 领域进度

- Task d_coordination_task 完成：194 个 model，10 个 model 有可证生命周期，新增 27 条，184 个跳过（wire/配置/展示投影/枚举/无状态服务结构）。逐条检查了构造返回类型、明确类型字面量与状态字段写入；spec 符合性与代码质量双裁决通过。`go run . graph validate --repo .` 已验证 `issues: null`；提交范围：`HEAD^..HEAD`。
- Task d_ledger 完成：48 个 model，4 个 model 有可证生命周期，新增 10 条，44 个跳过（查询/镜像/关系投影及配置快照）。`CreateCard`、`addComment`、`OpenDecision`、`MigrateCardWorkflow` 的返回类型/构造点，以及卡片和裁决的 `status` 持久化写入均有源码证据；spec 符合性与代码质量双裁决通过。`go run . graph validate --repo .` 已验证 `issues: null`；提交范围：`HEAD^..HEAD`。
- Task d_execution_host 完成：14 个 model，3 个 model 有可证生命周期，新增 3 条，11 个跳过（进程凭据、判定快照与 roster 结构无明确生命周期）。`CheckAdmission`、`Start`、`AcquireLock` 的返回类型直接证明构造点；spec 符合性与代码质量双裁决通过。`go run . graph validate --repo .` 已验证 `issues: null`；提交范围：`HEAD^..HEAD`。
- Task d_sessions 完成：20 个 model，4 个 model 有可证生命周期，新增 7 条，13 个跳过（hostproc、连接与快照结构无明确生命周期）。`Host.Open`、`Engine.Open`、`NewAttachment`、`newRing` 有直接构造/返回证据，`reap.exitCode` 与 `ring.n` 为真实状态写入；spec 符合性与代码质量双裁决通过。`go run . graph validate --repo .` 已验证 `issues: null`；提交范围：`HEAD^..HEAD`。
- Task d_executor 完成：97 个 model，10 个 model 有可证生命周期，新增 14 条，87 个跳过（结果/事件/协议投影及纯业务辅助结构）。四类 Proc 与四类 runState 均使用直接返回类型/构造点，FrameWriter 的 `turn`、`nextPart`、`seq` 是真实字段写入，Trailer 有明确返回类型；spec 符合性与代码质量双裁决通过。`go run . graph validate --repo .` 已验证 `issues: null`；提交范围：`HEAD^..HEAD`。
