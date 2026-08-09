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
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
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

// authFileName 是权威副本与任务副本共用的文件名。
const authFileName = "auth.json"

// authorityMu 串行化本进程内所有任务对权威副本的写回。
//
// 为什么用包级锁而不是文件锁：grok 自己的 ~/.grok/auth.json.lock 协议无文档
// （实测 15 字节、疑似 PID），跟着猜不如不猜。跨进程的安全性由「原子 rename」
// 与「重读 + 严格更晚才写」共同保证：并发读者永远读到完整文件，丢更新窗口被
// 压到 rename 前的几微秒且方向安全（只会少写一次，不会写旧覆盖新）。
var authorityMu sync.Mutex

// authorityAuthPath 返回权威副本路径 ~/.grok/auth.json。
//
// 抽出来是为了让 EnsureAuthLink 与本文件共用同一个真相来源——两处各拼一遍
// 路径，将来改动时漏掉一处就会让软链指向和写回目标错开。
func authorityAuthPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("解析用户主目录: %w", err)
	}
	return filepath.Join(home, ".grok", authFileName), nil
}

// SyncAuthToAuthority 跑一轮凭据巡检：把任务 home 里 grok 自行刷新出的新凭据
// 收编进权威副本，并把任务侧恢复成软链。
//
// 参数：
//   - homeDir: 任务级 GROK_HOME，即 <taskDir>/grokhome
//   - log: 日志入口；nil 时退回 slog.Default()。调用方应传入已带 task 字段的
//     logger（本函数不认识 task id）
//
// 返回：**仅在「本轮确实该写回却写失败」时返回错误**。其余情况（无事可做、
// 任务侧损坏、权威侧缺失或损坏）都返回 nil——它们是可接受的稳态，不该让调用方
// 的看门狗把它当异常。
//
// 注意：
//   - 绝大多数轮次在第一个 lstat 就返回，成本是一次系统调用，可以放心高频调用；
//   - 「复位软链」与「收编」是两件独立的事：只要发现任务侧不是软链就该复位，
//     哪怕本轮没收编到任何东西（陈旧拷贝留着会让任务下次临期必死）。两处例外
//     写在下面的分支注释里。
func SyncAuthToAuthority(homeDir string, log *slog.Logger) error {
	if log == nil {
		log = slog.Default()
	}
	link := filepath.Join(homeDir, authFileName)
	fi, err := os.Lstat(link)
	if err != nil {
		// 还没建链，或任务目录已被清理——都不是本函数该管的事，静默返回
		return nil
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return nil // 仍是软链：grok 没在这里刷新过，零动作（绝大多数轮次走这里）
	}

	authorityMu.Lock()
	defer authorityMu.Unlock()

	authPath, err := authorityAuthPath()
	if err != nil {
		log.Error("grok 凭据巡检无法定位权威副本", "cause", err)
		return nil
	}
	authority, err := readAuthFile(authPath)
	if err != nil {
		// 例外一：权威侧缺失或损坏时**不复位软链**。用户可能刚 grok logout，
		// 不替他凭空造回来；更要紧的是复位等于把任务手里那份可能仍有效的凭据
		// 换成一个指向坏文件的链接。
		log.Warn("grok 权威凭据不可读，跳过收编且不复位软链",
			"path", authPath, "cause", err)
		return nil
	}
	taskAuth, err := readAuthFile(link)
	if err != nil {
		// 任务侧那份已经读不动了，留着毫无价值：不写权威文件，但接回软链，
		// 反而可能让这个任务下一轮活过来
		log.Error("grok 任务侧凭据副本损坏，不写权威副本", "path", link, "cause", err)
		resetAuthLink(link, authPath, log)
		return nil
	}

	merged, adopted := mergeNewerEntries(authority, taskAuth)
	if len(adopted) == 0 {
		log.Debug("grok 任务侧凭据不更新，跳过收编", "path", link)
		resetAuthLink(link, authPath, log)
		return nil
	}
	if err := writeAuthFileAtomic(authPath, merged); err != nil {
		// 例外二：写回失败时**保留任务侧副本、不复位软链**——那份副本可能是
		// 唯一一份有效的新凭据，复位等于把它丢掉。下轮重试
		log.Error("grok 凭据写回权威副本失败，保留任务侧副本待下轮重试",
			"path", authPath, "accounts", adopted, "cause", err)
		return err
	}
	for _, k := range adopted {
		oldAt, _ := entryExpiresAt(authority[k])
		newAt, _ := entryExpiresAt(merged[k])
		// 只打账号键与 expires_at，绝不打 token 值（spec §5 日志纪律）
		log.Info("grok 凭据已收编写回权威副本", "account", k,
			"old_expires_at", oldAt, "new_expires_at", newAt)
	}
	resetAuthLink(link, authPath, log)
	return nil
}

