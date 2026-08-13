// onclosed_drop_internal_test.go —— 连接断开必须摘掉运行态的回归测试。
//
// 职责：
//   - 断言 onClosed 走完失败处置后，runs 表里不再留着这条已死的运行态
//   - 断言摘除是条件性的：不能误删 Resume 已经换上的新运行态
//
// 边界：
//   - 不起进程、不连网络，只验 runs 表的对账
package grok

import (
	"errors"
	"testing"

	"github.com/Xsxdot/handoff/internal/executor"
)

// TestOnClosedDropsRunState 连接断开后运行态必须从 runs 表摘除。
//
// 现场（2026-08-09 grok 端到端验收）：杀掉 grok serve 后，onClosed 发了失败
// 结果、关了事件通道，却把 runState 留在 runs 表里。这具僵尸把两条路都挡死了：
//   - Send：lookup 返回它 → 拿一条死连接去发指令
//   - Resume：冷恢复互斥以「runs 表里有条目」为判据 → 把僵尸当成「恢复进行中」，
//     直接返回不恢复，continue 500
//
// 这两个症状此前各修了一次（补哨兵、修撞名），但根因就是这里：adapter 记录的
// 运行态与进程真实存活性失配——正是本项目 Spec A 要消灭的那类失配。
func TestOnClosedDropsRunState(t *testing.T) {
	a, r := NewAdapterWithRunForTest("t1")
	a.onClosed(r, errors.New("连接断开"))

	if got := a.lookup("t1"); got != nil {
		t.Fatal("连接断开后 runs 表必须摘掉这条运行态，否则 Send/Resume 都会撞上僵尸")
	}
	// 失败结果仍要产出——摘运行态不能顺手把信号也吃掉
	select {
	case ev := <-r.EventsForTest():
		if ev.Type != "result" || ev.Result == nil || ev.Result.OK {
			t.Fatalf("应产出失败结果，实得 %+v", ev)
		}
	default:
		t.Fatal("连接断开必须产出失败结果事件")
	}
}

// TestOnClosedKeepsNewerRunState 摘除必须是条件性的：旧运行态的收尾不能误删
// Resume 已经换上的新运行态（冷恢复重起后旧连接的 OnClosed 可能才到）。
func TestOnClosedKeepsNewerRunState(t *testing.T) {
	a, old := NewAdapterWithRunForTest("t1")
	fresh := &runState{taskID: "t1", pending: map[string]pendingPerm{},
		evCh: make(chan executor.AdapterEvent, 8), acc: newTurnAccumulator()}
	a.mu.Lock()
	a.runs["t1"] = fresh // 冷恢复已经换上新的
	a.mu.Unlock()

	a.onClosed(old, errors.New("旧连接迟到的断开回调"))

	if got := a.lookup("t1"); got != fresh {
		t.Fatal("旧连接的收尾不得删掉冷恢复换上的新运行态")
	}
}
