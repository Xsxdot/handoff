// Package initflow 持有 handoff 首次配置的问答逻辑：问什么、按什么顺序问、
// 默认值怎么算、角色如何分支。
//
// 职责：
//   - 提供 AskAll：按角色分支问完全部问题，就地改写 *config.Config
//   - 提供默认值与选项的纯函数（DefaultRole / ListenPreset / ExecutorOptions / RoleOptions）
//
// 边界：
//   - **不决定 UI 形态**。问答经 Prompter 接口发生：CLI 侧是 huh（cmd/init_huh.go），
//     桌面壳侧是事件驱动实现（desktop/internal/shell/wizard.go）
//   - **不写盘**。AskAll 只改内存里的 cfg，Save 由调用方决定——半截答案不得落盘
//   - **不探测工具链**。探测结果由调用方传入
//   - 不得 import huh / bubbletea / cobra / isatty，见 boundary_test.go
//
// 本包由 cmd 下沉而来（spec §4.4）：那批逻辑本就与 TUI 解耦，但封在 package cmd
// 里且未导出，桌面壳够不着。下沉是为了让 CLI 与 GUI 共用同一份事实来源，
// 避免两套 role 默认值、两套 listen 预设各自漂移。
package initflow

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"runtime"
	"strconv"

	"github.com/Xsxdot/handoff/internal/config"
	"github.com/Xsxdot/handoff/internal/toolchain"
)

// 角色取值写入 Select 的 Value，也是配置语义上的角色名。
const (
	RoleExecutor    = "executor"    // 执行机：跑 agentd 与 executor
	RoleCoordinator = "coordinator" // 协调者机：派发与审阅
	RoleBoth        = "both"
)

// 监听三档的 Select Value。写入 listen 的是档位对应的地址，不是这些词。
const (
	listenLoopback = "loopback" // 127.0.0.1:7777
	listenAll      = "all"      // 0.0.0.0:7777
	listenCustom   = "custom"   // 再走 Input
)

const (
	listenLoopbackAddr = "127.0.0.1:7777"
	listenAllAddr      = "0.0.0.0:7777"
)

// AskAll 按字段表逐项提问并把答案写回 cfg。
//
// 参数：
//   - w: 产品输出（前言与字段的 Notice）；桌面壳不走这条路径
//   - p: 问答实现（生产 TTY 走 huh，测试走脚本化实现）
//   - cfg: 就地写回；出错时不保证未被部分修改，调用方**绝不可**在出错后落盘
//   - rs: 工具链探测结果，决定执行者选项与默认值
//   - cfgExisted: 配置文件是否已存在，影响监听预设的默认档
//
// 返回：
//   - isExec: 本机是否承担执行机角色（调用方据此决定后续是否装 service）
//   - role: 角色答案原文
//   - err: 用户取消或校验失败
//
// 注意：提问顺序即 Form 返回的切片顺序。想改问什么、问的顺序、默认值，
// 改 form.go，**不要改本函数**——本函数只负责把表渲染成一问一答。
func AskAll(w io.Writer, p Prompter, cfg *config.Config, rs []toolchain.Result, cfgExisted bool) (bool, string, error) {
	fmt.Fprintln(w, "\n以下每一问直接回车即保留预选项。") // CLI 专有前言，不进字段表

	fields := Form(cfg, rs, runtime.GOOS, cfgExisted)
	answers := make(map[string]string, len(fields))
	for _, f := range fields {
		if !Visible(f, answers) {
			continue
		}
		if f.Notice != "" {
			fmt.Fprintln(w, "\n"+f.Notice)
		}
		ans, err := askField(p, f, answers)
		if err != nil {
			return false, answers["role"], err
		}
		answers[f.Key] = ans
		// CLI 专有的答后提示：选了没装/未登录的执行者时警告一句（只警告不拦）。
		// 它不进字段表——字段表描述的是「问什么」，这是「答完之后往终端写什么」，
		// 桌面端不需要（选项标签里已经带着就绪状态）。
		if f.Key == "executor_default" {
			warnIfNotReady(w, rs, ans)
		}
	}
	if err := Apply(cfg, fields, answers); err != nil {
		return false, answers["role"], err
	}
	role := answers["role"]
	return role == RoleExecutor || role == RoleBoth, role, nil
}

