// opencodego_login_test.go —— opencode-go 登录账号来源（B453 轮 5，spec r1.3 S2、plan §8 r3）。
//
// 职责：锁三处承重——①console 状态端点的解析表（真样本三窗口与百分比、limit 0 跳过、
// access 缺失 = 无订阅、401/403/429/5xx）；②来源选择（entry 空自动：auth.json 的 API key
// 优先、否则 opencode.db 登录账号；entry=opencode.db 强制；其他 entry 只认 auth.json）与
// opencode.db 各失败分支；③请求形状（Bearer + x-org-id）、只读打开（含对方正开着 WAL）、
// 指纹取「账号 id + org id」而非 token。
// 边界：opencode.db 用临时目录里建的同名表（列取自 opencode 1.19.3 的 .schema）；外部接口
// 一律 httptest 回放真样本，不连 opencode.ai。
package quota

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const testDBRelPath = ".local/share/opencode/opencode.db"

// loginRow 描述 opencode.db 里要造的登录态；零值字段按「正常已登录」取缺省。
type loginRow struct {
	noTables  bool   // 库在、表不在
	noState   bool   // 有表、account_state 无行
	nullActv  bool   // account_state 行在、active_account_id 为 NULL
	id, token string // 账号 id 与 access_token
	org       string // active_org_id；"-" 表示 NULL
	expiryMs  *int64 // token_expiry；nil = 一小时后
	wal       bool   // 写者以 WAL 打开且测试期间不关（模拟 opencode 正开着库）
	noEmail   bool   // 旧版 account 表没有 email 列（m5 容错）
	noExpiry  bool   // account 表没有 token_expiry 列（段 9 m-b：不得走无 email 分支）
}

