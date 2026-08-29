package scheduling_test

// 编制域读面回归（B156.3 K3 Task A）：SquadRows/CarrierRows/QueueSnapshot 是
// gateway GET 端点的数据源（缝①编制域入站 api 门面的读半边）。
//
// rowsRegistry 是组装点适配器 facadeAsRegistry 的测试同构（server.go:2266）：
// 把账本门面翻译成 schedclient.Registry 端口，ErrNotFound 哨兵翻译承重
// （漏翻译 = 冷启动计数/读面整体失效）。放在这里是竖切的一部分——读面必须
// 真实穿过账本落盘。命名刻意区别于 K2 已并入的 facadeRegistry/newCASFixture，
// 两卡并行合并时不撞符号。
//
// 排序唯一性：QueueSnapshot 的队内序不自带比较器——位次全部委托既有
// position()（Enqueue 返回值的同一权威），T-A2 用同一批入队数据同时断言
// 快照位次与逐个 PopReady 出队次序一致，两半漂移即翻红。

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
	ledgerapi "github.com/Xsxdot/handoff/internal/ledger/api"
	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

type rowsRegistry struct{ f *ledgerapi.Facade }

func (a rowsRegistry) Put(kind, id string, expectVersion int, body []byte, actor string) (int, error) {
	return a.f.Put(kind, id, expectVersion, body, actor)
}

func (a rowsRegistry) Get(kind, id string) (schedclient.Record, error) {
	e, err := a.f.Get(kind, id)
	if err != nil {
		if errors.Is(err, ledger.ErrNotFound) {
			return schedclient.Record{}, schedclient.ErrNotFound
		}
		return schedclient.Record{}, err
	}
	return schedclient.Record{ID: e.ID, Version: e.Version, Seq: e.Seq, Body: e.Body}, nil
}

