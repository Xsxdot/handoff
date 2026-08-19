# 派发模板 + card dispatch + 节点执行器（Plan C / B156.1-推进）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 落地 DispatchTemplate 聚合与 `handoff template` 命令族、`handoff card dispatch`（派发即认领 + 模板版本/纪律块 hash 快照）、以及节点执行器 `internal/ledgernode`（裁决解析、回合计数、审阅节点、合并节点）——一期唯一的「编排」构件，主会话与看板按钮共用同一实现（spec §5：落码，不留 prose）。

**Architecture:** 模板是账本库里的不可变版本化聚合（与 workflow 同构）。`card dispatch` 站在 CLI 侧：读卡与模板 → 认领（CAS 转「进行中」+ driver claim）→ 拼 prompt（纪律块文件 + hash 校验 + plan 附件）→ 走**既有 dispatch 通道**（`client.Dispatch`，不另起炉灶）→ LinkTask + dispatched 事件快照。节点执行器是纯 Go 库：输入卡 + 节点定义，动作全部经账本库与 client 完成，自身无状态（回合计数从 card_events 推导）。合并节点只做客观判据（测试 + gofmt）+ 合回**有效基线分支**；基线是 main 时不自动合，推「待合并」。

**Tech Stack:** `internal/ledger`（Plan A/B）、`internal/client`（Dispatch/WaitEvent/Continue/Done）、`internal/localsync`（fetch task 分支）、`os/exec` git。

**前置条件：** Plan A、A2、B 已合入；基线全绿。`internal/client` 的方法签名（`Dispatch`/`DispatchOpts`/`WaitEvent`/`Continue`/`Done`）以实际代码为准——本 plan 按摸底记录书写，出入时改本 plan 侧调用，不改 client。

**House rules：** 同前三份 plan。节点执行器每个决策点必打 Info 日志（进节点/裁决结果/回合数/封顶转等人/合并结果），这是「审阅超轮为什么停」将来查账的唯一依据。

---

## File Structure

```
internal/ledger/
  templates.go        // DispatchTemplate 聚合 + TemplateDef + 出厂默认
  templates_test.go
internal/ledgernode/
  verdict.go          // handoff-verdict 解析（纯函数）
  verdict_test.go
  rounds.go           // 回合计数（从事件流推导，人工插手重置）
  rounds_test.go
  node.go             // ReviewNode / MergeNode 执行体
  node_test.go        // fake client / fixture git 仓测试
cmd/
  template.go         // template list/show/put
  template_test.go
  card_dispatch.go    // card dispatch 动词（挂进 cardCmd）
  card_dispatch_test.go
```

---

### Task 1: templates.go——DispatchTemplate 聚合与出厂默认

**Files:**
- Create: `internal/ledger/templates.go`
- Test: `internal/ledger/templates_test.go`

- [ ] **Step 1: 写失败测试**

```go
package ledger

import (
	"strings"
	"testing"
)

func TestTemplateVersioningAndDefaults(t *testing.T) {
	s := newTestStore(t)
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	tp, err := s.GetTemplate("feature-impl", 0)
	if err != nil || tp.Version != 1 {
		t.Fatalf("feature-impl: %v %+v", err, tp)
	}
	if tp.Def.Executor != "opencode" || tp.Def.DisciplinePath == "" {
		t.Fatalf("默认模板字段: %+v", tp.Def)
	}
	rv, err := s.GetTemplate("review-generic", 0)
	if err != nil {
		t.Fatalf("review-generic: %v", err)
	}
	// 审阅模板的 prompt 必须内嵌 handoff-verdict 输出契约（spec §5：契约随模板版本化）
	if !strings.Contains(rv.Def.Prompt, "handoff-verdict") {
		t.Fatalf("审阅模板缺输出契约: %q", rv.Def.Prompt)
	}
	// 幂等 seed
	if err := s.EnsureDefaultTemplates(); err != nil {
		t.Fatal(err)
	}
	if tp2, _ := s.GetTemplate("feature-impl", 0); tp2.Version != 1 {
		t.Fatalf("seed 不幂等: v%d", tp2.Version)
	}
	// put 出新版、旧版可读（与 workflow 同构）
	def := tp.Def
	def.Executor = "codex"
	v, err := s.PutTemplate("feature-impl", def)
	if err != nil || v != 2 {
		t.Fatalf("put v2: %d %v", v, err)
	}
	if old, _ := s.GetTemplate("feature-impl", 1); old.Def.Executor != "opencode" {
		t.Fatalf("v1 被改: %+v", old.Def)
	}
	// per-target 模型覆盖可表达（codex 模型名按机器不同是实测教训）
	def.ModelByTarget = map[string]string{"mac-02": "gpt-5.6-luna", "win-b37": "deepseek-v4-pro"}
	if _, err := s.PutTemplate("feature-impl", def); err != nil {
		t.Fatalf("model override: %v", err)
	}
	tp3, _ := s.GetTemplate("feature-impl", 0)
	if tp3.Def.ModelByTarget["mac-02"] != "gpt-5.6-luna" {
		t.Fatalf("覆盖丢失: %+v", tp3.Def)
	}
}
```

- [ ] **Step 2: 实现 templates.go**

```go
// DispatchTemplate 聚合：派发配方（executor/纪律块/prompt/目标机/分支
// 命名/模型覆盖），不可变版本化，与 workflow 同构。分支策略只管工作
// 分支**命名**——基线从卡的 base_branch 来（蓝图 §3.3）。
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
	Executor       string            `json:"executor"`         // opencode|codex|grok|claude
	Target         string            `json:"target"`           // 默认目标机（CLI --target 可覆盖）
	Purpose        string            `json:"purpose"`          // implement|review|merge
	BranchPrefix   string            `json:"branch_prefix"`    // 工作分支命名：<prefix>/<卡id>-<purpose>
	Prompt         string            `json:"prompt"`           // prompt 模板，{{TITLE}}/{{CARD}}/{{ACCEPT}} 占位
	DisciplinePath string            `json:"discipline_path"`  // 纪律块文件路径
	ModelByTarget  map[string]string `json:"model_by_target,omitempty"` // per-target 模型覆盖
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
	"只输出一个该 block；解析不到会转人工，不要省略。"

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
			DisciplinePath: "docs/superpowers/discipline/block-b.md",
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
```

