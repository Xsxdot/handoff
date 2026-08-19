// DispatchTemplate 聚合：派发配方（executor/纪律块/prompt/目标机/分支
// 命名/模型覆盖），不可变版本化，与 workflow 同构。分支策略只管工作
// 分支命名——基线从卡的 base_branch 来（蓝图 §3.3）。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// TemplateDef 派发模板定义。DisciplinePath 指向纪律块文件（仓内相对
// 路径）；派发时读文件算 hash 快照进事件——「那次派发用的哪版纪律块」
// 必须答得上。
type TemplateDef struct {
	Executor       string            `json:"executor"`
	Target         string            `json:"target"`
	Purpose        string            `json:"purpose"`
	BranchPrefix   string            `json:"branch_prefix"`
	Prompt         string            `json:"prompt"`
	DisciplinePath string            `json:"discipline_path"`
	ModelByTarget  map[string]string `json:"model_by_target,omitempty"`
}

// Template 一个版本化的派发模板。
type Template struct {
	Name      string
	Version   int
	Def       TemplateDef
	CreatedAt time.Time
}

// PutTemplate 写入下一版本（不改旧行）。
func (s *Store) PutTemplate(name string, def TemplateDef) (int, error) {
	raw, err := json.Marshal(def)
	if err != nil {
		return 0, fmt.Errorf("编码模板定义: %w", err)
	}
	var ver int
	err = s.mutate(func(tx *sql.Tx, _ *eventSink) error {
		if err := tx.QueryRow(s.q(`SELECT COALESCE(MAX(version),0) FROM dispatch_templates WHERE name = ?`),
			name).Scan(&ver); err != nil {
			return fmt.Errorf("查模板版本: %w", err)
		}
		ver++
		if _, err := tx.Exec(s.q(`INSERT INTO dispatch_templates (name, version, definition, created_at)
			VALUES (?,?,?,?)`), name, ver, string(raw), s.tval(time.Now())); err != nil {
			return fmt.Errorf("写模板 %s v%d: %w", name, ver, err)
		}
		return nil
	})
	return ver, err
}

// GetTemplate 取指定版本；0 = 最新。
func (s *Store) GetTemplate(name string, version int) (Template, error) {
	q := `SELECT name, version, definition, created_at FROM dispatch_templates WHERE name = ?`
	args := []any{name}
	if version > 0 {
		q += ` AND version = ?`
		args = append(args, version)
	}
	q += ` ORDER BY version DESC LIMIT 1`
	var t Template
	var raw string
	var ct any
	err := s.db.QueryRow(s.q(q), args...).Scan(&t.Name, &t.Version, &raw, &ct)
	if errors.Is(err, sql.ErrNoRows) {
		return Template{}, fmt.Errorf("模板 %s v%d: %w", name, version, ErrNotFound)
	}
	if err != nil {
		return Template{}, fmt.Errorf("读模板: %w", err)
	}
	if err := jsonUnmarshal(raw, &t.Def); err != nil {
		return Template{}, err
	}
	t.CreatedAt = toTime(ct)
	return t, nil
}

// ListTemplateNames 全部模板名。
func (s *Store) ListTemplateNames() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT name FROM dispatch_templates ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("列模板名: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// reviewVerdictContract 审阅输出契约原文——进审阅模板 prompt，随模板
// 版本化（改契约 = 出新模板版本，spec §5）。
const reviewVerdictContract = "回合结束时，在最终报文末尾输出你的裁决，格式为一个 fenced code block，" +
	"语言标记 handoff-verdict，内容是 JSON：\n" +
	"```handoff-verdict\n" +
	`{"verdict":"pass"或"fail","findings":[{"severity":"major"或"minor","summary":"一句话","file":"可选路径"}],"notes":"可选"}` +
	"\n```\n" +
	"只输出一个该 block；解析不到会转人工，不要省略。\n" +
	// why 要专门写收尾行的确切形状：回合铁律只给了两个出口——提问，或
	// 「commit 后输出 branch/commit/summary」。审阅被禁止提交，于是它唯一
	// 合法的出口只剩提问，三个执行器都照做把裁决塞进了工单（2026-08-19
	// 真机实测）。协议其实允许只带 summary 的收尾行，这里把它说明白。
	"收尾行：你不提交，所以本回合最后一行输出 " + "`" + `{"summary":"<裁决块原文，换行写成 \n>"}` + "`" + "。\n" +
	"不要用提问工单发裁决——工单是提问通道，节点只读回合末尾的最终报文。"

// EnsureDefaultTemplates 幂等 seed 出厂模板。已存在同名的不覆盖。
func (s *Store) EnsureDefaultTemplates() error {
	defaults := map[string]TemplateDef{
		"feature-impl": {
			Executor: "opencode", Purpose: "implement", BranchPrefix: "cards",
			DisciplinePath: "docs/superpowers/discipline/block-a.md",
			Prompt:         "实现以下工作项：{{TITLE}}（卡 {{CARD}}）。\n验收判据：{{ACCEPT}}\n完整需求见随附 plan。",
		},
		"review-generic": {
			Executor: "grok", Purpose: "review", BranchPrefix: "cards",
			// 审阅用只读纪律块：实现类纪律块写着「每个 task 完成即 commit」，
			// 派给审阅者会让它在审阅分支上真的提交东西（2026-08-19 真机实测
			// 出现过一次）——审阅的产出是裁决报文，不是提交
			DisciplinePath: "docs/superpowers/discipline/block-review.md",
			Prompt: "审阅卡 {{CARD}}（{{TITLE}}）对应分支的完整 diff：spec 符合性（要求全实现、没有多做）+ 代码质量双裁决。\n" +
				"验收判据：{{ACCEPT}}\n" + reviewVerdictContract,
		},
	}
	for name, def := range defaults {
		if _, err := s.GetTemplate(name, 0); err == nil {
			continue
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		if _, err := s.PutTemplate(name, def); err != nil {
			return err
		}
		log().Info("seed 默认派发模板", "name", name)
	}
	return nil
}
