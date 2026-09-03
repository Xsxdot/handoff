// card_seat.go —— CLI 当前会话席位身份的唯一出示入口。
//
// 边界：只读取 agentd 注入的 HANDOFF_SESSION_CLI/ID 并编码规范席位；不读取
// USER、主机名、PID 或 web actor，也不执行账本写入。bind、rebind --self、
// --step 与协调者房间发送共同消费这个 helper。
package cmd

import (
	"fmt"
	"os"

	"github.com/Xsxdot/handoff/internal/proto"
)

// currentSeatIdentity 从当前执行环境读取规范席位身份。
// 两个 HANDOFF_SESSION_* 键缺一不可；缺失时返回可行动错误，绝不从人尺度
// actor、主机名或浏览器地址推导会话。
func currentSeatIdentity() (string, error) {
	cli, cliOK := os.LookupEnv("HANDOFF_SESSION_CLI")
	sessionID, sessionOK := os.LookupEnv("HANDOFF_SESSION_ID")
	if !cliOK || !sessionOK || cli == "" || sessionID == "" {
		return "", fmt.Errorf("当前会话未出示席位身份：需要 HANDOFF_SESSION_CLI 与 HANDOFF_SESSION_ID，请在 grok/claude 会话中重试")
	}
	identity, err := proto.EncodeSeatIdentity(cli, sessionID)
	if err != nil {
		return "", fmt.Errorf("编码当前会话席位身份: %w", err)
	}
	return identity, nil
}
