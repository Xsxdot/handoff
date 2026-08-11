// updater.go —— 更新循环：定时查 → 下好待命 → 等空闲 → 换版 → 触发关停。
//
// 边界：
//   - 不 import internal/agentd：关停经注入的 Shutdown 闭包完成，避免包环
//   - 每一轮的任何失败都在内部处置并记日志，Tick 不返回错误——循环不能因为
//     一次网络抖动就停掉
package selfupdate

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/xushixin/handoff/internal/release"
)

// Checker 查最新发布。生产实现是 *release.Client。
type Checker interface {
	Latest(ctx context.Context) (release.Release, error)
}

// Fetcher 下载并校验某个发布，返回可供替换的临时文件路径。
// 生产实现是 *release.Installer。
type Fetcher interface {
	Fetch(ctx context.Context, rel release.Release, destDir string) (string, error)
}

// Options 是 Updater 的全部依赖。全部经注入，便于测试。
type Options struct {
	// DataDir 是 agentd 数据目录，pending.json 落在它下面。
	DataDir string
	// CurrentVersion 是本进程的 release 版本；**空串表示非 release 构建**。
	CurrentVersion string
	// Interval 是两轮之间的间隔。
	Interval time.Duration
	// BusyCount 返回当前活跃任务数（running + waiting_answer）。
	BusyCount func() (int, error)
	// Shutdown 触发优雅关停，返回是否本次触发生效。生产实现是 agentd.Shutdown.Trigger。
	Shutdown func(reason string) bool
	// Checker / Fetcher 见上。
	Checker Checker
	Fetcher Fetcher
	// Getenv 取环境变量（托管判据用）。生产传 os.Getenv。
	Getenv func(string) string
	// Executable 返回当前二进制路径（应已 EvalSymlinks 解析）。
	Executable func() (string, error)
	// Activate 把新二进制换到目标路径。生产实现是 release.Activate。
	Activate func(newPath, target string) (prevPath string, err error)
	// Log 是日志入口。
	Log *slog.Logger
}

// Updater 是更新循环。
type Updater struct {
	o Options

	// failedTags 记住下载/自检失败过的 tag，同 tag 不再重试。
	//
	// why：自检失败通常是这个 release 的资产本身有问题（架构拿错、构建坏了），
	// 重试只会每轮白下一次 20MB，且永远不会成功。新 tag 出来仍会被发现，
	// 因为查版本这一步照常每轮进行。
	failedTags map[string]bool
	mu         sync.Mutex

	warnUnknownOnce   sync.Once
	warnUnmanagedOnce sync.Once
}

// New 构造更新循环。
func New(o Options) *Updater {
	return &Updater{o: o, failedTags: map[string]bool{}}
}

// Reconcile 在 agentd 启动时调一次，收尾上一轮换版。
//
// 注意：
//   - pending 记的版本等于本进程版本，说明上一轮换版成功且新进程已被拉起，
//     清掉记录并留一条 Info——这条日志是「自更新走完了整条链」的唯一证据
//   - 版本对不上说明还在等窗口，保留
func (u *Updater) Reconcile() {
	p, err := LoadPending(u.o.DataDir)
	if err != nil {
		u.o.Log.Warn("读待命更新失败", "cause", err)
		return
	}
	if p == nil {
		return
	}
	if p.Version == u.o.CurrentVersion {
		u.o.Log.Info("更新完成", "version", p.Version, "downloaded_at", p.DownloadedAt)
		if err := ClearPending(u.o.DataDir); err != nil {
			u.o.Log.Warn("清理待命更新记录失败", "cause", err)
		}
		return
	}
	u.o.Log.Info("有待命更新，等空闲窗口", "pending", p.Version, "current", u.o.CurrentVersion)
}

// Run 按 Interval 循环跑 Tick，直到 ctx 取消。
func (u *Updater) Run(ctx context.Context) {
	u.o.Log.Info("自动更新循环启动", "interval", u.o.Interval)
	t := time.NewTicker(u.o.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			u.o.Log.Info("自动更新循环退出")
			return
		case <-t.C:
			u.Tick(ctx)
		}
	}
}

// Tick 跑一轮。
//
// 注意：
//   - **不返回错误**。任何失败都在内部处置并记日志——循环不能因为一次网络
//     抖动就停掉，而调用方（Run）对错误也没有任何有意义的处置
func (u *Updater) Tick(ctx context.Context) {
	if u.o.CurrentVersion == "" {
		// 非 release 构建比不出「新不新」。只 Warn 一次：6 小时一轮的循环在
		// 开发机上会跑很久，每轮刷同一条会把日志淹掉
		u.warnUnknownOnce.Do(func() {
			u.o.Log.Warn("本进程版本未知（非 release 构建），跳过自动更新；手动升级用 handoff upgrade --now")
		})
		return
	}

	p, err := LoadPending(u.o.DataDir)
	if err != nil {
		u.o.Log.Warn("读待命更新失败", "cause", err)
		return
	}

	// 已有待命就直接去判窗口，连版本都不用查——省一次限流额度
	if p == nil {
		p = u.download(ctx)
		if p == nil {
			return
		}
	}

	u.tryApply(p)
}

