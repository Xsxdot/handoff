# 存量迁移 + skill 切换（Plan E / B156.1-迁移）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把 `docs/superpowers/backlog.md`（web-console 汇流点分支上的真实总账，~160 条）迁入账本库：解析器 + 幂等导入命令 + 抽查反查工具。冻结注记与 skill 切换是**审核者本地步骤**（改 `~/.claude` 与汇流点分支，不派发），以清单形式附后。

**Architecture:** 新包 `internal/backlogmig`：markdown 表解析（10 列含「线」列）→ 迁移条目（状态映射/引用解析/验收拆分）→ 经 `internal/ledger` 落库。导入命令 `handoff card import-backlog` 藏在 card 族下（Hidden），`--dry-run` 缺省真跑需 `--commit`。**历史 B 号原样保留**（cards.id 就是 B 号，映射表 = 恒等 + 点号子卡的 parent 推导，落 ledger_meta 记迁移批次指纹）；历史共享 spec 的批次**不重建合并关系**（spec §8：考古够用，别造史）。幂等：已存在的卡 id 跳过（重跑安全）。

**Tech Stack:** Go 标准库（解析纯手写，表格式是自家的，不引 markdown 库）。

**前置条件：** Plan A/A2 已合入。**迁移执行前**（不是本 plan 代码完成前）：①汇流点分支实测 merge-base 无分叉遗漏（memory 教训：判分叉用实测不靠行数）；②备份 backlog.md。

---

## File Structure

```
internal/backlogmig/
  parse.go        // 表行解析：ID/线/标题/状态/优先级/Spec/原型/验收/变更痕迹/备注
  parse_test.go   // fixture = 真实总账里形态各异的行（emoji 态/点号/删除线/多链接）
  convert.go      // 解析行 → 账本操作序列（状态映射/引用抽取/事件生成）
  convert_test.go
  importer.go     // 幂等导入（dry-run 报告 / commit 落库）
  importer_test.go
cmd/
  card_import.go  // handoff card import-backlog（Hidden）
  card_import_test.go
```

---

### Task 1: parse.go——总账表解析

**Files:**
- Create: `internal/backlogmig/parse.go`
- Test: `internal/backlogmig/parse_test.go`

- [ ] **Step 1: 测试（fixture 行取自真实总账的形态族）**

```go
package backlogmig

import "testing"

// fixture 覆盖真实总账的行形态族：emoji 状态、done(已验/未验)、点号子卡、
// epic、搁置、Spec 链接、验收长文、备注含「属 B5」「见 B17」引用。
const fixture = `# Handoff 需求总账

## Backlog

| ID | 线 | 标题 | 状态 | 优先级 | Spec | 原型/流程图 | 验收 | 变更痕迹 | 备注 |
|----|----|------|------|--------|------|------------|------|---------|------|
| B5 | 共同 | 订单系统重写 | 📦 epic | 高 | — | — | — | — | v1 |
| B5.1 | 共同 | 下单流程 | 📋 specced | 高 | [spec](specs/order-create.md) | [原型](../../prototypes/x/) | — | — | 属 B5 |
| B6 | 共同 | 登录页改版 | 🔨 doing | 中 | — | — | — | 因 B9 回改 06-19 | 领于 06-19，见 B17 |
| B7 | 共同 | 修个小 bug | ✅ done(已验) | 低 | — | — | go test ./... (ok) 08-01 | — | — |
| B8 | 共同 | 又一个 | ✅ done(未验) | 低 | — | — | 未提供测试证据 08-02 | — | — |
| B9 | 共同 | 搁下的 | 🗄️ shelved | 中 | — | — | — | — | 等 B5 落地再说 |
| B10 | 共同 | 刚记的 | 💡 idea | 中 | — | — | — | — | — |

## 待验证的空白

