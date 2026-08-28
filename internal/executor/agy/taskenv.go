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
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

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

	// 写入 .git/info/exclude 避免工作区被判定为脏（原地任务 409 拦截）
	ensureGitExclude(workdir, ".agents/hooks.json")
	ensureGitExclude(workdir, ".agents/")

	agentsDir := filepath.Join(workdir, agentsDirName)
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		log.Error("创建 .agents 目录失败", "workdir", workdir, "cause", err)
		return "", "", fmt.Errorf("创建 %s: %w", agentsDir, err)
	}

	hooksPath = filepath.Join(agentsDir, hooksFileName)
	log.Info("agy 生成任务环境", "task", taskID, "workdir", workdir, "hooks", hooksPath, "sock", sockPath)

	cfg := make(map[string]any)
	if data, err := os.ReadFile(hooksPath); err == nil {
		if err := json.Unmarshal(data, &cfg); err != nil {
			log.Warn("解析既有 hooks.json 失败，将被覆盖", "path", hooksPath, "cause", err)
			cfg = make(map[string]any)
		}
	}

	gateCfg := hookNamedConfig{
		PreToolUse: []hookGroup{
			{
				Matcher: "run_command|write_to_file|replace_file_content|multi_replace_file_content|sed_file|read_url_content|search_web|invoke_subagent",
				Hooks: []hookHandler{
					{
						Type:    "command",
						Command: fmt.Sprintf("%s permission-hook --sock %s", strconv.Quote(handoffBin), strconv.Quote(sockPath)),
						Timeout: 86400,
					},
				},
			},
		},
	}
	cfg["handoff-safety-gate"] = gateCfg

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

// ensureGitExclude 将 pattern 追加写入 workdir 对应 git 仓库的 info/exclude 文件，
// 避免生成的任务物料（如 .agents/hooks.json）导致 git status --porcelain 变脏或触发 ensureCleanWorktree 拦截。
// info/exclude 仅对本地工作树生效，不影响 git 追踪历史，不会被提交或推送到远端。
func ensureGitExclude(workdir, pattern string) {
	out, err := exec.Command("git", "-C", workdir, "rev-parse", "--git-path", "info/exclude").Output()
	if err != nil {
		return
	}
	relOrAbs := strings.TrimSpace(string(out))
	if relOrAbs == "" {
		return
	}
	excludePath := relOrAbs
	if !filepath.IsAbs(excludePath) {
		excludePath = filepath.Join(workdir, relOrAbs)
	}
	data, err := os.ReadFile(excludePath)
	if err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == pattern {
				return
			}
		}
	}
	_ = os.MkdirAll(filepath.Dir(excludePath), 0755)
	f, err := os.OpenFile(excludePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.WriteString("\n" + pattern + "\n")
}
