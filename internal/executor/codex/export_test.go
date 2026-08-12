// export_test.go —— 包内实现细节的测试缝。
//
// 职责：把 unexported 的构造/替换点暴露给同包外的 _test 包，避免为测试改可见性。
// 边界：仅测试构建时编译（_test.go 后缀），不进生产二进制。
package codex

import (
	"encoding/json"
	"io"
	"log/slog"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/prochost"
)

// WriteServeInfoForTest 暴露 writeProcInfo，供 proc.json 回环测试。
func WriteServeInfoForTest(p *Proc) error {
	return writeProcInfo(p.TaskDir, &procInfo{Handle: p.Handle, Port: p.Port})
}

// ServeSpecForTest 暴露 serveSpec，供 codex_test 包做 argv/env 断言。
func ServeSpecForTest(repoPath, taskDir string, port int, env []string) prochost.Spec {
	return serveSpec(repoPath, taskDir, port, env)
}

// ParseItemNotificationForTest 暴露 item 通知解析。
func ParseItemNotificationForTest(raw []byte) (*ThreadItemView, bool) {
	it, ok := parseItemNotification(raw)
	if !ok {
		return nil, false
	}
	return &ThreadItemView{it}, true
}

// ThreadItemView 是 threadItem 的只读测试视图。
type ThreadItemView struct{ it *threadItem }

func (v *ThreadItemView) Type() string                { return v.it.Type }
func (v *ThreadItemView) ID() string                  { return v.it.ID }
func (v *ThreadItemView) Changes() []fileUpdateChange { return v.it.Changes }
func (v *ThreadItemView) RenderLine() string          { return v.it.renderLine() }

// ItemIndexHandle 是 itemIndex 的测试封装。
type ItemIndexHandle struct{ x *itemIndex }

// NewItemIndexForTest 建一个指定容量的索引。
func NewItemIndexForTest(n int) *ItemIndexHandle { return &ItemIndexHandle{newItemIndex(n)} }

func (h *ItemIndexHandle) PutForTest(id, typ string) {
	h.x.put(&threadItem{ID: id, Type: typ})
}

func (h *ItemIndexHandle) GetForTest(id string) (*ThreadItemView, bool) {
	it, ok := h.x.get(id)
	if !ok {
		return nil, false
	}
	return &ThreadItemView{it}, true
}

// DecisionForTest 暴露裁决映射。
func DecisionForTest(d string) string { return decisionFor(d) }

// ParseCommandApprovalForTest 暴露命令审批报文解析。
func ParseCommandApprovalForTest(raw []byte) (commandApproval, bool) {
	return parseCommandApproval(raw)
}

// PermRequestFromCommandForTest 暴露命令类权限判据。
func PermRequestFromCommandForTest(a commandApproval) *executor.PermRequest {
	return permRequestFromCommand(a)
}

// PermRequestFromFileChangeForTest 暴露文件变更类权限判据。
func PermRequestFromFileChangeForTest(v *ThreadItemView) *executor.PermRequest {
	if v == nil {
		return permRequestFromFileChange(nil)
	}
	return permRequestFromFileChange(v.it)
}

// CommandPermTextForTest 暴露命令审批的人读描述。
func CommandPermTextForTest(a commandApproval) string { return commandPermText(a) }

// ThreadItemForTest 造一个带 changes 的 fileChange item（每项形如 {path, kind}）。
func ThreadItemForTest(id, typ string, changes [][2]string) *ThreadItemView {
	it := &threadItem{ID: id, Type: typ}
	for _, c := range changes {
		it.Changes = append(it.Changes, fileUpdateChange{Path: c[0], Kind: changeKind{Type: c[1]}})
	}
	return &ThreadItemView{it}
}

// PermTableHandle 是 permTable 的测试封装。
type PermTableHandle struct{ t *permTable }

// NewPermTableForTest 建一张空的挂起表。
func NewPermTableForTest() *PermTableHandle { return &PermTableHandle{newPermTable()} }

func (h *PermTableHandle) NoteForTest(id string, reqID []byte, desc string) {
	h.t.note(id, reqID, desc)
}
func (h *PermTableHandle) TakeForTest(id string) (string, bool) {
	pp, ok := h.t.take(id)
	return pp.desc, ok
}
func (h *PermTableHandle) VoidAllForTest() int             { return h.t.voidAll() }
func (h *PermTableHandle) NoteRejectedForTest(desc string) { h.t.noteRejected(desc) }
func (h *PermTableHandle) TakeRejectedForTest() []string   { return h.t.takeRejected() }

// RejectedTurnQuestionForTest 暴露被拒清单的问题渲染。
func RejectedTurnQuestionForTest(r []string) string { return rejectedTurnQuestion(r) }

// NewAdapterWithRunForTest 造一个带运行态的 adapter（不起进程、不连 WS）。
func NewAdapterWithRunForTest(taskID string) (*Adapter, *runState) {
	a := New(quietTestLogger())
	r := a.newRunState(taskID, "", "")
	a.mu.Lock()
	a.runs[taskID] = r
	a.mu.Unlock()
	return a, r
}

// EventsForTest 返回运行态的事件通道。
func EventsForTest(r *runState) <-chan executor.AdapterEvent { return r.evCh }

// FinishTurnForTest 直接驱动回合收尾分类。
func FinishTurnForTest(a *Adapter, r *runState, status, errMsg, text string) {
	a.finishTurn(r, status, errMsg, text)
}

// NoteRejectedOnRunForTest 往运行态里塞一条被拒记录。
func NoteRejectedOnRunForTest(r *runState, desc string) { r.noteRejected(desc) }

// MarkStoppingForTest 置位主动停止标记。
func MarkStoppingForTest(r *runState) {
	r.emitMu.Lock()
	r.stopping = true
	r.emitMu.Unlock()
}

// NewHandlerForTest 造一个绑定到该运行态的通知/请求处理器。
func NewHandlerForTest(a *Adapter, r *runState) Handler { return &handler{a: a, r: r} }

// AttachFakeClientForTest 给运行态挂一条把应答吞掉的假连接。
//
// 为什么不给实现里的 r.cli.* 加 nil 守卫：那会把「连接已经没了却还在发裁决」
// 这种真 bug 一起吞掉。测试用假连接，实现里保持裸调用。
func AttachFakeClientForTest(r *runState) {
	r.cli = &Client{log: quietTestLogger(),
		replyHook: func(json.RawMessage, any) error { return nil }}
}

func quietTestLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// ParseUserInputForTest 暴露提问报文解析。
func ParseUserInputForTest(raw []byte) (string, []userInputQuestion, bool) {
	return parseUserInput(raw)
}

// UserInputTextForTest 暴露问题正文渲染。
func UserInputTextForTest(qs []userInputQuestion) string { return userInputText(qs) }

// UserInputReplyForTest 暴露应答体构造。
func UserInputReplyForTest(qs []userInputQuestion) map[string]any { return userInputReply(qs) }

// SwapLookPathForTest 替换 preflight 的 PATH 探测，返回还原函数。
func SwapLookPathForTest(fn func(string) (string, error)) func() {
	old := lookPath
	lookPath = fn
	return func() { lookPath = old }
}
