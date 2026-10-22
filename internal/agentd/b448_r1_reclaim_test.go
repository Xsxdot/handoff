package agentd

// b448_r1_reclaim_test.go —— B448 增补 r1（S7 名额持有机）的 agentd 接缝测试。
//
// 职责：经真实账本门面与真实编制域，锁住启动对账的入口与时序（T9/F48）、
// owned 取本机任务表口径（T5）、观察面不暴露持有分布（T3/Q-r1-5 甲）、
// RecoverOnStartup 不再对账（Q-r1-1 甲）。扣减规则本身在 scheduling 包测试。
// 边界：持有者名在这些夹具里是 os.Hostname()（测试编制域未配自名，F45 回退）。

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"

	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/ledgerstep"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// r1LaunchQueuePutCounter 包住真实账本端口，只数 launch 队列的写入次数——
// 首轮准入若撞 ErrNoSlot，drainQueuesOnce 轮末回填会再 Put 这一行；对账先于
// 首轮时这一行只被出队删除、从不回写。读写一律透传，不改任何读数。
type r1LaunchQueuePutCounter struct {
	inner schedclient.Registry
	mu    sync.Mutex
	puts  int
}

func (r *r1LaunchQueuePutCounter) Put(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	if kind == scheduling.KindLaunchQueue {
		r.mu.Lock()
		r.puts++
		r.mu.Unlock()
	}
	return r.inner.Put(kind, id, expectVersion, body, actor)
}

func (r *r1LaunchQueuePutCounter) Get(kind, id string) (schedclient.Record, error) {
	return r.inner.Get(kind, id)
}

func (r *r1LaunchQueuePutCounter) List(kind string) ([]schedclient.Record, error) {
	return r.inner.List(kind)
}

func (r *r1LaunchQueuePutCounter) Delete(kind, id string, expectVersion int, actor string) error {
	return r.inner.Delete(kind, id, expectVersion, actor)
}

func (r *r1LaunchQueuePutCounter) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.puts
}

type r1HoldersBody struct {
	Count   int            `json:"count"`
	Holders map[string]int `json:"holders"`
}

func r1Host(t *testing.T) string {
	t.Helper()
	host, err := os.Hostname()
	if err != nil || host == "" {
		t.Skipf("取不到 hostname，无法构造本机持有者名: %v", err)
	}
	return host
}

func r1PutBody(t *testing.T, f *ledgerapi.Facade, key string, body r1HoldersBody) {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("编码 %s: %v", key, err)
	}
	if _, err := f.Put("sched_running", key, 0, raw, "test"); err != nil {
		t.Fatalf("预置 %s: %v", key, err)
	}
}

func r1ReadBody(t *testing.T, f *ledgerapi.Facade, key string) r1HoldersBody {
	t.Helper()
	rec, err := f.Get("sched_running", key)
	if err != nil {
		t.Fatalf("读 %s: %v", key, err)
	}
	var b r1HoldersBody
	if err := json.Unmarshal(rec.Body, &b); err != nil {
		t.Fatalf("解码 %s: %v", key, err)
	}
	if len(b.Holders) == 0 {
		b.Holders = nil
	}
	return b
}

// T9（竖切）：上一进程被杀留下 count=1、holders={self:1}，载体上限 1；launch 队列
// 有一条协调者拉起请求。StartAutomation 之后首轮准入必须成功（Runner 收到 Launch），
// 而不是 ErrNoSlot 回填——对账必须先于循环首轮（F48）。
// 只看「最终准入成功」无牙：错序时首轮 no_slot 回填、对账后下一轮仍会准入成功；
// 故另数 launch 队列入队后的写入次数，首轮即准入时必须为 0（无回填）。
func TestB448R1StartAutomationReclaimsBeforeFirstAdmission(t *testing.T) {
	host := r1Host(t)
	env := setupNoPTYSquadEnv(t, 1)
	runner := seedQueueCoordinator(t, env)
	queueWrites := &r1LaunchQueuePutCounter{inner: facadeAsRegistry{f: env.srv.autoLedger}}
	env.srv.SetScheduling(scheduling.New(queueWrites))
	facade := ledgerapi.New(env.ledger)
	for _, key := range []string{"squad/coord/coord-carrier", "carrier/coord-carrier"} {
		r1PutBody(t, facade, key, r1HoldersBody{Count: 1, Holders: map[string]int{host: 1}})
	}
	coordCard := createCoordCard(t, env)
	if _, err := mustScheduling(t, env.srv).Enqueue(scheduling.IgnitionRequest{
		Card: coordCard, Squad: "coord", Actor: "test", Ready: true,
	}, scheduling.KindLaunchQueue); err != nil {
		t.Fatalf("入队协调者: %v", err)
	}
	enqueued := queueWrites.count()
	env.srv.runStepFn = func(_ context.Context, _ *ledgerstep.StepRunner, _, _ string) {}

	if err := startAutomationForTest(t, env.srv); err != nil {
		t.Fatalf("StartAutomation: %v", err)
	}
	waitFor(t, func() bool {
		launches, _, _ := runner.snapshot()
		return launches >= 1
	})
	waitFor(t, func() bool {
		return runningCountIn(t, env.srv.autoLedger, "carrier/coord-carrier") == 0
	})
	if backfills := queueWrites.count() - enqueued; backfills != 0 {
		t.Fatalf("首轮准入应直接成功、请求不回填：launch 队列入队后又被写 %d 次（对账晚于首轮准入，F48）", backfills)
	}
}

