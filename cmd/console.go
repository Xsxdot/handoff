// 本文件实现 handoff console 子命令：用主令牌换一张一次性 ticket，
// 并把兑换 URL 交给系统浏览器（或打印出来给桌面壳用）；B369.5 起新增
// 配对载体输出（--qr/--bundle），供移动 App 扫码配对。
//
// 职责：
//   - 调 client.IssueAuthTicket 取兑换 URL
//   - 默认调系统浏览器打开；--print-url 只打印（这是桌面壳的接线点）
//   - --bundle/--qr：读本机配置的 targets，逐机代领 ticket，组装
//     proto.PairBundle 并编码为 QR 载荷/终端二维码（不重实现 bundle 编解码）
//   - 设备名缺省取本机主机名（CLI 没有 User-Agent 可推断）
//
// 边界：
//   - 不实现任何鉴权逻辑：凭据的签发与校验全在 agentd 侧
//   - 不管前端是否存在：本命令的成功判据是「拿到兑换 URL」，兑换后落地页上有什么
//     由 agentd 决定（W5a 之后是真实控制台，不带 embedweb 标签构建时是 stub 说明页）。
//     不要因为落地页的形态变化而改这里的成功判据
//   - --target 可用，但那是**诊断入口**不是产品路径（产品路径是「只连本机
//     agentd，由它向远端转发」），不要因为它好用就当成跨机方案
//   - 配对载体编解码唯一定义在 internal/proto（契约 §8.3：CLI 不得重实现）；
//     本文件只做「读配置 → 逐机代领 ticket → 调 proto.EncodePairBundle → 渲染」
//   - 凭据卫生：token/ticket 只进 stdout 的载荷，绝不落日志；错误分支只带
//     target 名与 cause，不带 token
package cmd

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/proto"
	"github.com/spf13/cobra"
	"rsc.io/qr"
)

var (
	consolePrintURL bool
	consoleDevice   string
	consoleNoOpen   bool
	consoleQR       bool
	consoleBundle   bool
)

// pairBundleLifetime 是配对载体载荷的展示有效期。
//
// 每台 agentd 的一次性 ticket 寿命是 60s（internal/agentd/auth.go#ticketLifetime，
// 见 contract §9 附区条 5：寿命沿用 60s，N 张同时起算），载体不应承诺比 ticket
// 更长的窗口；这里刻意不 import internal/agentd（那会新增 d_cli→d_gateway 跨域边），
// 只复述同一常量并注明出处。
const pairBundleLifetime = 60 * time.Second

// consoleCmd 打开浏览器控制台或输出配对载体。
var consoleCmd = &cobra.Command{
	Use:   "console",
	Short: "在浏览器中打开 agentd 控制台，或输出移动端配对载体（--qr/--bundle）",
	// 位置参数没有意义：console 的输入全走 flag（--print-url/--device/--no-open），
	// 多余的参数说明用法错误，静默忽略会让拼错的命令「看似成功」——尤其桌面壳
	// 依赖 stdout 恰好一行的契约，多喂一个参数被吞掉会直接破坏那条契约
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if consoleQR || consoleBundle {
			// 配对载体模式（--qr/--bundle）与单 URL 模式（--print-url/--no-open）
			// 语义不同（前者是 App 扫码的整包，后者是桌面壳消费的一行 URL），
			// 同给会让 stdout 契约歧义——在触达任何网络前明确拒绝。
			// 注意 --print-url 与 --no-open 仍是同义（历史行为），不互斥。
			if consolePrintURL || consoleNoOpen {
				return errors.New("--qr/--bundle（配对载体）与 --print-url/--no-open（单 URL）不能同时使用")
			}
			return runConsolePair(cmd)
		}
		c, cleanup, err := newTargetClient()
		if err != nil {
			return err
		}
		defer cleanup()
		device := consoleDevice
		if device == "" {
			// CLI 没有 User-Agent 可推断，用主机名作缺省展示名；
			// 取不到主机名时留空，由服务端补浏览器名
			device, _ = os.Hostname()
		}
		tk, err := c.IssueAuthTicket(cmd.Context(), device)
		if err != nil {
			return err
		}
		if consolePrintURL || consoleNoOpen {
			// 只打 URL，一行，无任何前后缀：桌面壳直接把这一行交给 loadURL
			fmt.Fprintln(cmd.OutOrStdout(), tk.URL)
			return nil
		}
		if oerr := openBrowser(tk.URL); oerr != nil {
			// 打不开浏览器不是失败：把 URL 打出来，用户自己粘贴即可，
			// 而 ticket 只有 60 秒，静默失败会让人完全摸不着头脑
			fmt.Fprintf(cmd.ErrOrStderr(), "打开浏览器失败（%v），请手动打开下面的地址（60 秒内有效）：\n", oerr)
			fmt.Fprintln(cmd.OutOrStdout(), tk.URL)
		} else {
			// 成功打开也要让人知道发生了什么，否则静默无感
			fmt.Fprintln(cmd.ErrOrStderr(), "已在浏览器中打开控制台（链接 60 秒内有效）")
		}
		return nil
	},
}

