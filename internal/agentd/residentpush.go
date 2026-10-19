// residentpush.go —— B444 P1（S5 首故事）push 驻场者续轮：会话里人的无 @ 发言 → 驻场者（handoff 拉起的
// 载体）在同一 CLI 会话续一轮并回话（spec「目标架构与接缝语义」驻场者驱动形态；plan §2.2 B1–B3）。
//
// 职责（消费循环的一步，三段）：
//  1. 映射：roomMessageWakeEvents 经匹配函数 collab.Service.Recipients 落到驻场者地址 → residentAddressWake
//     读驻场行，push 且非自唤醒才产出 WakeEvent{Session, Resident}；desktop 驻场者照旧零 keystone（门铃另路）。
//  2. 驻场批：consumeResidentBatches 按会话分组，认领键 `resident:<会话>`（wake_claims.card 无外键，先例
//     projectnotify.go 的 notify:<type>），认领、准入、退避、收尾语义与卡批同形。
//  3. 回合：wakeResidentRound 读 bearing → 冻结载体准入 AdmitSeatCarrier → keystone.WakeResident → 释放名额。
//
// 边界：
//   - 判定只经 Recipients；不解析 mentions（与门铃、卡批同口径）；
//   - 驻场唤醒从不进卡批、从不经 transferCoordinatorWake（转交收件端亦丢弃，coordapi.go）；
//   - 非本机驻场者零认领、零回合、零指针行（缺陷族 R9：不转交又占认领会挡住 bearing 所在机）——跨机
//     转交需新 DTO，按 plan §6 须升级，本卡不做；
//   - 卡专属痕迹（wake_round 注释行、卡级 needs_human）不照搬：驻场者不是卡，失败的可见面是会话里的系统
//     指针行（collab.Service.Pointer），同一批至多一条（residentPushState）；
//   - keystone 不读驻场表：SessionRef 与简报上下文在这里读出后传入（plan §3 架构方向）。
//
// 失败处置（plan A1.9）：
//   - 准入满（可恢复排队）→ 认领留在飞、不标 seen，挡住水位，下一轮重读重试；落一条指针行；
//   - 其余错误（resume 失败、载体读不到、bearing 不成形）→ 收尾认领、按会话键退避、标 seen；落一条指针行；
//     消息仍在会话历史里，人再发一条即重试；
//   - 读驻场行/会话失败 → 返回 fatal，消费轮报错、水位不越过（C-228 同口径）；
//   - 会话已归档 → 零回合、标 seen（不活锁，缺陷族 R5）。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/keysclient"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/orchestration"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// residentClaimPrefix 是驻场唤醒在 wake_claims.card 列里的键前缀：`resident:<会话房间 id>`。卡号不含冒号
// 前缀，键空间不与卡认领相撞。
const residentClaimPrefix = "resident:"

// residentClaimKey 返回会话驻场唤醒的认领 / 退避键。
func residentClaimKey(session string) string { return residentClaimPrefix + session }

// residentPending 是驻场批里的一条待处理唤醒。
type residentPending struct {
	seq int64
	ev  keystone.WakeEvent
}

// residentPushState 是驻场续轮的进程内状态（零值可用，自带锁）：pointed[会话] = 最近一次已落指针行的
// 批次最大 seq——准入满时同一批按 2s 节拍重读，靠它保证同一批至多一条指针行（不刷屏）。成功即清。
// 进程内存：重启后同一批可能再落一条，安全方向（可见性多一条，不丢）。
type residentPushState struct {
	mu      sync.Mutex
	pointed map[string]int64
}

// shouldPoint 报告该会话这一批（maxSeq）是否还没落过指针行；是则记下并返回 true。
func (st *residentPushState) shouldPoint(session string, maxSeq int64) bool {
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.pointed == nil {
		st.pointed = make(map[string]int64)
	}
	if st.pointed[session] == maxSeq {
		return false
	}
	st.pointed[session] = maxSeq
	return true
}

// clear 在续轮成功后清掉该会话的指针水位，下一次失败重新可见。
func (st *residentPushState) clear(session string) {
	st.mu.Lock()
	delete(st.pointed, session)
	st.mu.Unlock()
}

