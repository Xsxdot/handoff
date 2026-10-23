// residentwake.go —— B435 O2 驻场者门铃信桥（spec S5 唤醒半边、D10 保留项；契约 C-234、C-403/404、
// C-407–C-412、C-415–C-418；拍板 D-A 门铃信；O2 plan Q1–Q3 甲）。
//
// 职责：自动化消费循环的一步——本轮新读到的会话消息经匹配函数 collab.Service.Recipients 落到某会话驻场者
// 地址、该驻场者登记了桌面端地址（cli_session）、且其 machine 是本机 → 每个地址写一封两行门铃信
// （proto.RenderResidentLetter，Count/MinSeq/MaxSeq 覆盖本批，C-407）进 $HOME/.handoff/desktop-wake，
// 随后对 desktop_session 调一次 continue 深链（C-412）。信里只有会话号、条数、seq 区间与取信命令，
// 交付唯一经 `handoff session wait`（D-A）。
//
// 边界（承重，TestB435ResidentBridgeSourceGuard 锁静态半边）：
//   - 判定只经 Recipients（C-234/C-408）；不解析 mentions、不判人类作者；
//   - 对账本只读驻场者行，不推进 session_delivery_cursors（C-410）、不落账本事件、不经 keystone（C-411）；
//   - 非本机驻场者零动作：零写信、零 opener、零转交、零 needs_human（C-415/C-416）；从不经
//     transferCoordinatorWake（C-417），不新建转交 DTO（C-418）；
//   - 只在消费循环里按铃，转交收件路径（handleCoordWake → automationWakeEvents）天然不按（Q2 甲）；
//   - 只调用 desktopwake.go 的保留符号（Q3 甲六组：writeDesktopLetter、claudeContinueURL、
//     desktopMailboxHome、opener、sweepDesktopLetterTmp、desktopClock），不引用 B434 卡级失败段。
//
// 失败处置（欠账 9，Q1 甲；不新增任何对外可见面）：
//   - Recipients / 读驻场者行失败 → 返回错误，消费轮报错、水位不越过（C-228 同口径），恢复后再按；
//   - 「信未落盘」（取 HOME、建目录、写/Link 失败）→ Error（session、identity、stage、cause），进程内按地址
//     有界重试（退避 residentWakeRetryBase 起倍增，至多 residentWakeMaxAttempts 次写信尝试），到上限 Error
//     「放弃按铃」；信箱键不安全是确定性拒绝，直接放弃。**不挡自动化水位**：门铃无内容，消息仍在驻场者
//     `session wait` 游标之后，失败不丢交付；
//   - 「信已落盘、continue 未发出」（深链非法、opener 未装配/报错/非 darwin）→ Error，收尾、不重写（D5 不对称）。
//
// 至少一次：按铃在推进水位之前；写信后、推进水位前崩溃 → 重启后同批再按一次（门铃无内容，重复无害）。
// 在途重试随进程重启丢失（同上，不丢消息）。日志从不记消息正文与作者。
package agentd

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// residentWakeRetryBase 是「信未落盘」后首次重试的退避；其后每次失败倍增（5s、10s、20s、40s）。
// 消费循环 2s 一轮，5s 起步让偶发失败（目录被并发清理等）在下一两轮内恢复。
const residentWakeRetryBase = 5 * time.Second

// residentWakeMaxAttempts 是同一地址一批门铃的写信尝试上限（含首次）。5 次覆盖约 75s：瞬时故障足以
// 恢复；持久故障（HOME 不可写）到上限即放弃，不无限打日志——消息本身仍在 `session wait` 积压里。
const residentWakeMaxAttempts = 5

// residentDoorbellRungMsg 是门铃完整成功（信已落盘且 continue 已发出）的唯一日志文案；未打开分支
// 绝不打这一句（「报成功但没做」的反面锁，residentwake_test.go 断言其缺席）。
const residentDoorbellRungMsg = "驻场者门铃已按：信已落盘、continue 已发出"

