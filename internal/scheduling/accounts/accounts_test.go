package accounts_test

// accounts_test.go —— 账号定义/读数两行写入的承重回归（B453 段 1，plan Q1/V3；B464 起账号由
// 发现建，夹具经 Reconcile 播种，人写只剩阈值/间隔编辑）。
//
// 职责：锁「人写定义」与「机写读数/撞额度标记」物理上不同行、互不顶版本、并发下
// 两侧字段都不丢；锁连带规则「探测开始时刻晚于标记才清标记」「失败保留旧读数与标记」。
// 边界：用内存 registry 实现 schedclient.Registry（与账本适配器同一 CAS 契约）；
// 跨机写回与 HTTP 面由 agentd 的 AccountProbeForward 测试承接。

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// memRegistry 是 schedclient.Registry 的内存实现：整行 CAS，与 ledger.RegistryPut 同判据。
type memRegistry struct {
	mu   sync.Mutex
	seq  int64
	rows map[string]schedclient.Record
}

func newMem() *memRegistry { return &memRegistry{rows: map[string]schedclient.Record{}} }

func (m *memRegistry) Put(kind, id string, expect int, body []byte, _ string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur := m.rows[kind+"/"+id]
	if cur.Version != expect {
		return 0, fmt.Errorf("%s/%s 期望 %d 实际 %d: %w", kind, id, expect, cur.Version, schedclient.ErrCASConflict)
	}
	m.seq++
	m.rows[kind+"/"+id] = schedclient.Record{ID: id, Version: expect + 1, Seq: m.seq, Body: append([]byte(nil), body...)}
	return expect + 1, nil
}

func (m *memRegistry) Get(kind, id string) (schedclient.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[kind+"/"+id]
	if !ok {
		return schedclient.Record{}, schedclient.ErrNotFound
	}
	return r, nil
}