- 这段不是表，不解析。
`

func TestParseBacklog(t *testing.T) {
	rows, err := Parse([]byte(fixture))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(rows) != 7 {
		t.Fatalf("行数 %d != 7", len(rows))
	}
	byID := map[string]Row{}
	for _, r := range rows {
		byID[r.ID] = r
	}
	if r := byID["B5.1"]; r.Parent != "B5" || r.State != "📋 specced" || r.SpecPath != "specs/order-create.md" {
		t.Fatalf("B5.1: %+v", r)
	}
	if r := byID["B7"]; r.State != "✅ done(已验)" || r.Acceptance == "" {
		t.Fatalf("B7: %+v", r)
	}
	if r := byID["B6"]; len(r.Refs) == 0 || r.Refs[0] != "B17" {
		t.Fatalf("B6 引用: %+v", r) // 备注「见 B17」→ Refs；「属 B5」由 Parent 承担不重复
	}
	if r := byID["B9"]; r.Notes == "" {
		t.Fatalf("B9 备注: %+v", r)
	}
}

func TestParseRejectsMalformed(t *testing.T) {
	// 列数不对的行：报错带行号，不静默跳过（静默跳行 = 迁移丢数据）
	bad := "| ID | 线 | 标题 | 状态 | 优先级 | Spec | 原型/流程图 | 验收 | 变更痕迹 | 备注 |\n" +
		"|-|-|-|-|-|-|-|-|-|-|\n| B1 | 只有两列 |\n"
	if _, err := Parse([]byte(bad)); err == nil {
		t.Fatal("坏行应报错")
	}
}
```

- [ ] **Step 2: 实现 parse.go**

```go
// Package backlogmig：markdown 总账 → 账本库的一次性迁移工具。
// 解析只认自家表格式（10 列，表头以 "| ID | 线 |" 开头）；表外内容
// （编号说明、待验证的空白）一概不解析。坏行报错带行号——迁移宁停
// 不丢。
package backlogmig

import (
	"fmt"
	"regexp"
	"strings"
)

// Row 总账一行的原始字段 + 少量解析产物。
type Row struct {
	ID, Line, Title, State, Priority string
	SpecPath, ProtoLink              string
	Acceptance, Changes, Notes       string
	Parent                           string   // 点号推导：B5.1 → B5
	Refs                             []string // 备注/变更痕迹里的 B 号引用（不含 Parent）
}

var (
	linkPat = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
	refPat  = regexp.MustCompile(`B\d+(?:\.\d+)*`)
)

// Parse 解析总账全文，返回表内全部行。
func Parse(src []byte) ([]Row, error) {
	lines := strings.Split(string(src), "\n")
	var rows []Row
	inTable := false
	for i, ln := range lines {
		t := strings.TrimSpace(ln)
		if strings.HasPrefix(t, "| ID | 线 |") {
			inTable = true
			continue
		}
		if !inTable {
			continue
		}
		if t == "" || !strings.HasPrefix(t, "|") {
			inTable = false // 表结束
			continue
		}
		if strings.HasPrefix(t, "|-") || strings.HasPrefix(t, "| -") || strings.HasPrefix(t, "|--") {
			continue // 分隔行
		}
		cells := splitRow(t)
		if len(cells) != 10 {
			return nil, fmt.Errorf("第 %d 行列数 %d != 10（迁移宁停不丢）: %.80s", i+1, len(cells), t)
		}
		r := Row{ID: cells[0], Line: cells[1], Title: cells[2], State: cells[3],
			Priority: cells[4], Acceptance: dash(cells[7]), Changes: dash(cells[8]), Notes: dash(cells[9])}
		if m := linkPat.FindStringSubmatch(cells[5]); m != nil {
			r.SpecPath = m[1]
		}
		if m := linkPat.FindStringSubmatch(cells[6]); m != nil {
			r.ProtoLink = m[1]
		}
		if dot := strings.LastIndex(r.ID, "."); dot > 0 {
			r.Parent = r.ID[:dot]
		}
		// 引用抽取：备注 + 变更痕迹里的 B 号，去掉自身与父卡
		seen := map[string]bool{r.ID: true, r.Parent: true}
		for _, ref := range refPat.FindAllString(r.Notes+" "+r.Changes, -1) {
			if !seen[ref] {
				seen[ref] = true
				r.Refs = append(r.Refs, ref)
			}
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("没解析到任何表行（表头变了？）")
	}
	return rows, nil
}

// splitRow 按 | 切列并 trim（首尾空列丢弃）。
func splitRow(t string) []string {
	parts := strings.Split(t, "|")
	if len(parts) >= 2 {
		parts = parts[1 : len(parts)-1]
	}
	out := make([]string, len(parts))
	for i, p := range parts {
		out[i] = strings.TrimSpace(p)
	}
	return out
}

// dash "—" 归一为空串。
func dash(s string) string {
	if s == "—" || s == "-" {
		return ""
	}
	return s
}
```

