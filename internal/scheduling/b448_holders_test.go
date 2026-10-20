package scheduling_test

// b448_holders_test.go —— B448 增补 r1（S7 名额持有机）的调度域接缝测试。
//
// 职责：经真实账本门面（ledger SQLite + facadeRegistry 适配）锁住 sched_running
// body 的持有分布维护（F38–F47）与本机遗留归还规则（T1–T8）。
// 边界：owned 由调用方（orchestration）从本机任务表算出，本文件直接给 map；
// 启动时序（T9/T10）与任务表口径在 agentd 包测试。

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"

	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// holdersBody 是测试侧对 F38 形状的独立解码（不复用生产类型，避免同错同过）。
type holdersBody struct {
	Count   int            `json:"count"`
	Holders map[string]int `json:"holders"`
}

// legacyBody 与旧二进制读者同形（registry_read.go / watchdog.go / readCount 基线）。
type legacyBody struct {
	Count int `json:"count"`
}

func readHolders(t *testing.T, f *ledgerapi.Facade, key string) (holdersBody, int) {
	t.Helper()
	rec, err := f.Get("sched_running", key)
	if err != nil {
		t.Fatalf("读 sched_running %s: %v", key, err)
	}
	var b holdersBody
	if err := json.Unmarshal(rec.Body, &b); err != nil {
		t.Fatalf("解码 sched_running %s: %v（body=%s）", key, err, rec.Body)
	}
	if len(b.Holders) == 0 {
		b.Holders = nil // F39：缺失/{}/null 同义，统一成 nil 比较
	}
	return b, rec.Version
}

func putRaw(t *testing.T, f *ledgerapi.Facade, key, body string) int {
	t.Helper()
	expect := 0
	if rec, err := f.Get("sched_running", key); err == nil {
		expect = rec.Version
	}
	v, err := f.Put("sched_running", key, expect, []byte(body), "test")
	if err != nil {
		t.Fatalf("直写 sched_running %s: %v", key, err)
	}
	return v
}

// newHoldersPair 返回共用一个账本的两个服务（自名 A、B），已登记执行者小队
// sq（成员 c1，上限 8）与载体 c1（上限 8，在线）。
func newHoldersPair(t *testing.T) (a, b *scheduling.Service, f *ledgerapi.Facade) {
	t.Helper()
	a, f = newEmptySvc(t)
	a.SetSelfName("A")
	b = scheduling.New(facadeRegistry{f: f})
	b.SetSelfName("B")
	putOnlineCarrier(t, a, scheduling.Carrier{Name: "c1", Machine: "linux-01", CLI: "opencode",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 8, Status: scheduling.StatusOnline})
	if err := a.PutSquad(scheduling.Squad{Name: "sq", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "c1", MaxConcurrency: 8}}}, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
	return a, b, f
}

func admitSq(t *testing.T, svc *scheduling.Service) {
	t.Helper()
	if _, err := svc.Admit(scheduling.IgnitionRequest{Card: "B1", Squad: "sq", Actor: "test"}); err != nil {
		t.Fatalf("准入: %v", err)
	}
}

// T1 / F43 / F44：准入与归还在同一次写里同时维护 count 与 holders[self]，
// 且保留他机条目。
func TestB448R1HoldersTrackAdmitAndRelease(t *testing.T) {
	a, b, f := newHoldersPair(t)
	admitSq(t, a)
	admitSq(t, a)
	admitSq(t, b)
	if err := a.Release("sq", "c1"); err != nil {
		t.Fatalf("A 归还: %v", err)
	}
	want := holdersBody{Count: 2, Holders: map[string]int{"A": 1, "B": 1}}
	for _, key := range []string{"squad/sq/c1", "carrier/c1"} {
		got, _ := readHolders(t, f, key)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s body=%+v，want %+v", key, got, want)
		}
	}
	// 归零删键：A 再还一次后 holders 里不再有 A。
	if err := a.Release("sq", "c1"); err != nil {
		t.Fatalf("A 再归还: %v", err)
	}
	got, _ := readHolders(t, f, "carrier/c1")
	if !reflect.DeepEqual(got, holdersBody{Count: 1, Holders: map[string]int{"B": 1}}) {
		t.Fatalf("A 归零后 body=%+v，want count=1 holders={B:1}", got)
	}
}

// T1（直派）：AdmitCarrier 只写载体键，同样记持有。
func TestB448R1AdmitCarrierRecordsHolder(t *testing.T) {
	a, _, f := newHoldersPair(t)
	if _, err := a.AdmitCarrier("c1"); err != nil {
		t.Fatalf("AdmitCarrier: %v", err)
	}
	got, _ := readHolders(t, f, "carrier/c1")
	if !reflect.DeepEqual(got, holdersBody{Count: 1, Holders: map[string]int{"A": 1}}) {
		t.Fatalf("直派 body=%+v，want count=1 holders={A:1}", got)
	}
}

