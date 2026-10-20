package agentd

// B233.11 启动对账接缝测试：启动对账对共享 sched_running 不清无主占用——本机任务表
// 没有非终态 owner 不构成清零授权，本机终态任务也不是。
//
// B448 r1（Q-r1-1 甲）：对账从 RecoverOnStartup 迁到 StartAutomation 的同步前置，
// 本文件五条用例改打新入口（startAutomationForTest），断言语义不变；夹具 body 都是
// 只含 count 的旧格式，按 F39 不触发对账写。RecoverOnStartup 仍随后调用，只为保留
// 原有的 executor 探活断言。

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

type testSchedRunningBody struct {
	Count int `json:"count"`
}

func setupRecoveryEnv(t *testing.T) *ledgerEnv {
	t.Helper()
	env := newNoPTYLedgerEnv(t)
	SetupAutomationForTest(t, env.srv, env.ledger)
	return env
}

func putSchedRunning(t *testing.T, f *ledgerapi.Facade, key string, count int) {
	t.Helper()
	body, err := json.Marshal(testSchedRunningBody{Count: count})
	if err != nil {
		t.Fatalf("编码 sched_running %s: %v", key, err)
	}
	if _, err := f.Put("sched_running", key, 0, body, "test"); err != nil {
		t.Fatalf("写入 sched_running %s: %v", key, err)
	}
}

func schedRunningRecord(t *testing.T, f *ledgerapi.Facade, key string) (int, int) {
	t.Helper()
	record, err := f.Get("sched_running", key)
	if err != nil {
		t.Fatalf("读取 sched_running %s: %v", key, err)
	}
	var body testSchedRunningBody
	if err := json.Unmarshal(record.Body, &body); err != nil {
		t.Fatalf("解码 sched_running %s: %v", key, err)
	}
	return record.Version, body.Count
}

func createRecoveryTask(t *testing.T, env *ledgerEnv, id string, state proto.TaskState, carrier, squad string) {
	t.Helper()
	now := time.Now().UTC()
	if err := env.st.CreateTask(&proto.Task{ID: id, Target: "local", Executor: "fake",
		Carrier: carrier, Squad: squad, State: state, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatalf("CreateTask %s: %v", id, err)
	}
}

// startAutomationForTest 打启动对账的真实入口 StartAutomation（对账成功后才起循环）；
// 循环 ctx 随测试结束取消。
func startAutomationForTest(t *testing.T, srv *Server) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return srv.StartAutomation(ctx)
}

