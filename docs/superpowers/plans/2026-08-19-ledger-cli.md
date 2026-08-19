# 账本 CLI（Plan A2 / B156.1-命令面）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 落地 `handoff card / workflow / decision` 三个命令族 + `card export` 逃生门——账本域的完整命令面，基于 Plan A 的 `internal/ledger` 库。

**Architecture:** **CLI 直连账本库，不经本机 agentd HTTP**。这是有意偏离「除 agentd 外全走 internal/client」的既有惯例，理由要写进代码注释：①账本域凭据本来就在协调机（config.ledger.dsn），CLI 与 agentd 是同一台机器上的对等消费者；②账本操作不应依赖本机 agentd 存活（agentd 挂了照样记账/查账）；③执行域的 store 之所以必须走 HTTP，是因为 task 数据在**别的机器**的 SQLite 里——账本没有这个约束。web 看板的 HTTP 面归 Plan D（那是浏览器，没法直连库）。executor 侧无变化：B115 白名单 fail-closed，新子命令天然被拒。

**Tech Stack:** cobra（既有模式：一命令族一文件、包级 var + init 注册）、`internal/ledger`（Plan A 交付）、`internal/config`。输出契约沿用现有：stdout 只出机器 JSON（一行一个对象），人话/表格走 stderr 或 `--json` 缺省时的 tabwriter。

**前置条件：** Plan A 已合入（`internal/ledger` 包存在且 `go test ./internal/ledger/` 全绿）。开工先跑 `go build ./... && go test ./...` 确认基线绿。

**House rules：** 同 Plan A（slog、错误 wrap 中文上下文、文件头/导出符号注释、gofmt）。CLI 层日志极少——它是前台进程，用户看的是 stdout/stderr，不是日志；只在长命令（无）里 slog。**既有 helper 的确切签名以仓库实际代码为准**（`loadCLIConfig`、`config.Save`、`resetPerRunState` 等在 `cmd/root.go` / `internal/config/config.go`），与本 plan 内联代码有出入时改测试/调用方，不改既有生产签名。

---

## File Structure

```
cmd/
  ledgercli.go       // openLedger（DSN 解析+回退）、ledgerActor、confirmDestructive、JSON 输出 helper
  card.go            // card 命令族全部动词
  workflow.go        // workflow list/show/put/migrate
  decision.go        // decision open/list/answer
  card_test.go       // 命令族端到端测试（进程内跑 rootCmd + 临时 SQLite 账本）
  workflow_test.go
  decision_test.go
internal/ledger/
  workflows.go       // 追加 MigrateCardWorkflow（小操作，属账本库）
  workflows_test.go  // 追加对应测试
```

---

### Task 1: cmd/ledgercli.go——账本 CLI 公共底座

**Files:**
- Create: `cmd/ledgercli.go`
- Test: `cmd/ledgercli_test.go`

- [ ] **Step 1: 写失败测试**

```go
// 账本 CLI 测试基座：进程内跑 rootCmd（复用 resetPerRunState 的可重入
// 设计），--config 指向临时目录，账本落该目录 SQLite。
package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Xsxdot/handoff/internal/config"
)

// runLedgerCLI 在 dir（DataDir 兼配置目录）下跑一条 handoff 命令。
// 首次调用自动写最小 config.yaml。返回 stdout/stderr 文本与错误。
func runLedgerCLI(t *testing.T, dir string, args ...string) (string, string, error) {
	t.Helper()
	cfgPath := filepath.Join(dir, "config.yaml")
	if _, err := os.Stat(cfgPath); err != nil {
		c := &config.Config{Listen: "127.0.0.1:0", Token: "t", DataDir: dir}
		if err := config.Save(cfgPath, c); err != nil { // 签名以 config.go 实际为准
			t.Fatalf("写测试配置: %v", err)
		}
	}
	resetAllFlags(rootCmd)
	var out, errb bytes.Buffer
	rootCmd.SetOut(&out)
	rootCmd.SetErr(&errb)
	rootCmd.SetArgs(append([]string{"--config", cfgPath}, args...))
	err := Execute()
	return out.String(), errb.String(), err
}

// resetAllFlags 递归把命令树上所有 flag 恢复默认值。cobra 的 flag 绑定
// 在包级变量上，跨 Execute() 持久——上一个测试设过的 --parent/--json
// 会静默污染下一个测试（repo 既有做法是逐个 t.Cleanup 手工复位，账本
// 命令族 flag 太多，统一在基座里回默认值）。
func resetAllFlags(c *cobra.Command) {
	c.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, sub := range c.Commands() {
		resetAllFlags(sub)
	}
}

func TestOpenLedgerFallbackSQLite(t *testing.T) {
	dir := t.TempDir()
	// 任意账本命令首跑即建库 + seed 默认工作流
	out, _, err := runLedgerCLI(t, dir, "workflow", "list")
	if err != nil {
		t.Fatalf("workflow list: %v", err)
	}
	if !strings.Contains(out, "feature") || !strings.Contains(out, "bug") {
		t.Fatalf("默认工作流缺失: %q", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "ledger.db")); err != nil {
		t.Fatalf("回退 SQLite 未落 DataDir: %v", err)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestOpenLedgerFallback -v`
Expected: FAIL（workflow 子命令不存在——cobra 报 unknown command）

- [ ] **Step 3: 实现 ledgercli.go**

