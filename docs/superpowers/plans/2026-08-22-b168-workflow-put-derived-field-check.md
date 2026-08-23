# B168 实现计划：`workflow put` 不许拿派生字段当入参校验

> 2026-08-22。协调者写。卡：B168（中）。

## 事实基线（协调者在 985f37135 上查证）

`cmd/workflow.go` 的 `wfPutCmd`：

```go
		if len(def.States) < 2 {
			return fmt.Errorf("状态序列至少两个状态")
		}
		st, err := openLedger()
		...
		version, err := st.PutWorkflow(args[0], def)
```

而 `States` 是 `PutWorkflow` **内部**由 `withStatesFromNodes()` 从 `Nodes` 派生的
只读投影（`internal/ledger/types.go` 的 `WorkflowDef` 注释原文：「**Nodes 是权威，
States/Gates 是写入时从 Nodes 派生的只读投影**」）。于是只写 `nodes` 的定义文件
——**正是节点模型确立后的权威形态，也是控制台写出的形态**——在 CLI 这一侧恒被拒。

文案还误导：报「状态序列至少两个状态」，而用户文件里明明有 3 个节点，会去找一个
根本不该由他写的 `States` 字段。

## 设计决定

1. **删掉这条前置校验，让 `PutWorkflow` 的 `validateNodes` 当唯一判据**——校验的
   位置错了，不是校验本身错。库层已经有权威校验，CLI 再判一次派生字段只会漂移。
2. **两者都空要报得指路**：`nodes` 与 `states` 都缺时，错误文案要点名两个字段名
   （由库层给出即可；若库层文案不含字段名，则在 CLI 侧把库层错误包一层，
   **不要**恢复前置校验）。
3. **顺带自查同族**：账本 CLI 里其他「先于 Store 判派生字段」的地方一并列出。
   这是「派生投影被当成入参校验」的一族，不是孤例。**只列出并在 ledger 里报告，
   不顺手改**——改动面要留给协调者裁决。

## Task 1：删前置校验

`cmd/workflow.go` 的 `wfPutCmd` 里删掉 `if len(def.States) < 2 { ... }` 三行。
删掉之后 `def` 直接进 `st.PutWorkflow`。

若 `PutWorkflow` 对「Nodes 与 States 皆空」的报文不含字段名（先跑一次实测确认，
别猜），在 CLI 侧包一层：

```go
		version, err := st.PutWorkflow(args[0], def)
		if err != nil {
			// 库层的 validateNodes 是唯一判据（Nodes 是权威，States 是写入时
			// 从它派生的只读投影）。这里只补一句「文件里该写什么」——原报文
			// 说的是内部形态，用户手里是一个 json 文件。
			return fmt.Errorf("写入工作流失败（定义文件里 nodes 与 states 至少给一个，节点模型下应给 nodes）: %w", err)
		}
```

## Task 2：同族自查（只报告，不改）

`grep -rn "def\.\|Def\." cmd/*.go` 里所有「调 Store 之前判定义体字段」的位置逐个
看一眼，判断该字段是不是 Store 内部派生的投影。把结论（文件:行 + 是/否同族）
写进 ledger 的自查节。**不要顺手改**。

## 测试映射

`cmd/` 包下新增（先 `grep -rn "wfPut\|workflow put" cmd/*_test.go` 找既有夹具复用）：

1. `TestWorkflowPutAcceptsNodesOnlyFile`：写一个只含 `nodes`（3 个节点、首节点
   `dispatch:false`）的临时 json 文件，跑 `workflow put`，断言退出无错且
   `GetWorkflow` 读回的 `Def.States` 有 3 个（投影由库层派生出来）。
   **这条就是本卡的判据**：修前必红（报「状态序列至少两个状态」）。
2. `TestWorkflowPutRejectsEmptyDefinitionWithFieldNames`：`{}` 空定义被拒，
   且报文里同时出现 `nodes` 与 `states` 两个词。

执行者必须先在**修改前**跑一次用例 1 并把红色报文原文抄进 ledger，再动手——
判据先在基线上跑过，才知道它罩的是这个 bug。

## 测试范围

- `go test ./cmd/`（触及包）
- `go build ./...`、`go vet ./...`、`gofmt -l .` 无输出

## 不属于本次

- 不改 `internal/ledger` 的任何校验逻辑。
- 不动控制台。
- 同族问题只报告不修。
