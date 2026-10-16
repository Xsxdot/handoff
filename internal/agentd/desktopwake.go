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

// desktopLetter 把一批唤醒事件投影成一封人可读的来信（以 DesktopWakeMarker 开头）。
func desktopLetter(card string, evs []keystone.WakeEvent, raws []proto.LedgerEvent) string {
	_, _, _ = card, evs, raws
	return "" // TODO(B434)
}

// writeDesktopLetter 把 letter 原子写进 home 下的信箱（同目录临时文件 + rename），
// 返回最终路径。文件命名见契约 §3.6（待裁决 P1）。
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

// deliverDesktopLetter 在本机投递一封信并发 continue（写信 + opener）。拉起器在
// 异机时的投递通道待裁决（契约 P3：推荐经 clientForTarget(machine) 的
// POST /api/desktop/deliver 一跳转交）；席位唤醒的异机路径不走本函数。
func (s *Server) deliverDesktopLetter(ctx context.Context, machine string, req proto.DesktopDeliverReq) (proto.DesktopDeliverResp, error) {
	_, _, _ = ctx, machine, req
	return proto.DesktopDeliverResp{}, proto.ErrDesktopNotImplemented // TODO(B434)
}

// wakeDesktopSeat 是 wakeCoordinatorRoundID 对「有桌面端记录的 bind 席位」的
// 分支：desk.Machine 为本机 → 渲染来信 → deliverDesktopLetter；异机 →
// transferCoordinatorWake(desk.Machine)。投递失败 → needs_human（前缀
// desktopWakeFailReasonPrefix），信若已落盘则留在信箱。
func (s *Server) wakeDesktopSeat(ctx context.Context, card, seat string, desk ledger.SeatDesktop,
	evs []keystone.WakeEvent, raws []proto.LedgerEvent, roundID string) (keystone.RoundResult, error) {
	_, _, _, _, _, _, _ = ctx, card, seat, desk, evs, raws, roundID
	return keystone.RoundResult{}, proto.ErrDesktopNotImplemented // TODO(B434)
}

// openViaDesktopLauncher 是拉起器代开：读项目拉起器 → 投递装着拉起材料
// （via=launcher）的来信。无登记 → MarkNeedsHuman(desktopNoLauncherReason) +
// errNoDesktopLauncher。
func (s *Server) openViaDesktopLauncher(ctx context.Context, card string) (proto.DesktopLauncherOpenResp, error) {
	_, _ = ctx, card
	return proto.DesktopLauncherOpenResp{}, proto.ErrDesktopNotImplemented // TODO(B434)
}
