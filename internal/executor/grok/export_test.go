package grok

import (
	"context"
	"encoding/json"
	"time"

	"github.com/xushixin/handoff/internal/executor"
	"github.com/xushixin/handoff/internal/executor/turn"
)

// WriteServeInfoForTest 暴露 serve.json 写入，供 grok_test 包做往返断言。
func WriteServeInfoForTest(p *Proc) error { return writeServeInfo(p) }

// NewTurnAccumulatorForTest 暴露回合累积器，供事件映射的纯逻辑断言
// （不起进程、不连网络）。
func NewTurnAccumulatorForTest() *turnAccumulator { return newTurnAccumulator() }

func (t *turnAccumulator) FeedRawForTest(raw []byte) { t.feedRaw(raw) }
func (t *turnAccumulator) TurnTextForTest() string   { return t.turnText() }
func (t *turnAccumulator) RenderTextForTest() string { return t.renderBuf.String() }
func (t *turnAccumulator) ClassifyForTest() (string, turn.Trailer) {
	return turn.ParseTrailer(t.turnText())
}

// NewAdapterWithRunForTest 造一个带空运行态的 adapter，供权限表与 Stop 竞态断言。
func NewAdapterWithRunForTest(taskID string) (*Adapter, *runState) {
	a := New(nil)
	r := &runState{taskID: taskID, pending: map[string]pendingPerm{},
		evCh: make(chan executor.AdapterEvent, 8), acc: newTurnAccumulator()}
	a.runs[taskID] = r
	return a, r
}

// AttachClientForTest 把已连好的 ACP 客户端挂到运行态上（供 Stop 竞态测试）。
func (r *runState) AttachClientForTest(cli *ACPClient) { r.cli = cli }

// EventsForTest 暴露事件通道（供断言 Stop 之后是否有假失败结果）。
func (r *runState) EventsForTest() <-chan executor.AdapterEvent { return r.evCh }

// NewHandlerForTest 构造一个挂到给定运行态的 ACP 回调面，供事件映射的集成断言
// （把假 agent 的 WS 消息经真实读循环打到 OnPermission）。
func NewHandlerForTest(a *Adapter, r *runState) ACPHandler { return &acpHandler{a: a, r: r} }

// SetTaskDirForTest 设定运行态的任务目录（OnAskQuestion 要写 render.log）。
func (r *runState) SetTaskDirForTest(dir string) { r.taskDir = dir }

// StartSessionForTest 只跑 Start 里「连接 → initialize → session/new」这一段，
// 不起 serve 进程，供 auth 错误路径断言。
func StartSessionForTest(wsURL, repoPath string) error {
	a := New(nil)
	r := &runState{taskID: "t", repoPath: repoPath, pending: map[string]pendingPerm{},
		evCh: make(chan executor.AdapterEvent, 8), acc: newTurnAccumulator()}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := DialACP(ctx, wsURL, &acpHandler{a: a, r: r}, nil)
	if err != nil {
		return err
	}
	defer cli.Close()
	r.cli = cli
	return a.openSession(ctx, r, repoPath)
}

func OptionIDForTest(decision string) string { return optionIDFor(decision) }

func NotePendingForTest(r *runState, id string, reqID []byte, desc string) {
	r.notePending(id, reqID, desc)
}
func VoidAllPendingForTest(r *runState) int { return r.voidAllPending() }

// RejectedForTest 暴露本回合被拒清单（供断言清单里是描述而非 id）。
func (r *runState) RejectedForTest() []string { return r.takeRejected() }

// FinishTurnForTest 直接驱动回合收尾分类，供断言「工具已提过问时兜底不再补一张
// 工单」——真机走一遍要 30 秒且依赖模型发挥，单测直接喂终局更稳。
func FinishTurnForTest(a *Adapter, r *runState, stopReason, turnText string) {
	r.turnMu.Lock()
	r.acc.feedRaw([]byte(`{"jsonrpc":"2.0","method":"session/update","params":` +
		`{"update":{"sessionUpdate":"agent_message_chunk","content":` +
		`{"type":"text","text":` + mustJSONString(turnText) + `}}}}`))
	r.turnMu.Unlock()
	a.finishTurn(r, ACPResult{Result: []byte(`{"stopReason":"` + stopReason + `"}`)})
}

// NoteAskedViaToolForTest 模拟 OnAskQuestion 已在本回合转交过一个提问。
func NoteAskedViaToolForTest(r *runState) { r.noteAskedViaTool() }

// SwapTmuxKillForTest 替换包级 tmux kill 执行点并返回恢复函数（供 Reap 测试
// 断言回收的会话名，绕开真实 tmux server）。
func SwapTmuxKillForTest(fn func(session string) error) func() {
	old := tmuxKill
	tmuxKill = fn
	return func() { tmuxKill = old }
}

func mustJSONString(s string) string {
	b, err := json.Marshal(s)
	if err != nil { // 测试辅助：入参是自己写的字面量，编不出来就是写错了
		panic(err)
	}
	return string(b)
}