（纪律块文件：Task 5 的 dispatch 实现里，DisciplinePath 不存在时报错并提示先落文件——**把 CLAUDE.md §4 的 A/B 版纪律块正文抄成 `docs/superpowers/discipline/block-a.md` / `block-b.md` 是本 Task 的一部分**，两个文件原文取自仓库根 `~/.claude/CLAUDE.md` §4 的两个代码块；执行者无法读 `~/.claude` 时，在 plan 派发包里随附原文——审核者派发前把两份纪律块文本追加到本 plan 附录。）

- [ ] **Step 3: 跑测试 + Commit**

Run: `go test ./internal/ledger/ -run TestTemplate -v` → PASS

```bash
git add internal/ledger/ docs/superpowers/discipline/
git commit -m "feat(ledger): DispatchTemplate 聚合——版本化/出厂默认/审阅输出契约随模板版本化"
```

---

### Task 2: cmd/template.go——template 命令族

**Files:**
- Create: `cmd/template.go`
- Test: `cmd/template_test.go`

- [ ] **Step 1: 测试**

```go
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTemplateListShowPut(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "template", "list")
	if err != nil || !strings.Contains(out, "feature-impl") || !strings.Contains(out, "review-generic") {
		t.Fatalf("list: %v %q", err, out)
	}
	out, _, err = runLedgerCLI(t, dir, "template", "show", "review-generic")
	if err != nil || !strings.Contains(out, "handoff-verdict") {
		t.Fatalf("show 应含输出契约: %v %q", err, out)
	}
	p := filepath.Join(dir, "tpl.json")
	_ = os.WriteFile(p, []byte(`{"executor":"codex","purpose":"implement","branch_prefix":"cards",
		"prompt":"x {{CARD}}","discipline_path":"docs/superpowers/discipline/block-b.md"}`), 0o644)
	out, _, err = runLedgerCLI(t, dir, "template", "put", "codex-impl", "--file", p)
	if err != nil || !strings.Contains(out, `"version":1`) {
		t.Fatalf("put: %v %q", err, out)
	}
}
```

- [ ] **Step 2: 实现 cmd/template.go**

与 `cmd/workflow.go` 同构（list 走 ListTemplateNames + GetTemplate 最新版 tabwriter；show 单 JSON 带 `--version`；put 读 `--file` JSON、校验 Executor/Prompt/DisciplinePath 非空后 PutTemplate）。`openLedger` 之后补一行 `st.EnsureDefaultTemplates()`（与 EnsureDefaultWorkflows 并列——**改在 `cmd/ledgercli.go` 的 openLedger 里统一做**，两处 seed 一个入口）。代码结构完全照抄 Task 4 的 workflow.go（同文件三动词 + init 注册 `templateCmd` 进 rootCmd），此处不重复贴——**唯一差异点**：put 的校验块：

```go
		if def.Executor == "" || def.Prompt == "" || def.DisciplinePath == "" {
			return fmt.Errorf("executor/prompt/discipline_path 三者必填")
		}
```

- [ ] **Step 3: 跑测试 + Commit**

Run: `go test ./cmd/ -run TestTemplate -v` → PASS

```bash
git add cmd/
git commit -m "feat(cli): template list/show/put——派发模板命令面，openLedger 统一 seed 两聚合默认"
```

---

### Task 3: verdict.go——裁决解析（纯函数）

**Files:**
- Create: `internal/ledgernode/verdict.go`
- Test: `internal/ledgernode/verdict_test.go`

- [ ] **Step 1: 测试**

```go
package ledgernode

import "testing"

func TestParseVerdict(t *testing.T) {
	pass := "审阅完成。\n```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```\n"
	v, err := ParseVerdict(pass)
	if err != nil || !v.Pass {
		t.Fatalf("pass: %v %+v", err, v)
	}
	fail := "有问题。\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[{\"severity\":\"major\",\"summary\":\"CAS 缺前值\",\"file\":\"a.go\"}],\"notes\":\"n\"}\n```"
	v, err = ParseVerdict(fail)
	if err != nil || v.Pass || len(v.Findings) != 1 || v.Findings[0].Severity != "major" {
		t.Fatalf("fail: %v %+v", err, v)
	}
	// 取最后一个 block（executor 中途引用示例的防御）
	two := "示例：\n```handoff-verdict\n{\"verdict\":\"pass\"}\n```\n真裁决：\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```"
	if v, _ = ParseVerdict(two); v.Pass {
		t.Fatalf("应取最后一个 block: %+v", v)
	}
	// 缺失/坏 JSON/未知 verdict 值：解析失败不猜
	for _, bad := range []string{
		"没有 block",
		"```handoff-verdict\n{broken\n```",
		"```handoff-verdict\n{\"verdict\":\"maybe\"}\n```",
	} {
		if _, err := ParseVerdict(bad); err == nil {
			t.Fatalf("应解析失败: %q", bad)
		}
	}
}
```

- [ ] **Step 2: 实现 verdict.go**

```go
// Package ledgernode 节点执行器：审阅/合并节点的唯一实现，主会话
// （经 CLI）与看板按钮（经 Plan D API）共用，三期规则引擎复用。
// 边界：无自有状态——回合计数从事件流推导，全部写入经 internal/ledger。
package ledgernode

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Finding 审阅发现项。
type Finding struct {
	Severity string `json:"severity"` // major|minor
	Summary  string `json:"summary"`
	File     string `json:"file,omitempty"`
}

// Verdict 解析后的裁决。
type Verdict struct {
	Pass     bool      `json:"pass"`
	Findings []Finding `json:"findings"`
	Notes    string    `json:"notes,omitempty"`
	Raw      string    `json:"-"` // block 原文（落账取证）
}

var verdictBlockPat = regexp.MustCompile("(?s)```handoff-verdict\\s*\\n(.*?)\\n?```")

