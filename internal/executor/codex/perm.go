// perm.go —— codex 权限请求的判据、挂起表与裁决映射。
//
// 职责：
//   - 把 item/*/requestApproval 的报文翻译成 executor.PermRequest（安全判据的输入）
//   - 维护 itemId → 待裁决请求 的挂起表，供 RespondPermission 回发
//   - 记录本回合被拒清单，回合收尾时一并交代给协调者
//
// 边界：
//   - **不做审批判断**：批不批由 manager 依协调者应答决定（executor 契约的硬边界）
//   - 不写 store、不发事件
//
// 裁决映射只有两个出口（spec §5.4，依据官方 schema）：
//   - accept  —— 放行这一次
//   - decline —— 拒这一次，**回合继续**
//
// 绝不使用 cancel（会立刻掐掉整个回合，等于协调者点一次「拒绝」就杀掉任务，
// 与另三个 adapter 行为不对等），也绝不使用 acceptForSession /
// acceptWithExecpolicyAmendment / applyNetworkPolicyAmendment（都是「以后同类
// 不再问」，正是 B23 明确否掉的语义）。
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/turn"
)

// permTextHardLimit 是权限描述的硬上限（64KB）。
//
// B6 的教训：权限文本**不为美观或安全而截断**——安全门与廉价模型审批者必须看到
// 全文。这个上限只防失控（比如模型生成一条几十 MB 的命令把事件库撑爆）。
const permTextHardLimit = 64 << 10

// decisionFor 把 handoff 的裁决翻译为 codex 的 decision 枚举。
//
// fail-closed：除 "once" 外一律 decline，绝不误放行——误拒的代价是协调者再来一轮，
// 误放的代价可能是不可逆的破坏性操作。
func decisionFor(decision string) string {
	if decision == "once" {
		return "accept"
	}
	return "decline"
}

// commandApproval 是 item/commandExecution/requestApproval 的报文视图。
type commandApproval struct {
	ItemID         string          `json:"itemId"`
	ThreadID       string          `json:"threadId"`
	TurnID         string          `json:"turnId"`
	Command        string          `json:"command"`
	Cwd            string          `json:"cwd"`
	CommandActions []commandAction `json:"commandActions"`
}

// parseCommandApproval 解析命令审批报文。
//
// 返回：报文与 true；JSON 非法或缺 itemId 时返回零值与 false（缺 itemId 就无法
// 回发裁决，登记进挂起表只会制造一张永远应答不了的工单）。
func parseCommandApproval(params json.RawMessage) (commandApproval, bool) {
	var a commandApproval
	if err := json.Unmarshal(params, &a); err != nil || a.ItemID == "" {
		return commandApproval{}, false
	}
	return a, true
}

// permRequestFromCommand 从命令审批报文构造结构化权限请求。
//
// 注意：Command 是**全文不截断**——B23/B27 的判据与廉价模型审批者都依赖它；
// 展示层的长度收口在 commandPermText 里做，两者刻意分离。
func permRequestFromCommand(a commandApproval) *executor.PermRequest {
	if strings.TrimSpace(a.Command) == "" {
		return nil
	}
	var paths []string
	for _, act := range a.CommandActions {
		if act.Path != "" {
			paths = append(paths, act.Path)
		}
	}
	return &executor.PermRequest{
		Tool:    executor.PermToolBash,
		Command: a.Command,
		Paths:   paths,
	}
}

// permRequestFromFileChange 从索引里的 fileChange item 构造结构化权限请求。
//
// 参数：
//   - it: itemId 索引命中的 item；**nil 表示索引未命中**
//
// 返回：
//   - 权限请求；it 为 nil 或没有任何 change 时返回 nil
//
// 注意：返回 nil 不是「无所谓」，而是**明确的 fail-closed 信号**——manager 拿到
// 没有 Perm 的权限事件会升级人工裁决。伪造一个空 Paths 的结构反而更危险：路径
// 判据会以为「没有越界路径」而自动放行（spec §5.4）。
//
// 工具分类：全部 kind.type == "update" 判 edit，只要有一个不是就判 write——
// write 的爆炸半径更大（新建/删除 vs 改已有），不确定时往大了判。
func permRequestFromFileChange(it *threadItem) *executor.PermRequest {
	if it == nil || len(it.Changes) == 0 {
		return nil
	}
	paths := make([]string, 0, len(it.Changes))
	allUpdate := true
	for _, c := range it.Changes {
		paths = append(paths, c.Path)
		if c.Kind.Type != "update" {
			allUpdate = false
		}
	}
	tool := executor.PermToolWrite
	if allUpdate {
		tool = executor.PermToolEdit
	}
	return &executor.PermRequest{Tool: tool, Paths: paths}
}

