// authsync.go —— grok 任务级 home 里凭据副本的收编写回。
//
// 职责：
//   - 发现 <taskDir>/grokhome/auth.json 从软链变成了普通文件（grok 刚在这里刷新过）
//   - 逐账号键比 expires_at，把严格更新的条目收编进权威副本 ~/.grok/auth.json
//   - 收尾把任务侧恢复成软链，复位「权威副本只有一份」的不变量
//
// 边界：
//   - 不起 goroutine：调用点是已有的 per-task 看门狗（resume.go 的 watchdog）
//   - 不解析条目内部结构：条目按 json.RawMessage 整体搬运，只读 expires_at 一个
//     字段用于比较——grok 升级改字段名不会让我们把用户凭据写残
//   - 不负责首次建链：那是 EnsureAuthLink 的职责，本文件只在发现破链时复位
//
// 为什么是「允许出现副本、及时收编」而不是「禁止出现副本」：grok 刷新令牌时替换
// 的是目录项（rename 或 unlink+create），软链和硬链都拦不住，禁止不了。详见
// docs/superpowers/specs/2026-08-09-handoff-grok-credential-ownership-design.md §2。
//
// 日志纪律：只打账号键、expires_at、任务 id，任何情况下不打 token 值。
package grok

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"
)

// authFile 是 auth.json 的顶层形状：账号键（形如 "<issuer>::<client_id>"）-> 条目原文。
//
// 值用 json.RawMessage 而非具体结构体是刻意的：条目里除 expires_at 外的字段
// （key / auth_mode / refresh_token / email …）没有文档，整体原样搬运才不会在
// grok 升级新增字段时把用户凭据写残。
type authFile map[string]json.RawMessage

// entryExpiresAt 从一条账号条目里取出 expires_at 并按 RFC3339 解析。
//
// 参数：
//   - raw: 一条账号条目的 JSON 原文
//
// 返回：过期时刻；条目不是对象、缺 expires_at、或时间格式不可解析时返回错误
//
// 注意：必须按时间值比较而非字符串比较——真机格式带小数秒
// （2026-08-09T15:55:11.522980Z），字符串序在跨时区/跨格式时不可靠。
func entryExpiresAt(raw json.RawMessage) (time.Time, error) {
	var e struct {
		ExpiresAt string `json:"expires_at"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return time.Time{}, fmt.Errorf("解析账号条目: %w", err)
	}
	if e.ExpiresAt == "" {
		return time.Time{}, fmt.Errorf("账号条目缺 expires_at")
	}
	t, err := time.Parse(time.RFC3339, e.ExpiresAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("解析 expires_at %q: %w", e.ExpiresAt, err)
	}
	return t, nil
}

// mergeNewerEntries 把 task 中严格更新的账号条目合并进 authority 的一份拷贝。
//
// 参数：
//   - authority: 权威副本解析出的账号字典（**不会被就地修改**，调用方还要用它打新旧对比日志）
//   - task: 任务 home 里那份副本解析出的账号字典
//
// 返回：
//   - merged: 合并结果；未被收编的键一律保留 authority 的原值
//   - adopted: 被收编的账号键，已按字典序排序（日志与断言都要稳定顺序）
//
// 注意：三条 fail-closed 规则，任一触发即不收编该键——
//   - authority 里没有这个键：无从比较，且它可能是别处残留，不凭空写入用户凭据；
//   - 任一侧 expires_at 解析失败；
//   - 任务侧不是**严格**更晚（相等不写，省掉无谓的写盘与日志）。
//
// 宁可少写一次，也不能写错一次：refresh token 一次性轮换，写反了直接弄坏用户登录态。
//
// 为什么这层不打日志：它是纯函数、没有 I/O、没有外部调用，调用方 SyncAuthToAuthority
// 持有 task id 与路径上下文，由它统一打「收编/跳过」两条日志更有信息量；在这里再打
// 一遍只会让高频跳过路径刷屏。
func mergeNewerEntries(authority, task authFile) (authFile, []string) {
	merged := make(authFile, len(authority))
	for k, v := range authority {
		merged[k] = v
	}
	var adopted []string
	for k, tv := range task {
		av, ok := authority[k]
		if !ok {
			continue
		}
		at, err := entryExpiresAt(av)
		if err != nil {
			continue
		}
		tt, err := entryExpiresAt(tv)
		if err != nil {
			continue
		}
		if !tt.After(at) {
			continue
		}
		merged[k] = tv
		adopted = append(adopted, k)
	}
	sort.Strings(adopted)
	return merged, adopted
}