```go
// 账本命令族的公共底座。职责：解析账本库位置并打开、CLI 侧 actor 标识、
// 破坏性动作确认。边界：本文件不含任何具体动词逻辑。
//
// 为什么 CLI 直连账本库而不经本机 agentd HTTP（有意偏离既有惯例）：
// 账本凭据本来就在协调机 config 里，CLI 与 agentd 是对等消费者；账本
// 操作不应依赖本机 agentd 存活；执行域必须走 HTTP 是因为 task 数据在
// 远端机器的 SQLite——账本（中心库/本机回退）没有这个约束。
package cmd

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
)

// openLedger 按配置打开账本库：ledger.dsn 非空连中心库，空则回退
// DataDir/ledger.db（单机模式）。每次打开幂等 seed 默认工作流。
// 调用方负责 Close。
func openLedger() (*ledger.Store, error) {
	cfg := loadCLIConfig()
	dsn := cfg.Ledger.DSN
	if dsn == "" {
		dsn = filepath.Join(cfg.DataDir, "ledger.db")
	}
	st, err := ledger.Open(dsn)
	if err != nil {
		return nil, fmt.Errorf("打开账本库: %w", err)
	}
	if err := st.EnsureDefaultWorkflows(); err != nil {
		st.Close()
		return nil, fmt.Errorf("seed 默认工作流: %w", err)
	}
	return st, nil
}

// ledgerActor CLI 写账时的 actor 标识：cli:<user>@<host>。事件流取证用，
// 不做鉴权（账本凭据即权限边界）。
func ledgerActor() string {
	user := os.Getenv("USER")
	if user == "" {
		user = "unknown"
	}
	host, _ := os.Hostname()
	return fmt.Sprintf("cli:%s@%s", user, host)
}

// confirmDestructive 三处破坏性动作（close/merge/workflow migrate）的
// 二次确认：--yes 跳过；非 TTY 且无 --yes 直接拒绝（脚本必须显式表态，
// 不许静默走破坏路径）。
func confirmDestructive(cmd *cobra.Command, yes bool, msg string) error {
	if yes {
		return nil
	}
	if f, ok := cmd.InOrStdin().(*os.File); !ok || !isatty.IsTerminal(f.Fd()) {
		return fmt.Errorf("%s：非交互环境需 --yes 显式确认", msg)
	}
	fmt.Fprintf(cmd.ErrOrStderr(), "%s (y/N) ", msg)
	line, _ := bufio.NewReader(cmd.InOrStdin()).ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return nil
	}
	return fmt.Errorf("%s：已取消", msg)
}
```

（`workflow list` 在 Task 4 才有——为让 Step 1 测试跑通，本 Task 先带上 Task 4 的 `workflow list` 最小动词？**不**：测试基座先用注释禁用断言会造假绿。处置：本 Task 的测试改为跟随 Task 4 一起转绿——Step 1 写下测试后，Step 3 实现底座，Step 5 允许该测试仍 FAIL 于 unknown command，**在 Task 4 Step 5 必须全绿**。执行者据此不要在本 Task 硬造 workflow 命令。）

- [ ] **Step 4: 编译**

Run: `go build ./cmd/`
Expected: 编译通过

- [ ] **Step 5: Commit**

```bash
git add cmd/ledgercli.go cmd/ledgercli_test.go
git commit -m "feat(cli): 账本命令底座——openLedger 回退解析/actor/破坏确认（直连账本库的架构注释在案）"
```

---

### Task 2: cmd/card.go——add/list/show/update/move

**Files:**
- Create: `cmd/card.go`
- Test: `cmd/card_test.go`

- [ ] **Step 1: 写失败测试**

```go
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCardAddListShowMove(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "card", "add", "第一张卡", "--project", "demo")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// stdout 契约：单行 JSON，含分配的 id
	var created struct {
		ID     string `json:"id"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &created); err != nil {
		t.Fatalf("add 输出非单行 JSON: %q", out)
	}
	if created.ID == "" || created.Status != "待办" {
		t.Fatalf("建卡返回: %+v", created)
	}

	// list 人类表格默认走 stdout tabwriter；--json 一行一对象
	out, _, err = runLedgerCLI(t, dir, "card", "list", "--project", "demo", "--json")
	if err != nil || !strings.Contains(out, created.ID) {
		t.Fatalf("list --json: %v %q", err, out)
	}

	// show：卡 + 关系 + 事件
	out, _, err = runLedgerCLI(t, dir, "card", "show", created.ID)
	if err != nil || !strings.Contains(out, "第一张卡") {
		t.Fatalf("show: %v %q", err, out)
	}

	// move + gate：feature 流无 spec 附件进「已出spec」应拒且文案指明缺附件
	_, stderr, err := runLedgerCLI(t, dir, "card", "move", created.ID, "已出spec")
	if err == nil || !strings.Contains(err.Error()+stderr, "spec") {
		t.Fatalf("gate 应拒且提示: %v %q", err, stderr)
	}
	// update --attach 后放行
	if _, _, err := runLedgerCLI(t, dir, "card", "update", created.ID, "--attach", "spec:specs/x.md"); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "move", created.ID, "已出spec"); err != nil {
		t.Fatalf("gate 放行: %v", err)
	}
	// --expect CAS 钉前值：错前值干净失败
	if _, _, err := runLedgerCLI(t, dir, "card", "move", created.ID, "进行中", "--expect", "待办"); err == nil {
		t.Fatal("错前值应失败")
	}
}

func TestCardAddChildAndBaseBranch(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "epic", "--project", "demo", "--base-branch", "desktop-shell")
	var epic struct{ ID string `json:"id"` }
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &epic)
	out, _, err := runLedgerCLI(t, dir, "card", "add", "子项", "--project", "demo", "--parent", epic.ID)
	if err != nil {
		t.Fatalf("子卡: %v", err)
	}
	var child struct{ ID string `json:"id"` }
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &child)
	if !strings.HasPrefix(child.ID, epic.ID+".") {
		t.Fatalf("子卡点号: %q", child.ID)
	}
	// 基线过滤能查到子卡（继承）
	out, _, _ = runLedgerCLI(t, dir, "card", "list", "--project", "demo", "--base-branch", "desktop-shell", "--json")
	if !strings.Contains(out, child.ID) {
		t.Fatalf("基线继承过滤: %q", out)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestCardAdd -v`
Expected: FAIL（unknown command "card"）

- [ ] **Step 3: 实现 card.go（本 Task 动词）**

```go
// handoff card 命令族：任务卡账本的命令面。谁是机器谁是人分得清：
// stdout 只出机器 JSON（一行一对象；list 缺省表格是唯一例外，--json
// 切换），人话走 stderr。状态名用中文原文（与 workflow 定义一致），
// 不设英文别名。
package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/spf13/cobra"
)

