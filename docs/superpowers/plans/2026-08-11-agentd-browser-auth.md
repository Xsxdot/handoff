# agentd 浏览器鉴权 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.
>
> **同样必读：`instrumenting-code`。** 本计划每个实现类 task 都带「加关键节点日志」与「加注释」两个 step，它们是交付物的一部分，不是可选项。

**Goal:** 让浏览器（系统浏览器 / 桌面薄壳 / 将来的手机）能访问 agentd 的全部 `/api` 与 `/ws` 路由，机制是「主令牌签发一次性 ticket → 兑换 httpOnly cookie 会话」，且 CLI 一行不改。

**Architecture:** 三层叠加。最外层是 **Host 白名单中间件**（先于鉴权，堵 DNS rebinding）；中间层是**扩展后的 auth 中间件**（Bearer 优先、cookie 兜底，任一通过即放行，并把身份放进 request context）；里层是 **4 条 auth 路由**（签发 ticket / 兑换 cookie / 列出与吊销会话 / 登出）。会话与 ticket 只以 SHA-256 哈希落库，ticket 的一次性由一条条件 `UPDATE` 的原子性保证。已建立的 WS 连接由一个每 30 秒复验会话的 watcher goroutine 负责在吊销后踢掉。

**Tech Stack:** Go 1.22+ `net/http`（方法路由 + `{id}` 通配）、`github.com/coder/websocket`、`modernc.org/sqlite`（经 `internal/store`）、`spf13/cobra`、`google/uuid`、`log/slog`。

## Global Constraints

以下取自 spec，逐条为硬约束，**每个 task 的要求都隐含包含本节**：

- **CLI 一行不改**：今天的 `Authorization: Bearer` 路径必须原样继续工作；现有测试全绿是验收前提（spec §1.2、§12.1）。
- **不引入任何「本机免鉴权」路径**：loopback 不是安全边界（spec §2）。
- **长期凭据永不进 URL**：只有一次性 ticket 能出现在 query 参数里（spec §3.1、§8.1）。
- **两张新表只存 SHA-256 哈希，明文永不落库**（spec §5.4）。
- **`Secure` cookie 属性的判据只能是 `r.TLS != nil`，不得读 `X-Forwarded-Proto`**；同理 `/console` URL 的 scheme 也只按 `r.TLS` 判定（spec §5.5）。
- **不得引入「假定 TLS 一定由外部代理承担」的硬编码假设**（spec §8.3）。
- **本轮不托管前端**：`/console` 的 302 目标固定为 `/`，`/` 返回 404 是预期结果；**不得为了让页面别 404 而塞占位首页**（spec §5.1）。
- **凭据纪律**：主令牌、ticket 明文、cookie 明文一律不得进日志；设备名与会话 id 可以（spec §11）。
- **合入顺序**：Task 3（放开 cookie）**不得早于** Task 2（Host 白名单）合入，否则中间存在一个 rebinding 可用的窗口（spec §13）。
- 注释与日志遵循全局规范：新文件写职责/边界头注释，导出方法写参数/返回/注意，复杂分支写「为什么」的中文注释；日志一律 `slog`，禁止 `fmt.Printf`。

---

## File Structure

| 文件 | 动作 | 职责 |
|---|---|---|
| `internal/store/auth.go` | 新建 | `sessions` / `auth_tickets` 两表的 CRUD、凭据哈希、ticket 原子消费 |
| `internal/store/auth_test.go` | 新建 | 存储层断言（一次性、并发、明文不落库、吊销） |
| `internal/store/store.go` | 改 | `Open` 的 DDL 数组里加两张表 |
| `internal/proto/auth.go` | 新建 | `AuthTicketResp` / `SessionInfo` 线格式（服务端与 client 共用） |
| `internal/config/config.go` | 改 | 加 `Web WebConfig{AllowedHosts}` |
| `internal/agentd/hostguard.go` | 新建 | Host 白名单中间件 |
| `internal/agentd/hostguard_test.go` | 新建 | 403 先于鉴权、rebinding 回归、无 Origin 的 Bearer 客户端不受影响 |
| `internal/agentd/auth.go` | 新建 | 常量、`identity` 与 context 存取、cookie 会话查找、续期节流、会话复验 watcher |
| `internal/agentd/authroutes.go` | 新建 | 4 条 auth 路由的 handler + 凭据生成 + 设备名净化 |
| `internal/agentd/auth_test.go` | 新建 | ticket→cookie 全链路、会话吊销、滑动续期、WS 踢连接 |
| `internal/agentd/server.go` | 改 | `Handler()` 挂新路由与两层中间件；`auth()` 扩展；`handleEvents` 起 watcher；`Server` 加 `sessionRecheck` 字段 |
| `internal/client/client.go` | 改 | `IssueAuthTicket` / `ListSessions` / `RevokeSession` |
| `cmd/console.go` | 新建 | `handoff console` |
| `cmd/console_test.go` | 新建 | `--print-url` 输出契约、agentd 未运行时的报错 |
| `cmd/sessions.go` | 新建 | `handoff sessions` / `handoff sessions revoke` |
| `cmd/sessions_test.go` | 新建 | 列出/吊销渲染、设备名净化 |
| `README.md` | 改 | 浏览器鉴权用法与桌面壳接线契约 |

**为什么新增文件而不是往 `server.go` 里加**：`server.go` 已经 1270 行，鉴权是一个边界清晰、可独立测试的关注点，塞进去只会让两者都更难读。`server.go` 的改动被刻意压到最小（挂路由、扩展 `auth`、起 watcher）。

## 本轮明确不做（spec 里已写、但不产出代码的部分）

- **§7 手机接入（`handoff console --qr`）**：spec 里只是设计，本轮不实现。ticket→cookie 这套机制本身已经把手机场景的凭据模型定死了，`--qr` 只是同一 URL 的另一种呈现，晚做不产生返工。
- **§8 中转口子**：留的是「不新增假设」这个口子，不是代码。本计划兑现它的方式是两条**否定式约束**（见 Global Constraints）：不读 `X-Forwarded-Proto`、不硬编码「TLS 一定由外部代理承担」。
- **§9 相邻缺口**（整机 `/ws/events` 订阅、agentd→agentd 转发）：属 backlog，不在本计划范围。**§9 已确认不需要新建机器注册表**——现有配对（`cfg.Targets` + `proto.Task.Target`）已经够用。

**桌面壳（spec §13 第 7 步）不在本仓库**：main 上没有 `desktop/` 目录（Electron 路线已封存在 `codex/plan02-workspace-resources-rest`，ADR-0009 定的新薄壳尚未开工）。因此第 7 步在本计划中收敛为「把 `--print-url` 的输出契约用测试钉死 + 在 README 写清壳侧接线方式」（Task 6 与 Task 8），**不写壳代码**。

---

## Task 1: 会话与 ticket 的存储层

**Files:**
- Create: `internal/store/auth.go`
- Create: `internal/store/auth_test.go`
- Modify: `internal/store/store.go:71-101`（`Open` 的 DDL 数组）

**Interfaces:**
- Consumes: `store.Store`、`store.ErrNotFound`、`fmtTime` / `parseTime`（同包私有）
- Produces:
  - `type store.Session struct { ID, TokenHash, DeviceName string; CreatedAt, ExpiresAt, LastSeenAt time.Time; RevokedAt *time.Time }`
  - `func store.HashCredential(plain string) string`
  - `func (s *Store) CreateAuthTicket(hash, deviceName string, createdAt, expiresAt time.Time) error`
  - `func (s *Store) ConsumeAuthTicket(hash string, now time.Time) (deviceName string, expiresAt time.Time, err error)`
  - `func (s *Store) CreateSession(sess *Session) error`
  - `func (s *Store) SessionByTokenHash(hash string) (*Session, error)`
  - `func (s *Store) SessionByID(id string) (*Session, error)`
  - `func (s *Store) ListSessions() ([]Session, error)`
  - `func (s *Store) RevokeSession(id string, at time.Time) error`
  - `func (s *Store) TouchSession(id string, lastSeen, expiresAt time.Time) error`

- [ ] **Step 1: 写失败的测试**

创建 `internal/store/auth_test.go`：

```go
// store 鉴权表测试：ticket 的一次性与并发原子性、明文不落库、会话增删查与吊销。
package store_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/store"
)

// openTestStore 打开一个临时库并注册清理，同时返回库文件路径（供白盒断言旁路开库）。
func openTestStore(t *testing.T) (*store.Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "handoff.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

// TestConsumeAuthTicketOnce 钉死 spec §12 断言 4：同一张 ticket 第二次消费必败。
func TestConsumeAuthTicketOnce(t *testing.T) {
	st, _ := openTestStore(t)
	now := time.Now()
	hash := store.HashCredential("plain-ticket")
	if err := st.CreateAuthTicket(hash, "mbp", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("CreateAuthTicket: %v", err)
	}
	device, expires, err := st.ConsumeAuthTicket(hash, now)
	if err != nil {
		t.Fatalf("首次消费应成功，得到: %v", err)
	}
	if device != "mbp" {
		t.Errorf("设备名 = %q，期望 mbp", device)
	}
	if !expires.After(now) {
		t.Errorf("过期时刻 %v 应晚于 %v", expires, now)
	}
	if _, _, err := st.ConsumeAuthTicket(hash, now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("二次消费应 ErrNotFound，得到: %v", err)
	}
}

// TestConsumeAuthTicketConcurrent 钉死 spec §12 断言 5：并发消费恰好一个成功。
//
// 这条测的是 SQL 条件 UPDATE 的原子性本身——若实现退化成「先查后改」，
// 本测试会看到多个成功。
func TestConsumeAuthTicketConcurrent(t *testing.T) {
	st, _ := openTestStore(t)
	now := time.Now()
	hash := store.HashCredential("race-ticket")
	if err := st.CreateAuthTicket(hash, "mbp", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("CreateAuthTicket: %v", err)
	}
	const n = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		okN  int
		errs []error
	)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, _, err := st.ConsumeAuthTicket(hash, now)
			mu.Lock()
			defer mu.Unlock()
			if err == nil {
				okN++
				return
			}
			if !errors.Is(err, store.ErrNotFound) {
				errs = append(errs, err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if len(errs) > 0 {
		t.Fatalf("出现非预期错误: %v", errs)
	}
	if okN != 1 {
		t.Fatalf("成功消费次数 = %d，期望恰好 1", okN)
	}
}

// TestAuthTicketPlaintextNotStored 钉死 spec §12 断言 7：ticket 明文不落库。
func TestAuthTicketPlaintextNotStored(t *testing.T) {
	st, path := openTestStore(t)
	now := time.Now()
	const plain = "super-secret-plaintext"
	if err := st.CreateAuthTicket(store.HashCredential(plain), "mbp", now, now.Add(time.Minute)); err != nil {
		t.Fatalf("CreateAuthTicket: %v", err)
	}
	if got := dumpColumn(t, path, "SELECT id FROM auth_tickets"); got == plain {
		t.Fatalf("库中出现 ticket 明文: %q", got)
	}
}

// TestSessionLifecycle 覆盖会话的建、查（按哈希/按 id）、列、续期、吊销。
func TestSessionLifecycle(t *testing.T) {
	st, _ := openTestStore(t)
	now := time.Now().Truncate(time.Second)
	sess := &store.Session{
		ID:         "sess-1",
		TokenHash:  store.HashCredential("cookie-plain"),
		DeviceName: "mbp / Safari",
		CreatedAt:  now,
		ExpiresAt:  now.Add(30 * 24 * time.Hour),
		LastSeenAt: now,
	}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	got, err := st.SessionByTokenHash(store.HashCredential("cookie-plain"))
	if err != nil {
		t.Fatalf("SessionByTokenHash: %v", err)
	}
	if got.ID != "sess-1" || got.DeviceName != "mbp / Safari" || got.RevokedAt != nil {
		t.Fatalf("查回的会话不对: %+v", got)
	}
	if _, err := st.SessionByTokenHash(store.HashCredential("wrong")); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("错误 cookie 应 ErrNotFound，得到: %v", err)
	}

	newExpires := now.Add(60 * 24 * time.Hour)
	newSeen := now.Add(time.Hour)
	if err := st.TouchSession("sess-1", newSeen, newExpires); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}
	got, err = st.SessionByID("sess-1")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if !got.ExpiresAt.Equal(newExpires) || !got.LastSeenAt.Equal(newSeen) {
		t.Fatalf("续期未写回: expires=%v last_seen=%v", got.ExpiresAt, got.LastSeenAt)
	}

	list, err := st.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("会话条数 = %d，期望 1", len(list))
	}

	if err := st.RevokeSession("sess-1", now.Add(2*time.Hour)); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	got, err = st.SessionByID("sess-1")
	if err != nil {
		t.Fatalf("吊销后仍应能查到（用于展示与复验）: %v", err)
	}
	if got.RevokedAt == nil {
		t.Fatal("吊销后 RevokedAt 仍为 nil")
	}
	if err := st.RevokeSession("sess-1", now.Add(3*time.Hour)); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("重复吊销应 ErrNotFound，得到: %v", err)
	}
	if err := st.RevokeSession("不存在", now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("吊销不存在会话应 ErrNotFound，得到: %v", err)
	}
}

// dumpColumn 旁路开库读一列的第一行，用于「明文不落库」这类白盒断言。
//
// 为什么另开一个连接而不加一个 Store 的导出查询方法：只为测试而在生产 API 上
// 开一个任意 SQL 的口子，代价远大于这里多两行。
func dumpColumn(t *testing.T, path, query string) string {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("旁路打开库: %v", err)
	}
	defer db.Close()
	var v string
	if err := db.QueryRow(query).Scan(&v); err != nil {
		t.Fatalf("查询 %s: %v", query, err)
	}
	return v
}
```