// ParseVerdict 从审阅报文提取最后一个 handoff-verdict block 并解析。
// 解析失败不猜（调用方转「等人」，原文落 timeline）——spec §5 契约。
func ParseVerdict(message string) (Verdict, error) {
	ms := verdictBlockPat.FindAllStringSubmatch(message, -1)
	if len(ms) == 0 {
		return Verdict{}, fmt.Errorf("报文中没有 handoff-verdict block")
	}
	raw := strings.TrimSpace(ms[len(ms)-1][1]) // 最后一个：防中途引用示例
	var wire struct {
		Verdict  string    `json:"verdict"`
		Findings []Finding `json:"findings"`
		Notes    string    `json:"notes"`
	}
	if err := json.Unmarshal([]byte(raw), &wire); err != nil {
		return Verdict{}, fmt.Errorf("裁决 JSON 解析失败: %w（原文: %.200s）", err, raw)
	}
	switch wire.Verdict {
	case "pass":
		return Verdict{Pass: true, Findings: wire.Findings, Notes: wire.Notes, Raw: raw}, nil
	case "fail":
		return Verdict{Pass: false, Findings: wire.Findings, Notes: wire.Notes, Raw: raw}, nil
	}
	return Verdict{}, fmt.Errorf("verdict 值 %q 不在 {pass,fail}", wire.Verdict)
}
```

- [ ] **Step 3: 跑测试 + Commit**

```bash
git add internal/ledgernode/
git commit -m "feat(ledgernode): handoff-verdict 解析——取最后 block、失败不猜"
```

---

### Task 4: rounds.go——回合计数（事件流推导 + 人工重置）

**Files:**
- Create: `internal/ledgernode/rounds.go`
- Test: `internal/ledgernode/rounds_test.go`

- [ ] **Step 1: 测试**

```go
package ledgernode

import (
	"encoding/json"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func ev(typ string, payload map[string]any) ledger.Event {
	raw, _ := json.Marshal(payload)
	return ledger.Event{Type: typ, Payload: raw}
}

func TestCountRounds(t *testing.T) {
	evs := []ledger.Event{
		ev(ledger.EvReviewVerdict, map[string]any{"node": "review", "pass": false}),
		ev(ledger.EvReviewVerdict, map[string]any{"node": "review", "pass": false}),
	}
	if n := CountRounds(evs, "review"); n != 2 {
		t.Fatalf("回合数: %d", n)
	}
	// 人工插手重置：human_reset 事件后重新计数（spec §5：人工介入是新基线）
	evs = append(evs, ev(ledger.EvComment, map[string]any{"kind": "普通", "body": "人工 continue", "human_reset_node": "review"}))
	evs = append(evs, ev(ledger.EvReviewVerdict, map[string]any{"node": "review", "pass": false}))
	if n := CountRounds(evs, "review"); n != 1 {
		t.Fatalf("重置后回合数: %d", n)
	}
	// 不同节点互不影响
	if n := CountRounds(evs, "merge"); n != 0 {
		t.Fatalf("异节点: %d", n)
	}
}
```

- [ ] **Step 2: 实现 rounds.go**

```go
// 回合计数：卡 × 节点粒度，从事件流推导，不存内存（spec §5——恢复
// 现场以账本为准，不信记忆）。规则：数该节点的 review_verdict 事件；
// 遇到带 human_reset_node=<节点> 的 comment 事件即清零（人工介入是
// 新基线，落事件注明）。
package ledgernode

import (
	"encoding/json"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// MaxRounds 默认封顶回合数，超限转「等人」。
const MaxRounds = 3

// CountRounds 数 node 自最近一次人工重置以来的裁决回合数。
// evs 必须按 seq 升序（EventsFromAsc 的自然输出）。
func CountRounds(evs []ledger.Event, node string) int {
	n := 0
	for _, e := range evs {
		var p map[string]any
		if err := json.Unmarshal(e.Payload, &p); err != nil {
			continue // 坏 payload 不参与计数，宁少勿多
		}
		switch e.Type {
		case ledger.EvReviewVerdict:
			if p["node"] == node {
				n++
			}
		case ledger.EvComment:
			if p["human_reset_node"] == node {
				n = 0
			}
		}
	}
	return n
}
```

- [ ] **Step 3: 跑测试 + Commit**

```bash
git add internal/ledgernode/
git commit -m "feat(ledgernode): 回合计数——事件流推导、人工插手清零、封顶常量"
```

---

### Task 5: card dispatch——派发即认领 + 快照落账

**Files:**
- Create: `cmd/card_dispatch.go`
- Modify: `internal/ledger/events.go`（追加 RecordDispatch）
- Test: `cmd/card_dispatch_test.go`、`internal/ledger/events_test.go`（追加）

- [ ] **Step 1: 账本库测试（events_test.go 追加）**

```go
func TestRecordDispatch(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "要派的卡")
	err := s.RecordDispatch(c.ID, DispatchSnapshot{
		Template: "feature-impl", TemplateVersion: 1, DisciplineHash: "1f3c9d",
		Target: "mac-02", TaskID: "T9", Branch: "cards/" + c.ID + "-implement",
		PlanPath: "plans/x.md", Actor: "cli:me@host",
	})
	if err != nil {
		t.Fatalf("record: %v", err)
	}
	evs, _ := s.EventsFromAsc([]string{c.ID}, 0, 10)
	found := false
	for _, e := range evs {
		if e.Type == EvDispatched {
			found = true
			var p map[string]any
			_ = json.Unmarshal(e.Payload, &p)
			if p["discipline_hash"] != "1f3c9d" || p["template_version"] != float64(1) {
				t.Fatalf("快照字段: %+v", p)
			}
		}
	}
	if !found {
		t.Fatal("缺 dispatched 事件")
	}
}
```

- [ ] **Step 2: 实现 RecordDispatch（events.go 追加）**

```go
// DispatchSnapshot 派发事件快照：模板版本 + 纪律块 hash + 落点。
// 「B107 那次派发用的哪版纪律块」从这里答（蓝图 §3.3 取证文化）。
type DispatchSnapshot struct {
	Template        string `json:"template"`
	TemplateVersion int    `json:"template_version"`
	DisciplineHash  string `json:"discipline_hash"`
	Target          string `json:"target"`
	TaskID          string `json:"task_id"`
	Branch          string `json:"branch"`
	PlanPath        string `json:"plan_path,omitempty"` // plan 附件挂派发事件（一 spec 多 plan）
	Actor           string `json:"-"`
}

// RecordDispatch 落派发事件。
func (s *Store) RecordDispatch(cardID string, snap DispatchSnapshot) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("派发落账: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvDispatched, snap.Actor, snap)
		return err
	})
}
```

- [ ] **Step 3: CLI 测试（card_dispatch_test.go）**

`client.Dispatch` 是网络调用——CLI 层测试只测**派发前逻辑**（认领 CAS、模板解析、纪律块 hash、prompt 拼装），网络段经函数变量注入假实现（照抄 `dispatchAfterTerminal`/`openTerminal` 的测试缝模式）：

```go
package cmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCardDispatchClaimAndSnapshot(t *testing.T) {
	dir := t.TempDir()
	// 纪律块文件就位（hash 快照的对象）
	dp := filepath.Join(dir, "block-a.md")
	_ = os.WriteFile(dp, []byte("# 执行纪律\n1. 逐 task 派 subagent。"), 0o644)

	out, _, _ := runLedgerCLI(t, dir, "card", "add", "要派的卡", "--project", "demo", "--workflow", "bug")
	var c struct{ ID string `json:"id"` }
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &c)
	_, _, _ = runLedgerCLI(t, dir, "card", "update", c.ID, "--accept", "测试全绿")

	// 假派发通道：记录收到的 prompt，返回固定 task
	var gotPrompt string
	restore := swapDispatchTransport(func(prompt, branch, target string) (string, error) {
		gotPrompt = prompt
		return "T-fake-1", nil
	})
	defer restore()

	out, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID,
		"--template", "feature-impl", "--target", "mac-02", "--discipline-override", dp)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if !strings.Contains(out, "T-fake-1") {
		t.Fatalf("输出应含 task id: %q", out)
	}
	// prompt 拼装：纪律块在最前 + 占位替换
	if !strings.HasPrefix(gotPrompt, "# 执行纪律") || !strings.Contains(gotPrompt, "要派的卡") ||
		!strings.Contains(gotPrompt, "测试全绿") {
		t.Fatalf("prompt 拼装: %q", gotPrompt)
	}
	// 派发即认领：卡进「进行中」，第二次派发干净失败并提示认领者
	show, _, _ := runLedgerCLI(t, dir, "card", "show", c.ID)
	if !strings.Contains(show, `"Status":"进行中"`) && !strings.Contains(show, `"status":"进行中"`) {
		t.Fatalf("未认领: %q", show)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "dispatch", c.ID, "--template", "feature-impl",
		"--target", "mac-02", "--discipline-override", dp); err == nil ||
		!strings.Contains(err.Error(), "认领") {
		t.Fatalf("重复派发应报已认领: %v", err)
	}
	// 快照落账：dispatched 事件含模板版本 + 纪律 hash + task 回链
	if !strings.Contains(show, "dispatched") || !strings.Contains(show, "discipline_hash") {
		t.Fatalf("快照事件缺失: %q", show)
	}
}
```

- [ ] **Step 4: 实现 cmd/card_dispatch.go**

```go
// card dispatch：按模板拼装 prompt + 纪律块，走既有 dispatch 通道；
// 派发即认领（CAS 进「进行中」就是 claim，第二个会话干净失败）；
// task 回链 + 模板版本/纪律 hash 快照落事件。
package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/spf13/cobra"
)

