// login.go —— 登录行与发现结果行的读写（B464 plan Q1/Q4/L5–L9）。
//
// 职责：登录行（kind=account_login）的读取与唯一写路径 mergeLogin（读 → 合并本写者负责的
// 字段 → CAS，冲突重读重试；发现器只在内容变化时写，人改代理（段 2）几乎不撞版本）；
// 发现结果行（kind=discovery_run，每机一行）的写与读。
//
// 边界：登录行只含位置、来源名与短指纹（plan L6），不含凭据值；本文件不判断「哪台是本机」，
// 机器名由调用方（所在机发现器）给出。
package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"sort"
	"time"

	"github.com/Xsxdot/handoff/internal/schedclient"
)

// ErrLoginNotFound 表示登录行不存在（errors.Is ErrNotFound 同时成立）。
var ErrLoginNotFound = fmt.Errorf("%w（登录）", ErrNotFound)

// Login 读一条登录（按 id）。不存在返回包 ErrLoginNotFound 的错误。
func (s *Service) Login(id string) (Login, error) {
	l, _, err := s.readLogin(id)
	if err != nil {
		return Login{}, err
	}
	if l == nil {
		return Login{}, fmt.Errorf("登录 %s: %w", id, ErrLoginNotFound)
	}
	return *l, nil
}

// Logins 列出全部登录（按 id 排序）。坏行跳过并告警（下一轮发现会重写它）。
func (s *Service) Logins() ([]Login, error) {
	rows, err := s.loginRows()
	if err != nil {
		return nil, err
	}
	out := make([]Login, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Login)
	}
	return out, nil
}

// loginRows 读全部登录行与版本（按 id 排序）。
func (s *Service) loginRows() ([]LoginView, error) {
	recs, err := s.repo.List(KindLogin)
	if err != nil {
		return nil, fmt.Errorf("列登录失败: %w", err)
	}
	out := make([]LoginView, 0, len(recs))
	for _, r := range recs {
		var l Login
		if err := json.Unmarshal(r.Body, &l); err != nil {
			logger().Warn("登录行解码失败，跳过（下一轮发现重写）", "id", r.ID, "version", r.Version, "cause", err)
			continue
		}
		l.ID = r.ID
		out = append(out, LoginView{Login: l, Version: r.Version})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Login.ID < out[j].Login.ID })
	return out, nil
}

// readLogin 读登录行与版本；不存在返回 nil 与版本 0。坏行按不存在处理（版本照返，下一次写
// 覆盖它），不让一条坏行永久卡死发现写回。
func (s *Service) readLogin(id string) (*Login, int, error) {
	raw, err := s.repo.Get(KindLogin, id)
	if err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			return nil, 0, nil
		}
		return nil, 0, fmt.Errorf("读登录 %s 失败: %w", id, err)
	}
	var l Login
	if err := json.Unmarshal(raw.Body, &l); err != nil {
		logger().Warn("登录行解码失败，按空行重建", "id", id, "version", raw.Version, "cause", err)
		return nil, raw.Version, nil
	}
	l.ID = id
	return &l, raw.Version, nil
}

// mergeLogin 是登录行唯一的写路径：读当前行 → mutate 合并本写者的字段（cur 为 nil = 行不存在）
// → 有变化才以读到的版本 CAS；冲突（对方刚写成功）则重读重试。mutate 返回 nil = 不写。
// 返回写入后的登录与是否真的写了。
func (s *Service) mergeLogin(id, writer string, mutate func(cur *Login) *Login) (Login, bool, error) {
	for attempt := 1; attempt <= maxCASAttempts; attempt++ {
		cur, version, err := s.readLogin(id)
		if err != nil {
			return Login{}, false, err
		}
		var snapshot *Login
		if cur != nil {
			c := *cur
			snapshot = &c
		}
		next := mutate(snapshot)
		if next == nil {
			if cur == nil {
				return Login{}, false, nil
			}
			return *cur, false, nil
		}
		next.ID = id
		body, err := json.Marshal(next)
		if err != nil {
			return Login{}, false, fmt.Errorf("编码登录 %s 失败: %w", id, err)
		}
		_, err = s.repo.Put(KindLogin, id, version, body, "accounts:"+writer)
		if err == nil {
			return *next, true, nil
		}
		if !errors.Is(err, schedclient.ErrCASConflict) {
			return Login{}, false, fmt.Errorf("写登录 %s 失败: %w", id, err)
		}
		logger().Debug("登录行 CAS 冲突，重读重试", "id", id, "writer", writer, "attempt", attempt)
		time.Sleep(time.Duration(rand.Int64N(int64(attempt) * int64(time.Millisecond))))
	}
	return Login{}, false, fmt.Errorf("登录 %s 连续 %d 次冲突: %w", id, maxCASAttempts, ErrRetryExhausted)
}

// writeDiscoveryRun 写本机发现结果行（每机一行，只有该机写；冲突即重读重试）。
func (s *Service) writeDiscoveryRun(run DiscoveryRun) error {
	body, err := json.Marshal(run)
	if err != nil {
		return fmt.Errorf("编码发现结果 %s 失败: %w", run.Machine, err)
	}
	for attempt := 1; attempt <= maxCASAttempts; attempt++ {
		version := 0
		if rec, err := s.repo.Get(KindDiscoveryRun, run.Machine); err == nil {
			version = rec.Version
		} else if !errors.Is(err, schedclient.ErrNotFound) {
			return fmt.Errorf("读发现结果 %s 失败: %w", run.Machine, err)
		}
		_, err := s.repo.Put(KindDiscoveryRun, run.Machine, version, body, "accounts:"+run.Machine)
		if err == nil {
			return nil
		}
		if !errors.Is(err, schedclient.ErrCASConflict) {
			return fmt.Errorf("写发现结果 %s 失败: %w", run.Machine, err)
		}
	}
	return fmt.Errorf("发现结果 %s 连续 %d 次冲突: %w", run.Machine, maxCASAttempts, ErrRetryExhausted)
}

// DiscoveryRuns 列出各机最近一轮发现结果（按机器名排序）。坏行跳过并告警。
func (s *Service) DiscoveryRuns() ([]DiscoveryRun, error) {
	recs, err := s.repo.List(KindDiscoveryRun)
	if err != nil {
		return nil, fmt.Errorf("列发现结果失败: %w", err)
	}
	out := make([]DiscoveryRun, 0, len(recs))
	for _, r := range recs {
		var run DiscoveryRun
		if err := json.Unmarshal(r.Body, &run); err != nil {
			logger().Warn("发现结果行解码失败，跳过", "machine", r.ID, "cause", err)
			continue
		}
		run.Machine = r.ID
		out = append(out, run)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Machine < out[j].Machine })
	return out, nil
}