// writeOpencodeDB 在 home 下建 opencode.db（opencode 1.19.3 的 account / account_state 表形状）。
// wal=true 时返回的 *sql.DB 由调用方在用完后关闭，期间 -wal 文件存在、数据还没 checkpoint。
func writeOpencodeDB(t *testing.T, home string, r loginRow) *sql.DB {
	t.Helper()
	path := filepath.Join(home, testDBRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	dsn := "file:" + path
	if r.wal {
		dsn += "?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec("CREATE TABLE IF NOT EXISTS session (id text PRIMARY KEY)") // 让库文件非空
	if !r.noTables {
		switch {
		case r.noEmail:
			exec("CREATE TABLE `account` (`id` text PRIMARY KEY, `url` text NOT NULL, " +
				"`access_token` text NOT NULL, `refresh_token` text NOT NULL, `token_expiry` integer, " +
				"`time_created` integer NOT NULL, `time_updated` integer NOT NULL)")
		case r.noExpiry:
			exec("CREATE TABLE `account` (`id` text PRIMARY KEY, `email` text NOT NULL, `url` text NOT NULL, " +
				"`access_token` text NOT NULL, `refresh_token` text NOT NULL, " +
				"`time_created` integer NOT NULL, `time_updated` integer NOT NULL)")
		default:
			exec("CREATE TABLE `account` (`id` text PRIMARY KEY, `email` text NOT NULL, `url` text NOT NULL, " +
				"`access_token` text NOT NULL, `refresh_token` text NOT NULL, `token_expiry` integer, " +
				"`time_created` integer NOT NULL, `time_updated` integer NOT NULL)")
		}
		exec("CREATE TABLE `account_state` (`id` integer PRIMARY KEY, `active_account_id` text, `active_org_id` text)")
		id, token := r.id, r.token
		if id == "" {
			id = "acc_test_1"
		}
		if token == "" {
			token = "oc-token-test"
		}
		exp := time.Now().Add(time.Hour).UnixMilli()
		if r.expiryMs != nil {
			exp = *r.expiryMs
		}
		switch {
		case r.noEmail:
			exec("INSERT INTO account (id, url, access_token, refresh_token, token_expiry, time_created, time_updated) "+
				"VALUES (?, 'https://opencode.ai', ?, 'refresh-x', ?, 1, 1)", id, token, exp)
		case r.noExpiry:
			exec("INSERT INTO account (id, email, url, access_token, refresh_token, time_created, time_updated) "+
				"VALUES (?, 'u@example.com', 'https://opencode.ai', ?, 'refresh-x', 1, 1)", id, token)
		default:
			exec("INSERT INTO account VALUES (?, 'u@example.com', 'https://opencode.ai', ?, 'refresh-x', ?, 1, 1)", id, token, exp)
		}
		if !r.noState {
			var actv, org any = id, r.org
			if r.nullActv {
				actv = nil
			}
			if r.org == "" {
				org = "org_test_1"
			} else if r.org == "-" {
				org = nil
			}
			exec("INSERT INTO account_state VALUES (1, ?, ?)", actv, org)
		}
	}
	if r.wal {
		t.Cleanup(func() { _ = db.Close() })
		return db
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return nil
}

// consoleServer 回放两条路径：API key 的 usage 与登录账号的 console 状态；记录收到的头。
type consoleServer struct {
	*httptest.Server
	mu                     sync.Mutex
	path, auth, org        string
	statusCode             int
	statusBody, usageReply string
}

func newConsoleServer(t *testing.T) *consoleServer {
	t.Helper()
	cs := &consoleServer{statusCode: http.StatusOK, statusBody: string(readTestdata(t, "opencodego_console_status_200.real.json")),
		usageReply: `{"usage":{"weekly":{"percent":40}}}`}
	cs.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cs.mu.Lock()
		cs.path, cs.auth, cs.org = r.URL.Path, r.Header.Get("Authorization"), r.Header.Get("x-org-id")
		code, body := cs.statusCode, cs.statusBody
		cs.mu.Unlock()
		if r.URL.Path == opencodeGoUsagePath {
			_, _ = w.Write([]byte(cs.usageReply))
			return
		}
		w.WriteHeader(code)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(cs.Close)
	return cs
}

func approx(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

func TestOpencodeGoConsoleStatusParseTable(t *testing.T) {
	sample := readTestdata(t, "opencodego_console_status_200.real.json")
	snap, err := ParseOpencodeGoConsoleStatus(http.StatusOK, sample)
	if err != nil {
		t.Fatalf("真样本应解析成功: %v", err)
	}
	if snap.PlanType != "go-plus" || snap.Blocked != nil || len(snap.Windows) != 3 {
		t.Fatalf("读数 = %+v", snap)
	}
	want := []struct {
		key   string
		pct   float64
		reset string
	}{
		{WindowSession, 17968895.0 / 4800000000 * 100, "2026-10-22T07:43:46Z"},
		{WindowWeekly, 585476885.0 / 12000000000 * 100, "2026-10-26T00:00:00Z"},
		{WindowMonthly, 585476885.0 / 24000000000 * 100, "2026-11-20T03:33:08Z"},
	}
	for i, w := range want {
		got := snap.Windows[i]
		if got.Key != w.key || !approx(got.UsedPercent, w.pct) || got.ResetsAt == nil || !got.ResetsAt.Equal(mustTime(t, w.reset)) {
			t.Fatalf("窗口 %d = %+v (reset %v)，want %s %.6f%% %s", i, got, got.ResetsAt, w.key, w.pct, w.reset)
		}
	}

	cases := []struct {
		name    string
		status  int
		body    string
		windows []string
		blocked Category
		failure Category
		msg     string
	}{
		{name: "limit 0 与缺 limit 的窗口不计", status: 200,
			body: `{"product":"go","access":{"meters":{"fiveHour":{"limitMicroCents":"0","usedMicroCents":"5"},` +
				`"week":{"usedMicroCents":"5"},"month":{"limitMicroCents":"100","usedMicroCents":"50","resetsAt":"2026-11-20T03:33:08.000Z"}}}}`,
			windows: []string{WindowMonthly}},
		{name: "access 为 null = 无订阅", status: 200, body: `{"product":"go","access":null}`, blocked: CategoryNoSubscription},
		{name: "access 缺失 = 无订阅", status: 200, body: `{"product":"go"}`, blocked: CategoryNoSubscription},
		{name: "全部窗口都不可计", status: 200, body: `{"access":{"meters":{"week":{"limitMicroCents":"0","usedMicroCents":"1"}}}}`, failure: CategoryParse},
		{name: "非 JSON", status: 200, body: `<html>`, failure: CategoryParse},
		{name: "401 = 登录凭据过期", status: 401, body: `{"error":"unauthorized"}`, failure: CategoryCredentialExpired, msg: "重新登录"},
		{name: "403 = 无 Go 订阅访问权", status: 403, body: `{"error":"forbidden"}`, blocked: CategoryNoSubscription, msg: "该 opencode 账号无 Go 订阅访问权"},
		{name: "429 限速不是额度", status: 429, body: `{}`, failure: CategoryRateLimited},
		{name: "5xx 上游", status: 502, body: `bad gateway`, failure: CategoryUpstream},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap, err := ParseOpencodeGoConsoleStatus(tc.status, []byte(tc.body))
			var f *Failure
			switch {
			case tc.failure != "":
				if !errors.As(err, &f) || f.Category != tc.failure || !strings.Contains(f.Message, tc.msg) {
					t.Fatalf("want 失败 %s（含 %q），得 snap=%+v err=%v", tc.failure, tc.msg, snap, err)
				}
			case tc.blocked != "":
				if err != nil || snap.Blocked == nil || snap.Blocked.Category != tc.blocked || !strings.Contains(snap.Blocked.Message, tc.msg) {
					t.Fatalf("want Blocked %s（含 %q），得 snap=%+v err=%v", tc.blocked, tc.msg, snap, err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
				var keys []string
				for _, w := range snap.Windows {
					keys = append(keys, w.Key)
				}
				if strings.Join(keys, ",") != strings.Join(tc.windows, ",") {
					t.Fatalf("窗口 = %v，want %v", keys, tc.windows)
				}
				if len(snap.Windows) == 1 && !approx(snap.Windows[0].UsedPercent, 50) {
					t.Fatalf("月窗口百分比 = %v，want 50", snap.Windows[0].UsedPercent)
				}
			}
		})
	}
}

func TestOpencodeGoSourceSelection(t *testing.T) {
	expired := time.Now().Add(-time.Minute).UnixMilli()
	apiAuth := `{"opencode-go":{"type":"api","key":"` + sentinelKey + `"}}`
	oauthAuth := `{"opencode-go":{"type":"oauth","access":"x"}}`
	cases := []struct {
		name    string
		auth    *string   // nil = 不建 auth.json
		db      *loginRow // nil = 不建 opencode.db
		entry   string
		path    string   // 期望打到的端点；空 = 不应出网
		failure Category // 非空 = 期望失败
		msg     []string // 失败文案须含
	}{
		{name: "自动：auth.json 有 API key 条目走现路径", auth: &apiAuth, db: &loginRow{}, path: opencodeGoUsagePath},
		{name: "自动：无 auth.json 回落登录账号", db: &loginRow{}, path: opencodeGoStatusPath},
		{name: "自动：条目不是 API key 回落登录账号", auth: &oauthAuth, db: &loginRow{}, path: opencodeGoStatusPath},
		{name: "entry=opencode.db 强制登录账号", auth: &apiAuth, db: &loginRow{}, entry: "opencode.db", path: opencodeGoStatusPath},
		{name: "其他 entry 只认 auth.json 不回落", db: &loginRow{}, entry: "opencode-go", failure: CategoryCredentialMissing},
		{name: "两处都没有", failure: CategoryCredentialMissing,
			msg: []string{filepath.FromSlash(testCredRelPath), "无 opencode-go 条目", filepath.FromSlash(testDBRelPath), "无登录账号"}},
		{name: "库在表不在", db: &loginRow{noTables: true}, entry: "opencode.db", failure: CategoryCredentialMissing, msg: []string{"无登录账号"}},
		{name: "无当前账号行", db: &loginRow{noState: true}, entry: "opencode.db", failure: CategoryCredentialMissing, msg: []string{"无登录账号"}},
		{name: "当前账号为空", db: &loginRow{nullActv: true}, entry: "opencode.db", failure: CategoryCredentialMissing, msg: []string{"无登录账号"}},
		{name: "登录已过期", db: &loginRow{expiryMs: &expired}, failure: CategoryCredentialExpired,
			msg: []string{"opencode 登录已过期，在该机运行 opencode 重新登录"}},
		{name: "org 为空", db: &loginRow{org: "-"}, failure: CategoryCredentialInvalid, msg: []string{"org"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := newConsoleServer(t)
			home := t.TempDir()
			if tc.auth != nil {
				home = writeOpencodeAuth(t, *tc.auth)
			}
			if tc.db != nil {
				writeOpencodeDB(t, home, *tc.db)
			}
			a := &OpencodeGo{BaseURL: cs.URL, CredRelPath: testCredRelPath}
			snap, err := a.Probe(context.Background(), Request{Kind: KindOpencodeGo, Home: home, Entry: tc.entry})
			if tc.failure != "" {
				var f *Failure
				if !errors.As(err, &f) || f.Category != tc.failure {
					t.Fatalf("want %s，得 snap=%+v err=%v", tc.failure, snap, err)
				}
				for _, m := range tc.msg {
					if !strings.Contains(f.Message, m) {
						t.Fatalf("失败文案应含 %q：%s", m, f.Message)
					}
				}
				if cs.path != "" {
					t.Fatalf("凭据不成立不应出网，却打了 %s", cs.path)
				}
				return
			}
			if err != nil {
				t.Fatalf("probe: %v", err)
			}
			if cs.path != tc.path {
				t.Fatalf("端点 = %q，want %q", cs.path, tc.path)
			}
			if tc.path == opencodeGoStatusPath {
				if cs.auth != "Bearer oc-token-test" || cs.org != "org_test_1" {
					t.Fatalf("登录账号请求头不对：Bearer 对=%v x-org-id 对=%v", cs.auth == "Bearer oc-token-test", cs.org == "org_test_1")
				}
				if snap.PlanType != "go-plus" || len(snap.Windows) != 3 {
					t.Fatalf("读数 = %+v", snap)
				}
				if snap.Fingerprint != Fingerprint("acc_test_1/org_test_1") || snap.Fingerprint == Fingerprint("oc-token-test") {
					t.Fatalf("登录账号指纹应取账号 id + org id，得 %s", snap.Fingerprint)
				}
			} else if cs.auth != "Bearer "+sentinelKey || snap.Fingerprint != Fingerprint(sentinelKey) {
				t.Fatalf("API key 来源请求头或指纹不对")
			}
		})
	}
}

// opencode 正开着库（WAL、未 checkpoint）时，只读打开仍读得到当前账号；且探测不改库文件。
func TestOpencodeGoLoginReadsLiveWALReadOnly(t *testing.T) {
	cs := newConsoleServer(t)
	home := t.TempDir()
	writer := writeOpencodeDB(t, home, loginRow{wal: true, token: "oc-token-test"})
	if _, err := os.Stat(filepath.Join(home, testDBRelPath+"-wal")); err != nil {
		t.Fatalf("前提：写者应留着 -wal 文件: %v", err)
	}
	dbPath := filepath.Join(home, testDBRelPath)
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	a := &OpencodeGo{BaseURL: cs.URL, CredRelPath: testCredRelPath}
	if _, err := a.Probe(context.Background(), Request{Kind: KindOpencodeGo, Home: home}); err != nil {
		t.Fatalf("WAL 未 checkpoint 时只读打开应读到登录账号: %v", err)
	}
	if cs.auth != "Bearer oc-token-test" {
		t.Fatal("没读到 WAL 里的 access_token")
	}
	// 写者还能继续写（我们没把库锁住），主库文件字节未被只读连接改动
	if _, err := writer.Exec("UPDATE account SET time_updated = 2"); err != nil {
		t.Fatalf("只读探测后写者应仍可写: %v", err)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("探测改动了 opencode.db 主库文件（只许只读）")
	}
}

// 载体检测的指纹比对与读数走同一 Identity：登录账号来源 = 「账号 id + org id」。
func TestOpencodeGoIdentityFollowsSourceSelection(t *testing.T) {
	reg := NewRegistry(map[string]Prober{KindOpencodeGo: &OpencodeGo{CredRelPath: testCredRelPath}})
	login := t.TempDir()
	writeOpencodeDB(t, login, loginRow{})
	if fp, err := reg.CredentialFingerprint(KindOpencodeGo, login, ""); err != nil || fp != Fingerprint("acc_test_1/org_test_1") {
		t.Fatalf("自动回落登录账号的指纹 = %q %v", fp, err)
	}
	// 登录过期不影响身份（同一账号），只影响探测
	exp := time.Now().Add(-time.Hour).UnixMilli()
	stale := t.TempDir()
	writeOpencodeDB(t, stale, loginRow{expiryMs: &exp})
	if fp, err := reg.CredentialFingerprint(KindOpencodeGo, stale, "opencode.db"); err != nil || fp != Fingerprint("acc_test_1/org_test_1") {
		t.Fatalf("过期登录的身份仍应可取，得 %q %v", fp, err)
	}
	api := writeOpencodeAuth(t, `{"opencode-go":{"type":"api","key":"`+sentinelKey+`"}}`)
	writeOpencodeDB(t, api, loginRow{})
	if fp, _ := reg.CredentialFingerprint(KindOpencodeGo, api, ""); fp != Fingerprint(sentinelKey) {
		t.Fatalf("auth.json 有 API key 时身份应取 key，得 %q", fp)
	}
	if _, err := reg.CredentialFingerprint(KindOpencodeGo, t.TempDir(), ""); err == nil {
		t.Fatal("两处都没有时应返回凭据缺失")
	}
}

// 审查第 3 轮 R3-m1：探测器打开 opencode.db 的 DSN（readLoginAccount 唯一用它）必须是只读
// 连接——对同一 DSN 打开后执行写语句须被 SQLite 拒绝；库文件不存在时也不得被创建。
func TestOpencodeGoLoginDSNIsReadOnly(t *testing.T) {
	home := t.TempDir()
	writeOpencodeDB(t, home, loginRow{})
	path := filepath.Join(home, testDBRelPath)
	db, err := sql.Open("sqlite", loginDSN(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	if err := db.QueryRow("SELECT count(*) FROM account").Scan(&n); err != nil || n != 1 {
		t.Fatalf("只读连接应能读: n=%d err=%v", n, err)
	}
	if _, err := db.Exec("UPDATE account SET time_updated = 9"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "readonly") {
		t.Fatalf("探测器的连接必须拒绝写入（SQLITE_READONLY），得 %v", err)
	}
	missing := filepath.Join(t.TempDir(), "opencode.db")
	if m, err := sql.Open("sqlite", loginDSN(missing)); err == nil {
		_ = m.Ping()
		_ = m.Close()
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("只读 DSN 不得建库，得 %v", err)
	}
}

// TestOpencodeGoLoginMissingTokenExpiryStaysCredentialMissing 锁 m-b：account 表缺 email 以外
// 的列（token_expiry）不是「旧版无 email」，必须按凭据缺失报原错误，不走无 email 容错分支。
func TestOpencodeGoLoginMissingTokenExpiryStaysCredentialMissing(t *testing.T) {
	home := t.TempDir()
	writeOpencodeDB(t, home, loginRow{noExpiry: true})
	var logs strings.Builder
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(prev)
	_, err := readLoginAccount(home, testDBRelPath)
	var f *Failure
	if !errors.As(err, &f) || f.Category != CategoryCredentialMissing {
		t.Fatalf("缺 token_expiry 列应按凭据缺失报，得 %v", err)
	}
	if !strings.Contains(f.Message, "no such column: token_expiry") {
		t.Fatalf("失败文案应保留原列错误，得 %q", f.Message)
	}
	if strings.Contains(logs.String(), "无 email 列") {
		t.Fatalf("缺 token_expiry 不得走无 email 容错分支，日志：%s", logs.String())
	}
}
