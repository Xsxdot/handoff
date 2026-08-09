# grok 凭据归属（周期巡检 + 收编写回）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 grok 任务在 home 里自行刷新出的新凭据被及时收编回 `~/.grok/auth.json`，任务侧恢复成软链，从而既不弄坏用户登录态、也不让后续 dispatch 拿到已作废的令牌。

**Architecture:** 新增 `internal/executor/grok/authsync.go`，分两层：纯合并逻辑（按账号键比 `expires_at`，严格更晚才收编）与 I/O 层（lstat 判形态 → 读两侧 → 合并 → 同目录临时文件 + fsync + rename 原子替换 → 复位软链）。调用点搭在已有的 per-task 看门狗 `Adapter.watchdog` 上，节流 30 秒一次，并在看门狗**两个出口**各补一次末轮巡检。不新增 goroutine、不动 agentd、不碰 `executor.Adapter` 接口。

**Tech Stack:** Go 标准库（`encoding/json`、`os`、`time`、`sync`、`log/slog`），项目现有 `slog` 日志入口。无新依赖。

**Spec:** [docs/superpowers/specs/2026-08-09-handoff-grok-credential-ownership-design.md](../specs/2026-08-09-handoff-grok-credential-ownership-design.md)

## Global Constraints

- **日志纪律（spec §5）**：只打账号键、`expires_at`、任务 id 三样；**任何情况下不打 token 值**，也不把凭据内容写进事件或 render.log。违反即为实现缺陷。
- **不用 `fmt.Printf`**：一律走注入的 `*slog.Logger`（`instrumenting-code`）。
- **`EnsureAuthLink` 的语义保持不动**：它负责「让任务能启动」，authsync 负责「让权威副本追上」。本计划只把它内部的路径拼装换成共享的 `authorityAuthPath()`，不改变外部行为。
- **fail-closed 原则**：任何比较不出来的情况（键缺失、`expires_at` 缺失或解析失败、非严格更晚）一律**不写**权威副本。宁可少写一次，不可写错一次。
- **不引入文件锁**：grok 自己的 `~/.grok/auth.json.lock` 协议无文档，不猜。并发用包级互斥锁 + 原子 rename 收口（spec §4.3）。
- **临时文件必须建在 `~/.grok/` 目录内**：`rename` 只在同一文件系统内保证原子。
- 验收口径（spec §8）：`go build ./...`、`go vet ./...`、`gofmt -l .` 无输出、`go test ./...` 全绿，且 `go test -race ./internal/executor/grok/` 绿。

---

### Task 1: 纯合并逻辑（authFile / entryExpiresAt / mergeNewerEntries）

这一层没有任何 I/O，是整个设计里唯一「可能写错用户凭据」的判断所在，先单独钉死。

**Files:**
- Create: `internal/executor/grok/authsync.go`
- Test: `internal/executor/grok/authsync_internal_test.go`

**Interfaces:**
- Consumes: 无（本任务是起点）
- Produces:
  - `type authFile map[string]json.RawMessage`
  - `func entryExpiresAt(raw json.RawMessage) (time.Time, error)`
  - `func mergeNewerEntries(authority, task authFile) (merged authFile, adopted []string)` — `adopted` 已按字典序排序，`authority` 不被修改

> 测试用**内部测试包**（`package grok`），与本包已有的 `askquestion_internal_test.go` 一致——被测符号全部不导出。

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/grok/authsync_internal_test.go`：

```go
package grok

import (
	"encoding/json"
	"testing"
)

// mkEntry 造一条最小账号条目：expires_at 用于比较，marker 落在 key 字段上，
// 用来验证收编是**整体搬运原文**而不是逐字段拼装（grok 加字段不能把凭据写残）。
func mkEntry(t *testing.T, expiresAt, marker string) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(map[string]string{
		"expires_at":    expiresAt,
		"key":           marker,
		"refresh_token": "rt-" + marker,
	})
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(b)
}

// markerOf 取出条目里的 key 字段，用于断言"哪一侧的原文胜出"。
func markerOf(t *testing.T, raw json.RawMessage) string {
	t.Helper()
	var e struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		t.Fatalf("解析条目失败: %v", err)
	}
	return e.Key
}

const acct = "https://auth.x.ai::b1a00492-0000-0000-0000-000000000000"

// TestEntryExpiresAtParsesRealFormat 钉住真机实测的带小数秒格式。
// devbox 上实测 expires_at = 2026-08-09T15:55:11.522980Z，
// 少了小数秒支持就会让所有收编静默走 fail-closed 分支（永远不写回）。
func TestEntryExpiresAtParsesRealFormat(t *testing.T) {
	got, err := entryExpiresAt(mkEntry(t, "2026-08-09T15:55:11.522980Z", "m"))
	if err != nil {
		t.Fatalf("解析真机格式出错: %v", err)
	}
	if got.IsZero() {
		t.Fatalf("解析结果为零值")
	}
}

