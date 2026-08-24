// DispatchTemplate 聚合：派发配方（executor/纪律块/prompt/目标机/分支
// 命名/模型覆盖），不可变版本化，与 workflow 同构。分支策略只管工作
// 分支命名——基线从卡的 base_branch 来（蓝图 §3.3）。
package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// TemplateDef 派发模板定义。
type TemplateDef struct {
	Executor     string `json:"executor"`
	Target       string `json:"target"`
	Purpose      string `json:"purpose"`
	BranchPrefix string `json:"branch_prefix"`
	Prompt       string `json:"prompt"`
	// Discipline 是派发该模板时点名的纪律块**角色名**（如 implement / review）；
	// 空=按 executor 兜底。
	Discipline string `json:"discipline,omitempty"`
	// DisciplinePath 是**已废弃**的旧字段（仓内相对路径）。
	//
	// 保留它不是为了兼容语义，是为了**不静默降级**：模板 def 存 JSON 且用宽松
	// 解码，直接删字段会让老行解出空 Discipline、退回 executor 兜底——审阅模板
	// 悄悄拿到实现块，正是本次重构要修的缺陷换个方式复活。读取时映射并 Warn，
	// 提示用户 template put 重写；确认线上无残留后再删。
	DisciplinePath string            `json:"discipline_path,omitempty"`
	ModelByTarget  map[string]string `json:"model_by_target,omitempty"`
}

// legacyDisciplinePaths 是废弃的 discipline_path 取值 → 角色名的映射表。
// 只认这三个出厂过的文件名：认不出来的自定义路径没法猜，映射为空退回兜底。
var legacyDisciplinePaths = map[string]string{
	"block-review.md": "review",
	"block-a.md":      "implement",
	"block-b.md":      "implement",
}

// disciplineNameFromLegacyPath 把废弃的 discipline_path 换算成角色名。
//
// 参数：path 旧字段原值（形如仓内纪律目录下的 review 文件）。
// 返回：角色名；认不出来时返回空串（调用方负责 Warn 并退回兜底）。
//
// 只按 basename 匹配已知的三个出厂文件名：用户自定义的路径指向的是什么纪律
// 我们不知道，猜错比退回兜底更危险。
func disciplineNameFromLegacyPath(path string) string {
	if path == "" {
		return ""
	}
	return legacyDisciplinePaths[filepath.Base(path)]
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
	// 老行只有废弃的 discipline_path：映射成名字，映不出来就退回兜底，
	// 两种情况都出声——静默降级会让审阅模板悄悄拿到实现块。
	if t.Def.Discipline == "" && t.Def.DisciplinePath != "" {
		if name := disciplineNameFromLegacyPath(t.Def.DisciplinePath); name != "" {
			t.Def.Discipline = name
			log().Warn("模板用了废弃字段 discipline_path，已按文件名映射为角色名；建议 template put 重写",
				"template", t.Name, "legacy_path", t.Def.DisciplinePath, "name", name)
		} else {
			log().Warn("模板用了废弃字段 discipline_path 且认不出对应角色，本次派发将按 executor 兜底；建议 template put 重写",
				"template", t.Name, "legacy_path", t.Def.DisciplinePath)
		}
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
