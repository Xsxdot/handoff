package scheduling_test

// 编制域准入原子化与序列化边界的域内回归（B156.3 K2 Task A/B）。
//
// facadeRegistry 是组装点适配器 facadeAsRegistry 的测试同构：把账本门面翻译成
// schedclient.Registry 端口。放在这里是竖切的一部分——计数与队列必须真实穿过
// 账本落盘；ErrNotFound 的哨兵翻译承重（漏了它冷启动计数路径整体失效，
// 见 plan §0 基线探针教训）。

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

type facadeRegistry struct{ f *ledgerapi.Facade }

func (a facadeRegistry) Put(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	v, err := a.f.Put(kind, id, expectVersion, body, actor)
	return v, translateRegistryErrForTest(err)
}

func (a facadeRegistry) Get(kind, id string) (schedclient.Record, error) {
	e, err := a.f.Get(kind, id)
	if err != nil {
		return schedclient.Record{}, translateRegistryErrForTest(err)
	}
	return schedclient.Record{ID: e.ID, Version: e.Version, Seq: e.Seq, Body: e.Body}, nil
}

func (a facadeRegistry) List(kind string) ([]schedclient.Record, error) {
	rows, err := a.f.List(kind)
	if err != nil {
		return nil, err
	}
	out := make([]schedclient.Record, 0, len(rows))
	for _, e := range rows {
		out = append(out, schedclient.Record{ID: e.ID, Version: e.Version, Seq: e.Seq, Body: e.Body})
	}
	return out, nil
}

func (a facadeRegistry) Delete(kind, id string, expectVersion int, actor string) error {
	return translateRegistryErrForTest(a.f.Delete(kind, id, expectVersion, actor))
}

// translateRegistryErrForTest 是组装点 translateRegistryErr 的测试同构：
// 账本同义错误 → schedclient 哨兵。ErrCASConflict 的翻译承重——漏了它，
// CAS 冲突变成硬失败，重试路径整体失效（本卡实测：并发判据因此 2~3/4）。
func translateRegistryErrForTest(err error) error {
	switch {
	case errors.Is(err, ledger.ErrNotFound):
		return schedclient.ErrNotFound
	case errors.Is(err, ledger.ErrCASConflict):
		return schedclient.ErrCASConflict
	default:
		return err
	}
}

// newCASFixture 开临时账本并登记双载体小队：载体物理位各 2（总容量 4），
// 小队政策位 10 刻意不构成约束——成功数恰 4 只能由载体全局物理位解释，
// 同时证明成员轮转真的发生了（c1 满后后续准入落到 c2）。
func newCASFixture(t *testing.T) (*scheduling.Service, *ledgerapi.Facade) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "cas.db"))
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	svc := scheduling.New(facadeRegistry{f: facade})
	for _, c := range []scheduling.Carrier{
		{Name: "c1", Machine: "m1", CLI: "opencode", Credential: scheduling.CredentialStandalone, MaxConcurrency: 2},
		{Name: "c2", Machine: "m2", CLI: "opencode", Credential: scheduling.CredentialStandalone, MaxConcurrency: 2},
	} {
		if err := svc.PutCarrier(c, 0); err != nil {
			t.Fatalf("登记载体 %s: %v", c.Name, err)
		}
	}
	if err := svc.PutSquad(scheduling.Squad{Name: "s1", Role: scheduling.RoleExecutor,
		Members: []string{"c1", "c2"}, MaxConcurrency: 10}, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
	return svc, facade
}

// runningCount 直读一条 sched_running 计数（经真实门面，缺失=0）。
func runningCount(t *testing.T, facade *ledgerapi.Facade, key string) int {
	t.Helper()
	e, err := facade.Get("sched_running", key)
	if err != nil {
		if errors.Is(err, ledger.ErrNotFound) {
			return 0
		}
		t.Fatalf("读计数 %s: %v", key, err)
	}
	var body struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(e.Body, &body); err != nil {
		t.Fatalf("解码计数 %s: %v", key, err)
	}
	return body.Count
}

