# Plan：graph sym 单点符号查询 + 采纳接线（codegraph 一期）

日期 2026-08-22 · 前置 [总纲 spec §4](2026-08-22-codegraph-agent-navigation-master.md) · 节点 `charter:plan` · 级别 L2

**执行划分**：Task 1、Task 2 派发执行；**Task 3 由协调者本地执行、不派发**（含 charter 全局仓改动与驱动 handoff 自身的验收，与执行纪律的「不调派发 CLI」冲突）。

## 基线判据复核记录（2026-08-22 已在基线跑过）

| 判据 | 基线实测 |
|---|---|
| `go build ./...` | exit 0 |
| `go test ./internal/codegraph/` | ok 0.726s |
| `go test ./cmd/` 现有 graph 测试形态 | `runGraph` helper + `fixtureRepo = "../internal/codegraph/testdata/repo"`（cmd/graph_test.go:15-26） |
| `graph chain UpsertSpend` | Error「节点 "UpsertSpend" 不在图中；近似候选: n_store_Store_UpsertSpend(Store.UpsertSpend)」——**裸方法名不命中是本 plan 要修的可用性缺口** |
| 夹具 repo 锚点 | `n_do`=Server.Do @ svc/server.go:4，源文件该行确为 `func (s *Server) Do() error {`；`e_skip` 带 `unscanned:true` |
| 既有锚点校验 | `CheckStale`（internal/codegraph/stale.go:30）：三级校验（文件存在→行号在界→行窗口 line-1..line+1 找 token；func 取 Name 尾段、model 取整名、entry 跳过 token 级）——ReAnchor 与它同源 |
| 命令注册点 | cmd/graph.go:333 `graphCmd.AddCommand(...)`；flag 复位 `graphResetState`（cmd/graph.go:69） |

## Task 1：codegraph 包 sym 查询与再锚定（派发）

**文件**：新建 `internal/codegraph/sym.go`、`internal/codegraph/sym_test.go`。

**Interfaces — Produces**（Task 2 消费，签名逐字）：

```go
func SymLookup(v *View, repoRoot, arg string) (*SymResult, error)
func ReAnchor(repoRoot string, n Node) (int, string)
type SymResult struct { View string; Query string; Matches []SymMatch }
type SymMatch struct { ID string; Domain string; Anchor string; ViewNode }
```

**Consumes**：`View`/`ViewNode`/`Node`（merge.go、types.go 现有定义，零修改）。

**步骤**：

1. 写失败测试 `sym_test.go`（全部用例见下），`go test ./internal/codegraph/ -run 'TestSym|TestReAnchor'` 跑红（编译错也算红）。
2. 落实现 `sym.go`，完整代码：

