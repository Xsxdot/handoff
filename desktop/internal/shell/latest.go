// 本文件负责「有没有新版安装包可下载」的查询与限流（spec §6）。
//
// 职责：
//   - CheckLatest 查最新 release 并与本机版本比，回答「要不要提示用户去下新版」
//
// 边界（承重）：
//   - **与同步路完全独立。** 同步用内嵌的字节、不出网、必然可完成；本文件
//     要出网、可失败。两者共用同一个进程但不共享任何状态，一条挂了不影响另一条
//   - **任何失败一律静默**，当作「没有新版」。通知是锦上添花，它自己绝不能
//     成为故障源（沿用 internal/selfupdate/clicheck.go 文件头的既有约定）
//   - 不下载、不安装。点击后打开浏览器由 UI 层做，本文件只回答查询
package shell

import (
	"context"
	"time"

	"github.com/Xsxdot/handoff/internal/selfupdate"
)

// LatestDeps 是查询的外部依赖，抽出来只为可测（真实实现要发 HTTP）。
type LatestDeps struct {
	// Fetch 返回最新 release 的 tag。生产实现是 release.NewClient(...).Latest
	Fetch func(ctx context.Context) (string, error)
	// Now 取当前时间，用于判缓存新鲜度
	Now func() time.Time
}

// CheckLatest 查有没有比 current 更新的 release。
//
// 参数：
//   - dataDir: handoff 的数据目录，限流缓存放在它下面
//   - current: 本机版本（即 embedbin.Version）。**为空一律返回不提示**
//
// 返回：
//   - tag: 最新版本号；newer 为 false 时无意义
//   - newer: 是否确实更新。任何失败、任何判不出，一律 false
//
// 注意：
//   - 缓存与 CLI 侧的更新提示**共用同一个文件**（selfupdate.CLICheckPath）。
//     看着像耦合，其实正是要的：api.github.com 有 60 次/小时/IP 的匿名限流，
//     多个消费者各查各的正是触发限流的方式，而限流一旦触发，agentd 的换版
//     也会跟着失败
//   - 方向判断走 selfupdate.CompareVersion，只判「不相等」会造出反向提示
//     （B59 验收当场抓出过：装了 v0.1.1 的机器被劝升到 v0.1.0）
func CheckLatest(ctx context.Context, dataDir, current string, d LatestDeps) (string, bool) {
	if current == "" {
		// 开发构建未注入版本。既不能说有新版（不知道跟谁比），也不能瞎猜——
		// 两种猜错的症状（一直提示 / 永不提示）都不报错，排查成本极高。
		// 提前返回还省掉一次请求：那是宝贵的限流额度
		logger.Debug("本机版本判不出，跳过新版检查")
		return "", false
	}
	now := time.Now
	if d.Now != nil {
		now = d.Now
	}

	c := selfupdate.LoadCLICheck(dataDir)
	if selfupdate.CLICheckStale(c, now()) {
		if d.Fetch == nil {
			logger.Debug("未配置 Fetch，跳过新版检查")
			return "", false
		}
		logger.Info("检查有没有新版安装包", "current", current)
		tag, err := d.Fetch(ctx)
		if err != nil {
			// 静默：见文件头的边界约定
			logger.Debug("查最新 release 失败，按没有新版处理", "cause", err)
			return "", false
		}
		c = &selfupdate.CLICheck{CheckedAt: now(), Latest: tag}
		if err := selfupdate.SaveCLICheck(dataDir, c); err != nil {
			// 写不进缓存不影响本次结论，只是下次会再查一遍
			logger.Debug("回写检查缓存失败，本次结论不受影响", "cause", err)
		}
	}
	if c == nil || c.Latest == "" {
		return "", false
	}
	cmp, ok := selfupdate.CompareVersion(c.Latest, current)
	if !ok || cmp <= 0 {
		logger.Debug("没有更新的版本", "latest", c.Latest, "current", current, "comparable", ok)
		return "", false
	}
	logger.Info("发现新版安装包", "latest", c.Latest, "current", current)
	return c.Latest, true
}
