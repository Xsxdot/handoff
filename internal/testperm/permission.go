// Package testperm 提供只供测试使用的文件权限前提探针。
//
// 职责：施加读/写限制，直接尝试对应操作，并在当前机器无法表达限制时 skip。
// 边界：只服务测试辅助，不参与生产运行时；不检查 euid，不改变运行身份，不注入生产错误。
package testperm

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"testing"
)

type probeAction uint8

const (
	probeContinue probeAction = iota
	probeSkip
	probeFatal
)

type probeDecision struct {
	action              probeAction
	restoreBeforeAction bool
	message             string
}

// DenyWrite 清除 path 的全部写 permission bits，并用一次真实写探针确认限制。
//
// 参数：t 是测试句柄；path 是已存在的目录或普通文件。
// 返回：无；限制生效则返回给调用点继续执行，限制失效则恢复现场并 skip。
// 注意：只有 errors.Is(err, fs.ErrPermission) 才算限制生效；其他探针错误会使测试失败。
func DenyWrite(t testing.TB, path string) {
	t.Helper()
	info := targetInfo(t, path, "写")
	restricted := info.Mode().Perm() &^ 0o222
	if info.IsDir() {
		apply(t, path, info.Mode().Perm(), restricted, "写", func() error {
			f, err := os.CreateTemp(path, ".handoff-permission-probe-*")
			if err != nil {
				return err
			}
			name := f.Name()
			closeErr := f.Close()
			removeErr := os.Remove(name)
			if closeErr != nil {
				return closeErr
			}
			return removeErr
		})
		return
	}
	apply(t, path, info.Mode().Perm(), restricted, "写", func() error {
		f, err := os.OpenFile(path, os.O_WRONLY, 0)
		if err != nil {
			return err
		}
		return f.Close()
	})
}

// DenyRead 清除 path 的全部读 permission bits，并用一次真实读探针确认限制。
//
// 参数：t 是测试句柄；path 是已存在的普通文件。
// 返回：无；限制生效则返回给调用点继续执行，限制失效则恢复现场并 skip。
// 注意：目录不是本入口的目标；无关探针错误不会被转换成 skip。
func DenyRead(t testing.TB, path string) {
	t.Helper()
	info := targetInfo(t, path, "读")
	if info.IsDir() {
		slog.Default().Error("读权限前提目标类型不支持", "operation", "读", "path", path, "reason", "目标是目录而不是普通文件")
		t.Fatalf("读权限前提目标必须是文件，path=%q", path)
		return
	}
	restricted := info.Mode().Perm() &^ 0o444
	apply(t, path, info.Mode().Perm(), restricted, "读", func() error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		return f.Close()
	})
}

func targetInfo(t testing.TB, path, operation string) os.FileInfo {
	logger := slog.Default()
	logger.Info("测试权限前提入口", "operation", operation, "path", path)
	logger.Debug("读取测试权限目标状态", "operation", operation, "path", path)
	info, err := os.Stat(path)
	if err != nil {
		logger.Error("读取测试权限目标状态失败", "operation", operation, "path", path, "err", err)
		t.Fatalf("权限前提目标不可用（operation=%s path=%q）: %v", operation, path, err)
		return nil
	}
	logger.Debug("测试权限目标状态已读取", "operation", operation, "path", path,
		"mode", info.Mode().Perm(), "is_dir", info.IsDir())
	return info
}

func apply(t testing.TB, path string, original, restricted os.FileMode, operation string,
	probe func() error) {
	logger := slog.Default()
	logger.Debug("施加测试权限限制前", "operation", operation, "path", path,
		"original_mode", original, "restricted_mode", restricted)
	if err := os.Chmod(path, restricted); err != nil {
		logger.Error("施加测试权限限制失败", "operation", operation, "path", path,
			"restricted_mode", restricted, "err", err)
		t.Fatalf("权限前提无法设置（operation=%s path=%q mode=%#o）: %v",
			operation, path, restricted, err)
		return
	}
	logger.Info("测试权限限制已施加", "operation", operation, "path", path,
		"restricted_mode", restricted)

	restored := false
	restore := func() {
		if restored {
			return
		}
		restored = true
		logger.Debug("恢复测试权限限制前", "operation", operation, "path", path,
			"original_mode", original)
		if err := os.Chmod(path, original); err != nil {
			logger.Error("恢复测试权限限制失败", "operation", operation, "path", path,
				"original_mode", original, "err", err)
			t.Errorf("恢复权限前提失败（operation=%s path=%q mode=%#o）: %v",
				operation, path, original, err)
			return
		}
		logger.Info("测试权限限制已恢复", "operation", operation, "path", path,
			"original_mode", original)
	}
	t.Cleanup(restore)

	logger.Debug("执行测试权限探针前", "operation", operation, "path", path)
	probeErr := probe()
	logger.Debug("执行测试权限探针后", "operation", operation, "path", path, "err", probeErr)
	decision := decideProbe(operation, path, probeErr)
	switch decision.action {
	case probeContinue:
		logger.Info("测试权限限制已被探针证实", "operation", operation, "path", path,
			"probe_err", probeErr)
	case probeSkip:
		logger.Warn("当前机器无法表达测试权限前提", "operation", operation, "path", path)
		restore()
		t.Skipf("%s", decision.message)
	case probeFatal:
		logger.Error("测试权限探针出现无关错误", "operation", operation, "path", path,
			"err", probeErr)
		restore()
		t.Fatalf("%s", decision.message)
	}
}

func decideProbe(operation, path string, probeErr error) probeDecision {
	if probeErr == nil {
		return probeDecision{
			action:              probeSkip,
			restoreBeforeAction: true,
			message: fmt.Sprintf("权限前提未成立：%s 探针成功，当前机器无法表达 path=%q 的限制；这不是禁用用例",
				operation, path),
		}
	}
	if errors.Is(probeErr, fs.ErrPermission) {
		return probeDecision{
			action:  probeContinue,
			message: fmt.Sprintf("限制已生效：%s 探针被拒绝，path=%q", operation, path),
		}
	}
	return probeDecision{
		action:              probeFatal,
		restoreBeforeAction: true,
		message: fmt.Sprintf("权限探针出现无关错误：operation=%s path=%q err=%v",
			operation, path, probeErr),
	}
}
