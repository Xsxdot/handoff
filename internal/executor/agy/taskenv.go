// taskenv.go —— agy 任务级运行环境与策略物料生成。
//
// 职责：
//   - 在 workdir/.agents 下生成 hooks.json（把 PreToolUse 钩子挂到本任务的 perm.sock 上）
//   - 渲染首回合 prompt（带任务 ID、计划内容与纪律块）
package agy

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/executor/turn"
)

const (
	agentsDirName = ".agents"
	hooksFileName = "hooks.json"
)

type hookHandler struct {
	Type    string `json:"type,omitempty"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

type hookGroup struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookHandler `json:"hooks"`
}

type hookNamedConfig struct {
	PreToolUse []hookGroup `json:"PreToolUse"`
}

// WriteTaskEnv 在 workdir 下准备 .agents/hooks.json 并渲染首回合 prompt。
func WriteTaskEnv(workdir, taskDir, taskID, planContent, sockPath, handoffBin, disciplineBlock string) (hooksPath, promptText string, err error) {
	log := slog.Default()
	agentsDir := filepath.Join(workdir, agentsDirName)
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		log.Error("创建 .agents 目录失败", "workdir", workdir, "cause", err)
		return "", "", fmt.Errorf("创建 %s: %w", agentsDir, err)
	}

	hooksPath = filepath.Join(agentsDir, hooksFileName)
	log.Info("agy 生成任务环境", "task", taskID, "workdir", workdir, "hooks", hooksPath, "sock", sockPath)

	cfg := map[string]hookNamedConfig{
		"handoff-safety-gate": {
			PreToolUse: []hookGroup{
				{
					Matcher: "run_command",
					Hooks: []hookHandler{
						{
							Type:    "command",
							Command: fmt.Sprintf("%s permission-hook --sock %s", handoffBin, sockPath),
							Timeout: 300,
						},
					},
				},
			},
		},
	}

	hooksJSON, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		log.Error("agy 序列化 hooks.json 失败", "task", taskID, "cause", err)
		return hooksPath, "", fmt.Errorf("序列化 hooks.json: %w", err)
	}

	if err := os.WriteFile(hooksPath, append(hooksJSON, '\n'), 0644); err != nil {
		log.Error("agy 写 hooks.json 失败", "path", hooksPath, "cause", err)
		return hooksPath, "", fmt.Errorf("写入 %s: %w", hooksPath, err)
	}

	promptText, err = turn.RenderPrompt(taskID, planContent, disciplineBlock)
	if err != nil {
		return hooksPath, "", err
	}
	return hooksPath, promptText, nil
}