var (
	cardDispatchTemplate   string
	cardDispatchTarget     string
	cardDispatchPlan       string
	cardDispatchDiscipline string // 测试/应急用：覆盖模板的纪律块路径
)

// dispatchTransport 网络段的测试缝：生产走 client.Dispatch，测试换假。
// 返回 task id。
var dispatchTransport = func(prompt, branch, target string) (string, error) {
	addr, token, err := Endpoints(target) // 既有 helper，签名以 root.go 为准
	if err != nil {
		return "", err
	}
	task, err := client.New(addr, token).Dispatch(nil, client.DispatchOpts{ // ctx 参数按实际签名传
		Prompt: prompt, Target: target, NewBranch: branch,
	})
	if err != nil {
		return "", err
	}
	return task.ID, nil
}

// swapDispatchTransport 测试注入。
func swapDispatchTransport(fn func(prompt, branch, target string) (string, error)) func() {
	old := dispatchTransport
	dispatchTransport = fn
	return func() { dispatchTransport = old }
}

var cardDispatchCmd = &cobra.Command{
	Use:   "dispatch <id>",
	Short: "按模板派发（派发即认领；快照模板版本与纪律块 hash）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		id, actor := args[0], ledgerActor()
		c, err := st.GetCard(id)
		if err != nil {
			return err
		}
		tpl, err := st.GetTemplate(cardDispatchTemplate, 0)
		if err != nil {
			return fmt.Errorf("取模板: %w", err)
		}
		target := cardDispatchTarget
		if target == "" {
			target = tpl.Def.Target
		}
		if target == "" {
			return fmt.Errorf("目标机未定：--target 或模板 target 至少一个")
		}
		// 纪律块：读文件、算 hash（快照对象是内容不是路径）
		dpath := tpl.Def.DisciplinePath
		if cardDispatchDiscipline != "" {
			dpath = cardDispatchDiscipline
		}
		discipline, err := os.ReadFile(dpath)
		if err != nil {
			return fmt.Errorf("读纪律块 %s（先把纪律块文件落库，见模板定义）: %w", dpath, err)
		}
		sum := sha256.Sum256(discipline)
		dhash := hex.EncodeToString(sum[:])[:12]

		// 派发即认领：CAS 进「进行中」。已在「进行中」= 已被认领。
		if c.Status == ledger.StatusDoing {
			return fmt.Errorf("卡 %s 已被认领（驱动 %s）", id, c.DriverSession)
		}
		if err := st.MoveCard(id, ledger.StatusDoing, c.Status, actor); err != nil {
			return fmt.Errorf("认领失败（可能被并发抢先）: %w", err)
		}
		if err := st.ClaimDriver(id, actor); err != nil {
			return fmt.Errorf("认领驱动: %w", err)
		}

		// prompt = 纪律块 + 模板占位替换（{{TITLE}}/{{CARD}}/{{ACCEPT}}）
		body := strings.NewReplacer(
			"{{TITLE}}", c.Title, "{{CARD}}", c.ID, "{{ACCEPT}}", c.AcceptanceCriteria,
		).Replace(tpl.Def.Prompt)
		prompt := string(discipline) + "\n\n---\n\n" + body

		branch := fmt.Sprintf("%s/%s-%s", tpl.Def.BranchPrefix, c.ID, tpl.Def.Purpose)
		taskID, err := dispatchTransport(prompt, branch, target)
		if err != nil {
			// 派发失败回滚认领：不留「进行中却无 task」的假账
			_ = st.MoveCard(id, c.Status, ledger.StatusDoing, actor)
			return fmt.Errorf("派发: %w", err)
		}
		if err := st.LinkTask(id, target, taskID, tpl.Def.Purpose, actor); err != nil {
			return fmt.Errorf("回链挂账: %w", err)
		}
		if err := st.RecordDispatch(id, ledger.DispatchSnapshot{
			Template: tpl.Name, TemplateVersion: tpl.Version, DisciplineHash: dhash,
			Target: target, TaskID: taskID, Branch: branch, PlanPath: cardDispatchPlan, Actor: actor,
		}); err != nil {
			return fmt.Errorf("快照落账: %w", err)
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"card": id, "task": taskID, "target": target, "branch": branch,
			"template": tpl.Name, "template_version": tpl.Version, "discipline_hash": dhash,
		})
	},
}

