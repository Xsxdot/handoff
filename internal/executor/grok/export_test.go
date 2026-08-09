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
	r := &runState{taskID: taskID, pending: map[string]json.RawMessage{},
		evCh: make(chan executor.AdapterEvent, 8), acc: newTurnAccumulator()}
	a.runs[taskID] = r
	return a, r
}

// AttachClientForTest 把已连好的 ACP 客户端挂到运行态上（供 Stop 竞态测试）。
func (r *runState) AttachClientForTest(cli *ACPClient) { r.cli = cli }

// EventsForTest 暴露事件通道（供断言 Stop 之后是否有假失败结果）。
func (r *runState) EventsForTest() <-chan executor.AdapterEvent { return r.evCh }

// StartSessionForTest 只跑 Start 里「连接 → initialize → session/new」这一段，
// 不起 serve 进程，供 auth 错误路径断言。
func StartSessionForTest(wsURL, repoPath string) error {
	a := New(nil)
	r := &runState{taskID: "t", repoPath: repoPath, pending: map[string]json.RawMessage{},
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

func NotePendingForTest(r *runState, id string, reqID []byte) { r.notePending(id, reqID) }
func VoidAllPendingForTest(r *runState) int                   { return r.voidAllPending() }