测试文件顶部需要 `_ "modernc.org/sqlite"` 的空导入（旁路 `sql.Open` 要用该驱动）。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/store/ -run 'TestConsumeAuthTicket|TestAuthTicket|TestSessionLifecycle' -v
```

Expected: 编译失败，`undefined: store.HashCredential` 等。

- [ ] **Step 3: 建两张表**

在 `internal/store/store.go` 的 `Open` DDL 数组末尾（`repos` 之后）追加：

```go
		`CREATE TABLE IF NOT EXISTS sessions (
  id           TEXT PRIMARY KEY,           -- 会话 id，可公开，用于列出与吊销
  token_hash   TEXT NOT NULL UNIQUE,       -- cookie 值的 SHA-256；明文不落库
  device_name  TEXT NOT NULL DEFAULT '',
  created_at   TIMESTAMP NOT NULL,
  expires_at   TIMESTAMP NOT NULL,
  last_seen_at TIMESTAMP NOT NULL,
  revoked_at   TIMESTAMP)`,
		`CREATE TABLE IF NOT EXISTS auth_tickets (
  id          TEXT PRIMARY KEY,            -- ticket 明文的 SHA-256
  device_name TEXT NOT NULL DEFAULT '',
  created_at  TIMESTAMP NOT NULL,
  expires_at  TIMESTAMP NOT NULL,
  consumed_at TIMESTAMP)`,
```

`store.go` 本轮只改这一处：两条 `CREATE TABLE IF NOT EXISTS` 追加进 `Open` 的 DDL 数组，不动任何既有语句、不加新的导出 API。

- [ ] **Step 4: 实现存储层**

创建 `internal/store/auth.go`：

```go
// 本文件实现浏览器鉴权所需的两张表：会话（sessions）与一次性 ticket（auth_tickets）。
//
// 职责：
//   - 凭据哈希：两张表都只存 SHA-256，明文永不落库
//   - ticket 的创建与**原子消费**（一次性由一条条件 UPDATE 的原子性保证）
//   - 会话的创建、按哈希/按 id 查询、列出、吊销、活跃与续期写入
//
// 边界：
//   - 不判断会话/ticket 是否「有效」：过期与吊销的判定属业务规则，由 agentd 侧做
//     （store 是叶子层；且 spec §11 要求把「会话过期」与「会话已吊销」作为不同
//     原因分别记日志，合并判定就拿不出这个区分）
//   - 不生成凭据明文、不设置 cookie、不做任何 HTTP 相关处理
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"
)

// Session 是一个浏览器会话记录。
//
// 注意：
//   - TokenHash 是 cookie 明文的 SHA-256；明文不落库，本结构体永远拿不到它
//   - RevokedAt 为 nil 表示未吊销；是否「有效」还要看 ExpiresAt，由调用方判定
type Session struct {
	ID         string
	TokenHash  string
	DeviceName string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	RevokedAt  *time.Time
}

// HashCredential 计算凭据明文的 SHA-256 十六进制串。
//
// 参数：
//   - plain: 凭据明文（ticket 或 cookie 值）
//
// 返回：
//   - 64 字符的小写十六进制哈希
//
// 注意：
//   - 为什么不加盐、不用 bcrypt：输入是 256 位高熵随机串（不是人选的口令），
//     没有字典攻击面；而查表必须是 O(1) 精确匹配，加盐会退化成逐行比对
func HashCredential(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}

// CreateAuthTicket 落库一张一次性 ticket。
//
// 参数：
//   - hash: ticket 明文的 SHA-256（由调用方算好；本层不接触明文）
//   - deviceName: 签发时登记的设备名，纯展示
//   - createdAt / expiresAt: 签发与过期时刻
//
// 返回：
//   - 数据库错误；重复 hash 会因主键冲突报错（概率上不可能，视为真故障）
func (s *Store) CreateAuthTicket(hash, deviceName string, createdAt, expiresAt time.Time) error {
	if _, err := s.db.ExecContext(context.Background(), `
INSERT INTO auth_tickets (id, device_name, created_at, expires_at, consumed_at)
VALUES (?, ?, ?, ?, NULL)`,
		hash, deviceName, fmtTime(createdAt), fmtTime(expiresAt)); err != nil {
		return fmt.Errorf("写入 ticket: %w", err)
	}
	return nil
}

// ConsumeAuthTicket 原子认领一张未消费的 ticket，并返回它的设备名与过期时刻。
//
// 参数：
//   - hash: ticket 明文的 SHA-256
//   - now: 认领时刻，写入 consumed_at
//
// 返回：
//   - deviceName / expiresAt: 该 ticket 的登记信息
//   - ErrNotFound: 不存在或已被消费（对调用方是同一个结论：这张票不能用）
//
// 注意：
//   - 一次性**由 SQL 条件 UPDATE 的原子性保证**，不靠「先查后改」——后者在并发下
//     会让同一张 ticket 换出两个会话
//   - **过期不在这里判**：返回 expiresAt 交给调用方比较。原因是本表的时间以
//     RFC3339Nano 文本存储，Go 的格式化会裁掉尾部零，导致「整秒」与「带小数秒」
//     的两个时刻在 SQLite 里按字典序比较时次序反转（`.` 的码位小于 `Z`）。
//     把比较放到 Go 侧用 time.Time 做，才是无坑的
func (s *Store) ConsumeAuthTicket(hash string, now time.Time) (string, time.Time, error) {
	res, err := s.db.ExecContext(context.Background(), `
UPDATE auth_tickets SET consumed_at = ? WHERE id = ? AND consumed_at IS NULL`,
		fmtTime(now), hash)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("消费 ticket: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("读取 ticket 影响行数: %w", err)
	}
	if n != 1 {
		return "", time.Time{}, ErrNotFound
	}
	var deviceName, expiresAt string
	if err := s.db.QueryRowContext(context.Background(),
		"SELECT device_name, expires_at FROM auth_tickets WHERE id = ?", hash).
		Scan(&deviceName, &expiresAt); err != nil {
		return "", time.Time{}, fmt.Errorf("读取 ticket 登记信息: %w", err)
	}
	return deviceName, parseTime(expiresAt), nil
}

// CreateSession 落库一个新会话。
//
// 参数：
//   - sess: 会话数据；ID 与 TokenHash 必须非空，RevokedAt 被忽略（新会话必未吊销）
//
// 返回：
//   - 数据库错误
func (s *Store) CreateSession(sess *Session) error {
	if _, err := s.db.ExecContext(context.Background(), `
INSERT INTO sessions (id, token_hash, device_name, created_at, expires_at, last_seen_at, revoked_at)
VALUES (?, ?, ?, ?, ?, ?, NULL)`,
		sess.ID, sess.TokenHash, sess.DeviceName,
		fmtTime(sess.CreatedAt), fmtTime(sess.ExpiresAt), fmtTime(sess.LastSeenAt)); err != nil {
		return fmt.Errorf("写入会话 %s: %w", sess.ID, err)
	}
	return nil
}

// sessionColumns 是会话查询的固定列序，与 scanSession 一一对应。
const sessionColumns = "id, token_hash, device_name, created_at, expires_at, last_seen_at, revoked_at"

// scanSession 按 sessionColumns 的列序把一行扫成 Session。
func scanSession(sc interface{ Scan(...any) error }) (*Session, error) {
	var (
		sess      Session
		created   string
		expires   string
		lastSeen  string
		revokedAt sql.NullString
	)
	if err := sc.Scan(&sess.ID, &sess.TokenHash, &sess.DeviceName,
		&created, &expires, &lastSeen, &revokedAt); err != nil {
		return nil, err
	}
	sess.CreatedAt = parseTime(created)
	sess.ExpiresAt = parseTime(expires)
	sess.LastSeenAt = parseTime(lastSeen)
	if revokedAt.Valid {
		t := parseTime(revokedAt.String)
		sess.RevokedAt = &t
	}
	return &sess, nil
}

// SessionByTokenHash 按 cookie 哈希查会话；不存在返回 ErrNotFound。
//
// 注意：已过期或已吊销的会话**照样返回**——有效性由调用方判定，这样才能把
// 「不存在 / 已过期 / 已吊销」记成三种不同的鉴权失败原因
func (s *Store) SessionByTokenHash(hash string) (*Session, error) {
	row := s.db.QueryRowContext(context.Background(),
		"SELECT "+sessionColumns+" FROM sessions WHERE token_hash = ?", hash)
	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("按哈希查询会话: %w", err)
	}
	return sess, nil
}

// SessionByID 按会话 id 查会话；不存在返回 ErrNotFound。
func (s *Store) SessionByID(id string) (*Session, error) {
	row := s.db.QueryRowContext(context.Background(),
		"SELECT "+sessionColumns+" FROM sessions WHERE id = ?", id)
	sess, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("按 id 查询会话 %s: %w", id, err)
	}
	return sess, nil
}

// ListSessions 列出全部会话（含已吊销与已过期），按创建时刻降序。
//
// 注意：已吊销的会话也返回——`handoff sessions` 要能看到「这台设备已经被吊销了」，
// 直接消失反而让人怀疑是不是漏看了
func (s *Store) ListSessions() ([]Session, error) {
	rows, err := s.db.QueryContext(context.Background(),
		"SELECT "+sessionColumns+" FROM sessions ORDER BY created_at DESC")
	if err != nil {
		return nil, fmt.Errorf("列出会话: %w", err)
	}
	defer rows.Close()
	out := make([]Session, 0, 8)
	for rows.Next() {
		sess, serr := scanSession(rows)
		if serr != nil {
			return nil, fmt.Errorf("扫描会话行: %w", serr)
		}
		out = append(out, *sess)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("遍历会话: %w", err)
	}
	return out, nil
}

// RevokeSession 吊销一个尚未吊销的会话。
//
// 返回：
//   - ErrNotFound: 会话不存在**或**已被吊销（两者对调用方是同一个结论：这次吊销没改变什么）
func (s *Store) RevokeSession(id string, at time.Time) error {
	res, err := s.db.ExecContext(context.Background(),
		"UPDATE sessions SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL",
		fmtTime(at), id)
	if err != nil {
		return fmt.Errorf("吊销会话 %s: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("读取吊销影响行数: %w", err)
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

// TouchSession 写回会话的最后活跃时刻与过期时刻（滑动续期）。
//
// 注意：调用方负责节流——本方法每次调用都真写库
func (s *Store) TouchSession(id string, lastSeen, expiresAt time.Time) error {
	if _, err := s.db.ExecContext(context.Background(),
		"UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?",
		fmtTime(lastSeen), fmtTime(expiresAt), id); err != nil {
		return fmt.Errorf("更新会话 %s 活跃时刻: %w", id, err)
	}
	return nil
}
```

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/store/ -v
```

Expected: 全部 PASS（含既有测试）。

- [ ] **Step 6: 加关键节点日志**

本层是 store 包，遵循它既有的叶子层纪律（包头注释第三条：**方法错误 return 前不打日志**，由调用方带上下文记录）。因此：

- 不在 `internal/store/auth.go` 的任何方法里打日志——这是刻意的，与 `CreateTicket` / `GetTicket` 一致。
- 唯一的例外沿用既有约定：建表成功已由 `Open` 的 Info 覆盖，不再新增。
- **本 task 的日志责任转移给 Task 4/6 的调用方**：签发、消费、建立、吊销四个节点的 Info 都在 agentd 侧打（spec §11）。在 `auth.go` 包头注释里显式写明这条转移，避免后来者以为漏了。

- [ ] **Step 7: 加注释**

- 文件头：职责 + 边界（已含在 Step 4 代码中，逐条核对是否与实现一致）。
- 每个导出方法：参数 / 返回 / 注意（已含）。
- 三处必须有的「为什么」注释：
  1. `HashCredential` 为什么不加盐；
  2. `ConsumeAuthTicket` 为什么用条件 UPDATE 而非先查后改，以及**为什么过期判定不放在 SQL 里**（RFC3339Nano 字典序陷阱）；
  3. `SessionByTokenHash` / `ListSessions` 为什么把无效会话也返回。

- [ ] **Step 8: 提交**

```bash
git add internal/store/auth.go internal/store/auth_test.go internal/store/store.go && git commit -m "feat(store): 会话与一次性 ticket 的存储层（哈希落库 + 原子消费）"
```

---

## Task 2: Host 白名单中间件

**Files:**
- Create: `internal/agentd/hostguard.go`
- Create: `internal/agentd/hostguard_test.go`
- Modify: `internal/agentd/server.go:133-153`（`Handler`）
- Modify: `internal/config/config.go:40-69`（`Config` 加 `Web` 字段）

**Interfaces:**
- Consumes: `Server.cfg`、`Server.log`、`writeJSON`
- Produces:
  - `func (s *Server) hostGuard(next http.Handler) http.Handler`
  - `func hostOnly(hostport string) string`
  - `type config.WebConfig struct { AllowedHosts []string \`yaml:"allowed_hosts"\` }`，`Config.Web WebConfig`

> **合入纪律：本 task 必须先于 Task 3 合入。** 先放开 cookie 再补白名单，中间会存在一个 DNS rebinding 可用的窗口（spec §13）。

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/hostguard_test.go`（**白盒，package agentd**：需要直接构造 Server 并伪造 `Host` 头）：

```go
// Host 白名单中间件测试：钉死 spec §12 断言 13/14/15。
//
// 边界：白盒测试（package agentd），因为要伪造 Host 头并直接读 Server 内部构造。
package agentd

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/websocket"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/store"
)

const hostTestToken = "host-test-token"

// newHostTestEnv 构造一个带真实 store 的 Server 与 httptest 服务。
func newHostTestEnv(t *testing.T, cfg *config.Config) (*Server, *httptest.Server, *strings.Builder) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "handoff.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	var logs strings.Builder
	srv := NewServer(cfg, st, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts, &logs
}

// doWithHost 发一个指定 Host 头的请求。
//
// 注意：必须用 req.Host 而不是 req.Header.Set("Host", ...)——net/http 的客户端
// 只认前者，后者会被静默忽略，测试会假通过。
func doWithHost(t *testing.T, ts *httptest.Server, host, auth string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks", nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Host = host
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestHostGuardRejectsForeignHostBeforeAuth 钉死断言 13：
// 伪造 Host 得到 403，且**先于**鉴权发生——带一个错误的 token 也仍是 403 而非 401，
// 攻击者从状态码里读不出「凭据对不对」。
func TestHostGuardRejectsForeignHostBeforeAuth(t *testing.T) {
	_, ts, logs := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	resp := doWithHost(t, ts, "evil.com", "Bearer 错的令牌")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("状态码 = %d，期望 403", resp.StatusCode)
	}
	// 正确的令牌同样是 403：证明白名单确实在鉴权之前
	resp = doWithHost(t, ts, "evil.com", "Bearer "+hostTestToken)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("带正确令牌时状态码 = %d，期望仍是 403", resp.StatusCode)
	}
	if !strings.Contains(logs.String(), "Host 不在白名单") {
		t.Error("缺少 Host 白名单拒绝的 Warn 日志——这是 rebinding 攻击的唯一信号")
	}
}

// TestHostGuardDNSRebindingRegression 钉死断言 14：
// Host 与 Origin 相等正是 coder/websocket 的 accept.go:239 会直接放过的组合，
// 必须在到达 websocket.Accept 之前就被白名单挡下。
func TestHostGuardDNSRebindingRegression(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/ws/events?task=任意", nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	req.Host = "evil.com"
	req.Header.Set("Origin", "http://evil.com")
	req.Header.Set("Authorization", "Bearer "+hostTestToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("rebinding 组合的状态码 = %d，期望 403", resp.StatusCode)
	}
}

// TestHostGuardAllowsLoopbackAndConfigured 钉死：回环三件套与配置扩展项放行，
// 端口不参与判定（httptest 的端口是随机的）。
func TestHostGuardAllowsLoopbackAndConfigured(t *testing.T) {
	cfg := &config.Config{
		Token:  hostTestToken,
		Listen: "192.168.1.10:7777",
		Web:    config.WebConfig{AllowedHosts: []string{"handoff.example.com"}},
	}
	_, ts, _ := newHostTestEnv(t, cfg)
	for _, host := range []string{
		"127.0.0.1:7777", "localhost:1234", "[::1]:7777",
		"192.168.1.10:7777", "handoff.example.com", "LOCALHOST:9",
	} {
		resp := doWithHost(t, ts, host, "Bearer "+hostTestToken)
		if resp.StatusCode == http.StatusForbidden {
			t.Errorf("Host %q 被 403，应放行", host)
		}
	}
}

// TestHostGuardWildcardListenNotAllowed 钉死：0.0.0.0 不进白名单——
// 它不是一个可用于访问的 Host，放进去没有意义。
func TestHostGuardWildcardListenNotAllowed(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken, Listen: "0.0.0.0:7777"})
	if resp := doWithHost(t, ts, "0.0.0.0:7777", "Bearer "+hostTestToken); resp.StatusCode != http.StatusForbidden {
		t.Fatalf("0.0.0.0 的状态码 = %d，期望 403", resp.StatusCode)
	}
}

