// card_seat_desktop.go —— B434 bind 席位的 Claude 桌面端记录：CLI 一侧的入参执法与
// `handoff card seat desktop set` 补写面。
//
// 职责：
//   - prepareSeatDesktop：card bind --desktop-session 与 card seat desktop set 共用的
//     入参执法（席位 cli 段必须是 claude、桌面端会话 id 形状、--via 词表、--machine 缺省），
//     在任何账本写入之前跑完——card bind 是两步提交（拍板 D2），本可当场拒绝的入参若留到
//     第二步才被账本拒，人会得到一个「已坐下、没记 id」的半态。
//   - resolveDesktopMachine：--machine 缺省取 os.Hostname()（契约条 46），多机部署时
//     提示 target 名对齐（契约 §11.2）。launcher register 也用它。
//   - cardSeatDesktopSetCmd（T3）：以卡上当前 driver_session 为 CAS 期望值补写记录。
//
// 边界：只经 ledger.Store 的公开写面写账本；不读不写 seat_bearings；不调 agentd；
// 不改席位真源 cards.driver_session。契约见 b434-contract.md §3.3、§3.10。
package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
)

// desktopSeatCLI 是唯一能挂桌面端记录 / 登记拉起器的席位物种：Claude 桌面端会话只跑
// claude（契约条 60、141）。
const desktopSeatCLI = "claude"

// desktopSeatInput 是 --desktop-session / --machine / --via 的原值与 --machine 是否显式给出。
type desktopSeatInput struct {
	Session        string
	Machine        string
	MachineChanged bool
	Via            string
}

// validSeatDesktopViaFlag 报告 --via 是否在 proto.SeatDesktopVia* 词表内（大小写敏感）。
// 账本写闸也判同一词表；CLI 先判是为了不让词表外的值留到两步提交的第二步才被拒。
func validSeatDesktopViaFlag(via string) bool {
	switch via {
	case proto.SeatDesktopViaPage, proto.SeatDesktopViaLauncher, proto.SeatDesktopViaManual:
		return true
	}
	return false
}

// prepareSeatDesktop 校验桌面端记录入参并补缺省值。
//
// 参数：identity 是将要（bind）或已经（seat desktop set）坐在卡上的席位身份
// cli:<cli>#<id>；in 是 flag 原值。
// 返回：desk 可直接交给 ledger.Store.SetSeatDesktop（BoundAt 留零值，账本取时钟）；
// hint 非空时调用方原样打到 stderr（机器名提示，不影响成败）；err 非 nil 时调用方
// 必须在任何账本写入之前返回。
// 注意：--via 由 flag 缺省值给出 manual（契约条 47）；显式空串按词表外拒绝。
func prepareSeatDesktop(identity string, in desktopSeatInput) (ledger.SeatDesktop, string, error) {
	cli, cliSession, err := proto.ParseSeatIdentity(identity)
	if err != nil {
		return ledger.SeatDesktop{}, "", fmt.Errorf("解析席位身份: %w", err)
	}
	if cli != desktopSeatCLI {
		return ledger.SeatDesktop{}, "", fmt.Errorf("--desktop-session 只能用于 claude 会话的席位，当前席位物种是 %q", cli)
	}
	if err := checkMailboxKey(cliSession); err != nil {
		return ledger.SeatDesktop{}, "", err
	}
	if err := proto.ValidateDesktopSessionID(in.Session); err != nil {
		return ledger.SeatDesktop{}, "", fmt.Errorf("--desktop-session 非法: %w", err)
	}
	if !validSeatDesktopViaFlag(in.Via) {
		return ledger.SeatDesktop{}, "", fmt.Errorf("--via 只接受 page、launcher、manual，得到 %q", in.Via)
	}
	machine, hint, err := resolveDesktopMachine(in.Machine, in.MachineChanged)
	if err != nil {
		return ledger.SeatDesktop{}, "", err
	}
	return ledger.SeatDesktop{Session: in.Session, Machine: machine, Via: in.Via}, hint, nil
}

// checkMailboxKey 执法席位 CLI 会话 id 可作信箱文件名的一段（契约 r10 第 5 条，与
// internal/ledger 拉起器写闸同一规则）：不含 / \ # 与空白、不以 . 开头。
//
// 为什么 CLI 要再判：桌面端唤醒把信写到 ~/.handoff/desktop-wake/<CLI id>.<ns>.md，含路径
// 分隔符的 id 会写出信箱目录、以 . 开头的 id 会落成隐藏文件或 ..；SetSeatDesktop 不判
// 席位 cli 段与会话 id（只判桌面端 id），不在这里拦就会坐下并记上一个永远收不到信的席位。
// # 与空白已被席位编码规则拒绝，这里仍列出是为了与拉起器写闸的规则逐条对齐。
// 注意：错误文案不带 id 原值（会话 id 不进日志，沿 currentSeatIdentity 约定）。
func checkMailboxKey(cliSession string) error {
	if cliSession == "" || strings.ContainsAny(cliSession, "/\\#") ||
		strings.IndexFunc(cliSession, unicode.IsSpace) >= 0 || strings.HasPrefix(cliSession, ".") {
		return fmt.Errorf("当前席位的 CLI 会话 id 不可作信箱文件名（不得含 / \\ # 或空白，不得以 . 开头），不能挂桌面端记录")
	}
	return nil
}

