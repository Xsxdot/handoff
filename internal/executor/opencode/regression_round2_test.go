// 本文件是第二轮外部代码审阅（docs/superpowers/reviews/2026-08-08-mvp-code-review-round2.md）
// 确认缺陷的回归测试。
//
// 职责：
//   - 锁定 C-1（git 兜底基线按回合刷新）、C-2（idle 去抖，中途 idle 不结束回合）、
//     N-5（serve.log 尾部有界读取）三项修复的行为契约
//
// 边界：
//   - 只测本包内的行为，不涉及 agentd 中介层（那部分回归测试在 internal/agentd）
//   - 不依赖真实 opencode 二进制与 tmux，全部走 fake server + 假探活
package opencode

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
)

// statusBusyEvent 构造一条 session.status（status.type=busy，回合进行中信号）。
func statusBusyEvent() string {
	return sseLine(map[string]any{
		"type":      "session.status",
		"sessionID": "sess-1",
		"properties": map[string]any{
			"sessionID": "sess-1",
			"status":    map[string]any{"type": "busy"},
		},
	})
}

// TestFallbackBaselineRefreshedPerTurn 验证 git 兜底分类的基线按「回合」刷新而非
// 按「run」固定（C-1）。
//
// 场景（handoff 主路径：提问 → reply → continue 的多回合）：
//   - 回合一：executor 干完活提交了新 commit 但忘了输出 trailer → 兜底判 result
//   - 回合二：审核者追问，executor 只用散文回答、没有任何新提交 → 必须判 question
//
// 修复前 startCommit 只在 startRun 捕获一次，回合二仍与 run 起点比较，
// hasNew 恒为 true，于是带着回合一的 commit hash 谎报 completed。
func TestFallbackBaselineRefreshedPerTurn(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	repo := initGit(t)
	_, ch := startFakeRun(t, fs, "task-baseline", repo, t.TempDir())

	// 回合一：先有新提交，再无 trailer 收尾 → 兜底应判 result
	gitCommit(t, repo, "feature.go", "package main\n", "回合一的产出")
	fs.push(userMsgEvent("msg-u1"))
	fs.push(partUpdatedEvent("msg-a1", "prt-a1", "第一回合做完了但忘了写 trailer"))
	fs.push(statusBusyEvent())
	fs.push(statusIdleEvent())
	first := waitEventType(t, ch, "result")
	if first.Result == nil || !first.Result.OK {
		t.Fatalf("回合一应判 result OK，实际 %+v", first.Result)
	}
	turn1Commit := first.Result.CommitHash

	// 回合二：没有任何新提交，同样无 trailer → 必须判 question 交审核者裁决
	fs.push(partUpdatedEvent("msg-a2", "prt-a2", "我确认了一下，不需要改动"))
	fs.push(statusBusyEvent())
	fs.push(statusIdleEvent())

	second := waitAnyClassified(t, ch)
	if second.Type == "result" {
		t.Fatalf("回合二没有新提交，却判成了 result（谎报完工，commit=%s，回合一 commit=%s）",
			second.Result.CommitHash, turn1Commit)
	}
	if second.Type != "question" {
		t.Fatalf("回合二应判 question，实际 %s", second.Type)
	}
	if !strings.Contains(second.Text, "不需要改动") {
		t.Errorf("question 文本应为回合二全文，实际 %q", second.Text)
	}
}

