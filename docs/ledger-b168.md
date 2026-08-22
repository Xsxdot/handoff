# B168 执行 ledger

## 基线

- 分支起点：`408cd912`。
- 修改前先加入计划要求的节点-only 回归用例，未改生产代码。
- `gofmt -w cmd/workflow_test.go && go test ./cmd/ -run '^TestWorkflowPutAcceptsNodesOnlyFile$' -count=1` 原始失败：

  ```text
  --- FAIL: TestWorkflowPutAcceptsNodesOnlyFile (0.00s)
      workflow_test.go:45: put nodes-only: 状态序列至少两个状态 ""
  FAIL
  FAIL github.com/Xsxdot/handoff/cmd 0.005s
  ```

- 随后加入空定义回归用例；`go test ./cmd/ -run '^TestWorkflowPut(AcceptsNodesOnlyFile|RejectsEmptyDefinitionWithFieldNames)$' -count=1` 原始失败：

  ```text
  --- FAIL: TestWorkflowPutAcceptsNodesOnlyFile (0.00s)
      workflow_test.go:45: put nodes-only: 状态序列至少两个状态 ""
  --- FAIL: TestWorkflowPutRejectsEmptyDefinitionWithFieldNames (0.00s)
      workflow_test.go:75: error should mention nodes and states, got "状态序列至少两个状态  Error: 状态序列至少两个状态\n"
  FAIL
  FAIL github.com/Xsxdot/handoff/cmd 0.004s
  ```

- 基线实测：删去 CLI 的 `States` 长度前置判断后，当前 `internal/ledger.Store.PutWorkflow` 对 `Nodes` 与 `States` 同为空仍会成功写入；本 Task 不改 `internal/ledger`，CLI 只补两字段同时缺失的指路错误。

## 进度

- Task 1：完成；双裁决第 1 轮通过。规范符合性：移除 `States` 长度前置校验，节点-only 定义进入 `PutWorkflow` 并读回 3 个投影状态；仅对 `nodes` 与 `states` 同时缺失给出字段指路，未改 `internal/ledger`。代码质量：`go test ./cmd/` 与 `git diff --check` 均通过，无修复轮次。
- Task 1 提交范围：`cmd/workflow.go`、`cmd/workflow_test.go`、本 ledger；提交信息：`fix(workflow): workflow put 以 nodes 为权威输入`。

## Task 2：同族自查（只报告，不改）

自查命令：`rg -n 'def\\.|Def\\.' cmd/*.go`。

- `cmd/workflow.go:89`：是同族。这里同时读取 `def.Nodes` 与 `def.States`；其中 `States` 是由 `Nodes` 派生的投影，`Nodes` 才是权威输入。本次仅保留两字段都缺失的文件形状指路，不再按 `States` 数量判定；已在 Task 1 修改，未扩大到其它语义校验。
- `cmd/template.go:82`：不是同族。`executor`、`prompt`、`discipline` 是 `TemplateDef` 的权威必填输入，`PutTemplate` 不从其它定义字段派生它们。
- `cmd/workflow.go:42,56`：不是同族。`workflow.Def.States` 发生在 `GetWorkflow` 之后，仅用于列表渲染，不是调 Store 之前的定义体校验。
- `cmd/workflow_test.go:57-58`：不是同族。仅断言 Store 读回的派生投影，属于回归测试。
- `cmd/workflow.go:79`、`cmd/template.go:70`、`cmd/workflow_test.go:21`：不是定义体字段引用，分别是文件参数说明或测试文件名。

结论：除 `cmd/workflow.go:89` 外，未发现其它“调 Store 之前拿派生投影做定义体入参校验”的账本 CLI 位置；`cmd/template.go:82` 为独立的权威字段必填校验，按要求只报告不改。

- Task 2：完成；双裁决第 1 轮通过。规范符合性：逐项列出匹配位置并区分同族/非同族，仅报告未改代码；代码质量：自查命令实际输出与分类一致。
- Task 2 提交范围：本 ledger；提交信息：`docs(workflow): 记录派生字段校验同族自查`。
