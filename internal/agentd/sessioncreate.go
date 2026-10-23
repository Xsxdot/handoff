// sessioncreate.go —— B444 P2 建会话编排（S1/S3/S4 服务端）与「叫驻场者」（plan Q4 甲）。
//
// 职责：把「项目内新建会话」这一次人类请求串成多领域写（spec 落位三问 ①：由 agentd 编排单元串起，不让 Web
// 逐步调）：校验小队（role=coordinator）→ 逐卡预校验（Q7：存在、未终态、项目相符、未属他会话）→ 按已接受
// 的卡算标题 → 建会话（含所属项目）→ 逐卡拉入 → SitResident(push，首轮在途) → 拉起驻场者首轮 → 补写 push 会话 id
// （B444 P2 返工 F1：先坐下，首轮里驻场者即是书写者）。
// 「叫驻场者」（launchSessionResident）是建会话与 POST /api/sessions/{id}/resident/launch 共用的同一函数。
//
// 边界：
//   - 首轮拉起与卡「叫机器人」共用 runOnCoordinatorCarrier（scheddrain.go，协调者裁决 2：抽共用、不复制）；
//     载体在远端时首轮转交载体机 agentd 执行（B448，firstround.go），驻场行由执行机写；
//   - 会话/拉卡/驻场写只经 collab 门面（s.rooms）；小队读与名额只经 scheduling；首轮只经 keystone——均为
//     既有依赖方向，collab 不因本卡新增对 scheduling/keystone 的依赖（spec「允许的新依赖方向」）；
//   - 同步（Q3 甲）：首轮跑完才返回；首轮失败不回滚会话与卡，响应带 resident.error，会话里落一条系统
//     指针行（谁、为什么、怎么重试）；
//   - 驻场行的载体机器必须是真实机器名（协调者裁决 R9）：载体登记为 local/本机/空 时换成本机配置名或
//     hostname，解析不到即拒坐（宁可首轮失败可见，也不写一个别的机器会误认领或本机认不出的名字）。
package agentd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/Xsxdot/handoff/internal/collab"
	"github.com/Xsxdot/handoff/internal/collab/client"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
	"github.com/google/uuid"
)

// 编排层的请求校验哨兵：一律 400、零写入。
var (
	// errSessionCreateInvalid 建会话请求不成形（缺项目、项目名非法等）。
	errSessionCreateInvalid = errors.New("建会话请求不成形")
	// errSquadNotFound 指定的小队未登记。
	errSquadNotFound = errors.New("小队未登记")
	// errSquadNotCoordinator 指定的小队角色不是协调者。
	errSquadNotCoordinator = errors.New("小队角色不是协调者")
)

// sessionCreateInput 是建会话编排的入参（HTTP 层已解析身份）。
type sessionCreateInput struct {
	Title   string   // 裁剪后的标题；空 = 自动命名
	Owner   string   // 群主（统一记法）
	Project string   // 所属项目（必填）
	Cards   []string // 待拉入的卡（可空）
	Squad   string   // 驻场小队名；空 = 唯一协调小队
	Actor   string   // 审计 actor
}

// residentSquadFor 解析驻场小队：name 空 → 唯一协调小队（resolveCoordinatorSquad 的 0/≥2 错误原样）；
// name 非空 → 必须已登记且 role=coordinator。错误一律包进 coordinatorLookupError（HTTP 400，指路文案
// 由 residentSquadMessage 给出）。
func (s *Server) residentSquadFor(name string) (scheduling.Squad, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		squad, err := s.resolveCoordinatorSquad()
		if err != nil {
			return scheduling.Squad{}, &coordinatorLookupError{err: err}
		}
		return squad, nil
	}
	rows, err := s.scheduling.SquadRows()
	if err != nil {
		return scheduling.Squad{}, fmt.Errorf("读小队登记: %w", err)
	}
	for _, row := range rows {
		if row.Squad.Name != name {
			continue
		}
		if row.Squad.Role != scheduling.RoleCoordinator {
			return scheduling.Squad{}, &coordinatorLookupError{err: fmt.Errorf("%w：%s 的角色是 %s",
				errSquadNotCoordinator, name, row.Squad.Role)}
		}
		return row.Squad, nil
	}
	return scheduling.Squad{}, &coordinatorLookupError{err: fmt.Errorf("%w：%s", errSquadNotFound, name)}
}