// TestTransientIdleDoesNotEndTurn 验证回合中途的瞬时 idle 不结束回合（C-2）。
//
// 场景：模型先输出一段中间说明 → 触发一次 idle（工具调用间隙等）→ 继续 busy 并
// 产出带 trailer 的最终文本 → 真正的 idle。
//
// 修复前任何 idle 都立即分类：中途那次 idle 会把「我先看看代码」当成一个完整回合，
// 若此时 repo 已有新提交更会误报 completed —— 审核者据此执行 done，Stop 会在
// opencode 仍在干活时杀掉 tmux 会话。
//
// 修复后 idle 只是「候选回合结束」，需静默 idleGrace 才生效；宽限期内任何新增
// 文本或 busy 状态都会撤销它。
func TestTransientIdleDoesNotEndTurn(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	repo := initGit(t)
	// 让 repo 带新提交：修复前中途 idle 会命中 git 兜底谎报 completed，
	// 这样断言失败时的现象与线上一致（不是单纯多一条 question）
	gitCommit(t, repo, "wip.go", "package main\n", "执行中的提交")
	_, ch := startFakeRun(t, fs, "task-transient-idle", repo, t.TempDir())

	fs.push(userMsgEvent("msg-u1"))
	fs.push(statusBusyEvent())
	fs.push(partUpdatedEvent("msg-a1", "prt-a1", "我先看看代码。"))
	fs.push(statusIdleEvent()) // 中途瞬时 idle：不得结束回合
	fs.push(statusBusyEvent()) // 宽限期内恢复 busy → 撤销上面的 idle
	fs.push(partUpdatedEvent("msg-a1", "prt-a2",
		"\n改完了\n{\"branch\":\"handoff/T1\",\"commit\":\"abc12345\",\"summary\":\"完成\"}"))
	fs.push(statusIdleEvent()) // 真正的回合结束

	ev := waitAnyClassified(t, ch)
	if ev.Type != "result" {
		t.Fatalf("应在真正的回合结束处判 result，实际 %s（text=%q）", ev.Type, ev.Text)
	}
	if ev.Result.Summary != "完成" {
		t.Fatalf("应取最终 trailer 的 summary，实际 %+v —— 中途 idle 提前结束了回合", ev.Result)
	}
}

// TestIdleClassifiesAfterGrace 验证宽限期过后正常 idle 仍然结束回合（C-2 的反向
// 保证）：去抖不得把「回合永远不结束」变成新的挂死方式。
func TestIdleClassifiesAfterGrace(t *testing.T) {
	quietLog(t)
	fs := newFakeServer(t)
	_, ch := startFakeRun(t, fs, "task-idle-normal", initGit(t), t.TempDir())

	fs.push(userMsgEvent("msg-u1"))
	fs.push(statusBusyEvent())
	fs.push(partUpdatedEvent("msg-a1", "prt-a1", "分析完毕\n{\"ask\":\"用哪个实现？\"}"))
	fs.push(statusIdleEvent())

	ev := waitEventType(t, ch, "question")
	if ev.Text != "用哪个实现？" {
		t.Fatalf("question 文本错误：%q", ev.Text)
	}
}

// TestServeLogTailBounded 验证 serve.log 尾部读取是有界的（N-5）。
//
// serve.log 由 tee -a 写满任务全程且无轮转，而 serveLogTail 的调用时机恰是
// serve 死亡/就绪超时——最不该再分配几百 MB 的时刻。修复前 os.ReadFile 整读，
// 100MiB 日志即分配 100MiB 只为取末尾 500 字节。
func TestServeLogTailBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "serve.log")
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("建 serve.log: %v", err)
	}
	// 稀疏文件：占逻辑大小但不实际写盘，末尾写入可校验的尾部内容
	const size = 100 << 20
	if err := f.Truncate(size); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	wantTail := "panic: opencode 崩溃现场"
	if _, err := f.WriteAt([]byte(wantTail), size); err != nil {
		t.Fatalf("写尾部: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("关闭: %v", err)
	}

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	got := serveLogTail(path)
	runtime.ReadMemStats(&after)

	if !strings.Contains(got, wantTail) {
		t.Fatalf("应返回日志尾部，实际 %q", got)
	}
	alloc := after.TotalAlloc - before.TotalAlloc
	if alloc > 4<<20 {
		t.Errorf("读取 %d 字节日志的尾部分配了 %d 字节（应有界，远小于文件本身）", size, alloc)
	}
}

// waitAnyClassified 消费事件通道直到出现一条回合分类事件（question/result），
// 跳过 progress 噪音。与 waitEventType 的区别：不预设期望类型，用于「分类结果
// 本身就是被测对象」的断言。
func waitAnyClassified(t *testing.T, ch <-chan executor.AdapterEvent) executor.AdapterEvent {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Type == "question" || ev.Type == "result" {
				return ev
			}
		case <-deadline:
			t.Fatalf("等待回合分类事件超时")
		}
	}
}
