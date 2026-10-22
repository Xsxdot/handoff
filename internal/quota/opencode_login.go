// opencode_login.go —— opencode-go 登录账号来源的两半：只读 opencode.db 取当前登录账号，
// 与 console 状态端点的解析（B453 轮 5，spec r1.3 S2、plan §8 r3）。
//
// 职责：
//   - readLoginAccount：以 SQLite URI mode=ro 打开 <HOME>/<opencode data>/opencode.db（opencode
//     可能正开着 WAL，只读连接照样读到未 checkpoint 的页），读 account_state 当前账号与组织、
//     account 行的 access_token 与 token_expiry（毫秒 epoch）。
//   - ParseOpencodeGoConsoleStatus：access.meters.{fiveHour,week,month} 的
//     usedMicroCents/limitMicroCents（字符串数字）→ session/weekly/monthly 已用%，resetsAt →
//     重置时刻，product → plan_type；access 缺失 = 无订阅。
//
// 边界：handoff 不写 opencode.db、不刷新 token（过期即报失败，等 opencode 自己刷新）；
// 错误文案只带库路径与表名，不带任何列值。列名取自 opencode 1.19.3 的 .schema（台账轮 5）。
package quota

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // 仓内既有 SQLite 驱动（store/ledger 同款），不新增依赖
)

// loginQueryTimeout 是读 opencode.db 的上限：库被 opencode 写锁住时 busy_timeout 等到这里为止。
const loginQueryTimeout = 5 * time.Second

// loginAccount 是 opencode.db 里的当前登录账号。token/org 是凭据，只交出网与抹除，不落日志。
type loginAccount struct {
	id, org, token string
	expiry         *time.Time // token_expiry；NULL = 未知（不按过期处理）
}

func (l loginAccount) expired(now time.Time) bool { return l.expiry != nil && !l.expiry.After(now) }

// readLoginAccount 只读读出当前登录账号。无库/无表/无当前账号 → credential_missing；
// 当前账号行缺 access_token → credential_invalid。org 是否为空由调用方判（Identity 也要它）。
func readLoginAccount(home, rel string) (loginAccount, error) {
	if rel == "" {
		return loginAccount{}, fail(CategoryUnsupportedPlatform, "本平台没有已知的 opencode 数据目录")
	}
	if home == "" || !filepath.IsAbs(home) {
		return loginAccount{}, fail(CategoryCredentialMissing, "凭据 HOME %q 不是绝对路径", home)
	}
	path := filepath.Join(home, rel)
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return loginAccount{}, fail(CategoryCredentialMissing, "%s 不存在，无登录账号（在该机运行 opencode 登录账号）", path)
		}
		return loginAccount{}, fail(CategoryCredentialMissing, "读取 %s 失败，无登录账号：%v", path, err)
	}
	// file: URI + mode=ro：驱动以 SQLITE_OPEN_URI 打开，只读、不建库；WAL 下照常读到最新提交。
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "mode=ro&_pragma=busy_timeout(3000)"}).String()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return loginAccount{}, fail(CategoryCredentialMissing, "只读打开 %s 失败，无登录账号：%v", path, err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	ctx, cancel := context.WithTimeout(context.Background(), loginQueryTimeout)
	defer cancel()

	var active, org sql.NullString
	err = db.QueryRowContext(ctx, "SELECT active_account_id, active_org_id FROM account_state ORDER BY id LIMIT 1").Scan(&active, &org)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return loginAccount{}, fail(CategoryCredentialMissing, "%s 无登录账号（account_state 为空，在该机运行 opencode 登录账号）", path)
	case err != nil && strings.Contains(err.Error(), "no such table"):
		return loginAccount{}, fail(CategoryCredentialMissing, "%s 无登录账号（没有 account_state 表，该机 opencode 未登录过账号）", path)
	case err != nil:
		return loginAccount{}, fail(CategoryCredentialMissing, "读取 %s 的 account_state 失败，无登录账号：%v", path, err)
	case !active.Valid || strings.TrimSpace(active.String) == "":
		return loginAccount{}, fail(CategoryCredentialMissing, "%s 无登录账号（当前没有选中的账号，在该机运行 opencode 登录账号）", path)
	}
	var token sql.NullString
	var expiry sql.NullInt64
	err = db.QueryRowContext(ctx, "SELECT access_token, token_expiry FROM account WHERE id = ?", active.String).Scan(&token, &expiry)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return loginAccount{}, fail(CategoryCredentialMissing, "%s 无登录账号（当前账号在 account 表里不存在）", path)
	case err != nil:
		return loginAccount{}, fail(CategoryCredentialMissing, "读取 %s 的 account 失败，无登录账号：%v", path, err)
	case !token.Valid || strings.TrimSpace(token.String) == "":
		return loginAccount{}, fail(CategoryCredentialInvalid, "%s 的当前登录账号缺少 access_token，在该机运行 opencode 重新登录", path)
	}
	acc := loginAccount{id: active.String, org: strings.TrimSpace(org.String), token: token.String}
	if expiry.Valid {
		t := time.UnixMilli(expiry.Int64)
		acc.expiry = &t
	}
	return acc, nil
}