var cardCmd = &cobra.Command{Use: "card", Short: "任务卡账本（工作项的建/查/流转/合并/拆分）"}

var (
	cardAddProject, cardAddPriority, cardAddParent, cardAddWorkflow, cardAddBase string
	cardListStatus, cardListProject, cardListBase                               string
	cardListBlocked, cardListNeeds, cardListJSON, cardListAll                   bool
	cardMoveExpect                                                              string
	cardUpdateTitle, cardUpdatePriority, cardUpdateAccept                       string
	cardUpdateAttach, cardUpdateDetach                                          string
)

var cardAddCmd = &cobra.Command{
	Use:   "add <标题>",
	Short: "建卡（B 号自动分配；--parent 建子卡）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		c, err := st.CreateCard(ledger.NewCard{
			Title: strings.Join(args, " "), Project: cardAddProject,
			Priority: cardAddPriority, Parent: cardAddParent,
			Workflow: cardAddWorkflow, BaseBranch: cardAddBase, Actor: ledgerActor(),
		})
		if err != nil {
			return fmt.Errorf("建卡: %w", err)
		}
		return printCardJSON(cmd, c)
	},
}

var cardListCmd = &cobra.Command{
	Use:   "list",
	Short: "列卡（含派生标记；缺省表格，--json 一行一对象）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		views, err := st.ListCards(ledger.CardFilter{
			Project: cardListProject, Status: cardListStatus, BaseBranch: cardListBase,
			Blocked: cardListBlocked, Needs: cardListNeeds, IncludeTerminal: cardListAll,
		})
		if err != nil {
			return fmt.Errorf("列卡: %w", err)
		}
		if cardListJSON {
			enc := json.NewEncoder(cmd.OutOrStdout())
			for _, v := range views {
				if err := enc.Encode(cardViewWire(v)); err != nil {
					return err
				}
			}
			return nil
		}
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "ID\t状态\t优先级\t标题\t标记")
		for _, v := range views {
			var marks []string
			if v.Following != "" {
				marks = append(marks, "跟随 "+v.Following)
			}
			if v.Blocked {
				marks = append(marks, "blocked:"+strings.Join(v.BlockedBy, ","))
			}
			if v.NeedsReason != "" {
				marks = append(marks, "⚑ "+v.NeedsReason)
			}
			if v.OpenDecisions > 0 {
				marks = append(marks, fmt.Sprintf("⚖ %d", v.OpenDecisions))
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", v.ID, v.Status, v.Priority, v.Title, strings.Join(marks, " "))
		}
		return w.Flush()
	},
}

var cardShowCmd = &cobra.Command{
	Use:   "show <id>",
	Short: "看卡：字段 + 关系 + 挂账 task + 最近事件（单 JSON 对象）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		c, err := st.GetCard(args[0])
		if err != nil {
			return err
		}
		rels, err := st.RelationsOf(c.ID)
		if err != nil {
			return err
		}
		links, err := st.TasksOf(c.ID)
		if err != nil {
			return err
		}
		evs, err := st.EventsFromAsc([]string{c.ID}, 0, 200)
		if err != nil {
			return err
		}
		base, err := st.EffectiveBaseBranch(c.ID)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{
			"card": c, "effective_base_branch": base,
			"relations": rels, "tasks": links, "events": evs,
		})
	},
}

var cardUpdateCmd = &cobra.Command{
	Use:   "update <id>",
	Short: "改卡：--title/--priority/--attach kind:path/--detach path/--accept 判据",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		id, actor := args[0], ledgerActor()
		if cardUpdateAttach != "" {
			kind, path, ok := strings.Cut(cardUpdateAttach, ":")
			if !ok {
				return fmt.Errorf("--attach 形如 kind:path（如 spec:specs/x.md）")
			}
			if err := st.AttachFile(id, kind, path, actor); err != nil {
				return err
			}
		}
		if cardUpdateDetach != "" {
			if err := st.DetachFile(id, cardUpdateDetach, actor); err != nil {
				return err
			}
		}
		if cardUpdateAccept != "" {
			if err := st.SetAcceptance(id, cardUpdateAccept, actor); err != nil {
				return err
			}
		}
		if cardUpdateTitle != "" || cardUpdatePriority != "" {
			if err := st.UpdateCardMeta(id, cardUpdateTitle, cardUpdatePriority, actor); err != nil {
				return err
			}
		}
		c, err := st.GetCard(id)
		if err != nil {
			return err
		}
		return printCardJSON(cmd, c)
	},
}

var cardMoveCmd = &cobra.Command{
	Use:   "move <id> <状态>",
	Short: "状态转移（CAS；--expect 显式钉前值；gate 拒绝会说清缺什么）",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.MoveCard(args[0], args[1], cardMoveExpect, ledgerActor()); err != nil {
			return err
		}
		c, err := st.GetCard(args[0])
		if err != nil {
			return err
		}
		return printCardJSON(cmd, c)
	},
}

// printCardJSON stdout 单行卡 JSON（机器契约）。
func printCardJSON(cmd *cobra.Command, c ledger.Card) error {
	return json.NewEncoder(cmd.OutOrStdout()).Encode(c)
}

// cardViewWire 列表行的线格式（字段名稳定，看板/脚本共用词汇）。
func cardViewWire(v ledger.CardView) map[string]any {
	return map[string]any{
		"id": v.ID, "title": v.Title, "status": v.Status, "priority": v.Priority,
		"project": v.Project, "parent": v.ParentID, "base_branch": v.BaseBranch,
		"following": v.Following, "blocked": v.Blocked, "blocked_by": v.BlockedBy,
		"needs": v.NeedsReason, "open_decisions": v.OpenDecisions,
	}
}

