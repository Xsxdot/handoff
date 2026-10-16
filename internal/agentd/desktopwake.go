// desktopwake.go —— B434 Claude 桌面端会话当协调者：控制面侧（Ticket 0 空壳）。
//
// 职责：
//   - 拉起材料：按卡产出「项目在本机的根目录 + 人尺度简报」（深链、复制兜底、
//     拉起器来信共用一份）；
//   - 唤醒信箱：把唤醒事件投影成一封信原子写进本机信箱，再对桌面端会话发
//     claude://code/continue 深链；席位不在本机时沿 B389 转交端点
//     （transferCoordinatorWake → POST /api/cards/{id}/coordinator/wake）交给那台
//     机器的 agentd，由它在本机走同一分支；
//   - 拉起器代开：给项目拉起器写一封装着拉起材料的信并 continue 唤醒它。
//
// 边界：写信箱与发深链是 gateway 对本机的出站 I/O（d_gateway），不进 d_execution，
// 不经 keystone（keystone.Wake / LaunchForCard 对桌面端席位零调用）。本机唤醒主缝
// （wakeDesktopSeat 本机分支、desktopLetter 其它事件段、writeDesktopLetter、
// claudeContinueURL、围栏渲染层）由 B434.4 落码；其余函数仍是空壳，行为在
// G1/G2/G3 落码（TODO(B434)）。契约见 docs/superpowers/specs/b434-contract.md §3.5–§3.8。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/collab/room"
	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/Xsxdot/handoff/internal/scheduling"
)

// errNoLocalProject 表示卡所属项目在本 agentd 没有登记位置；HTTP 映射 409，
// 错误文本逐字交给前端作禁用原因。
var errNoLocalProject = errors.New("项目在本机没有登记位置")

// errNoDesktopLauncher 表示项目没有登记拉起器；同时落 needs_human。
var errNoDesktopLauncher = errors.New(desktopNoLauncherReason)

// desktopNoLauncherReason 是无拉起器时 needs_human 的 reason 正文（spec 原文）。
const desktopNoLauncherReason = "无拉起器，请在桌面端开一场并登记"

// desktopWakeFailReasonPrefix 是桌面端唤醒投递失败时 needs_human reason 的固定前缀。
const desktopWakeFailReasonPrefix = "桌面端协调者唤醒失败："

// desktopBriefInput 是简报模板的全部输入；字段全部来自控制面账本/项目位置。
type desktopBriefInput struct {
	Card       string // 卡号
	Title      string // 卡标题
	BaseBranch string // 有效基线分支（EffectiveBaseBranch）
	Session    string // 卡所在 IM 会话 id（SessionOfCard，空串 = 不属任何会话）
	Owner      string // 所在会话群主身份（Session.Owner 原值，统一记法 user:<name>/agent:<name>）；未入群为空
	Folder     string // 项目根目录（本机）
	Via        string // proto.SeatDesktopVia*：写进 bind 命令行的 --via
}

// desktopOpener 把一条 claude:// 深链交给本机桌面端（生产为 macOS `open`）。
// 测试注入 spy；nil 视为「本机不能发深链」。
type desktopOpener func(url string) error

// desktopLaunchMaterial 按卡产出拉起材料（契约 §3.5）：Folder = 本机项目根目录
// （resolveLocalProjectRoot，与 resolveCoordWorkdir 同一解析点）、Brief = desktopBrief
// 渲染的人尺度简报、Launcher = 项目已登记拉起器的桌面端会话 id。
//
// 参数：card 卡号；via 写进简报 bind 命令行的 --via，只接受 proto.SeatDesktopViaPage
// （GET 材料端点）与 proto.SeatDesktopViaLauncher（renderLauncherLetter 复用，契约 §3.9.1）。
// 返回：卡不存在 → 包 ledger.ErrNotFound；项目无本机位置 → 包 errNoLocalProject；
// via 非法或其它读失败 → 包装后返回（错误文本已带卡号与原因）。读拉起器登记失败**不**
// 返回错误：记 Warn 后按未登记处理（Launcher 留空、JSON 不出键，契约条 202）。
// 注意：只读——不写账本、不打标、不碰进程内计数（条 14）；不经 keystone/scheduling
// （条 13、162）。日志：本函数只打契约条 202 要求的那条降级 Warn（它是该降级唯一的
// 痕迹，调用方拿不到）；成败日志由调用方边界层记一条（GET 为 handleDesktopLaunchMaterial），
// 避免 5s 轮询下一次请求多行重复。
func (s *Server) desktopLaunchMaterial(card, via string) (proto.DesktopLaunchMaterial, error) {
	if via != proto.SeatDesktopViaPage && via != proto.SeatDesktopViaLauncher {
		err := fmt.Errorf("拉起材料的 via 只能是 %q 或 %q，收到 %q",
			proto.SeatDesktopViaPage, proto.SeatDesktopViaLauncher, via)
		return proto.DesktopLaunchMaterial{}, err
	}
	c, err := s.ledger.GetCard(card)
	if err != nil {
		return proto.DesktopLaunchMaterial{}, err
	}
	folder, err := s.resolveLocalProjectRoot(c)
	if err != nil {
		return proto.DesktopLaunchMaterial{}, err
	}
	base, err := s.ledger.EffectiveBaseBranch(card)
	if err != nil {
		return proto.DesktopLaunchMaterial{}, fmt.Errorf("读卡 %s 有效基线: %w", card, err)
	}
	session, err := s.ledger.SessionOfCard(card)
	if err != nil {
		return proto.DesktopLaunchMaterial{}, fmt.Errorf("读卡 %s 所在会话: %w", card, err)
	}
	owner := ""
	if session != "" {
		sess, err := s.ledger.GetSession(session)
		if err != nil {
			return proto.DesktopLaunchMaterial{}, fmt.Errorf("读会话 %s 群主: %w", session, err)
		}
		owner = sess.Owner
	}
	m := proto.DesktopLaunchMaterial{
		Card:   card,
		Folder: folder,
		Brief: desktopBrief(desktopBriefInput{Card: card, Title: c.Title, BaseBranch: base,
			Session: session, Owner: owner, Folder: folder, Via: via}),
	}
	launcher, ok, err := s.ledger.DesktopLauncherOf(c.Project)
	switch {
	case err != nil:
		// 有意降级（契约条 202）：材料其余字段照给，「让拉起器代开」不出现；Warn 让它不静默。
		s.log.Warn("拉起材料读拉起器登记失败，按未登记处理", "card", card, "project", c.Project, "cause", err)
	case ok:
		m.Launcher = launcher.DesktopSession
	}
	return m, nil
}

