// timing.go —— 回合内的耗时分段（模型段 / 工具段 / 回合墙钟）。
//
// 职责：
//   - 接收四类信号（回合开始 / 工具开始 / 工具结束 / 回合结束），把一个回合的
//     墙钟切成交替的模型段与工具段，产出 proto.TimingEntry
//   - 幂等键一律从内容派生（回合号 + part / 批次号），不用进程内计数器
//
// 边界：
//   - 不写文件、不发事件：产出的条目交回调用方（adapter）经 AdapterEvent 上报
//   - 不认识任何具体 executor：四家喂进来的是同一组信号，口径因此可比
//   - **不产 other（未归类）条目**：它只在聚合层由差额算出（契约文档 §2.1）
//
// 为什么这四类信号足够：模型段的边界恰好是「回合开始 / 上一批工具全部结束」到
// 「第一个工具开始 / 回合结束」，全部可由这四个信号推出，不需要 adapter 额外
// 告诉我们「模型现在开始想了」——那个时刻在四家协议里根本没有统一的表达。
package turn

import (
	"fmt"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/executor"
	"github.com/Xsxdot/handoff/internal/proto"
)

// DetailRunes 是 TimingEntry.Detail 的 rune 上限（契约文档 §3.4 的凭据边界）。
//
// 截断在**采集侧**做：store 侧明确不截（UpsertTiming 的注释写死了这一点）。
// 两处都以为对方管了，是这类字段最常见的失守方式。
const DetailRunes = 200

// openTool 是一次还没收到结果的工具调用。
type openTool struct {
	tool   string
	detail string
	start  time.Time
	// waiting tracks an external person's response window. Keeping it here lets
	// ToolEnd subtract that window without changing the wire timing shape.
	waiting      bool
	waitingSince time.Time
	waitingMS    time.Duration
}

// Segmenter 把一个回合切成模型段与工具段。
//
// 并发安全：全部方法在同一把锁内完成读改写。adapter 的流处理与 Send 可能并发
// 触碰它（BeginTurn 来自 Send，工具信号来自流），与 FrameWriter 同款理由。
//
// nil 安全：全部方法对 nil 接收者是空操作（ToolEnd 返回 -1）。构造失败时
// adapter 直接持 nil，调用点不必到处判空——与 FrameWriter 同款约定。
type Segmenter struct {
	now func() time.Time

	mu   sync.Mutex
	turn int // 当前回合号；0 = 没有开着的回合
	// turnStart 是本回合起点，OffsetMS 与回合墙钟都以它为基准
	turnStart time.Time
	// batches 是本回合内**已完成**的工具批次数，模型段的键靠它派生
	batches int
	// apiStart 是当前模型段的起点；零值 = 当前没有开着的模型段（工具正在跑）
	apiStart time.Time
	open     map[string]*openTool
	live     int // 当前开着的工具数；由 1→0 才算一个批次结束
}

// NewSegmenter 创建段切分器。
//
// 参数：now 是时钟，传 nil 用 time.Now。
//
// 注意：**时钟必须能注入**。判据依赖真实时间的测试在 CI 负载下会偶发翻红，
// 而偶发红会被当噪音忽略，于是这条判据实际上失效了。
func NewSegmenter(now func() time.Time) *Segmenter {
	if now == nil {
		now = time.Now
	}
	return &Segmenter{now: now, open: map[string]*openTool{}}
}

// BeginTurn 开启回合号为 turn 的新回合，并收尾上一个还开着的回合。
//
// 参数：turn 必须取自 FrameWriter.Turn()——**不要自建计数器**，理由见该方法注释。
// 返回：要上报的条目（上一回合的收尾条目 + 本回合的初始 turn 条目）。
func (s *Segmenter) BeginTurn(turn int) []proto.TimingEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.closeTurnLocked()
	now := s.now()
	s.turn, s.turnStart, s.batches = turn, now, 0
	s.apiStart = now
	s.open = map[string]*openTool{}
	s.live = 0
	return append(out, s.turnEntryLocked(now))
}

// ToolStart 记一次工具调用的开始。
//
// 参数：part 是回合内唯一的调用标识（与 tool_call 帧的 Part 同值）；
// tool 是工具名（进 Label）；detail 是命令/入参摘要（进 Detail，本方法负责截断）。
// 返回：要上报的条目。本批工具的第一个 ToolStart 会顺带收掉当前模型段。
//
// 注意：同 part 重复 start 不重复计数——上游流重放时这条是唯一的防线。
func (s *Segmenter) ToolStart(part, tool, detail string) []proto.TimingEntry {
	if s == nil || part == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn == 0 {
		return nil // 回合外的信号一律丢弃，不猜回合号
	}
	now := s.now()
	var out []proto.TimingEntry
	if s.live == 0 {
		if e, ok := s.closeAPILocked(now); ok {
			out = append(out, e)
		}
	}
	if _, dup := s.open[part]; !dup {
		markerRunes := len([]rune(executor.TruncationMarker))
		contentRunes := DetailRunes - markerRunes
		headRunes := contentRunes / 2
		tailRunes := contentRunes - headRunes
		s.open[part] = &openTool{
			tool: tool, detail: HeadTailRunes(detail, headRunes, tailRunes), start: now,
		}
		s.live++
	}
	return append(out, s.turnEntryLocked(now))
}

