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