// residentAddressWake 把一个驻场者地址映射成驻场唤醒（plan §2.2 B1）。返回 ok=false 的合法情形：
// 无驻场行 / 身份已换（判定后有人 stand 或换人）/ desktop 驻场者（门铃另路，C-231 不回归）/ 作者就是
// 驻场者本人（自唤醒，A1.5 / 缺陷族 R8）。读驻场行失败上抛：判定读失败不得当作「不唤醒」吞掉。
func (s *Server) residentAddressWake(ev proto.LedgerEvent, msg proto.RoomMessage, addr proto.DeliveryAddress,
	isDefault bool) (keystone.WakeEvent, bool, error) {
	if s.autoLedger == nil {
		s.log.Error("驻场者地址判定不可用：账本门面未装配，不唤醒", "seq", ev.Seq, "session", addr.Session)
		return keystone.WakeEvent{}, false, nil
	}
	r, ok, err := s.autoLedger.SessionResident(addr.Session)
	if err != nil {
		s.log.Warn("命中驻场者地址的驻场行读取失败，本轮不越过该消息", "seq", ev.Seq, "session", addr.Session, "cause", err)
		return keystone.WakeEvent{}, false, fmt.Errorf("事件 %d 读会话 %s 驻场者: %w", ev.Seq, addr.Session, err)
	}
	switch {
	case !ok || r.Identity != addr.Identity:
		s.log.Info("命中驻场者已请下或换人，本条不唤醒", "seq", ev.Seq, "session", addr.Session,
			"address_identity", addr.Identity, "resident_present", ok)
		return keystone.WakeEvent{}, false, nil
	case r.DriverMode != proto.ResidentDriverPush:
		s.log.Info("会话消息命中桌面端驻场者：不经 keystone（门铃由 residentwake.go 另行判定）",
			"seq", ev.Seq, "session", addr.Session, "identity", addr.Identity, "default", isDefault)
		return keystone.WakeEvent{}, false, nil
	case ev.Actor == addr.Identity:
		s.log.Info("驻场者本人的发言不唤醒自己", "seq", ev.Seq, "session", addr.Session, "identity", addr.Identity)
		return keystone.WakeEvent{}, false, nil
	}
	s.log.Debug("会话消息命中 push 驻场者", "seq", ev.Seq, "session", addr.Session, "identity", addr.Identity,
		"default", isDefault)
	return keystone.WakeEvent{
		Kind: keystone.WakeMessage, Session: addr.Session, Resident: addr.Identity,
		Summary: truncateRunes(msg.Body, 400),
	}, true, nil
}

// consumeResidentBatches 处理本轮全部驻场批（按会话 id 排序，确定性）。返回：processed 成功续轮消费的事件数；
// wakeErr 首个单批失败（不断整轮，调用方并入 firstWakeErr）；fatal 读账本失败（调用方本轮报错、水位不越过）。
func (s *Server) consumeResidentBatches(ctx context.Context, pending map[string][]residentPending) (processed int, wakeErr, fatal error) {
	sessions := make([]string, 0, len(pending))
	for session := range pending {
		sessions = append(sessions, session)
	}
	sort.Strings(sessions)
	for _, session := range sessions {
		n, err, f := s.consumeResidentBatch(ctx, session, pending[session])
		processed += n
		if f != nil {
			return processed, wakeErr, f
		}
		if err != nil && wakeErr == nil {
			wakeErr = err
		}
	}
	return processed, wakeErr, nil
}

// markSeen 把一批 seq 记入 automationSeen（消费循环去重集）。
func (s *Server) markSeen(seqs ...int64) {
	s.automationMu.Lock()
	for _, seq := range seqs {
		s.automationSeen[seq] = struct{}{}
	}
	s.automationMu.Unlock()
}