// desktopBrief 渲染人尺度简报（契约 §3.5 模板逐行，r10 第 2 条修订第 3 行）：以
// proto.DesktopWakeMarker 开头，含卡号/标题/有效基线（空 →「未设」）/所在会话
// （空 →「未入群」）/项目根目录与三步指引；第 3 步末句三分支——入群且有群主
// →「需要人就 `@<群主>`」，入群但群主为空 →「需要人就打 needs_human」，未入群
// →「需要人就在卡上打 needs_human」；不含「一次性回合」。
// 纯函数：不读任何状态、不打日志（日志只在边界层记一条，见 desktopLaunchMaterial），
// 同输入同输出（G3 拉起器来信经 desktopLaunchMaterial 复用它）。
func desktopBrief(in desktopBriefInput) string {
	base := in.BaseBranch
	if base == "" {
		base = "未设"
	}
	session := in.Session
	if session == "" {
		session = "未入群"
	}
	// 群主已是统一记法 user:<name>/agent:<name>，可寻址写法是 "@"+群主原值（模板旧写
	// 「@user:<群主>」会渲染成 @user:user:sy，r10 已改）。没有可 @ 的升级终点时改为打标。
	escalate := "需要人就在卡上打 needs_human"
	switch {
	case in.Session != "" && in.Owner != "":
		escalate = "需要人就 `@" + in.Owner + "`"
	case in.Session != "":
		escalate = "需要人就打 needs_human"
	}
	lines := []string{
		proto.DesktopWakeMarker + " 你是卡 " + in.Card + " 的协调者，坐在这场 Claude 桌面端会话里。",
		"",
		"- 卡号：" + in.Card,
		"- 标题：" + in.Title,
		"- 有效基线分支：" + base,
		"- 所在会话：" + session,
		"- 项目根目录：" + in.Folder,
		"",
		"请按三步开始：",
		"1. 读 handoff skill。",
		"2. 用桌面端会话管理工具读到你自己的桌面端会话 id（" + proto.DesktopSessionPrefix + " 开头），然后执行：",
		"   handoff card bind " + in.Card + " --desktop-session <你的桌面端会话 id> --via " + in.Via,
		"3. 之后有事 handoff 会叫醒你，不必挂订阅；推卡按 charter 节点逐节点点火，升级按三档，" + escalate + "。",
	}
	return strings.Join(lines, "\n")
}

// handleDesktopLaunchMaterial 是 GET /api/cards/{id}/coordinator/desktop-launch
// （契约 §3.9）：返回 via=page 的拉起材料。注册时只套 withLedger、不套
// withCoordinator（条 13）。
// 响应：200 DesktopLaunchMaterial；404 卡不存在（ledgerErr）；409 项目无本机位置
// （{"error": <errNoLocalProject 包装文本>}，前端逐字显示为禁用原因）；其余读失败经
// ledgerErr（多为 500）。只读：不写账本、不打标、不动进程内计数（条 14）。
// 日志：控制台以 5s 节拍轮询本端点，每次请求只在这里记一条——200 一条 Info，
// 失败一条 Warn（cause 即下层错误，已带原因）；下层不再各记一遍。
func (s *Server) handleDesktopLaunchMaterial(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	m, err := s.desktopLaunchMaterial(id, proto.SeatDesktopViaPage)
	switch {
	case err == nil:
		s.log.Info("桌面端拉起材料已产出", "card", id, "via", proto.SeatDesktopViaPage,
			"has_launcher", m.Launcher != "")
		writeJSON(w, http.StatusOK, m)
	case errors.Is(err, errNoLocalProject):
		s.log.Warn("桌面端拉起材料不可用：项目在本机无登记位置", "card", id, "cause", err)
		writeErr(w, http.StatusConflict, err)
	default:
		s.log.Warn("桌面端拉起材料不可用", "card", id, "cause", err)
		ledgerErr(w, err)
	}
}

