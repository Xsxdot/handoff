// taskenv.go —— agy 任务级运行环境与策略物料生成。
//
// 职责：
//   - 在 workdir/.agents 下生成 hooks.json（把 PreToolUse 钩子挂到本任务的 perm.sock 上）
//   - 在 taskDir/agyhome 下生成 headless agy 读取的 hooks、原生 allow 策略与登录凭据
//   - 渲染首回合 prompt（带任务 ID、计划内容与纪律块）
//   - 用 taskDir sidecar 保存并恢复 hooks.json 的原文、skip-worktree 与 exclude 状态
//
// 边界：Stop、Start rollback 与 Reap 必须调用 RestoreTaskEnv；本文件不负责启停进程。
package agy

import (
	"encoding/json"
	"errors"
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
	agentsDirName   = ".agents"
	hooksFileName   = "hooks.json"
	restoreFileName = "agy-hooks-restore.json"
	agyHomeDirName  = "agyhome"
)

// nativeCommandAllow 是 agy 原生策略允许的命令前缀。
//
// 这里刻意不使用 command(*)：agy 的全匹配项会绕过 PreToolUse 的 deny 结果。
// agy 按命令行首词匹配 command(<target>)：command(git) 能放行 `git status && …`，
// 但放行不了 `pwd && git status`。pwd/cd 是跑分复合命令的常见首词。
var nativeCommandAllow = []string{
	"command(go)",
	"command(git)",
	"command(pwd)",
	"command(cd)",
	"command(echo)",
	"command(make)",
	"command(npm)",
	"command(npx)",
	"command(pnpm)",
	"command(yarn)",
	"command(node)",
	"command(python)",
	"command(python3)",
	"command(pip)",
	"command(pip3)",
	"command(cargo)",
	"command(bash)",
	"command(sh)",
	"command(ls)",
	"command(cat)",
	"command(grep)",
	"command(sed)",
	"command(find)",
	"command(mkdir)",
	"command(chmod)",
	"command(head)",
	"command(tail)",
	"command(rg)",
	"command(gofmt)",
	"command(handoff)",
}

type hooksRestoreState struct {
	Workdir        string `json:"workdir"`
	HooksPath      string `json:"hooks_path"`
	CreatedFile    bool   `json:"created_file"`
	OriginalJSON   []byte `json:"original_json,omitempty"`
	SkipWorktree   bool   `json:"skip_worktree"`
	ExcludePattern string `json:"exclude_pattern,omitempty"`
}

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