// consumeResidentBatch 处理一个会话的驻场批：前置闸（驻场行仍是 push 本人、本机、会话未归档）→ 认领 →
// 退避闸 → 回合 → 收尾。语义与卡批同形（wakeconsumer.go 卡批循环），差别只在可见面（指针行代替
// wake_round/needs_human）与前置闸。
func (s *Server) consumeResidentBatch(ctx context.Context, session string, batch []residentPending) (int, error, error) {
	seqs := make([]int64, 0, len(batch))
	for _, item := range batch {
		seqs = append(seqs, item.seq)
	}
	identity := batch[0].ev.Resident
	r, ok, err := s.autoLedger.SessionResident(session)
	if err != nil {
		s.log.Warn("驻场批读驻场行失败，本轮不越过该批", "session", session, "event_count", len(batch), "cause", err)
		return 0, nil, fmt.Errorf("驻场批读会话 %s 驻场者: %w", session, err)
	}
	if !ok || r.Identity != identity || r.DriverMode != proto.ResidentDriverPush {
		s.keystone.ForgetResident(session)
		s.markSeen(seqs...)
		s.log.Info("驻场批跳过：驻场者已请下、换人或不再是 push", "session", session, "identity", identity,
			"resident_present", ok, "event_count", len(batch))
		return 0, nil, nil
	}
	if !scheduling.IsLocalMachine(r.CarrierMachine) && !s.IsSelfTarget(r.CarrierMachine) {
		// R9：非 bearing 所在机零认领——不转交又占认领会挡住所在机；每机自动化游标独立，所在机自会读到。
		s.markSeen(seqs...)
		s.log.Info("驻场批跳过：push 驻场者载体不在本机，本机零认领", "session", session, "identity", identity,
			"carrier_machine", r.CarrierMachine, "event_count", len(batch))
		return 0, nil, nil
	}
	sess, err := s.autoLedger.GetSession(session)
	if err != nil {
		s.log.Warn("驻场批读会话失败，本轮不越过该批", "session", session, "cause", err)
		return 0, nil, fmt.Errorf("驻场批读会话 %s: %w", session, err)
	}
	if sess.Archived {
		s.keystone.ForgetResident(session)
		s.markSeen(seqs...)
		s.log.Info("驻场批跳过：会话已归档，在途消息不再唤醒驻场者", "session", session, "event_count", len(batch))
		return 0, nil, nil
	}
	key := residentClaimKey(session)
	claimed, err := s.claimWakeBatch(key, seqs)
	if err != nil {
		return 0, nil, err
	}
	if len(claimed) == 0 {
		return 0, nil, nil
	}
	claimedSet := make(map[int64]bool, len(claimed))
	for _, seq := range claimed {
		claimedSet[seq] = true
	}
	evs := make([]keystone.WakeEvent, 0, len(claimed))
	for _, item := range batch {
		if claimedSet[item.seq] {
			evs = append(evs, item.ev)
		}
	}
	maxSeq := maxSeqOf(claimed)
	if s.shouldSkipByBackoff(key, maxSeq) {
		s.log.Info("驻场唤醒退避窗内，跳过同 seq 重复试跑", "session", session)
		s.completeWakeBatch(key, claimed)
		return 0, nil, nil
	}
	stopRenew := s.startWakeClaimRenewal(key, claimed)
	result, wakeErr := s.wakeResidentRound(ctx, r, sess, evs)
	stopRenew()
	if wakeErr != nil {
		s.log.Error("驻场者续轮失败", "session", session, "identity", identity, "event_count", len(evs), "cause", wakeErr)
		s.pointResidentFailure(session, identity, maxSeq, wakeErr)
		if admissionStalled(wakeErr) {
			// 可恢复排队态：不收尾、不标 seen，认领留在飞挡住水位，下一轮重读重试（卡批 B390 P2 同口径）。
			s.log.Warn("驻场者续轮准入满员，保留认领按节拍重试", "session", session, "max_seq", maxSeq)
			return 0, wakeErr, nil
		}
		s.completeWakeBatch(key, claimed)
		s.recordWakeBackoff(key, maxSeq)
		s.markSeen(claimed...)
		return 0, wakeErr, nil
	}
	s.residentPush.clear(session)
	s.completeWakeBatch(key, claimed)
	s.markSeen(claimed...)
	s.log.Info("驻场者续轮已完成", "session", session, "identity", identity, "event_count", len(evs),
		"push_session", result.SessionID)
	return len(claimed), nil, nil
}