- [ ] **Step 3: 跑测试 + Commit**

```bash
git add internal/backlogmig/
git commit -m "feat(backlogmig): 总账表解析——10 列/点号父卡/引用抽取/坏行报错带行号"
```

---

### Task 2: convert.go——状态映射与账本操作序列

**Files:**
- Create: `internal/backlogmig/convert.go`
- Test: `internal/backlogmig/convert_test.go`

- [ ] **Step 1: 测试**

```go
package backlogmig

import "testing"

func TestConvertStateMapping(t *testing.T) {
	// spec §8 状态映射表逐条钉死
	cases := []struct {
		state, wantStatus, wantReason string
		wantVerified                  *bool
	}{
		{"💡 idea", "待办", "", nil},
		{"📦 epic", "待办", "", nil}, // epic 无独立态：进「待办」，epic 性由子卡存在表达
		{"📋 specced", "已出spec", "", nil},
		{"🔨 doing", "进行中", "", nil},
		{"✅ done(已验)", "已完成", "", boolp(true)},
		{"✅ done(未验)", "已完成", "", boolp(false)},
		{"🗄️ shelved", "终止", "搁置", nil},
		{"🧊", "终止", "搁置", nil},
	}
	for _, c := range cases {
		e, err := Convert(Row{ID: "B1", Title: "t", State: c.state, Priority: "中"})
		if err != nil {
			t.Fatalf("%s: %v", c.state, err)
		}
		if e.Status != c.wantStatus || e.TerminateReason != c.wantReason {
			t.Fatalf("%s → %+v", c.state, e)
		}
		if (e.Verified == nil) != (c.wantVerified == nil) ||
			(e.Verified != nil && *e.Verified != *c.wantVerified) {
			t.Fatalf("%s verified: %+v", c.state, e.Verified)
		}
	}
	// 未知状态报错不猜
	if _, err := Convert(Row{ID: "B1", State: "🤷 不认识"}); err == nil {
		t.Fatal("未知状态应报错")
	}
}

func TestConvertWorkflowAndComment(t *testing.T) {
	// 有 spec / 到过 specced 的走 feature 流；纯 idea/done 小修走 bug 流也能表达
	e, _ := Convert(Row{ID: "B1", Title: "t", State: "📋 specced", SpecPath: "specs/x.md", Priority: "高"})
	if e.Workflow != "feature" || e.SpecPath != "specs/x.md" {
		t.Fatalf("feature 流: %+v", e)
	}
	// 变更痕迹 + 备注 → 首条 comment 原文保留
	e, _ = Convert(Row{ID: "B2", Title: "t", State: "💡 idea", Priority: "中",
		Changes: "因 B9 回改 06-19", Notes: "领于 06-19"})
	if e.FirstComment == "" || !contains(e.FirstComment, "因 B9 回改") || !contains(e.FirstComment, "领于") {
		t.Fatalf("首评论: %q", e.FirstComment)
	}
}
```

（`boolp`/`contains` 为测试内三行 helper。）

- [ ] **Step 2: 实现 convert.go**