// residentWakeState 是门铃的进程内状态（Server.residentWake，零值可用，自带锁，不经 automationMu）。
//   - rung：已由门铃处置（按铃、未打开收尾、跳过、放弃、或已并入在途重试）的事件 seq——水位被他处
//     在飞认领挡住、同一事件被重读时不重按（每进程每事件至多一次）；按本轮起点水位修剪，
//     水位之下的 seq 永不再被读到；
//   - retries：「信未落盘」的在途重试，键 = 驻场者地址游标键（proto.DeliveryCursorKey）。
//
// 为什么不复用 automationSeen：主循环把纯驻场者事件当「不唤醒」标 seen，而 Recipients 读失败那一轮
// 门铃没按成——借用它会在恢复后把该事件当重复跳过而丢铃（plan 台账 L4.4）。
type residentWakeState struct {
	mu      sync.Mutex
	rung    map[int64]struct{}
	retries map[string]*residentDoorbellBatch
}

// residentDoorbellBatch 是一个驻场者地址的一批命中（一封信的内容来源）。
type residentDoorbellBatch struct {
	addr     proto.DeliveryAddress
	seqs     []int64
	count    int
	minSeq   int64
	maxSeq   int64
	failures int       // 已失败的写信尝试次数（只在重试表里有意义）
	nextAt   time.Time // 下一次重试的最早时刻（desktopClock 读数）
	notices  []*proto.TaskTicketNotice
}

func (b *residentDoorbellBatch) add(seq int64) {
	b.seqs = append(b.seqs, seq)
	b.count++
	if b.minSeq == 0 || seq < b.minSeq {
		b.minSeq = seq
	}
	if seq > b.maxSeq {
		b.maxSeq = seq
	}
}

func (b *residentDoorbellBatch) hasNotice(n proto.TaskTicketNotice) bool {
	for _, current := range b.notices {
		if current != nil && current.SourceMachine == n.SourceMachine && current.TaskID == n.TaskID && current.TicketID == n.TicketID && current.OwnershipVersion == n.OwnershipVersion {
			return true
		}
	}
	return false
}

// merge 把 other 的命中并进 b（同一地址：在途重试遇到新命中，一封信覆盖两批）。
func (b *residentDoorbellBatch) merge(other *residentDoorbellBatch) {
	for _, seq := range other.seqs {
		b.add(seq)
	}
	for _, n := range other.notices {
		if n != nil && !b.hasNotice(*n) {
			b.notices = append(b.notices, n)
		}
	}
}

func (st *residentWakeState) isRung(seq int64) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	_, ok := st.rung[seq]
	return ok
}

func (st *residentWakeState) markRung(seqs ...int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.rung == nil {
		st.rung = make(map[int64]struct{})
	}
	for _, seq := range seqs {
		st.rung[seq] = struct{}{}
	}
}

// pruneUpTo 删去 ≤ watermark 的已处置 seq（消费循环只读水位之后的事件，它们不会再出现）。
func (st *residentWakeState) pruneUpTo(watermark int64) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for seq := range st.rung {
		if seq <= watermark {
			delete(st.rung, seq)
		}
	}
}

// takeRetry 取出并移除 key 的在途重试（无则 nil）。
func (st *residentWakeState) takeRetry(key string) *residentDoorbellBatch {
	st.mu.Lock()
	defer st.mu.Unlock()
	b := st.retries[key]
	delete(st.retries, key)
	return b
}

func (st *residentWakeState) putRetry(key string, b *residentDoorbellBatch) {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.retries == nil {
		st.retries = make(map[string]*residentDoorbellBatch)
	}
	st.retries[key] = b
}