// resolveDesktopMachine 决定桌面端记录 / 拉起器登记的机器名。
//
// 参数：flagValue 是 --machine 原值；changed 表示命令行显式给了 --machine（显式空串照收，
// 账本里空串 = 本机）。
// 返回：machine 为显式值或 os.Hostname()；hint 非空表示缺省取了 hostname，而本机配置
// 有 targets 却没有同名项——其它 agentd 异机转交按 target 名寻址（契约 §11.2），名字
// 对不上转交会失败；err 仅在未显式给值且取不到 hostname 时返回。
// 注意：只提示不报错；单机部署（无 targets）不提示。
func resolveDesktopMachine(flagValue string, changed bool) (string, string, error) {
	if changed {
		return flagValue, "", nil
	}
	host, err := os.Hostname()
	if err != nil {
		return "", "", fmt.Errorf("取本机 hostname 作 --machine 缺省值失败（可显式给 --machine）: %w", err)
	}
	cfg := loadCLIConfig()
	if len(cfg.Targets) > 0 {
		if _, ok := cfg.Targets[host]; !ok {
			return host, fmt.Sprintf("提示：--machine 缺省取本机 hostname %q；其它机器的 agentd 按 target 名把唤醒转交到这里，若它们配置里本机的 target 名不是 %q，请用 --machine <那个名字> 重新登记", host, host), nil
		}
	}
	return host, "", nil
}

// seatDesktopSetRemedy 拼 card bind 第二步失败时给人的补救命令。
// 为什么原样带 --via：补写若降成缺省 manual，seat_desktop_set 事件就丢了「这场会话是
// 页面 / 拉起器开出来的」来源信息（故事 9；契约 r10 第 5 条）；manual 即缺省，省略。
func seatDesktopSetRemedy(id string, desk ledger.SeatDesktop) string {
	cmd := fmt.Sprintf("handoff card seat desktop set %s --desktop-session %s", id, desk.Session)
	if desk.Via != proto.SeatDesktopViaManual {
		cmd += " --via " + desk.Via
	}
	return cmd
}

// cardSeatDesktopSession / cardSeatDesktopMachine / cardSeatDesktopVia 是 seat desktop set 的 flag。
var cardSeatDesktopSession, cardSeatDesktopMachine, cardSeatDesktopVia string

var cardSeatDesktopCmd = &cobra.Command{
	Use:   "desktop",
	Short: "bind 席位的 Claude 桌面端记录维护",
}

// cardSeatDesktopSetCmd 为卡上已有的 claude bind 席位写入（覆盖）桌面端记录。
//
// 用途：card bind 两步提交第二步失败后的补救；card rebind --self 之后新会话重新登记
// 自己的桌面端 id（换绑在同事务里删了旧记录，契约 §3.10）。
// 注意：CAS 期望值取卡上当前 driver_session——调用方不必是席位本人（人可在终端代填），
// 但席位物种必须是 claude，与 card bind --desktop-session 同一道门。
var cardSeatDesktopSetCmd = &cobra.Command{
	Use:   "set <id> --desktop-session <local_…>",
	Short: "为卡上已有的 claude bind 席位写入桌面端会话 id（CAS：以当前席位为期望值）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		id := args[0]
		if cardSeatDesktopSession == "" {
			return fmt.Errorf("--desktop-session 必填")
		}
		slog.Default().Info("CLI 补写桌面端记录入口", "card", id, "desktop_session", cardSeatDesktopSession)
		st, err := openLedger()
		if err != nil {
			slog.Default().Warn("CLI 补写桌面端记录打开账本失败", "card", id, "cause", err)
			return err
		}
		defer st.Close()
		card, err := st.GetCard(id)
		if err != nil {
			slog.Default().Warn("CLI 补写桌面端记录读卡失败", "card", id, "cause", err)
			return fmt.Errorf("读取卡 %s: %w", id, err)
		}
		if card.DriverSession == "" {
			err := fmt.Errorf("卡 %s 空座：先 handoff card bind %s --desktop-session <local_…> 坐下", id, id)
			slog.Default().Warn("CLI 补写桌面端记录被拒：空座", "card", id)
			return err
		}
		desk, hint, err := prepareSeatDesktop(card.DriverSession, desktopSeatInput{
			Session: cardSeatDesktopSession, Machine: cardSeatDesktopMachine,
			MachineChanged: cmd.Flags().Changed("machine"), Via: cardSeatDesktopVia,
		})
		if err != nil {
			slog.Default().Warn("CLI 补写桌面端记录拒绝入参", "card", id, "cause", err)
			return fmt.Errorf("补写卡 %s 桌面端记录: %w", id, err)
		}
		if hint != "" {
			fmt.Fprintln(cmd.ErrOrStderr(), hint)
		}
		if err := st.SetSeatDesktop(id, card.DriverSession, desk); err != nil {
			slog.Default().Warn("CLI 补写桌面端记录失败", "card", id, "cause", err)
			return fmt.Errorf("补写卡 %s 桌面端记录: %w", id, err)
		}
		slog.Default().Info("CLI 补写桌面端记录完成", "card", id,
			"desktop_session", desk.Session, "machine", desk.Machine, "via", desk.Via)
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	cardSeatDesktopSetCmd.Flags().StringVar(&cardSeatDesktopSession, "desktop-session", "", "Claude 桌面端会话 id（local_…，必填）")
	cardSeatDesktopSetCmd.Flags().StringVar(&cardSeatDesktopMachine, "machine", "", "桌面端会话所在机器（缺省本机 hostname）")
	cardSeatDesktopSetCmd.Flags().StringVar(&cardSeatDesktopVia, "via", proto.SeatDesktopViaManual, "这场会话怎么开出来的：page|launcher|manual")
	cardSeatDesktopCmd.AddCommand(cardSeatDesktopSetCmd)
	cardSeatCmd.AddCommand(cardSeatDesktopCmd)
}