// T2 / F42：旧 body 上的新写——新解码器把 {"count":3} 读成 count=3、空持有。
func TestB448R1NewWriterOnLegacyBody(t *testing.T) {
	a, _, f := newHoldersPair(t)
	putRaw(t, f, "carrier/c1", `{"count":3}`)
	if _, err := a.AdmitCarrier("c1"); err != nil {
		t.Fatalf("AdmitCarrier: %v", err)
	}
	got, _ := readHolders(t, f, "carrier/c1")
	if !reflect.DeepEqual(got, holdersBody{Count: 4, Holders: map[string]int{"A": 1}}) {
		t.Fatalf("旧 body 上新写 body=%+v，want count=4 holders={A:1}", got)
	}
}

// T3 / F41：新 body 给旧读者（金样本）——只认 count 的解码器解新 body 不报错且
// count 正确；RunningCounts 投影仍只给 count。
func TestB448R1LegacyReaderDecodesNewBody(t *testing.T) {
	a, b, f := newHoldersPair(t)
	admitSq(t, a)
	admitSq(t, b)
	rec, err := f.Get("sched_running", "carrier/c1")
	if err != nil {
		t.Fatalf("读新 body: %v", err)
	}
	if !strings.Contains(string(rec.Body), `"holders"`) {
		t.Fatalf("新 body 应带 holders（否则金样本无意义）: %s", rec.Body)
	}
	var old legacyBody
	if err := json.Unmarshal(rec.Body, &old); err != nil || old.Count != 2 {
		t.Fatalf("旧形解码新 body：count=%d err=%v，want 2/nil（body=%s）", old.Count, err, rec.Body)
	}
	counts, err := a.RunningCounts()
	if err != nil {
		t.Fatalf("RunningCounts: %v", err)
	}
	if counts["carrier/c1"] != 2 || counts["squad/sq/c1"] != 2 {
		t.Fatalf("RunningCounts=%v，want 两键均 2", counts)
	}
}

// T4 / F46 / F47：本机无任务时扣光 holders[self]，他机条目不动，version+1。
func TestB448R1ReclaimDeductsLocalOnly(t *testing.T) {
	a, _, f := newHoldersPair(t)
	v0 := putRaw(t, f, "carrier/c1", `{"count":3,"holders":{"A":2,"B":1}}`)
	if _, err := a.ReclaimLocalOccupancy(map[string]int{}); err != nil {
		t.Fatalf("对账: %v", err)
	}
	got, v := readHolders(t, f, "carrier/c1")
	if !reflect.DeepEqual(got, holdersBody{Count: 1, Holders: map[string]int{"B": 1}}) || v != v0+1 {
		t.Fatalf("对账后 body=%+v v=%d，want count=1 holders={B:1} v=%d", got, v, v0+1)
	}
}

// T5（调度侧）：owned 内的占用保留，只扣差额。
func TestB448R1ReclaimKeepsOwned(t *testing.T) {
	a, _, f := newHoldersPair(t)
	putRaw(t, f, "carrier/c1", `{"count":3,"holders":{"A":2,"B":1}}`)
	if _, err := a.ReclaimLocalOccupancy(map[string]int{"carrier/c1": 1}); err != nil {
		t.Fatalf("对账: %v", err)
	}
	got, _ := readHolders(t, f, "carrier/c1")
	if !reflect.DeepEqual(got, holdersBody{Count: 2, Holders: map[string]int{"A": 1, "B": 1}}) {
		t.Fatalf("owned=1 对账后 body=%+v，want count=2 holders={A:1,B:1}", got)
	}
}

// T6 / F39：无 holders、holders 为 {} / null、或 holders[self]=0 时不写，version 不变。
func TestB448R1ReclaimNoopWithoutLocalHolder(t *testing.T) {
	a, _, f := newHoldersPair(t)
	bodies := map[string]string{
		"carrier/legacy": `{"count":2}`,
		"carrier/empty":  `{"count":2,"holders":{}}`,
		"carrier/null":   `{"count":2,"holders":null}`,
		"carrier/other":  `{"count":2,"holders":{"B":2}}`,
		"carrier/zero":   `{"count":0,"holders":{"B":1}}`,
	}
	versions := map[string]int{}
	for key, body := range bodies {
		versions[key] = putRaw(t, f, key, body)
	}
	if _, err := a.ReclaimLocalOccupancy(nil); err != nil {
		t.Fatalf("对账: %v", err)
	}
	for key, body := range bodies {
		rec, err := f.Get("sched_running", key)
		if err != nil {
			t.Fatalf("读 %s: %v", key, err)
		}
		if rec.Version != versions[key] || string(rec.Body) != body {
			t.Fatalf("%s 不应被写：before=(v%d,%s) after=(v%d,%s)", key, versions[key], body, rec.Version, rec.Body)
		}
	}
}