```go
// 职责：graph sym 的单点符号查询——名字决议（含方法尾段匹配）与查询时再锚定。
// 边界：只读仓库文件做锚定校验，不回写图数据；不做邻域遍历（那是 query.go 的活）。
package codegraph

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// SymMatch 是 sym 查询的一条结果卡片：节点全量信息 + 归属 + 再锚定结论。
type SymMatch struct {
	ID     string `json:"id"`
	Domain string `json:"domain,omitempty"` // 所属容器的领域 id；容器缺失或未归域时为空
	// Anchor 是再锚定结论：ok（Line 可用）/ moved（已就近重找，Line 是新行号）/
	// vanished（文件在但符号消失，Line 保留图值仅供参考）/ file_missing（文件不存在）/
	// unscanned（节点未扫描或无文件，不做锚定）。
	Anchor string `json:"anchor"`
	ViewNode
}

// SymResult 是一次 sym 查询的完整输出。多义时 Matches 含全部命中，由调用方自选。
type SymResult struct {
	View    string     `json:"view"`
	Query   string     `json:"query"`
	Matches []SymMatch `json:"matches"`
}

// SymLookup 决议 arg 并对每个命中节点做查询时再锚定。决议优先级：
// 节点 id 精确 > Name 精确 > 方法名尾段精确（"UpsertSpend" 命中 "Store.UpsertSpend"）。
// 多义时全部返回；未命中返回错误，错误文本带近似候选与覆盖债提示——
// 「图未覆盖」必须显式可见，agent 才知道该回落 grep 并记债（总纲 spec 用户故事 3）。
func SymLookup(v *View, repoRoot, arg string) (*SymResult, error) {
	ids := symResolve(v, arg)
	if len(ids) == 0 {
		return nil, fmt.Errorf(
			"符号 %q 不在图中（图未覆盖或名字有误）；近似候选: [%s]。确认图未覆盖时回落 grep，并把该符号记入本节点产出物的「图覆盖债」小节",
			arg, strings.Join(symFuzzy(v, arg), ", "))
	}
	r := &SymResult{View: v.Name, Query: arg}
	for _, id := range ids {
		n := v.Nodes[id]
		m := SymMatch{ID: id, ViewNode: n}
		if c, ok := v.Containers[n.Container]; ok {
			m.Domain = c.Domain
		}
		if n.Unscanned || n.File == "" {
			m.Anchor = "unscanned"
		} else {
			line, status := ReAnchor(repoRoot, n.Node)
			m.Line = line
			m.Anchor = status
		}
		r.Matches = append(r.Matches, m)
	}
	return r, nil
}

// symResolve 返回决议命中的节点 id 集（有序）。三级优先，高优先级命中即短路：
// 同级多命中不合并跨级——避免「精确名 + 尾段」混在一起让 agent 误读多义。
func symResolve(v *View, arg string) []string {
	if _, ok := v.Nodes[arg]; ok {
		return []string{arg}
	}
	var exact, tail []string
	for id, n := range v.Nodes {
		switch {
		case n.Name == arg:
			exact = append(exact, id)
		case symTail(n.Name) == arg:
			tail = append(tail, id)
		}
	}
	if len(exact) > 0 {
		sort.Strings(exact)
		return exact
	}
	sort.Strings(tail)
	return tail
}

// symTail 取 "Store.UpsertSpend" 的 "UpsertSpend"；无 '.' 时返回原名。
func symTail(name string) string {
	if i := strings.LastIndex(name, "."); i >= 0 {
		return name[i+1:]
	}
	return name
}

// symFuzzy 返回 contains 近似候选（最多 5 个），形态与 Resolve 的候选一致。
func symFuzzy(v *View, arg string) []string {
	low := strings.ToLower(arg)
	var out []string
	for id, n := range v.Nodes {
		if strings.Contains(strings.ToLower(n.Name), low) {
			out = append(out, id+"("+n.Name+")")
		}
	}
	sort.Strings(out)
	if len(out) > 5 {
		out = out[:5]
	}
	return out
}

// ReAnchor 对一个节点做查询时锚定，返回（可用行号, 结论）。
// token 规则与 CheckStale 同源：func 取 Name 尾段，其余取整名；entry 不做 token
// 级校验（注册行长相多样，同 stale.go 的理由），文件在即 ok。
// 窗口 line-1..line+1 内按词边界找到 token 即 ok；否则全文件重找：
// 优先「定义形状」行（去空白后以 func/type/export/interface 开头），
// 无定义形状取首个词边界命中行；全文件无命中 → vanished。
func ReAnchor(repoRoot string, n Node) (int, string) {
	raw, err := os.ReadFile(filepath.Join(repoRoot, n.File))
	if err != nil {
		return n.Line, "file_missing"
	}
	lines := strings.Split(string(raw), "\n")
	if n.Kind == "entry" {
		return n.Line, "ok"
	}
	token := n.Name
	if n.Kind == "func" {
		token = symTail(n.Name)
	}
	if n.Line >= 1 && n.Line <= len(lines) {
		lo, hi := n.Line-2, n.Line+1 // 0 基切片，覆盖 1 基的 line-1..line+1
		if lo < 0 {
			lo = 0
		}
		if hi > len(lines) {
			hi = len(lines)
		}
		for _, l := range lines[lo:hi] {
			if symTokenOnLine(l, token) {
				return n.Line, "ok"
			}
		}
	}
	def, any := 0, 0
	for i, l := range lines {
		if !symTokenOnLine(l, token) {
			continue
		}
		if any == 0 {
			any = i + 1
		}
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "func ") || strings.HasPrefix(t, "type ") ||
			strings.HasPrefix(t, "export ") || strings.HasPrefix(t, "interface ") {
			def = i + 1
			break
		}
	}
	switch {
	case def > 0:
		return def, "moved"
	case any > 0:
		return any, "moved"
	default:
		return n.Line, "vanished"
	}
}

// symTokenOnLine 按词边界判断 token 是否出现在行内——裸 Contains 会把 "Do"
// 误配进 "Done"，再锚定就会锚错行，所以两侧字符必须都不是标识符字符。
func symTokenOnLine(line, token string) bool {
	for start := 0; ; {
		i := strings.Index(line[start:], token)
		if i < 0 {
			return false
		}
		i += start
		before := i == 0 || !isIdentChar(line[i-1])
		after := i+len(token) >= len(line) || !isIdentChar(line[i+len(token)])
		if before && after {
			return true
		}
		start = i + len(token)
	}
}

func isIdentChar(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}
```

