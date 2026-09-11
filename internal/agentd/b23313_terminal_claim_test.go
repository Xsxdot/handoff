package agentd

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/executor/fake"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/Xsxdot/handoff/internal/store"
)

// 断言（Manager 级）：并发 Done 同一 waiting_review 任务，Claimed 恰一次；
// loser 拿到 Claimed=false 且 err==nil（幂等成功，不冒充释放者）。
func TestB23313ConcurrentDoneClaimedOnce(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	const taskID = "b23313-concurrent-done"
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake",
		Carrier: "muse", HomeDir: "~/.handoff/home/muse", State: proto.TaskStateWaitingReview,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	mgr := NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"fake": fake.New(nil)},
		env.srv.conf(), nil, nil, newTestGate(t), discardLogger())
	mgr.SetWorkspace(NewGitCapability())

	var wg sync.WaitGroup
	claimed := make(chan bool, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outcome, err := mgr.Done(context.Background(), taskID, "")
			claimed <- outcome.Claimed
			errs <- err
		}()
	}
	wg.Wait()
	close(claimed)
	close(errs)
	nClaimed := 0
	for c := range claimed {
		if c {
			nClaimed++
		}
	}
	if nClaimed != 1 {
		t.Fatalf("并发 Done 的 Claimed 次数=%d, want 1", nClaimed)
	}
	for err := range errs {
		if err != nil {
			t.Fatalf("并发 Done 的 loser 应 err==nil（幂等）: %v", err)
		}
	}
	// Manager.Done 本身不释放占用（释放是 handler 按 Claimed 门的副作用）；
	// 载体恰释放一次由下面 handler 级用例锁。
}

// 断言（handler 级，S1）：两个 goroutine 并发打 HTTP done，两次都 200；
// 载体占用恰释放一次（用 carrier 记录的 Version 增量计数，见
// receive_occupancy_test.go#TestHandleStopDoesNotReturnSuccessWithoutTerminalSnapshot）。
func TestB23313ConcurrentDoneReleasesOnce(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	if _, err := env.srv.Scheduling().AdmitCarrier("muse"); err != nil {
		t.Fatalf("AdmitCarrier: %v", err)
	}
	const taskID = "b23313-concurrent-done-release"
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake",
		Carrier: "muse", HomeDir: "~/.handoff/home/muse", State: proto.TaskStateWaitingReview,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	mgr := NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"fake": fake.New(nil)},
		env.srv.conf(), nil, nil, newTestGate(t), discardLogger())
	mgr.SetWorkspace(NewGitCapability())
	env.srv.SetManager(mgr)

	facade := receiverOccupancyFacade(env.ledgerEnv)
	before, err := facade.Get("sched_running", scheduling.OccupancyCarrierKey("muse"))
	if err != nil {
		t.Fatalf("读 done 前载体记录: %v", err)
	}
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rr := runAction(env.srv, actionRequest(taskID, "done", `{}`), env.srv.handleDone)
			codes <- rr.Code
		}()
	}
	wg.Wait()
	close(codes)
	for code := range codes {
		if code != http.StatusOK {
			t.Fatalf("并发 done 都应 200，got %d", code)
		}
	}
	if got := runningCountIn(t, facade, scheduling.OccupancyCarrierKey("muse")); got != 0 {
		t.Fatalf("done 后载体计数=%d, want 0", got)
	}
	after, err := facade.Get("sched_running", scheduling.OccupancyCarrierKey("muse"))
	if err != nil {
		t.Fatalf("读 done 后载体记录: %v", err)
	}
	if after.Version != before.Version+1 {
		t.Fatalf("载体释放必须恰一次：before=v%d after=v%d", before.Version, after.Version)
	}
}

// 断言（Manager 级，S1 Stop）：并发 Stop 非终态任务，一个赢得 CAS，
// 另一个返回 store.ErrBadTransit（HTTP 409 语义）。
func TestB23313ConcurrentStopSecondIs409(t *testing.T) {
	env := newReceiverTestEnv(t)
	seedDefaultFakeCarrier(t, env.srv, "fake")
	const taskID = "b23313-concurrent-stop"
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: taskID, Target: "local", Executor: "fake",
		Carrier: "muse", HomeDir: "~/.handoff/home/muse", State: proto.TaskStateRunning,
		CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask: %v", err)
	}
	mgr := NewManager(env.st, env.srv.Hub(), map[string]executor.Adapter{"fake": fake.New(nil)},
		env.srv.conf(), nil, nil, newTestGate(t), discardLogger())
	mgr.SetWorkspace(NewGitCapability())

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := mgr.Stop(context.Background(), taskID)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	nBadTransit := 0
	for err := range results {
		if errors.Is(err, store.ErrBadTransit) {
			nBadTransit++
		} else if err != nil {
			t.Fatalf("并发 Stop 只允许 ErrBadTransit，got %v", err)
		}
	}
	if nBadTransit != 1 {
		t.Fatalf("并发 Stop 的 ErrBadTransit 次数=%d, want 1", nBadTransit)
	}
}

// 断言（S2）：SetManager 收到 typed-nil 后落 nil，handler 仍 503，不 panic。
func TestB23313SetManagerRejectsTypedNil(t *testing.T) {
	env := newReceiverTestEnv(t)
	var typedNil *Manager
	env.srv.SetManager(typedNil)
	if env.srv.mgr != nil {
		t.Fatalf("typed-nil 必须落成 nil 接口，got %#v", env.srv.mgr)
	}
	rr := runAction(env.srv, actionRequest("x", "done", `{}`), env.srv.handleDone)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("manager 未就绪应 503，got %d", rr.Code)
	}
}
