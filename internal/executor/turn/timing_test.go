package turn

import (
	"testing"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// fakeClock 是注入给 Segmenter 的可控时钟。
//
// 时钟必须可注入，不是测试便利：判据依赖真实时间的测试在 CI 负载下会偶发
// 翻红，而偶发红会被当成噪音忽略，于是这条判据实际上失效了。
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time      { return c.t }
func (c *fakeClock) add(d time.Duration) { c.t = c.t.Add(d) }

func newTestClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)}
}

// pick 从条目集合里挑出某一 Kind 的全部条目，保持顺序。
func pick(es []proto.TimingEntry, k proto.TimingKind) []proto.TimingEntry {
	var out []proto.TimingEntry
	for _, e := range es {
		if e.Kind == k {
			out = append(out, e)
		}
	}
	return out
}

// runSeq 把一串操作喂给 Segmenter，收集全部产出的条目。
type seqStep func(s *Segmenter, c *fakeClock) []proto.TimingEntry

func runSeq(s *Segmenter, c *fakeClock, steps ...seqStep) []proto.TimingEntry {
	var all []proto.TimingEntry
	for _, st := range steps {
		all = append(all, st(s, c)...)
	}
	return all
}

func TestSegmenterSimpleTurn(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)

	all := runSeq(s, c,
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry { return s.BeginTurn(1) },
		// 模型想了 2s 才发出工具调用
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(2 * time.Second)
			return s.ToolStart("t1", "Bash", "go test ./...")
		},
		// 工具跑了 5s
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(5 * time.Second)
			_, es := s.ToolEnd("t1")
			return es
		},
		// 模型又想了 3s 收尾
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(3 * time.Second)
			return s.EndTurn()
		},
	)

	api := pick(all, proto.TimingKindAPI)
	if len(api) != 2 {
		t.Fatalf("应产出 2 个模型段，实得 %d：%+v", len(api), api)
	}
	if api[0].Key != "api/1/0" || api[0].DurMS != 2000 {
		t.Errorf("首个模型段应为 api/1/0 @2000ms，实得 %s @%d", api[0].Key, api[0].DurMS)
	}
	if api[1].Key != "api/1/1" || api[1].DurMS != 3000 {
		t.Errorf("次个模型段应为 api/1/1 @3000ms，实得 %s @%d", api[1].Key, api[1].DurMS)
	}

	tools := pick(all, proto.TimingKindTool)
	if len(tools) != 1 {
		t.Fatalf("应产出 1 个工具段，实得 %d", len(tools))
	}
	if tools[0].Key != "tool/1/t1" || tools[0].DurMS != 5000 || tools[0].OffsetMS != 2000 {
		t.Errorf("工具段应为 tool/1/t1 dur=5000 offset=2000，实得 %s dur=%d offset=%d",
			tools[0].Key, tools[0].DurMS, tools[0].OffsetMS)
	}
	if tools[0].Label != "Bash" || tools[0].Detail != "go test ./..." {
		t.Errorf("工具段应带 Label/Detail，实得 %q / %q", tools[0].Label, tools[0].Detail)
	}

	turns := pick(all, proto.TimingKindTurn)
	last := turns[len(turns)-1]
	if last.Key != "turn/1" || last.DurMS != 10000 {
		t.Errorf("末条回合条目应为 turn/1 @10000ms，实得 %s @%d", last.Key, last.DurMS)
	}
	// 三分法的不变式：api + tool 之和 == 回合墙钟（本例无空档、无并发）
	if api[0].DurMS+api[1].DurMS+tools[0].DurMS != last.DurMS {
		t.Error("无空档无并发时，模型段 + 工具段之和应恰好等于回合墙钟")
	}
}