// TestConcurrentAdmitRespectsTwoLevelCaps 并发 N 路打同一小队（总容量 M<N）：
// 成功数恰为 M、四条计数键逐一相等（比抽查强：每条都点数）。
//
// 重试预算注记（岔口三附加约束）：域内单次计数变更重试上限 8 次；N=16 高争用下
// 可能出现 ErrRetryExhausted（预算形状，非语义错），测试外侧只对它补吸收重试
// （上限 50 轮），其他失败一律判负。翻红时先查 ErrRetryExhausted 再查语义。
//
// 变异复验程序（必须执行并把两次输出落台账）：
//  1. 临时删掉 acquire 内两行 `if ... >= ...MaxConcurrency { return Binding{}, errMemberFull }`；
//  2. 跑本测试 → 必翻红（成功数冲向 N≠M；基线探针同形状实测 successes=6>4）；
//  3. 恢复代码 → 复跑转绿。
func TestConcurrentAdmitRespectsTwoLevelCaps(t *testing.T) {
	svc, facade := newCASFixture(t)
	const n, m = 16, 4
	var success atomic.Int64
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := scheduling.IgnitionRequest{Card: "B1", Squad: "s1",
				Node: "impl", Actor: "test"}
			for try := 0; try < 50; try++ {
				_, err := svc.Admit(req)
				switch {
				case err == nil:
					success.Add(1)
					return
				case errors.Is(err, scheduling.ErrRetryExhausted):
					continue // 预算不足：补吸收，不是语义结果
				default:
					return // ErrNoSlot 等：语义终态，如实计败
				}
			}
			t.Errorf("admit-%d 连续 50 轮预算耗尽，争用参数失真", i)
		}(i)
	}
	close(start)
	wg.Wait()
	if got := success.Load(); got != m {
		t.Fatalf("成功数=%d，期望恰=%d（少=预算或成员轮转缺陷；多=上界执法失效）", got, m)
	}
	// 计数终值逐一相等，不用抽查：
	for key, want := range map[string]int{"squad/s1": 4, "carrier/c1": 2, "carrier/c2": 2} {
		if got := runningCount(t, facade, key); got != want {
			t.Fatalf("计数 %s=%d，期望 %d", key, got, want)
		}
	}
}

// TestIgnitionRequestRoundtripThroughRegistry 锁序列化边界（breakdown K2 验收
// 第 4 组）：IgnitionRequest 经真实 Enqueue→registry JSON→PopReady 后字段逐一
// 相等，且「字段缺失」与「值为零」可分辨——Ready=false 必须以显式键存活
// （json tag 无 omitempty），Priority="" 以 omitempty 缺席且解码回零值。
// 手写投影共两处，逐一在此点名并锁住：
//
//	① Enqueue 侧 json.Marshal(queuedEntry{Req,Seq})（scheduling.go Enqueue）；
//	② PopReady 侧 json.Unmarshal(rec.Body, &queuedEntry)（scheduling.go PopReady）。
//
// queueID 主键公式（Card 或 Card|Node）在测试里复制了一份，属③号副本，
// 改公式必 here 翻红。
func TestIgnitionRequestRoundtripThroughRegistry(t *testing.T) {
	_, facade := newCASFixture(t)
	svc := scheduling.New(facadeRegistry{f: facade})
	cases := []struct {
		name string
		req  scheduling.IgnitionRequest
	}{
		{"零值分辨", scheduling.IgnitionRequest{Card: "B10", Squad: "s1",
			Node: "impl", Priority: "", Ready: false, Actor: "a"}},
		{"全字段", scheduling.IgnitionRequest{Card: "B11", Squad: "s1",
			Node: "impl", Target: "m9", Executor: "grok", Model: "fast",
			Priority: "高", Ready: true, Actor: "cli:u@h"}},
		{"卡级排队无节点", scheduling.IgnitionRequest{Card: "B12", Squad: "s1",
			Priority: "低", Ready: true, Actor: "a"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := tc.req.Card
			if tc.req.Node != "" {
				id = tc.req.Card + "|" + tc.req.Node
			}
			if _, err := svc.Enqueue(tc.req, scheduling.KindIgnitionQueue); err != nil {
				t.Fatalf("入队: %v", err)
			}
			raw, err := facade.Get(scheduling.KindIgnitionQueue, id)
			if err != nil {
				t.Fatalf("直读队列行: %v", err)
			}
			body := string(raw.Body)
			wantReadyKey := `"ready":` + map[bool]string{true: "true", false: "false"}[tc.req.Ready]
			if !strings.Contains(body, wantReadyKey) {
				t.Fatalf("ready 键未以显式值存活：%q 中找不到 %s", body, wantReadyKey)
			}
			if tc.req.Priority == "" && strings.Contains(body, `"priority"`) {
				t.Fatalf("Priority 空串应 omitempty 缺席，实际出现该键：%s", body)
			}
			got, ok, err := svc.PopReady(scheduling.KindIgnitionQueue)
			if err != nil || !ok {
				t.Fatalf("出队 ok=%v err=%v", ok, err)
			}
			if got != tc.req {
				t.Fatalf("roundtrip 不等：\n got=%+v\nwant=%+v", got, tc.req)
			}
		})
	}
	// 空队列出队：(zero,false,nil)，不出错不假装有货。
	if _, ok, err := svc.PopReady(scheduling.KindIgnitionQueue); ok || err != nil {
		t.Fatalf("空队列应 (false,nil)，实得 ok=%v err=%v", ok, err)
	}
}
