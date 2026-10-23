// migrate.go —— 存量载体自动对上账号登录（B464 S6，plan §5 / V12）。
//
// 职责：载体所在机在本机一轮发现成功写回之后调用 MigrateCarrierLogins，只对本机、且
// login 与 no_account 都缺席的载体按四条规则（命中即停）补上选择——
//  1. 载体 HOME 展开后与本机某条未失效、kind 兼容的登录 home_abs 相同且恰一条 → 写 login；
//  2. 多条 → 按模型前缀消歧（opencode-go/… → opencode-go 登录；<provider>/… → 该 provider 的
//     apikey 登录），仍不唯一 → 不写；
//  3. 零条且载体 HOME 是主 HOME → 写 no_account；
//  4. 其余 → 不写（读面「未选账号登录」，照常可派发）。
//
// 边界：幂等；从不覆盖人的选择；home_dir 串不动（plan Q3）；逐载体 CAS，冲突（人恰在编辑）
// 本轮放弃、下轮再试。只有所在机能展开 `~`、只有它刚扫过本机 HOME，所以「哪台是本机」与
// HOME 展开由组装侧注入，本域不猜。
package scheduling

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/schedclient"
	"github.com/Xsxdot/handoff/internal/scheduling/accounts"
)

// MigrationRule 是迁移命中的规则号（plan §5，1–4）；0 = 没处理（已选或非本机）。
type MigrationRule int

// MigrationResult 是一个载体的迁移结论（日志与测试用）。Login/NoAccount 是本轮写入的选择；
// 两者皆空 = 未写（规则 2 仍不唯一、规则 4，或 CAS 冲突本轮放弃）。
type MigrationResult struct {
	Carrier   string
	Rule      MigrationRule
	Login     string
	NoAccount bool
	Reason    string
}

// MigrateCarrierLogins 按 plan §5 迁移本机存量载体（见文件头）。logins 是本机本轮发现后的
// 全部登录（Reconcile 的结果）；isSelf 判载体机器是否本机；expandHome 在本机展开 `~`。
// 返回逐载体结论；读载体列表失败返回错误，单载体写失败只记在结论里（下轮再试）。
func (s *Service) MigrateCarrierLogins(logins []accounts.Login, isSelf func(string) bool,
	expandHome func(string) (string, error)) ([]MigrationResult, error) {
	rows, err := s.CarrierRows()
	if err != nil {
		return nil, fmt.Errorf("迁移存量载体：列载体失败: %w", err)
	}
	mainAbs, mainErr := expandHome("~")
	var out []MigrationResult
	for _, row := range rows {
		c := row.Carrier
		if c.LoginChosen() || !isSelf(c.Machine) {
			continue
		}
		home := strings.TrimSpace(c.HomeDir)
		if home == "" {
			home = "~"
		}
		abs, err := expandHome(home)
		if err != nil {
			res := MigrationResult{Carrier: c.Name, Rule: 4, Reason: "HOME 展开失败：" + err.Error()}
			statusLog().Warn("存量载体迁移：HOME 展开失败，留作未选", "carrier", c.Name, "home_dir", c.HomeDir, "cause", err)
			out = append(out, res)
			continue
		}
		res := decideMigration(c, abs, mainErr == nil && samePath(abs, mainAbs), logins)
		if res.Login != "" || res.NoAccount {
			if werr := s.writeMigration(c.Name, row.Version, res); werr != nil {
				res.Reason = "写入未成（下轮再试）：" + werr.Error()
				res.Login, res.NoAccount = "", false
				statusLog().Warn("存量载体迁移写入未成，下轮再试", "carrier", c.Name, "rule", res.Rule, "cause", werr)
				out = append(out, res)
				continue
			}
		}
		statusLog().Info("存量载体迁移", "carrier", c.Name, "machine", c.Machine, "rule", res.Rule,
			"login", res.Login, "no_account", res.NoAccount, "reason", res.Reason)
		out = append(out, res)
	}
	return out, nil
}

