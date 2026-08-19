# 存量切换 + skill 切换（Plan E / B156.1-切换）Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **08-19 重写（用户裁决）：不做存量导入。** 原方案是解析 backlog.md 全量导入
> （backlogmig 包：parse/convert/importer 四个 task）。计划审查在真实总账上
> 实测出导入器接不住的形态——🚫/🔀/🐛 三族状态未映射（迁移必 BLOCKED）、
> `✅ done(真机验)` 会被 `Contains("已验")` 静默错账、B80-B83/B89/B90 六对
> 真撞号会被幂等语义静默丢半（伪装成正常 Skipped）。用户裁决：**已有 backlog
> 不导入**，旧账冻结为历史正史，账本从零记新账，未完成项由一个 agent 用 CLI
> 直接补录。原四个代码 task 全部作废，本 plan 收缩为：一个垫号命令 + 审核者
> 本地切换清单。对应 spec §8（08-19 修订版）与判据⑧（修订版）。

**Goal:** 完成从 markdown 总账到账本的**切换收尾**：`card min-b` 垫 B 号水位（新旧两本账永不撞号）＋审核者本地清单（冻结注记、未完成项补录、skill 切换、判据⑧核对）。

**Tech Stack:** Go（一个 Hidden cobra 子命令，全部复用 Plan A/A2 既有构件）。

**前置条件：** Plan A/A2 已合入（EnsureMinB 在 Plan A Task 5、CLI 基座在 A2）。**切换执行**（审核者清单部分）另有前置：B156.1 全部 plan 验收通过——切换是账本域一期的最后一步，账本自己还没验收就把总账切过去等于把家当搬进没验收的房子。

---

## File Structure

```
cmd/
  card_minb.go        // card min-b <n>（Hidden）：垫 B 号水位
  card_minb_test.go
```

（`EnsureMinB` 已在 Plan A 的 ledger 包里，本 plan 只做 CLI 暴露。）

---

### Task 1: card min-b——B 号水位垫号命令

**Files:**
- Create: `cmd/card_minb.go`
- Test: `cmd/card_minb_test.go`

- [ ] **Step 1: 测试**

```go
package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// 垫号后新建卡号严格大于水位——判据⑧的单测形。
func TestCardMinB(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := runLedgerCLI(t, dir, "card", "min-b", "156"); err != nil {
		t.Fatalf("min-b: %v", err)
	}
	out, _, err := runLedgerCLI(t, dir, "card", "add", "垫号后的第一张卡", "--project", "demo")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	var c struct{ ID string `json:"id"` }
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &c)
	if c.ID != "B157" {
		t.Fatalf("垫号未生效，应 B157 实为 %s", c.ID)
	}
	// 水位只升不降：往回垫是无操作不是报错（幂等重跑安全）
	if _, _, err := runLedgerCLI(t, dir, "card", "min-b", "100"); err != nil {
		t.Fatalf("回垫应为无操作: %v", err)
	}
	out, _, _ = runLedgerCLI(t, dir, "card", "add", "又一张", "--project", "demo")
	_ = json.Unmarshal([]byte(strings.TrimSpace(out)), &c)
	if c.ID != "B158" {
		t.Fatalf("回垫不应降水位，应 B158 实为 %s", c.ID)
	}
	// 非数字参数干净报错
	if _, _, err := runLedgerCLI(t, dir, "card", "min-b", "abc"); err == nil {
		t.Fatal("非数字应拒")
	}
}
```

- [ ] **Step 2: 跑测试确认失败**

Run: `go test ./cmd/ -run TestCardMinB -v` → FAIL（unknown command "min-b"）

- [ ] **Step 3: 实现 cmd/card_minb.go**

```go
// card min-b：切换期一次性命令——把 B 号水位垫到历史总账 max B，
// 此后新建卡号严格大于历史号，markdown 旧账与账本新账永不撞号。
// Hidden：日常工作流用不到它，藏起来防误用。
package cmd

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

var cardMinBCmd = &cobra.Command{
	Use:    "min-b <n>",
	Short:  "垫 B 号水位（切换期一次性；只升不降）",
	Hidden: true,
	Args:   cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 0 {
			return fmt.Errorf("水位必须是非负整数，收到 %q", args[0])
		}
		st, err := openLedger()
		if err != nil {
			return err
		}
		defer st.Close()
		if err := st.EnsureMinB(n); err != nil { // 语义：只升不降（Plan A Task 5）
			return fmt.Errorf("垫号: %w", err)
		}
		fmt.Fprintf(cmd.OutOrStdout(), `{"ok":true,"min_b":%d}`+"\n", n)
		return nil
	},
}

func init() {
	cardCmd.AddCommand(cardMinBCmd)
}
```

**执行者注意**：`EnsureMinB` 的确切签名/语义以 Plan A 落地后的 `internal/ledger` 实际代码为准——若它落地为「设置下界、已更高则无操作」以外的语义，以「只升不降 + 幂等」为准修这里的调用方式，别改库。