// desktopLetter 把一批唤醒事件投影成一封来信（契约 §3.6 模板 r7）：只负责取数并组装
// desktopLetterSection，第三方原文一律放进 desktopQuote，交 renderDesktopLetter 渲染
// 围栏与作者名册（P9=丁，条 263–275）。
//
// 参数：card 被唤醒席位所在卡（resolveWakeCard 的目标，可能是事件卡的祖先）；seat 席位
// 身份（读未读数用，与 session wait 的 member 同值）；evs 本批唤醒摘要（不读 Summary——
// 它被截到 400 字）；raws 本批账本原始事件（按 seq 升序，内容的唯一来源）。
// 返回：来信全文；raws 为空、读引用/未读投影失败、工单信封解不开 → 错误（宁可不发信，
// 不发残信，条 80）。
// 段落（契约 §3.6 表）：room_message → 会话消息段（命中条 + 可解析的引用条 + 未读数）；
// task_mirrored 的 permission_request/question → 工单段；needs_human → 需要人段；其余
// （decision_*、task 终态等）→ 「<事件类型>」段。事件卡 ≠ 来信卡（子卡冒泡，条 87）时
// 段标题追加「（卡 <事件卡号>）」（契约 r10 第 9 条）；尾行「；会话消息：…」只在本批含
// 会话消息时出现（r10 第 8 条）。
func (s *Server) desktopLetter(card, seat string, evs []keystone.WakeEvent, raws []proto.LedgerEvent) (string, error) {
	if len(raws) == 0 {
		return "", fmt.Errorf("卡 %s 本批唤醒没有账本原始事件，无从组装来信", card)
	}
	a := &desktopLetterAssembler{s: s, card: card, seat: seat}
	sections := make([]desktopLetterSection, 0, len(raws))
	minSeq, maxSeq := raws[0].Seq, raws[0].Seq
	for _, ev := range raws {
		if ev.Seq < minSeq {
			minSeq = ev.Seq
		}
		if ev.Seq > maxSeq {
			maxSeq = ev.Seq
		}
		sec, err := a.section(ev)
		if err != nil {
			s.log.Warn("来信组装失败，本批不写信", "card", card, "seq", ev.Seq, "type", ev.Type, "cause", err)
			return "", err
		}
		sections = append(sections, sec)
	}
	head := fmt.Sprintf("%s 卡 %s 有 %d 件新事（账本 seq %d–%d）", proto.DesktopWakeMarker, card, len(raws), minSeq, maxSeq)
	letter := renderDesktopLetter(head, sections, desktopLetterTail(card, a.rooms))
	s.log.Debug("桌面端来信已组装", "card", card, "seat", seat,
		"event_count", len(raws), "wake_count", len(evs), "sessions", len(a.rooms), "bytes", len(letter))
	return letter, nil
}

// desktopLetterSessionReadCmd 是来信尾行「会话消息」分句的命令前缀（契约 §3.6 模板、
// r10 第 8 条，r12 第 1 条改字）：CLI 没有 `handoff session read` 子命令，读会话消息的
// 既有命令是 `handoff room read <room>`（cmd/room.go），会话 id 可直接作 room。改字只动
// 这一处常量与 TestB434DesktopLetterTailSessionClause、V 金样本的 tail 字串。
const desktopLetterSessionReadCmd = "handoff room read "

// desktopLetterTail 渲染来信尾行（契约 §3.6，r10 第 8 条）：rooms 为空时省去整段
// 「；会话消息：…」分句；多个会话按首次出现顺序、每个会话一条完整命令，以「、」连接。
func desktopLetterTail(card string, rooms []string) string {
	tail := "按分诊表处置；读全文：handoff card show " + card
	if len(rooms) > 0 {
		cmds := make([]string, len(rooms))
		for i, room := range rooms {
			cmds[i] = desktopLetterSessionReadCmd + room
		}
		tail += "；会话消息：" + strings.Join(cmds, desktopAuthorRosterSep)
	}
	return tail + "。处置完不必挂订阅，有事会再叫醒你。"
}

// desktopLetterAssembler 组装一封来信的各段，持有本信级状态：未读投影一封信只读一次
// （ListSessions 扫全量会话与卡，逐条读会把一批 N 条会话消息放大成 N 次全量扫描）；
// rooms 记本信出现过的会话 id（首次出现顺序，尾行用）。
type desktopLetterAssembler struct {
	s      *Server
	card   string
	seat   string
	unread map[string]int // nil = 尚未读未读投影
	rooms  []string
}

// section 按事件类型分派到四种段（契约 §3.6 表）。
func (a *desktopLetterAssembler) section(ev proto.LedgerEvent) (desktopLetterSection, error) {
	switch ev.Type {
	case ledger.EvRoomMessage:
		return a.roomMessage(ev)
	case ledger.EvTaskMirrored:
		return a.taskMirrored(ev)
	case ledger.EvNeedsHuman:
		return a.needsHuman(ev), nil
	default:
		return a.other(ev), nil
	}
}

// heading 给段标题骨架追加冒泡卡号：事件卡非空且不是来信卡时加「（卡 <事件卡号>）」
// （契约 r10 第 9 条）；段标题从不含 actor（条 275）。
func (a *desktopLetterAssembler) heading(base string, ev proto.LedgerEvent) string {
	if ev.CardID != "" && ev.CardID != a.card {
		return base + "（卡 " + ev.CardID + "）"
	}
	return base
}

