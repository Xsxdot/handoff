package scheduling_test

// 编制域准入原子化与序列化边界的域内回归（B156.3 K2 Task A/B）。
//
// facadeRegistry 是组装点适配器 facadeAsRegistry 的测试同构：把账本门面翻译成
// schedclient.Registry 端口。放在这里是竖切的一部分——计数与队列必须真实穿过
// 账本落盘；ErrNotFound 的哨兵翻译承重（漏了它冷启动计数路径整体失效，
// 见 plan §0 基线探针教训）。

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
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
		Members: []scheduling.SquadMember{{Carrier: "c1"}, {Carrier: "c2"}},
	}, 0); err != nil {
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
	for key, want := range map[string]int{"squad/s1/c1": 2, "squad/s1/c2": 2, "carrier/c1": 2, "carrier/c2": 2} {
		if got := runningCount(t, facade, key); got != want {
			t.Fatalf("计数 %s=%d，期望 %d", key, got, want)
		}
	}
}

// TestSquadMemberWireShapeAndLegacyRead 可执行冻结成员政策位的 JSON 形状，并锁住
// 存量 members:["carrier"] 的无损迁移：旧队级上限不进入新模型，旧成员政策按不限读入。
func TestSquadMemberWireShapeAndLegacyRead(t *testing.T) {
	q := scheduling.Squad{
		Name: "sq", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "c1", MaxConcurrency: 2}, {Carrier: "c2"}},
	}
	wire, err := json.Marshal(q)
	if err != nil {
		t.Fatalf("成员政策 JSON 编码: %v", err)
	}
	if got, want := string(wire), `{"name":"sq","role":"executor","members":[{"carrier":"c1","max_concurrency":2},{"carrier":"c2"}]}`; got != want {
		t.Fatalf("成员政策 wire 不符:\n got=%s\nwant=%s", got, want)
	}

	var legacy scheduling.Squad
	if err := json.Unmarshal([]byte(`{"name":"legacy","role":"executor","members":["c1","c2"],"max_concurrency":9}`), &legacy); err != nil {
		t.Fatalf("存量小队读取: %v", err)
	}
	if len(legacy.Members) != 2 || legacy.Members[0].Carrier != "c1" || legacy.Members[1].Carrier != "c2" ||
		legacy.Members[0].MaxConcurrency != 0 || legacy.Members[1].MaxConcurrency != 0 {
		t.Fatalf("存量成员未规范化为不限政策: %+v", legacy.Members)
	}
}

// TestAdmissionAndReleaseLogsCarryCapacityContext 锁公开准入/释放入口的可观测性：
// 满员或计数异常时，排障必须能区分小队政策键与载体物理键；本测试只观察 slog
// 默认出口，不把日志格式当作调度规则的第二份实现。
func TestAdmissionAndReleaseLogsCarryCapacityContext(t *testing.T) {
	svc, _ := newCASFixture(t)
	var logs bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	binding, err := svc.Admit(scheduling.IgnitionRequest{
		Card: "B-log", Squad: "s1", Target: "target-override",
		Executor: "executor-override", Model: "model-override", Actor: "test",
	})
	if err != nil {
		t.Fatalf("准入: %v", err)
	}
	if binding.Carrier != "c1" {
		t.Fatalf("登记顺序应先选 c1，得 %+v", binding)
	}
	if err := svc.Release(binding.Squad, binding.Carrier); err != nil {
		t.Fatalf("释放: %v", err)
	}
	output := logs.String()
	for _, want := range []string{"squad=s1", "carrier=c1", "member_policy=0", "carrier_cap=2"} {
		if !strings.Contains(output, want) {
			t.Fatalf("日志缺少 %q: %s", want, output)
		}
	}
}

// TestMemberPolicyChoosesLaterMemberAndReleaseIsIdempotent 锁生产 Admit 的成员级
// 选择：前成员政策位满时必须继续后成员；两级计数清零后重复 Release 不得变负，
// registry 中只能出现 squad/<队>/<载体> 与 carrier/<载体> 两类键。
func TestMemberPolicyChoosesLaterMemberAndReleaseIsIdempotent(t *testing.T) {
	svc, facade := newCASFixture(t)
	for _, carrier := range []scheduling.Carrier{
		{Name: "c1", Machine: "m1", CLI: "opencode", Credential: scheduling.CredentialStandalone, MaxConcurrency: 1},
		{Name: "c2", Machine: "m2", CLI: "opencode", Credential: scheduling.CredentialStandalone, MaxConcurrency: 3},
	} {
		if err := svc.PutCarrier(carrier, 1); err != nil {
			t.Fatalf("更新载体 %s 物理位: %v", carrier.Name, err)
		}
	}
	if err := svc.PutSquad(scheduling.Squad{
		Name: "s1", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{
			{Carrier: "c1", MaxConcurrency: 2},
			{Carrier: "c2", MaxConcurrency: 2},
		},
	}, 1); err != nil {
		t.Fatalf("更新成员政策: %v", err)
	}
	var bindings []scheduling.Binding
	for i := 0; i < 3; i++ {
		binding, err := svc.Admit(scheduling.IgnitionRequest{Card: fmt.Sprintf("B-member-%d", i), Squad: "s1", Actor: "test"})
		if err != nil {
			t.Fatalf("第 %d 次准入: %v", i+1, err)
		}
		bindings = append(bindings, binding)
	}
	if got := []string{bindings[0].Carrier, bindings[1].Carrier, bindings[2].Carrier}; !reflect.DeepEqual(got, []string{"c1", "c2", "c2"}) {
		t.Fatalf("成员政策/登记顺序选择不符: %v", got)
	}
	if _, err := svc.Admit(scheduling.IgnitionRequest{Card: "B-member-full", Squad: "s1", Actor: "test"}); !errors.Is(err, scheduling.ErrNoSlot) {
		t.Fatalf("两成员任一级满应 ErrNoSlot，得 %v", err)
	}
	for _, binding := range bindings {
		if err := svc.Release(binding.Squad, binding.Carrier); err != nil {
			t.Fatalf("释放 %s: %v", binding.Carrier, err)
		}
	}
	for _, carrier := range []string{"c1", "c2"} {
		if err := svc.Release("s1", carrier); err != nil {
			t.Fatalf("重复释放 %s: %v", carrier, err)
		}
	}
	for _, key := range []string{"squad/s1/c1", "squad/s1/c2", "carrier/c1", "carrier/c2"} {
		if got := runningCount(t, facade, key); got != 0 {
			t.Fatalf("释放后计数 %s=%d，want 0", key, got)
		}
	}
	rows, err := facade.List("sched_running")
	if err != nil {
		t.Fatalf("列运行计数: %v", err)
	}
	if len(rows) != 4 {
		t.Fatalf("运行计数键数量=%d，want 4: %+v", len(rows), rows)
	}
	for _, row := range rows {
		if strings.HasPrefix(row.ID, "squad/s1/") || strings.HasPrefix(row.ID, "carrier/") {
			continue
		}
		t.Fatalf("出现未声明运行计数键: %s", row.ID)
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
