// Package accounts 是编制域的账号领域（B453）：账号定义的登记、探测读数与撞额度
// 标记的合并写、账号状态推导。
//
// 职责：
//   - 定义行（kind=account）：人写，整行 CAS，与载体同一 version 语义；
//   - 读数行（kind=account_reading）：机写，「读 → 合并本写者负责的字段 → CAS，冲突重读
//     重试」，探测器与标记者互不丢写；
//   - 状态推导 Derive：准入（段 3）、API、CLI 共用这一份。
//
// 边界：持久化只经 schedclient.Registry 端口（组装点绑定账本）；不调外部接口、不读
// 凭据（那是 internal/quota 与 agentd 探测器）；不知道「哪台是本机」（探测执行权归
// agentd）。
package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/schedclient"
)

var (
	// ErrNotFound 表示账号定义行不存在。
	ErrNotFound = errors.New("accounts: 账号不存在")
	// ErrInvalid 表示登记输入未过校验；gateway 据此回 400。
	ErrInvalid = errors.New("accounts: 账号登记校验未过")
	// ErrRetryExhausted 表示读数行合并写连续 CAS 冲突超出预算。
	ErrRetryExhausted = errors.New("accounts: 读数行连续冲突")
)

// maxCASAttempts 与编制域计数的重试预算同值（plan Q1：上限沿用 maxCASAttempts=8）。
// 读数行只有探测器（每账号单飞）与标记者两类写者，冲突即对方刚写成功，8 轮足够收敛。
const maxCASAttempts = 8

func logger() *slog.Logger { return slog.Default().With("mod", "scheduling", "domain", "accounts") }

// Service 是账号领域的规则入口。无内存权威状态，全部经 Registry 持久。
type Service struct {
	repo schedclient.Registry
}

// New 用组装点绑定的注册表端口构造服务。
func New(repo schedclient.Registry) *Service { return &Service{repo: repo} }

// Put 以 CAS 写账号定义（expect=0 新建），返回新版本。
//
// 校验失败返回包 ErrInvalid 的错误；版本冲突返回包 schedclient.ErrCASConflict 的错误。
// 只写定义行，不碰读数行——人编辑不抹读数，探测写读数也不顶定义行版本（Q1）。
func (s *Service) Put(a Account, expect int) (int, error) {
	if err := validate(a); err != nil {
		logger().Warn("账号登记校验失败", "name", a.Name, "kind", a.Kind, "machine", a.Machine,
			"expect", expect, "cause", err)
		return 0, err
	}
	body, err := json.Marshal(a)
	if err != nil {
		logger().Error("编码账号定义失败", "name", a.Name, "cause", err)
		return 0, err
	}
	version, err := s.repo.Put(KindAccount, a.Name, expect, body, "accounts")
	if err != nil {
		logger().Warn("写入账号定义失败", "name", a.Name, "expect", expect, "cause", err)
		return 0, fmt.Errorf("账号 %s 写入失败: %w", a.Name, err)
	}
	logger().Info("写入账号定义成功", "name", a.Name, "kind", a.Kind, "machine", a.Machine,
		"expect", expect, "version", version, "interval_minutes", a.IntervalMinutes,
		"proxy_set", a.Proxy != "")
	return version, nil
}

// validate 是段 1 的最小校验：必填、kind 词表、间隔可选值、两条阈值次序。
// 同机同 HOME 同条目查重与 HOME 形态校验在段 2（plan V1）。
func validate(a Account) error {
	switch {
	case strings.TrimSpace(a.Name) == "" || strings.TrimSpace(a.Name) != a.Name || strings.Contains(a.Name, "/"):
		return fmt.Errorf("%w: 账号名必填，且不能含首尾空白或 /", ErrInvalid)
	case !slices.Contains([]string{KindOpencodeGo, KindCodex, KindGrok, KindAgy}, a.Kind):
		return fmt.Errorf("%w: 账号 %s 的类型 %q 不在 opencode-go/codex/grok/agy 之内", ErrInvalid, a.Name, a.Kind)
	case strings.TrimSpace(a.Machine) == "":
		return fmt.Errorf("%w: 账号 %s 必须指定所在机器", ErrInvalid, a.Name)
	case strings.TrimSpace(a.HomeDir) == "":
		return fmt.Errorf("%w: 账号 %s 必须指定凭据 HOME", ErrInvalid, a.Name)
	case !slices.Contains(AllowedIntervals, a.IntervalMinutes):
		return fmt.Errorf("%w: 账号 %s 的探测间隔 %d 分钟不在 %v 之内", ErrInvalid, a.Name, a.IntervalMinutes, AllowedIntervals)
	case a.WarnPercent <= 0 || a.WarnPercent >= a.LowPercent || a.LowPercent > 100:
		return fmt.Errorf("%w: 账号 %s 的阈值须满足 0 < 预警线(%d) < 低额度线(%d) ≤ 100", ErrInvalid, a.Name, a.WarnPercent, a.LowPercent)
	}
	return nil
}