// WriteTaskEnv 在 workdir 下准备 workspace 与任务 HOME 的 agy 策略物料，并渲染首回合 prompt。
//
// 参数：workdir 为任务工作区；taskDir 为任务专属目录；sockPath 为权限裁决套接字；
// 其余参数用于 prompt 与 hooks 命令。返回 workspace hooks 路径和首回合 prompt；
// oauth 凭据缺失或任一物料写入失败时返回错误。
func WriteTaskEnv(workdir, taskDir, taskID, planContent, sockPath, handoffBin, disciplineBlock string) (hooksPath, promptText string, err error) {
	log := slog.Default()
	hooksPath = filepath.Join(workdir, agentsDirName, hooksFileName)
	tracked := hooksTracked(workdir)
	log.Info("agy 准备任务环境", "task", taskID, "task_dir", taskDir, "workdir", workdir,
		"hooks", hooksPath, "tracked", tracked)

	state, sidecarExists, stateErr := readHooksRestoreState(taskDir)
	if stateErr != nil {
		log.Error("读取 agy hooks 恢复凭据失败", "task", taskID, "task_dir", taskDir, "cause", stateErr)
		return hooksPath, "", stateErr
	}
	if sidecarExists {
		if state.Workdir != workdir || state.HooksPath != hooksPath {
			err := fmt.Errorf("恢复凭据工作区不匹配: got %s/%s, want %s/%s", state.Workdir, state.HooksPath, workdir, hooksPath)
			log.Error("agy hooks 恢复凭据与任务环境不匹配", "task", taskID, "task_dir", taskDir, "cause", err)
			return hooksPath, "", err
		}
		log.Info("agy 复用既有 hooks 恢复凭据", "task", taskID, "task_dir", taskDir,
			"created_file", state.CreatedFile, "skip_worktree", state.SkipWorktree,
			"exclude_pattern", state.ExcludePattern)
	} else {
		state = hooksRestoreState{Workdir: workdir, HooksPath: hooksPath}
		original, readErr := os.ReadFile(hooksPath)
		switch {
		case readErr == nil:
			state.OriginalJSON = original
		case os.IsNotExist(readErr):
			state.CreatedFile = true
		default:
			log.Error("读取既有 hooks.json 失败", "task", taskID, "path", hooksPath, "cause", readErr)
			return hooksPath, "", fmt.Errorf("读取 %s: %w", hooksPath, readErr)
		}
		if err := writeHooksRestoreState(taskDir, state); err != nil {
			log.Error("写 agy hooks 恢复凭据失败", "task", taskID, "task_dir", taskDir, "cause", err)
			return hooksPath, "", err
		}
	}

	agentsDir := filepath.Join(workdir, agentsDirName)
	if err := os.MkdirAll(agentsDir, 0755); err != nil {
		log.Error("创建 .agents 目录失败", "workdir", workdir, "cause", err)
		return "", "", fmt.Errorf("创建 %s: %w", agentsDir, err)
	}

	log.Info("agy 生成任务环境", "task", taskID, "workdir", workdir, "hooks", hooksPath, "sock", sockPath,
		"tracked", tracked)

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

	agyHome := filepath.Join(taskDir, agyHomeDirName)
	agyGeminiDir := filepath.Join(agyHome, ".gemini")
	agyConfigDir := filepath.Join(agyGeminiDir, "config")
	agyCLIDir := filepath.Join(agyGeminiDir, "antigravity-cli")
	if err := os.MkdirAll(agyConfigDir, 0700); err != nil {
		log.Error("创建 agy 任务 HOME config 目录失败", "task", taskID, "path", agyConfigDir, "cause", err)
		return hooksPath, "", fmt.Errorf("创建 %s: %w", agyConfigDir, err)
	}
	if err := os.MkdirAll(agyCLIDir, 0700); err != nil {
		log.Error("创建 agy 任务 HOME antigravity-cli 目录失败", "task", taskID, "path", agyCLIDir, "cause", err)
		return hooksPath, "", fmt.Errorf("创建 %s: %w", agyCLIDir, err)
	}
	for _, dir := range []string{agyHome, agyGeminiDir, agyConfigDir, agyCLIDir} {
		if err := os.Chmod(dir, 0700); err != nil {
			log.Error("设置 agy 任务 HOME 目录权限失败", "task", taskID, "path", dir, "cause", err)
			return hooksPath, "", fmt.Errorf("设置 %s 权限: %w", dir, err)
		}
	}
	log.Info("agy 任务 HOME 目录已就绪", "task", taskID, "path", agyHome)

	userHome, err := os.UserHomeDir()
	if err != nil {
		log.Error("解析 agy oauth 用户 HOME 失败", "task", taskID, "cause", err)
		return hooksPath, "", fmt.Errorf("解析用户 HOME: %w", err)
	}
	sourceOAuth := filepath.Join(userHome, ".gemini", "antigravity-cli", "antigravity-oauth-token")
	oauthData, err := os.ReadFile(sourceOAuth)
	if err != nil {
		log.Error("读取 agy oauth token 失败", "task", taskID, "source", sourceOAuth, "cause", err)
		return hooksPath, "", fmt.Errorf("读取 agy oauth token %s: %w", sourceOAuth, err)
	}
	destOAuth := filepath.Join(agyCLIDir, "antigravity-oauth-token")
	if err := os.WriteFile(destOAuth, oauthData, 0600); err != nil {
		log.Error("拷贝 agy oauth token 失败", "task", taskID, "source", sourceOAuth, "destination", destOAuth, "cause", err)
		return hooksPath, "", fmt.Errorf("写入 agy oauth token %s: %w", destOAuth, err)
	}
	log.Info("agy oauth token 已拷贝到任务 HOME", "task", taskID, "source", sourceOAuth, "destination", destOAuth, "bytes", len(oauthData))

	originalOAuth := sourceOAuth + ".orig-google-oauth"
	if originalData, readErr := os.ReadFile(originalOAuth); readErr == nil {
		destOriginal := filepath.Join(agyCLIDir, "antigravity-oauth-token.orig-google-oauth")
		if err := os.WriteFile(destOriginal, originalData, 0600); err != nil {
			log.Error("拷贝 agy 原始 oauth token 失败", "task", taskID, "source", originalOAuth, "destination", destOriginal, "cause", err)
			return hooksPath, "", fmt.Errorf("写入 agy 原始 oauth token %s: %w", destOriginal, err)
		}
		log.Info("agy 原始 oauth token 已拷贝到任务 HOME", "task", taskID, "source", originalOAuth, "destination", destOriginal, "bytes", len(originalData))
	} else if !os.IsNotExist(readErr) {
		log.Error("读取 agy 原始 oauth token 失败", "task", taskID, "source", originalOAuth, "cause", readErr)
		return hooksPath, "", fmt.Errorf("读取 agy 原始 oauth token %s: %w", originalOAuth, readErr)
	}

	settings := struct {
		Permissions struct {
			Allow []string `json:"allow"`
		} `json:"permissions"`
	}{}
	settings.Permissions.Allow = append([]string(nil), nativeCommandAllow...)
	settingsJSON, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		log.Error("序列化 agy 原生权限策略失败", "task", taskID, "path", filepath.Join(agyCLIDir, "settings.json"), "cause", err)
		return hooksPath, "", fmt.Errorf("序列化 agy settings.json: %w", err)
	}
	settingsPath := filepath.Join(agyCLIDir, "settings.json")
	if err := os.WriteFile(settingsPath, append(settingsJSON, '\n'), 0600); err != nil {
		log.Error("写 agy 原生权限策略失败", "task", taskID, "path", settingsPath, "cause", err)
		return hooksPath, "", fmt.Errorf("写入 %s: %w", settingsPath, err)
	}
	log.Info("agy 原生权限策略已写入任务 HOME", "task", taskID, "path", settingsPath, "allow_count", len(nativeCommandAllow))

	taskHooksPath := filepath.Join(agyConfigDir, hooksFileName)
	if err := os.WriteFile(taskHooksPath, append(hooksJSON, '\n'), 0600); err != nil {
		log.Error("写 agy 任务 HOME hooks.json 失败", "task", taskID, "path", taskHooksPath, "cause", err)
		return hooksPath, "", fmt.Errorf("写入 %s: %w", taskHooksPath, err)
	}
	log.Info("agy 任务 HOME hooks.json 已写入", "task", taskID, "path", taskHooksPath, "sock", sockPath)

	if tracked {
		needsSkipWorktree := !state.SkipWorktree
		if needsSkipWorktree {
			// 先记恢复动作，再改 index；进程若在两步之间退出，Reap 仍能清掉标记。
			state.SkipWorktree = true
			if err := writeHooksRestoreState(taskDir, state); err != nil {
				log.Error("记录 hooks skip-worktree 状态失败", "task", taskID, "cause", err)
				return hooksPath, "", fmt.Errorf("记录 %s skip-worktree: %w", hooksPath, err)
			}
		}
		if err := updateHooksSkipWorktreeFn(workdir, true); err != nil {
			log.Error("设置 hooks.json skip-worktree 失败", "task", taskID, "workdir", workdir, "cause", err)
			if restoreErr := RestoreTaskEnv(taskDir); restoreErr != nil {
				log.Error("skip-worktree 失败后还原 agy hooks 也失败", "task", taskID, "cause", restoreErr)
			}
			return hooksPath, "", fmt.Errorf("设置 %s skip-worktree: %w", hooksPath, err)
		}
		if needsSkipWorktree {
			log.Info("agy hooks.json 已设置 skip-worktree", "task", taskID, "workdir", workdir)
		} else {
			log.Info("agy hooks.json skip-worktree 已确认", "task", taskID, "workdir", workdir)
		}
	} else {
		pattern := filepath.ToSlash(filepath.Join(agentsDirName, hooksFileName))
		if state.ExcludePattern == "" {
			alreadyExcluded, checkErr := gitExcludeContains(workdir, pattern)
			if checkErr != nil {
				// 无法确认 exclude 状态时不冒险写入；exclude 只是清洁 porcelain 的优化。
				log.Error("检查 agy hooks exclude 失败，继续任务", "task", taskID, "workdir", workdir,
					"pattern", pattern, "cause", checkErr)
			} else if alreadyExcluded {
				log.Info("agy hooks exclude 已由任务前配置提供", "task", taskID, "workdir", workdir,
					"pattern", pattern)
			} else {
				// 先记恢复动作，再写 exclude；这样崩溃不会留下无法追踪的本地规则。
				state.ExcludePattern = pattern
				if err := writeHooksRestoreState(taskDir, state); err != nil {
					log.Error("记录 hooks exclude 状态失败", "task", taskID, "cause", err)
					return hooksPath, "", fmt.Errorf("记录 %s exclude: %w", hooksPath, err)
				}
			}
		}
		if state.ExcludePattern != "" {
			_, excludeErr := ensureGitExcludeFn(workdir, pattern)
			if excludeErr != nil {
				// exclude 只负责隐藏未跟踪 hooks；失败不阻断任务，Restore 仍会清理文件。
				log.Error("追加 agy hooks exclude 失败，继续任务", "task", taskID, "workdir", workdir,
					"pattern", pattern, "cause", excludeErr)
			} else {
				log.Info("agy hooks exclude 已就绪", "task", taskID, "workdir", workdir,
					"pattern", pattern)
			}
		}
	}

	promptText, err = turn.RenderPrompt(taskID, planContent, disciplineBlock)
	if err != nil {
		return hooksPath, "", err
	}
	return hooksPath, promptText, nil
}

