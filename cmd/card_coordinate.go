// card_coordinate.go —— card add --coordinate 的开卡即绑接线（B156.3 K4，
// spec §5.1 入口 1）：卡创建成功后向本机 agentd 拉起协调者（source=card_create）。
//
// 失败语义：卡已创建，不因拉起失败回滚（否则用户重跑 add 会造重复卡）；错误以
// stderr 出声 + 结构化日志留痕，命令仍成功退出——建卡是本次命令的主产出，拉起是
// 可补动作（控制台一键拉起 / card coordinate）。plan §D5 / §6 待拍板 ③。
package cmd

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/spf13/cobra"
)

// coordinateAfterCreate 在卡创建成功后拉起并绑定协调者（source=card_create）。
// 返回错误由调用方降级呈现（不回滚建卡）；拨号目标固定本机 agentd——开卡动作
// 发生在哪台机器，绑定就落哪台机器的账本与自动化层。
func coordinateAfterCreate(cmd *cobra.Command, cardID string) error {
	ctx := cmd.Context()
	if ctx == nil {
		ctx = context.Background()
	}
	addr, token, err := LocalEndpoint()
	if err != nil {
		return fmt.Errorf("读取本机 agentd 端点失败: %w", err)
	}
	cl := client.New(addr, token)
	resp, err := cl.CoordinatorLaunchAs(ctx, cardID, "card_create")
	if err != nil {
		return err
	}
	slog.Default().Info("开卡即绑已拉起协调者", "card", cardID, "session", resp.SessionID, "agentd", addr)
	return nil
}
