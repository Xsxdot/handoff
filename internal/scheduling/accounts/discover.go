// discover.go —— 一台机器一轮发现结果的归并（B464 S1，plan Q1–Q4、V1/V5）。
//
// 职责：Reconcile 把所在机发现器扫到的「HOME → 已登录身份」落成账本事实——
//   - 同 kind（+ provider）同身份归到同一账号：账号 id 确定性导出（AccountID），不存在则以
//     expect=0 新建并自动起名；两机并发新建同一 id 时一方 CAS 落败，重读即归并；
//   - 每个（机器, HOME, kind, provider, 来源）槽位一条登录（LoginID），只在内容变化时写；
//     同一槽位换了身份 = 登录改挂账号（Info）；
//   - 本机上次在、本次扫不到的登录标「已失效」（不删行）；所在 HOME 本轮扫描出错的不标；
//   - 写 discovery_run/<机器>（「上次发现」时刻）。
//
// 边界：入参只有位置、来源名、短指纹与 label——原始身份材料与凭据值不进本包。扫描、
// 读凭据、判断本机都在 agentd 的 accountwork。
package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/schedclient"
)

// FoundLogin 是发现器在一个 HOME 里认出的一份登录（quota 离线取材的映射结果）。
type FoundLogin struct {
	Kind        string
	Provider    string // 仅 apikey
	Source      string // 凭据来源名（如 auth.json、opencode.db）
	Fingerprint string // sha256:<hex12>，身份短指纹
	Label       string // 可读身份（邮箱）；取不到为空
	ExpiresAt   *time.Time
}

// ScannedHome 是一个被扫描的 HOME：机器无关写法、所在机绝对路径、认出的登录与扫描错误。
// Errors 非空 = 本轮对该 HOME 的判断不完整，其下扫不到的登录不标已失效。
type ScannedHome struct {
	Home    string // `~` 或 `~/.handoff/home/<dir>`
	HomeAbs string
	Logins  []FoundLogin
	Errors  []string
}

// Discovery 是一台机器一轮发现的输入。Errors 是不归属单个 HOME 的错误（如列不出
// ~/.handoff/home）：非空时，不在 Homes 里的 HOME 的登录不标已失效。
type Discovery struct {
	Machine string
	At      time.Time
	Homes   []ScannedHome
	Errors  []string
}

// ReconcileResult 是一轮归并的结果。Logins 是归并后本机的全部登录（含已失效，按 id 排序），
// 供存量载体迁移（plan §5）使用。
type ReconcileResult struct {
	Logins      []Login
	Added       int
	Changed     int
	Gone        int
	NewAccounts int
}

// Reconcile 把一轮发现结果写进账本（见文件头）。返回本机登录与计数；任一行写失败返回错误，
// 已写成功的行保留（幂等全量重扫，下一轮补齐）。
func (s *Service) Reconcile(d Discovery) (ReconcileResult, error) {
	machine := strings.TrimSpace(d.Machine)
	if machine == "" {
		return ReconcileResult{}, fmt.Errorf("%w: 发现归并缺机器名", ErrInvalid)
	}
	at := d.At.UTC()
	if d.At.IsZero() {
		at = time.Now().UTC()
	}
	defs, _, err := s.definitions()
	if err != nil {
		return ReconcileResult{}, err
	}
	taken := make(map[string]bool, len(defs))
	for _, a := range defs {
		taken[a.Name] = true
	}
	var res ReconcileResult
	seen := map[string]bool{}
	scanned := map[string]bool{} // 本轮完整扫过（无错误）的 HOME
	runErrors := append([]string{}, d.Errors...)
	found := 0
	for _, h := range d.Homes {
		if len(h.Errors) == 0 {
			scanned[h.Home] = true
		}
		for _, e := range h.Errors {
			runErrors = append(runErrors, h.Home+": "+e)
		}
		for _, f := range h.Logins {
			accountID := AccountID(f.Kind, f.Provider, f.Fingerprint)
			if accountID == "" {
				logger().Warn("发现结果的身份指纹形状不对，跳过", "machine", machine, "home", h.Home,
					"kind", f.Kind, "source", f.Source)
				continue
			}
			found++
			created, err := s.ensureAccount(accountID, f, machine, at, defs, taken)
			if err != nil {
				return res, err
			}
			if created {
				res.NewAccounts++
			}
			id := LoginID(machine, h.Home, f.Kind, f.Provider, f.Source)
			seen[id] = true
			added, changed, err := s.upsertLogin(id, accountID, machine, h, f, at)
			if err != nil {
				return res, err
			}
			if added {
				res.Added++
			} else if changed {
				res.Changed++
			}
		}
	}
	complete := len(d.Errors) == 0
	rows, err := s.loginRows()
	if err != nil {
		return res, err
	}
	for _, lv := range rows {
		l := lv.Login
		if l.Machine != machine || seen[l.ID] || l.State == LoginGone {
			continue
		}
		homeInScan := false
		for _, h := range d.Homes {
			if h.Home == l.Home {
				homeInScan = true
				break
			}
		}
		if (homeInScan && !scanned[l.Home]) || (!homeInScan && !complete) {
			continue // 这个 HOME 本轮没看清，不下「已失效」结论
		}
		if _, wrote, err := s.mergeLogin(l.ID, machine, func(cur *Login) *Login {
			if cur == nil || cur.State == LoginGone {
				return nil
			}
			cur.State, cur.ChangedAt = LoginGone, at
			return cur
		}); err != nil {
			return res, err
		} else if wrote {
			res.Gone++
			logger().Warn("登录已失效：发现扫不到凭据", "login", l.ID, "machine", machine, "home", l.Home,
				"kind", l.Kind, "source", l.Source, "account", l.Account)
		}
	}
	if rows, err = s.loginRows(); err != nil {
		return res, err
	}
	for _, lv := range rows {
		if lv.Login.Machine == machine {
			res.Logins = append(res.Logins, lv.Login)
		}
	}
	if err := s.writeDiscoveryRun(DiscoveryRun{Machine: machine, At: at, Homes: len(d.Homes),
		Logins: found, Errors: runErrors}); err != nil {
		return res, err
	}
	logger().Info("发现归并完成", "machine", machine, "homes", len(d.Homes), "logins", found,
		"added", res.Added, "changed", res.Changed, "gone", res.Gone, "new_accounts", res.NewAccounts,
		"errors", len(runErrors))
	return res, nil
}

