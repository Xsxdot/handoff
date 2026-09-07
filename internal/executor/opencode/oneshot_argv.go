package opencode

import "github.com/Xsxdot/handoff/internal/executor"

// OneShotArgv 编码 opencode 一次性调用的 argv。
func OneShotArgv(model, prompt string, _ executor.OneShotLimits) ([]string, error) {
	if model != "" {
		return []string{"opencode", "run", "-m", model, prompt}, nil
	}
	return []string{"opencode", "run", prompt}, nil
}