func init() {
	cardAddCmd.Flags().StringVar(&cardAddProject, "project", "", "项目名（必填）")
	cardAddCmd.Flags().StringVar(&cardAddPriority, "priority", "中", "高|中|低")
	cardAddCmd.Flags().StringVar(&cardAddParent, "parent", "", "父卡 id（建子卡）")
	cardAddCmd.Flags().StringVar(&cardAddWorkflow, "workflow", "feature", "工作流名")
	cardAddCmd.Flags().StringVar(&cardAddBase, "base-branch", "", "基线分支（空=继承/主线）")
	_ = cardAddCmd.MarkFlagRequired("project")

	cardListCmd.Flags().StringVar(&cardListProject, "project", "", "按项目过滤")
	cardListCmd.Flags().StringVar(&cardListStatus, "status", "", "按状态过滤")
	cardListCmd.Flags().StringVar(&cardListBase, "base-branch", "", "按有效基线过滤")
	cardListCmd.Flags().BoolVar(&cardListBlocked, "blocked", false, "只列被阻塞的")
	cardListCmd.Flags().BoolVar(&cardListNeeds, "needs", false, "只列需要你的（等人/裁决）")
	cardListCmd.Flags().BoolVar(&cardListJSON, "json", false, "一行一 JSON 对象")
	cardListCmd.Flags().BoolVar(&cardListAll, "all", false, "含已完成/终止")

	cardMoveCmd.Flags().StringVar(&cardMoveExpect, "expect", "", "CAS 前值（脚本场景钉死）")

	cardUpdateCmd.Flags().StringVar(&cardUpdateTitle, "title", "", "改标题")
	cardUpdateCmd.Flags().StringVar(&cardUpdatePriority, "priority", "", "改优先级")
	cardUpdateCmd.Flags().StringVar(&cardUpdateAttach, "attach", "", "挂附件 kind:path")
	cardUpdateCmd.Flags().StringVar(&cardUpdateDetach, "detach", "", "摘附件 path")
	cardUpdateCmd.Flags().StringVar(&cardUpdateAccept, "accept", "", "设验收判据")

	cardCmd.AddCommand(cardAddCmd, cardListCmd, cardShowCmd, cardUpdateCmd, cardMoveCmd)
	rootCmd.AddCommand(cardCmd)
}
```

- [ ] **Step 4: 在 internal/ledger/cards.go 追加 UpdateCardMeta**

（Plan A 没有改标题/优先级的操作——这里补上，属账本库。）

```go
// UpdateCardMeta 改标题/优先级（空串 = 不改该项）。落 comment 事件。
func (s *Store) UpdateCardMeta(id, title, priority, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		c, err := getCardTx(s, tx, id)
		if err != nil {
			return fmt.Errorf("改卡: 卡 %s: %w", id, err)
		}
		if title == "" {
			title = c.Title
		}
		if priority == "" {
			priority = c.Priority
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET title = ?, priority = ?, updated_at = ? WHERE id = ?`),
			title, priority, s.tval(time.Now()), id); err != nil {
			return fmt.Errorf("写改卡: %w", err)
		}
		_, err = s.appendEvent(tx, sink, id, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("改卡：标题=%q 优先级=%s", title, priority)})
		return err
	})
}
```

配套在 `internal/ledger/cards_test.go` 追加：

```go
func TestUpdateCardMeta(t *testing.T) {
	s := seedStore(t)
	c := mk(t, s, "旧标题")
	if err := s.UpdateCardMeta(c.ID, "新标题", "高", "test"); err != nil {
		t.Fatalf("meta: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.Title != "新标题" || got.Priority != "高" {
		t.Fatalf("未生效: %+v", got)
	}
	// 只改一项：另一项保持
	_ = s.UpdateCardMeta(c.ID, "", "低", "test")
	got, _ = s.GetCard(c.ID)
	if got.Title != "新标题" || got.Priority != "低" {
		t.Fatalf("空串应保持原值: %+v", got)
	}
}
```

- [ ] **Step 5: 跑测试确认通过**

Run: `go test ./cmd/ -run TestCard -v && go test ./internal/ledger/ -run TestUpdateCardMeta -v`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add cmd/ internal/ledger/
git commit -m "feat(cli): card add/list/show/update/move——stdout 机器 JSON 契约 + 中文状态名 + CAS/gate 透传"
```

---

### Task 3: cmd/card.go 续——close/revive/link/unlink/merge/unmerge/split/note/export

**Files:**
- Modify: `cmd/card.go`
- Test: `cmd/card_test.go`（追加）

- [ ] **Step 1: 写失败测试（追加）**

```go
func TestCardCloseConfirmAndMerge(t *testing.T) {
	dir := t.TempDir()
	mkCard := func(title string) string {
		out, _, err := runLedgerCLI(t, dir, "card", "add", title, "--project", "demo", "--workflow", "bug")
		if err != nil {
			t.Fatalf("add %s: %v", title, err)
		}
		var c struct{ ID string `json:"id"` }
		_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &c)
		return c.ID
	}
	a, b, carrier := mkCard("a"), mkCard("b"), mkCard("carrier")

	// close 非交互无 --yes 拒绝（二次确认约定；只对不可逆的 取消|废弃 设门）
	if _, _, err := runLedgerCLI(t, dir, "card", "close", a, "--reason", "废弃"); err == nil {
		t.Fatal("无 --yes 应拒")
	}
	// 搁置可复活，不设确认门——无 --yes 也应直接成功
	if _, _, err := runLedgerCLI(t, dir, "card", "close", a, "--reason", "搁置"); err != nil {
		t.Fatalf("close 搁置不应要求确认: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "revive", a); err != nil {
		t.Fatalf("revive: %v", err)
	}

	// link 环检测透传
	if _, _, err := runLedgerCLI(t, dir, "card", "link", a, b); err != nil {
		t.Fatalf("link: %v", err)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "link", b, a); err == nil {
		t.Fatal("成环应拒")
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "unlink", a, b); err != nil {
		t.Fatalf("unlink: %v", err)
	}

	// merge --yes + 列表跟随 + unmerge
	if _, _, err := runLedgerCLI(t, dir, "card", "merge", a, b, "--into", carrier, "--yes"); err != nil {
		t.Fatalf("merge: %v", err)
	}
	out, _, _ := runLedgerCLI(t, dir, "card", "list", "--project", "demo", "--json")
	if !strings.Contains(out, `"following":"`+carrier+`"`) {
		t.Fatalf("跟随未呈现: %q", out)
	}
	if _, _, err := runLedgerCLI(t, dir, "card", "unmerge", a); err != nil {
		t.Fatalf("unmerge: %v", err)
	}

	// split
	out, _, err := runLedgerCLI(t, dir, "card", "split", carrier, "拆出的子项")
	if err != nil || !strings.Contains(out, carrier+".") {
		t.Fatalf("split: %v %q", err, out)
	}

	// note 引用建边
	if _, _, err := runLedgerCLI(t, dir, "card", "note", a, "与 #"+b+" 同源"); err != nil {
		t.Fatalf("note: %v", err)
	}
	// note --reset-node：人工重置回合计数的落账入口（Plan C 消费）
	out2, _, err := runLedgerCLI(t, dir, "card", "note", a, "人工看过重新计数", "--reset-node", "review")
	if err != nil || !strings.Contains(out2, `"human_reset_node":"review"`) {
		t.Fatalf("note --reset-node: %v %q", err, out2)
	}

	// export markdown 快照
	out, _, err = runLedgerCLI(t, dir, "card", "export")
	if err != nil || !strings.Contains(out, "| "+carrier+" |") {
		t.Fatalf("export: %v %q", err, out)
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestCardCloseConfirm -v` → FAIL（unknown command "close"）

- [ ] **Step 3: 实现（cmd/card.go 追加）**

```go
var (
	cardCloseReason string
	cardCloseYes    bool
	cardMergeInto   string
	cardMergeYes    bool
	cardNoteFix       bool
	cardNoteResetNode string
	cardExportOut   string
)

var cardCloseCmd = &cobra.Command{
	Use:   "close <id>",
	Short: "终止（--reason 取消|废弃|搁置；破坏性，需确认或 --yes）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// spec §4 定案：二次确认只三处，close 只对不可逆的 取消|废弃 设门；
		// 搁置可复活，不确认
		if cardCloseReason != "搁置" {
			if err := confirmDestructive(cmd, cardCloseYes,
				fmt.Sprintf("终止 %s（reason=%s）不可逆", args[0], cardCloseReason)); err != nil {
				return err
			}
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.CloseCard(args[0], cardCloseReason, ledgerActor()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardReviveCmd = &cobra.Command{
	Use:   "revive <id>",
	Short: "复活搁置卡（终止(搁置) → 待办）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.ReviveCard(args[0], ledgerActor()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardLinkCmd = &cobra.Command{
	Use:   "link <blocker> <blocked>",
	Short: "加阻塞边：前者阻塞后者（写入即环检测）",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.AddBlocks(args[0], args[1], ledgerActor()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardUnlinkCmd = &cobra.Command{
	Use:   "unlink <blocker> <blocked>",
	Short: "解除阻塞边",
	Args:  cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.RemoveRelation(args[0], args[1], ledger.RelBlocks); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardMergeCmd = &cobra.Command{
	Use:   "merge <ids...> --into <承载卡>",
	Short: "并入承载卡（改变多卡呈现，需确认或 --yes；跨基线/链式会被拒）",
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if cardMergeInto == "" {
			return fmt.Errorf("--into 必填")
		}
		if err := confirmDestructive(cmd, cardMergeYes,
			fmt.Sprintf("把 %s 并入 %s（被并卡将跟随承载卡流转）", strings.Join(args, ","), cardMergeInto)); err != nil {
			return err
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.MergeCards(args, cardMergeInto, ledgerActor()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardUnmergeCmd = &cobra.Command{
	Use:   "unmerge <id>",
	Short: "拆回（恢复自主流转，验收记录无损；恢复性操作不设确认）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.UnmergeCard(args[0], ledgerActor()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

var cardSplitCmd = &cobra.Command{
	Use:   "split <id> <标题>",
	Short: "拆子卡（自动挂 split_from）",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		child, err := st.SplitCard(args[0], strings.Join(args[1:], " "), ledgerActor())
		if err != nil {
			return err
		}
		return printCardJSON(cmd, child)
	},
}

var cardNoteCmd = &cobra.Command{
	Use:   "note <id> <text...>",
	Short: "记一笔（#B 号引用自动成关系边；--correction 记更正；--reset-node 重置节点回合计数）",
	Args:  cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		kind := "普通"
		if cardNoteFix {
			kind = "更正"
		}
		body := strings.Join(args[1:], " ")
		var ev ledger.Event
		if cardNoteResetNode != "" {
			// spec §5：人工介入重置回合计数，落事件注明——这是唯一写入口，
			// Plan C 的 CountRounds 读 human_reset_node 字段清零
			ev, err = st.AddCommentReset(args[0], body, kind, ledgerActor(), cardNoteResetNode)
		} else {
			ev, err = st.AddComment(args[0], body, kind, ledgerActor())
		}
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(ev)
	},
}

var cardExportCmd = &cobra.Command{
	Use:   "export",
	Short: "导出最薄 markdown 只读快照（逃生门；--out 写文件，缺省 stdout）",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		views, err := st.ListCards(ledger.CardFilter{IncludeTerminal: true})
		if err != nil {
			return err
		}
		var b strings.Builder
		b.WriteString("# 账本快照（只读导出）\n\n| ID | 标题 | 状态 | 优先级 | 项目 | 标记 |\n|---|---|---|---|---|---|\n")
		for _, v := range views {
			var marks []string
			if v.Following != "" {
				marks = append(marks, "跟随 "+v.Following)
			}
			if v.NeedsReason != "" {
				marks = append(marks, "⚑ "+v.NeedsReason)
			}
			fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s |\n",
				v.ID, v.Title, v.Status, v.Priority, v.Project, strings.Join(marks, " "))
		}
		if cardExportOut != "" {
			return os.WriteFile(cardExportOut, []byte(b.String()), 0o644)
		}
		_, err = fmt.Fprint(cmd.OutOrStdout(), b.String())
		return err
	},
}
```

`init()` 追加（并入 Task 2 的 init）：

```go
	cardCloseCmd.Flags().StringVar(&cardCloseReason, "reason", "", "取消|废弃|搁置（必填）")
	cardCloseCmd.Flags().BoolVar(&cardCloseYes, "yes", false, "跳过确认")
	_ = cardCloseCmd.MarkFlagRequired("reason")
	cardMergeCmd.Flags().StringVar(&cardMergeInto, "into", "", "承载卡 id（必填）")
	cardMergeCmd.Flags().BoolVar(&cardMergeYes, "yes", false, "跳过确认")
	cardNoteCmd.Flags().BoolVar(&cardNoteFix, "correction", false, "记为更正（变更痕迹）")
	cardNoteCmd.Flags().StringVar(&cardNoteResetNode, "reset-node", "", "重置该节点的裁决回合计数（如 review）")
	cardExportCmd.Flags().StringVar(&cardExportOut, "out", "", "输出文件路径")
	cardCmd.AddCommand(cardCloseCmd, cardReviveCmd, cardLinkCmd, cardUnlinkCmd,
		cardMergeCmd, cardUnmergeCmd, cardSplitCmd, cardNoteCmd, cardExportCmd)
```

（import 需补 `"os"`。）

- [ ] **Step 4: 跑测试确认通过 + Commit**

Run: `go test ./cmd/ -run TestCard -v` → PASS

```bash
git add cmd/
git commit -m "feat(cli): card close/revive/link/merge/unmerge/split/note/export——三处破坏确认按约定落位"
```

---

### Task 4: workflow 命令族 + 账本库 MigrateCardWorkflow

**Files:**
- Create: `cmd/workflow.go`
- Modify: `internal/ledger/workflows.go`（追加 MigrateCardWorkflow）
- Test: `cmd/workflow_test.go`、`internal/ledger/workflows_test.go`（追加）

- [ ] **Step 1: 写失败测试**

`internal/ledger/workflows_test.go` 追加：

```go
func TestMigrateCardWorkflow(t *testing.T) {
	s := newTestStore(t)
	def := WorkflowDef{States: []string{"待办", "进行中", "已完成"}}
	_, _ = s.PutWorkflow("wf", def)
	c, _ := s.CreateCard(NewCard{Title: "t", Project: "p", Workflow: "wf", Actor: "test"})
	def.States = []string{"待办", "评审中", "已完成"} // v2 删掉了「进行中」
	_, _ = s.PutWorkflow("wf", def)

	// 卡在 v1 的「待办」——v2 里仍有该状态，迁移放行
	if err := s.MigrateCardWorkflow(c.ID, 2, "test"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	got, _ := s.GetCard(c.ID)
	if got.WorkflowVersion != 2 {
		t.Fatalf("版本未迁: %+v", got)
	}
	// 迁回 v1、推进到「进行中」再迁 v2：当前状态不在新版，拒绝（防在途卡悬空）
	_ = s.MigrateCardWorkflow(c.ID, 1, "test")
	_ = s.MoveCard(c.ID, "进行中", "", "test")
	if err := s.MigrateCardWorkflow(c.ID, 2, "test"); err == nil {
		t.Fatal("状态悬空应拒")
	}
}
```

`cmd/workflow_test.go`：

```go
package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkflowListShowPut(t *testing.T) {
	dir := t.TempDir()
	out, _, err := runLedgerCLI(t, dir, "workflow", "list")
	if err != nil || !strings.Contains(out, "feature") {
		t.Fatalf("list: %v %q", err, out)
	}
	out, _, err = runLedgerCLI(t, dir, "workflow", "show", "feature")
	if err != nil || !strings.Contains(out, "已出spec") {
		t.Fatalf("show: %v %q", err, out)
	}
	// put 新版本
	defPath := filepath.Join(dir, "def.json")
	if err := os.WriteFile(defPath,
		[]byte(`{"states":["待办","进行中","已完成"],"gates":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, _, err = runLedgerCLI(t, dir, "workflow", "put", "hotfix", "--file", defPath)
	if err != nil || !strings.Contains(out, `"version":1`) {
		t.Fatalf("put: %v %q", err, out)
	}
}
```

- [ ] **Step 2: 实现 MigrateCardWorkflow（internal/ledger/workflows.go 追加）**

```go
// MigrateCardWorkflow 把卡显式迁到本工作流的另一个版本。防悬空校验：
// 卡的当前状态必须存在于目标版本的 States（否则拒绝，让人先挪状态）。
func (s *Store) MigrateCardWorkflow(cardID string, toVersion int, actor string) error {
	return s.mutate(func(tx *sql.Tx, sink *eventSink) error {
		c, err := getCardTx(s, tx, cardID)
		if err != nil {
			return fmt.Errorf("迁工作流: 卡 %s: %w", cardID, err)
		}
		target, err := s.getWorkflowTx(tx, c.WorkflowName, toVersion)
		if err != nil {
			return err
		}
		found := c.Status == StatusClosed // 终止态卡不受 States 约束
		for _, st := range target.Def.States {
			if st == c.Status {
				found = true
				break
			}
		}
		if !found {
			log().Warn("迁移被拒：状态悬空", "card", cardID, "status", c.Status, "to_version", toVersion)
			return fmt.Errorf("卡 %s 当前状态 %q 不在 %s v%d 中，先转移状态再迁: %w",
				cardID, c.Status, c.WorkflowName, toVersion, ErrBadState)
		}
		if _, err := tx.Exec(s.q(`UPDATE cards SET workflow_version = ?, updated_at = ? WHERE id = ?`),
			toVersion, s.tval(time.Now()), cardID); err != nil {
			return fmt.Errorf("写迁移: %w", err)
		}
		_, err = s.appendEvent(tx, sink, cardID, EvComment, actor,
			map[string]any{"kind": "普通", "body": fmt.Sprintf("工作流迁至 %s v%d", c.WorkflowName, toVersion)})
		return err
	})
}
```

- [ ] **Step 3: 实现 cmd/workflow.go**

```go
// handoff workflow 命令族：状态机形状聚合的命令面。不可变版本化——
// put 永远产生新版本；migrate 是三处破坏确认之一（批量改卡的呈现）。
package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/Xsxdot/handoff/internal/ledger"
	"github.com/spf13/cobra"
)

var workflowCmd = &cobra.Command{Use: "workflow", Short: "工作流聚合（状态机形状，不可变版本化）"}

var (
	wfShowVersion int
	wfPutFile     string
	wfMigrateTo   int
	wfMigrateYes  bool
)

var wfListCmd = &cobra.Command{
	Use: "list", Short: "列全部工作流（名 + 最新版本 + 状态序列）", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		w := tabwriter.NewWriter(cmd.OutOrStdout(), 2, 4, 2, ' ', 0)
		fmt.Fprintln(w, "名称\t最新版\t状态序列")
		for _, name := range []string{"feature", "bug"} { // 出厂两条恒在
			wf, err := st.GetWorkflow(name, 0)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s\tv%d\t%v\n", wf.Name, wf.Version, wf.Def.States)
		}
		// 用户自建工作流：ListWorkflows 若 Plan A 未提供，此处用
		// GetWorkflow 逐名查不可行——改为账本库补一个 ListWorkflowNames：
		names, err := st.ListWorkflowNames()
		if err != nil {
			return err
		}
		for _, name := range names {
			if name == "feature" || name == "bug" {
				continue
			}
			wf, err := st.GetWorkflow(name, 0)
			if err != nil {
				return err
			}
			fmt.Fprintf(w, "%s\tv%d\t%v\n", wf.Name, wf.Version, wf.Def.States)
		}
		return w.Flush()
	},
}

var wfShowCmd = &cobra.Command{
	Use: "show <name>", Short: "看工作流定义（--version 指定版本，缺省最新；单 JSON）", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		wf, err := st.GetWorkflow(args[0], wfShowVersion)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(wf)
	},
}

var wfPutCmd = &cobra.Command{
	Use: "put <name> --file <def.json>", Short: "写入新版本（不改旧版）", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		raw, err := os.ReadFile(wfPutFile)
		if err != nil {
			return fmt.Errorf("读定义文件: %w", err)
		}
		var def ledger.WorkflowDef
		if err := json.Unmarshal(raw, &def); err != nil {
			return fmt.Errorf("解析定义: %w", err)
		}
		if len(def.States) < 2 {
			return fmt.Errorf("状态序列至少两个状态")
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		ver, err := st.PutWorkflow(args[0], def)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"name": args[0], "version": ver})
	},
}

var wfMigrateCmd = &cobra.Command{
	Use: "migrate <card-id>", Short: "把卡迁到其工作流的指定版本（--to；需确认或 --yes）", Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if wfMigrateTo <= 0 {
			return fmt.Errorf("--to 必填且为正整数")
		}
		if err := confirmDestructive(cmd, wfMigrateYes,
			fmt.Sprintf("把 %s 迁到工作流 v%d", args[0], wfMigrateTo)); err != nil {
			return err
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.MigrateCardWorkflow(args[0], wfMigrateTo, ledgerActor()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	wfShowCmd.Flags().IntVar(&wfShowVersion, "version", 0, "指定版本（0=最新）")
	wfPutCmd.Flags().StringVar(&wfPutFile, "file", "", "定义 JSON 文件（必填）")
	_ = wfPutCmd.MarkFlagRequired("file")
	wfMigrateCmd.Flags().IntVar(&wfMigrateTo, "to", 0, "目标版本（必填）")
	wfMigrateCmd.Flags().BoolVar(&wfMigrateYes, "yes", false, "跳过确认")
	workflowCmd.AddCommand(wfListCmd, wfShowCmd, wfPutCmd, wfMigrateCmd)
	rootCmd.AddCommand(workflowCmd)
}
```

配套在 `internal/ledger/workflows.go` 追加 `ListWorkflowNames`：

```go
// ListWorkflowNames 全部工作流名（去重升序）。
func (s *Store) ListWorkflowNames() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT name FROM workflows ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("列工作流名: %w", err)
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
```

- [ ] **Step 4: 全量跑测试（含 Task 1 遗留的 TestOpenLedgerFallbackSQLite 此刻必须转绿）+ Commit**

Run: `go test ./cmd/ -v && go test ./internal/ledger/ -v` → 全 PASS

```bash
git add cmd/ internal/ledger/
git commit -m "feat(cli): workflow list/show/put/migrate + 账本库 MigrateCardWorkflow（状态悬空防护）"
```

---

### Task 5: decision 命令族

**Files:**
- Create: `cmd/decision.go`
- Test: `cmd/decision_test.go`

- [ ] **Step 1: 写失败测试**

```go
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecisionOpenListAnswer(t *testing.T) {
	dir := t.TempDir()
	out, _, _ := runLedgerCLI(t, dir, "card", "add", "有请示的卡", "--project", "demo", "--workflow", "bug")
	var c struct{ ID string `json:"id"` }
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &c)

	out, _, err := runLedgerCLI(t, dir, "decision", "open", "合并顺序怎么定？",
		"--card", c.ID, "--option", "done 时序", "--option", "依赖序")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var d struct{ ID int64 `json:"ID"` }
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &d); err != nil || d.ID == 0 {
		t.Fatalf("open 输出: %q", out)
	}
	// 项目级
	if _, _, err := runLedgerCLI(t, dir, "decision", "open", "推不推汇流线？"); err != nil {
		t.Fatalf("project-level: %v", err)
	}
	// list 缺省只列 open
	out, _, err = runLedgerCLI(t, dir, "decision", "list")
	if err != nil || strings.Count(out, "\n") != 2 {
		t.Fatalf("open 列表应两行: %v %q", err, out)
	}
	// answer
	if _, _, err := runLedgerCLI(t, dir, "decision", "answer", "1", "done 时序"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	out, _, _ = runLedgerCLI(t, dir, "decision", "list")
	if strings.Count(out, "\n") != 1 {
		t.Fatalf("答复后 open 应剩一行: %q", out)
	}
	// --all 两行且含答案
	out, _, _ = runLedgerCLI(t, dir, "decision", "list", "--all")
	if strings.Count(out, "\n") != 2 || !strings.Contains(out, "done 时序") {
		t.Fatalf("--all: %q", out)
	}
}
```

- [ ] **Step 2: 实现 cmd/decision.go**

```go
// handoff decision 命令族：裁决项。主会话回合末 open、用户 answer、
// 会话唤醒后 list 读答复——「推不推等你一句话」的闭环三件套。
package cmd

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
)

var decisionCmd = &cobra.Command{Use: "decision", Short: "裁决项（结构化请示：开/列/答）"}

var (
	decOpenCard    string
	decOpenOptions []string
	decListAll     bool
)

var decOpenCmd = &cobra.Command{
	Use: "open <正文...>", Short: "开裁决（--card 挂卡，缺省项目级；--option 可多次）", Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		d, err := st.OpenDecision(decOpenCard, strings.Join(args, " "), decOpenOptions, ledgerActor())
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(d)
	},
}

var decListCmd = &cobra.Command{
	Use: "list", Short: "列裁决（缺省只列未答复=全局裁决收件箱；--all 全量）", Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		ds, err := st.ListDecisions(!decListAll)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		for _, d := range ds {
			if err := enc.Encode(d); err != nil {
				return err
			}
		}
		return nil
	},
}

var decAnswerCmd = &cobra.Command{
	Use: "answer <id> <答复...>", Short: "答复裁决（答案落账 + 事件流，已答复的不许改）", Args: cobra.MinimumNArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("裁决 id 应为数字（D-3 写 3）: %w", err)
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.AnswerDecision(id, strings.Join(args[1:], " "), ledgerActor()); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), `{"ok":true}`)
		return nil
	},
}

func init() {
	decOpenCmd.Flags().StringVar(&decOpenCard, "card", "", "挂到卡（缺省项目级）")
	decOpenCmd.Flags().StringArrayVar(&decOpenOptions, "option", nil, "候选项（可多次）")
	decListCmd.Flags().BoolVar(&decListAll, "all", false, "含已答复")
	decisionCmd.AddCommand(decOpenCmd, decListCmd, decAnswerCmd)
	rootCmd.AddCommand(decisionCmd)
}
```

- [ ] **Step 3: 跑测试确认通过 + Commit**

Run: `go test ./cmd/ -run TestDecision -v` → PASS

```bash
git add cmd/
git commit -m "feat(cli): decision open/list/answer——回合末请示闭环的命令面"
```

---

### Task 6: 整包终审

- [ ] **Step 1: 终审命令**

```bash
gofmt -l cmd/ internal/ledger/          # 无输出
go vet ./cmd/ ./internal/ledger/        # 无输出
go test ./... 2>&1 | tail -20           # 全仓全绿
```

- [ ] **Step 2: 对照 spec §4 逐条自检**

spec §4 命令清单核对：card add/list/show/update/close ✓（revive 是 close(搁置) 的对偶，补充实现）；link/unlink ✓；merge/unmerge/split ✓；note ✓；move ✓；export ✓；workflow/template——workflow ✓，**template 命令族归 Plan C**（模板与派发同交付）；wait --card 归 Plan B；decision ✓；card dispatch 归 Plan C。二次确认三处（close/merge/migrate）✓；executor 白名单未动 ✓（fail-closed 自动拒新命令）。

- [ ] **Step 3: Commit**

```bash
git add -A
git commit -m "test(cli): 账本命令面整包终审——gofmt/vet/全仓测试"
```

---

## Self-Review 记录

1. **Spec 覆盖**：§4 除归属 Plan B/C 的三项（wait --card、template、dispatch）外全部落位，归属在 Task 6 Step 2 写明。
2. **类型一致性**：`openLedger`/`ledgerActor`/`confirmDestructive` 定义于 Task 1，Task 2–5 使用处签名一致；`UpdateCardMeta`/`MigrateCardWorkflow`/`ListWorkflowNames` 三个账本库补充操作各带测试。
3. **已知妥协**：`workflow list` 对出厂两条硬编码优先展示（用户流靠 ListWorkflowNames 补全）；CLI 每条命令独立 Open/Close 账本（进程短命，连接池不值得）；`decision answer` 的 id 输出字段名依赖 ledger.Decision 的 JSON 缺省大写字段（`d.ID` → `"ID"`——测试按此断言；若要小写线格式，Plan D 做 HTTP 面时统一，CLI 不先造一套）。
4. **测试基座**：`runLedgerCLI` 依赖 rootCmd 可重入（resetPerRunState 已有）；`config.Save` 签名以实际为准。
