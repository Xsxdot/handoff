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
		putOnlineCarrier(t, svc, c)
	}
	if err := svc.PutSquad(scheduling.Squad{Name: "s1", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "c1"}, {Carrier: "c2"}},
	}, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
	return svc, facade
}

func putOnlineCarrier(t *testing.T, svc *scheduling.Service, c scheduling.Carrier) {
	t.Helper()
	if err := svc.PutCarrier(c, 0); err != nil {
		t.Fatalf("登记载体 %s: %v", c.Name, err)
	}
	if _, err := svc.ApplyDetect(c.Name, scheduling.DetectEvidence{Reachable: true}, ""); err != nil {
		t.Fatalf("设置载体 %s online: %v", c.Name, err)
	}
}

func currentRegistryVersion(t *testing.T, facade *ledgerapi.Facade, kind, id string) int {
	t.Helper()
	e, err := facade.Get(kind, id)
	if err != nil {
		t.Fatalf("读 %s/%s 版本: %v", kind, id, err)
	}
	return e.Version
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

// TestAdmitAcrossSquadsRespectsSharedCarrierCap 锁 S1 的跨小队物理上限：两支
// 小队各自拥有同一载体的充足政策位，但载体物理位仍是全局封顶；成功数不能因
// 政策位按小队拆开而超过 carrier/c1 的物理上限。
func TestAdmitAcrossSquadsRespectsSharedCarrierCap(t *testing.T) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "shared-carrier.db"))
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	svc := scheduling.New(facadeRegistry{f: facade})
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "c1", Machine: "m1", CLI: "opencode", Credential: scheduling.CredentialStandalone,
		MaxConcurrency: 2,
	})
	for _, squad := range []string{"s1", "s2"} {
		if err := svc.PutSquad(scheduling.Squad{
			Name: squad, Role: scheduling.RoleExecutor,
			Members: []scheduling.SquadMember{{Carrier: "c1", MaxConcurrency: 8}},
		}, 0); err != nil {
			t.Fatalf("登记小队 %s: %v", squad, err)
		}
	}

	success := 0
	for i := 0; i < 8; i++ {
		squad := "s1"
		if i%2 == 1 {
			squad = "s2"
		}
		_, err := svc.Admit(scheduling.IgnitionRequest{
			Card: fmt.Sprintf("B-shared-%d", i), Squad: squad, Actor: "test",
		})
		if err == nil {
			success++
			continue
		}
		if !errors.Is(err, scheduling.ErrNoSlot) {
			t.Fatalf("小队 %s 第 %d 次准入: %v", squad, i+1, err)
		}
	}
	if success != 2 {
		t.Fatalf("共享载体成功数=%d，期望等于物理上限 2", success)
	}
	if got := runningCount(t, facade, "carrier/c1"); got != 2 {
		t.Fatalf("共享载体物理计数=%d，want 2", got)
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
	if err := svc.PutSquad(scheduling.Squad{
		Name: "coord", Role: scheduling.RoleCoordinator,
		Members: []scheduling.SquadMember{{Carrier: "c1"}},
	}, 0); err != nil {
		t.Fatalf("登记协调者小队: %v", err)
	}
	launchBinding, err := svc.LaunchAdmit("coord")
	if err != nil {
		t.Fatalf("协调者准入: %v", err)
	}
	if err := svc.Release(launchBinding.Squad, launchBinding.Carrier); err != nil {
		t.Fatalf("释放协调者: %v", err)
	}
	output := logs.String()
	assertEvent := func(event, squad string) {
		t.Helper()
		start := strings.Index(output, event+" ")
		if start < 0 {
			t.Fatalf("日志缺少事件 %q: %s", event, output)
		}
		line := output[start:]
		if end := strings.IndexByte(line, '\n'); end >= 0 {
			line = line[:end]
		}
		for _, want := range []string{"squad=" + squad, "carrier=c1", "member_policy=0", "carrier_cap=2"} {
			if !strings.Contains(line, want) {
				t.Fatalf("事件 %q 缺少 %q: %s", event, want, line)
			}
		}
	}
	for _, event := range []string{
		"msg=scheduling.admit.start", "msg=scheduling.admit.success",
		"msg=scheduling.release.start", "msg=scheduling.release.success",
	} {
		assertEvent(event, "s1")
	}
	for _, event := range []string{
		"msg=scheduling.launch_admit.start", "msg=scheduling.launch_admit.success",
	} {
		assertEvent(event, "coord")
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
		if err := svc.PutCarrier(carrier, currentRegistryVersion(t, facade, "carrier", carrier.Name)); err != nil {
			t.Fatalf("更新载体 %s 物理位: %v", carrier.Name, err)
		}
	}
	if err := svc.PutSquad(scheduling.Squad{
		Name: "s1", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{
			{Carrier: "c1", MaxConcurrency: 2},
			{Carrier: "c2", MaxConcurrency: 2},
		},
	}, currentRegistryVersion(t, facade, "squad", "s1")); err != nil {
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

func TestDeleteCarrier(t *testing.T) {
	svc, facade := newCASFixture(t)
	// 准备一个不在任何小队的未入队载体 c3
	c3 := scheduling.Carrier{Name: "c3", Machine: "m3", CLI: "opencode", Credential: scheduling.CredentialStandalone}
	if err := svc.PutCarrier(c3, 0); err != nil {
		t.Fatalf("登记 c3: %v", err)
	}
	c3Ver := currentRegistryVersion(t, facade, "carrier", "c3")

	t.Run("name 为空拒绝", func(t *testing.T) {
		err := svc.DeleteCarrier("", 1)
		if !errors.Is(err, scheduling.ErrInvalid) {
			t.Fatalf("期望 ErrInvalid，实得 %v", err)
		}
	})

	t.Run("仍在小队中拒绝删除且文案带小队名", func(t *testing.T) {
		c1Ver := currentRegistryVersion(t, facade, "carrier", "c1")
		err := svc.DeleteCarrier("c1", c1Ver)
		if !errors.Is(err, scheduling.ErrInvalid) {
			t.Fatalf("期望 ErrInvalid，实得 %v", err)
		}
		if !strings.Contains(err.Error(), "s1") {
			t.Fatalf("期望错误文案带小队名 s1，实得 %v", err)
		}
		// 载体仍在
		if _, err := svc.Carrier("c1"); err != nil {
			t.Fatalf("c1 仍应存在: %v", err)
		}
	})

	t.Run("载体不存在返回 ErrNotFound", func(t *testing.T) {
		err := svc.DeleteCarrier("nonexistent", 1)
		if !errors.Is(err, scheduling.ErrNotFound) {
			t.Fatalf("期望 ErrNotFound，实得 %v", err)
		}
	})

	t.Run("CAS 版本不对拒绝", func(t *testing.T) {
		err := svc.DeleteCarrier("c3", c3Ver+99)
		if !errors.Is(err, schedclient.ErrCASConflict) {
			t.Fatalf("期望 ErrCASConflict，实得 %v", err)
		}
		// 载体仍在
		if _, err := svc.Carrier("c3"); err != nil {
			t.Fatalf("c3 仍应存在: %v", err)
		}
	})

	t.Run("未入队载体成功删除后变为 NotFound", func(t *testing.T) {
		if err := svc.DeleteCarrier("c3", c3Ver); err != nil {
			t.Fatalf("删除未入队载体 c3 失败: %v", err)
		}
		if _, err := svc.Carrier("c3"); !errors.Is(err, scheduling.ErrNotFound) {
			t.Fatalf("删除后读 c3 应返回 ErrNotFound，实得: %v", err)
		}
	})
}

// newFrozenFixture 装配 B233.10 的冻结身份夹具：成员顺序固定为 A→B，
// 两个载体各有独立的物理身份与一格容量，计数通过真实 ledger facade 读写。
func newFrozenFixture(t *testing.T) (*scheduling.Service, *ledgerapi.Facade) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "frozen.db")
	st, err := ledger.Open(path)
	if err != nil {
		t.Fatalf("打开冻结身份账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	svc := scheduling.New(facadeRegistry{f: facade})
	for _, c := range []scheduling.Carrier{
		{Name: "A", Machine: "machine-A", CLI: "cli-A", HomeDir: "/home/A", Model: "model-A",
			Credential: scheduling.CredentialStandalone, MaxConcurrency: 1},
		{Name: "B", Machine: "machine-B", CLI: "cli-B", HomeDir: "/home/B", Model: "model-B",
			Credential: scheduling.CredentialStandalone, MaxConcurrency: 1},
	} {
		putOnlineCarrier(t, svc, c)
	}
	if err := svc.PutSquad(scheduling.Squad{Name: "S", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "A", MaxConcurrency: 1}, {Carrier: "B", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatalf("登记冻结身份小队: %v", err)
	}
	return svc, facade
}

func frozenBinding(carrier, target, executor, home, model string) scheduling.Binding {
	return scheduling.Binding{Squad: "S", Carrier: carrier, Target: target,
		Executor: executor, HomeDir: home, Model: model}
}

// TestB23310SelectDoesNotOccupy 锁住起源侧 Select 的只读语义：选择顺序与载体
// 身份来自真实登记，但任何 sched_running 键都不得因选择而出现或递增。
func TestB23310SelectDoesNotOccupy(t *testing.T) {
	svc, facade := newFrozenFixture(t)
	binding, err := svc.Select(scheduling.IgnitionRequest{Squad: "S", Model: "requested-model", Actor: "test"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if binding.Carrier != "A" || binding.Target != "machine-A" || binding.Executor != "cli-A" || binding.HomeDir != "/home/A" {
		t.Fatalf("Select 未按冻结顺序返回 A 的身份: %+v", binding)
	}
	if binding.Model != "requested-model" {
		t.Fatalf("Select model = %q，want requested-model", binding.Model)
	}
	for _, key := range []string{"carrier/A", "squad/S/A", "carrier/B", "squad/S/B"} {
		if got := runningCount(t, facade, key); got != 0 {
			t.Fatalf("Select 写入计数 %s=%d，want 0", key, got)
		}
	}
}

// TestB23310SelectReportsFullWithoutWriting 在 A/B 均满时锁住只读失败与版本不变，
// 防止 Select 偷用 Admit 或在检查失败后写入半程计数。
func TestB23310SelectReportsFullWithoutWriting(t *testing.T) {
	svc, facade := newFrozenFixture(t)
	for i, carrier := range []string{"A", "B"} {
		if _, err := svc.Admit(scheduling.IgnitionRequest{Card: fmt.Sprintf("full-%d", i), Squad: "S", Actor: "test"}); err != nil {
			t.Fatalf("预置 %s 满员: %v", carrier, err)
		}
	}
	keys := []string{"carrier/A", "squad/S/A", "carrier/B", "squad/S/B"}
	versions := make(map[string]int, len(keys))
	for _, key := range keys {
		versions[key] = currentRegistryVersion(t, facade, "sched_running", key)
	}
	_, err := svc.Select(scheduling.IgnitionRequest{Squad: "S", Actor: "test"})
	if !errors.Is(err, scheduling.ErrNoSlot) {
		t.Fatalf("满员 Select error = %v，want ErrNoSlot", err)
	}
	for _, key := range keys {
		if got := currentRegistryVersion(t, facade, "sched_running", key); got != versions[key] {
			t.Fatalf("满员 Select 改写 %s 版本 %d→%d", key, versions[key], got)
		}
	}
}

func TestB23310SelectSkipsFullMemberBeforeCarrier(t *testing.T) {
	svc, facade := newFrozenFixture(t)
	carrier, err := svc.Carrier("A")
	if err != nil {
		t.Fatalf("读取 carrier A: %v", err)
	}
	carrier.MaxConcurrency = 2
	record, err := facade.Get("carrier", "A")
	if err != nil {
		t.Fatalf("读取 carrier A 版本: %v", err)
	}
	body, err := json.Marshal(carrier)
	if err != nil {
		t.Fatalf("编码 carrier A: %v", err)
	}
	if _, err := facade.Put("carrier", "A", record.Version, body, "test"); err != nil {
		t.Fatalf("更新 carrier A 容量: %v", err)
	}
	if _, err := svc.Admit(scheduling.IgnitionRequest{Card: "member-full", Squad: "S", Actor: "test"}); err != nil {
		t.Fatalf("预置成员满员: %v", err)
	}
	binding, err := svc.Select(scheduling.IgnitionRequest{Squad: "S", Actor: "test"})
	if err != nil {
		t.Fatalf("成员满员后 Select: %v", err)
	}
	if binding.Carrier != "B" {
		t.Fatalf("成员满员但载体有位时不应选择 A: %+v", binding)
	}
}

// TestB23310AdmitFrozenUsesExactCarrier 锁住执行侧只认冻结载体：A 满而 B 空时，
// 对 A 的冻结准入必须失败，不能静默改选 B；对 B 的请求则只占 B 的两级键。
func TestB23310AdmitFrozenUsesExactCarrier(t *testing.T) {
	svc, facade := newFrozenFixture(t)
	if _, err := svc.Admit(scheduling.IgnitionRequest{Card: "preload", Squad: "S", Actor: "test"}); err != nil {
		t.Fatalf("预置 A 满员: %v", err)
	}
	_, err := svc.AdmitFrozen(frozenBinding("A", "machine-A", "cli-A", "/home/A", "model-A"))
	if !errors.Is(err, scheduling.ErrNoSlot) {
		t.Fatalf("A 满员冻结准入 error = %v，want ErrNoSlot", err)
	}
	if got := runningCount(t, facade, "carrier/B"); got != 0 {
		t.Fatalf("A 冻结失败后静默改选 B，carrier/B=%d", got)
	}
	if got := runningCount(t, facade, "squad/S/A"); got != 1 {
		t.Fatalf("A 预置成员计数=%d，want 1", got)
	}
	binding, err := svc.AdmitFrozen(frozenBinding("B", "machine-B", "cli-B", "/home/B", "model-B"))
	if err != nil {
		t.Fatalf("B 冻结准入: %v", err)
	}
	if binding.Carrier != "B" || runningCount(t, facade, "carrier/A") != 1 || runningCount(t, facade, "carrier/B") != 1 ||
		runningCount(t, facade, "squad/S/A") != 1 || runningCount(t, facade, "squad/S/B") != 1 {
		t.Fatalf("冻结准入计数/身份不符: binding=%+v A=%d B=%d", binding,
			runningCount(t, facade, "carrier/A"), runningCount(t, facade, "carrier/B"))
	}
}

// TestB23310AdmitFrozenRejectsPhysicalMismatch 锁住冻结身份与登记物理身份不一致
// 时先拒绝再计数，避免留下半程占用。
func TestB23310AdmitFrozenRejectsPhysicalMismatch(t *testing.T) {
	svc, facade := newFrozenFixture(t)
	_, err := svc.AdmitFrozen(frozenBinding("B", "machine-other", "cli-B", "/home/B", "model-B"))
	if !errors.Is(err, scheduling.ErrRoleMismatch) {
		t.Fatalf("物理身份不匹配 error = %v，want ErrRoleMismatch", err)
	}
	if got := runningCount(t, facade, "carrier/B"); got != 0 {
		t.Fatalf("物理身份拒绝后 carrier/B=%d，want 0", got)
	}
	if got := runningCount(t, facade, "squad/S/B"); got != 0 {
		t.Fatalf("物理身份拒绝后 squad/S/B=%d，want 0", got)
	}
}

// TestB23310AdmitFrozenNormalizesLocalAliases 锁住本机物理身份的同一套归一化：
// 起源冻结结果可能把本机目标写成空串，而载体登记使用 local；二者不能被误判为
// 物理身份不一致，否则真实 startCardStep 会在本机 HTTP 回路被拒绝。
func TestB23310AdmitFrozenNormalizesLocalAliases(t *testing.T) {
	svc, facade := newRowsFixture(t)
	putOnlineCarrier(t, svc, scheduling.Carrier{
		Name: "local-carrier", Machine: "local", CLI: "fake",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 1,
	})
	if err := svc.PutSquad(scheduling.Squad{Name: "local-squad", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "local-carrier", MaxConcurrency: 1}},
	}, 0); err != nil {
		t.Fatalf("登记本机测试小队: %v", err)
	}
	binding, err := svc.AdmitFrozen(scheduling.Binding{
		Squad: "local-squad", Carrier: "local-carrier", Target: "", Executor: "fake",
	})
	if err != nil {
		t.Fatalf("空串与 local 应视为同一本机目标: %v", err)
	}
	if binding.Carrier != "local-carrier" || runningCount(t, facade, "carrier/local-carrier") != 1 ||
		runningCount(t, facade, "squad/local-squad/local-carrier") != 1 {
		t.Fatalf("本机别名准入未按冻结载体占用: binding=%+v carrier=%d member=%d", binding,
			runningCount(t, facade, "carrier/local-carrier"),
			runningCount(t, facade, "squad/local-squad/local-carrier"))
	}
}

// TestB23310AdmitFrozenPreservesEmptyModel 锁住冻结 Model 的空值也属于身份快照：
// Select 得到空 Model 后，即使载体登记随后改成了新模型，AdmitFrozen 成功返回的
// Binding.Model 仍必须保持冻结时的空值，不能用 EffectiveModel 回填当前登记。
func TestB23310AdmitFrozenPreservesEmptyModel(t *testing.T) {
	svc, facade := newFrozenFixture(t)
	carrier, err := svc.Carrier("A")
	if err != nil {
		t.Fatalf("读取 carrier A: %v", err)
	}
	carrier.Model = ""
	record, err := facade.Get("carrier", "A")
	if err != nil {
		t.Fatalf("读取 carrier A 版本: %v", err)
	}
	body, err := json.Marshal(carrier)
	if err != nil {
		t.Fatalf("编码 carrier A: %v", err)
	}
	if _, err := facade.Put("carrier", "A", record.Version, body, "test"); err != nil {
		t.Fatalf("清空 carrier A model: %v", err)
	}

	frozen, err := svc.Select(scheduling.IgnitionRequest{Squad: "S", Actor: "test"})
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if frozen.Carrier != "A" || frozen.Model != "" {
		t.Fatalf("Select 未冻结空 Model: %+v", frozen)
	}

	carrier, err = svc.Carrier("A")
	if err != nil {
		t.Fatalf("重新读取 carrier A: %v", err)
	}
	carrier.Model = "current-model"
	record, err = facade.Get("carrier", "A")
	if err != nil {
		t.Fatalf("读取更新 carrier A 版本: %v", err)
	}
	body, err = json.Marshal(carrier)
	if err != nil {
		t.Fatalf("编码更新 carrier A: %v", err)
	}
	if _, err := facade.Put("carrier", "A", record.Version, body, "test"); err != nil {
		t.Fatalf("更新 carrier A model: %v", err)
	}

	admitted, err := svc.AdmitFrozen(frozen)
	if err != nil {
		t.Fatalf("AdmitFrozen: %v", err)
	}
	if admitted.Model != "" {
		t.Fatalf("AdmitFrozen 不得用当前载体 Model 回填冻结空值: %+v", admitted)
	}
	if err := svc.Release("S", "A"); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

// TestB23310AdmitFrozenEarlyErrorLogsOccupancyKeys 锁住冻结准入的每条早退错误都
// 带上两级占用键，避免容量现场只能靠 carrier 名人工拼接。
func TestB23310AdmitFrozenEarlyErrorLogsOccupancyKeys(t *testing.T) {
	previous := slog.Default()
	var logs bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(previous) })

	svc, _ := newFrozenFixture(t)
	if _, err := svc.AdmitFrozen(frozenBinding("B", "machine-other", "cli-B", "/home/B", "model-B")); err == nil {
		t.Fatal("物理身份不匹配应失败")
	}
	out := logs.String()
	for _, want := range []string{"member_key=squad/S/B", "carrier_key=carrier/B"} {
		if !strings.Contains(out, want) {
			t.Fatalf("冻结准入早退日志缺少 %s: %s", want, out)
		}
	}
}

// TestB23310AdmitFrozenDirectHasNoMemberKey 锁住无小队冻结直派只占 carrier 键，
// 不制造 squad//carrier 伪键。
func TestB23310AdmitFrozenDirectHasNoMemberKey(t *testing.T) {
	svc, facade := newFrozenFixture(t)
	binding, err := svc.AdmitFrozen(scheduling.Binding{Carrier: "B", Target: "machine-B",
		Executor: "cli-B", HomeDir: "/home/B", Model: "model-B"})
	if err != nil {
		t.Fatalf("直派冻结准入: %v", err)
	}
	if binding.Squad != "" || runningCount(t, facade, "carrier/B") != 1 {
		t.Fatalf("直派冻结结果/载体计数不符: %+v count=%d", binding, runningCount(t, facade, "carrier/B"))
	}
	if _, err := facade.Get("sched_running", "squad//B"); !errors.Is(err, ledger.ErrNotFound) {
		t.Fatalf("直派不应创建 squad//B，读结果=%v", err)
	}
}

// casConflictRegistry 把 sched_running 的写全部打成 CAS 冲突，读原样透传，
// 用于构造 AdmitFrozen 的 CAS 重试预算耗尽（瞬态争用），不改变实体登记读取。
type casConflictRegistry struct{ inner schedclient.Registry }

func (r casConflictRegistry) Put(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	if kind == "sched_running" {
		return 0, schedclient.ErrCASConflict
	}
	return r.inner.Put(kind, id, expectVersion, body, actor)
}

func (r casConflictRegistry) Get(kind, id string) (schedclient.Record, error) {
	return r.inner.Get(kind, id)
}

func (r casConflictRegistry) List(kind string) ([]schedclient.Record, error) {
	return r.inner.List(kind)
}

func (r casConflictRegistry) Delete(kind, id string, expectVersion int, actor string) error {
	return r.inner.Delete(kind, id, expectVersion, actor)
}

// TestB23310AdmitFrozenBudgetExhausted 锁住执行侧 CAS 预算耗尽仍以
// ErrRetryExhausted 原样外露：它是瞬态争用，不能被吞成成功，也不能伪装成
// ErrNoSlot（两者的用户处置不同——前者该重试/排队，后者是容量问题）。
func TestB23310AdmitFrozenBudgetExhausted(t *testing.T) {
	_, facade := newFrozenFixture(t)
	svc := scheduling.New(casConflictRegistry{inner: facadeRegistry{f: facade}})
	_, err := svc.AdmitFrozen(frozenBinding("A", "machine-A", "cli-A", "/home/A", "model-A"))
	if !errors.Is(err, scheduling.ErrRetryExhausted) {
		t.Fatalf("AdmitFrozen 预算耗尽 error = %v，want ErrRetryExhausted", err)
	}
}