func init() {
	cardDispatchCmd.Flags().StringVar(&cardDispatchTemplate, "template", "feature-impl", "派发模板名")
	cardDispatchCmd.Flags().StringVar(&cardDispatchTarget, "target", "", "目标机（覆盖模板）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchPlan, "plan", "", "plan 文件路径（挂派发事件）")
	cardDispatchCmd.Flags().StringVar(&cardDispatchDiscipline, "discipline-override", "", "覆盖纪律块路径（测试/应急）")
	cardCmd.AddCommand(cardDispatchCmd)
}
```

**执行者注意**：①`Endpoints`/`client.Dispatch` 的实际签名以 `cmd/root.go`、`internal/client/client.go` 为准（含 ctx、plan base64、model 覆盖参数——模板 `ModelByTarget[target]` 非空时传给 DispatchOpts.Model）；②基线分支：`EffectiveBaseBranch` 非空时传 `DispatchOpts.Base`，空 = 不传（沿用远端默认分支）；③`--plan` 非空时按既有 dispatch 的 planB64 路径随附文件。这三点都是对既有通道的**传参**，不新造逻辑。

- [ ] **Step 5: 跑测试 + Commit**

Run: `go test ./cmd/ -run TestCardDispatch -v && go test ./internal/ledger/ -run TestRecordDispatch -v` → PASS

```bash
git add cmd/ internal/ledger/
git commit -m "feat(cli): card dispatch——派发即认领(CAS+driver)/纪律块 hash 快照/失败回滚认领/基线传参"
```

---

### Task 6: node.go——审阅节点与合并节点

**Files:**
- Create: `internal/ledgernode/node.go`
- Test: `internal/ledgernode/node_test.go`

- [ ] **Step 1: 测试（fake 依赖，验证决策逻辑）**

```go
package ledgernode

import (
	"context"
	"strings"
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func nodeLedger(t *testing.T) (*ledger.Store, ledger.Card) {
	t.Helper()
	s, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	_ = s.EnsureDefaultWorkflows()
	_ = s.EnsureDefaultTemplates()
	c, _ := s.CreateCard(ledger.NewCard{Title: "被审卡", Project: "p", Workflow: "bug", Actor: "t"})
	return s, c
}

func TestReviewNodePassAndFailLoop(t *testing.T) {
	s, c := nodeLedger(t)
	msgs := []string{
		"第一轮\n```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[{\"severity\":\"major\",\"summary\":\"x\"}]}\n```",
		"第二轮\n```handoff-verdict\n{\"verdict\":\"pass\",\"findings\":[]}\n```",
	}
	i := 0
	n := &ReviewNode{St: s, Node: "review",
		RunReview: func(ctx context.Context, card ledger.Card) (string, error) {
			m := msgs[i]
			i++
			return m, nil
		},
	}
	// 第一轮 fail：落 review_verdict(fail)，返回 Continue 指示
	out, err := n.RunOnce(context.Background(), c.ID)
	if err != nil || out.Action != ActionContinue || len(out.Verdict.Findings) != 1 {
		t.Fatalf("round1: %v %+v", err, out)
	}
	// 第二轮 pass
	out, err = n.RunOnce(context.Background(), c.ID)
	if err != nil || out.Action != ActionPass {
		t.Fatalf("round2: %v %+v", err, out)
	}
	// 事件流：两条 review_verdict 可审计
	evs, _ := s.EventsFromAsc([]string{c.ID}, 0, 100)
	cnt := 0
	for _, e := range evs {
		if e.Type == ledger.EvReviewVerdict {
			cnt++
		}
	}
	if cnt != 2 {
		t.Fatalf("verdict 事件: %d", cnt)
	}
}

func TestReviewNodeRoundCapAndParseFailure(t *testing.T) {
	s, c := nodeLedger(t)
	failMsg := "```handoff-verdict\n{\"verdict\":\"fail\",\"findings\":[]}\n```"
	n := &ReviewNode{St: s, Node: "review",
		RunReview: func(ctx context.Context, card ledger.Card) (string, error) { return failMsg, nil }}
	for i := 0; i < MaxRounds; i++ {
		if out, err := n.RunOnce(context.Background(), c.ID); err != nil || out.Action != ActionContinue {
			t.Fatalf("round%d: %v %+v", i+1, err, out)
		}
	}
	// 第 4 次：封顶转等人（判据②的单测形）
	out, err := n.RunOnce(context.Background(), c.ID)
	if err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("封顶: %v %+v", err, out)
	}
	views, _ := s.ListCards(ledger.CardFilter{Project: "p", Needs: true})
	if len(views) != 1 || !strings.Contains(views[0].NeedsReason, "超轮") {
		t.Fatalf("等人标记: %+v", views)
	}

	// 解析失败不猜：坏报文 → 等人(裁决解析失败)，原文落 timeline
	s2, c2 := nodeLedger(t)
	n2 := &ReviewNode{St: s2, Node: "review",
		RunReview: func(ctx context.Context, card ledger.Card) (string, error) { return "没有 block 的报文", nil }}
	out, err = n2.RunOnce(context.Background(), c2.ID)
	if err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("解析失败处置: %v %+v", err, out)
	}
	evs, _ := s2.EventsFromAsc([]string{c2.ID}, 0, 100)
	rawSaved := false
	for _, e := range evs {
		if e.Type == ledger.EvComment && strings.Contains(string(e.Payload), "没有 block") {
			rawSaved = true
		}
	}
	if !rawSaved {
		t.Fatal("原文未落 timeline")
	}
}