// RestoreTaskEnv 从 taskDir 的 sidecar 恢复 agy hooks 物料。
//
// 参数：taskDir 为任务专属目录，sidecar 位于其中。
// 返回：所有恢复动作成功时返回 nil；缺少 sidecar 视为幂等成功。
// 注意：workdir 被回收时跳过文件恢复；已跟踪 hooks 的 skip-worktree 必须清除。
func RestoreTaskEnv(taskDir string) (err error) {
	log := slog.Default()
	sidecarPath := filepath.Join(taskDir, restoreFileName)
	log.Info("agy 开始还原任务 hooks", "task_dir", taskDir, "sidecar", sidecarPath)
	data, err := os.ReadFile(sidecarPath)
	if os.IsNotExist(err) {
		log.Info("agy hooks 无恢复凭据，按幂等路径返回", "task_dir", taskDir)
		return nil
	}
	if err != nil {
		log.Error("读取 agy hooks 恢复凭据失败", "task_dir", taskDir, "cause", err)
		return fmt.Errorf("读取 %s: %w", sidecarPath, err)
	}
	var state hooksRestoreState
	if err := json.Unmarshal(data, &state); err != nil {
		log.Error("解析 agy hooks 恢复凭据失败", "task_dir", taskDir, "cause", err)
		return fmt.Errorf("解析 %s: %w", sidecarPath, err)
	}
	if state.Workdir == "" || state.HooksPath == "" {
		err := fmt.Errorf("恢复凭据字段不完整")
		log.Error("agy hooks 恢复凭据无工作区路径", "task_dir", taskDir, "cause", err)
		return err
	}

	if _, statErr := os.Stat(state.Workdir); os.IsNotExist(statErr) {
		log.Info("工作区已不在，跳过 hooks 还原", "task_dir", taskDir, "workdir", state.Workdir)
		if removeErr := os.Remove(sidecarPath); removeErr != nil {
			log.Error("工作区已回收但删除 agy hooks 恢复凭据失败", "task_dir", taskDir, "cause", removeErr)
			return fmt.Errorf("删除 %s: %w", sidecarPath, removeErr)
		}
		log.Info("agy hooks 恢复凭据已清理", "task_dir", taskDir, "workdir", state.Workdir)
		return nil
	} else if statErr != nil {
		log.Error("检查 agy hooks 工作区失败", "task_dir", taskDir, "workdir", state.Workdir, "cause", statErr)
		return fmt.Errorf("检查工作区 %s: %w", state.Workdir, statErr)
	}

	var restoreErr error
	addRestoreError := func(operation string, cause error) {
		if cause == nil {
			return
		}
		log.Error("agy hooks 恢复步骤失败", "task_dir", taskDir, "workdir", state.Workdir,
			"operation", operation, "cause", cause)
		restoreErr = errors.Join(restoreErr, fmt.Errorf("%s: %w", operation, cause))
	}
	if state.CreatedFile {
		if removeErr := os.Remove(state.HooksPath); removeErr != nil && !os.IsNotExist(removeErr) {
			addRestoreError("删除新建 hooks.json", removeErr)
		}
	} else if writeErr := os.WriteFile(state.HooksPath, state.OriginalJSON, 0644); writeErr != nil {
		addRestoreError("写回 hooks.json 原文", writeErr)
	}

	if state.SkipWorktree {
		if clearErr := updateHooksSkipWorktree(state.Workdir, false); clearErr != nil {
			addRestoreError("清除 hooks.json skip-worktree", clearErr)
		} else {
			log.Info("agy hooks.json skip-worktree 已清除", "task_dir", taskDir, "workdir", state.Workdir)
		}
	}
	if state.ExcludePattern != "" {
		if excludeErr := removeGitExclude(state.Workdir, state.ExcludePattern); excludeErr != nil {
			addRestoreError("撤销 hooks exclude", excludeErr)
		} else {
			log.Info("agy hooks exclude 已撤销", "task_dir", taskDir, "workdir", state.Workdir,
				"pattern", state.ExcludePattern)
		}
	}
	if restoreErr != nil {
		log.Error("agy hooks 还原未完成，保留恢复凭据供重试", "task_dir", taskDir, "workdir", state.Workdir,
			"cause", restoreErr)
		return fmt.Errorf("还原 agy hooks: %w", restoreErr)
	}
	if err := os.Remove(sidecarPath); err != nil {
		log.Error("删除 agy hooks 恢复凭据失败", "task_dir", taskDir, "cause", err)
		return fmt.Errorf("删除 %s: %w", sidecarPath, err)
	}
	log.Info("agy hooks 还原完成", "task_dir", taskDir, "workdir", state.Workdir,
		"created_file", state.CreatedFile, "skip_worktree", state.SkipWorktree)
	return nil
}

