package agy

import "github.com/Xsxdot/handoff/internal/executor"

// OneShotArgv 编码 agy 一次性调用的 argv。
func OneShotArgv(model, prompt string, _ executor.OneShotLimits) ([]string, error) {
	if model != "" {
		return []string{"agy", "--model", model, "-p", prompt}, nil
	}
	return []string{"agy", "-p", prompt}, nil
}