```go
// 解析行 → 迁移条目：状态映射（spec §8 映射表的唯一落码点）、工作流
// 归属、首条 comment 组装。未知状态报错不猜——真总账若出现映射表外
// 的态，是映射表该补，不是迁移该编。
package backlogmig

import (
	"fmt"
	"strings"
)

// Entry 一条可落库的迁移条目。
type Entry struct {
	ID, Title, Status, TerminateReason, Priority, Parent string
	Workflow                                             string
	SpecPath                                             string
	Acceptance                                           string // → acceptance_criteria
	Verified                                             *bool  // 非 nil → 落 acceptance_recorded 事件
	FirstComment                                         string // 变更痕迹 + 备注原文
	Refs                                                 []string
}

// Convert 单行转换。
func Convert(r Row) (Entry, error) {
	e := Entry{ID: r.ID, Title: r.Title, Priority: r.Priority, Parent: r.Parent,
		SpecPath: r.SpecPath, Acceptance: r.Acceptance, Refs: r.Refs}
	if e.Priority == "" {
		e.Priority = "中"
	}
	st := strings.TrimSpace(r.State)
	switch {
	case strings.HasPrefix(st, "💡"), strings.HasPrefix(st, "📦"):
		e.Status = "待办"
	case strings.HasPrefix(st, "📋"):
		e.Status = "已出spec"
	case strings.HasPrefix(st, "🔨"):
		e.Status = "进行中"
	case strings.HasPrefix(st, "✅"):
		e.Status = "已完成"
		v := strings.Contains(st, "已验")
		e.Verified = &v
	case strings.HasPrefix(st, "🗄"), strings.HasPrefix(st, "🧊"):
		e.Status = "终止"
		e.TerminateReason = "搁置"
	default:
		return Entry{}, fmt.Errorf("卡 %s 状态 %q 不在映射表（补映射，别编）", r.ID, r.State)
	}
	// 工作流归属：到过 spec 关口（specced/有 spec 链接）→ feature；其余 bug 流
	// （bug 流无「已出spec」态，feature 流才有——归属错会让状态不在 States 里，
	//  importer 落库时会被 gate…不，导入走 ImportCard 直插不走 Move，因此这里
	//  必须自洽：Status=已出spec ⇒ Workflow=feature）
	if e.Status == "已出spec" || e.SpecPath != "" {
		e.Workflow = "feature"
	} else {
		e.Workflow = "bug"
	}
	var parts []string
	if r.Changes != "" {
		parts = append(parts, "变更痕迹："+r.Changes)
	}
	if r.Notes != "" {
		parts = append(parts, "备注："+r.Notes)
	}
	if r.Line != "" && r.Line != "共同" {
		parts = append(parts, "线："+r.Line)
	}
	if r.ProtoLink != "" {
		parts = append(parts, "原型："+r.ProtoLink)
	}
	e.FirstComment = strings.Join(parts, "\n")
	return e, nil
}
```

- [ ] **Step 3: 跑测试 + Commit**

```bash
git add internal/backlogmig/
git commit -m "feat(backlogmig): 状态映射与迁移条目——映射表唯一落码点，未知态报错不猜"
```

---

### Task 3: importer.go + ImportCard——幂等落库

**Files:**
- Create: `internal/backlogmig/importer.go`
- Modify: `internal/ledger/cards.go`（追加 ImportCard：指定 id 直插，迁移专用）
- Test: `internal/backlogmig/importer_test.go`

- [ ] **Step 1: 测试**