// residentSquadMessage 把小队识别错误翻成可行动文案（与卡「叫机器人」的 400 指路同口径）。
func residentSquadMessage(err error) string {
	switch {
	case errors.Is(err, errNoCoordinatorSquad):
		return "未登记协调者小队：先登记载体，再登记协调者小队（示例：handoff squad create --name coord --role coordinator --member coord-carrier）"
	case errors.Is(err, errAmbiguousCoordinatorSquad):
		return err.Error() + "：请显式指定驻场小队（squad）"
	case errors.Is(err, errSquadNotFound):
		return err.Error() + "（handoff squad list 查看已登记小队；或 handoff squad create 登记）"
	default:
		return err.Error()
	}
}

// screenSessionCards 逐卡预校验（Q7 甲；缺陷族 R6：逐卡收集、不整体回滚）。返回已接受的卡（请求序、去重）
// 与其标题、被拒的卡与点名冲突的原因。读账本失败（非「不存在」）上抛——不能把 I/O 故障说成「卡不存在」。
func (s *Server) screenSessionCards(project string, cards []string) (accepted []ledger.Card, rejected []proto.SessionRejectedCard, err error) {
	seen := map[string]bool{}
	for _, raw := range cards {
		id := strings.TrimSpace(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		card, err := s.ledger.GetCard(id)
		if errors.Is(err, ledger.ErrNotFound) {
			rejected = append(rejected, proto.SessionRejectedCard{Card: id, Reason: "卡不存在"})
			continue
		}
		if err != nil {
			return nil, nil, fmt.Errorf("预校验卡 %s: %w", id, err)
		}
		switch {
		case card.Status == ledger.StatusDone || card.Status == ledger.StatusClosed:
			rejected = append(rejected, proto.SessionRejectedCard{Card: id, Reason: "卡已终态（" + card.Status + "）"})
			continue
		case card.Project != project:
			rejected = append(rejected, proto.SessionRejectedCard{Card: id,
				Reason: fmt.Sprintf("卡属项目 %s，不是本会话的项目 %s", card.Project, project)})
			continue
		}
		owner, err := s.ledger.SessionOfCard(id)
		if err != nil {
			return nil, nil, fmt.Errorf("预校验卡 %s 的会话归属: %w", id, err)
		}
		if owner != "" {
			rejected = append(rejected, proto.SessionRejectedCard{Card: id, Reason: "卡已在会话 " + owner + " 里"})
			continue
		}
		accepted = append(accepted, card)
	}
	return accepted, rejected, nil
}

// autoSessionTitle 按已接受的卡算会话名（spec「自动命名规则」，协调者裁决 7）：1 张 → 卡标题；N>1 →
// 「<首张卡标题> 等 N 张」；零张 → proto.SessionUntitledTitle（由驻场者首轮 CAS 改名）。
func autoSessionTitle(accepted []ledger.Card) string {
	switch len(accepted) {
	case 0:
		return proto.SessionUntitledTitle
	case 1:
		return accepted[0].Title
	default:
		return fmt.Sprintf("%s 等 %d 张", accepted[0].Title, len(accepted))
	}
}

// createProjectSession 是建会话编排（plan §3 P2）。返回：校验失败 → 包 errSessionCreateInvalid /
// coordinatorLookupError 的错误（调用方 400，零写入）；建会话或读账本失败 → 其他错误（500）；
// 其余一律成功返回响应——拒卡进 RejectedCards，驻场者首轮失败进 Resident.Error（Q3 甲）。
func (s *Server) createProjectSession(ctx context.Context, in sessionCreateInput) (proto.SessionCreateResp, error) {
	var zero proto.SessionCreateResp
	if strings.TrimSpace(in.Project) == "" {
		return zero, fmt.Errorf("%w：缺所属项目 project（带 cards/squad 的建会话必须指定项目）", errSessionCreateInvalid)
	}
	squad, err := s.residentSquadFor(in.Squad)
	if err != nil {
		s.log.Warn("建会话被拒：驻场小队不可用", "project", in.Project, "squad", in.Squad, "cause", err)
		return zero, err
	}
	accepted, rejected, err := s.screenSessionCards(in.Project, in.Cards)
	if err != nil {
		s.log.Warn("建会话失败：卡预校验读账本出错", "project", in.Project, "cause", err)
		return zero, err
	}
	title := in.Title
	if title == "" {
		title = autoSessionTitle(accepted)
	}
	session, err := s.rooms.CreateSessionInProject(title, in.Owner, in.Project, in.Actor)
	if err != nil {
		if errors.Is(err, collab.ErrProjectName) {
			return zero, fmt.Errorf("%w：%v", errSessionCreateInvalid, err)
		}
		s.log.Warn("建会话失败：写会话", "project", in.Project, "cause", err)
		return zero, err
	}
	for _, card := range accepted {
		if err := s.rooms.JoinCard(session.ID, card.ID, in.Actor); err != nil {
			// 预校验到写入之间被别处抢走（唯一索引兜底）：逐卡记拒，不回滚已建会话。
			s.log.Warn("建会话拉卡失败，记为拒卡", "session", session.ID, "card", card.ID, "cause", err)
			rejected = append(rejected, proto.SessionRejectedCard{Card: card.ID, Reason: "拉入失败：" + err.Error()})
		}
	}
	outcome, launchErr := s.launchSessionResident(ctx, session.ID, squad)
	if launchErr != nil {
		outcome = proto.SessionResidentOutcome{Error: launchErr.Error()}
	}
	refreshed, err := s.autoLedger.GetSession(session.ID)
	if err != nil {
		// 会话已建好，只是回读失败：用写面返回值兜底，卡列表可能不全（下一次列表刷新即正确）。
		s.log.Warn("建会话后回读会话失败，响应用写面快照", "session", session.ID, "cause", err)
		refreshed = session
	}
	s.log.Info("项目内会话编排完成", "session", session.ID, "project", in.Project, "title", title,
		"accepted", len(accepted), "rejected", len(rejected), "resident", outcome.Identity,
		"resident_error", outcome.Error != "")
	return proto.SessionCreateResp{Session: refreshed, RejectedCards: rejected, Resident: &outcome}, nil
}

// launchSessionResident 为会话拉起 push 驻场者首轮并坐下（建会话与 resident/launch 共用，plan Q4 甲）。
// 顺序（B444 P2 返工 F1：先坐下、再拉起、后补写——首轮里驻场者已是书写者，能发言、能 CAS 改名）：
// 会话锁 → 读会话（不存在 ErrNoRoom、归档 ErrReadOnly）→ 已有驻场者（含首轮在途行）ErrResidentOccupied →
// runOnCoordinatorCarrier（准入 → 本机载体 → spec，workdir 按会话所属项目；载体在远端 → 转交载体机，B448）→
// 真实机器名（R9）→ collab.SitResident(push，push_session 空 = 首轮在途) → keystone.LaunchForResident →
// SetResidentPushSession。准入（含名额满、载体不可用）、首轮或补写失败 → 请下在途行（若已坐下，StandResident）
// + 会话里一条系统指针行；请求错（会话不存在/归档/已有驻场者）不落指针行（响应即可见面）。
func (s *Server) launchSessionResident(ctx context.Context, sessionID string, squad scheduling.Squad) (proto.SessionResidentOutcome, error) {
	outcome, _, err := s.launchSessionResidentOn(ctx, sessionID, squad.Name, "")
	return outcome, err
}

// launchSessionResidentOn 是 launchSessionResident 的本体，也是驻场者首轮转交收件端的执行路径（B448）。
//
// 参数：squad 是驻场小队名；frozenCarrier 空 = 受理端（LaunchAdmit 选载体，远端载体转交载体机）；非空 =
// 收件端（冻结准入，载体不在本机即防环拒绝）。返回：pointed 报告本次是否已在会话落了失败指针行（收件端据此
// 回执 pointed，F30）。指针归属（Q3 甲 / F31）：本机跑了骨架（含收件端）就由本机写；受理端转交失败时，拿到
// 回执 → 对端没写（pointed=false）且不是请求错才写；没拿到回执 → 重读驻场位，空才写（在途或已坐 = 指针归执行机）。
func (s *Server) launchSessionResidentOn(ctx context.Context, sessionID, squad, frozenCarrier string) (proto.SessionResidentOutcome, bool, error) {
	var zero proto.SessionResidentOutcome
	identity, err := proto.MemberIdentity(proto.IdentityKindAgent, squad)
	if err != nil {
		return zero, false, &coordinatorLookupError{err: fmt.Errorf("小队名 %q 不能作驻场身份 agent:<名>: %w", squad, err)}
	}
	lock := s.coordinatorLock(residentClaimKey(sessionID))
	lock.Lock()
	defer lock.Unlock()
	session, err := s.autoLedger.GetSession(sessionID)
	if errors.Is(err, client.ErrNotFound) {
		return zero, false, fmt.Errorf("会话 %s: %w", sessionID, collab.ErrNoRoom)
	}
	if err != nil {
		return zero, false, fmt.Errorf("读会话 %s: %w", sessionID, err)
	}
	if session.Archived {
		return zero, false, fmt.Errorf("会话 %s 已归档，不能叫驻场者: %w", sessionID, collab.ErrReadOnly)
	}
	if current, seated, err := s.autoLedger.SessionResident(sessionID); err != nil {
		return zero, false, fmt.Errorf("读会话 %s 驻场者: %w", sessionID, err)
	} else if seated {
		state := "已在座"
		if current.DriverMode == proto.ResidentDriverPush && current.PushSession == "" {
			state = "首轮在途"
		}
		return zero, false, fmt.Errorf("会话 %s 已有驻场者 %s（%s；换人先 handoff session stand）: %w",
			sessionID, current.Identity, state, collab.ErrResidentOccupied)
	}
	var sat proto.SessionResident
	var transferred *proto.SessionResidentOutcome
	_, err = s.runOnCoordinatorCarrier(coordinatorCarrierPlan{
		squad: squad, releaseKey: residentClaimKey(sessionID), frozenCarrier: frozenCarrier,
		attrs:   []any{"session", sessionID, "resident", identity},
		workdir: func() string { return s.resolveProjectWorkdir(session.HomeProject, "session", sessionID) },
		launch: func(round coordinatorCarrierRound) (keystone.RoundResult, error) {
			var zeroRound keystone.RoundResult
			machine, err := s.residentCarrierMachine(round.carrier.Machine)
			if err != nil {
				return zeroRound, err
			}
			sat, err = s.rooms.SitResident(proto.SessionResident{
				Session: sessionID, Identity: identity, DriverMode: proto.ResidentDriverPush,
				PushCLI: round.spec.CLI, Carrier: round.binding.Carrier, CarrierMachine: machine,
				HomeDir: round.spec.HomeDir, Workdir: round.spec.Workdir, Model: round.spec.Model,
			})
			if err != nil {
				s.log.Warn("驻场者首轮前坐下失败，不拉起", "session", sessionID, "identity", identity, "cause", err)
				return zeroRound, fmt.Errorf("驻场者坐下失败: %w", err)
			}
			s.log.Info("驻场者已坐下（首轮在途），开始拉起首轮", "session", sessionID, "identity", identity,
				"carrier", sat.Carrier, "carrier_machine", sat.CarrierMachine)
			roundID := uuid.NewString()
			if _, err := s.rooms.RecordResidentRound(sessionID, identity, roundID, "start", ""); err != nil {
				return zeroRound, fmt.Errorf("记录驻场者首轮开始事实: %w", err)
			}
			result, err := s.keystone.LaunchForResident(ctx, round.spec, keystone.ResidentBrief{
				Session: sessionID, Agent: squad, Title: session.Title, Cards: session.Cards,
			})
			switch {
			case err != nil:
				err = fmt.Errorf("拉起驻场者首轮失败: %w", err)
			case result.SessionID == "":
				err = fmt.Errorf("驻场者首轮未返回会话身份，无法续轮（session=%s）", sessionID)
			default:
				if fillErr := s.rooms.SetResidentPushSession(sessionID, identity, result.SessionID); fillErr != nil {
					err = fmt.Errorf("驻场者首轮已跑（会话 %s）但 push 会话 id 未补写: %w", result.SessionID, fillErr)
				}
			}
			if err != nil {
				if _, factErr := s.rooms.RecordResidentRound(sessionID, identity, roundID, "fail", err.Error()); factErr != nil {
					s.log.Error("首轮失败事实写入失败", "session", sessionID, "identity", identity, "round_id", roundID, "cause", factErr)
				}
				s.keystone.ForgetResident(sessionID)
				s.standInflightResident(sessionID, identity, err)
				return result, err
			}
			if _, factErr := s.rooms.RecordResidentRound(sessionID, identity, roundID, "end", ""); factErr != nil {
				// The actual round succeeded. Keep that result and leave the persisted
				// start as the last observed fact rather than inventing a failure.
				s.log.Error("首轮结束事实写入失败", "session", sessionID, "identity", identity, "round_id", roundID, "cause", factErr)
			}
			sat.PushSession = result.SessionID
			return result, nil
		},
		transfer: func(binding scheduling.Binding, carrier scheduling.Carrier) (keystone.RoundResult, error) {
			outcome, err := s.transferResidentFirstRound(ctx, sessionID, binding, carrier)
			if err != nil {
				return keystone.RoundResult{}, err
			}
			transferred = &outcome
			return keystone.RoundResult{Woke: true}, nil
		},
	})
	if err != nil {
		s.log.Error("叫驻场者失败", "session", sessionID, "squad", squad, "cause", err)
		pointed := false
		if s.residentFailureIsMine(sessionID, err) {
			pointed = s.pointResidentLaunchFailure(sessionID, identity, squad, err)
		}
		return zero, pointed, err
	}
	if transferred != nil {
		s.log.Info("驻场者已在载体机拉起并坐下", "session", sessionID, "identity", transferred.Identity,
			"carrier", transferred.Carrier, "carrier_machine", transferred.CarrierMachine)
		return *transferred, false, nil
	}
	s.log.Info("驻场者已拉起并坐下", "session", sessionID, "identity", identity, "carrier", sat.Carrier,
		"carrier_machine", sat.CarrierMachine, "push_session", sat.PushSession)
	return proto.SessionResidentOutcome{Identity: identity, Carrier: sat.Carrier, CarrierMachine: sat.CarrierMachine}, false, nil
}

// residentFailureIsMine 判定一次叫驻场者失败的指针行是否归本机写（Q3 甲 / F31）。不是转交失败 → 本机跑了
// 骨架，归本机。转交失败且拿到回执 → 对端已写（pointed）或这是请求错（会话不存在/归档/已有驻场者，B444
// 「请求错不落指针行」）则不归本机。没拿到回执 → 分不清对端执行了没有：重读驻场位，空 = 没送到或已请下，
// 归本机；非空（在途或已坐）= 指针归执行机；重读失败按归本机（宁可多一条也不让失败无痕）。
func (s *Server) residentFailureIsMine(sessionID string, err error) bool {
	var transferErr *firstRoundTransferError
	if !errors.As(err, &transferErr) {
		return true
	}
	if transferErr.received {
		switch transferErr.kind {
		case proto.FirstRoundKindResidentOccupied, proto.FirstRoundKindReadOnly, proto.FirstRoundKindNoRoom:
			return false
		}
		return !transferErr.pointed
	}
	_, seated, readErr := s.autoLedger.SessionResident(sessionID)
	if readErr != nil {
		s.log.Warn("转交无回执后重读驻场位失败，按本机补指针行", "session", sessionID,
			"machine", transferErr.machine, "cause", readErr)
		return true
	}
	if seated {
		s.log.Info("转交无回执但驻场位非空，指针归执行机", "session", sessionID, "machine", transferErr.machine)
		return false
	}
	return true
}

// standInflightResident 在首轮失败后请下首轮在途的驻场行（驻场位回到空，可经 resident/launch 重试）。
// 请下失败只留 Error：在途行会让人话一直暂缓不续轮，须人工 `handoff session stand <会话> --as <身份>`。
func (s *Server) standInflightResident(sessionID, identity string, cause error) {
	if err := s.rooms.StandResident(sessionID, identity); err != nil {
		s.log.Error("首轮失败后请下在途驻场行失败，需人工 session stand", "session", sessionID, "identity", identity,
			"launch_cause", cause, "cause", err)
		return
	}
	s.log.Info("首轮失败，已请下在途驻场行", "session", sessionID, "identity", identity, "launch_cause", cause)
}

// residentCarrierMachine 返回写进驻场行的真实机器名（协调者裁决 R9）：载体登记名不是本机别名就原样用；
// 是别名（空/local/本机）则取本机配置名，其次 hostname——P1 唤醒循环据 IsLocalMachine/IsSelfTarget 认领，
// 两者都认它；别的机器不会把它当本机。两者都取不到 → 错误（拒坐，不写别名）。
func (s *Server) residentCarrierMachine(registered string) (string, error) {
	name := strings.TrimSpace(registered)
	if name != "" && name != "local" && name != "本机" {
		return name, nil
	}
	if cfg := s.conf(); cfg != nil && strings.TrimSpace(cfg.Name) != "" {
		return strings.TrimSpace(cfg.Name), nil
	}
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("无法确定载体所在机器的真实名字（登记为 %q，本机未配置 name 且读不到 hostname: %v）", registered, err)
	}
	return host, nil
}