// ensureAccount 保证身份对应的账号行存在：已有 → 不写；没有 → 自动起名、expect=0 新建。
// 并发新建同一 id（两机同时发现同一身份）时一方 CAS 落败 = 对方已建，按已存在处理（归并）。
// 不同身份撞同一自动名的并发窗口接受（plan Q2：极低频，改名可修）。
func (s *Service) ensureAccount(id string, f FoundLogin, machine string, at time.Time,
	defs map[string]Account, taken map[string]bool) (bool, error) {
	if _, ok := defs[id]; ok {
		return false, nil
	}
	a := Account{ID: id, Kind: f.Kind, Provider: f.Provider, Label: f.Label, Fingerprint: f.Fingerprint,
		Name: autoName(f.Kind, f.Provider, f.Label, f.Fingerprint, taken), CreatedBy: machine, CreatedAt: at,
		IntervalMinutes: DefaultIntervalMinutes, WarnPercent: DefaultWarnPercent, LowPercent: DefaultLowPercent}
	body, err := json.Marshal(a)
	if err != nil {
		return false, fmt.Errorf("编码账号 %s 失败: %w", id, err)
	}
	if _, err := s.repo.Put(KindAccount, id, 0, body, "accounts:"+machine); err != nil {
		if !errors.Is(err, schedclient.ErrCASConflict) {
			logger().Error("发现新建账号失败", "id", id, "machine", machine, "cause", err)
			return false, fmt.Errorf("新建账号 %s 失败: %w", id, err)
		}
		existing, _, gerr := s.definition(id)
		if gerr != nil {
			return false, gerr
		}
		logger().Info("发现新建账号时他机已建，归并", "id", id, "machine", machine, "name", existing.Name)
		defs[id], taken[existing.Name] = existing, true
		return false, nil
	}
	defs[id], taken[a.Name] = a, true
	logger().Info("发现新账号", "id", id, "name", a.Name, "kind", a.Kind, "machine", machine,
		"label_set", a.Label != "")
	return true, nil
}

// upsertLogin 合并写发现器负责的登录字段；内容不变不写。返回是否新建、是否变化。
func (s *Service) upsertLogin(id, accountID, machine string, h ScannedHome, f FoundLogin, at time.Time) (bool, bool, error) {
	added := false
	var prevAccount string
	_, wrote, err := s.mergeLogin(id, machine, func(cur *Login) *Login {
		want := Login{Account: accountID, Machine: machine, Home: h.Home, HomeAbs: h.HomeAbs, Kind: f.Kind,
			Provider: f.Provider, Source: f.Source, Fingerprint: f.Fingerprint, State: LoginOK, ExpiresAt: utcPtr(f.ExpiresAt)}
		if cur == nil {
			added = true
			want.ChangedAt = at
			return &want
		}
		added = false
		prevAccount = cur.Account
		if sameDiscovered(*cur, want) {
			return nil
		}
		next := *cur // 保留人写字段（代理）与失效证据
		next.Account, next.Machine, next.Home, next.HomeAbs = want.Account, want.Machine, want.Home, want.HomeAbs
		next.Kind, next.Provider, next.Source, next.Fingerprint = want.Kind, want.Provider, want.Source, want.Fingerprint
		next.State, next.ExpiresAt, next.ChangedAt = LoginOK, want.ExpiresAt, at
		return &next
	})
	if err != nil {
		logger().Error("发现写登录失败", "login", id, "machine", machine, "home", h.Home, "cause", err)
		return false, false, err
	}
	switch {
	case wrote && added:
		logger().Info("发现新登录", "login", id, "machine", machine, "home", h.Home, "kind", f.Kind,
			"source", f.Source, "account", accountID)
	case wrote && prevAccount != accountID:
		logger().Info("登录改挂账号", "login", id, "machine", machine, "home", h.Home,
			"old_account", prevAccount, "new_account", accountID)
	case wrote:
		logger().Info("登录有变化", "login", id, "machine", machine, "home", h.Home, "account", accountID)
	}
	return wrote && added, wrote && !added, nil
}

// sameDiscovered 比较发现器负责的字段（不含变更时刻、人写字段与失效证据）。
func sameDiscovered(a, b Login) bool {
	return a.Account == b.Account && a.Machine == b.Machine && a.Home == b.Home && a.HomeAbs == b.HomeAbs &&
		a.Kind == b.Kind && a.Provider == b.Provider && a.Source == b.Source && a.Fingerprint == b.Fingerprint &&
		a.State == b.State && sameTime(a.ExpiresAt, b.ExpiresAt)
}

func sameTime(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Equal(*b)
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}