// retryKeys 返回在途重试的键（排序，保证一轮内处理顺序确定）。
func (st *residentWakeState) retryKeys() []string {
	st.mu.Lock()
	defer st.mu.Unlock()
	keys := make([]string, 0, len(st.retries))
	for k := range st.retries {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// residentDoorbellRound 是一轮门铃的计数（只进 Debug 汇总日志）。
type residentDoorbellRound struct {
	addresses, rung, unopened, undelivered, skipped int
	skipReasons                                     map[string]int
}

func (r *residentDoorbellRound) skip(reason string) {
	r.skipped++
	if r.skipReasons == nil {
		r.skipReasons = make(map[string]int)
	}
	r.skipReasons[reason]++
}

// ringResidentDoorbells 是门铃入口：由 consumeAutomationEventsOnce 在逐事件循环之后、卡批循环与水位
// 推进之前调用一次（卡批循环里 attach 暂缓会提前返回，门铃放其后会被跳过）。
//
// 参数：from 本轮起点水位（只看 seq > from 的事件，并据此修剪 rung）；events 本轮读到的事件（升序）。
// 返回：仅 Recipients 或读驻场者行失败时返回错误——调用方本轮报错、不推进水位（C-228）；写信/opener
// 失败不返回错误（Q1 甲：门铃失败不挡自动化水位），只进 Error 日志与在途重试。
func (s *Server) ringResidentDoorbells(from int64, events []proto.LedgerEvent) error {
	st := &s.residentWake
	st.pruneUpTo(from)
	round := residentDoorbellRound{}
	batches := map[string]*residentDoorbellBatch{}
	for _, ev := range events {
		if ev.Seq <= from || (ev.Type != ledger.EvRoomMessage && ev.Type != ledger.EvTaskTicketNotice) || st.isRung(ev.Seq) {
			continue
		}
		if ev.Type == ledger.EvTaskTicketNotice {
			n, err := decodeTaskTicketNotice(ev.Payload)
			if err != nil {
				return fmt.Errorf("doorbell notice decode seq=%d: %w", ev.Seq, err)
			}
			current, matches, err := s.currentTaskTicketNotice(n)
			if err != nil {
				return fmt.Errorf("read current doorbell notice: %w", err)
			}
			if !matches || current.Status != "pending" && current.Status != "needs_human" || n.Target.Kind != proto.DeliveryResident {
				st.markRung(ev.Seq)
				continue
			}
			resident, ok, err := s.autoLedger.SessionResident(n.Target.Session)
			if err != nil {
				return fmt.Errorf("read addressed desktop resident: %w", err)
			}
			addr := n.Target
			if reason, skip := s.residentDoorbellIneligible(addr, resident, ok); skip {
				if reason != "not_local" && reason != "push_resident" {
					st.markRung(ev.Seq)
				}
				continue
			}
			key := proto.DeliveryCursorKey(addr)
			b := batches[key]
			if b == nil {
				b = &residentDoorbellBatch{addr: addr}
				batches[key] = b
			}
			b.add(ev.Seq)
			b.notices = append(b.notices, &n)
			continue
		}
		msg, human, err := decodeRoomMessageForWake(ev)
		if err != nil {
			s.log.Error("门铃解码会话消息失败，本轮不越过该消息", "seq", ev.Seq, "cause", err)
			return fmt.Errorf("门铃：%w", err)
		}
		if !human {
			st.markRung(ev.Seq)
			continue
		}
		if s.rooms == nil {
			s.log.Error("门铃判定不可用：collab 服务未装配，本轮不按铃", "seq", ev.Seq, "room", msg.Room)
			return nil
		}
		delivery, err := s.rooms.Recipients(msg, ev.Actor)
		if err != nil {
			s.log.Warn("门铃匹配函数判定失败，本轮不越过该消息", "seq", ev.Seq, "room", msg.Room, "cause", err)
			return fmt.Errorf("门铃：事件 %d 的会话寻址判定失败: %w", ev.Seq, err)
		}
		hit := false
		for _, addr := range delivery.Addresses {
			if addr.Kind != proto.DeliveryResident {
				continue
			}
			key := proto.DeliveryCursorKey(addr)
			if key == "" {
				s.log.Warn("门铃：驻场者地址缺段，跳过", "seq", ev.Seq, "session", addr.Session)
				continue
			}
			b := batches[key]
			if b == nil {
				b = &residentDoorbellBatch{addr: addr}
				batches[key] = b
			}
			b.add(ev.Seq)
			hit = true
		}
		if !hit {
			st.markRung(ev.Seq)
		}
	}
	// Re-read pending and needs_human desktop notices on every poll. The ledger
	// cursor may already have passed a failed bell; durable notice state is the retry source.
	active, err := s.activeTaskTicketNotices()
	if err != nil {
		return fmt.Errorf("list active desktop notices: %w", err)
	}
	for i := range active {
		n := &active[i]
		if (n.Status != "pending" && n.Status != "needs_human") || n.LastError == "desktop_open_failed" || n.Target == nil || n.Target.Kind != proto.DeliveryResident {
			continue
		}
		resident, ok, readErr := s.autoLedger.SessionResident(n.Target.Session)
		if readErr != nil {
			return fmt.Errorf("read active notice resident: %w", readErr)
		}
		if _, skip := s.residentDoorbellIneligible(*n.Target, resident, ok); skip {
			continue
		}
		key := proto.DeliveryCursorKey(*n.Target)
		b := batches[key]
		if b == nil {
			b = &residentDoorbellBatch{addr: *n.Target}
			batches[key] = b
		}
		wire := proto.TaskTicketNotice{SourceMachine: n.SourceMachine, TaskID: n.TaskID, TicketID: n.TicketID, SourceSeq: n.SourceSeq, OwnershipVersion: n.OwnershipVersion, Target: *n.Target, TicketKind: n.TicketKind}
		if err := proto.ValidateTaskTicketNotice(wire); err != nil {
			s.log.Warn("active desktop notice is invalid; bell skipped", "task", n.TaskID, "ticket", n.TicketID, "reason", "invalid_notice")
			continue
		}
		if b.hasNotice(wire) {
			continue
		}
		b.add(n.SourceSeq)
		// The active projection carries current identity/version, unlike an old event.
		b.notices = append(b.notices, &wire)
	}

	keys := make([]string, 0, len(batches))
	for k := range batches {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	now := s.desktopClock()
	attempted := make(map[string]bool, len(keys))
	for _, key := range keys {
		b := batches[key]
		round.addresses++
		resident, ok, err := s.autoLedger.SessionResident(b.addr.Session)
		if err != nil {
			s.log.Warn("门铃读驻场者行失败，本轮不越过该批", "session", b.addr.Session,
				"identity", b.addr.Identity, "min_seq", b.minSeq, "max_seq", b.maxSeq, "cause", err)
			return fmt.Errorf("门铃：读会话 %s 驻场者: %w", b.addr.Session, err)
		}
		st.markRung(b.seqs...)
		if reason, ok := s.residentDoorbellIneligible(b.addr, resident, ok); ok {
			round.skip(reason)
			// 地址已不可按（换人、请下、改到他机）：在途重试一并作废。
			if prev := st.takeRetry(key); prev != nil {
				s.log.Info("门铃在途重试作废：驻场者地址已不可按", "session", b.addr.Session,
					"identity", b.addr.Identity, "reason", reason)
			}
			continue
		}
		if prev := st.takeRetry(key); prev != nil {
			prev.merge(b)
			b = prev
			if now.Before(b.nextAt) {
				// 退避窗内：新命中并进在途重试，等窗口到再写（不对坏信箱连续施压）。
				st.putRetry(key, b)
				s.log.Debug("门铃退避窗内，新命中并入在途重试", "session", b.addr.Session,
					"identity", b.addr.Identity, "count", b.count, "retry_at", b.nextAt)
				continue
			}
		}
		attempted[key] = true
		s.residentDoorbellAttempt(key, b, resident, now, &round)
	}

	// 未被本轮新命中触及、窗口已到的在途重试。
	for _, key := range st.retryKeys() {
		if attempted[key] {
			continue
		}
		b := st.takeRetry(key)
		if b == nil {
			continue
		}
		if now.Before(b.nextAt) {
			st.putRetry(key, b)
			continue
		}
		resident, ok, err := s.autoLedger.SessionResident(b.addr.Session)
		if err != nil {
			// 重试期的读失败不挡本轮水位（这批事件早已越过），按一次失败计入上限。
			s.residentDoorbellUndelivered(key, b, now, "读驻场者", err, &round)
			continue
		}
		if reason, inel := s.residentDoorbellIneligible(b.addr, resident, ok); inel {
			round.skip(reason)
			s.log.Info("门铃在途重试作废：驻场者地址已不可按", "session", b.addr.Session,
				"identity", b.addr.Identity, "reason", reason)
			continue
		}
		s.residentDoorbellAttempt(key, b, resident, now, &round)
	}

	if round.addresses > 0 || round.undelivered > 0 || round.rung > 0 || round.unopened > 0 {
		s.log.Debug("门铃本轮汇总", "from_cursor", from, "addresses", round.addresses, "rung", round.rung,
			"unopened", round.unopened, "undelivered", round.undelivered, "skipped", round.skipped,
			"skip_reasons", round.skipReasons)
	}
	return nil
}

// residentDoorbellIneligible 判驻场者地址此刻能不能按铃；返回 (原因, true) 表示跳过。
//   - 无驻场者行或身份 ≠ 地址身份（判定后有人 stand / 换人 sit）→ 不给新驻场者按旧地址的铃；
//   - push 驻场者（B444）→ 零写信，唤醒归 keystone 续轮（reason=push_resident）；
//   - 无 cli_session（未登记桌面端地址）→ 零写信（C-409）；
//   - machine 非本机（scheduling.IsLocalMachine 与 Server.IsSelfTarget 皆假）→ 零动作（C-415/C-416）；
//     machine 空 = 登记时的本机（C-415 字面，IsLocalMachine("") 为真）。
func (s *Server) residentDoorbellIneligible(addr proto.DeliveryAddress, r proto.SessionResident, ok bool) (string, bool) {
	switch {
	case !ok:
		s.log.Info("门铃跳过：会话已无驻场者", "session", addr.Session, "identity", addr.Identity)
		return "no_resident", true
	case r.Identity != addr.Identity:
		s.log.Info("门铃跳过：驻场者已换人，不按旧地址的铃", "session", addr.Session,
			"address_identity", addr.Identity, "resident_identity", r.Identity)
		return "identity_mismatch", true
	case r.DriverMode == proto.ResidentDriverPush:
		// B444 P1（plan Q2 甲 / R2）：push 驻场者由 keystone 续轮（residentpush.go），门铃不得再写信，
		// 否则同一条消息两路唤醒。显式判据，不依赖「push 行 cli_session 恰为空」这一巧合。
		s.log.Info("门铃跳过：push 驻场者由 keystone 续轮", "session", addr.Session, "identity", addr.Identity,
			"reason", "push_resident")
		return "push_resident", true
	case r.CLISession == "":
		s.log.Debug("门铃跳过：驻场者未登记桌面端地址", "session", addr.Session, "identity", addr.Identity)
		return "no_cli_session", true
	case !scheduling.IsLocalMachine(r.Machine) && !s.IsSelfTarget(r.Machine):
		s.log.Debug("门铃跳过：驻场者桌面端不在本机", "session", addr.Session, "identity", addr.Identity,
			"machine", r.Machine)
		return "not_local", true
	}
	return "", false
}

// residentDoorbellAttempt 对一批命中写一封门铃信并发 continue 深链，按结果收尾或进在途重试。
func (s *Server) residentDoorbellAttempt(key string, b *residentDoorbellBatch, r proto.SessionResident,
	now time.Time, round *residentDoorbellRound) {
	letter := proto.RenderResidentLetter(proto.ResidentLetter{
		Session: b.addr.Session, Count: b.count, MinSeq: b.minSeq, MaxSeq: b.maxSeq,
	})
	home, err := s.desktopMailboxHome()
	if err != nil {
		s.setDoorbellNoticeDelivery(b, "needs_human", "delivery_failed", "desktop_bell_failed")
		s.residentDoorbellUndelivered(key, b, now, "取 HOME", err, round)
		return
	}
	letterPath, err := writeDesktopLetter(home, r.CLISession, letter, s.desktopClock())
	if err != nil {
		s.residentDoorbellUndelivered(key, b, now, "写信箱", err, round)
		return
	}
	s.sweepDesktopLetterTmp(home, r.CLISession)
	s.log.Info("驻场者门铃信已落盘", "session", b.addr.Session, "identity", b.addr.Identity,
		"letter", letterPath, "count", b.count, "min_seq", b.minSeq, "max_seq", b.maxSeq)
	unopened := func(stage string, cause error) {
		s.setDoorbellNoticeDelivery(b, "needs_human", "delivery_failed", "desktop_open_failed")
		round.unopened++
		s.log.Error("驻场者门铃信已落盘、continue 未发出（不重写）", "session", b.addr.Session,
			"identity", b.addr.Identity, "letter", letterPath, "stage", stage, "cause", cause)
	}
	link, err := claudeContinueURL(r.DesktopSession)
	if err != nil {
		unopened("构造 continue 深链", err)
		return
	}
	if s.desktopOpen == nil {
		unopened("发 continue 深链", errors.New("本机未装配桌面端深链 opener"))
		return
	}
	if err := s.desktopOpen(link); err != nil {
		unopened("发 continue 深链", err)
		return
	}
	round.rung++
	s.setDoorbellNoticeDelivery(b, "delivered", "", "")
	s.log.Info(residentDoorbellRungMsg, "session", b.addr.Session, "identity", b.addr.Identity,
		"desktop_session", r.DesktopSession, "letter", letterPath, "count", b.count,
		"min_seq", b.minSeq, "max_seq", b.maxSeq)
}

func (s *Server) setDoorbellNoticeDelivery(b *residentDoorbellBatch, status, reason, lastError string) {
	for _, n := range b.notices {
		if err := s.setTaskNoticeDelivery(n, status, reason, lastError); err != nil {
			s.log.Warn("桌面通知状态更新失败", "task", n.TaskID, "ticket", n.TicketID, "status", status, "cause", err)
		}
	}
}

// residentDoorbellUndelivered 处置「信未落盘」：计一次失败；信箱键不安全（确定性拒绝）或到上限 →
// Error「放弃按铃」并收尾；否则按倍增退避登记在途重试。从不返回错误（Q1 甲：不挡水位）。
func (s *Server) residentDoorbellUndelivered(key string, b *residentDoorbellBatch, now time.Time,
	stage string, cause error, round *residentDoorbellRound) {
	round.undelivered++
	b.failures++
	if errors.Is(cause, errDesktopMailboxKeyUnsafe) || b.failures >= residentWakeMaxAttempts {
		s.setDoorbellNoticeDelivery(b, "needs_human", "delivery_failed", "desktop_bell_failed")
		s.log.Error("驻场者门铃信未落盘，放弃按铃（消息仍在 session wait 积压里）", "session", b.addr.Session,
			"identity", b.addr.Identity, "stage", stage, "attempts", b.failures, "count", b.count,
			"min_seq", b.minSeq, "max_seq", b.maxSeq, "cause", cause)
		return
	}
	s.setDoorbellNoticeDelivery(b, "needs_human", "delivery_failed", "desktop_bell_failed")
	b.nextAt = now.Add(residentWakeRetryBase << (b.failures - 1))
	s.residentWake.putRetry(key, b)
	s.log.Error("驻场者门铃信未落盘，稍后重试", "session", b.addr.Session, "identity", b.addr.Identity,
		"stage", stage, "attempts", b.failures, "max_attempts", residentWakeMaxAttempts,
		"retry_at", b.nextAt, "count", b.count, "cause", cause)
}