func TestSegmenterConcurrentTools(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)
	_ = s.BeginTurn(1)
	c.add(1 * time.Second)

	// claude 在一条 assistant 消息里并行发两个 tool_use
	_ = s.ToolStart("a", "Bash", "sleep 4")
	_ = s.ToolStart("b", "Read", "x.go")
	c.add(2 * time.Second)
	_, esB := s.ToolEnd("b") // b 先回
	c.add(2 * time.Second)
	_, esA := s.ToolEnd("a") // a 后回
	end := s.EndTurn()

	tools := append(pick(esB, proto.TimingKindTool), pick(esA, proto.TimingKindTool)...)
	if len(tools) != 2 {
		t.Fatalf("应产出 2 个工具段，实得 %d", len(tools))
	}
	var sum int64
	for _, e := range tools {
		sum += e.DurMS
		if e.OffsetMS != 1000 {
			t.Errorf("%s 的 offset 应为 1000（两者同时开始），实得 %d", e.Key, e.OffsetMS)
		}
	}
	// 这就是 OffsetMS 存在的理由：Σdur=6000，但墙钟跨度只有 4000。
	// 只存 dur 时聚合层会算出 other = 5000-0-6000 = -1000，被 max(0,·) 吞成 0。
	if sum != 6000 {
		t.Errorf("两个工具段之和应为 6000（2000+4000），实得 %d", sum)
	}
	last := pick(end, proto.TimingKindTurn)
	if len(last) == 0 || last[len(last)-1].DurMS != 5000 {
		t.Errorf("回合墙钟应为 5000ms，实得 %+v", last)
	}

	// 并发结束后只算**一个**批次：下一个模型段的键必须是 api/1/1 而不是 api/1/2
	api := pick(end, proto.TimingKindAPI)
	if len(api) != 1 || api[0].Key != "api/1/1" {
		t.Errorf("并发的一批工具只应让批次号 +1，实得 %+v", api)
	}
}

func TestSegmenterUnpairedToolProducesNothing(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)
	_ = s.BeginTurn(1)
	c.add(time.Second)
	_ = s.ToolStart("t1", "Bash", "sleep 100")
	// executor 半路死掉，tool_result 永不到达 → 直接收尾
	end := s.EndTurn()

	if got := pick(end, proto.TimingKindTool); len(got) != 0 {
		t.Errorf("没配上结果的工具**不产条目**（而不是产一条 dur=0），实得 %+v", got)
	}
	// 正面锁：确认它没有搬去别的 Kind 里冒充
	if got := pick(end, proto.TimingKindAPI); len(got) != 0 {
		t.Errorf("工具还开着时不该关模型段，实得 %+v", got)
	}
	// ToolEnd 对不认识的 part 必须回 -1（未知），不是 0（很快）
	if d, es := s.ToolEnd("t1"); d != -1 || es != nil {
		t.Errorf("回合已收尾后的迟到结果应回 (-1,nil)，实得 (%v,%+v)", d, es)
	}
}

func TestSegmenterKeysAreReplayStable(t *testing.T) {
	// 幂等键必须从内容派生：同一段序列跑两遍（模拟上游重放 / agentd 重启后
	// 重新喂同一批事件），产出的 Key 集合必须完全相同。用进程内计数器时这条会红。
	keysOf := func() []string {
		c := newTestClock()
		s := NewSegmenter(c.now)
		var all []proto.TimingEntry
		all = append(all, s.BeginTurn(7)...)
		c.add(time.Second)
		all = append(all, s.ToolStart("x", "Bash", "ls")...)
		c.add(time.Second)
		_, es := s.ToolEnd("x")
		all = append(all, es...)
		c.add(time.Second)
		all = append(all, s.ToolStart("y", "Bash", "pwd")...)
		c.add(time.Second)
		_, es = s.ToolEnd("y")
		all = append(all, es...)
		all = append(all, s.EndTurn()...)
		var ks []string
		for _, e := range all {
			ks = append(ks, e.Key)
		}
		return ks
	}
	a, b := keysOf(), keysOf()
	if len(a) != len(b) {
		t.Fatalf("两遍产出的条目数不同：%d vs %d", len(a), len(b))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("第 %d 条键不稳定：%q vs %q", i, a[i], b[i])
		}
	}
	// 回合号来自入参而非内部计数器：第 7 回合的键必须带 7
	if a[0] != "turn/7" {
		t.Errorf("回合号应取自入参，实得 %q", a[0])
	}
}

func TestSegmenterDetailTruncatedAtCollector(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)
	_ = s.BeginTurn(1)
	long := ""
	for i := 0; i < DetailRunes*2; i++ {
		long += "中"
	}
	_ = s.ToolStart("t", "Bash", long)
	_, es := s.ToolEnd("t")
	tools := pick(es, proto.TimingKindTool)
	if len(tools) != 1 {
		t.Fatal("应有一个工具段")
	}
	// 截断在采集侧做（契约文档 §3.4：store 明确不做截断，两处都以为对方管了
	// 是这类字段最常见的失守方式）
	if r := []rune(tools[0].Detail); len(r) > DetailRunes+len([]rune("…")) {
		t.Errorf("Detail 应在采集侧截到 %d rune 上下，实得 %d", DetailRunes, len(r))
	}
	if tools[0].Detail == long {
		t.Error("Detail 未被截断——凭据边界失守")
	}
}

func TestSegmenterEndTurnIsIdempotent(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)
	_ = s.BeginTurn(1)
	c.add(time.Second)
	if got := s.EndTurn(); len(got) == 0 {
		t.Fatal("首次 EndTurn 应产出条目")
	}
	if got := s.EndTurn(); got != nil {
		t.Errorf("重复 EndTurn 应返回 nil（emit 通道上可能重复触发），实得 %+v", got)
	}
}