func writeHooksRestoreState(taskDir string, state hooksRestoreState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 hooks 恢复凭据: %w", err)
	}
	if err := os.WriteFile(filepath.Join(taskDir, restoreFileName), data, 0600); err != nil {
		return fmt.Errorf("写 hooks 恢复凭据: %w", err)
	}
	return nil
}

func readHooksRestoreState(taskDir string) (hooksRestoreState, bool, error) {
	path := filepath.Join(taskDir, restoreFileName)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return hooksRestoreState{}, false, nil
	}
	if err != nil {
		return hooksRestoreState{}, true, fmt.Errorf("读取 %s: %w", path, err)
	}
	var state hooksRestoreState
	if err := json.Unmarshal(data, &state); err != nil {
		return hooksRestoreState{}, true, fmt.Errorf("解析 %s: %w", path, err)
	}
	return state, true, nil
}

// updateHooksSkipWorktreeFn 是 WriteTaskEnv 在修改 git index 前的测试缝。
var updateHooksSkipWorktreeFn = updateHooksSkipWorktree

// ensureGitExcludeFn 是 WriteTaskEnv 在修改 git exclude 前的测试缝。
var ensureGitExcludeFn = ensureGitExclude

func hooksTracked(workdir string) bool {
	return exec.Command("git", "-C", workdir, "ls-files", "--error-unmatch", filepath.ToSlash(filepath.Join(agentsDirName, hooksFileName))).Run() == nil
}