// askField 按 Kind 把一个字段分派给 Prompter。
//
// 默认值走 DefaultOf 而不是 f.Default：监听预设要跟着刚答完的角色翻档。
// Confirm 的答案统一编码成 "true"/"false" 字符串：字段表是同构的，
// 让答案 map 保持 map[string]string 才能被前端原样回传。
func askField(p Prompter, f Field, answers map[string]string) (string, error) {
	def := DefaultOf(f, answers)
	switch f.Kind {
	case KindSelect:
		return p.Select(f.Title, f.Options, def)
	case KindInput:
		return p.Input(f.Title, def)
	case KindConfirm:
		v, err := p.Confirm(f.Title, def == "true")
		if err != nil {
			return "", err
		}
		return strconv.FormatBool(v), nil
	}
	return "", fmt.Errorf("未知的字段类型 %q（字段 %s）", f.Kind, f.Key)
}

// RoleOptions 返回本平台可选的角色列表。
//
// 参数：goos 取 runtime.GOOS；抽成参数是为了让平台分支在任意 CI 上测得到
// （判据写死则 Windows 分支在 linux 的 CI 上永远测不到）。
//
// 返回：角色选项列表。
//
// 注意：B37 之前 Windows 只给协调者，因为 agentd 的进程承载层在该平台全是
// not implemented。进程承载层落地后三个角色一律可选，本函数不再分平台。
func RoleOptions(goos string) []Option {
	return []Option{
		{Value: RoleExecutor, Label: "执行机"},
		{Value: RoleCoordinator, Label: "协调者"},
		{Value: RoleBoth, Label: "两者"},
	}
}

// DefaultRole 挑角色预选项。
//
// 配置不记角色，只能从已有字段反推：有 targets 说明做过协调者；
// listen 不是 loopback 说明跑过执行机。推不出时：探到就绪执行者 → 执行机，
// 否则协调者。
func DefaultRole(cfg *config.Config, cfgExisted bool, rs []toolchain.Result, goos string) string {
	if cfgExisted {
		hasTargets := len(cfg.Targets) > 0
		kind := listenKind(cfg.Listen)
		hasRemoteListen := kind == listenAll || kind == listenCustom
		switch {
		case hasTargets && hasRemoteListen:
			return RoleBoth
		case hasTargets:
			return RoleCoordinator
		case hasRemoteListen:
			return RoleExecutor
		}
	}
	if toolchain.FirstReady(rs) != "" {
		return RoleExecutor
	}
	return RoleCoordinator
}

// ExecutorOptions 把四家探测结果编成 Select 选项。没装的也留在列表里——
// 探测只影响旁注和 warnIfNotReady，不阻断选择。
func ExecutorOptions(rs []toolchain.Result) []Option {
	opts := make([]Option, 0, len(rs))
	for _, r := range rs {
		opts = append(opts, Option{
			Value: r.Name,
			Label: fmt.Sprintf("%s（%s）", r.Name, r.State.String()),
		})
	}
	return opts
}

// ListenPreset 决定监听 Select 的光标停在哪一档。
//
// 为什么看「文件事先是否存在」而不是看当前 listen 字符串：
// config.Load 会把缺文件写成 127.0.0.1:7777，和用户选过「仅本机」是同一个值。
// 首次执行机要预选所有网卡（否则协调者连不上）；重跑时同一字符串必须保住 loopback。
func ListenPreset(listen string, cfgExisted, isExec bool) string {
	kind := listenKind(listen)
	if kind == listenLoopback && !cfgExisted && isExec {
		return listenAll
	}
	return kind
}

// listenKind 把当前 listen 归到三档之一。
//
// 0.0.0.0:7788 这类「通配但端口不是 7777」必须归手填：所有网卡那一档写死
// 0.0.0.0:7777，预选它会把人配好的端口冲掉，破坏幂等。
func listenKind(listen string) string {
	host, port, err := net.SplitHostPort(listen)
	if err != nil {
		return listenCustom
	}
	switch host {
	case "0.0.0.0", "::":
		if port != "7777" {
			return listenCustom
		}
		return listenAll
	case "127.0.0.1", "::1":
		if port != "7777" {
			return listenCustom
		}
		return listenLoopback
	default:
		return listenCustom
	}
}

// InstallService 是 MaybeInstallService 在「答 y」后代跑托管的安装入口。
//
// CLI 侧由 cmd/service.go 注入 installService——与 handoff service install
// 走同一条代码路径（B71 要求 init 不复制一份）；桌面壳侧注入它自己的安装实现。
// nil 时只打印提示、不 panic：托管是附属动作，配置此时已写盘，装不上也不该
// 让 init 退非零。
var InstallService func(w io.Writer, cfgPath string) error

