// taskenv.go —— codex 任务的启动物料：app-server 启动脚本与包内文件名常量。
//
// 职责：
//   - 生成 taskDir 下的 run_codex.sh（0600），把 B19 注入的 env 变量展开成 export 行
//   - 统一约定任务目录内的文件名（serve.log / render.log / serve.json）
//
// 边界：
//   - 不起进程（tmux 在 proc.go）、不碰协议（appserver.go）
//   - **刻意不生成任何 codex 配置文件**：本设计的安全档位全部协议级下发
//     （spec §2「配置下发：全部协议级，不碰任何 config 文件」），写配置文件会
//     让「代码钉死安全边界」这条保证多出一个可被绕过的入口
package codex

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/xushixin/handoff/internal/shellq"
)

const (
	serveScriptName = "run_codex.sh"
	serveLogName    = "serve.log"
	renderLogName   = "render.log"
	serveInfoName   = "serve.json"
)

// droppedEnvKeys 是 env 文件里出现即**丢弃**的变量。
//
// 为什么是丢弃而不是像 grok 那样靠 export 顺序覆盖：codex adapter 自身从不
// export CODEX_HOME（本设计刻意复用用户级 ~/.codex，spec §1.3），没有「后写的
// 那行」可以压过它。一旦生效，executor 会换到一个空 home 跑——凭据、插件、
// sessions 全部落空，任务以「未登录」形态失败且原因极难追。
var droppedEnvKeys = map[string]bool{
	"CODEX_HOME": true,
}

// WriteServeScript 在 taskDir 生成 codex app-server 的启动脚本。
//
// 参数：
//   - taskDir: 任务物料目录（须已存在，由调用方保证）
//   - port: app-server 的 WS 监听端口
//   - env: 注入到 app-server 进程的环境变量（形如 KEY=VALUE，已由 manager 展开）；
//     命中 droppedEnvKeys 的条目会被丢弃并打 WARN；非 KEY=VALUE 的条目直接跳过
//
// 返回：脚本绝对路径；写文件失败时返回错误
//
// 注意：
//   - 脚本权限 0600，重复调用幂等覆盖
//   - env 的值一律单引号包裹：Go 侧已展开过一次，不加引号会被 shell 再展开一次，
//     含 $ 的值会变成别的东西（B19）
func WriteServeScript(taskDir string, port int, env []string) (string, error) {
	log := slog.Default()
	serveLog := filepath.Join(taskDir, serveLogName)

	var envLines strings.Builder
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			continue // 形如 KEY=VALUE 之外的条目直接跳过，不让它污染脚本语法
		}
		if droppedEnvKeys[k] {
			log.Warn("env 文件定义了 handoff 禁止覆盖的变量，已丢弃",
				"key", k, "reason", "codex 复用用户级 ~/.codex，覆盖它会让凭据与插件全部落空")
			continue
		}
		envLines.WriteString("export " + k + "=" + shellq.Quote(v) + "\n")
	}

	script := fmt.Sprintf(`#!/bin/sh
# 由 agentd 生成：codex app-server 启动脚本（0600，勿外泄）。
# 刻意不设 codex 的 home 环境变量——本设计复用用户级 ~/.codex（spec §1.3），凭据零副本。
%sexec codex app-server --listen 'ws://127.0.0.1:%d' >> %s 2>&1
`, envLines.String(), port, shellq.Quote(serveLog))

	p := filepath.Join(taskDir, serveScriptName)
	if err := os.WriteFile(p, []byte(script), 0o600); err != nil {
		log.Error("写 codex 启动脚本失败", "path", p, "cause", err)
		return "", fmt.Errorf("写 codex serve 启动脚本 %s: %w", p, err)
	}
	log.Info("codex serve 启动脚本已生成", "path", p, "port", port)
	return p, nil
}