// commandPermText 渲染命令审批的人读描述（工单正文与被拒清单都用它）。
func commandPermText(a commandApproval) string {
	var b strings.Builder
	b.WriteString("运行命令：" + a.Command)
	if a.Cwd != "" {
		b.WriteString("\n工作目录：" + a.Cwd)
	}
	return turn.TruncateMarked(b.String(), permTextHardLimit)
}

// fileChangePermText 渲染文件变更审批的人读描述。
//
// 注意：it 为 nil（索引未命中）时也要给出可读文本——权限事件仍要发出去让协调者
// 知情，只是 Perm 为 nil 触发 fail-closed。
func fileChangePermText(it *threadItem) string {
	if it == nil {
		return "修改文件（codex 未提供变更清单，已按最保守方式升级人工裁决）"
	}
	var b strings.Builder
	b.WriteString("修改文件：\n")
	for _, c := range it.Changes {
		b.WriteString("  - " + c.Kind.Type + " " + c.Path + "\n")
	}
	return turn.TruncateMarked(b.String(), permTextHardLimit)
}

// pendingPerm 是一条待裁决的权限请求。
type pendingPerm struct {
	reqID json.RawMessage // JSON-RPC 请求 id，回发裁决必需
	desc  string          // 人读描述；被拒时记入被拒清单
}

// permTable 是挂起权限表与本回合被拒清单。并发安全。
type permTable struct {
	mu       sync.Mutex
	pending  map[string]pendingPerm
	byReq    map[string]string // JSON-RPC 请求 id → itemId（Task 6 的反查表，供 serverRequest/resolved 用）
	rejected []string
}

// newPermTable 建一张空表。
func newPermTable() *permTable {
	return &permTable{pending: map[string]pendingPerm{}}
}

// note 登记一个待裁决的权限请求。
//
// 参数：
//   - itemID: codex 的 itemId，manager 经它应答（事件的 PermissionID 与之同名）
//   - reqID: JSON-RPC 请求 id，应答回发必需
//   - desc: 人读描述；拒绝时记入被拒清单，**不用 itemId**
func (t *permTable) note(itemID string, reqID json.RawMessage, desc string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[itemID] = pendingPerm{reqID: reqID, desc: desc}
}

// take 取出并移除挂起项。
//
// 注意：不清对应的 byReq 孤儿条目——一条 map entry 的代价远小于反向索引的复杂度，
// 孤儿条目在 voidAll 时统一清掉。
func (t *permTable) take(itemID string) (pendingPerm, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	pp, ok := t.pending[itemID]
	delete(t.pending, itemID)
	return pp, ok
}

// voidAll 作废全部挂起项，返回作废数量（连接死亡时调用）。
func (t *permTable) voidAll() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	n := len(t.pending)
	t.pending = map[string]pendingPerm{}
	t.byReq = map[string]string{}
	return n
}

// noteRejected 记下本回合被拒的权限描述，回合收尾时一并交代给协调者。
func (t *permTable) noteRejected(desc string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.rejected = append(t.rejected, desc)
}

// takeRejected 取走并清空本回合的被拒记录。
//
// 注意：必须取走而非读取——不清空会让下一回合重复上报同一批被拒项，
// 协调者会收到一张内容陈旧的工单。
func (t *permTable) takeRejected() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.rejected
	t.rejected = nil
	return out
}

