// 本文件实现 handoff sessions 子命令族：列出与吊销浏览器会话。
//
// 职责：
//   - sessions：列出全部会话（含已吊销，显式标注）
//   - sessions revoke <id>：吊销指定会话
//   - 渲染前净化设备名：它来自客户端，可能含 ANSI 转义序列
//
// 边界：
//   - 不吊销主令牌：主令牌不可吊销（换它等于全部重配），本命令只管会话
//   - 不做交互确认：吊销一个会话是可恢复的（重新 handoff console 即可），
//     不值得一道确认门
package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// sessionsCmd 列出浏览器会话。
var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "列出浏览器会话（handoff console 建立的登录态）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		list, err := client.New(addr, token).ListSessions(cmd.Context())
		if err != nil {
			return err
		}
		renderSessions(cmd.OutOrStdout(), list)
		return nil
	},
}

// sessionsRevokeCmd 吊销一个会话。
var sessionsRevokeCmd = &cobra.Command{
	Use:   "revoke <session-id>",
	Short: "吊销指定的浏览器会话（手机丢失时用它，不必换主令牌）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		if err := client.New(addr, token).RevokeSession(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已吊销会话 %s\n", args[0])
		return nil
	},
}

func init() {
	sessionsCmd.AddCommand(sessionsRevokeCmd)
	rootCmd.AddCommand(sessionsCmd)
}

// renderSessions 渲染会话列表。
//
// 参数：
//   - w: 输出目标
//   - list: 会话列表（可为空）
func renderSessions(w io.Writer, list []proto.SessionInfo) {
	if len(list) == 0 {
		fmt.Fprintln(w, "没有浏览器会话。执行 handoff console 建立一个。")
		return
	}
	for _, s := range list {
		state := "有效"
		switch {
		case s.RevokedAt != nil:
			state = "已吊销"
		case !time.Now().Before(s.ExpiresAt):
			state = "已过期"
		}
		fmt.Fprintf(w, "%s  %s  %s  最后活跃 %s\n",
			s.ID, displayName(s.DeviceName), state, s.LastSeenAt.Local().Format("01-02 15:04"))
	}
}

// displayName 净化设备名后再打到终端。
//
// 为什么 CLI 也要净化（服务端已经净化过一道）：CLI 可能连的是一台**旧版**
// agentd，那边没有这道处理。展示层不能假设对端一定是新版——一个构造过的
// User-Agent 能往终端里注入 ANSI 转义序列。
func displayName(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if cleaned == "" {
		return "（未命名设备）"
	}
	rs := []rune(cleaned)
	if len(rs) > 40 {
		return string(rs[:40]) + "…"
	}
	return cleaned
}