// T7：扣减量以 count 为上限，计数不为负，self 条目被清。
func TestB448R1ReclaimCappedByCount(t *testing.T) {
	a, _, f := newHoldersPair(t)
	putRaw(t, f, "carrier/c1", `{"count":2,"holders":{"A":5}}`)
	if _, err := a.ReclaimLocalOccupancy(nil); err != nil {
		t.Fatalf("对账: %v", err)
	}
	got, _ := readHolders(t, f, "carrier/c1")
	if !reflect.DeepEqual(got, holdersBody{Count: 0}) {
		t.Fatalf("上限对账后 body=%+v，want count=0 无 holders", got)
	}
}

// F49：body 解码失败时对账报错，带 key 与 version。
func TestB448R1ReclaimRejectsMalformed(t *testing.T) {
	a, _, f := newHoldersPair(t)
	putRaw(t, f, "carrier/bad", `{"count":1}`)
	putRaw(t, f, "carrier/bad", `not-json`)
	_, err := a.ReclaimLocalOccupancy(nil)
	if err == nil || !strings.Contains(err.Error(), "carrier/bad") || !strings.Contains(err.Error(), "version=2") {
		t.Fatalf("损坏 body 应报带 key/version 的错，得 %v", err)
	}
}

// conflictOnceRegistry 在对账写（actor=scheduling、目标键、armed）时先让 B 并发
// 准入一次，再回 ErrCASConflict——模拟「对账读后、写前被他机抢写」。exhaust=true
// 时该键的写恒冲突（重试耗尽）。
type conflictOnceRegistry struct {
	inner   schedclient.Registry
	key     string
	mu      sync.Mutex
	armed   bool
	exhaust bool
	during  func()
}

func (r *conflictOnceRegistry) Put(kind, id string, expect int, body []byte, actor string) (int, error) {
	r.mu.Lock()
	hit := kind == "sched_running" && id == r.key && (r.armed || r.exhaust)
	during := r.during
	if hit && !r.exhaust {
		r.armed = false
	}
	r.mu.Unlock()
	if hit {
		if during != nil && !r.exhaust {
			during()
		}
		return 0, schedclient.ErrCASConflict
	}
	return r.inner.Put(kind, id, expect, body, actor)
}

func (r *conflictOnceRegistry) Get(kind, id string) (schedclient.Record, error) {
	return r.inner.Get(kind, id)
}
func (r *conflictOnceRegistry) List(kind string) ([]schedclient.Record, error) {
	return r.inner.List(kind)
}
func (r *conflictOnceRegistry) Delete(kind, id string, expect int, actor string) error {
	return r.inner.Delete(kind, id, expect, actor)
}

// T8：对账写撞 CAS 冲突时重读重算，并发的他机准入与本机扣减两者都保留；
// 重试耗尽只记 Error（带 key/version）不报错。
func TestB448R1ReclaimRetriesOnConflict(t *testing.T) {
	_, b, f := newHoldersPair(t)
	putRaw(t, f, "carrier/c1", `{"count":3,"holders":{"A":2,"B":1}}`)
	reg := &conflictOnceRegistry{inner: facadeRegistry{f: f}, key: "carrier/c1", armed: true}
	reg.during = func() {
		if _, err := b.AdmitCarrier("c1"); err != nil {
			t.Errorf("并发 B 准入: %v", err)
		}
	}
	a := scheduling.New(reg)
	a.SetSelfName("A")
	if _, err := a.ReclaimLocalOccupancy(nil); err != nil {
		t.Fatalf("对账: %v", err)
	}
	got, _ := readHolders(t, f, "carrier/c1")
	if !reflect.DeepEqual(got, holdersBody{Count: 2, Holders: map[string]int{"B": 2}}) {
		t.Fatalf("冲突重试后 body=%+v，want count=3-2+1=2 holders={B:2}", got)
	}
}

func TestB448R1ReclaimRetryExhaustedLogsAndContinues(t *testing.T) {
	_, _, f := newHoldersPair(t)
	v0 := putRaw(t, f, "carrier/c1", `{"count":3,"holders":{"A":2,"B":1}}`)
	putRaw(t, f, "carrier/c2", `{"count":1,"holders":{"A":1}}`)
	reg := &conflictOnceRegistry{inner: facadeRegistry{f: f}, key: "carrier/c1", exhaust: true}
	a := scheduling.New(reg)
	a.SetSelfName("A")
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	if _, err := a.ReclaimLocalOccupancy(nil); err != nil {
		t.Fatalf("重试耗尽不应阻断对账: %v", err)
	}
	if got, v := readHolders(t, f, "carrier/c1"); got.Count != 3 || v != v0 {
		t.Fatalf("耗尽键应保持原样，得 %+v v=%d", got, v)
	}
	if got, _ := readHolders(t, f, "carrier/c2"); got.Count != 0 {
		t.Fatalf("耗尽键之后的键应继续对账，得 %+v", got)
	}
	out := logs.String()
	if !strings.Contains(out, "level=ERROR") || !strings.Contains(out, "carrier/c1") || !strings.Contains(out, "version=") {
		t.Fatalf("重试耗尽日志缺 Error/key/version：%s", out)
	}
}