// TestNonBrowserBearerClientStillConnects 钉死断言 15：
// 不带 Origin 头的非浏览器客户端（即 CLI）带 Bearer 仍能完成 WS 升级——白名单不得误伤 CLI。
func TestNonBrowserBearerClientStillConnects(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	taskID := mustWSTask(t, srv.st)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(ts)+"/ws/events?task="+taskID, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + hostTestToken}},
	})
	if err != nil {
		t.Fatalf("CLI 形态的 WS 连接被拒: %v", err)
	}
	conn.Close(websocket.StatusNormalClosure, "")
}

// mustWSTask 造一个 running 状态的任务，返回它的 id。
//
// handleEvents 会先查任务是否存在、不存在就以 1008 关闭，所以任何要保持连接
// 存活的 WS 测试都必须先有一个真任务。
func mustWSTask(t *testing.T, st *store.Store) string {
	t.Helper()
	const id = "11111111-2222-3333-4444-555555555555"
	now := time.Now()
	mustCreateTask(t, st, &proto.Task{
		ID: id, RepoPath: "/r", Executor: "fake",
		State: proto.TaskStateRunning, CreatedAt: now, UpdatedAt: now,
	})
	return id
}

// wsURL 把 httptest 的 http:// 前缀换成 ws://。
func wsURL(ts *httptest.Server) string {
	return "ws" + strings.TrimPrefix(ts.URL, "http")
}
```

`mustCreateTask(t, st, *proto.Task)` 是 `internal/agentd/manager_test.go:163` 已有的同包辅助，直接复用（本文件同为 `package agentd`）。import 需补 `time`、`net/http/httptest`、`github.com/xushixin/handoff/internal/proto`。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run TestHostGuard -v
```

Expected: 编译失败（`config.WebConfig` 未定义），修掉编译错误后 403 断言 FAIL（当前无白名单，返回 401/200）。

- [ ] **Step 3: 加配置项**

`internal/config/config.go`，在 `Config` 结构体里加一个字段（放在 `Env` 之后）：

```go
	// Web 是浏览器控制台相关配置。
	Web WebConfig
```

并在 `TerminalConfig` 附近加：

```go
// WebConfig 是浏览器控制台相关配置。
//
// AllowedHosts 是 Host 白名单的扩展项——回环地址（127.0.0.1 / localhost / ::1）
// 与 Listen 的 host 恒在白名单内，无需重复配置。它为将来的域名/中转场景预留：
// agentd 部署在 handoff.example.com 后面时，不配这一项所有请求都会被 403。
//
// yaml:"allowed_hosts"：strict 解码器（KnownFields）按 tag 匹配键名，
// 不加 tag 时 yaml.v3 会把它映射成 allowedhosts（同 RepoRoot 的处理）。
type WebConfig struct {
	AllowedHosts []string `yaml:"allowed_hosts"`
}
```

顺带在 `internal/config/config_test.go` 补一条严格解码用例：`web:\n  allowed_hosts:\n    - foo.example.com` 能解出一个元素。

- [ ] **Step 4: 实现中间件并接线**

创建 `internal/agentd/hostguard.go`：

```go
// 本文件实现 Host 白名单中间件：在鉴权**之前**拒绝 Host 头不在白名单内的请求。
//
// 职责：
//   - 取 r.Host 的 host 部分（忽略端口）与白名单比对，不匹配即 403
//   - 白名单 = 回环三件套 + cfg.Listen 的 host（通配地址除外）+ cfg.Web.AllowedHosts
//   - 拒绝时打 Warn 并记 Host 与来源地址——这是 DNS rebinding 攻击的唯一信号
//
// 边界：
//   - 不做任何身份判断（那是 auth 中间件的事）。本层先于鉴权执行正是为了不让
//     攻击者从「凭据对不对」的状态码差异里读出信息
//   - **挡不住本机恶意进程**：进程可以伪造任意 Host 头，那一层由凭据兜住。
//     两层各司其职，不要指望其中任何一层单独成立
package agentd

import (
	"net"
	"net/http"
	"strings"
)

// loopbackHosts 是恒定在白名单内的回环名称。
var loopbackHosts = []string{"127.0.0.1", "localhost", "::1"}

// hostGuard 是 Host 白名单中间件，必须包在 auth 之外（先于鉴权执行）。
//
// 参数：
//   - next: 被包住的处理器（通常是 auth 包好的整棵路由）
//
// 返回：
//   - 带白名单校验的处理器
//
// 注意：
//   - 白名单在构造时算一次：cfg 构造后只读，无需每请求重算
//   - 它同时把 coder/websocket 默认 Origin 校验的洞补上了：rebinding 的
//     `Host: evil.com` 在这一层就被挡下，根本到不了 websocket.Accept
func (s *Server) hostGuard(next http.Handler) http.Handler {
	allowed := s.allowedHosts()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := allowed[strings.ToLower(hostOnly(r.Host))]; !ok {
			s.log.Warn("Host 不在白名单，拒绝请求（DNS rebinding 的唯一信号）",
				"host", r.Host, "remote_addr", r.RemoteAddr,
				"method", r.Method, "path", r.URL.Path)
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "Host 不被允许"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowedHosts 计算白名单集合（小写归一）。
func (s *Server) allowedHosts() map[string]struct{} {
	out := make(map[string]struct{}, len(loopbackHosts)+len(s.cfg.Web.AllowedHosts)+1)
	for _, h := range loopbackHosts {
		out[h] = struct{}{}
	}
	// cfg.Listen 的 host：agentd 监听在 192.168.x.x 时，用该地址访问是正当的。
	// 通配地址除外——0.0.0.0 / :: 不是可用于访问的 Host，放进白名单没有意义，
	// 还会让「监听全网卡」意外变成「接受一个叫 0.0.0.0 的域名」。
	if h := strings.ToLower(hostOnly(s.cfg.Listen)); h != "" && h != "0.0.0.0" && h != "::" {
		out[h] = struct{}{}
	}
	for _, h := range s.cfg.Web.AllowedHosts {
		if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
			out[h] = struct{}{}
		}
	}
	return out
}

// hostOnly 取 host:port 中的 host 部分，并去掉 IPv6 字面量的方括号。
//
// 为什么不直接拿 r.Host 比对：端口不是安全边界。同一个 agentd 会被
// 127.0.0.1:7777 与 httptest 的随机端口访问，把端口算进白名单会让全部现有
// 测试与任意换端口的部署一起失效。
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(hostport, "[]")
}

// sortedKeys 取集合的有序键，让启动日志里的白名单顺序稳定可比。
func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
```

（import 需含 `net`、`net/http`、`sort`、`strings`。）

`internal/agentd/server.go` 的 `Handler()` 末行改为：

```go
	return s.hostGuard(s.auth(mux))
```

并把函数头注释里的「返回带 Bearer 鉴权中间件的完整路由」改成「返回带 Host 白名单 + 鉴权两层中间件的完整路由」。

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/agentd/ ./internal/config/ -v
```

Expected: 新测试 PASS，**既有测试无一失败**（httptest 的 Host 是 127.0.0.1，落在默认白名单内）。

- [ ] **Step 6: 加关键节点日志**

- 拒绝分支：`Warn`，字段 `host` / `remote_addr` / `method` / `path`（spec §11 第 5 条）。已在 Step 4 代码中。
- 放行分支**不打日志**：这是每个请求都会走的路径，打日志等于把访问日志塞进 stderr。
- 白名单本身在启动时打一条 `Info`，让「为什么我的域名被 403」可查。在 `Handler()` 里 `hostGuard` 之前加：
  ```go
  s.log.Info("Host 白名单已生效", "hosts", sortedKeys(s.allowedHosts()))
  ```
  `sortedKeys` 是本文件内的小辅助（`slices.Sorted(maps.Keys(m))`），保证日志顺序稳定。

- [ ] **Step 7: 加注释**

- 文件头：职责 + 边界，边界里必须写明「挡不住本机恶意进程」（已含）。
- `hostGuard` / `hostOnly` / `WebConfig` 的导出注释（已含）。
- 两处「为什么」：端口不参与判定；通配地址不进白名单。

- [ ] **Step 8: 提交**

```bash
git add internal/agentd/hostguard.go internal/agentd/hostguard_test.go internal/agentd/server.go internal/config/ && git commit -m "feat(agentd): Host 白名单中间件，堵住 DNS rebinding"
```

---

## Task 3: auth 中间件扩展为「Bearer 或 cookie」

**Files:**
- Create: `internal/agentd/auth.go`
- Create: `internal/agentd/auth_test.go`
- Modify: `internal/agentd/server.go:155-183`（`auth`）

**Interfaces:**
- Consumes: Task 1 的 `store.Session` / `store.HashCredential` / `SessionByTokenHash` / `TouchSession`
- Produces:
  - 常量 `sessionCookieName = "handoff_session"`、`sessionLifetime`、`ticketLifetime`、`lastSeenThrottle`、`defaultSessionRecheck`
  - `type identity struct { session string }`（`session` 非空=会话身份，空=主令牌身份）
  - `func identityFrom(ctx context.Context) identity`
  - `func (s *Server) sessionFromRequest(r *http.Request) (*store.Session, string)`
  - `func (s *Server) refreshSession(sess *store.Session)`

- [ ] **Step 1: 写失败的测试**

创建 `internal/agentd/auth_test.go`（白盒，`package agentd`）：

```go
// 浏览器鉴权测试：cookie 会话放行、失效原因区分、滑动续期节流。
//
// 边界：白盒测试（package agentd），因为要直接造会话行、读 Server 内部常量与字段。
package agentd

import (
	"net/http"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/config"
	"github.com/xushixin/handoff/internal/store"
)

// mustSession 直接在库里造一个会话，返回 cookie 明文。
func mustSession(t *testing.T, st *store.Store, id string, expiresAt time.Time, revoked bool) string {
	t.Helper()
	plain := "cookie-" + id
	now := time.Now()
	sess := &store.Session{
		ID: id, TokenHash: store.HashCredential(plain), DeviceName: "测试设备",
		CreatedAt: now, ExpiresAt: expiresAt, LastSeenAt: now,
	}
	if err := st.CreateSession(sess); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if revoked {
		if err := st.RevokeSession(id, now); err != nil {
			t.Fatalf("RevokeSession: %v", err)
		}
	}
	return plain
}

// getWithCookie 带 cookie 发一个 GET 请求。
func getWithCookie(t *testing.T, ts *httptest.Server, path, cookie string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatalf("构造请求: %v", err)
	}
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	}
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

