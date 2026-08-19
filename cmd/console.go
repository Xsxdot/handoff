// 本文件实现 handoff console 子命令：用主令牌换一张一次性 ticket，
// 并把兑换 URL 交给系统浏览器（或打印出来给桌面壳用）。
//
// 职责：
//   - 调 client.IssueAuthTicket 取兑换 URL
//   - 默认调系统浏览器打开；--print-url 只打印（这是桌面壳的接线点）
//   - 设备名缺省取本机主机名（CLI 没有 User-Agent 可推断）
//
// 边界：
//   - 不实现任何鉴权逻辑：凭据的签发与校验全在 agentd 侧
//   - 不管前端是否存在：本命令的成功判据是「拿到兑换 URL」，兑换后落地页上有什么
//     由 agentd 决定（W5a 之后是真实控制台，不带 embedweb 标签构建时是 stub 说明页）。
//     不要因为落地页的形态变化而改这里的成功判据
//   - --target 可用，但那是**诊断入口**不是产品路径（产品路径是「只连本机
//     agentd，由它向远端转发」），不要因为它好用就当成跨机方案
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
)

var (
	consolePrintURL bool
	consoleDevice   string
	consoleNoOpen   bool
)

// consoleCmd 打开浏览器控制台。
var consoleCmd = &cobra.Command{
	Use:   "console",
	Short: "在浏览器中打开 agentd 控制台（换一次性 ticket 并兑换会话）",
	// 位置参数没有意义：console 的输入全走 flag（--print-url/--device/--no-open），
	// 多余的参数说明用法错误，静默忽略会让拼错的命令「看似成功」——尤其桌面壳
	// 依赖 stdout 恰好一行的契约，多喂一个参数被吞掉会直接破坏那条契约
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
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

func init() {
	consoleCmd.Flags().BoolVar(&consolePrintURL, "print-url", false, "只打印兑换 URL，不打开浏览器（桌面壳用）")
	consoleCmd.Flags().StringVar(&consoleDevice, "device", "", "设备展示名（缺省取本机主机名）")
	consoleCmd.Flags().BoolVar(&consoleNoOpen, "no-open", false, "不打开浏览器（等价于 --print-url）")
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
