# B277 Go flows 扫描报告

本文件记录 B277 抽取器在现役 handoff baseline 上的实际结果。

边界：只读取既有 `nodes`、`edges`、`implements` 与 Go 源码；只写
`codegraph/baseline.json` 的 `flows` 和 `meta` 扫描字段，不改业务源码、TS/TSX、
节点、边、接口关系或容器拓扑。

## 扫描结果

- 抽取器：`go run ./scripts/codegraph-flows --repo .`
- C17 种子：320 个未折叠跨域 Go 入缝。
- 第 1 轮：请求 320 个，成功写出 318 条。
- 接口实现闭包第 2 轮：请求 40 个 Go 实现方法，成功写出 40 条。
- 最终 `flows`：358 条；其中 352 条有非空 `steps`。
- `iface:true`：23 个步骤，目标为 `m_executor_Adapter` 或
  `m_opencode_serveHandle`；实现方法由既有 `implements` 类型字段 join。
- 入口键：0；非 `.go` 键：0。节点、边、implements、containers、domains、
  projections、lifecycle 与基线语义对比均未改变。
- 扫描器最多运行两轮；第 2 轮结束后仍有 6 个二阶实现候选，按计划上限不再展开。

## 跳过与空流程

解析/函数体缺失而跳过 2 个符号：

- `n_ledger_Store_EnsureDefaultTemplates`（`internal/ledger/templates.go`）
- `n_ledger_Store_EnsureDefaultWorkflows`（`internal/ledger/workflows.go`）

成功解析但没有可映射控制流步骤的 6 个键保留为空 `steps`，未伪造调用：

- `n_agentd_Server_SetLedger`
- `n_agentd_SetGitProxy`
- `n_agentd_SetTaskProcCounter`
- `n_agentd_machineUpgradeStatusCode`
- `n_executor_OneShotArgs`
- `n_prochost_SetPtyhostCredentialProvider`

## 实际校验

计划自检输出：`flows 358 bad 0`。

`codegraph validate` 输出：`containers 239`、`domains 20`、`nodes 3636`、
`edges 4735`、`issues null`，退出码 0。

抽查 `Manager.Dispatch`：源码首个错误守卫在 `internal/agentd/manager.go:708`，
flow 为 `s2`、`cond=err != nil`、`then=[s3]`，源码 return 在 710 行。
抽查 `BundleRange`：源码首个错误守卫在 `internal/agentd/bundle.go:66`，flow 为
`s10`、`cond=err != nil`、`then=[s11,s12]`，源码 return 在 68 行。

当前 go.mod 锁定的 `github.com/Xsxdot/charter/graph v0.9.0` CLI 不提供计划中的
`flow` 子命令；亲跑该命令得到 `Error: unknown command "flow" for "codegraph"`，
所以 CLI 的 `degraded=false` 查询未验证。基线 JSON 自检已确认有非空 flow，且
`codegraph validate` 已通过。