// TestCookieSessionPassesAPI 钉死断言 8 的 /api 一半：cookie 能通过 API 路由。
func TestCookieSessionPassesAPI(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	cookie := mustSession(t, srv.st, "sess-ok", time.Now().Add(24*time.Hour), false)
	if resp := getWithCookie(t, ts, "/api/tasks", cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
}

// TestSessionFailureReasons 钉死断言 9/11 与 spec §11 的原因区分：
// 吊销、过期、不存在、无凭据必须落成四条不同原因的 Warn。
func TestSessionFailureReasons(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(t *testing.T, st *store.Store) string
		reason string
	}{
		{"已吊销", func(t *testing.T, st *store.Store) string {
			return mustSession(t, st, "sess-revoked", time.Now().Add(24*time.Hour), true)
		}, "会话已吊销"},
		{"已过期", func(t *testing.T, st *store.Store) string {
			return mustSession(t, st, "sess-expired", time.Now().Add(-time.Minute), false)
		}, "会话过期"},
		{"不存在", func(t *testing.T, st *store.Store) string { return "不存在的 cookie" }, "会话不存在"},
		{"无凭据", func(t *testing.T, st *store.Store) string { return "" }, "无凭据"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, ts, logs := newHostTestEnv(t, &config.Config{Token: hostTestToken})
			cookie := c.setup(t, srv.st)
			resp := getWithCookie(t, ts, "/api/tasks", cookie)
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("状态码 = %d，期望 401", resp.StatusCode)
			}
			if !strings.Contains(logs.String(), c.reason) {
				t.Errorf("鉴权失败日志缺少原因 %q，实际日志: %s", c.reason, logs.String())
			}
		})
	}
}

// TestSlidingRenewal 钉死断言 12：剩余寿命不足一半时，一次请求把 expires_at 推后。
func TestSlidingRenewal(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	nearExpiry := time.Now().Add(sessionLifetime/2 - time.Hour)
	cookie := mustSession(t, srv.st, "sess-renew", nearExpiry, false)
	if resp := getWithCookie(t, ts, "/api/tasks", cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	got, err := srv.st.SessionByID("sess-renew")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if !got.ExpiresAt.After(nearExpiry.Add(time.Hour)) {
		t.Fatalf("expires_at = %v，未被推后（原值 %v）", got.ExpiresAt, nearExpiry)
	}
}

// TestNoRenewalWhenFresh 钉死节流规则：寿命充足时一次请求**不写库**。
//
// 为什么要专门测「不写」：文件树、事件流、终端都是高频路由，每请求一次写会把
// SQLite 写成瓶颈——这条断言是那个性能约束的守门人。
func TestNoRenewalWhenFresh(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	fresh := time.Now().Add(sessionLifetime - time.Hour)
	cookie := mustSession(t, srv.st, "sess-fresh", fresh, false)
	before, err := srv.st.SessionByID("sess-fresh")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if resp := getWithCookie(t, ts, "/api/tasks", cookie); resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
	after, err := srv.st.SessionByID("sess-fresh")
	if err != nil {
		t.Fatalf("SessionByID: %v", err)
	}
	if !after.ExpiresAt.Equal(before.ExpiresAt) || !after.LastSeenAt.Equal(before.LastSeenAt) {
		t.Fatalf("寿命充足时不应写库：before=%+v after=%+v", before, after)
	}
}

// TestBearerStillWorks 钉死断言 1 的核心：主令牌路径不受影响，且身份为 CLI。
func TestBearerStillWorks(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+hostTestToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("发送请求: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("状态码 = %d，期望 200", resp.StatusCode)
	}
}

// TestEmptyConfigTokenStillRejectsCookie 钉死断言 2：cfg.Token 为空时
// **连合法 cookie 也拒**——fail-closed 的语义不能被新增的 cookie 路径旁路掉。
func TestEmptyConfigTokenStillRejectsCookie(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: ""})
	cookie := mustSession(t, srv.st, "sess-any", time.Now().Add(24*time.Hour), false)
	if resp := getWithCookie(t, ts, "/api/tasks", cookie); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", resp.StatusCode)
	}
}
```

（补齐 `httptest` / `strings` 的 import；`srv.st` 是 `Server` 的私有字段，白盒测试可直接读。）

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run 'TestCookieSession|TestSessionFailureReasons|TestSlidingRenewal|TestNoRenewalWhenFresh|TestEmptyConfigTokenStillRejectsCookie' -v
```

Expected: 编译失败（`sessionCookieName` 未定义）；补齐后 cookie 用例 FAIL（401）。

- [ ] **Step 3: 实现**

创建 `internal/agentd/auth.go`：

```go
// 本文件实现浏览器鉴权的中间件侧：cookie 会话的查找、有效性判定、滑动续期节流，
// 以及把身份传递给下游 handler 的 context 载体。
//
// 职责：
//   - 定义会话/ticket 的寿命常量与 cookie 名
//   - sessionFromRequest：按 cookie 查会话并判定有效性，同时给出失败原因
//   - refreshSession：按节流规则写回滑动续期与最后活跃时刻
//   - identity：一次请求的身份（主令牌 or 某个会话），供 auth 路由与 WS 复验使用
//
// 边界：
//   - 不签发任何凭据（那是 authroutes.go 的事）
//   - 不做 Host 校验（那是 hostguard.go 的事，且它先于本层执行）
//   - 不碰 TLS：Secure 属性的判据只能是 r.TLS，见 authroutes.go 的说明
package agentd

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/xushixin/handoff/internal/store"
)

const (
	// sessionCookieName 是浏览器会话 cookie 的名字。
	sessionCookieName = "handoff_session"
	// sessionLifetime 是会话的默认寿命，也是滑动续期的目标寿命。
	sessionLifetime = 30 * 24 * time.Hour
	// ticketLifetime 是一次性 ticket 的寿命。窗口刻意短：它只需要覆盖
	// 「CLI 拿到 URL → 浏览器完成一次跳转」这段时间。
	ticketLifetime = 60 * time.Second
	// lastSeenThrottle 是 last_seen_at 的写入节流阈值。它只用于展示，
	// 不参与任何鉴权判断，精度到分钟足够。
	lastSeenThrottle = 5 * time.Minute
	// defaultSessionRecheck 是 WS 连接上会话复验的周期（见 watchSession）。
	defaultSessionRecheck = 30 * time.Second
)

// identity 是一次请求通过鉴权后的身份。
//
// session 非空 = 由浏览器会话鉴权通过，值为会话 id；
// session 为空 = 由主令牌（Bearer，即 CLI）鉴权通过。
type identity struct {
	session string
}

// identityKey 是 identity 在 request context 中的键类型。
// 用私有空结构体做键，杜绝跨包键碰撞。
type identityKey struct{}

// withIdentity 返回携带身份的新 context。
func withIdentity(ctx context.Context, id identity) context.Context {
	return context.WithValue(ctx, identityKey{}, id)
}

// identityFrom 取出本次请求的身份；未经 auth 中间件的请求返回零值（=主令牌身份）。
//
// 注意：零值与「主令牌身份」不可区分是有意的——本函数的全部调用点都在 auth
// 中间件之内，不存在「没经过鉴权却调用它」的路径
func identityFrom(ctx context.Context) identity {
	id, _ := ctx.Value(identityKey{}).(identity)
	return id
}

// sessionFromRequest 用 cookie 查会话并判定其有效性。
//
// 参数：
//   - r: 当前请求
//
// 返回：
//   - 有效会话；无效时为 nil
//   - reason: 失败原因，用于鉴权失败日志。spec §11 要求区分「无凭据 / Bearer
//     不匹配 / 会话不存在 / 会话过期 / 会话已吊销」，因此不能只回一个 bool
func (s *Server) sessionFromRequest(r *http.Request) (*store.Session, string) {
	c, err := r.Cookie(sessionCookieName)
	if err != nil || c.Value == "" {
		// 走到这里说明 Bearer 分支也没过：带了 Authorization 头就是令牌不匹配，
		// 没带就是压根没给凭据。两者的排查方向完全不同（配对 token 未同步 vs
		// 有人在扫端口），必须分开记
		if _, ok := bearerToken(r); ok {
			return nil, "Bearer 不匹配"
		}
		return nil, "无凭据"
	}
	sess, err := s.st.SessionByTokenHash(store.HashCredential(c.Value))
	switch {
	case errors.Is(err, store.ErrNotFound):
		return nil, "会话不存在"
	case err != nil:
		s.log.Error("查询会话失败", "cause", err)
		return nil, "会话查询失败"
	case sess.RevokedAt != nil:
		return nil, "会话已吊销"
	case !time.Now().Before(sess.ExpiresAt):
		return nil, "会话过期"
	}
	return sess, ""
}

// refreshSession 按节流规则写回滑动续期与最后活跃时刻。
//
// 参数：
//   - sess: 刚刚通过鉴权的会话
//
// 注意：
//   - **必须节流**：文件树、事件流、终端这些高频路由若每个请求都写一次库，
//     会把 SQLite 写成瓶颈。续期只在剩余寿命不足一半时做（正常使用下每 15 天
//     最多一次），last_seen_at 只在与库中值相差超过 lastSeenThrottle 时写
//   - **写失败只 Warn、不影响放行**：会话是否有效在调用本方法前已经判完，
//     续期失败最坏结果是会话提前过期——属安全侧失败，不该把一次正常请求变成 500
func (s *Server) refreshSession(sess *store.Session) {
	now := time.Now()
	expires := sess.ExpiresAt
	if time.Until(expires) < sessionLifetime/2 {
		expires = now.Add(sessionLifetime)
	}
	lastSeen := sess.LastSeenAt
	if now.Sub(lastSeen) > lastSeenThrottle {
		lastSeen = now
	}
	if expires.Equal(sess.ExpiresAt) && lastSeen.Equal(sess.LastSeenAt) {
		return // 两项都没到写入阈值：本次请求不碰库
	}
	if err := s.st.TouchSession(sess.ID, lastSeen, expires); err != nil {
		s.log.Warn("会话续期写入失败（不影响本次请求放行）", "session", sess.ID, "cause", err)
	}
}
```

改写 `internal/agentd/server.go` 的 `auth`（函数头注释在原有 why 之后追加 cookie 分支的说明）：

```go
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.cfg.Token == "" {
			s.log.Error("token 未配置，拒绝一切请求（fail-closed）：请在配置中设置 token 后重启 agentd",
				"remote_addr", r.RemoteAddr, "method", r.Method, "path", r.URL.Path)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未授权"})
			return
		}
		// 先 Bearer：CLI 是最高频的调用方，且这条路径不碰库
		if token, ok := bearerToken(r); ok &&
			subtle.ConstantTimeCompare([]byte(token), []byte(s.cfg.Token)) == 1 {
			next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), identity{})))
			return
		}
		// 后 cookie：浏览器 new WebSocket() 设不了请求头，只能走这条
		sess, reason := s.sessionFromRequest(r)
		if sess == nil {
			s.log.Warn("鉴权失败", "remote_addr", r.RemoteAddr, "method", r.Method,
				"path", r.URL.Path, "reason", reason)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未授权"})
			return
		}
		s.refreshSession(sess)
		next.ServeHTTP(w, r.WithContext(withIdentity(r.Context(), identity{session: sess.ID})))
	})
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/agentd/ -v
```

Expected: 新旧测试全绿。

- [ ] **Step 5: 加关键节点日志**

- 鉴权失败：`Warn`，在既有字段基础上**新增 `reason`**（spec §11 第 4 条）。已在 Step 3 中。
- 会话查询出错：`Error` + `cause`（是真故障，与「凭据不对」不同级）。
- 续期写失败：`Warn` + `session` + `cause`，并在文案里写明「不影响本次请求放行」，否则排查者会以为请求失败了。
- 成功放行**不打日志**：每请求一条会淹掉一切。

- [ ] **Step 6: 加注释**