// wakeResidentRound 跑一次 push 驻场者续轮（plan §3 P1：与 wakeCoordinatorRoundID 同骨架）：bearing →
// 冻结载体准入（小队 = 驻场身份 agent:<名> 的名字段）→ 规范化 spec → keystone.WakeResident → 释放名额。
// 准入失败包成 coordinatorAdmissionError（admissionStalled 据此判可恢复）。驻场回合不另注入席位 env
// 的特判：coordinatorRunner.Resume 注入的是驻场载体自己的 cli/会话对，驻场者按简报用 --agent 发言，
// 该对不对应任何卡席位，不会被当作席位身份落账。
func (s *Server) wakeResidentRound(ctx context.Context, r proto.SessionResident, sess proto.Session,
	evs []keystone.WakeEvent) (keystone.RoundResult, error) {
	var zero keystone.RoundResult
	kind, agent, err := proto.ParseMemberIdentity(r.Identity)
	if err != nil || kind != proto.IdentityKindAgent {
		return zero, fmt.Errorf("push 驻场者身份 %q 不是 agent:<小队名>", r.Identity)
	}
	if s.scheduling == nil || s.keystone == nil {
		return zero, errors.New("驻场者续轮：调度或 keystone 未装配")
	}
	binding, err := s.scheduling.AdmitSeatCarrier(agent, r.Carrier)
	if err != nil {
		return zero, &coordinatorAdmissionError{squad: agent, err: err}
	}
	defer s.releaseSchedulingBinding(residentClaimKey(r.Session), binding)
	spec, err := orchestration.NormalizeCoordinatorSpec(keysclient.SessionSpec{
		CLI: r.PushCLI, HomeDir: r.HomeDir, Model: r.Model, Workdir: r.Workdir,
	})
	if err != nil {
		s.log.Error("规范化驻场者 SessionSpec 失败", "session", r.Session, "carrier", r.Carrier, "cause", err)
		return zero, err
	}
	ref := keysclient.SessionRef{
		CLI: spec.CLI, SessionID: r.PushSession, HomeDir: spec.HomeDir, Workdir: spec.Workdir, Model: spec.Model,
	}
	s.log.Info("驻场者续轮开始", "session", r.Session, "identity", r.Identity, "squad", binding.Squad,
		"carrier", binding.Carrier, "cli", ref.CLI, "push_session", ref.SessionID, "event_count", len(evs))
	result, err := s.keystone.WakeResident(ctx, ref, keystone.ResidentBrief{
		Session: r.Session, Agent: agent, Title: sess.Title, Cards: sess.Cards,
	}, evs)
	if err != nil {
		return result, fmt.Errorf("驻场者续轮失败: %w", err)
	}
	s.log.Info("驻场者续轮结束", "session", r.Session, "push_session", result.SessionID, "ok", true)
	return result, nil
}

// pointResidentFailure 在会话里落一条系统指针行，说清谁、为什么、怎么重试（同一批至多一条）。
// 写失败只留 Warn：指针行是可见面，不覆盖唤醒结果本身。
func (s *Server) pointResidentFailure(session, identity string, maxSeq int64, cause error) {
	if !s.residentPush.shouldPoint(session, maxSeq) {
		s.log.Debug("驻场者失败指针行本批已落，不重复", "session", session, "max_seq", maxSeq)
		return
	}
	var body string
	if admissionStalled(cause) {
		body = fmt.Sprintf("驻场者 %s 暂未回话：所在小队名额已满，消息已排队，名额释放后自动重试（无需重发）。", identity)
	} else {
		body = fmt.Sprintf("驻场者 %s 本轮未能回话：%s。消息仍在会话里；排除载体故障后再发一条消息即重试"+
			"（载体状态可用 handoff squad list 查看）。", identity, truncateRunes(cause.Error(), 200))
	}
	if s.rooms == nil {
		s.log.Error("驻场者失败指针行未落：collab 服务未装配", "session", session, "identity", identity)
		return
	}
	seq, err := s.rooms.Pointer(session, proto.RoomMessage{Body: body})
	if err != nil {
		level := s.log.Warn
		if errors.Is(err, collab.ErrReadOnly) {
			level = s.log.Info
		}
		level("驻场者失败指针行写入失败", "session", session, "identity", identity, "cause", err)
		return
	}
	s.log.Info("驻场者失败指针行已落", "session", session, "identity", identity, "pointer_seq", seq, "max_seq", maxSeq)
}