// runConsolePair 组装并输出移动端配对载体（--bundle 出粘贴串，--qr 出终端二维码）。
//
// 与 --print-url 的分工：--print-url 只出本机一张 ticket 的兑换 URL（桌面壳接线，
// stdout 恰好一行）；--qr/--bundle 出的是完整 bundle（relay 端点 + 各 target 登记
// + 每机 ticket），消费者是移动 App 的扫码解码方。
//
// 参数：
//   - cmd: cobra 命令，提供 ctx 与 stdout/stderr
//
// 返回：
//   - 配置加载失败、无 target、bundle 非法、载荷超二维码容量时返回错误（非 0 退出）
func runConsolePair(cmd *cobra.Command) error {
	// 复用既有的 Endpoints 取机器清单：它封装了「读配置 → 本机 + 各 target 换算」
	// 这条既有路径（cmd/root.go），避免在 console.go 里新增对 config 包的直调
	// （d_cli→d_policy 预算已满 35/35，多一条直调即 over-budget）。
	eps, err := Endpoints("")
	if err != nil {
		return err
	}
	// 本机（Local）不是待配对的执行机，不进 bundle；只剩本机 = 无 target。
	var targets []Endpoint
	for _, ep := range eps {
		if !ep.Local {
			targets = append(targets, ep)
		}
	}
	if len(targets) == 0 {
		// 空 bundle 会撞 proto 的「无机器登记」拒收；这里提前给出可行动错误
		return fmt.Errorf("配置 %s 里没有任何 target，无法生成配对载体（先 handoff init 并登记执行机）", effectiveConfigPath())
	}
	device := consoleDevice
	if device == "" {
		device, _ = os.Hostname()
	}
	b, err := buildPairBundle(cmd.Context(), targets, device, slog.Default())
	if err != nil {
		return err
	}
	payload, err := proto.EncodePairBundle(b)
	if err != nil {
		// 不重实现编解码：非法 bundle 由 proto 拒收，这里只带上下文
		return fmt.Errorf("组装配对载体: %w", err)
	}
	if consoleBundle {
		// 恰好一行载荷：粘贴串形态，不受二维码容量限制
		fmt.Fprintln(cmd.OutOrStdout(), payload)
		return nil
	}
	art, err := renderBundleQR(payload)
	if err != nil {
		return err
	}
	fmt.Fprint(cmd.OutOrStdout(), art)
	return nil
}

// buildPairBundle 以各 target 的 Endpoint 登记逐机代领 ticket，组装一份 PairBundle。
//
// 语义（contract §3.1）：一份 bundle = 一个 relay 端点 + 管道 credential + 各机器
// 登记；relay 形态机器与直连形态机器按各自 Endpoint 形态登记。某机 ticket 代领失败
// （离线/连不上）只 Warn 并让该机无 ticket（部分 bundle，contract §3.1「离线机可缺」），
// 不整单失败。
//
// 参数：
//   - ctx: 请求上下文（逐机 IssueAuthTicket 用）
//   - targets: 待配对机器清单（Endpoints 换算结果，已剔除本机）；Endpoint 是
//     cmd 包内类型，经它取形态/令牌不新增 d_cli→d_policy 直调
//   - device: 设备展示名（传给 agentd 的 ticket 展示名，可空）
//   - log: 结构化日志器；失败逐机 Warn
//
// 返回：
//   - 组装完成的 PairBundle；relay 形态 target 使用多个不同 relay 端点时返回错误
//     （wire 只有单一 relay 端点，见 contract §3.1，无法表达多端点）
//
// 为什么先做完 relay 端点一致性检查再逐机领 ticket：多端点配置是纯粹的本地
// 配置冲突，应在触达任何网络之前失败（否则错误取决于哪台先拨号，且测试不可判）。
func buildPairBundle(ctx context.Context, targets []Endpoint, device string, log *slog.Logger) (proto.PairBundle, error) {
	if log == nil {
		log = slog.Default()
	}
	// 输出可复现（golden 字节稳定）：按机器名排序。
	sorted := make([]Endpoint, len(targets))
	copy(sorted, targets)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	// 阶段一：统一 relay 端点（wire 只承载一个）。
	relayURL := ""
	relayCred := ""
	for _, ep := range sorted {
		if ep.RelayURL == "" {
			continue
		}
		if relayURL == "" {
			relayURL, relayCred = ep.RelayURL, ep.Credential
			continue
		}
		if relayURL != ep.RelayURL {
			return proto.PairBundle{}, fmt.Errorf("target %q 的 relay 端点 %q 与其它 target 的 %q 不同——配对载体只承载单一 relay 端点（contract §3.1），请分码配对", ep.Name, ep.RelayURL, relayURL)
		}
	}
	// 阶段二：逐机登记并代领 ticket（此时不再可能因配置冲突中止）。
	// IssuedAt/ExpiresAt 取同一次 now：两次 time.Now() 可能跨秒，让 ExpiresAt-IssuedAt
	// 漂移出恰好 60s（契约 §9 附区条 5：N 张 ticket 同时起算）。
	now := time.Now().UTC()
	b := proto.PairBundle{
		Version:   proto.PairVersion,
		IssuedAt:  now,
		ExpiresAt: now.Add(pairBundleLifetime),
	}
	for _, ep := range sorted {
		b.Machines = append(b.Machines, pairMachineForTarget(ctx, ep, device, relayCred, log))
	}
	if relayURL != "" {
		b.Relay = &proto.PairRelay{URL: relayURL, Credential: relayCred}
	}
	return b, nil
}