// roomMessage 渲染会话消息段（条 74–78）：命中条正文全文进 room_message 围栏；
// ReplyTo 可解析时追加「引用 seq <n>：」骨架 + 引用条围栏；末行未读数骨架。数据来源与
// cmd/session.go#buildSessionWake 同源（MessageReference、ListSessions(seat).Unread）。
func (a *desktopLetterAssembler) roomMessage(ev proto.LedgerEvent) (desktopLetterSection, error) {
	var msg proto.RoomMessage
	if err := room.UnmarshalMessage(ev.Payload, &msg); err != nil {
		return desktopLetterSection{}, fmt.Errorf("卡 %s 来信解码会话消息 seq %d: %w", a.card, ev.Seq, err)
	}
	if a.s.rooms == nil {
		return desktopLetterSection{}, fmt.Errorf("卡 %s 来信读会话消息 seq %d：collab 服务未装配，读不到引用与未读", a.card, ev.Seq)
	}
	parts := []desktopLetterPart{{Quote: &desktopQuote{
		Author: ev.Actor, Seq: ev.Seq, Kind: desktopQuoteKindRoomMessage, Body: msg.Body,
	}}}
	ref, err := a.s.rooms.MessageReference(msg)
	if err != nil {
		return desktopLetterSection{}, fmt.Errorf("卡 %s 来信读会话消息 seq %d 的引用条: %w", a.card, ev.Seq, err)
	}
	if ref != nil {
		parts = append(parts,
			desktopLetterPart{Skeleton: fmt.Sprintf("引用 seq %d：", ref.Seq)},
			desktopLetterPart{Quote: &desktopQuote{
				Author: ref.Actor, Seq: ref.Seq, Kind: desktopQuoteKindRoomMessage, Body: ref.Body,
			}})
	}
	unread, err := a.unreadOf(msg.Room)
	if err != nil {
		return desktopLetterSection{}, fmt.Errorf("卡 %s 来信读会话 %s 未读投影: %w", a.card, msg.Room, err)
	}
	parts = append(parts, desktopLetterPart{Skeleton: fmt.Sprintf("会话 %s 里你还有 %d 条未读（含本条）。", msg.Room, unread)})
	if !slices.Contains(a.rooms, msg.Room) {
		a.rooms = append(a.rooms, msg.Room)
	}
	return desktopLetterSection{
		Heading: a.heading(fmt.Sprintf("## 会话消息 seq %d（会话 %s）", ev.Seq, msg.Room), ev),
		Parts:   parts,
	}, nil
}

// unreadOf 返回席位在会话 room 的未读数（含命中条）：首次调用读一次 ListSessions(seat)
// 并缓存；会话不在投影里按 0 计（与 buildSessionWake 同款）。
func (a *desktopLetterAssembler) unreadOf(room string) (int, error) {
	if a.unread == nil {
		summaries, err := a.s.rooms.ListSessions(a.seat)
		if err != nil {
			return 0, err
		}
		a.unread = make(map[string]int, len(summaries))
		for _, summary := range summaries {
			a.unread[summary.ID] = summary.Unread
		}
	}
	return a.unread[room], nil
}

// taskMirrored 渲染工单段（条 79）：permission_request/question 的信封内层载荷原文全文
// 进 ticket 围栏，段标题带 task_type；其它 task_type（task 终态）按「其它事件」段处理。
func (a *desktopLetterAssembler) taskMirrored(ev proto.LedgerEvent) (desktopLetterSection, error) {
	taskType, payload, err := mirroredTaskTypeAndPayload(ev)
	if err != nil {
		return desktopLetterSection{}, fmt.Errorf("卡 %s 来信解工单信封 seq %d: %w", a.card, ev.Seq, err)
	}
	if proto.EventType(taskType) != proto.EventTypePermissionRequest && proto.EventType(taskType) != proto.EventTypeQuestion {
		return a.other(ev), nil
	}
	return desktopLetterSection{
		Heading: a.heading(fmt.Sprintf("## 工单 seq %d（%s）", ev.Seq, taskType), ev),
		Parts: []desktopLetterPart{{Quote: &desktopQuote{
			Author: ev.Actor, Seq: ev.Seq, Kind: desktopQuoteKindTicket, Body: string(payload),
		}}},
	}, nil
}

// needsHuman 渲染需要人段（条 191、269）：reason 全文进 needs_human 围栏，author 为
// 打标者 actor。载荷解不出 reason 时退回载荷原文并 Warn（一条坏载荷不毒死唤醒链）。
func (a *desktopLetterAssembler) needsHuman(ev proto.LedgerEvent) desktopLetterSection {
	reason, ok := desktopNeedsHumanReason(ev)
	if !ok {
		a.s.log.Warn("needs_human 载荷无 reason，来信按载荷原文渲染", "card", a.card, "seq", ev.Seq)
	}
	return desktopLetterSection{
		Heading: a.heading(fmt.Sprintf("## 需要人 seq %d", ev.Seq), ev),
		Parts: []desktopLetterPart{{Quote: &desktopQuote{
			Author: ev.Actor, Seq: ev.Seq, Kind: desktopQuoteKindNeedsHuman, Body: reason,
		}}},
	}
}

// other 渲染其它事件段（decision_*、task 终态等，条 268）：载荷原文进 event 围栏。
func (a *desktopLetterAssembler) other(ev proto.LedgerEvent) desktopLetterSection {
	return desktopLetterSection{
		Heading: a.heading(fmt.Sprintf("## %s seq %d", ev.Type, ev.Seq), ev),
		Parts: []desktopLetterPart{{Quote: &desktopQuote{
			Author: ev.Actor, Seq: ev.Seq, Kind: desktopQuoteKindEvent, Body: string(ev.Payload),
		}}},
	}
}