// Get 读一个账号的完整读面（定义 + 读数行 + 推导状态）。
func (s *Service) Get(name string) (View, error) {
	def, err := s.repo.Get(KindAccount, name)
	if err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			return View{}, fmt.Errorf("账号 %s: %w", name, ErrNotFound)
		}
		return View{}, fmt.Errorf("读账号 %s 定义失败: %w", name, err)
	}
	var a Account
	if err := json.Unmarshal(def.Body, &a); err != nil {
		return View{}, fmt.Errorf("account/%s 解码失败: %w", name, err)
	}
	rec, _, err := s.readRecord(name)
	if err != nil {
		return View{}, err
	}
	return newView(a, def.Version, rec, time.Now()), nil
}

// List 列出全部账号（按名称排序）。没有定义行的读数行（孤儿）不出现。
func (s *Service) List() ([]View, error) {
	defs, err := s.repo.List(KindAccount)
	if err != nil {
		return nil, fmt.Errorf("列账号定义失败: %w", err)
	}
	readings, err := s.repo.List(KindReading)
	if err != nil {
		return nil, fmt.Errorf("列账号读数失败: %w", err)
	}
	byID := make(map[string]ReadingRecord, len(readings))
	for _, r := range readings {
		var rec ReadingRecord
		if err := json.Unmarshal(r.Body, &rec); err != nil {
			// 读数行是可重建的机写缓存：坏行按「无读数」展示，下次探测覆盖，不拖垮整张列表。
			logger().Warn("账号读数行解码失败，按无读数展示", "name", r.ID, "version", r.Version, "cause", err)
			continue
		}
		byID[r.ID] = rec
	}
	now := time.Now()
	out := make([]View, 0, len(defs))
	for _, d := range defs {
		var a Account
		if err := json.Unmarshal(d.Body, &a); err != nil {
			return nil, fmt.Errorf("account/%s 解码失败: %w", d.ID, err)
		}
		out = append(out, newView(a, d.Version, byID[d.ID], now))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Account.Name < out[j].Account.Name })
	return out, nil
}

func newView(a Account, version int, rec ReadingRecord, now time.Time) View {
	return View{Account: a, Version: version, State: Derive(rec, now), Stale: IsStale(rec, now), Record: rec}
}

// RecordProbe 把一次探测结果合并进读数行（所在机探测器调用）。
//
// 合并规则：成功 → 替换读数、清失败与连续失败数；撞额度标记只在本次探测**开始**
// 晚于标记时清除（防「探测发出 → 任务撞额度标记 → 旧探测返回抹掉新标记」）。
// 失败 → 记失败与连续失败数，保留上次成功读数与标记（spec S2/S3）。
// 账号定义不存在时返回 ErrNotFound，不写孤儿读数行。
func (s *Service) RecordProbe(name string, o ProbeOutcome) error {
	if (o.Reading == nil) == (o.Failure == nil) {
		return fmt.Errorf("账号 %s 的探测结果必须恰有读数或失败之一", name)
	}
	err := s.mergeReading(name, o.Writer, func(r *ReadingRecord) {
		started, finished := o.StartedAt.UTC(), o.FinishedAt.UTC()
		r.LastAttemptStartedAt, r.LastAttemptAt = &started, &finished
		if o.Failure != nil {
			p := *o.Failure
			r.LastError = &p
			r.FailureStreak++
			return
		}
		reading := *o.Reading
		r.Reading = &reading
		r.LastError, r.FailureStreak = nil, 0
		if r.QuotaMark != nil && started.After(r.QuotaMark.At) {
			r.QuotaMark = nil
		}
	})
	if err != nil {
		logger().Error("写入账号探测结果失败", "name", name, "writer", o.Writer,
			"success", o.Reading != nil, "cause", err)
		return err
	}
	logger().Info("写入账号探测结果", "name", name, "writer", o.Writer, "success", o.Reading != nil)
	return nil
}