- 文件头：职责 + 边界（已含）。
- `identity` / `identityFrom` / `sessionFromRequest` / `refreshSession` 的导出级注释（已含）。
- 三处「为什么」：为什么 Bearer 在前；为什么失败原因不能压成 bool；为什么续期必须节流且失败不阻断。
- `server.go` 的 `auth` 函数头补一段：cookie 分支存在的原因是浏览器 `new WebSocket()` 设不了请求头，**并保留原有的 L-2 fail-closed why 段落一字不改**。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/auth.go internal/agentd/auth_test.go internal/agentd/server.go && git commit -m "feat(agentd): auth 中间件支持 cookie 会话（Bearer 优先，节流续期）"
```

---

## Task 4: auth 路由（签发 / 兑换 / 列出 / 吊销 / 登出）

**Files:**
- Create: `internal/agentd/authroutes.go`
- Create: `internal/proto/auth.go`
- Modify: `internal/agentd/auth_test.go`（追加全链路用例）
- Modify: `internal/agentd/server.go:133-153`（`Handler`）

**Interfaces:**
- Consumes: Task 1 的全部 store 方法；Task 3 的常量与 `identityFrom`
- Produces:
  - `type proto.AuthTicketResp struct { URL string \`json:"url"\`; ExpiresAt time.Time \`json:"expires_at"\` }`
  - `type proto.SessionInfo struct { ID, DeviceName string; CreatedAt, ExpiresAt, LastSeenAt time.Time; RevokedAt *time.Time }`（json tag 全部蛇形）
  - handler：`handleIssueTicket` / `handleConsole` / `handleListSessions` / `handleRevokeSession` / `handleLogout`
  - `func randCredential() (string, error)`、`func sanitizeDeviceName(s string) string`

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/auth_test.go` 追加：

```go
// issueTicket 用主令牌换一张 ticket URL。
func issueTicket(t *testing.T, ts *httptest.Server, device string) proto.AuthTicketResp {
	t.Helper()
	return issueTicketRaw(t, ts, `{"device_name":"`+device+`"}`)
}

// issueTicketRaw 同上，但直接给出请求体原文——用于构造含转义序列的设备名。
func issueTicketRaw(t *testing.T, ts *httptest.Server, rawBody string) proto.AuthTicketResp {
	t.Helper()
	body := strings.NewReader(rawBody)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/tickets", body)
	req.Header.Set("Authorization", "Bearer "+hostTestToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("签发 ticket: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("签发状态码 = %d，期望 200", resp.StatusCode)
	}
	var out proto.AuthTicketResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析签发响应: %v", err)
	}
	return out
}

// noRedirectClient 返回一个不自动跟随 302 的客户端（要断言 Set-Cookie 与 Location）。
func noRedirectClient(ts *httptest.Server) *http.Client {
	c := *ts.Client()
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &c
}

// TestTicketToCookieHappyPath 钉死断言 3：有效 ticket 换得 cookie 并 302 到 /。
func TestTicketToCookieHappyPath(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	tk := issueTicket(t, ts, "我的-mbp")
	resp, err := noRedirectClient(ts).Get(tk.URL)
	if err != nil {
		t.Fatalf("兑换 ticket: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("状态码 = %d，期望 302", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q，期望 /", loc)
	}
	var got *http.Cookie
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			got = c
		}
	}
	if got == nil {
		t.Fatal("没有下发会话 cookie")
	}
	if !got.HttpOnly || got.SameSite != http.SameSiteStrictMode || got.Path != "/" {
		t.Errorf("cookie 属性不对: %+v", got)
	}
	if got.Secure {
		t.Error("明文 loopback 下不得设置 Secure——会让 cookie 直接失效")
	}
	// 拿到的 cookie 必须真的能用（断言 8）
	if r2 := getWithCookie(t, ts, "/api/tasks", got.Value); r2.StatusCode != http.StatusOK {
		t.Fatalf("用新 cookie 访问 /api/tasks 得到 %d，期望 200", r2.StatusCode)
	}
}

// TestTicketSingleUseOverHTTP 钉死断言 4 的 HTTP 层：同一 URL 第二次访问失败。
func TestTicketSingleUseOverHTTP(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	tk := issueTicket(t, ts, "mbp")
	cl := noRedirectClient(ts)
	if resp, _ := cl.Get(tk.URL); resp.StatusCode != http.StatusFound {
		t.Fatalf("首次兑换状态码 = %d，期望 302", resp.StatusCode)
	}
	resp, err := cl.Get(tk.URL)
	if err != nil {
		t.Fatalf("二次兑换: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("二次兑换状态码 = %d，期望 401", resp.StatusCode)
	}
}

// TestExpiredTicketRejected 钉死断言 6：过期 ticket 兑换失败。
//
// 直接在库里造一张已过期的 ticket，而不是等 60 秒。
func TestExpiredTicketRejected(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	const plain = "过期票"
	past := time.Now().Add(-time.Minute)
	if err := srv.st.CreateAuthTicket(store.HashCredential(plain), "mbp", past.Add(-time.Minute), past); err != nil {
		t.Fatalf("CreateAuthTicket: %v", err)
	}
	resp, err := noRedirectClient(ts).Get(ts.URL + "/console?ticket=" + url.QueryEscape(plain))
	if err != nil {
		t.Fatalf("兑换: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("状态码 = %d，期望 401", resp.StatusCode)
	}
}

// TestSessionRoutesListRevokeLogout 钉死断言 9 与 17 的服务端一半。
func TestSessionRoutesListRevokeLogout(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	tk := issueTicket(t, ts, "手机")
	resp, _ := noRedirectClient(ts).Get(tk.URL)
	resp.Body.Close()
	var cookie string
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			cookie = c.Value
		}
	}

	list := listSessions(t, ts)
	if len(list) != 1 || !strings.Contains(list[0].DeviceName, "手机") {
		t.Fatalf("会话列表不对: %+v", list)
	}

	req, _ := http.NewRequest(http.MethodDelete, ts.URL+"/api/auth/sessions/"+list[0].ID, nil)
	req.Header.Set("Authorization", "Bearer "+hostTestToken)
	dresp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("吊销: %v", err)
	}
	dresp.Body.Close()
	if dresp.StatusCode != http.StatusOK {
		t.Fatalf("吊销状态码 = %d，期望 200", dresp.StatusCode)
	}
	// 断言 9：吊销后新请求立即 401
	if r := getWithCookie(t, ts, "/api/tasks", cookie); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("吊销后状态码 = %d，期望 401", r.StatusCode)
	}
	// 列表里仍能看到它，且带 revoked_at
	after := listSessions(t, ts)
	if len(after) != 1 || after[0].RevokedAt == nil {
		t.Fatalf("吊销后的列表不对: %+v", after)
	}
}

// TestSessionCannotIssueTicket 钉死：会话身份不得签发新 ticket。
//
// 为什么：会话代表「一台已授权设备」，让它签发 ticket 等于让一台丢失的手机
// 无限制地再造设备，吊销就失去了意义。
func TestSessionCannotIssueTicket(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	cookie := mustSession(t, srv.st, "sess-x", time.Now().Add(24*time.Hour), false)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/tickets", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: cookie})
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("请求: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("状态码 = %d，期望 403", resp.StatusCode)
	}
}

// TestDeviceNameSanitized 钉死 spec §6：设备名里的控制字符必须在入库前被剥掉，
// 否则一个构造过的 User-Agent 能往终端里注入 ANSI 转义序列。
func TestDeviceNameSanitized(t *testing.T) {
	_, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	// 用 JSON 的 \u001b 转义把 ESC 送进请求体，模拟一个构造过的 --device 或 User-Agent
	tk := issueTicketRaw(t, ts, `{"device_name":"\u001b[31m设备名"}`)
	resp, err := noRedirectClient(ts).Get(tk.URL)
	if err != nil {
		t.Fatalf("兑换: %v", err)
	}
	resp.Body.Close()
	list := listSessions(t, ts)
	if len(list) != 1 {
		t.Fatalf("会话条数 = %d，期望 1", len(list))
	}
	if strings.ContainsRune(list[0].DeviceName, '\x1b') {
		t.Fatalf("设备名残留 ESC 控制字符: %q", list[0].DeviceName)
	}
	if !strings.Contains(list[0].DeviceName, "设备名") {
		t.Errorf("净化把正常字符也吃掉了: %q", list[0].DeviceName)
	}
}

// listSessions 用主令牌列出会话。
func listSessions(t *testing.T, ts *httptest.Server) []proto.SessionInfo {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/auth/sessions", nil)
	req.Header.Set("Authorization", "Bearer "+hostTestToken)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("列出会话: %v", err)
	}
	defer resp.Body.Close()
	var out []proto.SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("解析会话列表: %v", err)
	}
	return out
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run 'TestTicket|TestExpiredTicket|TestSessionRoutes|TestSessionCannotIssue|TestDeviceName' -v
```

Expected: 404 / 编译失败。

- [ ] **Step 3: 定义线格式**

创建 `internal/proto/auth.go`：

```go
// 本文件定义浏览器鉴权相关接口的线格式：ticket 签发响应与会话展示条目。
//
// 职责：
//   - 作为 agentd 服务端与 internal/client 之间的单一契约来源
//
// 边界：
//   - 只有线格式，不含任何行为；凭据明文永远不出现在这里
//     （AuthTicketResp 只回 URL，会话 cookie 只经 Set-Cookie 下发）
package proto

import "time"

// AuthTicketResp 是 POST /api/auth/tickets 的响应。
//
// URL 是可直接打开的兑换地址（含一次性 ticket）；ExpiresAt 是该 ticket 的过期时刻。
type AuthTicketResp struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// SessionInfo 是 GET /api/auth/sessions 的单条会话。
//
// 注意：不含任何凭据字段——cookie 哈希都不给，展示与吊销只需要 id
type SessionInfo struct {
	ID         string     `json:"id"`
	DeviceName string     `json:"device_name"`
	CreatedAt  time.Time  `json:"created_at"`
	ExpiresAt  time.Time  `json:"expires_at"`
	LastSeenAt time.Time  `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
}
```

- [ ] **Step 4: 实现路由**

创建 `internal/agentd/authroutes.go`：

```go
// 本文件实现浏览器鉴权的五条路由：签发 ticket、兑换 cookie、列出会话、吊销会话、登出。
//
// 职责：
//   - POST /api/auth/tickets      主令牌签发一次性 ticket，返回可打开的 /console URL
//   - GET  /console?ticket=<t>    原子消费 ticket → Set-Cookie → 302 到 /
//   - GET  /api/auth/sessions     列出会话（含已吊销，供人判断）
//   - DELETE /api/auth/sessions/{id}  吊销指定会话
//   - POST /api/auth/logout       吊销当前 cookie 会话并清除 cookie
//
// 边界：
//   - **不托管前端**：/console 的 302 目标固定为 /，本轮 agentd 尚未 embed 任何页面，
//     / 返回 404 是预期结果。不得为了「让页面别 404」而塞占位首页
//   - 不判断 Host（hostguard.go 已在更外层做完），不做会话续期（auth.go 的事）
//   - 不读 X-Forwarded-Proto：cookie 的 Secure 与 URL 的 scheme 只按 r.TLS 判定，
//     因为上游可能是一台不可信中转，让它决定安全属性方向是反的
package agentd

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"github.com/xushixin/handoff/internal/proto"
	"github.com/xushixin/handoff/internal/store"
)

// deviceNameMaxRunes 是设备名的展示长度上限。
const deviceNameMaxRunes = 64

// randCredential 生成 256 位随机凭据明文（ticket 与会话 cookie 共用）。
//
// 返回：
//   - 64 字符十六进制串
//   - crypto/rand 读取失败时返回错误（不 panic：这是一条 HTTP 路径，
//     500 比让整个 agentd 崩掉合理）
func randCredential() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("生成随机凭据: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// sanitizeDeviceName 净化设备名：剥掉控制字符并按 rune 截断。
//
// 参数：
//   - s: 客户端提供的原始设备名（来自 --device 或 User-Agent）
//
// 返回：
//   - 可安全写入库、可安全打印到终端的展示名
//
// 注意：
//   - 设备名**纯展示，不参与任何鉴权判断**，但它来自客户端——一个构造过的
//     User-Agent 能往终端里注入 ANSI 转义序列，因此必须在入库这道边界剥掉
func sanitizeDeviceName(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\t' || unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	rs := []rune(strings.TrimSpace(cleaned))
	if len(rs) > deviceNameMaxRunes {
		rs = rs[:deviceNameMaxRunes]
	}
	return string(rs)
}

// consoleURL 用请求自身的 Host 拼出 /console 的兑换地址。
//
// 为什么用 r.Host 而不是 cfg.Listen：CLI 可能经 --target 访问一台远端 agentd，
// 也可能经 --agentd 指定别的端点。请求打到哪里，能兑换的就是哪里。
// scheme 只按 r.TLS 判定，不读 X-Forwarded-Proto（理由见文件头边界）。
func consoleURL(r *http.Request, ticket string) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/console?ticket=" + url.QueryEscape(ticket)
}

// handleIssueTicket 由主令牌签发一次性 ticket。
func (s *Server) handleIssueTicket(w http.ResponseWriter, r *http.Request) {
	if id := identityFrom(r.Context()); id.session != "" {
		// 会话代表「一台已授权设备」。让它签发 ticket 等于让一台丢失的手机
		// 无限制地再造设备，吊销就失去意义
		s.log.Warn("会话身份尝试签发 ticket，已拒绝", "session", id.session, "remote_addr", r.RemoteAddr)
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "签发 ticket 需要主令牌"})
		return
	}
	var req struct {
		DeviceName string `json:"device_name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		// 空 body 合法（设备名可缺省），因此 EOF 不算错误
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求体非法"})
		return
	}
	plain, err := randCredential()
	if err != nil {
		s.log.Error("签发 ticket 失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "签发失败"})
		return
	}
	now := time.Now()
	expires := now.Add(ticketLifetime)
	device := sanitizeDeviceName(req.DeviceName)
	if err := s.st.CreateAuthTicket(store.HashCredential(plain), device, now, expires); err != nil {
		s.log.Error("签发 ticket 失败", "device_name", device, "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "签发失败"})
		return
	}
	s.log.Info("签发 ticket", "device_name", device, "expires_at", expires)
	writeJSON(w, http.StatusOK, proto.AuthTicketResp{URL: consoleURL(r, plain), ExpiresAt: expires})
}

// handleConsole 兑换 ticket：原子消费 → 建会话 → Set-Cookie → 302 到 /。
//
// 这是唯一不经主令牌/cookie 的路由——ticket 本身就是它的凭据。
func (s *Server) handleConsole(w http.ResponseWriter, r *http.Request) {
	plain := r.URL.Query().Get("ticket")
	if plain == "" {
		s.log.Warn("消费 ticket 失败", "result", "缺少 ticket 参数", "remote_addr", r.RemoteAddr)
		s.writeTicketError(w)
		return
	}
	now := time.Now()
	device, expiresAt, err := s.st.ConsumeAuthTicket(store.HashCredential(plain), now)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.log.Warn("消费 ticket 失败", "result", "不存在或已消费", "remote_addr", r.RemoteAddr)
		s.writeTicketError(w)
		return
	case err != nil:
		s.log.Error("消费 ticket 出错", "remote_addr", r.RemoteAddr, "cause", err)
		s.writeTicketError(w)
		return
	case !now.Before(expiresAt):
		s.log.Warn("消费 ticket 失败", "result", "已过期", "expires_at", expiresAt, "remote_addr", r.RemoteAddr)
		s.writeTicketError(w)
		return
	}
	token, err := randCredential()
	if err != nil {
		s.log.Error("建立会话失败", "cause", err)
		s.writeTicketError(w)
		return
	}
	sess := &store.Session{
		ID:         uuid.NewString(),
		TokenHash:  store.HashCredential(token),
		DeviceName: sanitizeDeviceName(joinDevice(device, browserName(r.UserAgent()))),
		CreatedAt:  now,
		ExpiresAt:  now.Add(sessionLifetime),
		LastSeenAt: now,
	}
	if err := s.st.CreateSession(sess); err != nil {
		s.log.Error("建立会话失败", "device_name", sess.DeviceName, "cause", err)
		s.writeTicketError(w)
		return
	}
	s.log.Info("消费 ticket 成功", "result", "成功", "session", sess.ID)
	s.log.Info("会话建立", "session", sess.ID, "device_name", sess.DeviceName, "expires_at", sess.ExpiresAt)
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// 只按 r.TLS：明文 loopback 下设 Secure 会让 cookie 直接失效
		Secure: r.TLS != nil,
		MaxAge: int(time.Until(sess.ExpiresAt).Seconds()),
	})
	// 302 到 /：本轮不托管前端，/ 返回 404 是预期结果（cookie 此时已设好）
	http.Redirect(w, r, "/", http.StatusFound)
}