func updateHooksSkipWorktree(workdir string, skip bool) error {
	mode := "--no-skip-worktree"
	if skip {
		mode = "--skip-worktree"
	}
	path := filepath.ToSlash(filepath.Join(agentsDirName, hooksFileName))
	if out, err := exec.Command("git", "-C", workdir, "update-index", mode, "--", path).CombinedOutput(); err != nil {
		if text := strings.TrimSpace(string(out)); text != "" {
			return fmt.Errorf("git update-index %s: %w: %s", mode, err, text)
		}
		return fmt.Errorf("git update-index %s: %w", mode, err)
	}
	return nil
}

func gitInfoExcludePath(workdir string) (string, error) {
	out, err := exec.Command("git", "-C", workdir, "rev-parse", "--git-path", "info/exclude").CombinedOutput()
	if err != nil {
		if text := strings.TrimSpace(string(out)); text != "" {
			return "", fmt.Errorf("git rev-parse info/exclude: %w: %s", err, text)
		}
		return "", fmt.Errorf("git rev-parse info/exclude: %w", err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("git rev-parse info/exclude 返回空路径")
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workdir, path)
	}
	return path, nil
}

func gitExcludeContains(workdir, pattern string) (bool, error) {
	excludePath, err := gitInfoExcludePath(workdir)
	if err != nil {
		return false, err
	}
	data, err := os.ReadFile(excludePath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取 %s: %w", excludePath, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) == pattern {
			return true, nil
		}
	}
	return false, nil
}

