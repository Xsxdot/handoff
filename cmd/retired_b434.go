// retired_b434.go —— B435 X：B434 退役命令与 flag 的墓碑表（plan b435.5 §0 Q3 甲；spec S7
// 「不存在或报改名提示」；拆解 §5.5「改名提示可行动」）。
//
// 职责：四处墓碑集中登记于一张表——隐藏命令 `handoff launcher …`、`handoff card seat desktop …`，
// 隐藏 flag `card bind --desktop-session/--machine/--via`、`card coordinate --desktop`。命中即非 0
// 退出，stderr 恰一行（cobra 的 Error: 行）给出指向 `session sit --desktop-session` 的固定提示。
//
// 边界：墓碑在任何账本打开、任何 HTTP 之前返回（命令墓碑在 RunE 首行、flag 墓碑在 PreRunE）；
// `--help` 不列（Hidden / MarkHidden）。本文件只为提示存在，日后整体删除即可（不被其它代码引用）。
package cmd

import (
	"fmt"
	"log/slog"

	"github.com/spf13/cobra"
)

// retiredB434Hint 是全部 B434 墓碑共用的提示正文（Q3 拍板稿）。
const retiredB434Hint = "已退役（B435）：桌面端会话改为在项目房间驻场——handoff session sit <项目房间> --as <身份> --desktop-session <local_…>；房间号见 handoff project room <项目>"

// retiredB434Command 是一条被退役的子命令：挂在 parent 下、名为 name 的隐藏命令。
type retiredB434Command struct {
	parent *cobra.Command
	name   string
	what   string // 提示里的命令全称
}

// retiredB434Flag 是一条被退役的 flag：仍在 cmd 上注册（否则 cobra 先报 unknown flag），但隐藏。
type retiredB434Flag struct {
	cmd     *cobra.Command
	name    string
	boolean bool
	what    string
}

var retiredB434Commands = []retiredB434Command{
	{parent: rootCmd, name: "launcher", what: "handoff launcher"},
	{parent: cardSeatCmd, name: "desktop", what: "handoff card seat desktop"},
}

var retiredB434Flags = []retiredB434Flag{
	{cmd: cardBindCmd, name: "desktop-session", what: "handoff card bind --desktop-session"},
	{cmd: cardBindCmd, name: "machine", what: "handoff card bind --machine"},
	{cmd: cardBindCmd, name: "via", what: "handoff card bind --via"},
	{cmd: cardCoordinateCmd, name: "desktop", boolean: true, what: "handoff card coordinate --desktop"},
}

// retiredB434Error 记一行 Debug 并返回墓碑错误。
// 为什么只打 Debug：CLI 的默认 slog 直接写 fd 2，Warn 会让真二进制的 stderr 多出一行日志，
// 破坏「stderr 恰一行可行动提示」；错误本身由 cobra 打到 stderr，不是静默。
func retiredB434Error(what string) error {
	slog.Default().Debug("命中 B434 退役入口", "entry", what)
	return fmt.Errorf("%s %s", what, retiredB434Hint)
}

func init() {
	for _, rc := range retiredB434Commands {
		what := rc.what
		rc.parent.AddCommand(&cobra.Command{
			Use:                rc.name,
			Short:              "已退役（B435）",
			Hidden:             true,
			DisableFlagParsing: true,
			Args:               cobra.ArbitraryArgs,
			RunE:               func(*cobra.Command, []string) error { return retiredB434Error(what) },
		})
	}
	byCmd := map[*cobra.Command][]retiredB434Flag{}
	for _, rf := range retiredB434Flags {
		if rf.boolean {
			rf.cmd.Flags().Bool(rf.name, false, "已退役（B435）")
		} else {
			rf.cmd.Flags().String(rf.name, "", "已退役（B435）")
		}
		_ = rf.cmd.Flags().MarkHidden(rf.name) // 刚注册的 flag，MarkHidden 不会失败
		byCmd[rf.cmd] = append(byCmd[rf.cmd], rf)
	}
	for c, flags := range byCmd {
		// 包裹而非覆盖：在本 init 之前已设的 PreRunE 于墓碑判定后继续执行；之后再赋值的 PreRunE 会覆盖本钩子
		// （当前无命令另设 PreRunE；测试锁 PreRunE 非空）。
		next := c.PreRunE
		c.PreRunE = func(cmd *cobra.Command, args []string) error {
			for _, rf := range flags {
				if cmd.Flags().Changed(rf.name) {
					return retiredB434Error(rf.what)
				}
			}
			if next != nil {
				return next(cmd, args)
			}
			return nil
		}
	}
}