func TestMergeNodeDecision(t *testing.T) {
	s, c := nodeLedger(t)
	// 基线 = 主线（空）：不自动合，推「待合并」不存在于 bug 流 → 转等人提示
	m := &MergeNode{St: s,
		Objective: func(ctx context.Context, card ledger.Card, base string) error { return nil },
		DoMerge:   func(ctx context.Context, card ledger.Card, base string) error { t.Fatal("main 不应自动合"); return nil },
	}
	out, err := m.RunOnce(context.Background(), c.ID)
	if err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("main 人工层: %v %+v", err, out)
	}
	// 基线 = 集成分支：客观判据过 → DoMerge 被调 → ActionMerged
	c2, _ := s.CreateCard(ledger.NewCard{Title: "集成线卡", Project: "p", Workflow: "bug",
		BaseBranch: "integration", Actor: "t"})
	called := false
	m2 := &MergeNode{St: s,
		Objective: func(ctx context.Context, card ledger.Card, base string) error { return nil },
		DoMerge: func(ctx context.Context, card ledger.Card, base string) error {
			called = true
			if base != "integration" {
				t.Fatalf("合并目标: %q", base)
			}
			return nil
		},
	}
	out, err = m2.RunOnce(context.Background(), c2.ID)
	if err != nil || out.Action != ActionMerged || !called {
		t.Fatalf("集成线自动合: %v %+v", err, out)
	}
	// 客观判据红：不合、转等人、原始报错落 timeline
	c3, _ := s.CreateCard(ledger.NewCard{Title: "红测试卡", Project: "p", Workflow: "bug",
		BaseBranch: "integration", Actor: "t"})
	m3 := &MergeNode{St: s,
		Objective: func(ctx context.Context, card ledger.Card, base string) error {
			return context.DeadlineExceeded // 任意错误代表测试红
		},
		DoMerge: func(ctx context.Context, card ledger.Card, base string) error { t.Fatal("红不应合"); return nil },
	}
	if out, err = m3.RunOnce(context.Background(), c3.ID); err != nil || out.Action != ActionNeedsHuman {
		t.Fatalf("客观判据红: %v %+v", err, out)
	}
}
```

- [ ] **Step 2: 实现 node.go**

```go
// 审阅节点与合并节点的执行体。依赖全部经函数字段注入（RunReview/
// Objective/DoMerge），决策逻辑与副作用分离——单测覆盖决策，真机
// 判据覆盖副作用（真派发/真 git）。
package ledgernode

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// Action 节点执行的结论。
type Action string

const (
	ActionPass       Action = "pass"        // 审阅通过，进下一节点
	ActionContinue   Action = "continue"    // 审阅 fail，发现项已落账，调用方 continue 修复
	ActionNeedsHuman Action = "needs_human" // 转等人（超轮/解析失败/main 层/判据红/冲突）
	ActionMerged     Action = "merged"      // 已自动合回基线
)

// Outcome 节点单次执行结果。
type Outcome struct {
	Action  Action
	Verdict Verdict // 仅审阅节点填
	Reason  string  // needs_human 的 reason
}

// ReviewNode 审阅节点。RunReview 跑一次审阅并返回最终报文（生产实现 =
// 按 review 模板派发 + wait 终态 + 取报文，Plan C 之后由主会话/看板注入；
// 本包不直接拨号）。
type ReviewNode struct {
	St        *ledger.Store
	Node      string // 节点名（回合计数的分组键）
	RunReview func(ctx context.Context, card ledger.Card) (string, error)
}

// RunOnce 执行一轮审阅：查回合 → 超限转等人 → 跑审阅 → 解析裁决 →
// 落账 → 给出下一步。
func (n *ReviewNode) RunOnce(ctx context.Context, cardID string) (Outcome, error) {
	lg := slog.Default().With("node", n.Node, "card", cardID)
	c, err := n.St.GetCard(cardID)
	if err != nil {
		return Outcome{}, err
	}
	evs, err := n.St.EventsFromAsc([]string{cardID}, 0, 10000)
	if err != nil {
		return Outcome{}, err
	}
	if r := CountRounds(evs, n.Node); r >= MaxRounds {
		reason := fmt.Sprintf("审阅超轮（%d/%d）", r, MaxRounds)
		lg.Info("回合封顶转等人", "rounds", r)
		if err := n.St.MarkNeedsHuman(cardID, reason, "node:"+n.Node); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
	}
	msg, err := n.RunReview(ctx, c)
	if err != nil {
		return Outcome{}, fmt.Errorf("跑审阅: %w", err)
	}
	v, perr := ParseVerdict(msg)
	if perr != nil {
		// 解析失败不猜：原文全文落 timeline 供人裁，转等人
		lg.Warn("裁决解析失败转等人", "err", perr)
		if _, err := n.St.AddComment(cardID, "裁决解析失败，审阅原文：\n"+msg, "普通", "node:"+n.Node); err != nil {
			return Outcome{}, err
		}
		if err := n.St.MarkNeedsHuman(cardID, "裁决解析失败", "node:"+n.Node); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: "裁决解析失败"}, nil
	}
	if err := n.St.RecordReviewVerdict(cardID, n.Node, v.Pass, v.Raw, "node:"+n.Node); err != nil {
		return Outcome{}, err
	}
	if v.Pass {
		lg.Info("审阅通过")
		return Outcome{Action: ActionPass, Verdict: v}, nil
	}
	lg.Info("审阅未过，进修复回路", "findings", len(v.Findings))
	return Outcome{Action: ActionContinue, Verdict: v}, nil
}

