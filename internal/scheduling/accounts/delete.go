// delete.go —— 账号删除、载体引用查询接口位与代理打码（B453 段 2；B464 引用改经登录）。
//
// 职责：删除前查引用（名下任一登录被载体引用即拒删并列出），无引用则 CAS 删定义行并连带
// 删读数行；代理 userinfo 的密码段打码（读面用）与「编辑回填打码值 = 保留原值」的判据。
// 边界：载体对登录的引用归载体记录（编制域），本域不读载体行——引用查询由组装点经
// SetReferrers 注入（未注入 = 无引用）。登录行的连带删除归 B464 段 2（plan Q17）：段 1 留下
// 的登录行在下次发现时让同一 id 的账号以自动名重新出现。
package accounts

import (
	"errors"
	"fmt"
	"strings"

	"github.com/Xsxdot/handoff/internal/proxycfg"
	"github.com/Xsxdot/handoff/internal/schedclient"
)

// ErrReferenced 表示账号仍被载体引用，拒删。具体载体见 *ReferencedError。
var ErrReferenced = errors.New("accounts: 账号仍被载体引用")

// ReferencedError 携带引用该账号名下登录的载体名（排序），gateway 据此回 409 与 carriers 列表。
type ReferencedError struct {
	Account  string
	Carriers []string
}

func (e *ReferencedError) Error() string {
	return fmt.Sprintf("账号 %s 仍被载体 %s 引用，先在载体里改选其他账号登录", e.Account, strings.Join(e.Carriers, "、"))
}

// Is 让 errors.Is(err, ErrReferenced) 成立。
func (e *ReferencedError) Is(target error) bool { return target == ErrReferenced }

// SetReferrers 注入「登录 id → 引用它的载体名」的查询（组装点调用一次）。nil = 无引用。
func (s *Service) SetReferrers(fn func() (map[string][]string, error)) { s.referrers = fn }

// referrerMap 返回登录 id → 引用载体名；未注入返回空表。
func (s *Service) referrerMap() (map[string][]string, error) {
	if s.referrers == nil {
		return map[string][]string{}, nil
	}
	refs, err := s.referrers()
	if err != nil {
		return nil, fmt.Errorf("查询登录的载体引用失败: %w", err)
	}
	if refs == nil {
		refs = map[string][]string{}
	}
	return refs, nil
}

// Delete 以 CAS 删除账号定义（ref = 账号 id 或名字，expect = 定义行当前版本），并连带删读数行。
//
// 返回：名下登录被引用 → *ReferencedError（errors.Is ErrReferenced）；不存在 → ErrNotFound；
// 版本不符 → schedclient.ErrCASConflict。读数行删除失败只告警（孤儿读数行不展示、
// 探测器跳过），不让删除失败。
func (s *Service) Delete(ref string, expect int) error {
	logger().Info("删除账号请求", "ref", ref, "expect", expect)
	v, err := s.Get(ref)
	if err != nil {
		return err
	}
	id, name := v.Account.ID, v.Account.Name
	if len(v.Carriers) > 0 {
		logger().Warn("删除账号被拒：名下登录仍被载体引用", "id", id, "name", name, "carriers", v.Carriers)
		return &ReferencedError{Account: name, Carriers: v.Carriers}
	}
	if err := s.repo.Delete(KindAccount, id, expect, "accounts"); err != nil {
		logger().Warn("删除账号定义失败", "id", id, "name", name, "expect", expect, "cause", err)
		return fmt.Errorf("删除账号 %s 失败: %w", name, err)
	}
	if rec, err := s.repo.Get(KindReading, id); err == nil {
		if derr := s.repo.Delete(KindReading, id, rec.Version, "accounts"); derr != nil {
			logger().Warn("删除账号读数行失败（孤儿行不展示，可忽略）", "id", id, "cause", derr)
		}
	} else if !errors.Is(err, schedclient.ErrNotFound) {
		logger().Warn("读取账号读数行失败（孤儿行不展示，可忽略）", "id", id, "cause", err)
	}
	logger().Info("删除账号成功", "id", id, "name", name, "expect", expect)
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