```go
package backlogmig

import (
	"testing"

	"github.com/Xsxdot/handoff/internal/ledger"
)

func TestImportIdempotent(t *testing.T) {
	s, err := ledger.Open(t.TempDir() + "/ledger.db")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.EnsureDefaultWorkflows()

	rows, _ := Parse([]byte(fixture))
	rep, err := Import(s, rows, "handoff", false) // commit
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if rep.Created != 7 || rep.Skipped != 0 {
		t.Fatalf("首轮: %+v", rep)
	}
	// 状态/字段抽查（判据⑧的单测形）
	c, _ := s.GetCard("B7")
	if c.Status != "已完成" || c.AcceptanceCriteria == "" {
		t.Fatalf("B7: %+v", c)
	}
	// done(已验) 落了 acceptance_recorded(verified=true)
	evs, _ := s.EventsFromAsc([]string{"B7"}, 0, 50)
	okAcc := false
	for _, e := range evs {
		if e.Type == ledger.EvAcceptanceRecorded {
			okAcc = true
		}
	}
	if !okAcc {
		t.Fatal("B7 缺验收事件")
	}
	// 子卡父链 + 引用成边
	c, _ = s.GetCard("B5.1")
	if c.ParentID != "B5" {
		t.Fatalf("父链: %+v", c)
	}
	rels, _ := s.RelationsOf("B6")
	hasRef := false
	for _, r := range rels {
		if r.Type == ledger.RelRelates && r.To == "B17" {
			hasRef = true // B17 不在本批 → 不建边；改断言：
		}
	}
	_ = hasRef // B17 不存在：预期不建边、引用留在首评论原文里（考古够用）
	// 幂等重跑：全 skip 不重建
	rep2, err := Import(s, rows, "handoff", false)
	if err != nil || rep2.Created != 0 || rep2.Skipped != 7 {
		t.Fatalf("重跑: %v %+v", err, rep2)
	}
	// B 号水位垫高：迁移后新建卡不撞历史号
	nc, _ := s.CreateCard(ledger.NewCard{Title: "新卡", Project: "handoff", Workflow: "bug", Actor: "t"})
	if nc.ID != "B11" {
		t.Fatalf("水位: %s", nc.ID)
	}
}

func TestImportDryRun(t *testing.T) {
	s, _ := ledger.Open(t.TempDir() + "/ledger.db")
	defer s.Close()
	_ = s.EnsureDefaultWorkflows()
	rows, _ := Parse([]byte(fixture))
	rep, err := Import(s, rows, "handoff", true)
	if err != nil || rep.Created != 7 {
		t.Fatalf("dry-run 报告: %v %+v", err, rep)
	}
	if _, err := s.GetCard("B5"); err == nil {
		t.Fatal("dry-run 不应落库")
	}
}
```

- [ ] **Step 2: ImportCard（internal/ledger/cards.go 追加）**