// desktopNeedsHumanReason 解出 needs_human 载荷 {"reason": …} 的原因文本；载荷不是该
// 形状时返回载荷原文与 false（调用方 Warn）。唤醒映射与来信渲染共用这一处解码。
func desktopNeedsHumanReason(ev proto.LedgerEvent) (string, bool) {
	var p struct {
		Reason *string `json:"reason"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil || p.Reason == nil {
		return string(ev.Payload), false
	}
	return *p.Reason, true
}

// writeDesktopLetter 把 letter 原子写进 home 下的信箱（契约 §3.6，P1=A）。
//
// 参数：home 写信进程用户的 HOME；cliSession 席位身份里的 CLI 会话 id（信箱键）；
// letter 来信全文；now 取 UnixNano 作文件名序号。
// 返回：最终路径 $home/.handoff/desktop-wake/<cliSession>.<19 位零填充 UnixNano>.md。
// 过程：目录不存在以 0o700 创建（条 70）；同目录写 .<最终名>.tmp（O_EXCL、0o600）→
// Sync → rename 成最终名，并发读者只见完整信（条 69；mod 跳过点开头文件）。
// 注意：cliSession 为空、含 '/' 或 '\'、以 '.' 开头一律拒绝且不碰文件系统——席位
// session_id 段允许这些字符（proto.validateSeatPart），不拦会把信写出信箱目录或写成
// mod 不读的隐藏文件。失败时清理临时文件。同纳秒碰撞与并发写的补强归 B434 G2（条 68）。
func writeDesktopLetter(home, cliSession, letter string, now time.Time) (string, error) {
	switch {
	case cliSession == "":
		return "", errors.New("信箱键（CLI 会话 id）为空")
	case strings.ContainsAny(cliSession, `/\`):
		return "", fmt.Errorf("信箱键 %q 含路径分隔符，拒绝写信", cliSession)
	case strings.HasPrefix(cliSession, "."):
		return "", fmt.Errorf("信箱键 %q 以点开头（会被当成临时/隐藏文件），拒绝写信", cliSession)
	case strings.TrimSpace(home) == "":
		return "", errors.New("信箱所在 HOME 为空")
	}
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("建信箱目录 %s: %w", dir, err)
	}
	name := fmt.Sprintf("%s.%019d.md", cliSession, now.UnixNano())
	final := filepath.Join(dir, name)
	tmp := filepath.Join(dir, "."+name+".tmp")
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("建信箱临时文件 %s: %w", tmp, err)
	}
	if _, err := f.WriteString(letter); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("写信箱临时文件 %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("落盘信箱临时文件 %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("关闭信箱临时文件 %s: %w", tmp, err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("信件改名 %s → %s: %w", tmp, final, err)
	}
	return final, nil
}

// claudeContinueURL 构造 claude://code/continue?session=<url 编码的桌面端会话 id>（条 83）。
// 先过 proto.ValidateDesktopSessionID：id 不合法即报错，调用方不得调 opener（条 84）。
func claudeContinueURL(desktopSession string) (string, error) {
	if err := proto.ValidateDesktopSessionID(desktopSession); err != nil {
		return "", fmt.Errorf("continue 深链的桌面端会话 id 非法: %w", err)
	}
	return proto.ClaudeContinueURLPrefix + url.QueryEscape(desktopSession), nil
}

// deliverDesktopLetter 是拉起器投递（r5）。machine 为本机 → renderLauncherLetter
// (req.Card) 渲染来信（唯一渲染点）→ 写信 + opener；否则（拉起器在异机，P3=A）
// 经 clientForTarget(machine).MarkForwarded() 的 POST /api/desktop/deliver 一跳转交，
// 只搬运 req（含 Card），不在本机渲染。席位唤醒的异机路径不走本函数（走 B389
// transferCoordinatorWake，拍板 D3）。
func (s *Server) deliverDesktopLetter(ctx context.Context, machine string, req proto.DesktopDeliverReq) (proto.DesktopDeliverResp, error) {
	_, _, _ = ctx, machine, req
	return proto.DesktopDeliverResp{}, proto.ErrDesktopNotImplemented // TODO(B434)
}

// handleDesktopDeliver 是 POST /api/desktop/deliver（P3=A）：以本机身份调
// deliverDesktopLetter 的本机分支（按 Card 在本机解析 Folder 并渲染），从不再
// 转发（一跳封顶）；项目在本机无位置 → 409 {"error": <errNoLocalProject 包装文本>}；
// 卡不存在 → 404（ledgerErr，error 含卡号）。按卡读共享账本，注册时须套 withLedger
// （未配账本 503，先于体校验），鉴权沿 /api 既有 token 中间件在外层（契约 r6 §4.15）。
// 未注册（TODO(B434)）。
func (s *Server) handleDesktopDeliver(w http.ResponseWriter, r *http.Request) {
	_ = r
	writeErr(w, http.StatusNotImplemented, proto.ErrDesktopNotImplemented) // TODO(B434)
}

// desktopUndeliveredError 标记「信未落盘的桌面端唤醒失败」（本机写信/渲染失败、
// 异机转交失败）：消费循环按准入满员同款处置——不 completeWakeBatch、不标 seen、认领留在飞挡水位，下一轮
// 重读重试；needs_human 只由认领本批的发起端消费循环按「桌面端失败段」首次落
// 一条（独立计数表，不经 recordAdmissionStall），被转交端从不落（契约 §3.7）。
type desktopUndeliveredError struct {
	machine string
	err     error
}

func (e *desktopUndeliveredError) Error() string {
	return "桌面端唤醒未投递（" + e.machine + "）: " + e.err.Error()
}

func (e *desktopUndeliveredError) Unwrap() error { return e.err }

// desktopUndelivered 报告 err 是否为「信未落盘」的桌面端唤醒失败（消费循环分支用）。
func desktopUndelivered(err error) bool {
	var stall *desktopUndeliveredError
	return errors.As(err, &stall)
}

// desktopUnopenedError 标记「信已落盘、continue 深链没发出去」（opener 失败，
// 本机或被转交端）：消费循环按终局处置（completeWakeBatch + 标 seen，不重试），
// 并按桌面端失败段落 needs_human；handleCoordWake 遇到它回 200 +
// CoordinatorWakeResp.LetterUnopened=true（契约 §3.7、条 109–111、165–170）。
type desktopUnopenedError struct {
	letterPath string
	err        error
}

func (e *desktopUnopenedError) Error() string {
	return "桌面端来信已落盘但 continue 未发出（" + e.letterPath + "）: " + e.err.Error()
}

func (e *desktopUnopenedError) Unwrap() error { return e.err }

// desktopUnopened 报告 err 是否为「信已落盘、opener 失败」。
func desktopUnopened(err error) bool {
	var unopened *desktopUnopenedError
	return errors.As(err, &unopened)
}

// forwardedWakeKey 是 ctx 上「本次唤醒来自他机转交」的标记键。
type forwardedWakeKey struct{}

// withForwardedWake 由 handleCoordWake 包在 r.Context() 上：被转交端对桌面端
// 席位不再二次转交（一跳封顶）。
func withForwardedWake(ctx context.Context) context.Context {
	return context.WithValue(ctx, forwardedWakeKey{}, true)
}

// isForwardedWake 报告 ctx 是否带转交标记。
func isForwardedWake(ctx context.Context) bool {
	v, _ := ctx.Value(forwardedWakeKey{}).(bool)
	return v
}

// wakeDesktopSeat 是 wakeCoordinatorRoundID 对「有桌面端记录的 bind 席位」的
// 分支：desk.Machine 为本机 → 渲染来信 → 写信 + opener；异机且未被转交过 →
// transferCoordinatorWake(desk.Machine)。本函数**从不**自己落 needs_human，只返回
// 类型化错误交给调用方：写信/渲染失败、转交非 2xx、异机且 isForwardedWake(ctx)
// （一跳封顶，不再转交）→ desktopUndeliveredError；opener 失败（本机）或转交回包
// LetterUnopened=true → desktopUnopenedError。
//
// 现状（B434.4）：本机分支已实现，成功返回 RoundResult{Woke: true, SessionID: desk.Session}
// （handleCoordWake 据 Woke 回 200）；信箱根取 s.desktopHome，空则 os.UserHomeDir()
// （信箱属写信进程用户，契约 r10 第 11 条）；
// opener 取 s.desktopOpen，nil 视为不能发深链。异机分支暂返回 desktopUndeliveredError，
// 由 B434 G2 换成 transferCoordinatorWake + 一跳封顶。零 keystone 调用、零账本写。
func (s *Server) wakeDesktopSeat(ctx context.Context, card, seat string, desk ledger.SeatDesktop,
	evs []keystone.WakeEvent, raws []proto.LedgerEvent, roundID string) (keystone.RoundResult, error) {
	var zero keystone.RoundResult
	machine := desk.Machine
	if machine == "" {
		machine = "本机"
	}
	s.log.Info("桌面端席位唤醒开始", "card", card, "seat", seat, "desktop_session", desk.Session,
		"machine", machine, "via", desk.Via, "event_count", len(raws), "round_id", roundID)
	undelivered := func(stage string, cause error) (keystone.RoundResult, error) {
		s.log.Error("桌面端席位唤醒未投递（信未落盘）", "card", card, "machine", machine,
			"stage", stage, "round_id", roundID, "cause", cause)
		return zero, &desktopUndeliveredError{machine: machine, err: fmt.Errorf("%s: %w", stage, cause)}
	}
	if !scheduling.IsLocalMachine(desk.Machine) && !s.IsSelfTarget(desk.Machine) {
		// 跨机转交（transferCoordinatorWake、isForwardedWake 一跳封顶，用到 ctx）归 B434 G2。
		return undelivered("跨机转交", fmt.Errorf("桌面端席位在异机 %s，跨机转交尚未接通", desk.Machine))
	}
	_, cliSession, err := proto.ParseSeatIdentity(seat)
	if err != nil {
		return undelivered("解析席位 CLI 会话 id", err)
	}
	letter, err := s.desktopLetter(card, seat, evs, raws)
	if err != nil {
		return undelivered("组装来信", err)
	}
	home := s.desktopHome
	if home == "" {
		if home, err = os.UserHomeDir(); err != nil {
			return undelivered("取 HOME", err)
		}
	}
	letterPath, err := writeDesktopLetter(home, cliSession, letter, time.Now())
	if err != nil {
		return undelivered("写信箱", err)
	}
	s.log.Info("桌面端来信已落盘", "card", card, "letter", letterPath, "bytes", len(letter), "round_id", roundID)
	unopened := func(stage string, cause error) (keystone.RoundResult, error) {
		s.log.Error("桌面端来信已落盘但 continue 未发出", "card", card, "letter", letterPath,
			"stage", stage, "round_id", roundID, "cause", cause)
		return zero, &desktopUnopenedError{letterPath: letterPath, err: fmt.Errorf("%s: %w", stage, cause)}
	}
	link, err := claudeContinueURL(desk.Session)
	if err != nil {
		return unopened("构造 continue 深链", err)
	}
	if s.desktopOpen == nil {
		return unopened("发 continue 深链", errors.New("本机未装配桌面端深链 opener"))
	}
	if err := s.desktopOpen(link); err != nil {
		return unopened("发 continue 深链", err)
	}
	s.log.Info("桌面端席位唤醒完成", "card", card, "desktop_session", desk.Session,
		"letter", letterPath, "round_id", roundID)
	return keystone.RoundResult{Woke: true, SessionID: desk.Session}, nil
}

// renderLauncherLetter 渲染拉起器来信（r5，唯一渲染点，只在拉起器所在机器调用）：
// desktopLaunchMaterial(card, SeatDesktopViaLauncher) → 套 §3.6 拉起器来信模板。
// 项目在本机无位置 → 透传 errNoLocalProject 包装错误。
func (s *Server) renderLauncherLetter(card string) (string, error) {
	_ = card
	return "", proto.ErrDesktopNotImplemented // TODO(B434)
}

// desktopNeedsHumanSystemActors 是「系统组件自打」的 needs_human actor 精确值表
// （B434 P5=B）：这些标不叫醒桌面端协调者。取值来自现有 MarkNeedsHuman 生产者
// （wakeconsumer.go:84/:483 "agentd"、keystone.go:178 "keystone"、
// scheddrain.go:337 "coordinator"），本卡 §3.7 失败段与无拉起器标同为 "agentd"。
var desktopNeedsHumanSystemActors = []string{"agentd", "keystone", "coordinator"}

// desktopNeedsHumanSystemActorPrefix 是工作流节点自打标的 actor 前缀
// （ledgerstep runner.go:264 / node.go:78 "node:<节点名>"），同属系统组件。
const desktopNeedsHumanSystemActorPrefix = "node:"

// isSystemNeedsHumanActor 报告 actor 是否为系统组件（精确命中
// desktopNeedsHumanSystemActors 或以 desktopNeedsHumanSystemActorPrefix 开头）。
func isSystemNeedsHumanActor(actor string) bool {
	_ = actor
	return false // TODO(B434)
}

// desktopNeedsHumanWakes 是 needs_human 对桌面端席位的唤醒映射（P5=B），由
// automationWakeEvents 在调用纯函数 automationWakeEvent 之前对 EvNeedsHuman 分流：
// 目标 = resolveWakeCard(ev.CardID)；目标须是有桌面端记录的 bind 席位，否则返回空
// （coordinate 席位与无记录 bind 维持 B394 现行判据）；actor 为系统组件或等于目标
// 席位身份 → 返回空；命中 → 一条 keystone.WakeTaskTerminal（Card=ev.CardID，
// Summary="needs_human: <reason>"）。
func (s *Server) desktopNeedsHumanWakes(ev proto.LedgerEvent) ([]keystone.WakeEvent, error) {
	_ = ev
	return nil, proto.ErrDesktopNotImplemented // TODO(B434)
}

// openViaDesktopLauncher 是拉起器代开：读项目拉起器 → 投递装着拉起材料
// （via=launcher）的来信。无登记 → MarkNeedsHuman(desktopNoLauncherReason) +
// errNoDesktopLauncher。
func (s *Server) openViaDesktopLauncher(ctx context.Context, card string) (proto.DesktopLauncherOpenResp, error) {
	_, _ = ctx, card
	return proto.DesktopLauncherOpenResp{}, proto.ErrDesktopNotImplemented // TODO(B434)
}

// ---- 来信围栏（B434 P9=丁，契约 §3.6、条 263–279）----
//
// 来信里每段第三方原文（命中条、引用条、工单载荷、needs_human 原因、其它事件
// 载荷）都放进一个围栏块，开栏行标注作者身份（账本 actor 原值，经
// sanitizeDesktopQuoteAuthor）、seq 与种类；围栏外只允许 handoff 自生成的骨架
// 文本（卡号、seq、未读数、处置提示）。来信仍以 asUser:true 提交（拍板 D7），
// 授权边界由围栏 + skill.DesktopLetterAuthorizationRule 承担。

// desktopQuoteFenceOpen 是围栏开栏行前缀；完整开栏行为
// desktopQuoteFenceOpen + " author=<a> seq=<n> kind=<k>"。
const desktopQuoteFenceOpen = "```handoff-quote"

// desktopQuoteFenceClose 是闭栏行（独占一行）。
const desktopQuoteFenceClose = "```"

// 围栏种类（开栏行 kind= 的取值全集）。
const (
	desktopQuoteKindRoomMessage = "room_message" // 会话消息命中条与引用条
	desktopQuoteKindTicket      = "ticket"       // task_mirrored 工单载荷
	desktopQuoteKindNeedsHuman  = "needs_human"  // needs_human 原因
	desktopQuoteKindEvent       = "event"        // 其它事件（decision_*、task 终态）载荷
)

// desktopAuthorRosterPrefix 与 desktopAuthorRosterSep 组成来信第 2 行作者名册：
// prefix + 去重身份按首次出现顺序以 sep 连接。
const (
	desktopAuthorRosterPrefix = "本信含以下身份的原文："
	desktopAuthorRosterSep    = "、"
)

// desktopQuote 是一段第三方原文：Author 为账本 actor 原值，Body 为未转义原文。
type desktopQuote struct {
	Author string
	Seq    int64
	Kind   string
	Body   string
}

// desktopLetterPart 是来信段内的一个部件：Skeleton 非空 = handoff 自生成的骨架行；
// Quote 非 nil = 第三方原文围栏块。二者恰有其一。
type desktopLetterPart struct {
	Skeleton string
	Quote    *desktopQuote
}

// desktopLetterSection 是来信的一段：Heading 为骨架段标题（不含 actor），Parts 依序渲染。
type desktopLetterSection struct {
	Heading string
	Parts   []desktopLetterPart
}

// escapeDesktopQuoteBody 转义围栏正文：每段连续 ≥3 个反引号中的每个反引号前加
// 一个反斜杠；1–2 个连续反引号与其它字符（含已有反斜杠）原样（条 270–271）。
// 为什么选反斜杠而不选变长围栏：开栏/闭栏行固定为三反引号，名册与 kind 解析不随
// 正文变，金样本能锁死字面量（契约 §3.6 围栏规则）。按字节扫描安全：反引号是
// ASCII，不会出现在 UTF-8 多字节序列内部。
func escapeDesktopQuoteBody(body string) string {
	var b strings.Builder
	b.Grow(len(body))
	for i := 0; i < len(body); {
		if body[i] != '`' {
			b.WriteByte(body[i])
			i++
			continue
		}
		j := i
		for j < len(body) && body[j] == '`' {
			j++
		}
		if j-i >= 3 {
			for k := i; k < j; k++ {
				b.WriteString("\\`")
			}
		} else {
			b.WriteString(body[i:j])
		}
		i = j
	}
	return b.String()
}

// desktopQuoteAuthorReplacer 把作者值里会破坏开栏行的字符换成 "_"（条 272）。
var desktopQuoteAuthorReplacer = strings.NewReplacer(" ", "_", "\t", "_", "\r", "_", "\n", "_", "`", "_")

// sanitizeDesktopQuoteAuthor 把 actor 中的空格、\t、\r、\n 与反引号各替换为 "_"，
// 保证开栏行单行且不可被提前闭栏（条 272）。名册用同一清洗值。
func sanitizeDesktopQuoteAuthor(actor string) string {
	return desktopQuoteAuthorReplacer.Replace(actor)
}

// desktopQuoteBlock 渲染一个围栏块：开栏行（desktopQuoteFenceOpen + " author=<清洗后>
// seq=<n> kind=<k>"）、转义后正文、闭栏行，各以 "\n" 结尾（条 267）。
func desktopQuoteBlock(q desktopQuote) string {
	var b strings.Builder
	b.WriteString(desktopQuoteFenceOpen)
	b.WriteString(" author=")
	b.WriteString(sanitizeDesktopQuoteAuthor(q.Author))
	b.WriteString(" seq=")
	b.WriteString(strconv.FormatInt(q.Seq, 10))
	b.WriteString(" kind=")
	b.WriteString(q.Kind)
	b.WriteByte('\n')
	b.WriteString(escapeDesktopQuoteBody(q.Body))
	b.WriteByte('\n')
	b.WriteString(desktopQuoteFenceClose)
	b.WriteByte('\n')
	return b.String()
}

// renderDesktopLetter 是来信纯渲染层（契约 §3.6 布局）：第 1 行 head（以
// DesktopWakeMarker 开头）→ 第 2 行作者名册（全部围栏块清洗后的 author 按首次出现
// 顺序去重，系统 actor 照列）→ 空行 → 每段「标题行 + 各部件 + 空行」→ 末行 tail，
// 每行以 "\n" 结尾。部件 Quote 非 nil 渲染围栏块，否则渲染 Skeleton 骨架行。
// desktopLetter 负责取数并组装 sections，不自行拼接任何第三方文本（条 263–275）。
func renderDesktopLetter(head string, sections []desktopLetterSection, tail string) string {
	var authors []string
	seen := make(map[string]bool)
	for _, sec := range sections {
		for _, part := range sec.Parts {
			if part.Quote == nil {
				continue
			}
			author := sanitizeDesktopQuoteAuthor(part.Quote.Author)
			if !seen[author] {
				seen[author] = true
				authors = append(authors, author)
			}
		}
	}
	var b strings.Builder
	b.WriteString(head)
	b.WriteByte('\n')
	b.WriteString(desktopAuthorRosterPrefix)
	b.WriteString(strings.Join(authors, desktopAuthorRosterSep))
	b.WriteString("\n\n")
	for _, sec := range sections {
		b.WriteString(sec.Heading)
		b.WriteByte('\n')
		for _, part := range sec.Parts {
			if part.Quote != nil {
				b.WriteString(desktopQuoteBlock(*part.Quote))
				continue
			}
			b.WriteString(part.Skeleton)
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString(tail)
	b.WriteByte('\n')
	return b.String()
}
