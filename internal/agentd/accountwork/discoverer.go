// discoverer.go —— 所在机账号登录发现器（B464 S1，plan Q1–Q4/Q21、§5 迁移触发）。
//
// 职责：启动时扫一次、之后按节拍扫：主 HOME 与 `~/.handoff/home/*` 一层（不递归、不跟软链、
// 不扫其他路径，spec「实现决定」），逐 HOME 交 quota 离线取材认出已登录身份，映射成账号域
// 值类型后经 Reconcile 写进共享账本（同身份归并、自动起名、扫不到 → 已失效）；每轮成功写回
// 之后调 AfterRound（组装点接存量载体迁移，plan §5）。
//
// 边界：
//   - 只读凭据（quota 只读打开，绝不刷新/改写/复制）；原始身份材料不出 quota，这里只见来源名
//     与短指纹；
//   - 同一时刻只跑一轮（单飞）；周期任务 ticker + ctx，Stop() 等循环退出；
//   - 「哪台是本机」只体现在 Machine（本机自名）上：发现器只写本机的登录。
package accountwork

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Xsxdot/handoff/internal/quota"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// DefaultDiscoverEvery 是发现节拍（plan §9 授权 30s–5min；定 60s：新登录在一分钟内出现，
// 每轮只写一行 discovery_run，登录行只在内容变化时写）。
const DefaultDiscoverEvery = 60 * time.Second

// isolatedHomeRel 是隔离 HOME 根相对主 HOME 的路径（与 scheduling.IsolatedHomeRoot 同一约定）。
const isolatedHomeRel = ".handoff/home"

// LoginReader 是发现器对额度适配子系统的使用方接口（实现：*quota.Registry）。
type LoginReader interface {
	Logins(home string) ([]quota.LoginFinding, []error)
}

// DiscoveryAccounts 是发现器对账号域的使用方接口（实现：*accounts.Service）。
type DiscoveryAccounts interface {
	Reconcile(d accounts.Discovery) (accounts.ReconcileResult, error)
}

// DiscovererConfig 是发现器的装配参数，全部由组装点（cmd/agentd.go）注入。
type DiscovererConfig struct {
	Accounts DiscoveryAccounts
	Quota    LoginReader
	Machine  string                 // 本机自名（登录行的 machine）
	MainHome func() (string, error) // 主 HOME 绝对路径（os.UserHomeDir）
	// AfterRound 在一轮发现成功写回后调用（存量载体迁移，plan §5）；nil = 不调。
	AfterRound func(res accounts.ReconcileResult)
	Every      time.Duration // 0 = DefaultDiscoverEvery
	Logger     *slog.Logger
}

// Discoverer 是所在机发现器。零值不可用，经 NewDiscoverer 构造。
type Discoverer struct {
	cfg DiscovererConfig
	log *slog.Logger

	round sync.Mutex // 单飞：同一时刻只跑一轮

	mu      sync.Mutex
	cancel  context.CancelFunc
	started bool
	stopped bool
	wg      sync.WaitGroup
}

// NewDiscoverer 构造发现器（不启动）。
func NewDiscoverer(cfg DiscovererConfig) *Discoverer {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}
	if cfg.Every <= 0 {
		cfg.Every = DefaultDiscoverEvery
	}
	return &Discoverer{cfg: cfg, log: logger.With("component", "account_discoverer")}
}

