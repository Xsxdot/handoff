package codex

import "github.com/Xsxdot/handoff/internal/executor"

// OneShotArgv 编码 codex 一次性调用的 argv。
func OneShotArgv(model, prompt string, _ executor.OneShotLimits) ([]string, error) {
	argv := []string{"codex", "exec", "--skip-git-repo-check", "--ephemeral",
		"--color", "never", "--sandbox", "read-only", "--ignore-user-config"}
	if model != "" {
		argv = append(argv, "-m", model)
	}
	return append(argv, prompt), nil
}