func TestSegmenterNilSafe(t *testing.T) {
	// 与 FrameWriter 同款约定：构造失败时 adapter 直接持 nil，调用点不判空
	var s *Segmenter
	if got := s.BeginTurn(1); got != nil {
		t.Error("nil.BeginTurn 应为空操作")
	}
	if got := s.ToolStart("a", "b", "c"); got != nil {
		t.Error("nil.ToolStart 应为空操作")
	}
	if d, es := s.ToolEnd("a"); d != -1 || es != nil {
		t.Error("nil.ToolEnd 应返回 (-1,nil)")
	}
	if got := s.EndTurn(); got != nil {
		t.Error("nil.EndTurn 应为空操作")
	}
}

func TestSegmenterPauseWaitingMovesWindowToOther(t *testing.T) {
	c := newTestClock()
	s := NewSegmenter(c.now)

	all := runSeq(s, c,
		func(s *Segmenter, _ *fakeClock) []proto.TimingEntry { return s.BeginTurn(1) },
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(2 * time.Second)
			return s.ToolStart("t1", "Bash", "go test ./...")
		},
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(3 * time.Second)
			return s.PauseWaiting("t1")
		},
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(60 * time.Second)
			return s.Resume("t1")
		},
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(5 * time.Second)
			_, entries := s.ToolEnd("t1")
			return entries
		},
		func(s *Segmenter, c *fakeClock) []proto.TimingEntry {
			c.add(2 * time.Second)
			return s.EndTurn()
		},
	)

	tools := pick(all, proto.TimingKindTool)
	if len(tools) != 1 {
		t.Fatalf("应恰好有一条工具段，实得 %d", len(tools))
	}
	if tools[0].DurMS != 8000 || tools[0].OffsetMS != 2000 {
		t.Fatalf("工具段应只含暂停前 3s 与恢复后 5s，dur=%d offset=%d",
			tools[0].DurMS, tools[0].OffsetMS)
	}
	var total, api, tool int64
	for _, e := range all {
		switch e.Kind {
		case proto.TimingKindTurn:
			if e.DurMS > total {
				total = e.DurMS
			}
		case proto.TimingKindAPI:
			api += e.DurMS
		case proto.TimingKindTool:
			tool += e.DurMS
		}
	}
	if total != 72000 || api != 4000 || tool != 8000 {
		t.Fatalf("总账应为 total=72000 api=4000 tool=8000，实得 %d/%d/%d",
			total, api, tool)
	}
	if other := total - api - tool; other != 60000 {
		t.Fatalf("审批等待应进入 other，实得 %dms", other)
	}
}

func TestSegmenterWaitingLifecycleIsNilSafe(t *testing.T) {
	var nilSegmenter *Segmenter
	if got := nilSegmenter.PauseWaiting("t1"); got != nil {
		t.Fatalf("nil PauseWaiting 应为空，实得 %v", got)
	}
	if got := nilSegmenter.Resume("t1"); got != nil {
		t.Fatalf("nil Resume 应为空，实得 %v", got)
	}

	c := newTestClock()
	s := NewSegmenter(c.now)
	s.BeginTurn(1)
	c.add(time.Second)
	s.ToolStart("t1", "Bash", "echo hi")
	if got := s.PauseWaiting("missing"); got != nil {
		t.Fatalf("未知 part 不应产生条目：%v", got)
	}
	s.PauseWaiting("t1")
	c.add(2 * time.Second)
	if got := s.PauseWaiting("t1"); got != nil {
		t.Fatalf("重复 Pause 不应重复打开窗口：%v", got)
	}
	s.Resume("t1")
	c.add(3 * time.Second)
	if got := s.Resume("t1"); got != nil {
		t.Fatalf("重复 Resume 不应重复扣时：%v", got)
	}
	c.add(time.Second)
	dur, _ := s.ToolEnd("t1")
	if dur != 4*time.Second {
		t.Fatalf("连续多次信号后的工具耗时应为 1s+3s，实得 %s", dur)
	}

	s.BeginTurn(2)
	s.ToolStart("t2", "Bash", "echo pending")
	c.add(time.Second)
	s.PauseWaiting("t2")
	c.add(30 * time.Second)
	s.EndTurn()
	if dur, entries := s.ToolEnd("t2"); dur != -1 || entries != nil {
		t.Fatalf("回合终止应收口未闭窗口且丢弃未结束工具，dur=%s entries=%v", dur, entries)
	}
}