func (a rowsRegistry) List(kind string) ([]schedclient.Record, error) {
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

func (a rowsRegistry) Delete(kind, id string, expectVersion int, actor string) error {
	return a.f.Delete(kind, id, expectVersion, actor)
}

// newRowsFixture 开临时账本并返回经真实适配器的编制域服务。
func newRowsFixture(t *testing.T) (*scheduling.Service, *ledgerapi.Facade) {
	t.Helper()
	st, err := ledger.Open(filepath.Join(t.TempDir(), "rows.db"))
	if err != nil {
		t.Fatalf("打开临时账本: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	facade := ledgerapi.New(st)
	return scheduling.New(rowsRegistry{f: facade}), facade
}

func mustBody(t *testing.T, f *ledgerapi.Facade, kind, id string) []byte {
	t.Helper()
	e, err := f.Get(kind, id)
	if err != nil {
		t.Fatalf("直读 %s/%s: %v", kind, id, err)
	}
	return e.Body
}

// TestRowsCarryVersionsForCASLock 缝①读半边：登记两次（新建+更新）后行版本
// 单调，且 omitempty 边界按线约定存活——max_concurrency=0 键缺席、healthy
// 恒显式 true（PutCarrier 的防饿死翻真）。这是「字段缺失 vs 值为零」分辨的
// registry 侧半边（wire 侧半边在 Task C 的 TC2）。
func TestRowsCarryVersionsForCASLock(t *testing.T) {
	svc, facade := newRowsFixture(t)
	c1 := scheduling.Carrier{Name: "c1", Machine: "m1", CLI: "opencode",
		Credential: scheduling.CredentialStandalone, MaxConcurrency: 2}
	if err := svc.PutCarrier(c1, 0); err != nil {
		t.Fatalf("登记载体: %v", err)
	}
	c1.HomeDir = "/home/c1"
	if err := svc.PutCarrier(c1, 1); err != nil {
		t.Fatalf("更新载体: %v", err)
	}
	rows, err := svc.CarrierRows()
	if err != nil {
		t.Fatalf("列载体行: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("载体行数 %d ≠ 1", len(rows))
	}
	r := rows[0]
	if r.Version != 2 || r.Carrier.HomeDir != "/home/c1" ||
		r.Carrier.MaxConcurrency != 2 || !r.Carrier.Healthy {
		t.Fatalf("载体行不符: %+v", r)
	}
	raw := string(mustBody(t, facade, "carrier", "c1"))
	if !strings.Contains(raw, `"healthy":true`) {
		t.Fatalf("healthy 键应显式出现：%s", raw)
	}
	if !strings.Contains(raw, `"max_concurrency":2`) {
		t.Fatalf("max_concurrency=2 应显式出现：%s", raw)
	}
	z := scheduling.Carrier{Name: "z", Machine: "m", CLI: "cli",
		Credential: scheduling.CredentialMainHomeSync}
	if err := svc.PutCarrier(z, 0); err != nil {
		t.Fatalf("登记零上限载体: %v", err)
	}
	rawZ := string(mustBody(t, facade, "carrier", "z"))
	if strings.Contains(rawZ, `"max_concurrency"`) {
		t.Fatalf("max_concurrency=0 应 omitempty 缺席：%s", rawZ)
	}
	if err := svc.PutSquad(scheduling.Squad{Name: "sq", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "c1"}}}, 0); err != nil {
		t.Fatalf("登记小队: %v", err)
	}
	sqRows, err := svc.SquadRows()
	if err != nil {
		t.Fatalf("列小队行: %v", err)
	}
	if len(sqRows) != 1 || sqRows[0].Version != 1 ||
		sqRows[0].Squad.Role != scheduling.RoleExecutor {
		t.Fatalf("小队行不符: %+v", sqRows)
	}
}

// TestSquadRowsLegacyMembersRoundtripThroughRegistry 穿真实 registry JSON 验收存量
// members:["carrier"] 的兼容读，以及成功写回后只保留新成员对象；旧队级
// max_concurrency 不得复制到任何成员或重新出现。空 members 仍是合法空队。
func TestSquadRowsLegacyMembersRoundtripThroughRegistry(t *testing.T) {
	svc, facade := newRowsFixture(t)
	for _, name := range []string{"c1", "c2"} {
		if err := svc.PutCarrier(scheduling.Carrier{
			Name: name, Machine: "m1", CLI: "opencode",
			Credential: scheduling.CredentialStandalone,
		}, 0); err != nil {
			t.Fatalf("登记载体 %s: %v", name, err)
		}
	}
	if _, err := facade.Put("squad", "legacy", 0, []byte(`{"name":"legacy","role":"executor","members":["c1","c2"],"max_concurrency":9}`), "test"); err != nil {
		t.Fatalf("写入存量小队: %v", err)
	}
	if _, err := facade.Put("squad", "empty", 0, []byte(`{"name":"empty","role":"executor","members":[]}`), "test"); err != nil {
		t.Fatalf("写入空小队: %v", err)
	}

	rows, err := svc.SquadRows()
	if err != nil {
		t.Fatalf("读取小队行: %v", err)
	}
	var legacy *scheduling.Squad
	for _, row := range rows {
		if row.Squad.Name == "legacy" {
			copy := row.Squad
			legacy = &copy
		}
	}
	if legacy == nil || len(legacy.Members) != 2 || legacy.Members[0] != (scheduling.SquadMember{Carrier: "c1"}) || legacy.Members[1] != (scheduling.SquadMember{Carrier: "c2"}) {
		t.Fatalf("存量成员未规范化为不限对象: %+v", legacy)
	}
	if err := svc.PutSquad(*legacy, 1); err != nil {
		t.Fatalf("写回新成员形状: %v", err)
	}
	raw, err := facade.Get("squad", "legacy")
	if err != nil {
		t.Fatalf("读取写回小队: %v", err)
	}
	if strings.Contains(string(raw.Body), `"max_concurrency"`) || strings.Contains(string(raw.Body), `"members":["`) {
		t.Fatalf("写回仍含旧队级/字符串成员形状: %s", raw.Body)
	}
	var empty scheduling.Squad
	if err := json.Unmarshal([]byte(`{"name":"empty","role":"executor","members":[]}`), &empty); err != nil {
		t.Fatalf("空队解码: %v", err)
	}
	if empty.Members == nil || len(empty.Members) != 0 {
		t.Fatalf("空成员数组必须保持合法空切片: %+v", empty.Members)
	}
}

// TestQueueSnapshotMatchesDrainOrder 缝①：快照位次 = 清队循环的真实次序。
// launch_queue 整队排 ignition_queue 之前；launch 队内高优先级先于低优先级；
// ignition 队内 Ready=true 先于 Ready=false（优先级只在同级比）。
// 随后按快照位次逐个 PopReady，出队序列必须一一对应——位次权威是同一个
// position()，出现第二份排序语义时本测试翻红。
func TestQueueSnapshotMatchesDrainOrder(t *testing.T) {
	svc, _ := newRowsFixture(t)
	seeds := []struct {
		kind string
		req  scheduling.IgnitionRequest
	}{
		{scheduling.KindIgnitionQueue, scheduling.IgnitionRequest{Card: "B1",
			Squad: "s", Node: "impl", Priority: "高", Ready: false, Actor: "a"}},
		{scheduling.KindLaunchQueue, scheduling.IgnitionRequest{Card: "B2",
			Squad: "coord", Priority: "低", Ready: false, Actor: "b"}},
		{scheduling.KindIgnitionQueue, scheduling.IgnitionRequest{Card: "B3",
			Squad: "s", Priority: "低", Ready: true, Actor: "c"}},
		{scheduling.KindLaunchQueue, scheduling.IgnitionRequest{Card: "B4",
			Squad: "coord", Priority: "高", Ready: false, Actor: "d"}},
	}
	for i, s := range seeds {
		if _, err := svc.Enqueue(s.req, s.kind); err != nil {
			t.Fatalf("入队 #%d: %v", i, err)
		}
	}
	snap, err := svc.QueueSnapshot()
	if err != nil {
		t.Fatalf("快照: %v", err)
	}
	type wantRow struct {
		kind, card string
		pos        int
		ready      bool
	}
	wantOrder := []wantRow{
		{scheduling.KindLaunchQueue, "B4", 1, false}, // 高优先级先出
		{scheduling.KindLaunchQueue, "B2", 2, false},
		{scheduling.KindIgnitionQueue, "B3", 3, true}, // 就绪快照优先于优先级词表
		{scheduling.KindIgnitionQueue, "B1", 4, false},
	}
	if len(snap) != len(wantOrder) {
		t.Fatalf("快照行数 %d ≠ %d: %+v", len(snap), len(wantOrder), snap)
	}
	for i, w := range wantOrder {
		got := snap[i]
		if got.Kind != w.kind || got.Req.Card != w.card ||
			got.Position != w.pos || got.Req.Ready != w.ready {
			t.Fatalf("第 %d 位 = %s/%s pos=%d ready=%v，want %s/%s pos=%d ready=%v",
				i+1, got.Kind, got.Req.Card, got.Position, got.Req.Ready,
				w.kind, w.card, w.pos, w.ready)
		}
		if got.ID == "" || got.Seq != 0 {
			t.Fatalf("元数据异常（首次入队序应为 0）: %+v", got)
		}
	}
	for _, w := range wantOrder {
		req, ok, err := svc.PopReady(w.kind)
		if err != nil || !ok {
			t.Fatalf("出队 %s: ok=%v err=%v", w.card, ok, err)
		}
		if req.Card != w.card {
			t.Fatalf("出队顺序漂移：want %s got %s", w.card, req.Card)
		}
	}
	if _, ok, err := svc.PopReady(scheduling.KindIgnitionQueue); ok || err != nil {
		t.Fatalf("清空后出队应 (false,nil)，得 ok=%v err=%v", ok, err)
	}
}

// TestPutValidationWrapsErrInvalid 锁 ErrInvalid 的产生臂（修复轮 Major-3）：
// PutCarrier/PutSquad 的四处校验 return 必须以 %w 包 ErrInvalid——此前是裸
// fmt.Errorf，空白名/词表外取值上浮到网关 default→500（用户可修错误落 5xx）。
// 网关据此把「用户填错（400）」与「registry 故障（500）」分流。
func TestPutValidationWrapsErrInvalid(t *testing.T) {
	svc, _ := newRowsFixture(t)
	carrierIncomplete := svc.PutCarrier(scheduling.Carrier{Name: "", Machine: "m",
		CLI: "opencode", Credential: scheduling.CredentialStandalone}, 0)
	if !errors.Is(carrierIncomplete, scheduling.ErrInvalid) {
		t.Fatalf("载体登记不完整应包 ErrInvalid，得 %v", carrierIncomplete)
	}
	credentialOut := svc.PutCarrier(scheduling.Carrier{Name: "c", Machine: "m",
		CLI: "opencode", Credential: "boss"}, 0)
	if !errors.Is(credentialOut, scheduling.ErrInvalid) {
		t.Fatalf("凭据来源词表外应包 ErrInvalid，得 %v", credentialOut)
	}
	squadBlank := svc.PutSquad(scheduling.Squad{Name: "  ", Role: scheduling.RoleExecutor}, 0)
	if !errors.Is(squadBlank, scheduling.ErrInvalid) {
		t.Fatalf("小队名空白应包 ErrInvalid，得 %v", squadBlank)
	}
	roleOut := svc.PutSquad(scheduling.Squad{Name: "sq", Role: "boss"}, 0)
	if !errors.Is(roleOut, scheduling.ErrInvalid) {
		t.Fatalf("角色词表外应包 ErrInvalid，得 %v", roleOut)
	}
	// 成员引用缺失仍以 ErrNotFound 链上浮（Major-1 判据的域侧半臂）：
	// 网关 handleSquadPut 靠 errors.Is(ErrNotFound) 把它分到 400。
	memberMissing := svc.PutSquad(scheduling.Squad{Name: "sq", Role: scheduling.RoleExecutor,
		Members: []scheduling.SquadMember{{Carrier: "ghost"}}}, 0)
	if !errors.Is(memberMissing, scheduling.ErrNotFound) {
		t.Fatalf("成员引用缺失应包 ErrNotFound，得 %v", memberMissing)
	}
}