func (m *memRegistry) List(kind string) ([]schedclient.Record, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []schedclient.Record
	for k, r := range m.rows {
		if len(k) > len(kind) && k[:len(kind)+1] == kind+"/" {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *memRegistry) Delete(kind, id string, expect int, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.rows[kind+"/"+id]
	if !ok || cur.Version != expect {
		return schedclient.ErrCASConflict
	}
	delete(m.rows, kind+"/"+id)
	return nil
}

var base = time.Date(2026, 10, 22, 8, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

// ogID 是夹具账号的确定性 id（seedAccount 经发现归并建出）。
const ogID = "opencode-go:aaaa11112222"

// seedAccount 以一次发现归并建出夹具账号（B464：账号只由发现建）并返回其定义。
func seedAccount(t *testing.T, svc *accounts.Service) accounts.Account {
	t.Helper()
	// 发现时刻取 now 而不是固定 base：可探登录要求所在机发现在 DiscoveryFreshFor 内，
	// 固定 base 会让这两个用例在作者写下的时点之后必然过期（时间炸弹，与产品行为无关）。
	if _, err := svc.Reconcile(accounts.Discovery{Machine: "linux-01", At: time.Now(), Homes: []accounts.ScannedHome{
		{Home: "~", HomeAbs: "/root", Logins: []accounts.FoundLogin{
			{Kind: accounts.KindOpencodeGo, Source: "opencode.db", Fingerprint: "sha256:aaaa11112222"}}},
	}}); err != nil {
		t.Fatalf("播种账号: %v", err)
	}
	v, err := svc.Get(ogID)
	if err != nil {
		t.Fatalf("读播种账号: %v", err)
	}
	return v.Account
}

func successAt(started, finished int, used float64) accounts.ProbeOutcome {
	return accounts.ProbeOutcome{
		StartedAt: at(started), FinishedAt: at(finished), Writer: "linux-01",
		Reading: &accounts.Reading{ProbedAt: at(finished), Windows: []accounts.Window{
			{Key: "weekly", UsedPercent: used},
		}},
	}
}

func TestMergeProbeAndMarkSeparateRowsDoNotBumpDefinition(t *testing.T) {
	svc := accounts.New(newMem())
	acct := seedAccount(t, svc)
	if err := svc.RecordProbe(ogID, successAt(0, 1, 30)); err != nil {
		t.Fatalf("写读数: %v", err)
	}
	if err := svc.MarkQuota(ogID, accounts.QuotaMark{At: at(2), Task: "t1", Category: "quota_exhausted"}); err != nil {
		t.Fatalf("写标记: %v", err)
	}
	view, err := svc.Get(ogID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Version != 1 {
		t.Fatalf("机写不得顶高定义行版本：version=%d", view.Version)
	}
	// 人持 version=1 编辑：不因读数/标记写入而冲突。
	edited := acct
	edited.IntervalMinutes = 30
	if v, err := svc.Put(edited, 1); err != nil || v != 2 {
		t.Fatalf("人持旧读到的版本编辑应成功: v=%d err=%v", v, err)
	}
	view, _ = svc.Get(ogID)
	if view.Account.IntervalMinutes != 30 || view.Record.Reading == nil ||
		view.Record.Reading.Windows[0].UsedPercent != 30 || view.Record.QuotaMark == nil {
		t.Fatalf("人写定义抹掉了机写字段或反之: %+v", view)
	}
	if view.State != accounts.StateOut {
		t.Fatalf("有撞额度标记应为无额度，得 %s", view.State)
	}
}

func TestMergeProbeClearsMarkOnlyWhenStartedAfterMark(t *testing.T) {
	svc := accounts.New(newMem())
	seedAccount(t, svc)
	if err := svc.RecordProbe(ogID, successAt(0, 1, 20)); err != nil {
		t.Fatal(err)
	}
	if err := svc.MarkQuota(ogID, accounts.QuotaMark{At: at(10), Task: "t1"}); err != nil {
		t.Fatal(err)
	}
	// 探测发出（9）早于标记（10）、返回（11）晚于标记：旧探测不得抹新标记。
	if err := svc.RecordProbe(ogID, successAt(9, 11, 25)); err != nil {
		t.Fatal(err)
	}
	view, _ := svc.Get(ogID)
	if view.Record.QuotaMark == nil || view.State != accounts.StateOut {
		t.Fatalf("开始早于标记的探测抹掉了标记: %+v", view.Record)
	}
	if view.Record.Reading.Windows[0].UsedPercent != 25 {
		t.Fatalf("读数仍应更新: %+v", view.Record.Reading)
	}
	// 探测失败：不清标记，保留旧读数。
	if err := svc.RecordProbe(ogID, accounts.ProbeOutcome{StartedAt: at(20), FinishedAt: at(21),
		Failure: &accounts.Problem{Category: "network", Message: "断网"}}); err != nil {
		t.Fatal(err)
	}
	view, _ = svc.Get(ogID)
	if view.Record.QuotaMark == nil || view.Record.Reading == nil || view.Record.Reading.Windows[0].UsedPercent != 25 ||
		view.Record.LastError == nil || view.Record.FailureStreak != 1 {
		t.Fatalf("失败应保留旧读数与标记并记失败: %+v", view.Record)
	}
	// 开始晚于标记的成功探测：清标记、清失败。
	if err := svc.RecordProbe(ogID, successAt(30, 31, 40)); err != nil {
		t.Fatal(err)
	}
	view, _ = svc.Get(ogID)
	if view.Record.QuotaMark != nil || view.Record.LastError != nil || view.Record.FailureStreak != 0 {
		t.Fatalf("开始晚于标记的成功探测应清标记与失败: %+v", view.Record)
	}
	if view.State != accounts.StateOK {
		t.Fatalf("40%% 读数应为正常，得 %s", view.State)
	}
}

func TestMergeRefusesOrphanReading(t *testing.T) {
	svc := accounts.New(newMem())
	err := svc.RecordProbe("ghost", successAt(0, 1, 1))
	if !errors.Is(err, accounts.ErrNotFound) {
		t.Fatalf("无定义行不得写读数（孤儿行），得 %v", err)
	}
}

// TestConcurrentHumanProbeMarkerKeepAllFields 是 V3 的承重负载：人写定义、探测器写
// 读数、标记者写标记各 N=50 轮并发；结束后三侧最后一次写都在，人侧零 CAS 冲突。
func TestConcurrentHumanProbeMarkerKeepAllFields(t *testing.T) {
	const n = 50
	svc := accounts.New(newMem())
	seedAccount(t, svc)
	intervals := []int{5, 10, 15, 30, 60}
	var wg sync.WaitGroup
	var humanConflicts int
	var errs []error
	var mu sync.Mutex
	record := func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}
	wg.Add(3)
	go func() { // 人：每轮 GET 取版本 → PUT
		defer wg.Done()
		for i := 0; i < n; i++ {
			view, err := svc.Get(ogID)
			if err != nil {
				record(err)
				return
			}
			a := view.Account
			a.IntervalMinutes = intervals[i%len(intervals)]
			if _, err := svc.Put(a, view.Version); err != nil {
				if errors.Is(err, schedclient.ErrCASConflict) {
					humanConflicts++
				}
				record(err)
				return
			}
		}
	}()
	go func() { // 探测器：开始时刻 2i
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := svc.RecordProbe(ogID, successAt(2*i, 2*i, float64(i))); err != nil {
				record(err)
				return
			}
		}
	}()
	go func() { // 标记者：标记时刻 2i+1
		defer wg.Done()
		for i := 0; i < n; i++ {
			if err := svc.MarkQuota(ogID, accounts.QuotaMark{At: at(2*i + 1), Task: fmt.Sprintf("t%d", i)}); err != nil {
				record(err)
				return
			}
		}
	}()
	wg.Wait()
	if len(errs) > 0 || humanConflicts > 0 {
		t.Fatalf("并发写出错（人侧冲突 %d 次）: %v", humanConflicts, errs)
	}
	view, err := svc.Get(ogID)
	if err != nil {
		t.Fatal(err)
	}
	if view.Version != n+1 || view.Account.IntervalMinutes != intervals[(n-1)%len(intervals)] {
		t.Fatalf("定义行应为人最后一次写: version=%d interval=%d", view.Version, view.Account.IntervalMinutes)
	}
	r := view.Record
	if r.Reading == nil || r.Reading.Windows[0].UsedPercent != float64(n-1) {
		t.Fatalf("读数应为探测器最后一次写: %+v", r.Reading)
	}
	// 最后一个标记（2n-1）晚于任何探测开始（≤2n-2）：必须保留。
	if r.QuotaMark == nil || !r.QuotaMark.At.Equal(at(2*n-1)) {
		t.Fatalf("最后一个标记丢失: %+v", r.QuotaMark)
	}
}

// conflictingRegistry 在内存 registry 外注入确定性 CAS 冲突：读数行的前 n 次 Put 之前先跑
// interleave（另一个写者抢先成功写一次），被包的 Put 因此按真实 CAS 判据落败。
type conflictingRegistry struct {
	*memRegistry
	n          int
	interleave func(round int)
	puts       int
}

func (c *conflictingRegistry) Put(kind, id string, expect int, body []byte, writer string) (int, error) {
	if kind == accounts.KindReading && c.puts < c.n {
		c.puts++
		c.interleave(c.puts)
	}
	return c.memRegistry.Put(kind, id, expect, body, writer)
}

// TestMergeReadingCASConflictRetriesKeepBothSides 是审查第 1 轮 m1：读数/标记并发写的确定性
// 用例。探测写回前 3 次都被标记者抢先（真实版本冲突），第 4 次成功：读数与最后一次标记都在；
// 冲突超出预算（8 次）返回 ErrRetryExhausted，不被静默吞掉（变异 M3a 在这里确定性转红）。
func TestMergeReadingCASConflictRetriesKeepBothSides(t *testing.T) {
	mem := newMem()
	marker := accounts.New(mem)
	reg := &conflictingRegistry{memRegistry: mem, n: 3}
	reg.interleave = func(round int) {
		if err := marker.MarkQuota(ogID, accounts.QuotaMark{At: at(100 + round), Task: fmt.Sprintf("m%d", round)}); err != nil {
			t.Fatalf("标记者写入: %v", err)
		}
	}
	prober := accounts.New(reg)
	seedAccount(t, accounts.New(mem))
	if err := prober.RecordProbe(ogID, successAt(10, 20, 42)); err != nil {
		t.Fatalf("前 3 次冲突后应重读重试成功，得 %v", err)
	}
	v, err := prober.Get(ogID)
	if err != nil {
		t.Fatal(err)
	}
	r := v.Record
	if r.Reading == nil || r.Reading.Windows[0].UsedPercent != 42 {
		t.Fatalf("冲突重试后读数应写入，得 %+v", r.Reading)
	}
	if r.QuotaMark == nil || r.QuotaMark.Task != "m3" {
		t.Fatalf("标记者最后一次标记应保留（探测开始早于标记，不清），得 %+v", r.QuotaMark)
	}
	if reg.puts != 3 {
		t.Fatalf("应恰好注入 3 次冲突，得 %d", reg.puts)
	}

	// 冲突超出预算：返回 ErrRetryExhausted，读数不落、错误不被吞
	reg.n, reg.puts = 1<<30, 0
	err = prober.RecordProbe(ogID, successAt(200, 210, 7))
	if !errors.Is(err, accounts.ErrRetryExhausted) {
		t.Fatalf("连续冲突超出预算应返回 ErrRetryExhausted，得 %v", err)
	}
	if v, _ := prober.Get(ogID); v.Record.Reading.Windows[0].UsedPercent != 42 {
		t.Fatalf("预算耗尽的写不应落库，得 %+v", v.Record.Reading)
	}
}
