// card_seat.go —— CLI 当前会话席位身份的唯一出示入口。
//
// 边界：只读取命令身份 flag、受控的 HANDOFF/宿主会话键并编码规范席位；不读取
// USER、主机名、PID 或 web actor，也不执行账本写入。bind、rebind --self、
// --step 与协调者房间发送共同消费这个 helper。
package cmd

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/Xsxdot/handoff/internal/proto"
)

// currentSeatIdentity 按 flag → HANDOFF 注入 → 已核对宿主键的顺序出示席位身份。
// 参数：flagCLI、flagSession 是当前命令本地 --cli/--session 的原值；空值表示该
// flag 没有提供有效段。返回：规范 cli:<cli>#<session_id>；错误表示来源残缺、
// 来源冲突、来源歧义或现有编码规则拒绝。完整 HANDOFF 对忽略宿主键，是为了让
// 从 grok/claude 拉起的无头机器人在继承父进程宿主键时仍出示自身席位。注意：不
// 读取 USER/hostname/PID/ledgerActor/web actor，也不把 session 值写日志。
func currentSeatIdentity(flagCLI, flagSession string) (string, error) {
	encode := func(source, cli, sessionID string) (string, error) {
		identity, err := proto.EncodeSeatIdentity(cli, sessionID)
		if err != nil {
			return "", fmt.Errorf("编码%s席位身份: %w", source, err)
		}
		return identity, nil
	}

	var flagIdentity string
	flagPresent := flagCLI != "" || flagSession != ""
	if flagPresent {
		if flagCLI == "" || flagSession == "" {
			return "", fmt.Errorf("席位 flag 不完整：--cli 与 --session 必须同时填写，不能与环境变量拼接")
		}
		var err error
		flagIdentity, err = encode("flag", flagCLI, flagSession)
		if err != nil {
			return "", err
		}
	}

	injectedCLI, _ := os.LookupEnv("HANDOFF_SESSION_CLI")
	injectedSession, _ := os.LookupEnv("HANDOFF_SESSION_ID")
	var environmentIdentity string
	environmentPresent := false
	if injectedCLI != "" || injectedSession != "" {
		if injectedCLI == "" || injectedSession == "" {
			missing := make([]string, 0, 2)
			if injectedCLI == "" {
				missing = append(missing, "HANDOFF_SESSION_CLI")
			}
			if injectedSession == "" {
				missing = append(missing, "HANDOFF_SESSION_ID")
			}
			return "", fmt.Errorf("当前会话出示失败：注入环境缺少或为空 %s，不回退宿主会话", strings.Join(missing, "、"))
		}
		var err error
		environmentIdentity, err = encode("HANDOFF 注入", injectedCLI, injectedSession)
		if err != nil {
			return "", err
		}
		environmentPresent = true
	} else {
		grokSession, _ := os.LookupEnv("GROK_SESSION_ID")
		claudeSession, _ := os.LookupEnv("CLAUDE_CODE_SESSION_ID")
		switch {
		case grokSession != "" && claudeSession != "":
			return "", fmt.Errorf("当前会话出示失败：同时存在 GROK_SESSION_ID 与 CLAUDE_CODE_SESSION_ID，请去掉其中一个或改用 --cli 与 --session")
		case grokSession != "":
			var err error
			environmentIdentity, err = encode("GROK_SESSION_ID", "grok", grokSession)
			if err != nil {
				return "", err
			}
			environmentPresent = true
		case claudeSession != "":
			var err error
			environmentIdentity, err = encode("CLAUDE_CODE_SESSION_ID", "claude", claudeSession)
			if err != nil {
				return "", err
			}
			environmentPresent = true
		}
	}

	if flagIdentity != "" {
		if environmentPresent && flagIdentity != environmentIdentity {
			return "", fmt.Errorf("当前会话出示失败：--cli/--session 与当前环境会话不一致，请使用当前会话的一对或去掉环境来源")
		}
		slog.Default().Info("CLI 席位身份出示完成", "source", "flag")
		return flagIdentity, nil
	}
	if environmentPresent {
		slog.Default().Info("CLI 席位身份出示完成", "source", "environment")
		return environmentIdentity, nil
	}
	return "", fmt.Errorf("当前会话未出示席位身份：请在 grok/claude 对话里重试，或使用 --cli <物种> 与 --session <会话 id> 出示自己的一对")
}