// ToolEnd 记一次工具调用的结束。
//
// 返回：
//   - dur: 本次调用耗时，直接交给 FrameWriter.ToolResult。**没配上 start 时
//     返回 -1（不知道），不是 0**——0ms 是一次真实可能的极快调用
//   - entries: 要上报的条目；没配上时为 nil（**不产 dur=0 的假条目**）
func (s *Segmenter) ToolEnd(part string) (time.Duration, []proto.TimingEntry) {
	if s == nil {
		return -1, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	ot, ok := s.open[part]
	if !ok {
		return -1, nil
	}
	delete(s.open, part)
	s.live--
	now := s.now()
	finishWaitingLocked(ot, now)
	dur := now.Sub(ot.start) - ot.waitingMS
	if dur < 0 {
		dur = 0
	}
	out := []proto.TimingEntry{{
		Key:      fmt.Sprintf("tool/%d/%s", s.turn, part),
		Kind:     proto.TimingKindTool,
		Turn:     s.turn,
		DurMS:    dur.Milliseconds(),
		Label:    ot.tool,
		Detail:   ot.detail,
		OffsetMS: ot.start.Sub(s.turnStart).Milliseconds(),
	}}
	// 只有**整批**结束才算一个批次：并发的多个工具共享一个批次号，
	// 否则模型段的键会跳号，而跳号本身不报错、只是账对不上
	if s.live == 0 {
		s.batches++
		s.apiStart = now
	}
	return dur, append(out, s.turnEntryLocked(now))
}

// PauseWaiting pauses an open tool while waiting for an external person.
//
// Parameters: part is the existing tool-call pairing key. Returns a current
// turn marker on success, nil for a nil receiver, an unknown part, a closed
// turn, an empty part, or an already-paused tool. The waiting interval is
// subtracted when ToolEnd closes the tool; no new wire timing kind is emitted.
func (s *Segmenter) PauseWaiting(part string) []proto.TimingEntry {
	if s == nil || part == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn == 0 {
		return nil
	}
	ot, ok := s.open[part]
	if !ok || ot.waiting {
		return nil
	}
	now := s.now()
	ot.waiting = true
	ot.waitingSince = now
	return []proto.TimingEntry{s.turnEntryLocked(now)}
}

// Resume ends an external person's response window for an open tool.
//
// Parameters: part is the existing tool-call pairing key. Returns a current
// turn marker on success, nil for a nil receiver, an unknown part, a closed
// turn, an empty part, or a tool that is not paused. A clock moving backwards
// contributes no negative waiting duration.
func (s *Segmenter) Resume(part string) []proto.TimingEntry {
	if s == nil || part == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.turn == 0 {
		return nil
	}
	ot, ok := s.open[part]
	if !ok || !ot.waiting {
		return nil
	}
	now := s.now()
	finishWaitingLocked(ot, now)
	return []proto.TimingEntry{s.turnEntryLocked(now)}
}

// EndTurn 收尾当前回合：关掉还开着的模型段，刷最后一条 turn 条目。
//
// 幂等：回合已收尾时返回 nil。调用点（adapter 的回合收尾入口）可能被重复触发。
//
// 注意：还开着的工具**不产条目**——没有结束时刻就没有耗时。它造成的缺口由
// 聚合层的 Partial 标出来，这正是 Partial 存在的理由。
func (s *Segmenter) EndTurn() []proto.TimingEntry {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closeTurnLocked()
}

// closeTurnLocked 收尾当前回合。调用方必须持锁。
func (s *Segmenter) closeTurnLocked() []proto.TimingEntry {
	if s.turn == 0 {
		return nil
	}
	now := s.now()
	for _, ot := range s.open {
		finishWaitingLocked(ot, now)
	}
	var out []proto.TimingEntry
	if e, ok := s.closeAPILocked(now); ok {
		out = append(out, e)
	}
	out = append(out, s.turnEntryLocked(now))
	s.open = nil
	s.live = 0
	s.turn = 0
	return out
}

// finishWaitingLocked closes an active waiting window. Callers must hold mu.
// A backwards clock is treated as zero elapsed time so a delayed permission
// response cannot create a negative tool duration.
func finishWaitingLocked(ot *openTool, now time.Time) {
	if ot == nil || !ot.waiting {
		return
	}
	if elapsed := now.Sub(ot.waitingSince); elapsed > 0 {
		ot.waitingMS += elapsed
	}
	ot.waiting = false
	ot.waitingSince = time.Time{}
}

// closeAPILocked 关掉当前模型段。没有开着的模型段时返回 (零值,false)。
// 调用方必须持锁。
func (s *Segmenter) closeAPILocked(now time.Time) (proto.TimingEntry, bool) {
	if s.apiStart.IsZero() {
		return proto.TimingEntry{}, false
	}
	e := proto.TimingEntry{
		Key:   fmt.Sprintf("api/%d/%d", s.turn, s.batches),
		Kind:  proto.TimingKindAPI,
		Turn:  s.turn,
		DurMS: now.Sub(s.apiStart).Milliseconds(),
	}
	s.apiStart = time.Time{}
	return e, true
}

// turnEntryLocked 造一条当前回合的墙钟条目。调用方必须持锁。
//
// 每次段事件都刷一条（拍板 P3=(a)）：它按同键覆盖，重复上报无害，而代价是
// 「任务跑到一半时也读得到真实总时长」——审核者最想看耗时的时刻，恰恰是
// 「它怎么还没跑完」的那一刻。
func (s *Segmenter) turnEntryLocked(now time.Time) proto.TimingEntry {
	return proto.TimingEntry{
		Key:   fmt.Sprintf("turn/%d", s.turn),
		Kind:  proto.TimingKindTurn,
		Turn:  s.turn,
		DurMS: now.Sub(s.turnStart).Milliseconds(),
	}
}