```go
// ImportCard 迁移专用：指定 id 直插（绕过 B 号分配与工作流首态规则，
// 状态由调用方给定——迁移是搬史实不是走流程）。仅当 id 不存在时插入，
// 返回是否插入。生产路径禁用：不导出到 CLI 常规动词。
func (s *Store) ImportCard(c Card, actor string) (bool, error) {
	created := false
	err := s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		if _, err := getCardTx(s, tx, c.ID); err == nil {
			return nil // 已存在：幂等跳过
		} else if !errors.Is(err, ErrNotFound) {
			return err
		}
		var parent, base, tr any
		if c.ParentID != "" {
			parent = c.ParentID
		}
		if c.BaseBranch != "" {
			base = c.BaseBranch
		}
		if c.TerminateReason != "" {
			tr = c.TerminateReason
		}
		raw, _ := json.Marshal(c.Attachments)
		now := time.Now()
		if _, err := tx.Exec(s.q(`INSERT INTO cards
			(id, title, status, terminate_reason, priority, project, parent_id,
			 workflow_name, workflow_version, attachments, acceptance_criteria,
			 base_branch, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`),
			c.ID, c.Title, c.Status, tr, c.Priority, c.Project, parent,
			c.WorkflowName, c.WorkflowVersion, string(raw), c.AcceptanceCriteria,
			base, s.tval(now), s.tval(now)); err != nil {
			return fmt.Errorf("导入卡 %s: %w", c.ID, err)
		}
		if _, err := s.appendEvent(tx, sink, c.ID, EvCardCreated, actor,
			map[string]any{"title": c.Title, "imported": true}); err != nil {
			return err
		}
		created = true
		return nil
	})
	return created, err
}
```

- [ ] **Step 3: 实现 importer.go**

```go
// 幂等导入：解析行 → Convert → ImportCard + 附属账（附件/首评论/验收
// 事件/引用边/水位垫高）。父卡先于子卡（按 id 点号深度排序）。
package backlogmig

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/Xsxdot/handoff/internal/ledger"
)

// Report 导入报告。
type Report struct {
	Created, Skipped int
	MaxB             int
	Warnings         []string
}

// Import rows 落库。dryRun=true 只做转换与校验、报告将发生什么，不写库。
func Import(st *ledger.Store, rows []Row, project string, dryRun bool) (Report, error) {
	var rep Report
	entries := make([]Entry, 0, len(rows))
	for _, r := range rows {
		e, err := Convert(r)
		if err != nil {
			return rep, err
		}
		entries = append(entries, e)
		if n := topNum(e.ID); n > rep.MaxB {
			rep.MaxB = n
		}
	}
	// 父卡先插：按点号深度稳定排序
	sort.SliceStable(entries, func(i, j int) bool {
		return strings.Count(entries[i].ID, ".") < strings.Count(entries[j].ID, ".")
	})
	if dryRun {
		rep.Created = len(entries)
		return rep, nil
	}
	wf := map[string]int{}
	for _, name := range []string{"feature", "bug"} {
		w, err := st.GetWorkflow(name, 0)
		if err != nil {
			return rep, fmt.Errorf("默认工作流缺失（先 EnsureDefaultWorkflows）: %w", err)
		}
		wf[name] = w.Version
	}
	for _, e := range entries {
		card := ledger.Card{ID: e.ID, Title: e.Title, Status: e.Status,
			TerminateReason: e.TerminateReason, Priority: e.Priority, Project: project,
			ParentID: e.Parent, WorkflowName: e.Workflow, WorkflowVersion: wf[e.Workflow],
			AcceptanceCriteria: e.Acceptance}
		if e.SpecPath != "" {
			card.Attachments = []ledger.Attachment{{Kind: "spec", Path: e.SpecPath}}
		}
		created, err := st.ImportCard(card, "migrate")
		if err != nil {
			return rep, err
		}
		if !created {
			rep.Skipped++
			continue
		}
		rep.Created++
		if e.FirstComment != "" {
			if _, err := st.AddComment(e.ID, e.FirstComment, "普通", "migrate"); err != nil {
				return rep, fmt.Errorf("首评论 %s: %w", e.ID, err)
			}
		}
		if e.Verified != nil {
			if err := st.RecordAcceptance(e.ID, *e.Verified, "迁移自 backlog.md 验收列", "migrate"); err != nil {
				return rep, fmt.Errorf("验收事件 %s: %w", e.ID, err)
			}
		}
		for _, ref := range e.Refs {
			// 引用目标在库才建边；不在 = 留在首评论原文里（尽力解析，spec §8）
			if _, err := st.GetCard(ref); err != nil {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s 引用 %s 不在库，留原文", e.ID, ref))
				continue
			}
			if err := st.AddRelation(e.ID, ref, ledger.RelRelates, "migrate"); err != nil {
				rep.Warnings = append(rep.Warnings, fmt.Sprintf("%s→%s 建边失败: %v", e.ID, ref, err))
			}
		}
	}
	if err := st.EnsureMinB(rep.MaxB); err != nil {
		return rep, fmt.Errorf("垫 B 号水位: %w", err)
	}
	return rep, nil
}

func topNum(id string) int {
	top := id
	if i := strings.Index(id, "."); i > 0 {
		top = id[:i]
	}
	n, _ := strconv.Atoi(strings.TrimPrefix(top, "B"))
	return n
}
```

（注意 Refs 建边顺序问题：后插的卡引用先插的卡没问题；**前向引用**（B6 引用 B17 且 B17 在同批靠后）会被 Warnings 掉——修法：引用边统一放**全部卡插完后的第二遍**。执行者按此实现：Import 主循环只收集 `(from, ref)`，循环结束统一建边。上面测试的 hasRef 断言按第二遍语义改为「B17 不在批内 → 无边 + 一条 Warning」。）

- [ ] **Step 4: 跑测试 + Commit**

```bash
git add internal/backlogmig/ internal/ledger/
git commit -m "feat(backlogmig): 幂等导入——ImportCard 直插/两遍建边/验收事件/水位垫高/dry-run"
```

---

### Task 4: cmd/card_import.go——导入命令

**Files:**
- Create: `cmd/card_import.go`
- Test: `cmd/card_import_test.go`

- [ ] **Step 1: 实现（Hidden 命令，dry-run 缺省）**

```go
// card import-backlog：markdown 总账一次性迁移入库。Hidden——这不是
// 日常动词，迁移完成即历史。缺省 dry-run；真跑 --commit。
package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/Xsxdot/handoff/internal/backlogmig"
	"github.com/spf13/cobra"
)

var (
	importFile    string
	importProject string
	importCommit  bool
)

var cardImportCmd = &cobra.Command{
	Use:    "import-backlog",
	Short:  "迁移 backlog.md 入账本（缺省 dry-run，--commit 真跑）",
	Hidden: true,
	Args:   cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		src, err := os.ReadFile(importFile)
		if err != nil {
			return fmt.Errorf("读总账: %w", err)
		}
		rows, err := backlogmig.Parse(src)
		if err != nil {
			return err
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		rep, err := backlogmig.Import(st, rows, importProject, !importCommit)
		if err != nil {
			return err
		}
		if !importCommit {
			fmt.Fprintln(cmd.ErrOrStderr(), "dry-run：未写库。确认无误后加 --commit")
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(rep)
	},
}

func init() {
	cardImportCmd.Flags().StringVar(&importFile, "file", "docs/superpowers/backlog.md", "总账路径")
	cardImportCmd.Flags().StringVar(&importProject, "project", "handoff", "落库 project")
	cardImportCmd.Flags().BoolVar(&importCommit, "commit", false, "真写库")
	cardCmd.AddCommand(cardImportCmd)
}
```

测试：临时目录写 fixture 总账 → dry-run 输出含 `"Created":7` 且库无卡 → `--commit` 后 `card show B7` 命中 → 再跑 `--commit` 全 Skipped。

- [ ] **Step 2: 跑测试 + 终审 + Commit**

```bash
go test ./cmd/ -run TestCardImport -v
gofmt -l internal/backlogmig/ cmd/ && go vet ./... && go test ./...
git add cmd/ && git commit -m "feat(cli): card import-backlog——Hidden/dry-run 缺省/报告落 stdout"
```

---

## 审核者本地清单（不派发——spec §8 步骤 1/3/4 的人工位）

迁移**执行**（代码合入后、真库上跑）按序：

1. **对齐汇流点**：web-console 分支（或届时的 main）`git merge-base` 实测各在途分支无 backlog.md 分叉遗漏；有分叉先合再迁。备份 `backlog.md`。
2. **真跑**：`handoff card import-backlog --file docs/superpowers/backlog.md --project handoff`（dry-run 看报告）→ 核对 Created/Warnings → `--commit`。
3. **抽查**（判据⑧）：随机 10 条 `card show` 对照原文逐列核；任一不符停下修 convert 再重迁（幂等允许）。
4. **冻结注记**：backlog.md 顶部加「已迁入账本库（日期/批次），本文件冻结只读，新条目走 handoff card add」。
5. **skill 切换（先切 skill 再冻结文件，防在途 worktree 旧副本继续追加）**：`~/.claude/skills/product-backlog/SKILL.md` 改写为指针（指向 `handoff card` 命令面 + 领活用 `card list --needs`/`--status 已出spec`）；`~/.claude/CLAUDE.md` §3 表格里 product-backlog 行描述同步；handoff skill 补 card/decision/wait --card 的状态机约束段。
6. **backlog B156.1 行**按 evidence gate 收口。

## Self-Review 记录

1. **宁停不丢**贯穿：坏行报错带行号、未知状态报错、引用解析不到留原文 + Warning——迁移工具的失败模式全部是显式的。
2. **两遍建边**修了前向引用问题（Task 3 文末注明，执行者按第二遍语义实现并改对应断言）。
3. **epic 的表达**：📦 epic 不设独立状态——「待办 + 有子卡」就是 epic 性（统一 Card 模型下类型即阶段+关系，映射表如此定案）。
4. **已知妥协**：`ImportCard` 是账本库里唯一绕过流程规则的直插口（迁移是搬史实），Hidden + 不进白名单 + 文档注明生产路径禁用；「线」列不映射结构（迁移时代已单线），进首评论保原文。