// MarkQuota 给账号打撞额度标记（任务/检测撞额度时同步调用；段 3/4 接入调用方）。
// 已有更晚的标记时保留更晚者。账号定义不存在返回 ErrNotFound。
func (s *Service) MarkQuota(name string, m QuotaMark) error {
	m.At = m.At.UTC()
	err := s.mergeReading(name, "mark:"+m.Machine, func(r *ReadingRecord) {
		if r.QuotaMark == nil || !m.At.Before(r.QuotaMark.At) {
			mark := m
			r.QuotaMark = &mark
		}
	})
	if err != nil {
		logger().Error("写入撞额度标记失败", "name", name, "task", m.Task, "carrier", m.Carrier, "cause", err)
		return err
	}
	logger().Info("写入撞额度标记", "name", name, "task", m.Task, "carrier", m.Carrier,
		"machine", m.Machine, "category", m.Category)
	return nil
}

// mergeReading 是读数行唯一的写路径：读当前行 → mutate 合并本写者的字段 → 以读到的
// 版本 CAS；冲突（对方刚写成功）则重读重试。行不存在按版本 0 新建。
func (s *Service) mergeReading(name, writer string, mutate func(*ReadingRecord)) error {
	if _, err := s.repo.Get(KindAccount, name); err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			return fmt.Errorf("账号 %s: %w", name, ErrNotFound)
		}
		return fmt.Errorf("读账号 %s 定义失败: %w", name, err)
	}
	for attempt := 1; attempt <= maxCASAttempts; attempt++ {
		rec, version, err := s.readRecord(name)
		if err != nil {
			return err
		}
		mutate(&rec)
		rec.Writer, rec.WrittenAt = writer, time.Now().UTC()
		body, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("编码账号 %s 读数行失败: %w", name, err)
		}
		_, err = s.repo.Put(KindReading, name, version, body, "accounts:"+writer)
		if err == nil {
			return nil
		}
		if !errors.Is(err, schedclient.ErrCASConflict) {
			return fmt.Errorf("写账号 %s 读数行失败: %w", name, err)
		}
		logger().Debug("账号读数行 CAS 冲突，重读重试", "name", name, "writer", writer, "attempt", attempt)
		// 抖动退让：两类写者若节拍同相，紧贴着重试会让同一方连续落败（饿死）；随机
		// 错开下一次读-写窗口即可收敛。生产写频是分钟级，这段等待几乎不会发生。
		time.Sleep(time.Duration(rand.Int64N(int64(attempt) * int64(time.Millisecond))))
	}
	return fmt.Errorf("账号 %s 读数行连续 %d 次冲突: %w", name, maxCASAttempts, ErrRetryExhausted)
}

// readRecord 读读数行与其版本；不存在返回零值与版本 0。坏行按空行处理（可重建缓存，
// 下一次写覆盖它），不让一条坏行永久卡死探测写回。
func (s *Service) readRecord(name string) (ReadingRecord, int, error) {
	raw, err := s.repo.Get(KindReading, name)
	if err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			return ReadingRecord{}, 0, nil
		}
		return ReadingRecord{}, 0, fmt.Errorf("读账号 %s 读数行失败: %w", name, err)
	}
	var rec ReadingRecord
	if err := json.Unmarshal(raw.Body, &rec); err != nil {
		logger().Warn("账号读数行解码失败，按空行重建", "name", name, "version", raw.Version, "cause", err)
		return ReadingRecord{}, raw.Version, nil
	}
	return rec, raw.Version, nil
}

// Derive 按读数行推导账号状态（plan Q4 优先级；段 1 不含低额度档）：
//  1. 有撞额度标记 → 无额度；
//  2. 最近一次尝试失败（成功即清 LastError，故非空即「晚于最近成功」）→ 探测失败；
//  3. 从无成功读数 → 未探测；
//  4. 读数明示不可用，或任一未过重置时刻的窗口已用 ≥ 100 → 无额度；否则正常。
//
// 已过重置时刻的窗口按 0% 计：重置已发生，只是读数还没刷新（Q4 补条）。
func Derive(rec ReadingRecord, now time.Time) State {
	switch {
	case rec.QuotaMark != nil:
		return StateOut
	case rec.LastError != nil:
		return StateFail
	case rec.Reading == nil:
		return StateNone
	case rec.Reading.Blocked != nil:
		return StateOut
	}
	for _, w := range rec.Reading.Windows {
		if w.ResetsAt != nil && !w.ResetsAt.After(now) {
			continue
		}
		if w.UsedPercent >= 100 {
			return StateOut
		}
	}
	return StateOK
}

// IsStale 报告最近成功读数是否已过期（> 30 分钟）。只是展示标记，不参与状态与准入。
func IsStale(rec ReadingRecord, now time.Time) bool {
	return rec.Reading != nil && now.Sub(rec.Reading.ProbedAt) > StaleAfter
}