// decideMigration 是 §5 四条规则的纯判定（不写库）。
func decideMigration(c Carrier, homeAbs string, isMainHome bool, logins []accounts.Login) MigrationResult {
	var match []accounts.Login
	for _, l := range logins {
		if l.State != accounts.LoginGone && accounts.Compatible(c.CLI, l.Kind) && samePath(l.HomeAbs, homeAbs) {
			match = append(match, l)
		}
	}
	switch {
	case len(match) == 1:
		return MigrationResult{Carrier: c.Name, Rule: 1, Login: match[0].ID, Reason: "同机同 HOME 恰一条兼容登录"}
	case len(match) > 1:
		if picked, ok := pickByModel(c.Model, match); ok {
			return MigrationResult{Carrier: c.Name, Rule: 2, Login: picked.ID, Reason: "多条兼容登录，按模型前缀消歧"}
		}
		return MigrationResult{Carrier: c.Name, Rule: 2, Reason: fmt.Sprintf("同 HOME 有 %d 条兼容登录且模型前缀无法消歧，留作未选", len(match))}
	case isMainHome:
		return MigrationResult{Carrier: c.Name, Rule: 3, NoAccount: true, Reason: "主 HOME 无兼容登录 → 无账号 · 主 HOME"}
	default:
		return MigrationResult{Carrier: c.Name, Rule: 4, Reason: "HOME 里没有兼容登录，留作未选"}
	}
}

// pickByModel 是 plan Q14：模型 `opencode-go/…` → 唯一的 opencode-go 登录；`<provider>/…` →
// 该 provider 唯一的 apikey 登录；其余或仍不唯一 → false。
func pickByModel(model string, match []accounts.Login) (accounts.Login, bool) {
	prefix, _, ok := strings.Cut(strings.TrimSpace(model), "/")
	if !ok || prefix == "" {
		return accounts.Login{}, false
	}
	var hit []accounts.Login
	for _, l := range match {
		if (prefix == accounts.KindOpencodeGo && l.Kind == accounts.KindOpencodeGo) ||
			(l.Kind == accounts.KindAPIKey && l.Provider == prefix) {
			hit = append(hit, l)
		}
	}
	if len(hit) != 1 {
		return accounts.Login{}, false
	}
	return hit[0], true
}

// samePath 比较两条本机绝对路径：Clean 后相同，或解软链后相同（mac `/Users` 与经软链的同一路径）。
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	a, b = filepath.Clean(a), filepath.Clean(b)
	if a == b {
		return true
	}
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

// writeMigration 以读到的版本 CAS 只补选择字段（状态、HOME 串原样）。期间被人改过 → 冲突，
// 本轮放弃；人已做了选择 → 不覆盖。
func (s *Service) writeMigration(name string, version int, res MigrationResult) error {
	rec, err := s.repo.Get(kindCarrier, name)
	if err != nil {
		return fmt.Errorf("读载体 %s: %w", name, err)
	}
	if rec.Version != version {
		return fmt.Errorf("载体 %s 在迁移期间被修改: %w", name, schedclient.ErrCASConflict)
	}
	var c Carrier
	if err := json.Unmarshal(rec.Body, &c); err != nil {
		return fmt.Errorf("载体 %s/%d 解码失败: %w", name, rec.Version, err)
	}
	if c.LoginChosen() {
		return errors.New("载体已有选择，不覆盖")
	}
	c.Login, c.NoAccount = res.Login, res.NoAccount
	body, err := json.Marshal(c)
	if err != nil {
		return fmt.Errorf("编码载体 %s: %w", name, err)
	}
	if _, err := s.repo.Put(kindCarrier, name, version, body, "scheduling.migrate"); err != nil {
		return fmt.Errorf("写载体 %s: %w", name, err)
	}
	return nil
}