// writeTicketError 输出兑换失败的说明。
//
// 为什么是 text/plain 而不是一张 HTML 错误页：本轮 agentd 尚未托管任何前端，
// 纯文本既够用，又完全没有 HTML 注入面。
func (s *Server) writeTicketError(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	if _, err := io.WriteString(w, "这个链接已失效，请重新执行 handoff console\n"); err != nil {
		s.log.Warn("写出 ticket 失效说明失败", "err", err)
	}
}

// joinDevice 把登记设备名与浏览器名拼成展示名（任一为空时不留多余分隔符）。
func joinDevice(device, browser string) string {
	switch {
	case device == "":
		return browser
	case browser == "":
		return device
	}
	return device + " / " + browser
}

// browserName 从 User-Agent 里粗略识别浏览器名。
//
// 只做展示用途，因此刻意保持极简：识别不出就返回空串，绝不把整个 UA 串塞进
// 设备名（那既难看又是一条把攻击者可控长文本写进库的路径）。
// 注意顺序：Edge 与 Chrome 的 UA 都含 "Chrome"，Chrome 与 Safari 的都含 "Safari"。
func browserName(ua string) string {
	switch {
	case strings.Contains(ua, "Edg/"):
		return "Edge"
	case strings.Contains(ua, "Firefox/"):
		return "Firefox"
	case strings.Contains(ua, "Chrome/"):
		return "Chrome"
	case strings.Contains(ua, "Safari/"):
		return "Safari"
	}
	return ""
}

// handleListSessions 列出全部会话（Bearer 或 cookie 均可）。
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	sessions, err := s.st.ListSessions()
	if err != nil {
		s.log.Error("列出会话失败", "cause", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "列出会话失败"})
		return
	}
	out := make([]proto.SessionInfo, 0, len(sessions))
	for _, sess := range sessions {
		out = append(out, proto.SessionInfo{
			ID: sess.ID, DeviceName: sess.DeviceName, CreatedAt: sess.CreatedAt,
			ExpiresAt: sess.ExpiresAt, LastSeenAt: sess.LastSeenAt, RevokedAt: sess.RevokedAt,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRevokeSession 吊销指定会话（Bearer 或 cookie 均可）。
func (s *Server) handleRevokeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.revoke(r, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "会话不存在或已吊销"})
			return
		}
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "吊销失败"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// handleLogout 吊销当前 cookie 会话并清除 cookie。
//
// 只接受会话身份：CLI 用主令牌调它没有「当前会话」可言，属用法错误。
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	id := identityFrom(r.Context())
	if id.session == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "登出需要会话 cookie"})
		return
	}
	if err := s.revoke(r, id.session); err != nil && !errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "登出失败"})
		return
	}
	// MaxAge<0 让浏览器立即删除该 cookie；属性必须与下发时一致，否则删不掉
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookieName, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: r.TLS != nil, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// revoke 执行吊销并记录发起方，供 handleRevokeSession 与 handleLogout 共用。
func (s *Server) revoke(r *http.Request, id string) error {
	by := "主令牌"
	if cur := identityFrom(r.Context()); cur.session != "" {
		by = "会话 " + cur.session
	}
	if err := s.st.RevokeSession(id, time.Now()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.log.Warn("吊销会话未命中（不存在或已吊销）", "session", id, "by", by)
			return err
		}
		s.log.Error("吊销会话失败", "session", id, "by", by, "cause", err)
		return err
	}
	s.log.Info("吊销会话", "session", id, "by", by)
	return nil
}
```

`internal/agentd/server.go` 的 `Handler()` 改为（新增四条 API 路由 + 一个外层 mux 承载 `/console`）：

```go
	mux.HandleFunc("POST /api/auth/tickets", s.handleIssueTicket)
	mux.HandleFunc("GET /api/auth/sessions", s.handleListSessions)
	mux.HandleFunc("DELETE /api/auth/sessions/{id}", s.handleRevokeSession)
	mux.HandleFunc("POST /api/auth/logout", s.handleLogout)

	// /console 是唯一不经主令牌/cookie 的路由——ticket 本身就是它的凭据，
	// 因此它挂在 auth 之外、hostGuard 之内。Go 1.22 的 mux 按精确度选择，
	// "GET /console" 胜过 "/"
	root := http.NewServeMux()
	root.Handle("/", s.auth(mux))
	root.HandleFunc("GET /console", s.handleConsole)

	s.log.Info("Host 白名单已生效", "hosts", sortedKeys(s.allowedHosts()))
	return s.hostGuard(root)
```

同步更新 `Handler` 的路由清单注释（加 5 条新路由）。

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./internal/agentd/ ./internal/proto/ -v
```

Expected: 全绿。

- [ ] **Step 6: 加关键节点日志**

对齐 spec §11 逐条自检：

| 节点 | 级别 | 字段 | 位置 |
|---|---|---|---|
| 签发 ticket | Info | `device_name`、`expires_at`（**无明文**） | `handleIssueTicket` |
| 消费 ticket | Info/Warn | `result`（成功/已消费/已过期/不存在）、成功时 `session` | `handleConsole` |
| 会话建立 | Info | `session`、`device_name`、`expires_at` | `handleConsole` |
| 吊销 | Info | `session`、`by` | `revoke` |
| 会话签发 ticket 被拒 | Warn | `session`、`remote_addr` | `handleIssueTicket` |

**凭据纪律自检**：全文件搜索，确认 `plain` / `token` 变量从不出现在任何 `s.log.*` 调用的参数里。

- [ ] **Step 7: 加注释**

- 文件头：职责（五条路由）+ 边界（不托管前端 / 不判 Host / 不读 `X-Forwarded-Proto`）。已含。
- 每个 handler 的注释说明它的凭据要求。
- 五处「为什么」：为什么会话不能签发 ticket；为什么用 `r.Host` 拼 URL；为什么 `Secure` 只看 `r.TLS`；为什么失效说明用纯文本；为什么 `browserName` 只做极简识别。

- [ ] **Step 8: 提交**

```bash
git add internal/agentd/authroutes.go internal/agentd/auth_test.go internal/agentd/server.go internal/proto/auth.go && git commit -m "feat(agentd): ticket 签发/兑换与会话列出/吊销/登出路由"
```

---

## Task 5: WS 连接的周期性会话复验

**Files:**
- Modify: `internal/agentd/auth.go`（加 `watchSession`）
- Modify: `internal/agentd/server.go:63-99`（`Server` 加 `sessionRecheck` 字段、`NewServer` 赋默认值）、`server.go:1051` 附近（`handleEvents` 起 watcher）
- Modify: `internal/agentd/auth_test.go`（追加 WS 用例）

**Interfaces:**
- Consumes: Task 3 的 `identityFrom`、Task 1 的 `SessionByID`
- Produces: `func (s *Server) watchSession(ctx context.Context, conn *websocket.Conn, sessionID, taskID string)`；`Server.sessionRecheck time.Duration`（测试可注入）

- [ ] **Step 1: 写失败的测试**

在 `internal/agentd/auth_test.go` 追加：

```go
// TestWSKickedAfterRevoke 钉死断言 10：吊销后，已建立的 WS 在一个复验周期内被 1008 关闭。
//
// 注入毫秒级复验周期（同 replayLimit/liveLimit 的白盒注入手法），免等 30 秒。
func TestWSKickedAfterRevoke(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	srv.sessionRecheck = 20 * time.Millisecond
	taskID := mustWSTask(t, srv.st)
	cookie := mustSession(t, srv.st, "sess-ws", time.Now().Add(24*time.Hour), false)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(ts)+"/ws/events?task="+taskID, &websocket.DialOptions{
		HTTPHeader: http.Header{"Cookie": {sessionCookieName + "=" + cookie}},
	})
	if err != nil {
		t.Fatalf("cookie 形态的 WS 连接被拒: %v", err)
	}
	defer conn.CloseNow()

	if err := srv.st.RevokeSession("sess-ws", time.Now()); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	// 读到错误即被踢；断言 close 码是 1008（PolicyViolation）
	_, _, rerr := conn.Read(ctx)
	if rerr == nil {
		t.Fatal("吊销后连接仍可读，未被踢")
	}
	if websocket.CloseStatus(rerr) != websocket.StatusPolicyViolation {
		t.Fatalf("close 码 = %v，期望 1008", websocket.CloseStatus(rerr))
	}
}

// TestWSBearerNotWatched 钉死：Bearer（CLI）连接不做会话复验，不受任何吊销影响。
func TestWSBearerNotWatched(t *testing.T) {
	srv, ts, _ := newHostTestEnv(t, &config.Config{Token: hostTestToken})
	srv.sessionRecheck = 20 * time.Millisecond
	taskID := mustWSTask(t, srv.st)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, wsURL(ts)+"/ws/events?task="+taskID, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + hostTestToken}},
	})
	if err != nil {
		t.Fatalf("WS 连接被拒: %v", err)
	}
	defer conn.CloseNow()
	// 等若干个复验周期后连接仍应健在
	readCtx, readCancel := context.WithTimeout(ctx, 200*time.Millisecond)
	defer readCancel()
	_, _, rerr := conn.Read(readCtx)
	if websocket.CloseStatus(rerr) == websocket.StatusPolicyViolation {
		t.Fatal("Bearer 连接被会话复验误踢")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./internal/agentd/ -run TestWS -v
```

Expected: `TestWSKickedAfterRevoke` FAIL（吊销后连接仍在）。

- [ ] **Step 3: 实现**

`Server` 结构体新增字段（紧挨 `replayLimit` / `liveLimit`，并补一句同款注释）：

```go
	// sessionRecheck 是 WS 连接上会话复验的周期（defaultSessionRecheck 的实例副本），
	// 供测试注入毫秒级值验证「吊销后被踢」（生产恒为默认值）。
	sessionRecheck time.Duration
```

`NewServer` 里加 `sessionRecheck: defaultSessionRecheck,`。

`internal/agentd/auth.go` 追加：

```go
// watchSession 周期性复验 WS 连接背后的会话，失效即以 close code 1008 关闭连接。
//
// 参数：
//   - ctx: 连接生命周期上下文（conn.CloseRead 返回的那个），连接断开即取消
//   - conn: 要看护的 WS 连接
//   - sessionID / taskID: 仅用于日志定位
//
// 注意：
//   - **为什么不建「会话 id → 连接 cancel 函数」的中心注册表**：注册表漏登记、
//     漏清理都不会报错，只会表现为「吊销了但没断」或连接泄漏，两者都难以观察
//     （封存清单缺陷 #4 的教训）。周期性复验是自愈的——查询失败就关连接，
//     fail-closed，最坏情况只是一个周期的滞后
//   - 只对会话身份的连接启动；Bearer（CLI）连接不受影响
func (s *Server) watchSession(ctx context.Context, conn *websocket.Conn, sessionID, taskID string) {
	t := time.NewTicker(s.sessionRecheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return // 连接已断开，复验随之结束
		case <-t.C:
			sess, err := s.st.SessionByID(sessionID)
			switch {
			case err == nil && sess.RevokedAt == nil && time.Now().Before(sess.ExpiresAt):
				continue // 仍然有效
			case err != nil && !errors.Is(err, store.ErrNotFound):
				// 查询失败也断开：fail-closed。事件全部落库，客户端凭 cursor
				// 重连即可完整补拉，断开是无损的
				s.log.Error("WS 会话复验失败，断开连接（fail-closed）",
					"session", sessionID, "task", taskID, "cause", err)
			default:
				s.log.Info("WS 会话已失效，断开在线连接", "session", sessionID, "task", taskID)
			}
			if cerr := conn.Close(websocket.StatusPolicyViolation, "session revoked"); cerr != nil {
				s.log.Warn("WS 关闭失效会话连接失败", "session", sessionID, "task", taskID, "err", cerr)
			}
			return
		}
	}
}
```

（`auth.go` 的 import 补 `github.com/coder/websocket`。）

`handleEvents` 在 `ctx := conn.CloseRead(r.Context())` 之后、`s.hub.Subscribe` 之前插入：

```go
	// 会话身份的连接必须周期性复验：Hub 只按 taskID 路由、不持有会话身份，
	// 吊销一个会话不会自动断开它已经建立的 WS，而手机丢失场景下
	// 「吊销了但还连着」不可接受。Bearer（CLI）连接不受影响
	if id := identityFrom(r.Context()); id.session != "" {
		go s.watchSession(ctx, conn, id.session, taskID)
	}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./internal/agentd/ -race -v
```

