// seat_desktop.go —— B434 桌面端席位记录与项目拉起器登记（Ticket 0 空壳）。
//
// 职责：bind 席位的「Claude 桌面端会话 id + 所在机器」与每项目至多一个的
// 拉起器登记。两者都是账本行，与 cards.driver_session 的席位真源分开存放：
// 席位真源不变（bind / cli:claude#<CLI 会话 id>），本记录只回答「怎样把这场
// 会话叫醒」。不复用 seat_bearings：那张表的存在即意味着 coordinate 席位
// （B389 规则 4，见 binding.go#validateSeatBearing），见契约拍板 D1。
//
// 边界：本文件只有类型与方法签名；DDL、与 BindSeat/RebindSeat/clearSeatTx 的
// 生命周期联动、事件落账在实现节点落码（TODO(B434)）。契约见
// docs/superpowers/specs/b434-contract.md §3.3–§3.4。
package ledger

import (
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
)

// SeatDesktop 是 bind 席位的桌面端记录：这场 claude 对话在 Claude 桌面端里的
// 会话 id 与所在机器，以及它是怎么被开出来的。
type SeatDesktop struct {
	Session string // 桌面端会话 id（local_…），proto.ValidateDesktopSessionID 校验
	Machine string // 所在机器：与 SeatBearing.Machine 同一值域（空串/本机名/登记的 target 名）
	Via     string // proto.SeatDesktopVia*
	BoundAt time.Time
}

// SetSeatDesktop 为卡上已有的 bind 席位写入桌面端记录（upsert）。CAS 断言卡上
// 席位身份等于 expect，且来源为 bind；同事务落恰一条 EvSeatDesktopSet。
// coordinate 席位、空座、身份不符一律显式报错，不静默忽略。
func (s *Store) SetSeatDesktop(id, expect string, desk SeatDesktop) error {
	_, _, _ = id, expect, desk
	return proto.ErrDesktopNotImplemented // TODO(B434)
}

// SeatDesktopOf 读该卡的桌面端记录；无记录返回 ok=false 且不报错。
// 它不参与「是不是空座」的判定——空座只由 cards 两列判。
func (s *Store) SeatDesktopOf(id string) (SeatDesktop, bool, error) {
	_ = id
	return SeatDesktop{}, false, proto.ErrDesktopNotImplemented // TODO(B434)
}

// DesktopLauncher 是某项目的拉起器登记：一场由人开过的普通桌面端会话，
// 人不在时由控制面写信 + continue 唤醒它代开协调者会话。
type DesktopLauncher struct {
	Project        string // 卡的 project 名（cards.project 同值域）
	DesktopSession string // 桌面端会话 id（local_…）
	CLISession     string // 该会话的 CLAUDE_CODE_SESSION_ID（信箱文件名用它）
	Machine        string // 所在机器，值域同 SeatDesktop.Machine
	RegisteredAt   time.Time
	Actor          string
}

// RegisterDesktopLauncher 登记（覆盖）项目的拉起器；每项目至多一条。
func (s *Store) RegisterDesktopLauncher(l DesktopLauncher) error {
	_ = l
	return proto.ErrDesktopNotImplemented // TODO(B434)
}

// DesktopLauncherOf 读项目的拉起器；无登记返回 ok=false 且不报错。
func (s *Store) DesktopLauncherOf(project string) (DesktopLauncher, bool, error) {
	_ = project
	return DesktopLauncher{}, false, proto.ErrDesktopNotImplemented // TODO(B434)
}

// UnregisterDesktopLauncher 删除项目的拉起器登记（幂等）。
func (s *Store) UnregisterDesktopLauncher(project string) error {
	_ = project
	return proto.ErrDesktopNotImplemented // TODO(B434)
}

// ListDesktopLaunchers 列出全部拉起器登记，按 project 升序。
func (s *Store) ListDesktopLaunchers() ([]DesktopLauncher, error) {
	return nil, proto.ErrDesktopNotImplemented // TODO(B434)
}