// MergeNode 合并节点。Objective 跑客观判据（测试+gofmt，生产实现在
// 集成工作区跑真命令）；DoMerge 执行合并（生产 = fetch task 分支 +
// merge + push）。两者注入，决策留此处：
//   基线 = main（空）      → 不自动合，转「待合并/等人」（人工层）
//   基线 = 集成分支         → 客观判据绿才合；红/冲突转等人，原错落 timeline
func (m *MergeNode) RunOnce(ctx context.Context, cardID string) (Outcome, error) {
	lg := slog.Default().With("node", "merge", "card", cardID)
	c, err := m.St.GetCard(cardID)
	if err != nil {
		return Outcome{}, err
	}
	base, err := m.St.EffectiveBaseBranch(cardID)
	if err != nil {
		return Outcome{}, err
	}
	if base == "" {
		reason := "基线是主线：合并永远人工"
		lg.Info("main 层不自动合", "card", cardID)
		if err := m.St.MarkNeedsHuman(cardID, reason, "node:merge"); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: reason}, nil
	}
	if err := m.Objective(ctx, c, base); err != nil {
		// 客观判据红：原始报错原文落 timeline，不归因不重试
		lg.Warn("客观判据红转等人", "err", err)
		if _, cerr := m.St.AddComment(cardID, "合并前客观判据未过：\n"+err.Error(), "普通", "node:merge"); cerr != nil {
			return Outcome{}, cerr
		}
		if err := m.St.MarkNeedsHuman(cardID, "合并判据未过", "node:merge"); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: "合并判据未过"}, nil
	}
	if err := m.DoMerge(ctx, c, base); err != nil {
		lg.Warn("合并执行失败转等人", "err", err)
		if _, cerr := m.St.AddComment(cardID, "合并失败（冲突清单/报错）：\n"+err.Error(), "普通", "node:merge"); cerr != nil {
			return Outcome{}, cerr
		}
		if err := m.St.MarkNeedsHuman(cardID, "合并冲突", "node:merge"); err != nil {
			return Outcome{}, err
		}
		return Outcome{Action: ActionNeedsHuman, Reason: "合并冲突"}, nil
	}
	lg.Info("已自动合回基线", "base", base)
	return Outcome{Action: ActionMerged}, nil
}

// MergeNode 见 RunOnce 文档。
type MergeNode struct {
	St        *ledger.Store
	Objective func(ctx context.Context, card ledger.Card, base string) error
	DoMerge   func(ctx context.Context, card ledger.Card, base string) error
}
```

配套在 `internal/ledger/events.go` 追加：

```go
// RecordReviewVerdict 落审阅裁决事件（node 是回合计数分组键，raw 是
// verdict block 原文取证）。
func (s *Store) RecordReviewVerdict(cardID, node string, pass bool, raw, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, cardID); err != nil {
			return fmt.Errorf("裁决落账: 卡 %s: %w", cardID, err)
		}
		_, err := s.appendEvent(tx, sink, cardID, EvReviewVerdict, actor,
			map[string]any{"node": node, "pass": pass, "raw": raw})
		return err
	})
}
```

- [ ] **Step 3: 跑测试 + Commit**

Run: `go test ./internal/ledgernode/ ./internal/ledger/ -v` → 全 PASS

```bash
git add internal/ledgernode/ internal/ledger/
git commit -m "feat(ledgernode): 审阅/合并节点执行体——超轮封顶/解析失败不猜/main 永远人工/客观判据先行"
```

---

### Task 7: 生产接线——审阅派发与合并的真实现（薄层）

**Files:**
- Create: `internal/ledgernode/wire.go`

节点执行体的三个注入点接到真通道。**本 Task 不写单测**（全是外部副作用），行为验证归真机判据；每个函数必须按 house rules 打足日志。

- [ ] **Step 1: 实现 wire.go**

```go
// 节点注入点的生产实现：审阅派发走 dispatch 通道 + wait 终态 + 取
// 报文；客观判据/合并在协调机本地工作区跑真命令。审阅 task 生命周期
// 在此收口（裁决落账后 done 归档，不留孤儿——spec §5）。
package ledgernode

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/Xsxdot/handoff/internal/client"
	"github.com/Xsxdot/handoff/internal/ledger"
)

// NewDispatchReview 生产 RunReview：按 review 模板派发审阅 task →
// wait 终态 → 取最终报文 → done 归档 → 返回报文。
// dispatch 函数复用 cmd 层的 card dispatch 逻辑（模板/纪律块/挂账），
// 经参数传入避免 import cmd。
func NewDispatchReview(st *ledger.Store,
	dispatch func(ctx context.Context, cardID, template string) (target, taskID string, err error),
	endpoints func(target string) (addr, token string, err error),
) func(ctx context.Context, card ledger.Card) (string, error) {
	return func(ctx context.Context, card ledger.Card) (string, error) {
		target, taskID, err := dispatch(ctx, card.ID, "review-generic")
		if err != nil {
			return "", fmt.Errorf("派发审阅: %w", err)
		}
		addr, token, err := endpoints(target)
		if err != nil {
			return "", err
		}
		cl := client.New(addr, token)
		// wait 终态（progress 过滤沿用既有语义）
		if _, err := cl.WaitEvent(ctx, taskID, false); err != nil {
			return "", fmt.Errorf("等审阅终态: %w", err)
		}
		msg, err := finalMessage(ctx, cl, taskID)
		if err != nil {
			return "", err
		}
		// 收口：裁决报文已到手，审阅 task done 归档，不留孤儿
		if err := cl.Done(ctx, taskID); err != nil { // 方法名/签名以 client 实际为准
			return msg, fmt.Errorf("归档审阅 task（报文已取到，仅归档失败）: %w", err)
		}
		return msg, nil
	}
}