// Start 启动周期发现（启动即扫一次）。ctx 取消或 Stop 后退出。重复调用只生效一次。
func (d *Discoverer) Start(parent context.Context) {
	d.mu.Lock()
	if d.started || d.stopped {
		d.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(parent)
	d.started, d.cancel = true, cancel
	d.wg.Add(1)
	d.mu.Unlock()
	d.log.Info("账号发现器已启动", "machine", d.cfg.Machine, "every", d.cfg.Every)
	go func() {
		defer d.wg.Done()
		ticker := time.NewTicker(d.cfg.Every)
		defer ticker.Stop()
		for {
			if _, err := d.RunOnce(ctx); err != nil && ctx.Err() == nil {
				d.log.Warn("账号发现一轮失败，下一轮重试", "machine", d.cfg.Machine, "cause", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

// Stop 取消生命周期并等循环退出。未启动时无操作；可重复调用。
func (d *Discoverer) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	already := d.stopped
	d.stopped = true
	d.mu.Unlock()
	if cancel == nil || already {
		return
	}
	cancel()
	d.wg.Wait()
	d.log.Info("账号发现器已停止", "machine", d.cfg.Machine)
}

// RunOnce 同步跑一轮发现（周期与「立即发现」共用；单飞——已有一轮在跑时等它结束后再跑）。
// 返回本轮归并结果；扫描与写回失败返回错误（已写成功的行保留，下一轮补齐）。
func (d *Discoverer) RunOnce(ctx context.Context) (accounts.ReconcileResult, error) {
	d.round.Lock()
	defer d.round.Unlock()
	if err := ctx.Err(); err != nil {
		return accounts.ReconcileResult{}, err
	}
	started := time.Now()
	disc, err := d.scan()
	if err != nil {
		d.log.Error("账号发现：扫描失败", "machine", d.cfg.Machine, "cause", err)
		return accounts.ReconcileResult{}, err
	}
	res, err := d.cfg.Accounts.Reconcile(disc)
	if err != nil {
		d.log.Error("账号发现：写回账本失败", "machine", d.cfg.Machine, "homes", len(disc.Homes), "cause", err)
		return res, fmt.Errorf("发现结果写回账本失败: %w", err)
	}
	logins := 0
	for _, h := range disc.Homes {
		logins += len(h.Logins)
	}
	d.log.Info("账号发现一轮完成", "machine", d.cfg.Machine, "homes", len(disc.Homes), "logins", logins,
		"added", res.Added, "changed", res.Changed, "gone", res.Gone, "new_accounts", res.NewAccounts,
		"errors", len(disc.Errors), "elapsed", time.Since(started))
	if d.cfg.AfterRound != nil {
		d.cfg.AfterRound(res)
	}
	return res, nil
}

// scan 列出本轮要看的 HOME 并逐个离线取材。主 HOME 取不到是硬错误（整轮作废）；隔离 HOME 根
// 不存在是正常（没建过）；列不出隔离 HOME 根记进 Errors（未扫到的 HOME 不标已失效）。
func (d *Discoverer) scan() (accounts.Discovery, error) {
	if d.cfg.MainHome == nil {
		return accounts.Discovery{}, errors.New("发现器缺主 HOME 读取函数")
	}
	mainAbs, err := d.cfg.MainHome()
	if err != nil || mainAbs == "" || !filepath.IsAbs(mainAbs) {
		return accounts.Discovery{}, fmt.Errorf("取主 HOME 失败（%q）: %v", mainAbs, err)
	}
	mainAbs = filepath.Clean(mainAbs)
	disc := accounts.Discovery{Machine: d.cfg.Machine, At: time.Now()}
	disc.Homes = append(disc.Homes, d.scanHome(accounts.MainHome, mainAbs))
	root := filepath.Join(mainAbs, isolatedHomeRel)
	entries, err := os.ReadDir(root)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		d.log.Warn("账号发现：列不出隔离 HOME 根，本轮不对其下登录下已失效结论", "root", root, "cause", err)
		disc.Errors = append(disc.Errors, fmt.Sprintf("列不出 %s：%v", root, err))
	default:
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
		for _, e := range entries {
			abs := filepath.Join(root, e.Name())
			info, lerr := os.Lstat(abs)
			if lerr != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				// 只扫一层真实目录：软链（可能指向范围外）与普通文件一律不看。
				d.log.Debug("账号发现：跳过非目录或软链", "path", abs)
				continue
			}
			disc.Homes = append(disc.Homes, d.scanHome("~/"+isolatedHomeRel+"/"+e.Name(), abs))
		}
	}
	return disc, nil
}

// scanHome 对一个 HOME 离线取材并映射成账号域值类型（只有来源名、短指纹、label、到期）。
func (d *Discoverer) scanHome(home, abs string) accounts.ScannedHome {
	out := accounts.ScannedHome{Home: home, HomeAbs: abs}
	findings, problems := d.cfg.Quota.Logins(abs)
	for _, f := range findings {
		out.Logins = append(out.Logins, accounts.FoundLogin{Kind: f.Kind, Provider: f.Provider, Source: f.Source,
			Fingerprint: f.Fingerprint, Label: f.Label, ExpiresAt: f.ExpiresAt})
	}
	for _, p := range problems {
		out.Errors = append(out.Errors, p.Error())
	}
	if len(problems) > 0 {
		d.log.Warn("账号发现：HOME 有看不清的凭据，本轮不对其下登录下已失效结论", "home", home, "problems", len(problems))
	}
	return out
}