func TestEntryExpiresAtRejectsBadInput(t *testing.T) {
	cases := map[string]json.RawMessage{
		"非 JSON 对象":      json.RawMessage(`"just a string"`),
		"缺 expires_at":   json.RawMessage(`{"key":"k"}`),
		"expires_at 非时间": json.RawMessage(`{"expires_at":"not-a-time"}`),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := entryExpiresAt(raw); err == nil {
				t.Errorf("应报错，实际返回 nil")
			}
		})
	}
}

// TestMergeNewerEntriesAdoptsOnlyStrictlyNewer 是本设计的误伤防线：
// 相等和更旧都必须不写——用户刚 grok login 过时倒灌回去等于直接弄坏登录态。
func TestMergeNewerEntriesAdoptsOnlyStrictlyNewer(t *testing.T) {
	const authExp = "2026-08-09T15:55:11.522980Z"
	cases := []struct {
		name        string
		taskExp     string
		wantAdopted bool
	}{
		{"任务侧更晚 → 收编", "2026-08-09T21:55:11.522980Z", true},
		{"两侧相等 → 不收编", authExp, false},
		{"任务侧更旧 → 不收编（防倒灌）", "2026-08-09T09:55:11.522980Z", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			authority := authFile{acct: mkEntry(t, authExp, "authority")}
			task := authFile{acct: mkEntry(t, c.taskExp, "task")}

			merged, adopted := mergeNewerEntries(authority, task)

			if c.wantAdopted {
				if len(adopted) != 1 || adopted[0] != acct {
					t.Fatalf("adopted = %v，期望 [%s]", adopted, acct)
				}
				if m := markerOf(t, merged[acct]); m != "task" {
					t.Errorf("胜出方 = %q，期望 task 侧原文", m)
				}
			} else {
				if len(adopted) != 0 {
					t.Fatalf("adopted = %v，期望空", adopted)
				}
				if m := markerOf(t, merged[acct]); m != "authority" {
					t.Errorf("胜出方 = %q，期望保留 authority 原值", m)
				}
			}
			// 输入不可被就地改写：调用方还要拿 authority 打新旧 expires_at 日志
			if m := markerOf(t, authority[acct]); m != "authority" {
				t.Errorf("authority 被就地修改了")
			}
		})
	}
}

// TestMergeNewerEntriesKeepsOtherAccounts 守住多账号：auth.json 是账号字典，
// 整文件覆盖会在用户有第二个账号时把它抹掉（spec §4.2）。
func TestMergeNewerEntriesKeepsOtherAccounts(t *testing.T) {
	const other = "https://auth.x.ai::other-client"
	authority := authFile{
		acct:  mkEntry(t, "2026-08-09T15:55:11.522980Z", "authority-a"),
		other: mkEntry(t, "2026-08-09T15:55:11.522980Z", "authority-b"),
	}
	task := authFile{acct: mkEntry(t, "2026-08-09T21:55:11.522980Z", "task-a")}

	merged, adopted := mergeNewerEntries(authority, task)

	if len(adopted) != 1 || adopted[0] != acct {
		t.Fatalf("adopted = %v，期望只收编 %s", adopted, acct)
	}
	if m := markerOf(t, merged[other]); m != "authority-b" {
		t.Errorf("其他账号被动了：%q", m)
	}
	if len(merged) != 2 {
		t.Errorf("merged 键数 = %d，期望 2", len(merged))
	}
}

