package orchestration

// b452_probe_test.go —— B452 段 0 第 3 条承重假设：mediate 处理 completed 之后由另一 goroutine 调 Done
// 不与中介循环互锁。
//
// 不改产品代码的近似：订阅 hub，在收到 completed 事件的那一刻另起 goroutine 调 Done——此刻 mediate
// 仍在 handleResult 的尾部（Publish 之后的 sweep）或已回到 range 等下一条事件；Done 会经 adapter.Stop
// 关闭事件通道，中介循环随之退出。断言：时限内到达 completed、adapter.Stop 恰被调用。
import (
	"context"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/proto"
)

func TestB452ProbeDoneFromGoroutineAfterCompletedNoDeadlock(t *testing.T) {
	repo := initTestRepo(t)
	fk := fake.New(nil) // 空脚本：Start 后停在续接门禁，便于先订阅再放行
	m, st, hub := newTestManagerWithApprover(t, map[string]executor.Adapter{"fake": fk}, "fake", nil)
	pid := registerTestProject(t, m, repo)
	task, err := m.Dispatch(context.Background(), DispatchReq{
		ProjectID: pid, Prompt: "x", Executor: "fake", NewWorktree: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	events, cancel := hub.Subscribe(task.ID)
	defer cancel()
	doneErr := make(chan error, 1)
	go func() {
		for ev := range events {
			if ev.Type == proto.EventTypeCompleted {
				go func() {
					_, err := m.Done(context.Background(), task.ID, "probe")
					doneErr <- err
				}()
				return
			}
		}
	}()
	fk.Add(task.ID, fake.Step{Finish: executor.Result{OK: true, Summary: "s", FinalText: "结论"}})
	if err := fk.Send(context.Background(), task.ID, "go"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-doneErr:
		if err != nil {
			t.Fatalf("goroutine 内 Done 失败: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("5s 内 Done 未返回：疑似与 mediate 互锁")
	}
	cur, err := st.GetTask(task.ID)
	if err != nil || cur.State != proto.TaskStateCompleted {
		t.Fatalf("任务应 completed：%+v err=%v", cur, err)
	}
	stops := fk.Stops()
	if len(stops) != 1 || stops[0] != task.ID {
		t.Fatalf("adapter.Stop 应恰调一次（%s），得 %v", task.ID, stops)
	}
}
