// 本文件把「打开控制台之前的版本对账与同步」整条序列收在一个可测函数里
// （spec §5）。
//
// 职责：
//   - SyncOnOpen 按承重顺序编排：EnsureRunning → 探 Busy → PlanSync → DoSync
//     → WaitAgentdBack，并把结果折成一个 SyncOutcome 交给调用方
//
// 边界（承重）：
//   - **顺序必须留在本文件，不能散回 main.go。** 散回去它就无法被单独测试，
//     而这是整个设计最容易被后人改坏的地方（见 TestSyncOnOpenOrderIsLoadBearing）
//   - **本函数绝不阻断调用方**（spec D8）：任何失败都折进 SyncOutcome.Err
//     返回，绝不 panic、绝不 os.Exit、绝不无限等待。调用方拿到 Err 之后
//     仍然必须继续加载控制台
//   - 不碰 UI。进度只经 Progress 回调往外送，本文件不知道有没有窗口
package shell

import (
	"context"
	"fmt"
)

// OpenSyncDeps 是打开控制台前那段同步序列的全部外部依赖。
type OpenSyncDeps struct {
	// EnsureRunning 确保 agentd 在跑。必须最先调——闸一判据要从它那儿探
	EnsureRunning func() error
	// InstalledPath 返回已装二进制的路径（生产实现是 ResolveBinPath("")）。
	// 注意取的是**实际在用的**那一份，不是约定落点：agentd 正是从它启动的
	InstalledPath func() (string, error)
	// InstalledVersion 从二进制里读版本号，读不出返回空串
	InstalledVersion func(path string) string
	// Busy 返回活跃任务数。生产实现是 client.Status 取 len(Active)
	Busy func(ctx context.Context) (int, error)
	// EmbedVersion 是内嵌二进制的版本（embedbin.Version），开发构建下为空
	EmbedVersion string
	// EmbedAvailable 是本次构建有没有内嵌二进制（embedbin.Available()）
	EmbedAvailable bool
	// Sync 是换版动作的依赖，见 DoSync
	Sync SyncDeps
	// Wait 是等待 agentd 回来的依赖，见 WaitAgentdBack
	Wait WaitDeps
	// Progress 是阶段回调，供 UI 显示。传 nil 安全
	Progress func(stage string)
}

// SyncOutcome 是一次对账的结果。
//
// **Err 非 nil 不代表调用方该停下。** 它只说明「同步这件事没做成」，控制台
// 照样要打开（spec D8）。调用方的正确处置是：把 Err 如实展示出来，然后继续。
type SyncOutcome struct {
	// Plan 是四态决策结果，供 UI 决定显示什么
	Plan SyncPlan
	// Busy 是探到的活跃任务数；探测失败时为 -1
	Busy int
	// Err 是同步过程中的错误，nil 表示没出错（含「本就不需要同步」）
	Err error
}

// SyncOnOpen 在打开控制台之前对账并（必要时）同步 agentd/CLI 到内嵌的版本。
//
// 返回的 SyncOutcome 永远可用，**本函数不会返回错误、不会 panic、不会挂住**。
//
// 注意：调用方拿到结果后必须继续加载控制台，无论 Outcome.Err 是什么。
func SyncOnOpen(ctx context.Context, d OpenSyncDeps) SyncOutcome {
	progress := d.Progress
	if progress == nil {
		progress = func(string) {}
	}

	// ① EnsureRunning 必须最先：闸一判据要从 agentd 的 /api/status 探，
	// 它不在跑就探不出。这里起的是**旧**二进制，无妨——同步紧接着会重启它
	if err := d.EnsureRunning(); err != nil {
		logger.Error("确保 agentd 运行失败，跳过本次对账", "cause", err)
		return SyncOutcome{Plan: SyncSkip, Busy: -1, Err: fmt.Errorf("确保 agentd 运行: %w", err)}
	}

	installed, err := d.InstalledPath()
	if err != nil {
		logger.Error("定位已装 handoff 失败，跳过本次对账", "cause", err)
		return SyncOutcome{Plan: SyncSkip, Busy: -1, Err: fmt.Errorf("定位已装 handoff: %w", err)}
	}
	installedVer := d.InstalledVersion(installed)
	decision := DecideRelease(installed, installedVer, d.EmbedVersion)

	// ② 闸一必须在换文件之前，与 cmd/upgrade.go:500 同序。反过来会留下
	// 「磁盘是新的、跑着的是旧的」这种持续不一致，且用户看不出为什么
	busy, berr := d.Busy(ctx)
	if berr != nil {
		// 探不出就按「有任务」处置：猜错的代价不对称——误判空闲会在用户
		// 有活跃任务时重启 agentd，误判繁忙只是这次不升级
		logger.Warn("探活跃任务数失败，按有任务处置", "cause", berr)
		busy = -1
	}

	plan := PlanSync(decision, busy, d.EmbedAvailable)
	logger.Info("同步对账结果", "plan", plan.String(), "decision", decision.String(),
		"installed", installed, "installed_version", installedVer,
		"embedded_version", d.EmbedVersion, "busy", busy)

	if plan != SyncDo {
		return SyncOutcome{Plan: plan, Busy: busy}
	}

	if err := DoSync(ctx, installed, false, d.Sync, progress); err != nil {
		logger.Error("同步失败，将用现有版本继续打开控制台", "cause", err)
		return SyncOutcome{Plan: plan, Busy: busy, Err: err}
	}

	// ③ 等 agentd 带着新版本回来才算完。不等就握手，会打到一个正在退出的
	// 进程上——报错是 401 或连接被拒，看起来跟「刚升过级」毫无关系
	progress("正在等 agentd 重启完成")
	if err := WaitAgentdBack(ctx, d.EmbedVersion, d.Wait); err != nil {
		logger.Error("agentd 未在预期时间内带新版本回来", "want_version", d.EmbedVersion, "cause", err)
		return SyncOutcome{Plan: plan, Busy: busy, Err: err}
	}
	logger.Info("同步完成", "version", d.EmbedVersion)
	return SyncOutcome{Plan: plan, Busy: busy}
}