3. `go test ./internal/codegraph/ -run 'TestSym|TestReAnchor'` 跑绿。
4. `gofmt -l internal/codegraph/`（空输出）→ 提交。

**测试用例（sym_test.go，全部必写）**——夹具视图从 `testdata/repo` 经 `LoadGraph` + `Merge(g, nil)` 构造；ReAnchor 漂移用例用 `t.TempDir()` 自建文件，不改共享夹具：

| 用例 | 断言 |
|---|---|
| `TestSymLookupTailMatch` | `SymLookup(v, repo, "Do")` → 1 条，ID `n_do`，Anchor `ok`，Line 4，Domain `d_svc/api`，Signature 非空 |
| `TestSymLookupExactName` | `"Server.Do"` → 1 条 `n_do` |
| `TestSymLookupByID` | `"n_do"` → 1 条 |
| `TestSymLookupAmbiguousTail` | 手工构造含 `A.Close`/`B.Close` 两节点的 View → `"Close"` 返回 2 条、按 id 排序 |
| `TestSymLookupMiss` | `"Nope"` → err 非 nil，文本含「图未覆盖」与「近似候选」 |
| `TestSymLookupUnscanned` | `"demo skip"` → Anchor `unscanned` |
| `TestReAnchorOK` | TempDir 写 5 行文件、token 在第 3 行、Node.Line=3 → (3, "ok") |
| `TestReAnchorMoved` | 同文件 Node.Line=1（窗口不含 token）→ 返回定义行行号与 "moved" |
| `TestReAnchorMovedPrefersDefinition` | 文件先出现注释提及 token、后出现 `func token(` 定义行 → 返回定义行 |
| `TestReAnchorVanished` | 文件不含 token → (原行, "vanished") |
| `TestReAnchorFileMissing` | 不存在的路径 → (原行, "file_missing") |
| `TestReAnchorWordBoundary` | 文件只含 "Done" → token "Do" 判 vanished（词边界防误配） |

**测试范围声明**：本 task 只跑 `go test ./internal/codegraph/`。
**日志声明**：codegraph 是纯查询库，包内既有惯例（query.go/stale.go）无 logger、错误语义即返回值，本 task 遵循之；CLI 层错误走 cobra stderr。此为与包惯例一致的显式决定，非漏项。
**注释**：文件头职责+边界、每个导出符号的注释已含在上方代码块内，落码时一并写入。

## Task 2：CLI 子命令 sym 与 summary（派发）

**文件**：`cmd/graph.go`（改）、`cmd/graph_test.go`（增测）。

**Interfaces — Consumes**：Task 1 的 `codegraph.SymLookup` / `SymResult`（签名见 Task 1）。**Produces**：`handoff graph sym <符号>`、`handoff graph summary` 两个子命令。

**步骤**：

1. `cmd/graph_test.go` 末尾加三个失败测试（见下），`go test ./cmd/ -run 'TestGraphSym|TestGraphSummary'` 跑红。
2. `cmd/graph.go` 在 `graphDomainsCmd` 定义之后（约 306 行处）插入：

```go
// graphSymCmd 单点符号查询：agent 探索「X 在哪 / 什么形状」的第一跳，
// 输出行号已做查询时再锚定（图数据允许陈旧，输出必须当下可用）。
var graphSymCmd = &cobra.Command{
	Use:   "sym <符号名或节点 id>",
	Short: "单点符号查询：位置（已再锚定）、签名、字段、摘要、归属",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		v, _, err := graphLoadView()
		if err != nil {
			return err
		}
		r, err := codegraph.SymLookup(v, graphRepo, args[0])
		if err != nil {
			return err
		}
		return graphPrintJSON(cmd, r)
	},
}

// graphSummaryCmd 输出一段图存在性摘要，供 SessionStart hook 注入会话上下文：
// 让 agent 开局就知道图存在、先查图再 grep。
var graphSummaryCmd = &cobra.Command{
	Use:   "summary",
	Short: "图摘要（供会话开局注入：规模、领域数、查询子命令菜单）",
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		g, err := codegraph.LoadGraph(graphRepo)
		if err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(),
			"本仓库有代码图：%d 节点 / %d 边 / %d 领域（codegraph/）。探索已有代码先查图：handoff graph sym <符号>（定位+签名+字段，行号已再锚定）、who-calls <符号>（上游影响面）、chain <符号>（下游链）、domains（领域树）；图未命中再 grep，并把未命中符号记入产出物的「图覆盖债」小节。\n",
			len(g.Nodes), len(g.Edges), len(g.Domains))
		return nil
	},
}
```

3. cmd/graph.go:333 的 AddCommand 追加两个命令：