// HostGOOS / HostGeteuid 是 MaybeInstallService 里那道平台门的测试缝。
//
// 为什么必须是缝：托管路径在 macOS（launchd，用户级）与 Linux（systemd，要 root）
// 上行为**相反**——前者当场装，后者只打一行 sudo 提示就返回。直接读
// runtime.GOOS 的话，一套用例只能覆盖跑测试的那个平台，另一条分支在该平台上
// 恒不成立：开发机是 macOS，于是「答 y 必须真的调 Install」在 Linux CI 上
// 必然失败（2026-08-13 实测三条），而 Linux 那条真行为反倒从来没人验过。
// 下沉到 initflow 后 cmd 仍要钉平台（runInitWith 钉成 darwin），故导出。
var (
	HostGOOS    = func() string { return runtime.GOOS }
	HostGeteuid = os.Geteuid
)

// MaybeInstallService 在执行机上追问是否现在把 agentd 交给进程管理器托管，
// 答 y 则就地代跑。
//
// 参数：
//   - w: 面向用户的输出
//   - p: 问答通道（与 AskAll 共用同一实例）
//   - isExec: 本机角色是否包含执行机
//   - cfgPath: 配置路径（传给服务单元）
//
// 注意：
//   - 无返回值：托管失败**绝不**让 init 失败。配置此时已经写盘，为一个附属动作
//     把整条 init 退非零，用户会以为配置没保存（与 install.sh 对 skill install
//     的处置同一个道理）
//   - Linux 上非 root 时不代跑：systemd 单元要写 /etc/systemd/system，需要 root，
//     而 init 不 sudo。此时只打印命令
//   - why 要追问而不是只提示：托管是「机器重启后 agentd 还回得来」的唯一保障，
//     它此前只是最后一行提示——B71 现场那台就是这么变成手工拉起的，重启后
//     PATH 全靠运气
func MaybeInstallService(w io.Writer, p Prompter, isExec bool, cfgPath string) {
	if !isExec {
		// 协调者机不跑 agentd，托管对它没有意义
		return
	}
	if HostGOOS() == "linux" && HostGeteuid() != 0 {
		fmt.Fprintln(w, "\n下一步   sudo handoff service install")
		fmt.Fprintln(w, "         systemd 单元要写 /etc/systemd/system，需要 root，init 不替你 sudo。")
		fmt.Fprintln(w, "         没有托管的 agentd 在机器重启后不会自己回来。")
		return
	}
	ok, err := p.Confirm("\n现在把 agentd 交给本机进程管理器托管", true)
	if err != nil {
		// 配置已经写盘，附属问答失败不能让 init 退非零
		fmt.Fprintf(w, "托管追问失败：%v\n", err)
		return
	}
	if !ok {
		fmt.Fprintln(w, "\n下一步   handoff service install")
		fmt.Fprintln(w, "         没有托管的 agentd 在机器重启后不会自己回来。")
		return
	}
	fmt.Fprintln(w)
	if InstallService == nil {
		// 调用方没注入安装入口（桌面壳未接线时的兜底）：不 panic，
		// 照「托管失败不阻断」处置。薄壳走不到此分支——这条路理论上不该
		// 被走到，真有人从桌面侧调了它即设计被违反的信号，现场只剩日志能说明。
		slog.Warn("initflow.MaybeInstallService 被调用但没有安装入口", "cfg_path", cfgPath)
		fmt.Fprintln(w, "没有可用的服务安装入口，稍后单独重跑 handoff service install 即可。")
		fmt.Fprintln(w, "没有托管的 agentd 在机器重启后不会自己回来。")
		return
	}
	if err := InstallService(w, cfgPath); err != nil {
		fmt.Fprintf(w, "托管失败：%v\n", err)
		fmt.Fprintln(w, "配置已经写好了，稍后单独重跑 handoff service install 即可。")
	}
}

// warnIfNotReady 在选了「没装」或「未登录」的执行者时警告一句——只警告，不拦。
//
// why 不拦：一台刚装好的机器上什么都还没登录，但用户知道自己等会儿要登；
// 拦住等于逼他先去登录再回来重跑 init。
func warnIfNotReady(w io.Writer, rs []toolchain.Result, name string) {
	for _, r := range rs {
		if r.Name != name {
			continue
		}
		if r.State == toolchain.StateMissing {
			fmt.Fprintf(w, "  ⚠ %s 没装。配置照写，但派活前需要先装上。\n", name)
		} else if r.State == toolchain.StateNoCreds {
			fmt.Fprintf(w, "  ⚠ %s 已安装但未登录。配置照写，但派活前需要先登录。\n", name)
		}
		return
	}
	fmt.Fprintf(w, "  ⚠ %s 不在已知的四家里（opencode/claude/grok/codex），派发时会报未注册。\n", name)
}
