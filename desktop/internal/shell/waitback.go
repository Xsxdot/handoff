// 本文件负责「触发 agentd 重启之后，等它带着新版本回来」（spec §5 承重③、§5.2）。
//
// 职责：
//   - WaitAgentdBack 轮询 agentd 的版本号，直到它等于期望值或超时
//   - 在首次探测失败后主动催一次进程管理器（Windows 的 60 秒重复触发窗口）
//
// 边界：
//   - 不负责换版，也不负责触发重启——那是 DoSync 的职责。分开是因为两者的
//     失败语义不同：换版失败意味着没换成，这里失败意味着换了但没起来
//   - 不做任何补救动作（不回滚、不重装服务）。判不出来就如实报错，由调用方
//     决定怎么告诉用户
package shell

import (
	"context"
	"fmt"
	"time"
)

// waitBackTimeout 是等 agentd 回来的上限。
//
// 90 秒 = Windows 计划任务 60 秒的重复触发窗口 + 余量。macOS 的 launchd
// KeepAlive 是秒级，用不到这么久；上限按最慢的那个平台取，否则 Windows 上
// 会在管理器还没来得及拉起时就判失败。
const waitBackTimeout = 90 * time.Second

// waitBackInterval 是两次探测之间的间隔。
const waitBackInterval = 500 * time.Millisecond

// waitBackNow 是时间缝：生产取真实时间，测试把 deadline 立即推进到过期，
// 不必真的等 90 秒。等待逻辑的承重是「到期返回错误」，这条分支必须能被单测
// 真正走到，不能只靠 context 取消间接覆盖。
var waitBackNow = time.Now

// WaitDeps 是等待动作的外部依赖集合，抽成结构体只为可测——真实实现会发
// HTTP 请求并调用平台的服务管理器，两者都不能在单元测试里真跑。
type WaitDeps struct {
	// Version 返回当前 agentd 自报的版本号。生产实现是
	// client.New(addr, token).Status(ctx) 取 BuildInfo 的版本字段
	Version func(ctx context.Context) (string, error)
	// Nudge 主动催进程管理器把 agentd 拉起来。生产实现在 Windows 上是
	// schtasks /Run（见 internal/service/windows.go:271），其余平台可为 nil
	Nudge func() error
	// Sleep 是可注入的等待，测试用它避免真睡
	Sleep func(time.Duration)
}

// WaitAgentdBack 等 agentd 重启完成并带着 wantVer 这个版本回来。
//
// 参数：
//   - wantVer: 期望的版本号（即内嵌二进制的版本，embedbin.Version）
//
// 返回：
//   - 超时或 ctx 取消时返回错误；探测本身的错误不终止循环（重启期间连不上
//     是正常的），只在超时后作为最后一次的原因带出去
//
// 注意（承重）：
//   - **判据是版本号相等，不是「调得通」。** agentd 优雅关停期间仍会应答在途
//     请求，只判连通会立刻通过，然后握手到一个正在退出的进程上
//   - **Nudge 只在首次探测失败之后调一次。** 早于此调用会撞上 Windows 的
//     MultipleInstancesPolicy=IgnoreNew——旧进程还没退时催会被拒，而拒绝码
//     0x800710E0 正是 rc5 那个 bug 的同一个值
func WaitAgentdBack(ctx context.Context, wantVer string, d WaitDeps) error {
	sleep := d.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	logger.Info("等 agentd 带着新版本回来", "want_version", wantVer, "timeout", waitBackTimeout)

	deadline := waitBackNow().Add(waitBackTimeout)
	nudged := false
	var lastErr error
	var lastVer string
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			logger.Error("等待被取消", "want_version", wantVer, "attempts", attempt, "cause", err)
			return fmt.Errorf("等 agentd 回来被取消（已试 %d 次，最后看到版本 %q）: %w", attempt, lastVer, err)
		}
		ver, err := d.Version(ctx)
		switch {
		case err != nil:
			lastErr = err
			// 循环内高频日志降级到 Debug，否则 90 秒会刷出近两百行
			logger.Debug("探测 agentd 版本失败，继续等", "attempt", attempt, "cause", err)
		case ver == wantVer:
			logger.Info("agentd 已带新版本回来", "version", ver, "attempts", attempt)
			return nil
		default:
			lastVer = ver
			// 这一支正是本函数存在的理由：连得上、但还是旧进程在应答
			logger.Debug("agentd 应答的仍是旧版本，继续等", "attempt", attempt, "got", ver, "want", wantVer)
		}

		// 首次探测之后才催：早于此旧进程可能还没退，Windows 上会被
		// IgnoreNew 拒掉且把拒绝码写进「上次结果」
		if !nudged && d.Nudge != nil {
			nudged = true
			if nerr := d.Nudge(); nerr != nil {
				logger.Warn("催进程管理器拉起 agentd 失败，改为等它自己拉", "cause", nerr)
			} else {
				logger.Info("已催进程管理器拉起 agentd")
			}
		}

		if waitBackNow().After(deadline) {
			logger.Error("等 agentd 回来超时", "want_version", wantVer, "last_version", lastVer,
				"attempts", attempt, "cause", lastErr)
			return fmt.Errorf("等 agentd 回到 %s 超时（%s，已试 %d 次，最后看到版本 %q，最后错误 %v）",
				wantVer, waitBackTimeout, attempt, lastVer, lastErr)
		}
		sleep(waitBackInterval)
	}
}
