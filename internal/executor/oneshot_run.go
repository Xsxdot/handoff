package executor

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

const maxOneShotOutput = 64 << 10

func RunOneShotProcess(ctx context.Context, argv []string, env []string, workdir, homeDir string) (string, error) {
	if ctx == nil {
		return "", fmt.Errorf("OneShot.Invoke 禁止 nil ctx")
	}
	if len(argv) == 0 {
		return "", fmt.Errorf("OneShot argv 为空")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if len(env) > 0 {
		cmd.Env = env
	}
	if workdir != "" {
		cmd.Dir = workdir
	}
	if homeDir != "" {
		cmd.Env = append(cmd.Environ(), "HOME="+homeDir)
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	b := out.Bytes()
	if len(b) > maxOneShotOutput {
		b = b[:maxOneShotOutput]
	}
	return string(b), err
}