Expected: 全绿，且 `-race` 无告警（watcher 与写循环并发操作同一个 conn，`coder/websocket` 的 `Close` 是并发安全的，但仍须用 `-race` 验一遍）。

- [ ] **Step 5: 加关键节点日志**

- 会话失效被踢：`Info` + `session` + `task`（spec §11 第 7 条——缺了它「吊销了但手机还连着」将无从排查）。
- 复验查询失败：`Error` + `cause`，文案里写明是 fail-closed。
- `Close` 失败：`Warn`（沿用既有「WS 关闭任务不存在连接失败」的写法）。
- 每次复验通过**不打日志**：30 秒一条 × 每连接，会把日志刷成噪音。

- [ ] **Step 6: 加注释**

- `watchSession` 的完整导出级注释，含「为什么不建中心注册表」（已含）。
- `handleEvents` 插入点的「为什么」注释（已含）。
- `Server.sessionRecheck` 字段注释说明它是测试注入点。

- [ ] **Step 7: 提交**

```bash
git add internal/agentd/auth.go internal/agentd/auth_test.go internal/agentd/server.go && git commit -m "feat(agentd): WS 周期性会话复验，吊销后踢掉在线连接"
```

---

## Task 6: client 方法 + `handoff console`

**Files:**
- Modify: `internal/client/client.go`（追加三个方法）
- Create: `cmd/console.go`
- Create: `cmd/console_test.go`

**Interfaces:**
- Consumes: `proto.AuthTicketResp` / `proto.SessionInfo`、`Client.do` / `Client.httpError`、`cmd.TargetEndpoint`
- Produces:
  - `func (c *Client) IssueAuthTicket(ctx context.Context, deviceName string) (*proto.AuthTicketResp, error)`
  - `func (c *Client) ListSessions(ctx context.Context) ([]proto.SessionInfo, error)`
  - `func (c *Client) RevokeSession(ctx context.Context, id string) error`
  - `handoff console [--print-url] [--device <name>] [--no-open]`
  - `func openBrowser(rawURL string) error`（`cmd` 包内）

- [ ] **Step 1: 写失败的测试**

创建 `cmd/console_test.go`：

```go
// handoff console 测试：--print-url 的输出契约（桌面壳靠它接线）与 agentd 未运行时的报错。
package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestConsolePrintURLOutputContract 钉死断言 16 与桌面壳的接线契约：
// stdout 恰好是一行可用 URL，没有任何其他噪音——壳会直接把它交给 loadURL。
func TestConsolePrintURLOutputContract(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/tickets" {
			t.Errorf("非预期路径: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer 测试令牌" {
			t.Errorf("Authorization = %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		// 用 r.Host 拼，和真 agentd 的 consoleURL 一致
		json.NewEncoder(w).Encode(map[string]any{
			"url":        "http://" + r.Host + "/console?ticket=abc",
			"expires_at": "2026-08-11T00:00:00Z",
		})
	}))
	defer ts.Close()

	var stdout bytes.Buffer
	runConsoleForTest(t, &stdout, ts.URL, "测试令牌", []string{"--print-url"})

	lines := strings.Split(strings.TrimRight(stdout.String(), "\n"), "\n")
	if len(lines) != 1 {
		t.Fatalf("stdout 行数 = %d，期望恰好 1 行（壳直接消费这一行）: %q", len(lines), stdout.String())
	}
	if !strings.HasPrefix(lines[0], "http") || !strings.Contains(lines[0], "/console?ticket=") {
		t.Fatalf("stdout 不是可用 URL: %q", lines[0])
	}
}

// TestConsoleAgentdNotRunning 钉死断言 18：agentd 未运行时明确报错，不退化成超时。
func TestConsoleAgentdNotRunning(t *testing.T) {
	// 先起一个 httptest 再立刻关掉，拿到一个确定没人监听的地址
	ts := httptest.NewServer(http.NotFoundHandler())
	dead := ts.URL
	ts.Close()

	var stdout bytes.Buffer
	err := runConsoleForTest(t, &stdout, dead, "测试令牌", []string{"--print-url"})
	if err == nil {
		t.Fatal("agentd 未运行时应报错")
	}
	if !strings.Contains(err.Error(), "连接 agentd") {
		t.Fatalf("报错文案未点明连不上 agentd: %v", err)
	}
	if stdout.Len() != 0 {
		t.Errorf("失败时 stdout 应为空，实际: %q", stdout.String())
	}
}
```

在同一文件里补上 `runConsoleForTest`（沿用 `cmd/dispatch_test.go:24` 的 `runDispatch` 骨架，其中 `writeTestConfig` / `resetFlags` / `testToken` 均定义在 `cmd/root_test.go:25-39`，直接复用）：

```go
// runConsoleForTest 以给定 flags 执行 console 子命令，把 stdout 写进 stdout 参数。
//
// addr 是 fake agentd 的 http:// 地址；token 写进临时配置，让 TargetEndpoint 取到它。
func runConsoleForTest(t *testing.T, stdout *bytes.Buffer, addr, token string, extraArgs []string) error {
	t.Helper()
	cfgPath := writeTestConfig(t,
		"listen: \""+strings.TrimPrefix(addr, "http://")+"\"\ntoken: \""+token+"\"\n")
	resetFlags(t)
	targetName = ""
	configPath = cfgPath
	// 与 runDispatch 一致：清掉 --agentd 的 Changed 标记，让地址取自配置的 listen
	rootCmd.PersistentFlags().Lookup("agentd").Changed = false

	rootCmd.SetArgs(append([]string{"console"}, extraArgs...))
	rootCmd.SetOut(stdout)
	var errBuf bytes.Buffer
	rootCmd.SetErr(&errBuf)
	t.Cleanup(func() {
		rootCmd.SetArgs(nil)
		rootCmd.SetOut(nil)
		rootCmd.SetErr(nil)
	})
	return Execute()
}
```

`resetFlags` 不会重置本 task 新加的三个 `console` flag（它们是包级变量，跨用例会残留）。因此在 `console.go` 的 `init()` 之外，`runConsoleForTest` 开头再补一句显式复位：

```go
	consolePrintURL, consoleDevice, consoleNoOpen = false, "", false
```

漏了这句，`TestConsoleAgentdNotRunning` 会因为上一个用例留下的 `--print-url` 而看似通过——**这类跨用例污染是最难查的假绿**。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./cmd/ -run TestConsole -v
```

Expected: `unknown command "console"`。

- [ ] **Step 3: 实现 client 方法**

在 `internal/client/client.go` 追加：

```go
// IssueAuthTicket 请求 agentd 签发一次性 ticket，返回可直接打开的兑换 URL。
//
// 参数：
//   - deviceName: 设备展示名，纯展示，服务端会净化控制字符
//
// 返回：
//   - 兑换 URL 与过期时刻
//   - 连不上 agentd 时返回带诊断提示的错误（不退化成一句裸的 dial 失败）
func (c *Client) IssueAuthTicket(ctx context.Context, deviceName string) (*proto.AuthTicketResp, error) {
	resp, err := c.do(ctx, http.MethodPost, "/api/auth/tickets",
		map[string]string{"device_name": deviceName})
	if err != nil {
		return nil, fmt.Errorf("连接 agentd %s 失败（它在运行吗？可先执行 handoff status 确认）: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("签发 ticket", resp)
	}
	var out proto.AuthTicketResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析签发响应: %w", err)
	}
	return &out, nil
}

// ListSessions 列出 agentd 上的全部浏览器会话（含已吊销）。
func (c *Client) ListSessions(ctx context.Context) ([]proto.SessionInfo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/api/auth/sessions", nil)
	if err != nil {
		return nil, fmt.Errorf("连接 agentd %s 失败（它在运行吗？可先执行 handoff status 确认）: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, c.httpError("列出会话", resp)
	}
	var out []proto.SessionInfo
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("解析会话列表: %w", err)
	}
	return out, nil
}

// RevokeSession 吊销指定会话。
//
// 返回：
//   - 404 时错误里含服务端原文「会话不存在或已吊销」
func (c *Client) RevokeSession(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/api/auth/sessions/"+url.PathEscape(id), nil)
	if err != nil {
		return fmt.Errorf("连接 agentd %s 失败（它在运行吗？可先执行 handoff status 确认）: %w", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return c.httpError("吊销会话", resp)
	}
	return nil
}
```

- [ ] **Step 4: 实现 `handoff console`**

创建 `cmd/console.go`：

```go
// 本文件实现 handoff console 子命令：用主令牌换一张一次性 ticket，
// 并把兑换 URL 交给系统浏览器（或打印出来给桌面壳用）。
//
// 职责：
//   - 调 client.IssueAuthTicket 取兑换 URL
//   - 默认调系统浏览器打开；--print-url 只打印（这是桌面壳的接线点）
//   - 设备名缺省取本机主机名（CLI 没有 User-Agent 可推断）
//
// 边界：
//   - 不实现任何鉴权逻辑：凭据的签发与校验全在 agentd 侧
//   - 不管前端是否存在：本轮 agentd 尚未托管页面，兑换成功后落在 404 是预期的，
//     cookie 已经设好
//   - --target 可用，但那是**诊断入口**不是产品路径（产品路径是「只连本机
//     agentd，由它向远端转发」），不要因为它好用就当成跨机方案
package cmd

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
)

var (
	consolePrintURL bool
	consoleDevice   string
	consoleNoOpen   bool
)

// consoleCmd 打开浏览器控制台。
var consoleCmd = &cobra.Command{
	Use:   "console",
	Short: "在浏览器中打开 agentd 控制台（换一次性 ticket 并兑换会话）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		device := consoleDevice
		if device == "" {
			// CLI 没有 User-Agent 可推断，用主机名作缺省展示名；
			// 取不到主机名时留空，由服务端补浏览器名
			device, _ = os.Hostname()
		}
		tk, err := client.New(addr, token).IssueAuthTicket(cmd.Context(), device)
		if err != nil {
			return err
		}
		if consolePrintURL || consoleNoOpen {
			// 只打 URL，一行，无任何前后缀：桌面壳直接把这一行交给 loadURL
			fmt.Fprintln(cmd.OutOrStdout(), tk.URL)
			return nil
		}
		if oerr := openBrowser(tk.URL); oerr != nil {
			// 打不开浏览器不是失败：把 URL 打出来，用户自己粘贴即可，
			// 而 ticket 只有 60 秒，静默失败会让人完全摸不着头脑
			fmt.Fprintf(cmd.ErrOrStderr(), "打开浏览器失败（%v），请手动打开下面的地址（60 秒内有效）：\n", oerr)
			fmt.Fprintln(cmd.OutOrStdout(), tk.URL)
		}
		return nil
	},
}

func init() {
	consoleCmd.Flags().BoolVar(&consolePrintURL, "print-url", false, "只打印兑换 URL，不打开浏览器（桌面壳用）")
	consoleCmd.Flags().StringVar(&consoleDevice, "device", "", "设备展示名（缺省取本机主机名）")
	consoleCmd.Flags().BoolVar(&consoleNoOpen, "no-open", false, "不打开浏览器（等价于 --print-url）")
	rootCmd.AddCommand(consoleCmd)
}

// openBrowser 用系统默认方式打开一个 URL。
//
// 注意：各平台命令不同；不支持的平台返回错误，由调用方降级为打印 URL
func openBrowser(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, truncateBytes(string(out), 200))
	}
	return nil
}
```

（`truncateBytes` 已存在于 `cmd/wait.go`，直接复用。）

- [ ] **Step 5: 跑测试确认通过**

```bash
go test ./cmd/ ./internal/client/ -v
```

Expected: 全绿。

- [ ] **Step 6: 加关键节点日志**

CLI 的「日志」是给人看的 stderr 输出，纪律与服务端不同但同样不能静默：

- 成功且 `--print-url`：stdout 一行 URL，stderr 不输出任何东西（**输出契约由测试钉死**）。
- 成功且打开浏览器：stderr 打一行 `已在浏览器中打开控制台（链接 60 秒内有效）`，否则用户看不出发生了什么。
- 打开浏览器失败：stderr 说明原因 + stdout 给出 URL（已含）。
- 连不上 agentd：错误里点名地址与「它在运行吗」（在 client 侧，已含）。
- `internal/client` 侧的三个方法沿用既有 `httpError` 的分级日志，不额外加。

- [ ] **Step 7: 加注释**

- `cmd/console.go` 文件头：职责 + 边界（含 `--target` 是诊断入口这条）。已含。
- `consoleCmd` / `openBrowser` 与三个 client 方法的导出注释。已含。
- 两处「为什么」：为什么设备名缺省取主机名；为什么打不开浏览器要降级打印而不是报错退出。

- [ ] **Step 8: 提交**

```bash
git add internal/client/client.go cmd/console.go cmd/console_test.go && git commit -m "feat(cli): handoff console —— 换 ticket 并打开浏览器控制台"
```

---

## Task 7: `handoff sessions` / `handoff sessions revoke`

**Files:**
- Create: `cmd/sessions.go`
- Create: `cmd/sessions_test.go`
- Modify: `cmd/console_test.go`（把 `runConsoleForTest` 重构成通用的 `runSubcommandForTest`）

**Interfaces:**
- Consumes: Task 6 的 `client.ListSessions` / `client.RevokeSession`、`proto.SessionInfo`
- Produces: `handoff sessions`、`handoff sessions revoke <session-id>`；`func renderSessions(w io.Writer, list []proto.SessionInfo)`、`func displayName(s string) string`

- [ ] **Step 1: 写失败的测试**

创建 `cmd/sessions_test.go`：

```go
// handoff sessions 测试：渲染、吊销、以及设备名的终端注入防护。
package cmd

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/xushixin/handoff/internal/proto"
)

