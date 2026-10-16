// desktopwake.go —— B434 Claude 桌面端会话当协调者：控制面侧。
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
// 不经 keystone（keystone.Wake / LaunchForCard 对桌面端席位零调用）。本机唤醒主缝与
// 围栏渲染层由 B434.4 落码；拉起材料由 G1 落码；来信全段、信箱补强、needs_human 唤醒、
// 失败段、跨机转交与生产 opener 由 G2（B434.5）落码；拉起器代开与投递（openViaDesktopLauncher、
// deliverDesktopLetter、renderLauncherLetter、handleDesktopDeliver、handleDesktopLauncherOpen）
// 由 G3（B434.6）落码。契约见 docs/superpowers/specs/b434-contract.md §3.5–§3.9.1。
package agentd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/client"
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

// desktopLetterSessionCmdSep 连接尾行里多个会话的读消息命令（契约 r10 第 8 条「以『、』连接」）。
// 与作者名册分隔符 desktopAuthorRosterSep 同值但是两条独立规则，各自一处定义，改一个不牵动另一个。
const desktopLetterSessionCmdSep = "、"

// desktopLetterTail 渲染来信尾行（契约 §3.6，r10 第 8 条）：rooms 为空时省去整段
// 「；会话消息：…」分句；多个会话按首次出现顺序、每个会话一条完整命令，以「、」连接。
func desktopLetterTail(card string, rooms []string) string {
	tail := "按分诊表处置；读全文：handoff card show " + card
	if len(rooms) > 0 {
		cmds := make([]string, len(rooms))
		for i, room := range rooms {
			cmds[i] = desktopLetterSessionReadCmd + room
		}
		tail += "；会话消息：" + strings.Join(cmds, desktopLetterSessionCmdSep)
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

// errDesktopMailboxKeyUnsafe 是 writeDesktopLetter 拒绝不安全信箱键（CLI 会话 id 为空、
// 含路径分隔符、以 '.' 开头）时包装的哨兵（契约 r12 第 6 条）：G3 收件机据此把请求映射
// 为 400，席位唤醒侧仍按「未投递」处置（拒绝逻辑本身是 V 落的，这里只加可判别的身份）。
var errDesktopMailboxKeyUnsafe = errors.New("信箱键不安全")

// desktopLetterNameRetries 是同一 CLI 会话、同一纳秒（或并发写者撞名）时文件名序号
// 顺延的上限：每撞一次 UnixNano 段 +1。64 远大于同纳秒能出现的写者数，超过即报错
// （按「未投递」处置）而不是覆盖。
const desktopLetterNameRetries = 64

// writeDesktopLetter 把 letter 原子写进 home 下的信箱（契约 §3.6，P1=A）。
//
// 参数：home 写信进程用户的 HOME；cliSession 席位身份里的 CLI 会话 id（信箱键）；
// letter 来信全文；now 取 UnixNano 作文件名序号。
// 返回：最终路径 $home/.handoff/desktop-wake/<cliSession>.<19 位零填充 UnixNano>.md。
// 过程：目录不存在以 0o700 创建（条 70）；同目录写 .<最终名>.tmp（O_EXCL、0o600）→
// Sync → os.Link 成最终名 → 删临时名。并发读者只见完整信（条 69；mod 跳过点开头文件）。
// 为什么用 Link 不用 Rename：rename 会静默覆盖同名信——同会话同纳秒两次写信（测试固定
// 时钟、或转交收件与本机消费循环并发）第二封会吞掉第一封（条 68，台账 L4 实测）；Link
// 遇已存在的最终名返回 EEXIST，据此把序号 +1 重试，从不覆盖。
// 注意：cliSession 为空、含 '/' 或 '\'、以 '.' 开头一律拒绝且不碰文件系统——席位
// session_id 段允许这些字符（proto.validateSeatPart），不拦会把信写出信箱目录或写成
// mod 不读的隐藏文件；拒绝错误包 errDesktopMailboxKeyUnsafe（契约 r12 第 6 条，G3 收件机
// 映射 400）。HOME 为空不属信箱键问题，不包该哨兵。失败时清理本次临时文件。
func writeDesktopLetter(home, cliSession, letter string, now time.Time) (string, error) {
	switch {
	case cliSession == "":
		return "", fmt.Errorf("%w：CLI 会话 id 为空", errDesktopMailboxKeyUnsafe)
	case strings.ContainsAny(cliSession, `/\`):
		return "", fmt.Errorf("%w：%q 含路径分隔符，拒绝写信", errDesktopMailboxKeyUnsafe, cliSession)
	case strings.HasPrefix(cliSession, "."):
		return "", fmt.Errorf("%w：%q 以点开头（会被当成临时/隐藏文件），拒绝写信", errDesktopMailboxKeyUnsafe, cliSession)
	case strings.TrimSpace(home) == "":
		return "", errors.New("信箱所在 HOME 为空")
	}
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("建信箱目录 %s: %w", dir, err)
	}
	base := now.UnixNano()
	for i := int64(0); i < desktopLetterNameRetries; i++ {
		name := fmt.Sprintf("%s.%019d.md", cliSession, base+i)
		final := filepath.Join(dir, name)
		taken, err := writeDesktopLetterOnce(filepath.Join(dir, "."+name+".tmp"), final, letter)
		if err != nil {
			return "", err
		}
		if !taken {
			return final, nil
		}
	}
	return "", fmt.Errorf("信箱 %s 里 %s 起连续 %d 个序号都已占用，拒绝覆盖", dir, cliSession, desktopLetterNameRetries)
}

// writeDesktopLetterOnce 以 tmp → final 的固定名写一次信：tmp 或 final 已存在 →
// taken=true（调用方换下一个序号，不覆盖）；其余失败返回错误并删除本次 tmp。
// Link 成功后删 tmp 失败不报错：信已按最终名落盘（mod 只读最终名），残留的点开头
// tmp 由 sweepDesktopLetterTmp 按年龄清理。
func writeDesktopLetterOnce(tmp, final, letter string) (taken bool, err error) {
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("建信箱临时文件 %s: %w", tmp, err)
	}
	if _, err := f.WriteString(letter); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return false, fmt.Errorf("写信箱临时文件 %s: %w", tmp, err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return false, fmt.Errorf("落盘信箱临时文件 %s: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return false, fmt.Errorf("关闭信箱临时文件 %s: %w", tmp, err)
	}
	if err := os.Link(tmp, final); err != nil {
		_ = os.Remove(tmp)
		if errors.Is(err, fs.ErrExist) {
			return true, nil
		}
		return false, fmt.Errorf("信件落名 %s → %s: %w", tmp, final, err)
	}
	_ = os.Remove(tmp)
	return false, nil
}

// desktopLetterTmpMaxAge 是信箱临时文件的残留判定年龄：正常写信从建 tmp 到删 tmp 在
// 毫秒级，超过一小时仍在的只可能是写信中途进程崩溃的遗留（V review 遗留项）。
const desktopLetterTmpMaxAge = time.Hour

// sweepDesktopLetterTmp 删除 home 信箱里本 CLI 会话、修改时间早于 desktopLetterTmpMaxAge
// 的临时文件（.<cliSession>.<19 位>.md.tmp）。只在本会话一封信成功落盘后调用（此时
// cliSession 已过 writeDesktopLetter 的安全校验）；用真实墙钟比 mtime（不用 desktopClock
// 测试缝——缝被固定到未来时会误删并发写者的新 tmp）。尽力而为：失败只 Warn，不影响投递。
func (s *Server) sweepDesktopLetterTmp(home, cliSession string) {
	dir := filepath.Join(home, proto.DesktopWakeDirRel)
	entries, err := os.ReadDir(dir)
	if err != nil {
		s.log.Warn("清理信箱临时文件：读目录失败", "dir", dir, "cause", err)
		return
	}
	pattern := regexp.MustCompile(`^\.` + regexp.QuoteMeta(cliSession) + `\.[0-9]{19}\.md\.tmp$`)
	removed := 0
	for _, e := range entries {
		if e.IsDir() || !pattern.MatchString(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < desktopLetterTmpMaxAge {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			s.log.Warn("清理信箱临时文件失败", "file", e.Name(), "cause", err)
			continue
		}
		removed++
	}
	if removed > 0 {
		s.log.Info("已清理信箱残留临时文件", "dir", dir, "cli_session", cliSession, "removed", removed)
	}
}

// desktopClock 是桌面端时钟缝（V review m3）：生产 desktopNow 为 nil 取 time.Now；测试固定
// 它来复现同纳秒写信、推进失败段退避窗。与薄壳状态 TTL（desktopstate.go）共用同一缝。
func (s *Server) desktopClock() time.Time {
	if s.desktopNow != nil {
		return s.desktopNow()
	}
	return time.Now()
}

// claudeContinueURL 构造 claude://code/continue?session=<url 编码的桌面端会话 id>（条 83）。
// 先过 proto.ValidateDesktopSessionID：id 不合法即报错，调用方不得调 opener（条 84）。
func claudeContinueURL(desktopSession string) (string, error) {
	if err := proto.ValidateDesktopSessionID(desktopSession); err != nil {
		return "", fmt.Errorf("continue 深链的桌面端会话 id 非法: %w", err)
	}
	return proto.ClaudeContinueURLPrefix + url.QueryEscape(desktopSession), nil
}

// errDesktopDeepLinkUnsupported 是非 darwin 平台生产 opener 的固定错误（契约 §3.7）：
// 席位唤醒照常写信，continue 一步按「信已落盘、continue 未发出」走失败段。
var errDesktopDeepLinkUnsupported = errors.New("本平台不支持 Claude 桌面端深链")

// desktopOpenTimeout 是 darwin `open` 子进程的上限：open 只把 URL 交给 LaunchServices
// 即返回（毫秒级），挂住多半是 GUI 会话异常（屏幕锁定、登出）；给上限免得拖住单协程消费循环。
const desktopOpenTimeout = 15 * time.Second

// PlatformDesktopOpener 按 GOOS 给出生产 opener（契约 §3.7、r12 第 3 条）：darwin →
// openDesktopDeepLinkDarwin；其余平台 → 恒返回 errDesktopDeepLinkUnsupported。
// 参数 goos 而非读 runtime.GOOS：测试在任何平台都能锁住两条分支。
// 注意：NewServer 不调用它——装配只在 cmd/agentd.go 组装点经 SetDesktopOpener 完成，测试进程
// 经 NewServer 得到的 Server 保持 nil opener，永远不会在开发机上真的拉起桌面端。
func PlatformDesktopOpener(goos string) func(string) error {
	if goos != "darwin" {
		return func(string) error { return errDesktopDeepLinkUnsupported }
	}
	return openDesktopDeepLinkDarwin
}

// SetDesktopOpener 装配 continue 深链 opener（契约 r12 第 3 条，与既有 srv.SetX 同形）：只在
// 组装期调用一次，运行期只读不加锁。open 为 nil 等价于未装配（wakeDesktopSeat 按「信已落盘、
// continue 未发出」处置）。
func (s *Server) SetDesktopOpener(open func(string) error) {
	s.desktopOpen = open
	s.log.Info("桌面端 continue 深链 opener 已装配", "installed", open != nil)
}

// openDesktopDeepLinkDarwin 以 `open <link>` 把 continue 深链交给 Claude 桌面端。
//   - 只放行 proto.ClaudeContinueURLPrefix 开头的链接（opener 不做通用 URL 打开器）；
//   - 子进程带 desktopOpenTimeout 上限；失败把合并输出带进错误，交 wakeDesktopSeat 的
//     「未打开」出口记 Error。
func openDesktopDeepLinkDarwin(link string) error {
	if !strings.HasPrefix(link, proto.ClaudeContinueURLPrefix) {
		return fmt.Errorf("拒绝打开非 continue 深链: %q", link)
	}
	ctx, cancel := context.WithTimeout(context.Background(), desktopOpenTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, "open", link).CombinedOutput()
	if err != nil {
		return fmt.Errorf("open %s: %w（输出：%s）", link, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// deliverDesktopLetter 是拉起器投递（契约 §3.9.1，r5）：把「请替我开一场协调者会话」的
// 来信投进拉起器 CLI 会话的信箱，并对拉起器桌面端会话发 continue 深链。
//
// 参数：machine 拉起器登记的机器（空串 / 本机名 / 指向本机的 target 名 = 本机，判据与
// wakeDesktopSeat 同式）；req 投递请求（CLISession 信箱键、DesktopSession continue 目标、
// Card 卡号）。
// 分支：本机 → deliverLauncherLetterLocal（renderLauncherLetter 是唯一渲染点，按**本机**
// 项目位置解析 Folder，条 217）；异机 → forwardLauncherLetter（POST /api/desktop/deliver，
// 发起端不渲染、不解析 Folder，条 213/214）。
// 返回：err == nil ⇔ 信已落盘（本机或收件机）；resp.Opened 报告 continue 是否发出——
// opener 失败或未装配不算错误（信已保住，处置归调用方）。err != nil 时信未落盘：项目
// 无本机位置（本机或收件机 409）→ errors.Is(err, errNoLocalProject)，收件机 409 时
// err.Error() 逐字等于收件机 error 原文（条 220）；其余原样包装。
// 注意：从不落 needs_human（由 openViaDesktopLauncher 统一落，被转交端从不打标）；从不读
// 拉起器登记——machine 与 req 都由调用方给定，所以收件机 handleDesktopDeliver 以
// machine="" 调用时恒走本机分支、零出站（一跳封顶，条 155）。席位唤醒的异机路径不走
// 本函数（走 B389 transferCoordinatorWake，拍板 D3）。
func (s *Server) deliverDesktopLetter(ctx context.Context, machine string, req proto.DesktopDeliverReq) (proto.DesktopDeliverResp, error) {
	if scheduling.IsLocalMachine(machine) || s.IsSelfTarget(machine) {
		return s.deliverLauncherLetterLocal(req)
	}
	return s.forwardLauncherLetter(ctx, machine, req)
}

// forwardLauncherLetter 是 deliverDesktopLetter 的异机分支：经 clientForTarget(machine)
// .MarkForwarded() 发一次 POST /api/desktop/deliver（带 X-Handoff-Forwarded，条 153），只搬运
// req；单次时限 desktopTransferTimeout（与席位唤醒转交同一上限，契约 r12 第 4 条——对端挂住时
// 不把代开请求无限拖住）。收件机 409（项目在它那里没有登记位置）→ desktopRemoteNoLocalProjectError
// （文本逐字取收件机 error，可 errors.Is(errNoLocalProject)）；请求已发出却没拿到回包（超时、
// 读回包时断连）→ *desktopDeliveryUnknownError（收件机可能已写信，调用方不得说「未投递」）；
// 其余失败（取客户端、拨号失败、非 2xx 含 400/503/404/500）原样包装上抛，由调用方按投递失败处置。
func (s *Server) forwardLauncherLetter(ctx context.Context, machine string, req proto.DesktopDeliverReq) (proto.DesktopDeliverResp, error) {
	var zero proto.DesktopDeliverResp
	c, err := s.clientForTarget(machine)
	if err != nil {
		s.log.Error("拉起器投递取目标客户端失败", "card", req.Card, "machine", machine, "cause", err)
		return zero, fmt.Errorf("取拉起器机器 %s 的客户端: %w", machine, err)
	}
	tctx, cancel := context.WithTimeout(ctx, desktopTransferTimeout)
	defer cancel()
	s.log.Info("拉起器投递转交到拉起器所在机器", "card", req.Card, "machine", machine)
	resp, err := c.MarkForwarded().DesktopDeliver(tctx, req)
	if err != nil {
		if code, ok := client.StatusCode(err); ok && code == http.StatusConflict {
			text, _ := client.ServerErrorText(err)
			s.log.Warn("拉起器所在机器上项目无登记位置", "card", req.Card, "machine", machine, "cause", text)
			return zero, &desktopRemoteNoLocalProjectError{text: text}
		}
		if launcherDeliveryOutcomeUnknown(err) {
			s.log.Error("拉起器投递请求已发出但没拿到回包，结果未知", "card", req.Card, "machine", machine, "cause", err)
			return zero, &desktopDeliveryUnknownError{err: fmt.Errorf("投递到拉起器机器 %s: %w", machine, err)}
		}
		s.log.Error("拉起器投递到目标机失败", "card", req.Card, "machine", machine, "cause", err)
		return zero, fmt.Errorf("投递到拉起器机器 %s: %w", machine, err)
	}
	s.log.Info("拉起器投递已由收件机受理", "card", req.Card, "machine", machine,
		"handled_by", resp.HandledBy, "written", resp.Written, "opened", resp.Opened)
	return *resp, nil
}

// desktopRemoteNoLocalProjectError 是收件机回 409「项目在本机没有登记位置」的透传：
// Error() 逐字等于收件机 error 原文（条 220），Unwrap 到 errNoLocalProject，使发起端与
// 本机无位置走同一条 409 映射、同样不落 needs_human（条 221）。
// 机器名不进 Error()（须与收件机原文逐字相同），由 forwardLauncherLetter 的 Warn 日志记。
type desktopRemoteNoLocalProjectError struct {
	text string
}

func (e *desktopRemoteNoLocalProjectError) Error() string { return e.text }

func (e *desktopRemoteNoLocalProjectError) Unwrap() error { return errNoLocalProject }

// desktopDeliveryUnknownError 是「投递请求已发到收件机、却没拿到回包」（review minor 1）：收件机
// 可能已经写好信、发了 continue，也可能没有。发起端据此落「投递结果未知」而非「来信未投递」，
// 提醒人先看拉起器信箱再决定是否重来（重复代开会开出两场会话）。
type desktopDeliveryUnknownError struct{ err error }

func (e *desktopDeliveryUnknownError) Error() string { return e.err.Error() }

func (e *desktopDeliveryUnknownError) Unwrap() error { return e.err }

// launcherDeliveryOutcomeUnknown 判定一次 DesktopDeliver 失败是否属「请求已发出、结果未知」：
// 拨号失败（net.OpError.Op == "dial"，含拨号阶段撞上时限）请求根本没出去 → false；读回包时的
// 网络错误（net.OpError.Op == "read"，如 connection reset）、时限到（context.DeadlineExceeded /
// net.Error.Timeout）、读回包时连接关闭（io.EOF / io.ErrUnexpectedEOF）→ true。HTTP 非 2xx 不经
// 这里（已拿到回包，结果确定）。
func launcherDeliveryOutcomeUnknown(err error) bool {
	var op *net.OpError
	if errors.As(err, &op) {
		switch op.Op {
		case "dial":
			return false
		case "read":
			return true
		}
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// deliverLauncherLetterLocal 是 deliverDesktopLetter 的本机分支：渲染 → 构造 continue 深链
// → 写信 → opener。深链先于写信构造：会话 id 非法时一封信都不落（不留发不出去的信）。
// 信箱根取 desktopMailboxHome()（G2 收口的唯一 HOME 规则，契约 r12 第 10 条）；文件名序号取
// desktopClock()（与席位唤醒同一时钟缝）；落盘后顺手清本会话的残留临时文件（与 wakeDesktopSeat
// 同款，尽力而为）。
// 返回：信落盘即 Written=true、err=nil；Opened 仅在 s.desktopOpen 非 nil 且成功时为 true。
// 信箱键不安全时 err 包 errDesktopMailboxKeyUnsafe（收件机据此回 400）。
func (s *Server) deliverLauncherLetterLocal(req proto.DesktopDeliverReq) (proto.DesktopDeliverResp, error) {
	var zero proto.DesktopDeliverResp
	letter, err := s.renderLauncherLetter(req.Card)
	if err != nil {
		return zero, err
	}
	link, err := claudeContinueURL(req.DesktopSession)
	if err != nil {
		return zero, err
	}
	home, err := s.desktopMailboxHome()
	if err != nil {
		return zero, fmt.Errorf("取拉起器信箱根目录: %w", err)
	}
	letterPath, err := writeDesktopLetter(home, req.CLISession, letter, s.desktopClock())
	if err != nil {
		return zero, fmt.Errorf("写拉起器信箱: %w", err)
	}
	s.sweepDesktopLetterTmp(home, req.CLISession)
	s.log.Info("拉起器来信已落盘", "card", req.Card, "cli_session", req.CLISession,
		"letter", letterPath, "bytes", len(letter))
	resp := proto.DesktopDeliverResp{HandledBy: s.localMachineName(), Written: true}
	if s.desktopOpen == nil {
		s.log.Error("拉起器来信已落盘但本机未装配 continue opener", "card", req.Card, "letter", letterPath)
		return resp, nil
	}
	if err := s.desktopOpen(link); err != nil {
		s.log.Error("拉起器来信已落盘但 continue 未发出", "card", req.Card, "letter", letterPath,
			"desktop_session", req.DesktopSession, "cause", err)
		return resp, nil
	}
	resp.Opened = true
	s.log.Info("拉起器 continue 已发出", "card", req.Card, "desktop_session", req.DesktopSession)
	return resp, nil
}

// validateDesktopDeliverReq 校验 /api/desktop/deliver 请求体：card 非空（条 222）；
// cli_session 能编进 cli:claude#<id> 席位语法（与拉起器登记写闸同源，proto.EncodeSeatIdentity）；
// desktop_session 过 proto.ValidateDesktopSessionID。信箱键的路径安全（`/`、`\`、点开头）
// 仍由 writeDesktopLetter 最后一道拦截，它以 errDesktopMailboxKeyUnsafe 哨兵拒绝，
// handleDesktopDeliver 据此回 400（契约 r12 第 6 条）——规则只在一处定义，不在这里复制。
func validateDesktopDeliverReq(req proto.DesktopDeliverReq) error {
	if strings.TrimSpace(req.Card) == "" {
		return errors.New("card 必填")
	}
	if _, err := proto.EncodeSeatIdentity("claude", req.CLISession); err != nil {
		return fmt.Errorf("cli_session 非法: %w", err)
	}
	if err := proto.ValidateDesktopSessionID(req.DesktopSession); err != nil {
		return fmt.Errorf("desktop_session 非法: %w", err)
	}
	return nil
}

// handleDesktopDeliver 是 POST /api/desktop/deliver（P3=A，收件机）：按 req.Card 经共享账本
// 读卡、用**本机**登记的项目位置渲染拉起器来信，写信 + opener。
// 守卫：外层 /api token 中间件（401，条 154/246）→ withLedger（503，先于体校验，条 243/245）。
// 响应：200 DesktopDeliverResp（opener 失败或未装配 → {written:true, opened:false}，信留在信箱，
// 条 157）；400 体解码失败、字段非法（条 222）或信箱键不安全（errDesktopMailboxKeyUnsafe，
// 契约 r12 第 6 条）；404 卡不存在（error 含卡号，条 247）；409 卡的项目在本机无登记位置
// （条 218）；500 其余（列项目位置失败、mgr 未装配、写信失败——发起端按投递失败处置，契约
// r10 第 3 条）。
// 注意：deliverDesktopLetter 的 machine 恒传 ""——不论是否带 X-Handoff-Forwarded，收件机都只在
// 本机写信，从不出站转发（一跳封顶，条 155）；不读拉起器登记、不打 needs_human（条 171 同理：
// 等人标只由发起端落）。
func (s *Server) handleDesktopDeliver(w http.ResponseWriter, r *http.Request) {
	var req proto.DesktopDeliverReq
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, forwardBodyLimit)).Decode(&req); err != nil {
		s.log.Warn("拉起器投递请求体解码失败", "forwarded", isForwarded(r), "cause", err)
		writeErr(w, http.StatusBadRequest, fmt.Errorf("解析请求体: %w", err))
		return
	}
	if err := validateDesktopDeliverReq(req); err != nil {
		s.log.Warn("拉起器投递请求字段非法", "card", req.Card, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.log.Info("收到拉起器投递", "card", req.Card, "cli_session", req.CLISession,
		"desktop_session", req.DesktopSession, "forwarded", isForwarded(r))
	resp, err := s.deliverDesktopLetter(r.Context(), "", req)
	switch {
	case err == nil:
		s.log.Info("拉起器投递完成", "card", req.Card, "written", resp.Written, "opened", resp.Opened)
		writeJSON(w, http.StatusOK, resp)
	case errors.Is(err, errDesktopMailboxKeyUnsafe):
		s.log.Warn("拉起器投递被拒：信箱键不安全", "card", req.Card, "cli_session", req.CLISession, "cause", err)
		writeErr(w, http.StatusBadRequest, err)
	case errors.Is(err, errNoLocalProject):
		s.log.Warn("拉起器投递被拒：项目在本机无登记位置", "card", req.Card, "cause", err)
		writeErr(w, http.StatusConflict, err)
	case errors.Is(err, ledger.ErrNotFound):
		s.log.Warn("拉起器投递被拒：卡不存在", "card", req.Card, "cause", err)
		writeErr(w, http.StatusNotFound, err)
	default:
		s.log.Error("拉起器投递失败", "card", req.Card, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
	}
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
// 本机分支成功返回 RoundResult{Woke: true, SessionID: desk.Session}（handleCoordWake 据 Woke
// 回 200）；信箱根取 desktopMailboxHome()（信箱属写信进程用户，契约 r10 第 11 条、r12 第 10
// 条）；opener 取 s.desktopOpen（cmd 组装点经 SetDesktopOpener 装配；NewServer 不装配，nil 视为不能
// 发深链）。异机分支见
// wakeRemoteDesktopSeat。零 keystone 调用；本函数自身零账本写（转交失败的 wake_round fail 行
// 由 transferCoordinatorWake 落，B389 既有）。
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
		return s.wakeRemoteDesktopSeat(ctx, card, seat, desk, raws, roundID, undelivered)
	}
	_, cliSession, err := proto.ParseSeatIdentity(seat)
	if err != nil {
		return undelivered("解析席位 CLI 会话 id", err)
	}
	letter, err := s.desktopLetter(card, seat, evs, raws)
	if err != nil {
		return undelivered("组装来信", err)
	}
	home, err := s.desktopMailboxHome()
	if err != nil {
		return undelivered("取 HOME", err)
	}
	letterPath, err := writeDesktopLetter(home, cliSession, letter, s.desktopClock())
	if err != nil {
		return undelivered("写信箱", err)
	}
	s.sweepDesktopLetterTmp(home, cliSession)
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

// desktopTransferTimeout 是桌面端席位跨机转交的单次时限。被转交端只写一封信 + 发一次
// 深链（毫秒到秒级），不像 coordinate 转交要跑整个协调者回合；给上限是为了对端挂住时
// 不把单协程的消费循环（与其后的清队）拖住。包级 var 而非 const：测试缩短它验证超时出口。
var desktopTransferTimeout = 30 * time.Second

// wakeRemoteDesktopSeat 是 wakeDesktopSeat 的异机分支（契约 §3.7，拍板 D3）：
//   - 本次已是转交来的（isForwardedWake）→ 一跳封顶，不再出站，返回未投递
//     （「席位机器 <machine> 对被转交端也非本机」，条 98/99）；
//   - 否则经 B389 transferCoordinatorWake 交给 desk.Machine 的 agentd（带 desktopTransferTimeout）：
//     对端回 letter_unopened → transferCoordinatorWake 已映射成 *desktopUnopenedError，原样返回；
//     其余失败（取客户端、不可达、非 2xx、超时）→ 未投递；成功 → 透传对端回合结果。
//
// undelivered 是 wakeDesktopSeat 的未投递出口（统一日志与错误包装）。本函数不落 needs_human。
func (s *Server) wakeRemoteDesktopSeat(ctx context.Context, card, seat string, desk ledger.SeatDesktop,
	raws []proto.LedgerEvent, roundID string,
	undelivered func(stage string, cause error) (keystone.RoundResult, error)) (keystone.RoundResult, error) {
	if isForwardedWake(ctx) {
		return undelivered("一跳封顶", fmt.Errorf("席位机器 %s 对被转交端也非本机", desk.Machine))
	}
	tctx, cancel := context.WithTimeout(ctx, desktopTransferTimeout)
	defer cancel()
	s.log.Info("桌面端席位在异机，转交唤醒", "card", card, "machine", desk.Machine,
		"desktop_session", desk.Session, "event_count", len(raws), "round_id", roundID)
	res, err := s.transferCoordinatorWake(tctx, card, seat, desk.Machine, raws, s.wakeClaimHolder(), roundID)
	if desktopUnopened(err) {
		return keystone.RoundResult{}, err
	}
	if err != nil {
		return undelivered("跨机转交", err)
	}
	s.log.Info("桌面端席位唤醒已转交", "card", card, "machine", desk.Machine,
		"woke", res.Woke, "round_id", roundID)
	return res, nil
}

// desktopMailboxHome 是信箱根目录规则的唯一定义（契约 r10 第 11 条、r12 第 10 条）：
// Server.desktopHome 非空（装配期注入，测试指向临时目录）原样返回，否则取写信进程用户的
// os.UserHomeDir()。席位唤醒本机分支（wakeDesktopSeat）与 G3 拉起器收件机都只经它取信箱根，
// 不各自拼 HOME。返回：UserHomeDir 失败的原错误（调用方按「未投递」处置）。
func (s *Server) desktopMailboxHome() (string, error) {
	if s.desktopHome != "" {
		return s.desktopHome, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("取信箱所在 HOME: %w", err)
	}
	return home, nil
}

// desktopQuoteKindBrief 是拉起器来信里简报围栏的种类（契约 r12 第 7 条）。只用于拉起器来信，
// 席位唤醒信（desktopLetter）的种类全集不变。
const desktopQuoteKindBrief = "brief"

// desktopLauncherLetterAuthor 是简报围栏的作者：简报由 agentd 按账本渲染（r12 第 7 条）。
const desktopLauncherLetterAuthor = "agentd"

// renderLauncherLetter 渲染拉起器来信（契约 §3.6 拉起器来信模板，r5 唯一渲染点，只在拉起器
// 所在机器经 deliverLauncherLetterLocal 调用）：desktopLaunchMaterial(card,
// SeatDesktopViaLauncher) 的 Folder 与 Brief 套进模板，逐字为
// DesktopWakeMarker + " 请替我开一场协调者会话。\n\n项目根目录：<Folder>\n\n用桌面端会话管理工具
// 在上述目录新建会话，把下面整段简报作为首条消息发出：\n\n" + 简报围栏块。
// 为什么简报进围栏（契约 r12 第 7 条，P9 精神覆盖拉起器来信）：简报含卡标题——建卡人写的
// 第三方文本，拉起器会话按常设授权以 asUser 执行来信，裸放会让标题里的指令性文本越过信任
// 边界。围栏复用来信围栏渲染（desktopQuoteBlock：作者 agentd、kind=brief，正文三反引号转义）；
// seq 写 0，因为简报不是某条账本事件。Folder 来自本机项目登记（操作者写入），留在骨架里。
// 返回：材料错误原样透传（项目无本机位置 → errNoLocalProject 包装文本；卡不存在 → 包
// ledger.ErrNotFound）。纯读：不写账本、不打日志（成败由边界层记）。
func (s *Server) renderLauncherLetter(card string) (string, error) {
	m, err := s.desktopLaunchMaterial(card, proto.SeatDesktopViaLauncher)
	if err != nil {
		return "", err
	}
	return proto.DesktopWakeMarker + " 请替我开一场协调者会话。\n\n项目根目录：" + m.Folder +
		"\n\n用桌面端会话管理工具在上述目录新建会话，把下面整段简报作为首条消息发出：\n\n" +
		desktopQuoteBlock(desktopQuote{Author: desktopLauncherLetterAuthor, Kind: desktopQuoteKindBrief, Body: m.Brief}), nil
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
// 用精确值表而不是「不含 ':'」之类的形状判据：人与会话身份也可能是裸词（如 actor
// "test"），形状判据会把人打的标误当系统标吞掉（契约 §3.8.1 规则 3）。
func isSystemNeedsHumanActor(actor string) bool {
	return slices.Contains(desktopNeedsHumanSystemActors, actor) ||
		strings.HasPrefix(actor, desktopNeedsHumanSystemActorPrefix)
}

// desktopNeedsHumanWakes 是 needs_human 对桌面端席位的唤醒映射（P5=B，契约 §3.8.1），由
// automationWakeEvents 在调用纯函数 automationWakeEvent 之前对 EvNeedsHuman 分流。判定按
// 契约规则顺序：1 目标 = resolveWakeCard(ev.CardID)，空 → 不唤醒；2 目标须是 bind 席位且
// SeatDesktopOf 命中，否则不唤醒（coordinate 与无记录 bind 维持 B394）；3 actor 是系统组件
// → 不唤醒（本卡失败段与无拉起器标的 actor 都是 agentd，切断回声环，条 187）；4 actor 等于
// 目标席位身份（协调者自打）→ 不唤醒；5 命中 → 一条 WakeTaskTerminal（Card=ev.CardID，
// Summary="needs_human: <reason>"）——复用 WakeTaskTerminal 是为过 keystone.Decide 的种类清单。
// 返回：读目标卡或桌面端记录失败 → 错误（消费循环本轮中止、下轮重读，同会话寻址读失败）；
// 不唤醒的每条判定留 Debug（判定理由可查）。
func (s *Server) desktopNeedsHumanWakes(ev proto.LedgerEvent) ([]keystone.WakeEvent, error) {
	target := s.resolveWakeCard(ev.CardID)
	if target == "" {
		s.log.Debug("needs_human 不唤醒：无可叫醒的席位卡", "seq", ev.Seq, "card", ev.CardID)
		return nil, nil
	}
	card, err := s.ledger.GetCard(target)
	if err != nil {
		return nil, fmt.Errorf("needs_human seq %d 唤醒判定读席位卡 %s: %w", ev.Seq, target, err)
	}
	if card.DriverSource != string(proto.SeatSourceBind) {
		s.log.Debug("needs_human 不唤醒：目标是 coordinate 席位（B394）", "seq", ev.Seq, "card", ev.CardID, "target", target)
		return nil, nil
	}
	_, hasDesktop, err := s.ledger.SeatDesktopOf(target)
	if err != nil {
		return nil, fmt.Errorf("needs_human seq %d 唤醒判定读桌面端记录 %s: %w", ev.Seq, target, err)
	}
	if !hasDesktop {
		s.log.Debug("needs_human 不唤醒：目标 bind 席位无桌面端记录", "seq", ev.Seq, "card", ev.CardID, "target", target)
		return nil, nil
	}
	if isSystemNeedsHumanActor(ev.Actor) {
		s.log.Debug("needs_human 不唤醒：系统组件自打", "seq", ev.Seq, "card", ev.CardID, "actor", ev.Actor)
		return nil, nil
	}
	if ev.Actor == card.DriverSession {
		s.log.Debug("needs_human 不唤醒：席位自打", "seq", ev.Seq, "card", ev.CardID, "actor", ev.Actor)
		return nil, nil
	}
	reason, ok := desktopNeedsHumanReason(ev)
	if !ok {
		s.log.Warn("needs_human 载荷无 reason，摘要按载荷原文", "seq", ev.Seq, "card", ev.CardID)
	}
	s.log.Info("needs_human 唤醒桌面端席位", "seq", ev.Seq, "card", ev.CardID, "target", target, "actor", ev.Actor)
	return []keystone.WakeEvent{{Kind: keystone.WakeTaskTerminal, Card: ev.CardID, Summary: "needs_human: " + reason}}, nil
}

// driverTakeoverChangedSeat 报告一条 driver_takeover 是否真的换了卡上席位（review m4）：
// 换绑（RebindSeat）载荷 to = 新席位身份；运行锁过期抢占（AcquireRunLock）复用同一事件类型，
// 载荷 to 却是运行锁持有者，席位并没变——清失败段只该发生在前者。判据：载荷 to 等于卡上当前
// DriverSession。之后若又换绑，后一条 takeover 自会命中，故读「当前」而非「事件当时」不漏清。
// 载荷解不开或读卡失败 → Warn 并按「换了」处理：多清一次段的代价是同段可能多落一条
// needs_human，少清的代价是新席位继承旧退避窗，前者是安全方向。
func (s *Server) driverTakeoverChangedSeat(ev proto.LedgerEvent) bool {
	var p struct {
		To string `json:"to"`
	}
	if err := json.Unmarshal(ev.Payload, &p); err != nil {
		s.log.Warn("driver_takeover 载荷解码失败，按席位已变清失败段", "seq", ev.Seq, "card", ev.CardID, "cause", err)
		return true
	}
	card, err := s.ledger.GetCard(ev.CardID)
	if err != nil {
		s.log.Warn("driver_takeover 读卡失败，按席位已变清失败段", "seq", ev.Seq, "card", ev.CardID, "cause", err)
		return true
	}
	if p.To != card.DriverSession {
		s.log.Debug("driver_takeover 未换席位（运行锁抢占），不清失败段", "seq", ev.Seq, "card", ev.CardID, "to", p.To)
		return false
	}
	return true
}

// desktopWakeStallEntry 是一张卡当前「桌面端失败段」的进程内状态（段 = 两次成功之间的
// 连续失败）。受 Server.automationMu 保护；agentd 重启清零（契约 §3.7：重启后同段可能
// 再多落一条 needs_human，可接受）。
type desktopWakeStallEntry struct {
	Failures int       // 本段连续失败次数；0→1 的那一次落 needs_human
	RetryAt  time.Time // 「信未落盘」后下一次重试的最早时刻（desktopClock 读数）；零值 = 不限
	Round    string    // 本段 wake_round 轮次标识：段内重试复用，转交失败行一段一行（B393 一轮一组）
}

// desktopWakeRetryBase / desktopWakeRetryMax 是「信未落盘」重试退避（契约 §11.3 交 plan 定）：
// 第 n 次连续失败后至少等 base·2^(n-1)，封顶 max。为什么要退避：转交对端不可达时每次
// 尝试要吃满拨号超时（client.dialTimeout=10s），消费循环是单协程，2s 节拍下会把所有卡的
// 唤醒拖慢；退避期间认领留在飞（消费循环每轮重新认领即续租），事件不标 seen、不丢。
const (
	desktopWakeRetryBase = 2 * time.Second
	desktopWakeRetryMax  = time.Minute
)

// desktopWakeRetryDelay 返回第 failures 次连续「未落盘」失败后的退避时长。
func desktopWakeRetryDelay(failures int) time.Duration {
	d := desktopWakeRetryBase
	for i := 1; i < failures && d < desktopWakeRetryMax; i++ {
		d *= 2
	}
	return min(d, desktopWakeRetryMax)
}

// desktopWakeRound 返回卡当前失败段的 wake_round 轮次标识；不在失败段返回 ""。
func (s *Server) desktopWakeRound(card string) string {
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	return s.desktopWakeStall[card].Round
}

// desktopWakeRetryPending 报告卡是否处在「信未落盘」退避窗内，并返回窗口截止时刻。
func (s *Server) desktopWakeRetryPending(card string) (time.Time, bool) {
	now := s.desktopClock()
	s.automationMu.Lock()
	defer s.automationMu.Unlock()
	until := s.desktopWakeStall[card].RetryAt
	return until, !until.IsZero() && now.Before(until)
}

// recordDesktopWakeFailure 把一次桌面端唤醒失败（desktopUndelivered 或 desktopUnopened）
// 计入卡的失败段：Failures+1；首次记下本段轮次 roundID；未落盘失败设退避窗。计数从 0
// 变 1 的那一次落 MarkNeedsHuman(card, desktopWakeFailReasonPrefix+<原因>, "agentd")——
// needs_human 只由认领本批的发起端在这里写（契约 §3.7 单一原则），不经 recordAdmissionStall、
// 不动 automationStall（条 173/174）。落标失败：Error + 计数退回 0，让下一次失败重试落标
// （不让一次账本抖动把整段变成无人可见）。
func (s *Server) recordDesktopWakeFailure(card, roundID string, cause error) {
	undelivered := desktopUndelivered(cause)
	retryAt := time.Time{}
	s.automationMu.Lock()
	if s.desktopWakeStall == nil {
		s.desktopWakeStall = make(map[string]desktopWakeStallEntry)
	}
	e := s.desktopWakeStall[card]
	e.Failures++
	if e.Round == "" {
		e.Round = roundID
	}
	e.RetryAt = time.Time{}
	if undelivered {
		retryAt = s.desktopClock().Add(desktopWakeRetryDelay(e.Failures))
		e.RetryAt = retryAt
	}
	s.desktopWakeStall[card] = e
	s.automationMu.Unlock()

	s.log.Warn("桌面端唤醒失败，计入失败段", "card", card, "failures", e.Failures,
		"undelivered", undelivered, "retry_at", retryAt, "round_id", e.Round, "cause", cause)
	if e.Failures != 1 {
		return
	}
	reason := desktopWakeFailReasonPrefix + cause.Error()
	if err := s.ledger.MarkNeedsHuman(card, reason, "agentd"); err != nil {
		s.log.Error("桌面端失败段落 needs_human 失败，下一次失败重试落标", "card", card, "cause", err)
		s.automationMu.Lock()
		if cur := s.desktopWakeStall[card]; cur.Failures == 1 {
			cur.Failures = 0
			s.desktopWakeStall[card] = cur
		}
		s.automationMu.Unlock()
		return
	}
	s.log.Error("桌面端失败段已落 needs_human", "card", card, "round_id", e.Round)
}

// clearDesktopWakeStall 结束该卡的桌面端失败段：一次唤醒成功后（契约 §3.7：段 = 两次成功
// 之间），或席位换绑 / 桌面端记录重写事件流经消费循环时（契约 r12 第 4 条末句：旧段的退避窗、
// 已落标计数与段轮次不得被新席位继承）。reason 只进日志，区分两种结束。对不在失败段的卡
// 是空操作。
func (s *Server) clearDesktopWakeStall(card, reason string) {
	s.automationMu.Lock()
	e, ok := s.desktopWakeStall[card]
	delete(s.desktopWakeStall, card)
	s.automationMu.Unlock()
	if ok {
		s.log.Info("桌面端失败段结束", "card", card, "reason", reason, "failures", e.Failures, "round_id", e.Round)
	}
}

// openViaDesktopLauncher 是拉起器代开（契约 §3.9 POST 行、§3.9.1；故事 5、6）：空座前置 →
// 读项目拉起器 → deliverDesktopLetter(launcher.Machine, {cli_session, desktop_session, card})。
//
// 返回（按判定顺序）：卡不存在 → 包 ledger.ErrNotFound；席位有人（DriverSession 或
// DriverSource 非空，判据同 card coordinate）→ *desktopSeatOccupiedError（先于拉起器查找，
// 零写信零打标，条 261）；读拉起器登记失败 → *desktopLaunchInternalError（不打标，500）；无登记 →
// MarkNeedsHuman(card, desktopNoLauncherReason, "agentd") 恰一次后返回 errNoDesktopLauncher
// （条 144–147）；项目在写信机器上无登记位置 → errors.Is(errNoLocalProject)，不打标（条 220–221）；
// 其余投递失败（信未落盘，或信已落盘但 continue 没发出）→ 落一条 needs_human（reason 以
// desktopWakeFailReasonPrefix 开头；信已落盘时附「信已在拉起器信箱，勿重复代开」，契约 r12
// 第 6 条）后返回 *desktopLaunchFailedError（条 158、249–250）；打标本身失败 →
// *desktopLaunchInternalError（500，不冒充已打标）。成功 → {Launcher: 拉起器桌面端会话 id,
// Machine: 拉起器登记的机器原值（r12 第 8 条）, HandledBy: 实际写信的机器}。
// 注意：needs_human 只由本函数（发起端）落；不经 desktopWakeStall / recordAdmissionStall——
// 代开是一次请求一次结果，不进消费循环的失败段；不经 keystone/scheduling（条 162、254）。
func (s *Server) openViaDesktopLauncher(ctx context.Context, card string) (proto.DesktopLauncherOpenResp, error) {
	var zero proto.DesktopLauncherOpenResp
	c, err := s.ledger.GetCard(card)
	if err != nil {
		return zero, err
	}
	if c.DriverSession != "" || c.DriverSource != "" {
		return zero, &desktopSeatOccupiedError{card: card}
	}
	launcher, ok, err := s.ledger.DesktopLauncherOf(c.Project)
	if err != nil {
		s.log.Error("拉起器代开读拉起器登记失败（不打标）", "card", card, "project", c.Project, "cause", err)
		return zero, &desktopLaunchInternalError{err: fmt.Errorf("读项目 %s 的拉起器登记: %w", c.Project, err)}
	}
	if !ok {
		if err := s.ledger.MarkNeedsHuman(card, desktopNoLauncherReason, "agentd"); err != nil {
			s.log.Error("无拉起器且落 needs_human 失败", "card", card, "project", c.Project, "cause", err)
			return zero, &desktopLaunchInternalError{err: fmt.Errorf("%s；落 needs_human 失败: %w", desktopNoLauncherReason, err)}
		}
		s.log.Warn("项目未登记拉起器，已落 needs_human", "card", card, "project", c.Project)
		return zero, errNoDesktopLauncher
	}
	req := proto.DesktopDeliverReq{CLISession: launcher.CLISession, DesktopSession: launcher.DesktopSession, Card: card}
	resp, err := s.deliverDesktopLetter(ctx, launcher.Machine, req)
	var unknown *desktopDeliveryUnknownError
	switch {
	case errors.Is(err, errNoLocalProject):
		return zero, err
	case errors.As(err, &unknown):
		return zero, s.launcherDeliveryFailed(card, launcher.Machine,
			"投递结果未知（"+unknown.Error()+"）；若信已到拉起器信箱则勿重复代开", nil)
	case err != nil:
		return zero, s.launcherDeliveryFailed(card, launcher.Machine, "来信未投递", err)
	case !resp.Opened:
		return zero, s.launcherDeliveryFailed(card, launcher.Machine,
			"来信已落盘（"+resp.HandledBy+" 信箱）但 continue 深链未发出；"+desktopLauncherLetterKeptHint, nil)
	}
	return proto.DesktopLauncherOpenResp{Launcher: launcher.DesktopSession, Machine: launcher.Machine,
		HandledBy: resp.HandledBy}, nil
}

// desktopLauncherLetterKeptHint 是「信已落盘、continue 未发出」时 502 error 与 needs_human
// reason 的尾句（契约 r12 第 6 条）：信大概率会被活着的拉起器轮询取走，重复代开会开出两场会话。
const desktopLauncherLetterKeptHint = "信已在拉起器信箱，勿重复代开"

// launcherDeliveryFailed 为拉起器投递失败落一条 needs_human（actor "agentd"——系统组件，
// 不会叫醒桌面端席位，契约 §3.8.1 规则 3）并返回 *desktopLaunchFailedError；打标失败时返回
// *desktopLaunchInternalError（端点据此回 500 而非 502，避免「说已打标其实没打」）。
// 参数：machine 拉起器登记的机器（空串记作「本机」）；what 失败描述（骨架文本）；cause 底层原因
// （可为 nil，信已落盘、continue 未发出时无底层错误）。
func (s *Server) launcherDeliveryFailed(card, machine, what string, cause error) error {
	where := machine
	if where == "" {
		where = "本机"
	}
	reason := desktopWakeFailReasonPrefix + "拉起器（" + where + "）代开卡 " + card + " 失败，" + what
	if cause != nil {
		reason += "：" + launcherFailureCauseText(cause)
	}
	if err := s.ledger.MarkNeedsHuman(card, reason, "agentd"); err != nil {
		s.log.Error("拉起器投递失败且落 needs_human 失败", "card", card, "machine", machine, "reason", reason, "cause", err)
		return &desktopLaunchInternalError{err: fmt.Errorf("%s；落 needs_human 失败: %w", reason, err)}
	}
	s.log.Error("拉起器投递失败，已落 needs_human", "card", card, "machine", machine, "reason", reason)
	return &desktopLaunchFailedError{reason: reason, cause: cause}
}

// launcherFailureCauseText 是投递失败原因进 needs_human reason 的文本（review minor 2）：收件机回了
// 非 2xx → 「拉起器所在机器回 <码>：<error 原文>」（client.ServerErrorText 去掉 `状态码 N: {"error":…}`
// 外壳，人读到的是收件机的可行动文案）；否则 cause.Error() 原样（不可达、取客户端失败等）。
func launcherFailureCauseText(cause error) string {
	text, ok := client.ServerErrorText(cause)
	if !ok {
		return cause.Error()
	}
	if code, ok := client.StatusCode(cause); ok {
		return fmt.Sprintf("拉起器所在机器回 %d：%s", code, text)
	}
	return text
}

// desktopSeatOccupiedError 是代开端点的空座前置拒绝（r7 条 261）：文本逐字为
// 「卡 <id> 已有人坐，换人用 handoff card rebind <id> --self 或 --launch」。
type desktopSeatOccupiedError struct{ card string }

func (e *desktopSeatOccupiedError) Error() string {
	return "卡 " + e.card + " 已有人坐，换人用 handoff card rebind " + e.card + " --self 或 --launch"
}

// desktopLaunchInternalError 是代开路径上的服务端故障：读拉起器登记失败，或该落的 needs_human
// 没落成（契约 r12 第 6 条）。端点据此显式回 500——不经 ledgerErr（底层错误可能包
// ErrNotFound/CAS，会被映射成 404/409），也不回 409/502（那两个码对人意味着「已打标」）。
// Error() 透传底层原因；Unwrap 供日志与上层判别。
type desktopLaunchInternalError struct{ err error }

func (e *desktopLaunchInternalError) Error() string { return e.err.Error() }

func (e *desktopLaunchInternalError) Unwrap() error { return e.err }

// desktopLaunchFailedError 是「拉起器投递失败且已落 needs_human」：Error() 等于落标的
// reason（端点 502 的 error 与卡上等人原因逐字相同）；Unwrap 到底层原因（可能为 nil）。
type desktopLaunchFailedError struct {
	reason string
	cause  error
}

func (e *desktopLaunchFailedError) Error() string { return e.reason }

func (e *desktopLaunchFailedError) Unwrap() error { return e.cause }

// handleDesktopLauncherOpen 是 POST /api/cards/{id}/coordinator/desktop-launch（契约 §3.9，
// 体 `{}` 不读）：调 openViaDesktopLauncher 并映射状态码。注册时只套 withLedger。
// 响应：200 DesktopLauncherOpenResp（三键恒出）；404 卡不存在；409 席位有人 / 无拉起器（已落
// needs_human）/ 项目无本机位置（本机或收件机，error 逐字透传）；502 投递失败（已落 needs_human，
// error = 落标 reason）；500 读拉起器登记失败或打标失败（显式 writeErr，不经 ledgerErr——打标
// 失败的底层错误可能包 ErrNotFound/CAS，经 ledgerErr 会被映射成 404/409，契约 r12 第 6 条）。
// 非 2xx 体一律 {"error": <原文>}。
func (s *Server) handleDesktopLauncherOpen(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.log.Info("收到拉起器代开请求", "card", id)
	resp, err := s.openViaDesktopLauncher(r.Context(), id)
	var occupied *desktopSeatOccupiedError
	var internal *desktopLaunchInternalError
	var failed *desktopLaunchFailedError
	switch {
	case err == nil:
		s.log.Info("拉起器代开已投递", "card", id, "launcher", resp.Launcher,
			"machine", resp.Machine, "handled_by", resp.HandledBy)
		writeJSON(w, http.StatusOK, resp)
	case errors.As(err, &internal):
		s.log.Error("拉起器代开服务端故障", "card", id, "cause", err)
		writeErr(w, http.StatusInternalServerError, err)
	case errors.As(err, &failed):
		writeErr(w, http.StatusBadGateway, err)
	case errors.As(err, &occupied), errors.Is(err, errNoDesktopLauncher), errors.Is(err, errNoLocalProject):
		s.log.Warn("拉起器代开被拒", "card", id, "cause", err)
		writeErr(w, http.StatusConflict, err)
	default:
		s.log.Warn("拉起器代开失败", "card", id, "cause", err)
		ledgerErr(w, err)
	}
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
