// 本文件负责内嵌 handoff 二进制的「释出」决策与实际落盘（spec §5.3）。
//
// 职责：
//   - DecideRelease 是纯函数：吃「现状」（有没有安装、已有版本、内嵌版本），
//     吐出三态动作之一，不碰文件系统——因此三态规则可以穷举测试，无需造
//     18MB 的假二进制。
//   - ReleaseBinary 才碰盘：把数据流写落指定位置，供 agentd 服务直接使用。
//
// 边界（承重，见 spec §5.3）：
//   - **绝不覆盖用户已有的安装**。已有且能用就直接用；已有但比内嵌旧只提示
//     不自动换——换版要重启 agentd，自动换会打断正在跑的任务。因此
//     ReleaseBinary 对已存在的目标一律报错，绝不「顺手覆盖」。
//   - 版本判不出的所有分支一律偏保守（用用户已有的），因为猜错的代价不对称：
//     不覆盖最坏是用户少个新特性，覆盖错了是把用户手装的二进制换掉。
//   - 版本比较统一走 selfupdate.CompareVersion
package shell

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/Xsxdot/handoff/internal/selfupdate"
)

// logger 是包级日志入口，默认 slog.Default()。
//
// 为什么用可变包级变量而不是函数参数：DecideRelease 与 ReleaseBinary 的签名由
// spec §5.3 固定（前者纯函数不吃日志，后者只收 (dst, data)），带不上日志参数。
// 手法与 lifecycle.newManager 同一套「测试缝」，必要时可注入替身。
var logger = slog.Default()

// ReleaseDecision 是释出动作的三态枚举。
type ReleaseDecision int

const (
	// DecisionInstall 表示没有既有安装，应释出内嵌的那份。
	DecisionInstall ReleaseDecision = iota
	// DecisionUseExisting 表示直接使用用户已有的安装，不释出。
	DecisionUseExisting
	// DecisionNotifyOutdated 表示已有安装比内嵌的旧：只提示，不自动换。
	DecisionNotifyOutdated
)

// String 返回三态的可读名。
func (d ReleaseDecision) String() string {
	switch d {
	case DecisionInstall:
		return "install"
	case DecisionUseExisting:
		return "use-existing"
	case DecisionNotifyOutdated:
		return "notify-outdated"
	default:
		return fmt.Sprintf("ReleaseDecision(%d)", int(d))
	}
}

// DecideRelease 根据「现状」决定对已安装的 handoff 采取哪种动作，是纯函数。
//
// 参数：
//   - existing: 已有安装的路径，空串表示本机没有安装
//   - existVer: 已有安装的版本号（形如 vX.Y.Z），空串表示判不出
//   - embedVer: 内嵌二进制的版本号，开发构建未注入 ldflags 时为空
//
// 判据（偏保守）：
//   - existing 为空 → DecisionInstall，无条件释出。此时没有既有安装可覆盖，
//     「绝不覆盖」这条承重不适用；内嵌版本判不出也不影响——若 embedbin 实际
//     不可用，由调用方在 ReleaseBinary 之前检查 embedbin.Available() 兜底。
//   - existing 非空时，任何「版本判不出」或「已有不旧于内嵌」都走
//     DecisionUseExisting；只有确认已有比内嵌旧才 DecisionNotifyOutdated。
//
// 注意：本函数不写日志。决策日志（三态、已有版本、内嵌版本）由调用方在拿到
// 返回值后自行记录，以免破坏「纯函数」这条约束。
func DecideRelease(existing, existVer, embedVer string) ReleaseDecision {
	if existing == "" {
		return DecisionInstall
	}
	// 已有安装。以下分支全走保守：绝不用内嵌的换掉用户手上的。
	if embedVer == "" {
		// 内嵌版本判不出（开发构建未注入）：不知道内嵌的多新，就不能假设
		// 覆盖旧版安全，直接用用户的。
		return DecisionUseExisting
	}
	cmp, ok := selfupdate.CompareVersion(existVer, embedVer)
	if !ok {
		// 已有版本判不出（空或形态不符）：猜错代价不对称——不覆盖最坏是用户
		// 少个新特性，覆盖错了是把用户手装的二进制换掉。用用户的。
		return DecisionUseExisting
	}
	switch {
	case cmp >= 0:
		// 已有的不比内嵌旧（更新或同版），直接用。
		return DecisionUseExisting
	default:
		// 已有的比内嵌旧：只提示，不自动换——换版要重启 agentd，自动换会
		// 打断正在跑的任务。
		return DecisionNotifyOutdated
	}
}

// ReleaseBinary 把 data 原样写落到 dst，供 agentd 直接执行。
//
// 规则（承重）：
//   - 目标已存在（含悬空的符号链接）一律报错返回，绝不覆盖。DecideRelease
//     说了不释出还走到这里，属于调用方 bug，此时必须报错而不是「顺手覆盖」。
//   - 释出走「临时文件 + rename」落位：写入中途产生的半截文件永远不会以
//     dst 这个名出现，也就不会被 launchd 当成可执行拉起来。
//   - 最终权限 0755，否则 launchd 拉不起来（症状是「装好了但 agentd 起不来」）。
func ReleaseBinary(dst string, data io.Reader) error {
	if _, err := os.Lstat(dst); err == nil {
		logger.Error("拒绝释出：目标已存在，绝不覆盖用户安装", "dst", dst)
		return fmt.Errorf("释出目标已存在 %s: %w", dst, os.ErrExist)
	} else if !os.IsNotExist(err) {
		logger.Error("检查释出目标失败", "dst", dst, "cause", err)
		return fmt.Errorf("检查释出目标 %s: %w", dst, err)
	}
	dir := filepath.Dir(dst)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		logger.Error("创建释出目录失败", "dir", dir, "cause", err)
		return fmt.Errorf("创建释出目录 %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".handoff-release-*")
	if err != nil {
		logger.Error("创建临时文件失败", "dst", dst, "cause", err)
		return fmt.Errorf("创建临时文件: %w", err)
	}
	tmpName := tmp.Name()
	// 成功落位后置空，防止 defer 把刚 rename 出去的文件删掉。
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, data); err != nil {
		tmp.Close()
		logger.Error("写入内嵌二进制失败", "dst", dst, "cause", err)
		return fmt.Errorf("写入内嵌二进制: %w", err)
	}
	if err := tmp.Close(); err != nil {
		logger.Error("关闭临时文件失败", "dst", dst, "cause", err)
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	// 在 rename 前就 chmod 0755：dst 一出现就带着正确的执行位，不存在
	// 「已可见但还没权限」的窗口。
	if err := os.Chmod(tmpName, 0o755); err != nil {
		logger.Error("设置执行权限失败", "dst", dst, "cause", err)
		return fmt.Errorf("设置执行权限: %w", err)
	}
	if err := os.Rename(tmpName, dst); err != nil {
		logger.Error("落位释出目标失败", "dst", dst, "cause", err)
		return fmt.Errorf("落位释出目标: %w", err)
	}
	tmpName = ""
	logger.Info("内嵌二进制已释出", "dst", dst, "perm", "0755")
	return nil
}