// errResidentRoundInterrupted 是启动清扫写进指针行的原因（首轮回合由拉起它的 agentd 进程执行，进程重启后
// 首轮结果丢失，push_session 永远不会补写）。
var errResidentRoundInterrupted = errors.New("首轮被 agentd 重启打断（重启前在途的首轮不会再补写会话 id）")

// sweepInterruptedResidents 是 agentd 启动清扫（B444 P2，协调者裁决：agentd 崩溃留下的首轮在途行）：列出 push
// 且 push_session 空的驻场行，只处置载体机器是**本机**的（R9：别机的行归别机，绝不碰）——逐行请下并落一条
// 「首轮被重启打断」指针行（带 `handoff session resident launch` 重试命令）。每行一条 Info、末尾一条汇总；
// 任何错误只记带上下文的日志，绝不阻断启动。
//
// 与本进程内真在跑的首轮不冲突：launchSessionResident 全程持会话的 coordinatorLock；本函数对每行 TryLock，
// 拿不到 = 本进程正在拉起它（真在途），跳过；拿到后重读一次驻场行，确认仍是同身份的在途行才请下。
func (s *Server) sweepInterruptedResidents() {
	if s.rooms == nil || s.autoLedger == nil {
		s.log.Warn("驻场者启动清扫跳过：collab 服务或账本门面未装配", "has_rooms", s.rooms != nil,
			"has_ledger", s.autoLedger != nil)
		return
	}
	rows, err := s.rooms.InflightPushResidents()
	if err != nil {
		s.log.Error("驻场者启动清扫失败：列首轮在途行出错（不阻断启动）", "cause", err)
		return
	}
	stood, foreign, skipped := 0, 0, 0
	for _, r := range rows {
		if !scheduling.IsLocalMachine(r.CarrierMachine) && !s.IsSelfTarget(r.CarrierMachine) {
			foreign++
			s.log.Info("驻场者启动清扫：在途行属别机，不碰", "session", r.Session, "identity", r.Identity,
				"carrier_machine", r.CarrierMachine)
			continue
		}
		if s.standInterruptedResident(r) {
			stood++
		} else {
			skipped++
		}
	}
	s.log.Info("驻场者启动清扫完成", "inflight_rows", len(rows), "stood", stood, "foreign", foreign, "skipped", skipped)
}

