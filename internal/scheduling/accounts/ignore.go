// ignore.go —— 账号忽略标记的读写（B467 L11–L14、Q1/Q8）。
//
// 职责：Ignore 写一条与账号同 id 的 account_ignore 行（行在 ⇔ 已忽略），Restore 删它。
// 规则归本域：被载体引用不能忽略（复用删除的引用查询）；两者幂等；账号定义行、登录行、
// 读数行一概不动。发现归并、Put、Delete 从不读写忽略行——所以「忽略被旧二进制改写定义行
// 抹掉」「忽略与发现竞态」这两类失败路径在结构上不存在。
// 边界：载体引用查询由组装点经 SetReferrers 注入；本域不读载体行。
package accounts

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/Xsxdot/handoff/internal/schedclient"
)

// ignoreBody 把忽略标记编码成 registry body；by 为空写 "local"（L12）。
func ignoreBody(m IgnoreMark) ([]byte, error) {
	m.At = m.At.UTC()
	if strings.TrimSpace(m.By) == "" {
		m.By = "local"
	}
	body, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("编码忽略标记失败: %w", err)
	}
	return body, nil
}

// decodeIgnore 解码忽略行。坏行按「已忽略、at 未知」处理并 Warn：忽略是人的决定，宁可多藏
// 一个账号，也不让坏行悄悄把它恢复（与读数行「坏行按空」的取舍相反）。
func decodeIgnore(rec schedclient.Record) IgnoreMark {
	var m IgnoreMark
	if err := json.Unmarshal(rec.Body, &m); err != nil {
		logger().Warn("忽略行解码失败，按已忽略、时间未知处理", "id", rec.ID, "version", rec.Version, "cause", err)
		return IgnoreMark{}
	}
	return m
}

// ignoreMark 读一个账号的忽略行；不存在返回 ok=false。坏行按已忽略返回。
func (s *Service) ignoreMark(id string) (IgnoreMark, bool, error) {
	rec, err := s.repo.Get(KindIgnore, id)
	if err != nil {
		if errors.Is(err, schedclient.ErrNotFound) {
			return IgnoreMark{}, false, nil
		}
		return IgnoreMark{}, false, fmt.Errorf("读账号 %s 的忽略行失败: %w", id, err)
	}
	return decodeIgnore(rec), true, nil
}

// ignoreMarks 读全部忽略行并按账号 id 索引（读面挂 Ignored 用）。坏行按已忽略。
func (s *Service) ignoreMarks() (map[string]IgnoreMark, error) {
	recs, err := s.repo.List(KindIgnore)
	if err != nil {
		return nil, fmt.Errorf("列账号忽略行失败: %w", err)
	}
	out := make(map[string]IgnoreMark, len(recs))
	for _, rec := range recs {
		out[rec.ID] = decodeIgnore(rec)
	}
	return out, nil
}

// Ignore 忽略一个账号（ref = 账号 id 或名字），返回写后读面。by 是写入时入口 agentd 自名，
// 空则写 "local"（L12）。
//
// 规则：账号不存在 → ErrNotFound；名下任一登录被载体引用 → *ReferencedError（文案说「不能
// 忽略」，不产生忽略行）；已忽略再调 → 成功且 at/by 不变（幂等，A16）。
func (s *Service) Ignore(ref, by string) (View, error) {
	id, err := s.resolve(ref)
	if err != nil {
		return View{}, err
	}
	v, err := s.Get(id)
	if err != nil {
		return View{}, err
	}
	if v.Ignored != nil {
		logger().Info("忽略账号：已忽略，幂等返回", "id", id, "name", v.Account.Name)
		return v, nil
	}
	if len(v.Carriers) > 0 {
		logger().Warn("忽略账号被拒：名下登录仍被载体引用", "id", id, "name", v.Account.Name, "carriers", v.Carriers)
		return View{}, &ReferencedError{Account: v.Account.Name, Carriers: v.Carriers, Action: "忽略"}
	}
	at := time.Now()
	body, err := ignoreBody(IgnoreMark{At: at, By: by})
	if err != nil {
		return View{}, err
	}
	if _, err := s.repo.Put(KindIgnore, id, 0, body, "accounts:"+actorOf(by)); err != nil {
		if !errors.Is(err, schedclient.ErrCASConflict) {
			logger().Error("写忽略行失败", "id", id, "name", v.Account.Name, "by", by, "cause", err)
			return View{}, fmt.Errorf("忽略账号 %s 失败: %w", v.Account.Name, err)
		}
		// 并发另一人先忽略：视为已忽略，at/by 以后写者为准不改。
		logger().Info("忽略账号：并发已写，按已忽略返回", "id", id, "name", v.Account.Name)
	}
	nv, err := s.Get(id)
	if err != nil {
		return View{}, err
	}
	// 读回可能已被并发 Restore 删掉忽略行（nv.Ignored=nil），用本次写入的 by/at 记日志，不解引用读面。
	logger().Info("忽略账号成功", "id", id, "name", nv.Account.Name, "by", actorOf(by),
		"at", at.UTC(), "logins", len(nv.Logins), "carriers", nv.Carriers)
	return nv, nil
}

// Restore 恢复一个账号（ref = 账号 id 或名字），返回写后读面。未忽略时同样成功（幂等，A18）；
// 账号不存在 → ErrNotFound（A19）。两人同时恢复时删冲突重读一次，不存在即成功。
func (s *Service) Restore(ref string) (View, error) {
	id, err := s.resolve(ref)
	if err != nil {
		return View{}, err
	}
	if _, _, err := s.definition(id); err != nil {
		return View{}, err
	}
	for attempt := 1; attempt <= 2; attempt++ {
		rec, err := s.repo.Get(KindIgnore, id)
		if err != nil {
			if errors.Is(err, schedclient.ErrNotFound) {
				break
			}
			logger().Error("恢复账号：读忽略行失败", "id", id, "cause", err)
			return View{}, fmt.Errorf("读账号 %s 的忽略行失败: %w", id, err)
		}
		if err := s.repo.Delete(KindIgnore, id, rec.Version, "accounts"); err != nil {
			if errors.Is(err, schedclient.ErrCASConflict) && attempt == 1 {
				logger().Info("恢复账号：忽略行 CAS 冲突，重读一次", "id", id)
				continue
			}
			logger().Error("恢复账号：删忽略行失败", "id", id, "version", rec.Version, "cause", err)
			return View{}, fmt.Errorf("恢复账号 %s 失败: %w", id, err)
		}
		break
	}
	nv, err := s.Get(id)
	if err != nil {
		return View{}, err
	}
	logger().Info("恢复账号成功", "id", id, "name", nv.Account.Name, "probe_login", nv.ProbeLogin, "logins", len(nv.Logins))
	return nv, nil
}

// actorOf 是写者标识里的人读后缀：by 为空用 "local"（与 body 规则一致）。
func actorOf(by string) string {
	by = strings.TrimSpace(by)
	if by == "" {
		return "local"
	}
	return by
}