// TestMergeNewerEntriesFailsClosed 三条 fail-closed 分支各一例。
func TestMergeNewerEntriesFailsClosed(t *testing.T) {
	const newer = "2026-08-09T21:55:11.522980Z"
	const older = "2026-08-09T15:55:11.522980Z"
	cases := []struct {
		name      string
		authority authFile
		task      authFile
	}{
		{
			name:      "权威侧没有该键 → 无从比较，不收编",
			authority: authFile{},
			task:      authFile{acct: mkEntry(t, newer, "task")},
		},
		{
			name:      "权威侧 expires_at 不可解析 → 不收编",
			authority: authFile{acct: json.RawMessage(`{"expires_at":"garbage"}`)},
			task:      authFile{acct: mkEntry(t, newer, "task")},
		},
		{
			name:      "任务侧 expires_at 不可解析 → 不收编",
			authority: authFile{acct: mkEntry(t, older, "authority")},
			task:      authFile{acct: json.RawMessage(`{"expires_at":"garbage"}`)},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, adopted := mergeNewerEntries(c.authority, c.task)
			if len(adopted) != 0 {
				t.Errorf("adopted = %v，期望空（fail-closed）", adopted)
			}
		})
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/grok/ -run 'TestEntryExpiresAt|TestMergeNewerEntries' -v`
Expected: 编译失败，`undefined: entryExpiresAt` / `undefined: authFile` / `undefined: mergeNewerEntries`

- [ ] **Step 3: 写最小实现**

创建 `internal/executor/grok/authsync.go`（本步只写文件头 + 本任务的三个符号；I/O 层在 Task 2 追加到同一文件）：

```go
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
```

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/grok/ -run 'TestEntryExpiresAt|TestMergeNewerEntries' -v`
Expected: 全部 PASS（含 3 个 fail-closed 子用例与 3 个严格更晚子用例）

- [ ] **Step 5: 加关键节点日志**

本任务**刻意不打日志**，这是唯一的例外，必须在代码里写明理由，否则后人会以为是漏了。在 `mergeNewerEntries` 的注释块末尾追加一行：

```go
// 为什么这层不打日志：它是纯函数、没有 I/O、没有外部调用，调用方 SyncAuthToAuthority
// 持有 task id 与路径上下文，由它统一打「收编/跳过」两条日志更有信息量；在这里再打
// 一遍只会让高频跳过路径刷屏。
```

- [ ] **Step 6: 加注释自检**

对照 `instrumenting-code` 逐项确认（本步不产出新代码，只核对 Step 3 已写的内容）：
- 文件头注释含职责 + 边界 + 「为什么允许副本存在」的 why —— 有
- 三个符号（`authFile`、`entryExpiresAt`、`mergeNewerEntries`）都有 doc 注释 —— 有
- 非显然分支有 why 注释：`json.RawMessage` 的选型理由、fail-closed 三条、`!tt.After(at)` 的「严格」含义 —— 有

- [ ] **Step 7: 提交**

```bash
gofmt -l internal/executor/grok/ && go vet ./internal/executor/grok/ && go test ./internal/executor/grok/
```

```bash
git add internal/executor/grok/authsync.go internal/executor/grok/authsync_internal_test.go && git commit -m "feat(grok): 凭据收编的纯合并逻辑（严格更晚才写，fail-closed）"
```

---

### Task 2: I/O 层（SyncAuthToAuthority 与原子写回）

**Files:**
- Modify: `internal/executor/grok/authsync.go`（追加 I/O 层）
- Modify: `internal/executor/grok/taskenv.go:141-161`（`EnsureAuthLink` 改用共享的 `authorityAuthPath()`）
- Test: `internal/executor/grok/authsync_test.go`（新建，外部测试包 `package grok_test`，与 `taskenv_test.go` 一致）

**Interfaces:**
- Consumes（Task 1）：`authFile`、`entryExpiresAt(json.RawMessage) (time.Time, error)`、`mergeNewerEntries(authority, task authFile) (authFile, []string)`
- Produces:
  - `func SyncAuthToAuthority(homeDir string, log *slog.Logger) error` — 导出，供 Task 3 从 `resume.go` 调用
  - `func authorityAuthPath() (string, error)` — 不导出，`EnsureAuthLink` 与本层共用
  - `const authFileName = "auth.json"`
  - `var authorityMu sync.Mutex`

> `os.UserHomeDir()` 在 darwin/linux 上读 `$HOME`，因此 `t.Setenv("HOME", t.TempDir())` 就能造出假的权威 home，零重构。`t.Setenv` 会让该用例不能与 `t.Parallel()` 同用——本文件所有用例都不要加 `t.Parallel()`。

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/grok/authsync_test.go`：

```go
package grok_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/xushixin/handoff/internal/executor/grok"
)

const (
	acct     = "https://auth.x.ai::b1a00492-0000-0000-0000-000000000000"
	expOld   = "2026-08-09T15:55:11.522980Z"
	expNewer = "2026-08-09T21:55:11.522980Z"
)

// authJSON 造一份 auth.json 文本；marker 落在 key 字段上用于判定哪一侧胜出。
func authJSON(t *testing.T, expiresAt, marker string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]map[string]string{
		acct: {"expires_at": expiresAt, "key": marker, "refresh_token": "rt-" + marker},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fakeHome 把 HOME 指向临时目录并按需写出权威副本，返回
// （权威 auth.json 路径, 任务级 grokhome 路径）。
// authorityBody 为 nil 表示"权威副本不存在"。
func fakeHome(t *testing.T, authorityBody []byte) (authPath, homeDir string) {
	t.Helper()
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	grokDir := filepath.Join(fake, ".grok")
	if err := os.MkdirAll(grokDir, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath = filepath.Join(grokDir, "auth.json")
	if authorityBody != nil {
		if err := os.WriteFile(authPath, authorityBody, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	homeDir = filepath.Join(t.TempDir(), "grokhome")
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	return authPath, homeDir
}

// writeTaskCopy 在任务 home 里放一份**普通文件**形态的 auth.json，
// 模拟 grok 刚在这里刷新过（替换掉了软链）。
func writeTaskCopy(t *testing.T, homeDir string, body []byte) string {
	t.Helper()
	p := filepath.Join(homeDir, "auth.json")
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// assertSymlinkTo 断言 link 是指向 target 的软链。
func assertSymlinkTo(t *testing.T, link, target string) {
	t.Helper()
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("lstat %s 失败: %v", link, err)
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s 不是软链（mode=%v）", link, fi.Mode())
	}
	got, err := os.Readlink(link)
	if err != nil {
		t.Fatal(err)
	}
	if got != target {
		t.Errorf("软链指向 %s，期望 %s", got, target)
	}
}

// markerIn 读出文件里该账号条目的 key 字段。
func markerIn(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("解析 %s 失败: %v", path, err)
	}
	return m[acct]["key"]
}

// 用例 1（spec §7）：软链未被替换 → 零动作，权威文件字节不变。
func TestSyncAuthLeavesIntactSymlinkAlone(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expOld, "authority"))
	if err := grok.EnsureAuthLink(homeDir); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(authPath)
	if err != nil {
		t.Fatal(err)
	}

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("SyncAuthToAuthority 出错: %v", err)
	}

	after, _ := os.ReadFile(authPath)
	if string(before) != string(after) {
		t.Errorf("权威文件被动了：%s", after)
	}
	assertSymlinkTo(t, filepath.Join(homeDir, "auth.json"), authPath)
}