// pairMachineForTarget 组装单台机器的登记；ticket 代领失败只标为无 ticket（部分 bundle）。
// 不返回 error：单机失败是「部分 bundle」语义（contract §3.1），不整单失败。
func pairMachineForTarget(ctx context.Context, ep Endpoint, device, relayCred string, log *slog.Logger) proto.PairMachine {
	m := proto.PairMachine{Name: ep.Name, Token: ep.Token}
	if ep.RelayURL != "" {
		m.Node = ep.Node
		if ep.Credential != relayCred {
			// 机器级凭证覆盖：通常空，沿用 bundle.Relay（contract §3.1）
			m.Credential = ep.Credential
		}
	} else {
		m.Addr = ep.Addr
	}
	tk, err := issuePairTicketForTarget(ctx, ep.Name, device)
	if err != nil {
		// 部分 bundle：离线机标记、上线后补配（contract §3.1；不整单失败）
		log.Warn("配对载体：该机 ticket 代领失败，标为无 ticket", "target", ep.Name, "cause", err)
		return m
	}
	m.Ticket = tk
	return m
}

// issuePairTicketForTarget 用该 target 的主令牌代领一张一次性 ticket。
//
// 参数：
//   - ctx: 请求上下文
//   - name: config.Targets 里的登记名（经 newTargetClientNamed 选路：直连或 relay）
//   - device: 设备展示名（可空）
//
// 返回：
//   - ticket（URL + 过期时刻）；选路失败或 agentd 连不上时返回错误，由调用方标离线
func issuePairTicketForTarget(ctx context.Context, name, device string) (*proto.PairTicket, error) {
	c, cleanup, err := newTargetClientNamed(name)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	resp, err := c.IssueAuthTicket(ctx, device)
	if err != nil {
		return nil, err
	}
	return &proto.PairTicket{URL: resp.URL, ExpiresAt: resp.ExpiresAt}, nil
}

// renderBundleQR 把配对载荷渲染成终端二维码（半块字符 + 4 模块静默区）。
//
// 参数：
//   - payload: proto.EncodePairBundle 的紧凑 JSON 串
//
// 返回：
//   - 多行二维码文本（换行结尾）；载荷超过单码容量时返回带 --bundle 退路的错误
func renderBundleQR(payload string) (string, error) {
	code, err := qr.Encode(payload, qr.L)
	if err != nil {
		// L 级单码容量约 2953 字节（rsc.io/qr v0.2.0 实测）；超界退路 = 粘贴串
		return "", fmt.Errorf("生成二维码失败（载荷 %d 字节，可能超过单码容量）: %w；改用 handoff console --bundle 输出粘贴串", len(payload), err)
	}
	const quiet = 4 // 静默区：扫码器需要
	size := code.Size
	black := func(x, y int) bool { return code.Black(x, y) }
	var sb strings.Builder
	for y := -quiet; y < size+quiet; y += 2 {
		for x := -quiet; x < size+quiet; x++ {
			top, bot := black(x, y), black(x, y+1)
			switch {
			case top && bot:
				sb.WriteRune('█')
			case top && !bot:
				sb.WriteRune('▀')
			case !top && bot:
				sb.WriteRune('▄')
			default:
				sb.WriteByte(' ')
			}
		}
		sb.WriteByte('\n')
	}
	return sb.String(), nil
}

func init() {
	consoleCmd.Flags().BoolVar(&consolePrintURL, "print-url", false, "只打印兑换 URL，不打开浏览器（桌面壳用）")
	consoleCmd.Flags().StringVar(&consoleDevice, "device", "", "设备展示名（缺省取本机主机名）")
	consoleCmd.Flags().BoolVar(&consoleNoOpen, "no-open", false, "不打开浏览器（等价于 --print-url）")
	consoleCmd.Flags().BoolVar(&consoleQR, "qr", false, "输出配对载体终端二维码（供移动 App 扫码配对）")
	consoleCmd.Flags().BoolVar(&consoleBundle, "bundle", false, "输出配对载体粘贴串（一行 JSON，二维码超容量时的退路）")
	// --qr 与 --bundle 是同一份载荷的两种编码（终端二维码 / 一行粘贴串），
	// 同给输出语义歧义；--print-url 与 --no-open 是历史同义词，保持不互斥。
	consoleCmd.MarkFlagsMutuallyExclusive("qr", "bundle")
	rootCmd.AddCommand(consoleCmd)
}

// openBrowser 用系统默认方式打开一个 URL。
//
// 注意：各平台命令不同；不支持的平台返回错误，由调用方降级为打印 URL
func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, truncateBytes(string(out), 200))
	}
	return nil
}