// ParseOpencodeGoConsoleStatus 把 console 状态端点的状态码与响应体解析成读数（外部缝的解析入口）。
//
// 判定：401 → credential_expired（登录 token 失效，等 opencode 刷新或重新登录）；403 →
// 成功读数且 Blocked=无订阅（该账号无 Go 访问权）；429 → rate_limited；其余非 2xx → upstream；
// 2xx 的 access 缺失/null → Blocked=无订阅；meters 里 limit 为 0 或缺的窗口不计，一个可计
// 窗口都没有 → parse。
func ParseOpencodeGoConsoleStatus(status int, body []byte) (Snapshot, error) {
	switch status {
	case http.StatusUnauthorized:
		return Snapshot{}, fail(CategoryCredentialExpired, "opencode 登录凭据已失效（HTTP 401），在该机运行 opencode 重新登录")
	case http.StatusForbidden:
		return Snapshot{Blocked: fail(CategoryNoSubscription, "该 opencode 账号无 Go 订阅访问权（HTTP 403）")}, nil
	}
	if err := statusFailure(KindOpencodeGo, status, body, "opencode 登录凭据无效"); err != nil {
		return Snapshot{}, err
	}
	var doc struct {
		Product string          `json:"product"`
		Access  json.RawMessage `json:"access"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return Snapshot{}, fail(CategoryParse, "console 状态不是合法 JSON：%s", snippet(body))
	}
	out := Snapshot{PlanType: doc.Product}
	if len(doc.Access) == 0 || string(doc.Access) == "null" {
		out.Blocked = fail(CategoryNoSubscription, "该 opencode 账号没有 Go 订阅（console 状态无 access）")
		return out, nil
	}
	var access struct {
		Meters map[string]json.RawMessage `json:"meters"`
	}
	if err := json.Unmarshal(doc.Access, &access); err != nil {
		return Snapshot{}, fail(CategoryParse, "console 状态 access 形状无法解析：%s", snippet(body))
	}
	for _, pair := range []struct{ field, key string }{
		{"fiveHour", WindowSession}, {"week", WindowWeekly}, {"month", WindowMonthly},
	} {
		raw, ok := access.Meters[pair.field]
		if !ok || string(raw) == "null" {
			continue
		}
		var m struct {
			Limit    any            `json:"limitMicroCents"`
			Used     any            `json:"usedMicroCents"`
			ResetsAt flexibleMoment `json:"resetsAt"`
		}
		if err := json.Unmarshal(raw, &m); err != nil {
			return Snapshot{}, fail(CategoryParse, "access.meters.%s 形状无法解析：%v", pair.field, err)
		}
		limit, okL := num(m.Limit)
		used, okU := num(m.Used)
		if !okL || limit <= 0 || !okU {
			continue // limit 为 0/缺或 used 缺：该窗口不计
		}
		out.Windows = append(out.Windows, Window{Key: pair.key, UsedPercent: clampPercent(used / limit * 100), ResetsAt: m.ResetsAt.t})
	}
	if len(out.Windows) == 0 {
		return Snapshot{}, fail(CategoryParse, "access.meters 里没有可计的窗口（fiveHour/week/month 的 limit 都为 0 或缺）：%s", snippet(body))
	}
	return out, nil
}
