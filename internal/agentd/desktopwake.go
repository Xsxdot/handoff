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
// 不经 keystone（keystone.Wake / LaunchForCard 对桌面端席位零调用）。本文件只有
// 签名与常量；行为在实现节点落码（TODO(B434)）。契约见
// docs/superpowers/specs/b434-contract.md §3.5–§3.8。
package agentd

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/Xsxdot/handoff/internal/keystone"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
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
	Folder     string // 项目根目录（本机）
	Via        string // proto.SeatDesktopVia*：写进 bind 命令行的 --via
}

// desktopOpener 把一条 claude:// 深链交给本机桌面端（生产为 macOS `open`）。
// 测试注入 spy；nil 视为「本机不能发深链」。
type desktopOpener func(url string) error

// desktopLaunchMaterial 按卡产出拉起材料。项目无本机位置 → errNoLocalProject。
func (s *Server) desktopLaunchMaterial(card, via string) (proto.DesktopLaunchMaterial, error) {
	_, _ = card, via
	return proto.DesktopLaunchMaterial{}, proto.ErrDesktopNotImplemented // TODO(B434)
}

// desktopBrief 渲染人尺度简报：以 proto.DesktopWakeMarker 开头，含卡号/标题/
// 有效基线/会话 id/项目根目录与三步指引；不含「一次性回合」。
func desktopBrief(in desktopBriefInput) string {
	_ = in
	return "" // TODO(B434)
}

// desktopLetter 把一批唤醒事件投影成一封人可读的来信（以 DesktopWakeMarker 开头），
// 内容与 session wait / card wait 同源（契约 §3.6）：会话消息给命中条全文、
// 引用条（s.rooms.MessageReference）与该席位在该会话的未读数
// （s.rooms.ListSessions(seat) 的 Unread）；工单给 task_mirrored 载荷原文。
// 只负责取数并组装 desktopLetterSection：第三方原文一律放进 desktopQuote，
// 交 renderDesktopLetter 渲染围栏与作者名册（P9=丁，契约条 263–275）。
// 读引用/未读失败上抛（宁可不发信，不发残信）。
func (s *Server) desktopLetter(card, seat string, evs []keystone.WakeEvent, raws []proto.LedgerEvent) (string, error) {
	_, _, _, _ = card, seat, evs, raws
	return "", proto.ErrDesktopNotImplemented // TODO(B434)
}

// writeDesktopLetter 把 letter 原子写进 home 下的信箱（同目录临时文件 + rename），
// 返回最终路径。文件名 <cliSession>.<19 位零填充 UnixNano>.md，临时文件
// .<同名>.tmp（契约 §3.6，P1=A 已拍板）。
func writeDesktopLetter(home, cliSession, letter string, now time.Time) (string, error) {
	_, _, _, _ = home, cliSession, letter, now
	return "", proto.ErrDesktopNotImplemented // TODO(B434)
}

// claudeContinueURL 构造 claude://code/continue?session=<url 编码的桌面端会话 id>；
// id 不合法即报错。
func claudeContinueURL(desktopSession string) (string, error) {
	_ = desktopSession
	return "", proto.ErrDesktopNotImplemented // TODO(B434)
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
func (s *Server) wakeDesktopSeat(ctx context.Context, card, seat string, desk ledger.SeatDesktop,
	evs []keystone.WakeEvent, raws []proto.LedgerEvent, roundID string) (keystone.RoundResult, error) {
	_, _, _, _, _, _, _ = ctx, card, seat, desk, evs, raws, roundID
	return keystone.RoundResult{}, proto.ErrDesktopNotImplemented // TODO(B434)
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
// 一个反斜杠；1–2 个连续反引号与其它字符原样（条 270–271）。
func escapeDesktopQuoteBody(body string) string {
	_ = body
	return "" // TODO(B434)
}

// sanitizeDesktopQuoteAuthor 把 actor 中的空格、\t、\r、\n 与反引号各替换为 "_"，
// 保证开栏行单行且不可被提前闭栏（条 272）。
func sanitizeDesktopQuoteAuthor(actor string) string {
	_ = actor
	return "" // TODO(B434)
}

// desktopQuoteBlock 渲染一个围栏块：开栏行、转义后正文、闭栏行（条 267）。
func desktopQuoteBlock(q desktopQuote) string {
	_ = q
	return "" // TODO(B434)
}

// renderDesktopLetter 是来信纯渲染层：首行 head（以 DesktopWakeMarker 开头）→
// 作者名册行 → 各段 → 尾行 tail。desktopLetter 负责取数并组装 sections，
// 不自行拼接任何第三方文本（条 263–275）。
func renderDesktopLetter(head string, sections []desktopLetterSection, tail string) string {
	_, _, _ = head, sections, tail
	return "" // TODO(B434)
}