// standInterruptedResident 处置一行本机首轮在途驻场行：TryLock → 重读确认 → 请下 → 指针行。返回是否已请下。
func (s *Server) standInterruptedResident(r proto.SessionResident) bool {
	lock := s.coordinatorLock(residentClaimKey(r.Session))
	if !lock.TryLock() {
		s.log.Info("驻场者启动清扫：本进程正在拉起该会话的驻场者，跳过", "session", r.Session, "identity", r.Identity)
		return false
	}
	defer lock.Unlock()
	current, ok, err := s.autoLedger.SessionResident(r.Session)
	if err != nil {
		s.log.Error("驻场者启动清扫：重读驻场行失败，跳过", "session", r.Session, "identity", r.Identity, "cause", err)
		return false
	}
	if !ok || current.Identity != r.Identity || current.DriverMode != proto.ResidentDriverPush || current.PushSession != "" {
		s.log.Info("驻场者启动清扫：驻场行已变化，跳过", "session", r.Session, "identity", r.Identity, "resident_present", ok)
		return false
	}
	if err := s.rooms.StandResident(r.Session, r.Identity); err != nil {
		s.log.Error("驻场者启动清扫：请下在途行失败，需人工 session stand", "session", r.Session, "identity", r.Identity,
			"cause", err)
		return false
	}
	if s.keystone != nil {
		s.keystone.ForgetResident(r.Session)
	}
	squad := strings.TrimPrefix(r.Identity, "agent:")
	s.pointResidentLaunchFailure(r.Session, r.Identity, squad, errResidentRoundInterrupted)
	s.log.Info("驻场者启动清扫：已请下被重启打断的在途行", "session", r.Session, "identity", r.Identity,
		"carrier", r.Carrier, "carrier_machine", r.CarrierMachine)
	return true
}

// pointResidentLaunchFailure 在会话里落一条系统指针行：谁没叫起来、为什么、怎么重试。写失败只留 Warn。
// 返回是否已落（B448 收件端据此回执 pointed）。
func (s *Server) pointResidentLaunchFailure(sessionID, identity, squad string, cause error) bool {
	body := fmt.Sprintf("驻场者 %s 未能拉起：%s。会话与卡已保留；排除故障后重试：handoff session resident launch %s --squad %s",
		identity, truncateRunes(cause.Error(), 200), sessionID, squad)
	seq, err := s.rooms.Pointer(sessionID, proto.RoomMessage{Body: body})
	if err != nil {
		s.log.Warn("叫驻场者失败指针行写入失败", "session", sessionID, "identity", identity, "cause", err)
		return false
	}
	s.log.Info("叫驻场者失败指针行已落", "session", sessionID, "identity", identity, "pointer_seq", seq)
	return true
}