// T5（agentd 口径）：owned 取本机任务表里非终态（含 waiting_review、pending）且
// 带载体的任务，按 OccupancyKeys 计数；终态任务不计入。他机条目不动。
func TestB448R1ReclaimKeepsLiveLocalTasks(t *testing.T) {
	host := r1Host(t)
	env := setupRecoveryEnv(t)
	createRecoveryTask(t, env, "r1-pending", proto.TaskStatePending, "carrier-A", "squad-S")
	createRecoveryTask(t, env, "r1-review", proto.TaskStateWaitingReview, "carrier-A", "squad-S")
	createRecoveryTask(t, env, "r1-done", proto.TaskStateCompleted, "carrier-A", "squad-S")
	facade := ledgerapi.New(env.ledger)
	carrierKey := scheduling.OccupancyCarrierKey("carrier-A")
	memberKey := scheduling.OccupancyMemberKey("squad-S", "carrier-A")
	r1PutBody(t, facade, carrierKey, r1HoldersBody{Count: 4, Holders: map[string]int{host: 3, "B": 1}})
	r1PutBody(t, facade, memberKey, r1HoldersBody{Count: 3, Holders: map[string]int{host: 3}})

	if err := startAutomationForTest(t, env.srv); err != nil {
		t.Fatalf("StartAutomation: %v", err)
	}
	if got, want := r1ReadBody(t, facade, carrierKey), (r1HoldersBody{Count: 3, Holders: map[string]int{host: 2, "B": 1}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("载体键对账后 %+v，want %+v", got, want)
	}
	if got, want := r1ReadBody(t, facade, memberKey), (r1HoldersBody{Count: 2, Holders: map[string]int{host: 2}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("成员键对账后 %+v，want %+v", got, want)
	}
}

// Q-r1-1 甲：RecoverOnStartup 只做 executor 恢复，不再读 sched_running——损坏 body
// 不再让它失败（阻断职责已移到 StartAutomation，见 TestB23310RecoverRejectsMalformedSchedRunning）。
func TestB448R1RecoverOnStartupNoLongerReconciles(t *testing.T) {
	env := setupRecoveryEnv(t)
	createRecoveryTask(t, env, "r1-running", proto.TaskStateRunning, "carrier-A", "squad-S")
	facade := ledgerapi.New(env.ledger)
	if _, err := facade.Put("sched_running", "carrier/bad", 0, []byte(`not-json`), "test"); err != nil {
		t.Fatalf("写损坏 body: %v", err)
	}
	probes := 0
	err := env.srv.RecoverOnStartup(func(string) bool {
		probes++
		return true
	}, func(string) {}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err != nil || probes != 1 {
		t.Fatalf("RecoverOnStartup 不应再对账：err=%v probes=%d，want nil/1", err, probes)
	}
}

// T3（观察面）：body 带 holders 后，GET /api/squads 的 running[].count 不变，
// 响应里没有 holders 字段（Q-r1-5 甲，wire 零改）。
func TestB448R1SquadsRunningViewHidesHolders(t *testing.T) {
	env := newSchedNoPTYEnv(t)
	putOnlineCarrier(t, env.svc, scheduling.Carrier{Name: "c9", Machine: "m1", CLI: "opencode",
		HomeDir: "/h", MaxConcurrency: 4,
		Status: scheduling.StatusOnline})
	for i := 0; i < 2; i++ {
		if _, err := env.svc.AdmitCarrier("c9"); err != nil {
			t.Fatalf("AdmitCarrier: %v", err)
		}
	}
	rec, err := env.srv.autoLedger.Get("sched_running", "carrier/c9")
	if err != nil || !strings.Contains(string(rec.Body), `"holders"`) {
		t.Fatalf("前提：账本 body 应带 holders，得 %s err=%v", rec.Body, err)
	}
	code, body := schedReq(t, env, http.MethodGet, "/api/squads", "")
	if code != http.StatusOK {
		t.Fatalf("GET 应 200，得 %d：%s", code, body)
	}
	if strings.Contains(body, "holders") {
		t.Fatalf("观察面不应暴露 holders：%s", body)
	}
	var resp proto.SquadsResp
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("解码: %v", err)
	}
	for _, row := range resp.Running {
		if row.Key == "carrier/c9" && row.Count == 2 {
			return
		}
	}
	t.Fatalf("running 视图缺 carrier/c9=2：%+v", resp.Running)
}
