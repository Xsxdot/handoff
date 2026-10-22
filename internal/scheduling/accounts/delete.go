// delete.go —— 账号删除、载体引用查询接口位与代理打码（B453 段 2，spec S1/F3）。
//
// 职责：删除前查引用（被载体引用拒删并列出），无引用则 CAS 删定义行并连带删读数行；
// 代理 userinfo 的密码段打码（读面用）与「编辑回填打码值 = 保留原值」的判据。
// 边界：载体对账号的引用归载体记录（编制域），本域不读载体行——引用查询由组装点经
// SetReferrers 注入（段 3 接上编制域；未注入 = 无引用）。
package accounts

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/schedclient"
)

// ErrReferenced 表示账号仍被载体引用，拒删。具体载体见 *ReferencedError。
var ErrReferenced = errors.New("accounts: 账号仍被载体引用")

// ReferencedError 携带引用该账号的载体名（排序），gateway 据此回 409 与 carriers 列表。
type ReferencedError struct {
	Account  string
	Carriers []string
}

func (e *ReferencedError) Error() string {
	return fmt.Sprintf("账号 %s 仍被载体 %s 引用，先在载体里改选其他账号或不选", e.Account, strings.Join(e.Carriers, "、"))
}

// Is 让 errors.Is(err, ErrReferenced) 成立。
func (e *ReferencedError) Is(target error) bool { return target == ErrReferenced }

// SetReferrers 注入「哪些载体引用了这个账号」的查询（组装点调用一次）。nil = 无引用。
func (s *Service) SetReferrers(fn func(account string) ([]string, error)) { s.referrers = fn }

// carriersOf 返回引用该账号的载体名（排序，非 nil）。
func (s *Service) carriersOf(name string) ([]string, error) {
	if s.referrers == nil {
		return []string{}, nil
	}
	refs, err := s.referrers(name)
	if err != nil {
		return nil, fmt.Errorf("查询账号 %s 的载体引用失败: %w", name, err)
	}
	out := append([]string{}, refs...)
	sort.Strings(out)
	return out, nil
}

// Delete 以 CAS 删除账号定义（expect = 定义行当前版本），并连带删读数行。
//
// 返回：被引用 → *ReferencedError（errors.Is ErrReferenced）；不存在 → ErrNotFound；
// 版本不符 → schedclient.ErrCASConflict。读数行删除失败只告警（孤儿读数行不展示、
// 探测器跳过），不让删除失败。
func (s *Service) Delete(name string, expect int) error {
	logger().Info("删除账号请求", "name", name, "expect", expect)
	if _, err := s.repo.Get(KindAccount, name); err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			return fmt.Errorf("账号 %s: %w", name, ErrNotFound)
		}
		return fmt.Errorf("读账号 %s 定义失败: %w", name, err)
	}
	refs, err := s.carriersOf(name)
	if err != nil {
		logger().Error("删除账号：查引用失败", "name", name, "cause", err)
		return err
	}
	if len(refs) > 0 {
		logger().Warn("删除账号被拒：仍被载体引用", "name", name, "carriers", refs)
		return &ReferencedError{Account: name, Carriers: refs}
	}
	if err := s.repo.Delete(KindAccount, name, expect, "accounts"); err != nil {
		logger().Warn("删除账号定义失败", "name", name, "expect", expect, "cause", err)
		return fmt.Errorf("删除账号 %s 失败: %w", name, err)
	}
	if rec, err := s.repo.Get(KindReading, name); err == nil {
		if derr := s.repo.Delete(KindReading, name, rec.Version, "accounts"); derr != nil {
			logger().Warn("删除账号读数行失败（孤儿行不展示，可忽略）", "name", name, "cause", derr)
		}
	} else if !errors.Is(err, schedclient.ErrNotFound) {
		logger().Warn("读取账号读数行失败（孤儿行不展示，可忽略）", "name", name, "cause", err)
	}
	logger().Info("删除账号成功", "name", name, "expect", expect)
	return nil
}

// MaskProxy 返回读面用的代理串：密码段打码（`user:***@host`）。口径唯一定义在
// proxycfg.Mask，账号 API 回显、探测失败文案与日志共用。
func MaskProxy(proxy string) string { return proxycfg.Mask(proxy) }

// isMaskedEcho 报告 incoming 是否正是 stored 的打码形式（且 stored 确有被打码的密码）——
// 控制台编辑回填的是读面里的打码值，此时保留原值，不把 *** 写进账本。
func isMaskedEcho(incoming, stored string) bool {
	return stored != "" && incoming != stored && incoming == MaskProxy(stored)
}