// download 查版本并把新版下好待命；无新版或失败时返回 nil。
func (u *Updater) download(ctx context.Context) *Pending {
	rel, err := u.o.Checker.Latest(ctx)
	if err != nil {
		// 不重试不退避：interval 本身就是退避（spec §4.7）
		u.o.Log.Warn("查最新版本失败，等下一轮", "cause", err)
		return nil
	}
	if rel.Tag == u.o.CurrentVersion {
		u.o.Log.Debug("已是最新", "version", rel.Tag)
		return nil
	}
	u.mu.Lock()
	failed := u.failedTags[rel.Tag]
	u.mu.Unlock()
	if failed {
		u.o.Log.Debug("该版本此前下载/自检失败，不再重试", "version", rel.Tag)
		return nil
	}

	target, err := u.o.Executable()
	if err != nil {
		u.o.Log.Error("取当前二进制路径失败，无法确定下载落点", "cause", err)
		return nil
	}
	// 临时文件必须与目标同目录：os.Rename 的原子性只在同一文件系统内成立
	dir := filepath.Dir(target)

	path, err := u.o.Fetcher.Fetch(ctx, rel, dir)
	if err != nil {
		u.mu.Lock()
		u.failedTags[rel.Tag] = true
		u.mu.Unlock()
		u.o.Log.Error("下载或校验新版本失败，同版本不再重试", "version", rel.Tag, "cause", err)
		return nil
	}

	p := &Pending{Version: rel.Tag, Path: path, DownloadedAt: time.Now().UTC()}
	if err := SavePending(u.o.DataDir, p); err != nil {
		// 记不下来不影响本轮换版，但重启后要重下一次，值得一条 Warn
		u.o.Log.Warn("持久化待命更新失败（重启后需重新下载）", "cause", err)
	}
	u.o.Log.Info("新版本已就绪，等空闲窗口", "version", rel.Tag, "path", path)
	return p
}

// tryApply 判两条闸，都过了就换版并触发关停。
func (u *Updater) tryApply(p *Pending) {
	// 闸一：非托管则拒绝。换完 exit(0) 之后没人拉起，机器上就此没有 handoff
	// 在跑，且没有任何信号告诉任何人——这是整个自动更新里最重要的一条防线
	if !IsManaged(u.o.Getenv) {
		u.warnUnmanagedOnce.Do(func() {
			u.o.Log.Warn("新版本已就绪，但当前进程非托管启动，自动重启会导致 agentd 停止服务；"+
				"请先 handoff service install，或手动 handoff upgrade --now", "version", p.Version)
		})
		return
	}

	// 闸二：空闲窗口（D12）。running + waiting_answer 为 0 才换
	busy, err := u.o.BusyCount()
	if err != nil {
		// 判不出来就不换（fail-closed）：换错了的代价是中断正在跑的任务
		u.o.Log.Warn("空闲判定失败，本轮不换版", "cause", err)
		return
	}
	if busy > 0 {
		u.o.Log.Info("新版本已就绪，等活跃任务结束", "version", p.Version, "active", busy)
		return
	}

	target, err := u.o.Executable()
	if err != nil {
		u.o.Log.Error("取当前二进制路径失败，无法换版", "cause", err)
		return
	}
	prev, err := u.o.Activate(p.Path, target)
	if err != nil {
		// 保留 pending，下轮重试——rename 失败常见于权限/挂载问题，
		// 这类问题可能被人修好
		u.o.Log.Error("替换二进制失败，保留待命下轮重试", "version", p.Version, "target", target, "cause", err)
		return
	}
	u.o.Log.Info("二进制已替换，即将优雅关停由进程管理器拉起新版",
		"version", p.Version, "target", target, "prev", prev)

	// 换完就把 pending 清掉：新进程起来后不该再看到一条已经生效的待命记录。
	// Reconcile 里那条「版本相同就清掉」是兜底（本次清理失败时仍能收尾）
	if err := ClearPending(u.o.DataDir); err != nil {
		u.o.Log.Warn("清理待命更新记录失败", "cause", err)
	}
	if !u.o.Shutdown(fmt.Sprintf("update:%s", p.Version)) {
		u.o.Log.Warn("触发关停时已在关停中", "version", p.Version)
	}
}