// rejectedTurnQuestion 把被拒清单拼成交给协调者的问题。
//
// 注意：正文里放的是权限**描述**而不是 itemId——被拒清单存在的意义是让协调者
// 知道「模型刚才想干什么、被挡了」，一串不透明 id 等于没说。
func rejectedTurnQuestion(rejected []string) string {
	var b strings.Builder
	b.WriteString("本回合有权限请求被拒，模型可能改用其它做法或停下。被拒清单：\n")
	for _, d := range rejected {
		b.WriteString("  - " + d + "\n")
	}
	b.WriteString("请确认下一步该怎么做。")
	return b.String()
}

// noteReqID 记下 JSON-RPC 请求 id 到 itemId 的反查关系。
//
// 为什么需要：serverRequest/resolved 通知带的是 **requestId**（JSON-RPC id），
// 而挂起表按 itemId 索引。没有这张反查表，「该请求已被别处了结」这个通知就无法
// 落到具体挂起项上，那张工单会一直挂着，协调者裁决时才发现回发失败。
func (t *permTable) noteReqID(reqID, itemID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.byReq == nil {
		t.byReq = map[string]string{}
	}
	t.byReq[reqID] = itemID
}

// dropByReqID 按 JSON-RPC 请求 id 摘掉挂起项，返回对应 itemId。
func (t *permTable) dropByReqID(reqID string) (string, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	itemID, ok := t.byReq[reqID]
	if !ok {
		return "", false
	}
	delete(t.byReq, reqID)
	delete(t.pending, itemID)
	return itemID, true
}

// PermissionsVolatile 表明本 adapter 的权限请求随连接消亡。
//
// manager 据此在 agentd 重启后拒绝恢复「尚有未决权限工单」的任务：按最保守路径
// 假设 thread/resume 不会重发未决授权请求（spec §8）。
func (a *Adapter) PermissionsVolatile() bool { return true }

// RespondPermission 应答 codex 的权限请求。
//
// 参数：
//   - taskID: 目标任务
//   - permID: 权限请求 id（即 codex 的 itemId，裸值不带命名空间前缀）
//   - decision: "once"（批准本次）或 "reject"（拒绝）
//   - reason: codex 的应答体只有 decision，带不了消息，本 adapter 忽略（spec §2.5）
//
// 返回：
//   - 任务不在运行中、或挂起表查不到该 permID 时，包装 executor.ErrTaskNotRunning
//     ——两者都意味着「executor 侧那次请求已经不在了」，调用方据此转失败交协调者，
//     而不是当作可重试的瞬时错误
func (a *Adapter) RespondPermission(ctx context.Context, taskID, permID, decision, _ string) error {
	r := a.lookup(taskID)
	if r == nil {
		a.log.Warn("权限应答时任务不在运行中", "task", taskID, "perm", permID)
		return fmt.Errorf("任务 %s 无运行态: %w", taskID, executor.ErrTaskNotRunning)
	}
	pp, ok := r.take(permID)
	if !ok {
		a.log.Warn("权限应答找不到挂起请求（连接已重建或已作废）", "task", taskID, "perm", permID)
		return fmt.Errorf("权限请求 %s 已不在挂起表: %w", permID, executor.ErrTaskNotRunning)
	}

	d := decisionFor(decision)
	a.log.Info("回发权限裁决", "task", taskID, "perm", permID, "decision", decision, "mapped", d)
	if err := r.cli.Reply(pp.reqID, map[string]any{"decision": d}); err != nil {
		a.log.Error("回发权限裁决失败", "task", taskID, "perm", permID, "cause", err)
		return fmt.Errorf("回发权限裁决: %w", err)
	}
	if d == "decline" {
		// 记入被拒清单的是权限描述而非 permID：被拒清单存在的意义是让协调者知道
		// 「模型刚才想干什么、被挡了」，一串不透明 id 等于没说。长度收口在回合
		// 收尾的 turn.ClampQuestion 里做，不在本处截短。
		r.noteRejected(pp.desc)
	}
	r.appendRenderDelta("【裁决】" + decision + " → " + d + "：" + pp.desc)
	a.flushRender(r)
	a.log.Info("权限裁决已送达 executor", "task", taskID, "perm", permID)
	return nil
}