```go
	graphCmd.AddCommand(graphValidateCmd, graphCheckCmd, graphAbsorbCmd, graphViewsCmd, graphChainCmd, graphWhoCallsCmd, graphDomainsCmd, graphSymCmd, graphSummaryCmd)
```

4. 跑绿 → `gofmt -l cmd/` 空 → 提交。

**测试用例（沿 runGraph/fixtureRepo 形态）**：

| 用例 | 断言 |
|---|---|
| `TestGraphSym` | `runGraph(t, "sym", "Do", "--repo", fixtureRepo)` → err nil；JSON 解出 `matches` 长度 1，`matches[0].id=="n_do"`、`anchor=="ok"`、`line==4`、`signature` 非空 |
| `TestGraphSymMiss` | `runGraph(t, "sym", "Nope", "--repo", fixtureRepo)` → err 非 nil，错误文本含「图未覆盖」 |
| `TestGraphSummary` | `runGraph(t, "summary", "--repo", fixtureRepo)` → err nil，输出含「节点」与「graph sym」（不锁具体数字，夹具会长大） |

**测试范围声明**：本 task 只跑 `go test ./cmd/ -run 'TestGraphSym|TestGraphSummary'` 与既有 `go test ./cmd/`。
**日志/注释**：同 Task 1 的声明；两个命令的职责注释已在代码块内。

## Task 3：采纳接线与验收实证（协调者本地执行，不派发）

前置：Task 1/2 已合并、`handoff` 二进制已重装为含 sym 的版本。

1. **charter 仓**（`~/workspace/charter`，独立提交）：
   - `skills/spec/SKILL.md` 「对话之前：事实调查」小节末尾加 bullet：
     > **有图先查图**：项目有 `codegraph/`（入库代码图）时，符号定位、签名/字段、调用关系优先走图查询（`handoff graph sym / who-calls / chain / domains`），未命中再 grep/读码，并把未命中符号记入本节点产出物的「图覆盖债」小节，由后续重扫消化。
   - `skills/plan/SKILL.md` 第 1 条「判据先在基线跑」末尾加句：
     > 现状签名与调用面的查证，项目有代码图时优先 `handoff graph sym / who-calls`，未命中再 grep 并记覆盖债。
2. **handoff 仓**新建 `.claude/settings.json`（入库）：

```json
{
  "hooks": {
    "SessionStart": [
      { "hooks": [ { "type": "command", "command": "handoff graph summary 2>/dev/null || true" } ] }
    ]
  }
}
```

   `|| true` + 静默 stderr：本机 handoff 版本旧（无 summary）或不在 PATH 时 hook 不报错、注入为空。
3. **验收实证**（真实仓库跑符号清单，记录命中率——同时作为覆盖派发卡的验收基线）：

```
for s in UpsertSpend handleSpend SpendEntry Cumulative TaskCumulative BeginTurn mapToolPart TuiTab; do handoff graph sym "$s"; done
```

   预期两阶段：覆盖卡（任务 62eafd7b）合并**前**——前 5 个命中（含尾段决议）、后 3 个走未命中路径且错误文本含记债提示；合并**后**——8 个全命中。
4. 新开一个 handoff 仓会话，确认开局上下文含图摘要行。

## 四项检查

1. **缺陷族对抗审查**：①序列化边界——SymResult 是 agent 消费的唯一投影，JSON 形状由 TestGraphSym 直接断言（穿过真实 cobra+encoder 链路），无手搭 map；②锚定 off-by-one——窗口切片边界有 TestReAnchorOK/Moved 双向夹逼；③假绿——TestReAnchorWordBoundary 是决定性反例（裸 Contains 会让它假绿）；④时序/资源/恢复——纯只读单次进程，无命中。
2. **序列化边界设问**：新增字段（Anchor/Domain）从产生到消费只有一处投影（graphPrintJSON），TestGraphSym 断言穿过它；`omitempty` 语义（Domain 空=未归域）在 SymMatch 注释写明。
3. **上下文预算**：Task 1 文件集 = sym.go + sym_test.go + 只读 types/merge/stale；Task 2 = cmd/graph.go + graph_test.go。均有界。
4. **类型标注**：d_contract 为 logic 域，机内闭环；无真机清单，但 Task 3 的实证与新会话摘要检查是显式人工验收步骤。

## 自审三查

- spec 覆盖：故事 1→T1/T2；故事 2→T1 多义用例；故事 3→T1 Miss 用例+错误文本；故事 4→T1 ReAnchor；故事 5→T2 summary+T3 hook；故事 6→T3 charter 条款。全指到。
- 占位符：无。
- 跨 task 签名：T2 消费的 `SymLookup(v, graphRepo, args[0])` 与 T1 Produces 逐字一致。
