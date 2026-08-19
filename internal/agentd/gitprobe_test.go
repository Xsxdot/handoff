// gitprobe_test.go —— gitProbe 的两条约定：探测未命中不记 ERROR，且返回值与
// gitRun 逐字段等价。
//
// 边界：本文件不测 gitRun 的成功路径（既有测试已覆盖），只测「失败时的日志级别」
// 与「两者返回值一致」这两件 gitProbe 独有的事。
package agentd

import (
	"context"
	"log/slog"
	"strings"
	"testing"
)

// captureLogs 把 slog 默认 logger 换成写进 buffer 的 Debug 级 handler，
// 返回读取函数；测试结束自动还原。
//
// 为什么改默认 logger：workspace.go 的 log() 取的是 slog.Default()（运行时取值，
// 不是依赖注入），这是本包既有的约定，测试只能从这里切进去。
func captureLogs(t *testing.T) func() string {
	t.Helper()
	var buf strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prev) })
	return buf.String
}

// TestGitProbeMissDoesNotLogError 钉住 B81：探测未命中记 DEBUG 不记 ERROR。
func TestGitProbeMissDoesNotLogError(t *testing.T) {
	repo := initTestRepo(t)
	logs := captureLogs(t)

	_, _, err := gitProbe(context.Background(), repo, "rev-parse", "--verify", "--quiet", "refs/heads/绝不存在的分支")
	if err == nil {
		t.Fatal("探测不存在的分支应返回非 nil error")
	}
	out := logs()
	if strings.Contains(out, "level=ERROR") {
		t.Errorf("探测未命中不该产生 ERROR；日志：%s", out)
	}
	if !strings.Contains(out, "level=DEBUG") {
		t.Errorf("探测未命中应留一条 DEBUG 供排障；日志：%s", out)
	}
}

// TestGitRunMissStillLogsError 反向钉住：真故障通道没被一起降级。
func TestGitRunMissStillLogsError(t *testing.T) {
	repo := initTestRepo(t)
	logs := captureLogs(t)

	if _, _, err := gitRun(context.Background(), repo, "rev-parse", "--verify", "--quiet", "refs/heads/绝不存在的分支"); err == nil {
		t.Fatal("应返回非 nil error")
	}
	if out := logs(); !strings.Contains(out, "level=ERROR") {
		t.Errorf("gitRun 的失败仍应是 ERROR；日志：%s", out)
	}
}

// TestGitProbeReturnsSameAsGitRun 钉住两者返回值逐字段等价——gitProbe 只改日志
// 级别，调用方仍按 err != nil 判未命中，不得有任何语义差异。
func TestGitProbeReturnsSameAsGitRun(t *testing.T) {
	repo := initTestRepo(t)
	args := []string{"rev-parse", "--verify", "--quiet", "refs/heads/绝不存在的分支"}

	runOut, runErrText, runErr := gitRun(context.Background(), repo, args...)
	probeOut, probeErrText, probeErr := gitProbe(context.Background(), repo, args...)

	if runOut != probeOut {
		t.Errorf("stdout 不一致：gitRun=%q gitProbe=%q", runOut, probeOut)
	}
	if runErrText != probeErrText {
		t.Errorf("stderr 不一致：gitRun=%q gitProbe=%q", runErrText, probeErrText)
	}
	if (runErr == nil) != (probeErr == nil) {
		t.Errorf("error 有无不一致：gitRun=%v gitProbe=%v", runErr, probeErr)
	}
}