// 用例 2（spec §7）：普通文件且 expires_at 更晚 → 收编写回 + 软链恢复。
func TestSyncAuthAdoptsNewerCopyAndRestoresLink(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expOld, "authority"))
	writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("SyncAuthToAuthority 出错: %v", err)
	}

	if m := markerIn(t, authPath); m != "task" {
		t.Errorf("权威副本 key = %q，期望被任务侧新凭据收编为 task", m)
	}
	if fi, err := os.Stat(authPath); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("权威副本权限 = %v，期望 0600", fi.Mode().Perm())
	}
	assertSymlinkTo(t, filepath.Join(homeDir, "auth.json"), authPath)
}

// 用例 3（spec §7）：任务侧更旧 → 不写回（防倒灌），**但软链仍被恢复**。
// 后半句是自审补的防线：陈旧拷贝留在 home 里，任务下次临期必然拿已作废的
// refresh token 去刷、必然失败。
func TestSyncAuthRefusesStaleCopyButStillRestoresLink(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expNewer, "authority"))
	writeTaskCopy(t, homeDir, authJSON(t, expOld, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("SyncAuthToAuthority 出错: %v", err)
	}

	if m := markerIn(t, authPath); m != "authority" {
		t.Errorf("权威副本被倒灌成 %q，期望保持 authority", m)
	}
	assertSymlinkTo(t, filepath.Join(homeDir, "auth.json"), authPath)
}

// 用例 4（spec §7）：多账号，只覆盖该账号键，其他键原值保留。
func TestSyncAuthKeepsOtherAccounts(t *testing.T) {
	const other = "https://auth.x.ai::other-client"
	authority := map[string]map[string]string{
		acct:  {"expires_at": expOld, "key": "authority-a"},
		other: {"expires_at": expOld, "key": "authority-b"},
	}
	ab, _ := json.Marshal(authority)
	authPath, homeDir := fakeHome(t, ab)
	writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("SyncAuthToAuthority 出错: %v", err)
	}

	b, _ := os.ReadFile(authPath)
	var got map[string]map[string]string
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got[acct]["key"] != "task" {
		t.Errorf("目标账号未被收编：%q", got[acct]["key"])
	}
	if got[other]["key"] != "authority-b" {
		t.Errorf("其他账号被动了：%q", got[other]["key"])
	}
}

// 用例 5（spec §7）：权威文件缺失 → 跳过、不创建、**也不建软链**。
// 用户可能刚 grok logout，不替他凭空造回来；软链指向不存在的目标只会让
// 下次启动更难诊断。
func TestSyncAuthSkipsWhenAuthorityMissing(t *testing.T) {
	authPath, homeDir := fakeHome(t, nil)
	link := writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("权威缺失不该返回错误，实际: %v", err)
	}

	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Errorf("不应创建权威文件，err=%v", err)
	}
	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("任务侧副本不该被删: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("权威缺失时不应复位软链")
	}
}

// 用例 6（spec §7）：任务侧 JSON 损坏 → 不写，权威文件字节级不变；软链仍被恢复
// （那份拷贝已经读不动了，留着毫无价值，接回权威副本反而可能救活任务）。
func TestSyncAuthIgnoresCorruptTaskCopyButRestoresLink(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expOld, "authority"))
	before, _ := os.ReadFile(authPath)
	writeTaskCopy(t, homeDir, []byte("{not json"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("任务侧损坏不该返回错误，实际: %v", err)
	}

	after, _ := os.ReadFile(authPath)
	if string(before) != string(after) {
		t.Errorf("权威文件被动了：%s", after)
	}
	assertSymlinkTo(t, filepath.Join(homeDir, "auth.json"), authPath)
}

