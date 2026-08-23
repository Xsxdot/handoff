# B159 实现计划：approve 用例的就绪门要取送达注销点，不取任务状态

> 2026-08-22。协调者写。卡：B159（中）。

## 事实基线（协调者在 985f37135 上查证）

`internal/agentd/approver_test.go:250` 起：

```go
	task := mustApproverDispatch(t, m)
	waitTaskState(t, st, task.ID, proto.TaskStateWaitingReview) // 就绪门
	tk := mustGetTicket(t, st, task.ID+":"+fk.PermID())
	if tk.Answer == nil || *tk.Answer != "allow" || tk.DeliveredAt == nil {   // 断言
```

就绪门等的是**任务状态**，断言的是**工单的送达标记**。两者不是同一条链的同一点：
`DeliveredAt` 由 `Manager.markDelivered`（`internal/agentd/manager.go:2793`
→ `store.MarkTicketDelivered`）在送达之后写，与任务迁 `waiting_review` 不同步。
CI 上（ubuntu runner，run 32215687442）实测到的正是这个窗口：`Answer` 已是
`allow`、`AnsweredAt` 已写、`DeliveredAt` 为 nil。本地 `-count=25` 不复现——
它要负载才显形。断言 2、3（事件与 `LastDecision`）同理，只是还没撞上。

这属于记忆里记过的第一族：**就绪门用了代理条件**。

## 设计决定

1. **补一个等送达的门**，而不是给断言加重试：门与断言必须指向同一条链的同一点。
2. **判据不靠刷次数，靠变异**。本地跑一万次也证明不了什么（它在 runner 上才显形），
   但有一个决定性实验：**人为把送达标记延后**，修前必红、修后必绿。
   这比「跑 N 次没红」强，因为它直接把那个窗口撑开。
3. **三条断言都挪到新门之后**，不只挪第 1 条——另外两条是同一个窗口里的邻居。

## Task 1：加就绪门助手

`internal/agentd/approver_test.go`（或该包既有的 test helper 文件，先
`grep -rn "func waitTaskState" internal/agentd/*_test.go` 找到它，把新助手放同一处）：

```go
// waitTicketDelivered 等到工单的送达标记真的落库。
//
// why 不能用 waitTaskState 代替：任务迁 waiting_review 与 markDelivered 写
// delivered_at 是两条独立路径，中间有真实窗口——CI 上实测到过 Answer 已写、
// DeliveredAt 仍是 nil 的一瞬（B159）。就绪门要取这条链**彻底**跑完的注销点，
// 不取中途的任务状态。
func waitTicketDelivered(t *testing.T, st *store.Store, ticketID string) proto.Ticket {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		tk, err := st.GetTicket(ticketID)   // 按该包既有取工单助手的实际签名来
		if err == nil && tk.DeliveredAt != nil {
			return tk
		}
		if time.Now().After(deadline) {
			t.Fatalf("等工单 %s 送达标记超时（最后一次读到 %+v，err=%v）", ticketID, tk, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```

`TestApproverApprovesPermissionWithoutWaking` 里把
`tk := mustGetTicket(...)` 换成 `tk := waitTicketDelivered(t, st, task.ID+":"+fk.PermID())`，
**断言一行不改**。`waitTaskState` 那行保留（它仍是「直通完成、未在 waiting_answer
停留」这条判据的载体，注释里已写明）。

## Task 2：同族自查

`grep -rn "waitTaskState" internal/agentd/*_test.go`，逐个看它后面的断言读的是
不是任务状态之外的字段（工单、事件、fake 的 LastDecision）。同族的一并挪到
对应的门后面；**没把握的列进 ledger 报给协调者，不要硬改**。

## 测试映射与变异（决定性实验，必须真跑）

1. 改前基线：`go test ./internal/agentd/ -run TestApproverApprovesPermissionWithoutWaking
   -count=20` —— 大概率全绿（本地不复现），把结果原文抄进 ledger，**不要**据此
   声称问题不存在。
2. **变异实验（本卡的真判据）**：在 `Manager.markDelivered` 里临时插
   `time.Sleep(200 * time.Millisecond)`（只在本地实验，**不提交**），
   跑同一条用例：
   - 修改前：必须**红**在 `DeliveredAt == nil`（证明就绪门确实取早了）
   - 修改后：必须**绿**（证明新门罩得住这个窗口）
   两次结果的原文都抄进 ledger。若修改前没红，说明这个变异没撑开正确的窗口，
   停下来报告协调者，**不要**自行改判据蒙混过关。
3. 回归：`go test ./internal/agentd/ -count=1` 全绿。

## 测试范围

- `go test ./internal/agentd/`
- `go build ./...`、`go vet ./...`、`gofmt -l .` 无输出

## 不属于本次

- 不改生产代码（`markDelivered` 的时序不动，实验用的 sleep 绝不许提交）。
- 不改 approve 路径的任何语义。
- 不处理 B162（另一条 WS 偶发红，独立卡）。