- [ ] **Step 4: 跑测试 + Commit**

Run: `go test ./cmd/ -run TestCardMinB -v` → PASS

```bash
git add cmd/
git commit -m "feat(cli): card min-b——切换期 B 号水位垫号（Hidden，只升不降）"
```

---

### Task 2: 终审

- [ ] `gofmt -l cmd/ ; go vet ./cmd/... ; go test ./cmd/ -count=1` 全绿。
- [ ] 对照 spec §8（08-19 修订版）：代码面只有垫号命令 ✓；导入器不存在 ✓（`git grep backlogmig` 零命中）。
- [ ] Commit：`test(cli): 切换收尾终审`

---

## 审核者本地清单（不派发；按序执行，前置 = B156.1 全部 plan 验收通过）

> 涉及 `~/.claude` 全局 skill、汇流点分支正史、以及驱动 handoff 自身——
> 全部审核者本地，永不进派发包（B105 教训）。

1. **对齐汇流点**：web-console（或届时的汇流分支）与 main 实测 merge-base 确认无分叉遗漏（memory 教训：判分叉用实测不靠行数）；备份 backlog.md。
2. **垫号**：在冻结时刻的总账上取 max B（`awk -F'|' '/^\|/ {gsub(/ /,"",$2)} $2 ~ /^B[0-9]+$/ {sub(/^B/,"",$2); if ($2+0>m) m=$2+0} END{print m}' docs/superpowers/backlog.md`——重号条目取号不受影响，max 就是 max），跑 `handoff card min-b <maxB>`，再 `card add` 一张验证卡确认新号 > maxB（判据⑧的垫号半句）。
3. **skill 切换（先于冻结）**：`~/.claude/skills/product-backlog` 改写为指向 card 命令族的指针（记需求 = `card add`、领活 = `card list --needs` + `card move`、排期语义并入优先级字段）；CLAUDE.md §3 的 product-backlog 行同步改；handoff skill 补 card 命令族一节。**先切 skill 再冻结文件**——顺序反了会有一段真空期，在途 worktree 的旧 skill 副本还在往冻结前的文件里追加。
4. **冻结总账**：backlog.md 顶部加注记「本账已冻结（日期）：新事项一律 `handoff card add`，本文件保留为历史正史不再更新；未完成项已补录进账本（新卡首条 note 含旧号）」。提交进汇流点分支。
5. **未完成项补录（派一个本地 agent 执行，交互式看着它跑）**：
   - 范围 = 状态为 💡 / 📦 / 📋 / 🔨 / 🐛 / 🧊 的条目（按 §8 修订版；🗄️ shelved 不迁——真要复活时再开新卡；done 系含未验/待真机验一律留历史账，要补验另开新卡引用旧号）。冻结时刻实测范围数量级 ≈ 15-20 条，人工可核。
   - 每条：`card add <标题> --project <项目>`（📋 有 spec 的接 `card update --attach spec:<路径>` 再 `card move <id> 已出spec`；🔨 的推进到对应态）；首条 `card note <新id> "迁自 B<旧号>：<原备注/变更痕迹摘录>"`；已知依赖用 `card link`，相关引用在 note 里写 `#B<新号>` 自动成边。
   - agent 的输入 = 冻结后的 backlog.md 原文 + 本清单第 5 条原文；输出 = 新旧号对照表（贴回本清单存档）。
6. **判据⑧核对（修订版）**：补录卡逐张核（数量少全查不抽查）——首条 note 的旧号在冻结文件里 grep 命中、spec 附件路径存在、验证卡号 > 水位。
7. **B156.1 收口**：evidence gate——判据①-⑭ 全部有据后，把 B156.1 推「已完成」（此时已在账本里推卡，不再写 markdown）。

---

## Self-Review 记录

1. **本 plan 的代码面收缩到一个命令是刻意的**：切换的复杂度全在流程序（先 skill 后冻结、先验收后切换、补录范围裁剪），这些是审核者清单不是可派发代码——原方案把 160 条历史数据的解析当工程做，审查证明数据形态比解析器假设脏（三族未映射 + 静默错账 + 六对重号），修解析器不如不解析。
2. **补录走 CLI 而非直插库**：`card add` 拿的是新号（垫号后），旧号只活在 note 里——两本账身份彻底分离，不存在「同一个 B 号在两处指不同的事」（原方案 B80 撞号问题的病根）。代价：历史考古要跨两本账（grep 冻结文件 + note 检索），接受。
3. **backlogmig／ImportCard 不再存在**：原方案的 `internal/backlogmig` 包与 `card import-backlog` 命令随本次重写整体作废，不实现；Plan A 的 `ImportCard`（指定 id 直插）若已实现则保留不删——无害的库能力，但一期没有调用方。终审的 `git grep backlogmig` 零命中是收缩的证明。