// TestRenderSessionsStripsControlChars 钉死 spec §6：设备名里的 ANSI 转义序列
// 绝不能原样打到终端上——服务端已经净化过一道，但 CLI 不能假设对端是新版 agentd。
func TestRenderSessionsStripsControlChars(t *testing.T) {
	now := time.Now()
	var buf bytes.Buffer
	renderSessions(&buf, []proto.SessionInfo{{
		ID: "sess-1", DeviceName: "设备\x1b[31m名\x07",
		CreatedAt: now, ExpiresAt: now.Add(time.Hour), LastSeenAt: now,
	}})
	out := buf.String()
	if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\x07') {
		t.Fatalf("输出里残留控制字符: %q", out)
	}
	if !strings.Contains(out, "sess-1") {
		t.Errorf("输出缺少会话 id: %q", out)
	}
}

// TestRenderSessionsMarksRevoked 钉死：已吊销的会话要显式标出，而不是看起来正常。
func TestRenderSessionsMarksRevoked(t *testing.T) {
	now := time.Now()
	revoked := now.Add(-time.Minute)
	var buf bytes.Buffer
	renderSessions(&buf, []proto.SessionInfo{{
		ID: "sess-1", DeviceName: "手机", CreatedAt: now,
		ExpiresAt: now.Add(time.Hour), LastSeenAt: now, RevokedAt: &revoked,
	}})
	if !strings.Contains(buf.String(), "已吊销") {
		t.Fatalf("已吊销的会话未标出: %q", buf.String())
	}
}

// TestRenderSessionsEmpty 钉死：空列表要说人话，不是打一片空白。
func TestRenderSessionsEmpty(t *testing.T) {
	var buf bytes.Buffer
	renderSessions(&buf, nil)
	if !strings.Contains(buf.String(), "没有") {
		t.Fatalf("空列表输出不友好: %q", buf.String())
	}
}
```

再补一条端到端用例，验证 CLI 真的发出了正确的请求：

```go
// TestSessionsEndToEnd 验证 sessions 列出与 revoke 发出的实际 HTTP 请求。
func TestSessionsEndToEnd(t *testing.T) {
	var gotMethod, gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet {
			fmt.Fprint(w, `[{"id":"sess-1","device_name":"手机","created_at":"2026-08-11T00:00:00Z",`+
				`"expires_at":"2036-08-11T00:00:00Z","last_seen_at":"2026-08-11T00:00:00Z","revoked_at":null}]`)
			return
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer ts.Close()

	var out bytes.Buffer
	if err := runSubcommandForTest(t, &out, ts.URL, testToken, []string{"sessions"}); err != nil {
		t.Fatalf("sessions: %v", err)
	}
	if gotMethod != http.MethodGet || gotPath != "/api/auth/sessions" {
		t.Fatalf("列出请求 = %s %s", gotMethod, gotPath)
	}
	if !strings.Contains(out.String(), "sess-1") || !strings.Contains(out.String(), "手机") {
		t.Fatalf("列表输出不含会话: %q", out.String())
	}

	out.Reset()
	if err := runSubcommandForTest(t, &out, ts.URL, testToken, []string{"sessions", "revoke", "sess-1"}); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if gotMethod != http.MethodDelete || gotPath != "/api/auth/sessions/sess-1" {
		t.Fatalf("吊销请求 = %s %s，期望 DELETE /api/auth/sessions/sess-1", gotMethod, gotPath)
	}
	if !strings.Contains(out.String(), "已吊销会话 sess-1") {
		t.Errorf("吊销成功未回显: %q", out.String())
	}
}
```

`runSubcommandForTest` 与 Task 6 的 `runConsoleForTest` 是同一个骨架，只是子命令由参数给出。**本 task 顺手把 Task 6 的 `runConsoleForTest` 重构成 `runSubcommandForTest(t, stdout, addr, token, args)`**（`console` 用例改为传 `append([]string{"console"}, ...)`），避免两份几乎相同的辅助。

- [ ] **Step 2: 跑测试确认失败**

```bash
go test ./cmd/ -run TestRenderSessions -v
```

Expected: `undefined: renderSessions`。

- [ ] **Step 3: 实现**

创建 `cmd/sessions.go`：

```go
// 本文件实现 handoff sessions 子命令族：列出与吊销浏览器会话。
//
// 职责：
//   - sessions：列出全部会话（含已吊销，显式标注）
//   - sessions revoke <id>：吊销指定会话
//   - 渲染前净化设备名：它来自客户端，可能含 ANSI 转义序列
//
// 边界：
//   - 不吊销主令牌：主令牌不可吊销（换它等于全部重配），本命令只管会话
//   - 不做交互确认：吊销一个会话是可恢复的（重新 handoff console 即可），
//     不值得一道确认门
package cmd

import (
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/spf13/cobra"
	"github.com/xushixin/handoff/internal/client"
	"github.com/xushixin/handoff/internal/proto"
)

// sessionsCmd 列出浏览器会话。
var sessionsCmd = &cobra.Command{
	Use:   "sessions",
	Short: "列出浏览器会话（handoff console 建立的登录态）",
	RunE: func(cmd *cobra.Command, _ []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		list, err := client.New(addr, token).ListSessions(cmd.Context())
		if err != nil {
			return err
		}
		renderSessions(cmd.OutOrStdout(), list)
		return nil
	},
}

// sessionsRevokeCmd 吊销一个会话。
var sessionsRevokeCmd = &cobra.Command{
	Use:   "revoke <session-id>",
	Short: "吊销指定的浏览器会话（手机丢失时用它，不必换主令牌）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		addr, token, err := TargetEndpoint()
		if err != nil {
			return err
		}
		if err := client.New(addr, token).RevokeSession(cmd.Context(), args[0]); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "已吊销会话 %s\n", args[0])
		return nil
	},
}

func init() {
	sessionsCmd.AddCommand(sessionsRevokeCmd)
	rootCmd.AddCommand(sessionsCmd)
}

// renderSessions 渲染会话列表。
//
// 参数：
//   - w: 输出目标
//   - list: 会话列表（可为空）
func renderSessions(w io.Writer, list []proto.SessionInfo) {
	if len(list) == 0 {
		fmt.Fprintln(w, "没有浏览器会话。执行 handoff console 建立一个。")
		return
	}
	for _, s := range list {
		state := "有效"
		switch {
		case s.RevokedAt != nil:
			state = "已吊销"
		case !time.Now().Before(s.ExpiresAt):
			state = "已过期"
		}
		fmt.Fprintf(w, "%s  %s  %s  最后活跃 %s\n",
			s.ID, displayName(s.DeviceName), state, s.LastSeenAt.Local().Format("01-02 15:04"))
	}
}

// displayName 净化设备名后再打到终端。
//
// 为什么 CLI 也要净化（服务端已经净化过一道）：CLI 可能连的是一台**旧版**
// agentd，那边没有这道处理。展示层不能假设对端一定是新版——一个构造过的
// User-Agent 能往终端里注入 ANSI 转义序列。
func displayName(s string) string {
	cleaned := strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	if cleaned == "" {
		return "（未命名设备）"
	}
	rs := []rune(cleaned)
	if len(rs) > 40 {
		return string(rs[:40]) + "…"
	}
	return cleaned
}
```

- [ ] **Step 4: 跑测试确认通过**

```bash
go test ./cmd/ -v
```

Expected: 全绿。

- [ ] **Step 5: 加关键节点日志**

- 吊销成功：stdout 明确回一句 `已吊销会话 <id>`（成功路径不静默）。
- 吊销未命中：错误由 client 的 `httpError` 带出服务端原文「会话不存在或已吊销」，CLI 不再包一层含糊话。
- 列表为空：明确说「没有浏览器会话」并给出下一步，而不是空白。

- [ ] **Step 6: 加注释**

- 文件头：职责 + 边界（不吊销主令牌 / 不做交互确认）。已含。
- `renderSessions` / `displayName` 的注释，含「为什么 CLI 也要净化」。已含。

- [ ] **Step 7: 提交**

```bash
git add cmd/sessions.go cmd/sessions_test.go && git commit -m "feat(cli): handoff sessions 列出与吊销浏览器会话"
```

---

## Task 8: 文档 —— 用法与桌面壳接线契约

**Files:**
- Modify: `README.md`

**Interfaces:**
- Consumes: 前七个 task 的全部对外行为
- Produces: 无代码产物；产出的是「桌面壳该怎么接」的书面契约

- [ ] **Step 1: 写 README 小节**

在 README 的 CLI 命令一节之后插入「浏览器控制台」小节，内容必须包含：

````markdown
### 浏览器控制台

```bash
handoff console                 # 打开系统浏览器（自动换一次性 ticket）
handoff console --print-url     # 只打印兑换 URL，不打开浏览器
handoff sessions                # 列出已建立的浏览器会话
handoff sessions revoke <id>    # 吊销一个会话（手机丢失时用它）
```

**机制**：`console` 用主令牌向 agentd 换一张 **60 秒、一次性**的 ticket，
浏览器打开该 URL 后 agentd 原子消费它，下发一个 httpOnly cookie 会话（默认 30 天，
滑动续期），此后 `/api` 与 `/ws` 全部路由都用这个 cookie。

**长期凭据永远不进 URL**——URL 里只有那张一次性 ticket。

**Host 白名单**：agentd 只接受 Host 为 `127.0.0.1` / `localhost` / `::1` /
配置的 `listen` 地址的请求。放到域名后面时必须配：

```yaml
web:
  allowed_hosts:
    - handoff.example.com
```

不配的表现是**全部请求 403**，agentd 日志里有 `Host 不在白名单`。

**桌面壳接线契约**（壳内零凭据逻辑）：

1. 探测本机 agentd 是否在监听；
2. 执行 `handoff console --print-url`，**stdout 恰好一行，就是 URL**；
3. `loadURL(那一行)`。

壳不读 `config.yaml`、不碰主令牌、不实现任何鉴权代码。会话过期时页面返回 401，
壳重跑第 2、3 步即可，用户无感。
````

同时在 README 的配置示例里补上 `web.allowed_hosts` 段。

- [ ] **Step 2: 核对文档与实现一致**

逐条对照本计划 Task 2/4/6 的实际实现，确认 README 里写的路由名、flag 名、配置键名、默认值、日志文案都能在代码里搜到。**任何对不上的，改文档而不是改记忆。**

- [ ] **Step 3: 提交**

```bash
git add README.md && git commit -m "docs: README 补浏览器控制台用法与桌面壳接线契约"
```

---

## 验收：spec §12 的 18 条断言归位表

实现完成后逐条核对，每条都要指得出具体的测试函数：

| # | 断言 | 落点 |
|---|---|---|
| 1 | 现有 Bearer 路径全部照常 | 既有全部测试 + `TestBearerStillWorks`（Task 3） |
| 2 | `cfg.Token == ""` 仍拒绝一切 | `TestEmptyConfigTokenStillRejectsCookie`（Task 3） |
| 3 | 有效 ticket 换得 cookie 并 302 到 `/` | `TestTicketToCookieHappyPath`（Task 4） |
| 4 | 同一 ticket 第二次失败 | `TestConsumeAuthTicketOnce`（Task 1）+ `TestTicketSingleUseOverHTTP`（Task 4） |
| 5 | 并发消费恰好一个成功 | `TestConsumeAuthTicketConcurrent`（Task 1） |
| 6 | 过期 ticket 失败 | `TestExpiredTicketRejected`（Task 4） |
| 7 | ticket 明文不落库 | `TestAuthTicketPlaintextNotStored`（Task 1） |
| 8 | cookie 通过 `/api` 与 `/ws` | `TestCookieSessionPassesAPI`（Task 3）+ `TestWSKickedAfterRevoke` 的建连段（Task 5） |
| 9 | 吊销后新请求立即 401 | `TestSessionRoutesListRevokeLogout`（Task 4） |
| 10 | 吊销后已建立的 WS 被 1008 关闭 | `TestWSKickedAfterRevoke`（Task 5） |
| 11 | 会话过期后 401 | `TestSessionFailureReasons/已过期`（Task 3） |
| 12 | 滑动续期生效 | `TestSlidingRenewal` + `TestNoRenewalWhenFresh`（Task 3） |
| 13 | 伪造 Host → 403 且先于鉴权 | `TestHostGuardRejectsForeignHostBeforeAuth`（Task 2） |
| 14 | rebinding 回归（Host==Origin） | `TestHostGuardDNSRebindingRegression`（Task 2） |
| 15 | 无 Origin 的 Bearer 客户端仍能连 | `TestNonBrowserBearerClientStillConnects`（Task 2） |
| 16 | `--print-url` 输出可用 URL 且不开浏览器 | `TestConsolePrintURLOutputContract`（Task 6） |
| 17 | `sessions` 列出 / `revoke` 后消失或标记 | `TestSessionRoutesListRevokeLogout`（Task 4）+ `TestRenderSessionsMarksRevoked`（Task 7） |
| 18 | agentd 未运行时明确报错 | `TestConsoleAgentdNotRunning`（Task 6） |

**完工前的 `instrumenting-code` 自检**（逐项打勾，任一未过即未完工）：

- [ ] 每个错误分支都带上下文与 cause 打了日志
- [ ] 每个外部调用（库操作、HTTP）前后有日志或由调用方覆盖
- [ ] 成功路径不静默：签发 / 消费 / 建立 / 吊销 / 被踢五个节点都有 Info
- [ ] 全仓无 `fmt.Printf` 作为日志机制（CLI 的人读输出经 `cmd.OutOrStdout()` 不算）
- [ ] 每个新文件有职责 + 边界头注释
- [ ] 每个导出方法有参数 / 返回 / 注意注释
- [ ] **凭据纪律**：`grep` 确认主令牌、ticket 明文、cookie 明文从未作为日志字段出现