// ensureGitExclude 将 pattern 追加写入 workdir 对应 git 仓库的 info/exclude 文件，
// 避免未跟踪的 hooks.json 导致 git status --porcelain 变脏或触发 ensureCleanWorktree 拦截。
// info/exclude 仅对本地工作树生效，不影响 git 追踪历史，不会被提交或推送到远端。
func ensureGitExclude(workdir, pattern string) (bool, error) {
	log := slog.Default()
	excludePath, err := gitInfoExcludePath(workdir)
	if err != nil {
		log.Error("解析 git info/exclude 路径失败", "workdir", workdir, "pattern", pattern, "cause", err)
		return false, err
	}
	data, err := os.ReadFile(excludePath)
	if err == nil {
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.TrimSpace(line) == pattern {
				return false, nil
			}
		}
	} else if !os.IsNotExist(err) {
		log.Error("读取 git info/exclude 失败", "workdir", workdir, "path", excludePath, "pattern", pattern, "cause", err)
		return false, fmt.Errorf("读取 %s: %w", excludePath, err)
	}
	if err := os.MkdirAll(filepath.Dir(excludePath), 0755); err != nil {
		log.Error("创建 git info/exclude 目录失败", "workdir", workdir, "path", excludePath, "pattern", pattern, "cause", err)
		return false, fmt.Errorf("创建 %s: %w", filepath.Dir(excludePath), err)
	}
	f, err := os.OpenFile(excludePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Error("打开 git info/exclude 失败", "workdir", workdir, "path", excludePath, "pattern", pattern, "cause", err)
		return false, fmt.Errorf("打开 %s: %w", excludePath, err)
	}
	_, writeErr := f.WriteString("\n" + pattern + "\n")
	closeErr := f.Close()
	if writeErr != nil {
		log.Error("写 git info/exclude 失败", "workdir", workdir, "path", excludePath, "pattern", pattern, "cause", writeErr)
		return true, fmt.Errorf("写 %s: %w", excludePath, writeErr)
	}
	if closeErr != nil {
		log.Error("关闭 git info/exclude 失败", "workdir", workdir, "path", excludePath, "pattern", pattern, "cause", closeErr)
		return true, fmt.Errorf("关闭 %s: %w", excludePath, closeErr)
	}
	return true, nil
}

func removeGitExclude(workdir, pattern string) error {
	excludePath, err := gitInfoExcludePath(workdir)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(excludePath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("读取 %s: %w", excludePath, err)
	}
	lines := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(lines))
	removed := false
	for _, line := range lines {
		if strings.TrimSpace(line) == pattern {
			removed = true
			continue
		}
		kept = append(kept, line)
	}
	if !removed {
		return nil
	}
	if err := os.WriteFile(excludePath, []byte(strings.Join(kept, "\n")), 0644); err != nil {
		return fmt.Errorf("写回 %s: %w", excludePath, err)
	}
	return nil
}
