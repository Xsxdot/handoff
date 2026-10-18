// seat_mode.go —— B435 席位驱动模式的 wire 词表（契约 docs/superpowers/specs/b435-contract.md ③）。
//
// 职责：定义席位驱动模式的唯一字面量。push = agentd 看到命中消息起一轮 print 回合；
// pull = 席位持有者自己跑 wait 收件。一切「起不起回合 / 谁来交付」的判定只读本字段，
// 不读席位来源词（SeatSource 只作记录）。
//
// 边界：本文件只有词表与字面量解析；落账列（cards.driver_mode）、CLI --mode 缺省、
// 消费循环分叉在各自子系统落码。
package proto

import "fmt"

// SeatMode 是卡上协调者席位的驱动模式。空串只在空座上合法。
type SeatMode string

const (
	// SeatModePush：agentd 驱动。命中该席位的消息由消费循环认领并起 print 回合。
	SeatModePush SeatMode = "push"
	// SeatModePull：持有者自驱。agentd 对该席位零回合，消息由 card wait 交付。
	SeatModePull SeatMode = "pull"
)

// ParseSeatMode 把 CLI/wire 上的字面量解析成 SeatMode；只认 push 与 pull 两值
// （大小写敏感、不去空白——wire 值逐字比较）。错误信息可行动（列出合法值）。
func ParseSeatMode(raw string) (SeatMode, error) {
	switch SeatMode(raw) {
	case SeatModePush, SeatModePull:
		return SeatMode(raw), nil
	default:
		return "", fmt.Errorf("席位模式非法 %q：只认 push 或 pull", raw)
	}
}