// 用例 6b：权威侧 JSON 损坏 → 不写、**不复位软链**。
// 复位等于把任务手里那份可能仍有效的凭据换成一个指向坏文件的链接（spec §5）。
func TestSyncAuthKeepsTaskCopyWhenAuthorityCorrupt(t *testing.T) {
	_, homeDir := fakeHome(t, []byte("{not json"))
	link := writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err != nil {
		t.Fatalf("权威侧损坏不该返回错误，实际: %v", err)
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("任务侧副本不该被删: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("权威侧损坏时不应复位软链")
	}
	if m := markerIn(t, link); m != "task" {
		t.Errorf("任务侧副本内容被动了：%q", m)
	}
}

// 用例 7（spec §7）：写回失败 → **任务侧副本被保留、软链未被恢复**。
// 与用例 3 方向相反：那条守"别倒灌"，这条守"别把唯一一份新凭据丢掉"。
// 构造手法：把假的 ~/.grok 置为 0500（可读可进、不可写），
// CreateTemp 因此失败，而读权威副本仍然成功。
func TestSyncAuthKeepsTaskCopyWhenWriteFails(t *testing.T) {
	authPath, homeDir := fakeHome(t, authJSON(t, expOld, "authority"))
	grokDir := filepath.Dir(authPath)
	if err := os.Chmod(grokDir, 0o500); err != nil {
		t.Fatal(err)
	}
	// 必须还原，否则 t.TempDir 的清理会失败
	t.Cleanup(func() { _ = os.Chmod(grokDir, 0o700) })
	link := writeTaskCopy(t, homeDir, authJSON(t, expNewer, "task"))

	if err := grok.SyncAuthToAuthority(homeDir, nil); err == nil {
		t.Fatalf("写回失败应返回错误")
	}

	fi, err := os.Lstat(link)
	if err != nil {
		t.Fatalf("任务侧副本不该被删: %v", err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		t.Errorf("写回失败时不应复位软链——那份副本可能是唯一的新凭据")
	}
	if m := markerIn(t, link); m != "task" {
		t.Errorf("任务侧副本内容被动了：%q", m)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/grok/ -run TestSyncAuth -v`
Expected: 编译失败，`undefined: grok.SyncAuthToAuthority`

- [ ] **Step 3: 写最小实现**

在 `internal/executor/grok/authsync.go` 的 import 块补上 `"log/slog"`、`"os"`、`"path/filepath"`、`"sync"`，然后在文件末尾追加：

```go
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
// 失败不向上传播：写回若已成功就不该因为复位失败而回滚，下一轮巡检会再试。
func resetAuthLink(link, target string, log *slog.Logger) {
	if err := os.Remove(link); err != nil && !os.IsNotExist(err) {
		log.Error("grok 清理任务侧凭据副本失败，软链未复位", "path", link, "cause", err)
		return
	}
	if err := os.Symlink(target, link); err != nil {
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
```

- [ ] **Step 4: 让 `EnsureAuthLink` 共用同一个路径真相来源**

修改 `internal/executor/grok/taskenv.go:141-148`，把开头的路径拼装换掉（其余逻辑一字不动）：

```go
func EnsureAuthLink(homeDir string) error {
	target, err := authorityAuthPath()
	if err != nil {
		return err
	}
	link := filepath.Join(homeDir, authFileName)
```

原先的 `home, err := os.UserHomeDir()` 三行与 `filepath.Join(home, ".grok", "auth.json")` 一并删除。若删完 `taskenv.go` 里不再有 `os.UserHomeDir` 的其他调用，`os` 包仍被 `os.MkdirAll` 等使用，import 不需要动。

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./internal/executor/grok/ -run 'TestSyncAuth|TestEnsureAuthLink' -v`
Expected: 8 条 TestSyncAuth* 与既有的 `TestEnsureAuthLinkIsIdempotentAndRepairs` 全部 PASS

- [ ] **Step 6: 变异检查——确认用例 3 与用例 7 真的咬住了**

spec §8 点名这两条必须先跑 RED。Step 2 的 RED 是**编译失败**，那只证明符号不存在，不证明用例能分别抓到「倒灌」与「丢掉唯一一份新凭据」。这两条是本设计两个相反方向的防线，必须各做一次变异检查。

变异一（去掉「严格更晚」防线）：把 `mergeNewerEntries` 里的

```go
		if !tt.After(at) {
			continue
		}
```

临时改成 `if false && !tt.After(at) {`，然后跑：

> **不要写成 `if false { continue }`**：那样 `at` 与 `tt` 变成未使用变量，Go 直接编译失败，你拿到的是 build error 而不是测试失败——什么都证明不了。`false &&` 保住了两个变量的引用，短路后守卫失效，这才是要的变异。

Run: `go test ./internal/executor/grok/ -run 'TestSyncAuthRefusesStaleCopy|TestMergeNewerEntriesAdoptsOnlyStrictlyNewer' -v`
Expected: **FAIL**，报「权威副本被倒灌成 task」与「胜出方 = task，期望保留 authority 原值」。看到 FAIL 后把代码改回去。

变异二（去掉「写回失败不复位」防线）：把 `SyncAuthToAuthority` 里写回失败分支的 `return err` 临时改成 `resetAuthLink(link, authPath, log); return err`，然后跑：

Run: `go test ./internal/executor/grok/ -run TestSyncAuthKeepsTaskCopyWhenWriteFails -v`
Expected: **FAIL**，报「写回失败时不应复位软链」。看到 FAIL 后把代码改回去。

若某条变异后仍然 PASS，说明用例没咬住，先修用例再继续——例如变异二仍绿多半是 `chmod 0500` 没生效（以 root 跑测试会绕过权限位）。

**选注入点的通用教训**（本计划实施时真踩到过）：故障注入必须卡在**被测的那一步**上，不能卡在它之前。为 `resetAuthLink` 补的「无破链窗口」用例，最初用 `chmod(home, 0500)` 注入——但删文件需要的是目录写权限，旧的「先删后建」实现在第一步 `os.Remove` 就被 EACCES 挡住直接返回了，那个「文件已删、链接未建」的窗口根本没被走到，用例对新旧两种实现一律绿。要暴露该窗口，注入点必须让 `Symlink` 失败而 `Remove` 仍能成功：用超长 target（`"/" + strings.Repeat("a", 2000)`）触发 `ENAMETOOLONG`，目录保持可写。**任何「验证防线存在」的用例，都要用变异检查确认它对缺了防线的实现真的变红**，否则它只是看起来在测。

改回后重跑确认全绿：

Run: `go test ./internal/executor/grok/ -run 'TestSyncAuth|TestMergeNewerEntries' -v`
Expected: 全部 PASS

- [ ] **Step 7: 加关键节点日志**

对照本步已写的实现逐条核对（缺任一条即为本任务未完成）：
- 成功路径不静默：每个被收编的账号一条 Info「grok 凭据已收编写回权威副本」，带 account / old_expires_at / new_expires_at；复位成功一条 Info「grok auth 软链已复位」
- 高频跳过路径降级 Debug：「grok 任务侧凭据不更新，跳过收编」
- 每个错误分支带 cause 与路径：定位权威副本失败（Error）、权威凭据不可读（Warn）、任务侧副本损坏（Error）、写回失败（Error，带 accounts）、清理副本失败（Error）、软链复位失败（Error）
- 全程无 `fmt.Printf`
- **日志纪律**：`grep -n 'token\|refresh_token\|"key"' internal/executor/grok/authsync.go` 检查没有任何日志字段会带出 token 值——只有账号键与时间进日志

- [ ] **Step 8: 加注释自检**

- 新增的每个导出/非导出符号都有 doc 注释：`SyncAuthToAuthority`（含「只在该写回却写失败时返回错误」这条契约）、`authorityAuthPath`、`resetAuthLink`、`readAuthFile`、`writeAuthFileAtomic`、`authorityMu`、`authFileName`
- why 注释到位：包级锁 vs 文件锁的选型、临时文件必须同目录、两处「不复位软链」的例外各自的理由
- `EnsureAuthLink` 的既有注释不变（语义没变）

- [ ] **Step 9: 提交**

```bash
gofmt -l internal/executor/grok/ && go vet ./internal/executor/grok/ && go test -race ./internal/executor/grok/
```

```bash
git add internal/executor/grok/authsync.go internal/executor/grok/authsync_test.go internal/executor/grok/taskenv.go && git commit -m "feat(grok): 凭据巡检收编写回（原子替换 + 软链复位）"
```

---

### Task 3: 挂到看门狗上（含两个出口的末轮巡检）

**Files:**
- Modify: `internal/executor/grok/adapter.go:71-99`（`runState` 加 `lastAuthSync` 字段）
- Modify: `internal/executor/grok/resume.go:27-30`（加 `authSyncInterval` 常量）、`resume.go:118-146`（`watchdog` 接入）
- Test: `internal/executor/grok/watchdog_internal_test.go`（新建，内部测试包 `package grok`——`runState` 不导出）

**Interfaces:**
- Consumes（Task 2）：`SyncAuthToAuthority(homeDir string, log *slog.Logger) error`、`authFileName`；以及既有的 `homeDirName = "grokhome"`（`taskenv.go:38`）
- Produces:
  - `const authSyncInterval = 30 * time.Second`
  - `runState.lastAuthSync time.Time`
  - `func (a *Adapter) syncAuthThrottled(r *runState)`
  - `func (a *Adapter) syncAuthOnce(r *runState)`

> **`lastAuthSync` 不加锁是刻意的**：它只被该任务自己的看门狗 goroutine 读写。实现时**不要**顺手给它套 `turnMu`——那会把一个无竞争的字段绑到高频回合锁上（spec §4.1）。跨任务的并发面只在写权威文件那一步，已由 Task 2 的 `authorityMu` 收口。

- [ ] **Step 1: 写失败的测试**

创建 `internal/executor/grok/watchdog_internal_test.go`：

```go
package grok

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/executor"
)

// wdAuthJSON 造一份单账号 auth.json 文本。
func wdAuthJSON(t *testing.T, expiresAt, marker string) []byte {
	t.Helper()
	b, err := json.Marshal(map[string]map[string]string{
		"https://auth.x.ai::c": {"expires_at": expiresAt, "key": marker},
	})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// wdSetup 造出：假 HOME 下的权威副本（旧凭据）+ 任务目录下 grokhome 里一份
// **更新的普通文件**副本，模拟"grok 刚刷新过、看门狗还没来得及收编"。
// 返回（权威 auth.json 路径, 任务目录）。
func wdSetup(t *testing.T) (authPath, taskDir string) {
	t.Helper()
	fake := t.TempDir()
	t.Setenv("HOME", fake)
	grokDir := filepath.Join(fake, ".grok")
	if err := os.MkdirAll(grokDir, 0o700); err != nil {
		t.Fatal(err)
	}
	authPath = filepath.Join(grokDir, authFileName)
	if err := os.WriteFile(authPath, wdAuthJSON(t, "2026-08-09T15:55:11.522980Z", "authority"), 0o600); err != nil {
		t.Fatal(err)
	}
	taskDir = t.TempDir()
	homeDir := filepath.Join(taskDir, homeDirName)
	if err := os.MkdirAll(homeDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(homeDir, authFileName),
		wdAuthJSON(t, "2026-08-09T21:55:11.522980Z", "task"), 0o600); err != nil {
		t.Fatal(err)
	}
	return authPath, taskDir
}

// wdMarker 读出权威副本里的 key 字段，判断是否已被收编。
func wdMarker(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]string
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m["https://auth.x.ai::c"]["key"]
}

// deadPort 返回一个刚被释放、确定没人监听的回环端口，
// 让 Proc.Alive() 立刻连接被拒（不必等超时）。
func deadPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return p
}

// 用例 8a（spec §7）：evClosed 正常退场前，必须补最后一次巡检。
// 否则任务终结时躺在 home 里的新凭据会随任务目录一起被归档掉。
func TestWatchdogSyncsAuthOnNormalExit(t *testing.T) {
	authPath, taskDir := wdSetup(t)
	a := New(nil)
	r := &runState{
		taskID:  "t-closed",
		taskDir: taskDir,
		proc:    &Proc{TaskDir: taskDir, Port: deadPort(t)},
		evCh:    make(chan executor.AdapterEvent, 64),
	}
	// 事件通道已关闭 = 任务已终结，看门狗第一次醒来就该退场
	r.closeEvents()

	a.watchdog(r) // 同步跑：evClosed 分支会在一个 tick 内返回

	if m := wdMarker(t, authPath); m != "task" {
		t.Errorf("正常退场前未收编新凭据，权威副本 key = %q", m)
	}
}

// 用例 8b（spec §7）：探活判死退场前，同样必须补最后一次巡检。
// 漏这条就等于漏掉"任务跑挂了但刚刷新过"这一整类。
func TestWatchdogSyncsAuthOnDeathExit(t *testing.T) {
	authPath, taskDir := wdSetup(t)
	a := New(nil)
	r := &runState{
		taskID:  "t-dead",
		taskDir: taskDir,
		proc:    &Proc{TaskDir: taskDir, Port: deadPort(t)},
		evCh:    make(chan executor.AdapterEvent, 64),
		// 预置 lastAuthSync 让循环内的节流巡检**不触发**，这样本用例唯一可能
		// 完成收编的路径就是退场 defer。不预置的话第一次循环就会顺手同步掉，
		// 用例即使删掉 defer 也照样绿——那就白测了
		lastAuthSync: time.Now(),
	}

	a.watchdog(r) // 连续 watchdogFailThreshold 次探活失败后判死退场（约 600ms）

	if m := wdMarker(t, authPath); m != "task" {
		t.Errorf("判死退场前未收编新凭据，权威副本 key = %q", m)
	}
	// 顺带确认判死路径本身没被改坏
	if !r.evClosed {
		t.Errorf("判死后事件通道应已关闭")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./internal/executor/grok/ -run TestWatchdogSyncsAuth -v`
Expected: 两条都 FAIL，报 `权威副本 key = "authority"`（看门狗还没接巡检）

- [ ] **Step 3: 写最小实现**

3a. `internal/executor/grok/adapter.go` 的 `runState` 结构体里，在 `proc` / `cli` 之后加一个字段：

```go
	// lastAuthSync 是上一次凭据巡检的时刻，用于把巡检节流到 authSyncInterval。
	//
	// **刻意不加锁**：只被该任务自己的看门狗 goroutine 读写，无竞争。别顺手把它
	// 塞进 turnMu 的保护范围——那会把一个无竞争的字段绑到高频回合锁上。
	lastAuthSync time.Time
```

3b. `internal/executor/grok/resume.go` 的常量块（`resume.go:27-30`）追加：

```go
	// authSyncInterval 是凭据巡检的节流间隔。lstat 是微秒级，节流不是为了性能，
	// 是为了别让日志和写盘跟着 200ms 的探活节拍变吵。30 秒也是权威副本可能
	// 陈旧的时间上界。
	authSyncInterval = 30 * time.Second
```

3c. 改 `watchdog`（`resume.go:118`）。函数体开头加 `defer`，循环体内加节流调用：

```go
func (a *Adapter) watchdog(r *runState) {
	// 退场前一律补最后一次巡检：看门狗有两个出口（evClosed 正常退场、探活判死
	// 退场），用 defer 才能结构性地同时覆盖，而不是在两个 return 前各抄一遍——
	// 抄漏一个就等于漏掉"任务跑挂了但刚刷新过"这一整类。
	defer a.syncAuthOnce(r)

	interval, okStreak, failStreak := watchdogFastInterval, 0, 0
	for {
		time.Sleep(interval)
		r.emitMu.Lock()
		closed := r.evClosed
		r.emitMu.Unlock()
		if closed {
			return // 任务已终结，看门狗退场
		}
		a.syncAuthThrottled(r)
		if r.proc.Alive() {
		// ……以下原样不动……
```

3d. 在 `resume.go` 末尾追加两个方法：

```go
// syncAuthThrottled 按 authSyncInterval 节流地跑一轮凭据巡检。
//
// 注意：只被看门狗 goroutine 调用，因此读写 r.lastAuthSync 无需加锁。
func (a *Adapter) syncAuthThrottled(r *runState) {
	if time.Since(r.lastAuthSync) < authSyncInterval {
		return
	}
	a.syncAuthOnce(r)
}

// syncAuthOnce 无条件跑一轮凭据巡检（退场路径与节流路径共用）。
//
// 错误已在 SyncAuthToAuthority 内部记过日志，这里只做兜底记录：巡检失败不该
// 影响看门狗判死这件正事。
func (a *Adapter) syncAuthOnce(r *runState) {
	r.lastAuthSync = time.Now()
	if r.taskDir == "" {
		return // 没有任务目录就没有任务级 home，无从巡检
	}
	if err := SyncAuthToAuthority(filepath.Join(r.taskDir, homeDirName),
		a.log.With("task", r.taskID)); err != nil {
		a.log.Warn("grok 凭据巡检未完成，下轮重试", "task", r.taskID, "cause", err)
	}
}
```

`resume.go` 的 import 需要补 `"path/filepath"`（若尚未引入）。

- [ ] **Step 4: 跑测试确认通过**

Run: `go test ./internal/executor/grok/ -run TestWatchdogSyncsAuth -v`
Expected: 两条都 PASS

再跑整包与竞态检测：

Run: `go test -race ./internal/executor/grok/ -v`
Expected: 全绿，无 race 报告

- [ ] **Step 5: 加关键节点日志**

- 退场路径与节流路径的成功/跳过日志已由 `SyncAuthToAuthority` 内部产出（Task 2），此处**不重复打**，否则 30 秒一条会把 agentd 日志淹掉
- `syncAuthOnce` 的错误兜底：Warn「grok 凭据巡检未完成，下轮重试」，带 task 与 cause
- 传给 `SyncAuthToAuthority` 的 logger 必须是 `a.log.With("task", r.taskID)`——巡检层自己不认识 task id，不带就没法把日志归到具体任务上
- 全程无 `fmt.Printf`

- [ ] **Step 6: 加注释自检**

- `lastAuthSync` 字段有「为什么不加锁」的 why 注释
- `authSyncInterval` 常量有「节流是为了日志而非性能」的 why 注释
- `watchdog` 顶部的 `defer` 有「两个出口、用 defer 才能结构性覆盖」的 why 注释
- `syncAuthThrottled` / `syncAuthOnce` 各有 doc 注释

- [ ] **Step 7: 更新 grok adapter 文档中的凭据段落**

`docs/` 下 grok adapter 的说明里若写有「auth.json 走软链共享」的描述，补一句巡检收编机制与 30 秒节流；找出待改处：

```bash
grep -rn "auth.json\|EnsureAuthLink" docs/ --include=*.md
```

按 grep 结果就地补一段：任务 home 里的 auth.json 是软链，grok 刷新时会把它替换成普通文件，看门狗每 30 秒巡检一次、把更新的凭据收编回 `~/.grok/auth.json` 并复位软链。若 grep 无命中则跳过本步。

- [ ] **Step 8: 提交**

```bash
gofmt -l . && go vet ./... && go build ./... && go test -race ./internal/executor/grok/ && go test ./...
```

```bash
git add internal/executor/grok/ docs/ && git commit -m "feat(grok): 看门狗接入凭据巡检，两个出口各补末轮收编（B26）"
```

---

## 真机验收（spec §8，代码完成后单独执行）

三项，缺一不可：

1. devbox 上跨过一次真实的令牌刷新（6 小时一轮），确认 `~/.grok/auth.json` 的 `expires_at` 前进；
2. 刷新后新派发一个 grok 任务，`session/new` 成功、任务正常 `running`（这是 B26 现象的直接反面）；
3. agentd 日志中出现「grok 凭据已收编写回权威副本」与「grok auth 软链已复位」，且**通篇 grep 不到 token 值**：

```bash
grep -c 'eyJ\|refresh_token' ~/.handoff/logs/agentd.log
```

Expected: `0`（日志路径按 devbox 实际配置调整）
