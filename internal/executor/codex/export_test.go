// export_test.go —— 包内实现细节的测试缝。
//
// 职责：把 unexported 的构造/替换点暴露给同包外的 _test 包，避免为测试改可见性。
// 边界：仅测试构建时编译（_test.go 后缀），不进生产二进制。
package codex

import "github.com/xushixin/handoff/internal/executor"

// WriteServeInfoForTest 暴露 writeServeInfo，供 serve.json 回环测试。
func WriteServeInfoForTest(p *Proc) error { return writeServeInfo(p) }

// SwapTmuxKillForTest 替换 tmux kill 测试缝，返回还原函数。
func SwapTmuxKillForTest(fn func(session string) error) func() {
	old := tmuxKill
	oldHas := tmuxHasSession
	tmuxKill = fn
	tmuxHasSession = func(string) bool { return false } // 配套：让「会话不存在」成立
	return func() { tmuxKill = old; tmuxHasSession = oldHas }
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
func (h *PermTableHandle) VoidAllForTest() int           { return h.t.voidAll() }
func (h *PermTableHandle) NoteRejectedForTest(desc string) { h.t.noteRejected(desc) }
func (h *PermTableHandle) TakeRejectedForTest() []string { return h.t.takeRejected() }

// RejectedTurnQuestionForTest 暴露被拒清单的问题渲染。
func RejectedTurnQuestionForTest(r []string) string { return rejectedTurnQuestion(r) }
