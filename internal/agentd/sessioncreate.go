// sessioncreate.go —— B444 P2 建会话编排（S1/S3/S4 服务端）与「叫驻场者」（plan Q4 甲）。
//
// 职责：把「项目内新建会话」这一次人类请求串成多领域写（spec 落位三问 ①：由 agentd 编排单元串起，不让 Web
// 逐步调）：校验小队（role=coordinator）→ 逐卡预校验（Q7：存在、未终态、项目相符、未属他会话）→ 按已接受
// 的卡算标题 → 建会话（含所属项目）→ 逐卡拉入 → 拉起驻场者首轮 → SitResident(push)。
// 「叫驻场者」（launchSessionResident）是建会话与 POST /api/sessions/{id}/resident/launch 共用的同一函数。
//
// 边界：
//   - 首轮拉起与卡「叫机器人」共用 runOnCoordinatorCarrier（scheddrain.go，协调者裁决 2：抽共用、不复制）；
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
// 顺序：会话锁 → 读会话（不存在 ErrNoRoom、归档 ErrReadOnly）→ 已有驻场者 ErrResidentOccupied →
// runOnCoordinatorCarrier（准入 → 本机载体 → spec，workdir 按会话所属项目）→ keystone.LaunchForResident →
// 真实机器名（R9）→ collab.SitResident(push)。任一步在「进入名额窗口之后」失败，会话里落一条系统指针行；
// 之前的失败（会话不存在/归档/已有驻场者）不落（那是调用方的请求错，响应即可见面）。
func (s *Server) launchSessionResident(ctx context.Context, sessionID string, squad scheduling.Squad) (proto.SessionResidentOutcome, error) {
	var zero proto.SessionResidentOutcome
	identity, err := proto.MemberIdentity(proto.IdentityKindAgent, squad.Name)
	if err != nil {
		return zero, &coordinatorLookupError{err: fmt.Errorf("小队名 %q 不能作驻场身份 agent:<名>: %w", squad.Name, err)}
	}
	lock := s.coordinatorLock(residentClaimKey(sessionID))
	lock.Lock()
	defer lock.Unlock()
	session, err := s.autoLedger.GetSession(sessionID)
	if errors.Is(err, client.ErrNotFound) {
		return zero, fmt.Errorf("会话 %s: %w", sessionID, collab.ErrNoRoom)
	}
	if err != nil {
		return zero, fmt.Errorf("读会话 %s: %w", sessionID, err)
	}
	if session.Archived {
		return zero, fmt.Errorf("会话 %s 已归档，不能叫驻场者: %w", sessionID, collab.ErrReadOnly)
	}
	if current, seated, err := s.autoLedger.SessionResident(sessionID); err != nil {
		return zero, fmt.Errorf("读会话 %s 驻场者: %w", sessionID, err)
	} else if seated {
		return zero, fmt.Errorf("会话 %s 已有驻场者 %s（换人先 handoff session stand）: %w",
			sessionID, current.Identity, collab.ErrResidentOccupied)
	}
	var sat proto.SessionResident
	_, err = s.runOnCoordinatorCarrier(squad.Name, residentClaimKey(sessionID),
		[]any{"session", sessionID, "resident", identity},
		func() string { return s.resolveProjectWorkdir(session.HomeProject, "session", sessionID) },
		func(round coordinatorCarrierRound) (keystone.RoundResult, error) {
			result, err := s.keystone.LaunchForResident(ctx, round.spec, keystone.ResidentBrief{
				Session: sessionID, Agent: squad.Name, Title: session.Title, Cards: session.Cards,
			})
			if err != nil {
				return result, fmt.Errorf("拉起驻场者首轮失败: %w", err)
			}
			if result.SessionID == "" {
				return result, fmt.Errorf("驻场者首轮未返回会话身份，无法续轮（session=%s）", sessionID)
			}
			machine, err := s.residentCarrierMachine(round.carrier.Machine)
			if err != nil {
				s.keystone.ForgetResident(sessionID)
				return result, err
			}
			sat, err = s.rooms.SitResident(proto.SessionResident{
				Session: sessionID, Identity: identity, DriverMode: proto.ResidentDriverPush,
				PushCLI: round.spec.CLI, PushSession: result.SessionID,
				Carrier: round.binding.Carrier, CarrierMachine: machine,
				HomeDir: round.spec.HomeDir, Workdir: round.spec.Workdir, Model: round.spec.Model,
			})
			if err != nil {
				s.keystone.ForgetResident(sessionID)
				s.log.Error("驻场者首轮已跑但驻场行未落", "session", sessionID, "push_session", result.SessionID, "cause", err)
				return result, fmt.Errorf("驻场者首轮已跑（会话 %s）但驻场行未落: %w", result.SessionID, err)
			}
			return result, nil
		})
	if err != nil {
		s.log.Error("叫驻场者失败", "session", sessionID, "squad", squad.Name, "cause", err)
		s.pointResidentLaunchFailure(sessionID, identity, squad.Name, err)
		return zero, err
	}
	s.log.Info("驻场者已拉起并坐下", "session", sessionID, "identity", identity, "carrier", sat.Carrier,
		"carrier_machine", sat.CarrierMachine, "push_session", sat.PushSession)
	return proto.SessionResidentOutcome{Identity: identity, Carrier: sat.Carrier, CarrierMachine: sat.CarrierMachine}, nil
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

// pointResidentLaunchFailure 在会话里落一条系统指针行：谁没叫起来、为什么、怎么重试。写失败只留 Warn。
func (s *Server) pointResidentLaunchFailure(sessionID, identity, squad string, cause error) {
	body := fmt.Sprintf("驻场者 %s 未能拉起：%s。会话与卡已保留；排除故障后重试：handoff session resident launch %s --squad %s",
		identity, truncateRunes(cause.Error(), 200), sessionID, squad)
	seq, err := s.rooms.Pointer(sessionID, proto.RoomMessage{Body: body})
	if err != nil {
		s.log.Warn("叫驻场者失败指针行写入失败", "session", sessionID, "identity", identity, "cause", err)
		return
	}
	s.log.Info("叫驻场者失败指针行已落", "session", sessionID, "identity", identity, "pointer_seq", seq)
}
