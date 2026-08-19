//go:build windows

// managed_windows.go —— Windows 侧的托管判据。
//
// 职责：回答「本进程 exit(0) 之后，还有没有人把同一个二进制拉起来」。
// 边界：不装、不卸、不启停服务——那是 internal/service 的事，这里只查。
package selfupdate

import (
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/service"
)

func init() { platformManaged = windowsManaged }

// windowsManaged 用「计划任务指不指向我」当托管判据。
//
// 为什么不能沿用环境变量那条路：systemd 注入 INVOCATION_ID、launchd 注入
// XPC_SERVICE_NAME，而 Task Scheduler 对被它拉起的进程**什么都不注入**，
// 「谁把我拉起来的」在 Windows 上根本问不出来。换个问法就问得出，而且问的
// 恰好是闸二真正要的那个保证：任务在不在、它登记的是不是我这个二进制。
//
// 返回：(是否托管, 判否的理由原文)。取不到自身路径时 fail-closed。
func windowsManaged() (bool, string) {
	exe, err := os.Executable()
	if err != nil {
		slog.Default().Warn("托管判据：拿不到自身可执行文件路径，按非托管处理", "cause", err)
		return false, "拿不到本进程的可执行文件路径: " + err.Error()
	}
	// 与 service install 写 XML 时同一条纪律：解到真实路径再比，
	// 否则一边是 symlink 一边是目标，字面比对必然对不上
	if resolved, rerr := filepath.EvalSymlinks(exe); rerr == nil {
		exe = resolved
	}
	ok, why := service.UnitReferences(slog.Default(), exe)
	if !ok {
		slog.Default().Debug("托管判据：判否", "exe", exe, "reason", why)
	}
	return ok, why
}