// finalMessage 从 task 事件流取最终报文文本。事件类型与字段以
// internal/proto 实际为准（completed/failed 事件的 payload 含最终
// message；取不到时报错，绝不拿中间 progress 凑数）。
func finalMessage(ctx context.Context, cl *client.Client, taskID string) (string, error) {
	// 实现要点（执行者按 proto 实际结构写）：
	// 1) cl 侧拉全量事件（attach 快照或 EventsFromAsc 语义的客户端入口）；
	// 2) 找最后一条 completed/failed 事件，取其报文字段；
	// 3) 缺失即 error——上游会转「等人」，这正是想要的行为。
	return clientFinalMessage(ctx, cl, taskID)
}

// NewLocalObjective 生产客观判据：在协调机工作区 repoDir 内
// fetch + 在临时 worktree 里跑 go test 与 gofmt。任何非零退出返回
// 原始输出（上游原文落 timeline，不归因）。
func NewLocalObjective(repoDir string) func(ctx context.Context, card ledger.Card, base string) error {
	return func(ctx context.Context, card ledger.Card, base string) error {
		links, err := taskBranch(card) // 从最近 dispatched 事件取工作分支名
		if err != nil {
			return err
		}
		script := strings.Join([]string{
			"set -e",
			"git fetch origin " + links + " " + base,
			"tmp=$(mktemp -d)",
			"git worktree add \"$tmp\" origin/" + links,
			"trap 'git worktree remove --force \"$tmp\"' EXIT",
			"cd \"$tmp\"",
			"test -z \"$(gofmt -l .)\"",
			"go test ./...",
		}, "\n")
		out, err := exec.CommandContext(ctx, "bash", "-c", script).CombinedOutput()
		if err != nil {
			return fmt.Errorf("客观判据未过:\n%s", out)
		}
		_ = repoDir // cmd.Dir = repoDir，执行者接线时设置
		return nil
	}
}

// NewLocalMerge 生产合并：--no-ff 合 task 分支进基线并 push；冲突时
// abort 并返回冲突文件清单（原文落 timeline）。
func NewLocalMerge(repoDir string) func(ctx context.Context, card ledger.Card, base string) error {
	return func(ctx context.Context, card ledger.Card, base string) error {
		branch, err := taskBranch(card)
		if err != nil {
			return err
		}
		script := strings.Join([]string{
			"set -e",
			"git fetch origin " + branch + " " + base,
			"git checkout " + base,
			"git merge --no-ff origin/" + branch +
				" || { git diff --name-only --diff-filter=U; git merge --abort; exit 1; }",
			"git push origin " + base,
		}, "\n")
		out, err := exec.CommandContext(ctx, "bash", "-c", script).CombinedOutput()
		if err != nil {
			return fmt.Errorf("合并失败:\n%s", out)
		}
		_ = repoDir
		return nil
	}
}
```

**执行者注意（本 Task 的两处「以实际为准」）**：`clientFinalMessage` 与 `taskBranch` 是本 Task 内必须写实的两个函数——前者按 `internal/proto` 的 completed/failed 事件真实字段实现（写一个解析单测，fixture 取自真实事件 JSON）；后者从卡的最近一条 `dispatched` 事件 payload 取 `branch` 字段（有 Plan C Task 5 的快照在，纯查询）。两个 exec 脚本的 `cmd.Dir = repoDir` 要真的设上（上面 `_ = repoDir` 是给你留的显式记号，不设即 bug）。

- [ ] **Step 2: 构建 + Commit**

Run: `go build ./... && go vet ./internal/ledgernode/`

```bash
git add internal/ledgernode/
git commit -m "feat(ledgernode): 生产接线——审阅派发/finalMessage/本地客观判据/本地合并（冲突清单落账）"
```

---

### Task 8: 整包终审

- [ ] **Step 1:**

```bash
gofmt -l internal/ledger/ internal/ledgernode/ cmd/
go vet ./internal/ledger/ ./internal/ledgernode/ ./cmd/
go test ./... 2>&1 | tail -20
```

- [ ] **Step 2: 对照 spec §5 自检**

裁决 schema 与通道（最后 block、失败不猜、契约在模板里版本化）✓；回合计数（卡×节点、事件流推导、封顶 3、人工重置）✓；合并节点（客观判据先行、只合基线分支、main 永远人工、冲突清单落 timeline）✓；审阅 task 收口（done 归档）✓；派发即认领 + 快照 ✓；`--plan` 挂派发事件 ✓。

- [ ] **Step 3: Commit**

```bash
git add -A && git commit -m "test(node): 派发/节点执行器整包终审"
```

---

## Self-Review 记录

1. **决策与副作用分离**是本 plan 的主设计：ReviewNode/MergeNode 的全部分支逻辑有单测（超轮/解析失败/main 层/判据红/冲突），副作用（真派发、真 git）收在 wire.go 薄层 + 真机判据。
2. **已知妥协**：wire.go 的 `clientFinalMessage`/`taskBranch` 按「以实际为准」交给执行者写实（各配一个解析单测）——这是本 plan 唯一两处非全码步骤，因为 proto 事件字段名在摸底报告里没有精确记录，编造字段名比留给执行者对照真代码更危险；merge 脚本用 bash -c（协调机是 mac/linux；Windows 协调机暂不在一期范围）。
3. **真机判据归属（审核者本地）**：判据①（标准例全程一个 wait）②（3 轮封顶真机复现）；纪律块文件（block-a/b.md）正文从 CLAUDE.md §4 抄录入库——审核者在派发前完成或在派发包附录随附。