func recoverWithoutProbe(t *testing.T, srv *Server) error {
	t.Helper()
	if err := startAutomationForTest(t, srv); err != nil {
		return err
	}
	probed := false
	err := srv.RecoverOnStartup(func(string) bool {
		probed = true
		return true
	}, func(string) {}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if probed {
		t.Fatal("pending/无任务启动对账不应探活 executor")
	}
	return err
}

func TestB23310RecoverKeepsPendingCarrierAndMember(t *testing.T) {
	env := setupRecoveryEnv(t)
	createRecoveryTask(t, env, "recover-pending", proto.TaskStatePending, "carrier-A", "squad-S")
	facade := ledgerapi.New(env.ledger)
	carrierKey := scheduling.OccupancyCarrierKey("carrier-A")
	memberKey := scheduling.OccupancyMemberKey("squad-S", "carrier-A")
	putSchedRunning(t, facade, carrierKey, 1)
	putSchedRunning(t, facade, memberKey, 1)
	carrierVersion, carrierCount := schedRunningRecord(t, facade, carrierKey)
	memberVersion, memberCount := schedRunningRecord(t, facade, memberKey)

	if err := recoverWithoutProbe(t, env.srv); err != nil {
		t.Fatalf("pending 启动对账: %v", err)
	}
	if gotVersion, gotCount := schedRunningRecord(t, facade, carrierKey); gotVersion != carrierVersion || gotCount != carrierCount {
		t.Fatalf("pending carrier owner 被改写：before=(v%d,count%d) after=(v%d,count%d)", carrierVersion, carrierCount, gotVersion, gotCount)
	}
	if gotVersion, gotCount := schedRunningRecord(t, facade, memberKey); gotVersion != memberVersion || gotCount != memberCount {
		t.Fatalf("pending member owner 被改写：before=(v%d,count%d) after=(v%d,count%d)", memberVersion, memberCount, gotVersion, gotCount)
	}
}

func TestB23311RecoverKeepsForeignOccupancyWithoutLocalOwner(t *testing.T) {
	env := setupRecoveryEnv(t)
	facade := ledgerapi.New(env.ledger)
	carrierKey := scheduling.OccupancyCarrierKey("carrier-A")
	memberKey := scheduling.OccupancyMemberKey("squad-S", "carrier-A")
	putSchedRunning(t, facade, carrierKey, 2)
	putSchedRunning(t, facade, memberKey, 3)
	carrierVersion, _ := schedRunningRecord(t, facade, carrierKey)
	memberVersion, _ := schedRunningRecord(t, facade, memberKey)

	if err := recoverWithoutProbe(t, env.srv); err != nil {
		t.Fatalf("无本机任务启动对账: %v", err)
	}
	if gotVersion, gotCount := schedRunningRecord(t, facade, carrierKey); gotVersion != carrierVersion || gotCount != 2 {
		t.Fatalf("本机无任务不得清共享 carrier：before=(v%d,count2) after=(v%d,count%d)", carrierVersion, gotVersion, gotCount)
	}
	if gotVersion, gotCount := schedRunningRecord(t, facade, memberKey); gotVersion != memberVersion || gotCount != 3 {
		t.Fatalf("本机无任务不得清共享 member：before=(v%d,count3) after=(v%d,count%d)", memberVersion, gotVersion, gotCount)
	}
}

func TestB23311RecoverKeepsForeignKeysAlongsideLocalOwner(t *testing.T) {
	env := setupRecoveryEnv(t)
	createRecoveryTask(t, env, "recover-running", proto.TaskStateRunning, "carrier-A", "squad-S")
	facade := ledgerapi.New(env.ledger)
	localCarrier := scheduling.OccupancyCarrierKey("carrier-A")
	localMember := scheduling.OccupancyMemberKey("squad-S", "carrier-A")
	foreignCarrier := scheduling.OccupancyCarrierKey("carrier-F")
	foreignMember := scheduling.OccupancyMemberKey("squad-F", "carrier-F")
	keys := []string{localCarrier, localMember, foreignCarrier, foreignMember}
	for _, key := range keys {
		putSchedRunning(t, facade, key, 1)
	}

	if err := startAutomationForTest(t, env.srv); err != nil {
		t.Fatalf("启动对账: %v", err)
	}
	probes := 0
	err := env.srv.RecoverOnStartup(func(string) bool {
		probes++
		return true
	}, func(string) {}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatalf("本机 owner 与他机键并存启动对账: %v", err)
	}
	if probes != 1 {
		t.Fatalf("running 探活次数=%d, want 1", probes)
	}
	for _, key := range keys {
		if version, count := schedRunningRecord(t, facade, key); version != 1 || count != 1 {
			t.Fatalf("本机 owner 与他人占用并存时 %s 被改写：version=%d count=%d", key, version, count)
		}
	}
}

func TestB23311RecoverKeepsLiveAndTerminalOwners(t *testing.T) {
	env := setupRecoveryEnv(t)
	createRecoveryTask(t, env, "recover-running", proto.TaskStateRunning, "carrier-A", "squad-S")
	createRecoveryTask(t, env, "recover-review", proto.TaskStateWaitingReview, "carrier-B", "squad-T")
	createRecoveryTask(t, env, "recover-completed", proto.TaskStateCompleted, "carrier-C", "squad-U")
	facade := ledgerapi.New(env.ledger)
	keys := []string{
		scheduling.OccupancyCarrierKey("carrier-A"), scheduling.OccupancyMemberKey("squad-S", "carrier-A"),
		scheduling.OccupancyCarrierKey("carrier-B"), scheduling.OccupancyMemberKey("squad-T", "carrier-B"),
		scheduling.OccupancyCarrierKey("carrier-C"), scheduling.OccupancyMemberKey("squad-U", "carrier-C"),
	}
	for _, key := range keys {
		putSchedRunning(t, facade, key, 1)
	}

	if err := startAutomationForTest(t, env.srv); err != nil {
		t.Fatalf("启动对账: %v", err)
	}
	probes := 0
	err := env.srv.RecoverOnStartup(func(string) bool {
		probes++
		return true
	}, func(string) {}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)))
	if err != nil {
		t.Fatalf("活跃 owner 启动对账: %v", err)
	}
	if probes != 2 {
		t.Fatalf("running/waiting_review 探活次数=%d, want 2", probes)
	}
	for _, key := range keys {
		if version, count := schedRunningRecord(t, facade, key); version != 1 || count != 1 {
			t.Fatalf("任务 owner %s 被启动对账改写：version=%d count=%d", key, version, count)
		}
	}
}

func TestB23310RecoverRejectsMalformedSchedRunning(t *testing.T) {
	env := setupRecoveryEnv(t)
	facade := ledgerapi.New(env.ledger)
	key := scheduling.OccupancyCarrierKey("carrier-malformed")
	if _, err := facade.Put("sched_running", key, 0, []byte(`{"count":1}`), "test"); err != nil {
		t.Fatalf("写入 malformed 前记录: %v", err)
	}
	record, err := facade.Get("sched_running", key)
	if err != nil {
		t.Fatalf("读取 malformed 前记录: %v", err)
	}
	if _, err := facade.Put("sched_running", key, record.Version, []byte(`not-json`), "test"); err != nil {
		t.Fatalf("写入 malformed 记录: %v", err)
	}
	var logs bytes.Buffer
	env.srv.log = slog.New(slog.NewTextHandler(&logs, nil))
	err = startAutomationForTest(t, env.srv)
	if err == nil || !strings.Contains(err.Error(), key) {
		t.Fatalf("malformed sched_running 应返回带 key 错误，实际 %v", err)
	}
	if strings.Contains(logs.String(), "自动化清队与事件唤醒循环启动") {
		t.Fatal("对账失败后不得启动自动化循环")
	}
	if !strings.Contains(logs.String(), key) || !strings.Contains(logs.String(), "version=2") {
		t.Fatalf("malformed 日志缺少 key/version：%s", logs.String())
	}
}
