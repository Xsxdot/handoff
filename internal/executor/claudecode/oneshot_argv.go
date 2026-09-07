package claudecode

import "github.com/Xsxdot/handoff/internal/executor"

// OneShotArgv 编码 claude 一次性调用的 argv。
func OneShotArgv(model, prompt string, _ executor.OneShotLimits) ([]string, error) {
	if model != "" {
		return []string{"claude", "-p", "--model", model, prompt}, nil
	}
	return []string{"claude", "-p", prompt}, nil
}