// resetAuthLink 把任务侧的普通文件换回指向权威副本的软链，复位不变量。
//
// 原子性：先在同目录建一个随机名字的临时软链，再 rename 覆盖到 link。rename 对
// 已存在的普通文件是原子的，中间不存在「文件已删、链接未建」的瞬间。
//
// 旧实现是「先 Remove 再 Symlink」两步：Symlink 一旦失败，任务 home 里的 auth.json
// 就消失了，而下一轮巡检开头 os.Lstat 报错会直接 return、永不重试——这个任务从此
// 没有凭据。这与 spec §5「恢复软链失败…下轮再试恢复」的承诺相悖，这里修正。
//
// 失败不向上传播：写回若已成功就不该因为复位失败而回滚，任务侧原文件保持不动，
// 下一轮巡检会再试。
func resetAuthLink(link, target string, log *slog.Logger) {
	dir := filepath.Dir(link)
	tmp, err := os.MkdirTemp(dir, authFileName+".handoff-link-")
	if err != nil {
		log.Error("grok auth 软链复位失败，下轮重试",
			"link", link, "target", target, "cause", err)
		return
	}
	// MkdirTemp 只是借它拿一个同目录的随机唯一名字；Symlink 要求目标不存在，
	// 先删掉这个占位目录
	if err := os.Remove(tmp); err != nil {
		log.Error("grok auth 软链复位失败，下轮重试",
			"link", link, "target", target, "cause", err)
		return
	}
	if err := os.Symlink(target, tmp); err != nil {
		_ = os.Remove(tmp) // 清理可能已建成的临时软链，不留垃圾
		log.Error("grok auth 软链复位失败，下轮重试",
			"link", link, "target", target, "cause", err)
		return
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp) // rename 失败：任务侧原文件仍在，清掉临时软链下轮再试
		log.Error("grok auth 软链复位失败，下轮重试",
			"link", link, "target", target, "cause", err)
		return
	}
	log.Info("grok auth 软链已复位", "link", link)
}

// readAuthFile 读取并解析一份 auth.json。
func readAuthFile(path string) (authFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读 %s: %w", path, err)
	}
	var af authFile
	if err := json.Unmarshal(b, &af); err != nil {
		return nil, fmt.Errorf("解析 %s: %w", path, err)
	}
	return af, nil
}

// writeAuthFileAtomic 用「同目录临时文件 + fsync + rename」原子替换权威副本。
//
// 临时文件必须建在**目标同目录**：rename 只在同一文件系统内保证原子，写到 /tmp
// 再搬过去会退化成非原子的跨设备拷贝，用户的 grok CLI 可能读到半截文件。
//
// 权限固定 0600：里面是凭据。
func writeAuthFileAtomic(path string, af authFile) error {
	b, err := json.Marshal(af)
	if err != nil {
		return fmt.Errorf("序列化凭据: %w", err)
	}
	f, err := os.CreateTemp(filepath.Dir(path), authFileName+".handoff-")
	if err != nil {
		return fmt.Errorf("建临时文件: %w", err)
	}
	tmp := f.Name()
	// rename 成功后这行是 no-op（路径已不存在）；失败时负责不留垃圾
	defer func() { _ = os.Remove(tmp) }()

	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return fmt.Errorf("设置临时文件权限: %w", err)
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return fmt.Errorf("写临时文件: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return fmt.Errorf("fsync 临时文件: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("关闭临时文件: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("原子替换 %s: %w", path, err)
	}
	return nil
}
