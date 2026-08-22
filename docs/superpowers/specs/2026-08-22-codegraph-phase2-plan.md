# Plan：codegraph 二期——投影边、entity/resolve/contract set

日期 2026-08-22 · 前置 [总纲 §5](2026-08-22-codegraph-agent-navigation-master.md) · 节点 `charter:plan` · 级别 L2

**执行划分**：Task 1/2/3 派发（同一分支串行）；**Task 4 由协调者本地执行、不派发**（charter 仓、盘点派发、真机回归都驱动 handoff 自身）。

## 基线判据复核记录（2026-08-22 实测）

| 判据 | 基线实测 |
|---|---|
| `go build ./...` && `go test ./internal/codegraph/ ./cmd/` | 全绿（一期合并后复跑） |
| implements 先例四处 | types.go:88/101-102（`Edge` 二元组独立列表）、merge.go:46-101（基线并入+diff 叠加带 status）、validate.go:28/128（两端存在校验）、absorb.go:29/44（`mergeEdges` 併入） |
| `Contract`/`Target`/`LoadTarget`/`ValidateTarget` | target.go:41-55/61/87，Contract 含 From/To/Entries/Interfaces/LegacyBudget |
| Go↔TS 同名 model 匹配面 | 230 Go / 82 TS，同名 57 对（含 Cumulative） |
| 手搭点图内不可见实证 | `who-calls SpendEntry --depth 1` 只返回 `Store.UpsertSpend`；`Manager.handleSpend` 无边——投影边必须扫描产出 |
| 一期可复用件 | `ReAnchor`/`symTokenOnLine`/`symTail`（sym.go）、`graphPrintJSON`/`graphResetState`/`graphLoadView`（cmd/graph.go） |

## Task 1：schema 扩展——projections 与 projScanned（派发）

**文件**：`internal/codegraph/types.go`、`merge.go`、`validate.go`、`absorb.go` 及各自测试。

**Interfaces — Produces**（Task 2/3 消费，逐字）：

```go
type Projection [3]string // [投影点节点 id, model 节点 id, kind]
// Graph 增字段：  Projections []Projection `json:"projections,omitempty"`
// Diff 增字段：   ProjectionsAdded []Projection `json:"projectionsAdded,omitempty"`
//                ProjectionsDeleted []Projection `json:"projectionsDeleted,omitempty"`
// Node 增字段：   ProjScanned bool `json:"projScanned,omitempty"`
// View 增字段：   Projections []ViewProjection `json:"projections"`
type ViewProjection struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`   // typed | handroll | twin
	Status string `json:"status,omitempty"`
}
```

**步骤**：

1. 失败测试先行（`go test ./internal/codegraph/ -run 'TestProjection'` 跑红）：
   - `TestProjectionValidate`：夹具图加一条 kind 非法（"xx"）与一条端点悬空的 projection → validate 各报一条 issue；合法三 kind 全过。
   - `TestProjectionMerge`：diff 的 ProjectionsAdded 叠加后 View.Projections 带 `status:"added"`；ProjectionsDeleted 命中的标 `status:"deleted"`（键用 `from+"\x00"+to+"\x00"+kind`）。
   - `TestProjectionAbsorb`：absorb 后基线含新增边、剔除已删边；节点被删（dead）时其投影边一并剔除——对齐 absorb.go:44 mergeEdges 对 dead 的处理。
2. types.go 落四处字段 + `Projection` 类型注释（wire 兼容决策同 implements：独立顶层列表、存量 baseline 零迁移；kind 语义三行注释——typed=类型可见投影、handroll=手搭 map/字面量拼装（类型系统不可见）、twin=跨语言孪生 model↔model）。
3. merge.go：`Merge` 并入基线 projections；diff 叠加逻辑照 implements 块（merge.go:85-101）同构新写一段（键含 kind）。
4. validate.go：projections 两端节点存在 + kind ∈ {typed, handroll, twin}；diff 侧同查（照 validate.go:128 形态）。twin 边额外校验两端 kind 均为 model。
5. absorb.go：`mergeProjections`（照 mergeEdges 写三元组版，dead 节点连带剔除）。
6. 跑绿 → `gofmt -l internal/codegraph/` 空 → 提交。

**测试范围**：只跑 `go test ./internal/codegraph/`。**日志/注释**：同一期声明——纯库无 logger 为包惯例；新类型与字段注释在步骤 2 写明。

## Task 2：`graph entity` 查询（派发）

**文件**：新建 `internal/codegraph/entity.go`、`entity_test.go`；`cmd/graph.go` 加子命令。

**Interfaces — Produces**：

```go
func EntityLookup(v *View, repoRoot, arg string) (*EntityResult, error)
type EntityResult struct {
	View        string       `json:"view"`
	Query       string       `json:"query"`
	Model       SymMatch     `json:"model"`             // 复用 sym 卡片（含再锚定）
	Twins       []SymMatch   `json:"twins,omitempty"`    // twin 边指向的对侧 model
	Typed       []ProjSite   `json:"typed,omitempty"`
	Handroll    []ProjSite   `json:"handroll,omitempty"`
	ProjScanned bool         `json:"projScanned"`
	Warning     string       `json:"warning,omitempty"`  // 未盘点时非空
}
type ProjSite struct {
	SymMatch
	Via string `json:"via,omitempty"` // 边来源：direct（本 model）| twin:<对侧id>（孪生侧）
}
```

**Consumes**：Task 1 的 View.Projections/ViewProjection；一期 `SymLookup` 内部件（`ReAnchor`、容器域归属逻辑——从 sym.go 抽 `symMatchFor(v, repoRoot, id string) SymMatch` 小函数供 sym/entity 共用，sym.go 同步改用，行为不变）。

**决议与语义**：

- arg 决议复用 symResolve；命中节点须 kind=model，否则报错「X 不是 model 节点（kind=…），entity 只查数据实体」。
- 多义（如 Cumulative 双命中 Go+TS）：**选 Go 侧为主卡**（file 前缀非 web/ 的优先），其余进 Twins 合并展示；同侧仍多义则报错列候选。
- 链的合成：本 model 的全部投影边按 kind 分组；twin 边对侧 model 的 typed/handroll 边也并入（Via 标 `twin:<对侧id>`）——链要覆盖序列化边界两侧。
- 未盘点：主卡节点 `ProjScanned==false` 时 Warning 填「该实体未做投影盘点——链可能不完整，勿当序列化边界清单用；盘点走扫描派发」。
- 每个 ProjSite/Twin 过 ReAnchor（同 sym）。

**测试**（夹具在 testdata/repo 的 baseline 上加 projections 段与一个 TS 风格 model 节点；注意 cmd/graph_test.go 对该夹具的既有断言 `nodes==7`，新增节点后同步改那一处断言并在提交信息说明）：

| 用例 | 断言 |
|---|---|
| `TestEntityBasic` | model 主卡 + typed/handroll 分组正确、行号已锚定 |
| `TestEntityTwinMergesRemoteSites` | twin 对侧的 handroll 边并入且 Via 带 `twin:` 前缀 |
| `TestEntityUnscannedWarns` | ProjScanned=false → Warning 非空 |
| `TestEntityNotModel` | 对 func 节点查询 → 报错含「不是 model」 |
| `TestEntityGoPreferredOnTie` | Go/TS 同名 → 主卡是 Go 侧，TS 进 Twins |

**cmd 子命令**（照 graphSymCmd 形态，AddCommand 追加 `graphEntityCmd`）：

```go
var graphEntityCmd = &cobra.Command{
	Use:   "entity <model 名或节点 id>",
	Short: "数据实体的投影链：typed/handroll 投影点 + 跨语言孪生（序列化边界四查入口）",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		defer graphResetState()
		v, _, err := graphLoadView()
		if err != nil {
			return err
		}
		r, err := codegraph.EntityLookup(v, graphRepo, args[0])
		if err != nil {
			return err
		}
		return graphPrintJSON(cmd, r)
	},
}
```

cmd 烟测 `TestGraphEntity`：夹具上跑通、JSON 形状含 model/typed 键。

**测试范围**：`go test ./internal/codegraph/ ./cmd/`。**日志/注释**：同前声明；entity.go 文件头写职责边界。

## Task 3：`graph resolve` 与 `graph contract set`（派发）

**文件**：新建 `internal/codegraph/resolve.go`、`resolve_test.go`、`contractset.go`、`contractset_test.go`；`cmd/graph.go` 加两个子命令与 flag。

**Interfaces — Produces**：

```go
func ResolveAnchor(v *View, repoRoot, ref string) (*AnchorResult, error) // ref 形如 "path/file.go#Symbol"
type AnchorResult struct {
	Ref    string `json:"ref"`
	File   string `json:"file"`
	Line   int    `json:"line"`
	Anchor string `json:"anchor"`           // ok | moved | vanished | file_missing
	NodeID string `json:"nodeId,omitempty"` // 图内命中时填
}
func CheckDocAnchors(v *View, repoRoot, docPath string) ([]AnchorResult, error)
func SetContract(repoRoot string, c Contract) (before, after *Contract, err error)
```

**ResolveAnchor 语义**：拆 ref 为 file+symbol（`#` 右侧）；先在图内找 file 相同且 Name 或尾段等于 symbol 的节点 → 命中走 ReAnchor 并填 NodeID；图外退化为纯文件搜索：全文件按 `symTokenOnLine` 词边界找 symbol，定义形状行优先（复用 ReAnchor 的重找段逻辑——从 sym.go 抽 `findTokenLine(lines []string, token string) (def, any int)` 共用，sym.go 同步改用）；找到即 `moved`（图外没有「原行」概念，统一 moved 语义=行号由搜索得出）、找不到 `vanished`、文件缺 `file_missing`。

**CheckDocAnchors 语义**：读 markdown，正则 `` `([\w./-]+\.[A-Za-z]+)#([A-Za-z_][A-Za-z0-9_.]*)` `` 提取反引号内的 `file#Symbol` 引用（去重），逐条 ResolveAnchor。CLI `graph resolve --doc <md>`：全部 ok/moved 退出 0（moved 打印新行号），任一 vanished/file_missing 退出非零。

**SetContract 语义**：LoadTarget → 按 From+To 找条目（有则改：**仅覆盖调用方给出的字段**——CLI 未传 `--budget` 不动预算、未传 `--entries` 不动清单；无则建）→ ValidateTarget（issues 非空即报错不写）→ `json.MarshalIndent(t, "", "  ")` 整文件写回（格式统一化：首次 set 会产生一次性格式 diff，之后稳定——这是决策不是缺陷，写进函数注释）→ 返回改动前后的条目副本供 CLI 打印对照。CLI：

```
handoff graph contract set --from d_a --to d_b [--entries x,y] [--interfaces i] [--budget N]
```

flag 解析用 cobra 的 `StringSliceVar`/`IntVar`，`Changed("budget")` 判「未传不动」；新增 flag 记得进 `graphResetState`。

**测试**：

| 用例 | 断言 |
|---|---|
| `TestResolveAnchorInGraph` | 图内 file#尾段 命中 → NodeID 非空、anchor ok |
| `TestResolveAnchorOutOfGraph` | 图外文件真实符号 → moved + 正确行号；不存在符号 → vanished |
| `TestCheckDocAnchors` | TempDir 写 md 含两好一坏引用 → 3 条结果、坏的 vanished |
| `TestSetContractCreateAndUpdate` | 建新条目、改 budget 不动 entries；ValidateTarget 失败（如 to 域不存在）不写文件 |
| cmd 烟测 | `TestGraphResolveDoc`（坏锚退出非零）、`TestGraphContractSet`（TempDir 复制夹具 target 后 set，diff 对照输出） |

**测试范围**：`go test ./internal/codegraph/ ./cmd/`。**日志/注释**：同前声明。

## Task 4：采纳接线、盘点派发与真机回归（协调者本地，不派发）

1. charter 仓 `skills/contract/SKILL.md` 与 `skills/breakdown/SKILL.md`：引用锚条款——「文档引用现状代码推荐 `file#Symbol` 符号锚（项目有 codegraph 时），出稿自检跑 `handoff graph resolve --doc <本文档>`，坏锚即修」。
2. Task 1-3 合并后，**派发投影点盘点卡**（同覆盖线模式）：对 proto 包全部 wire model（及 twin 名单）识别 typed/handroll 投影点与跨语言孪生，落 projections + projScanned；validate 过；交付说明列「盘点过但零投影」的 model 名单。
3. 真机回归（穿真实序列化边界）：`handoff graph entity Cumulative`——链上必须同时出现 store 侧（typed）、agentd 手搭侧（handroll，含 `Manager.handleUsage`/`handleSpend` 一类）、TS 孪生（twin）；`graph entity SpendEntry` 的 handroll 组非空。任一缺即回执行者。
4. `graph resolve --doc docs/superpowers/specs/2026-08-22-codegraph-sym-plan.md` 在真实仓库跑通（该文档含 file:line 老式引用不受影响，验证的是不误报）。

## 四项检查

1. **缺陷族**：①序列化边界——EntityResult 即该族的自动化检查器本身，其自身投影仅 graphPrintJSON 一处，cmd 烟测穿链断言；②假完整感——未盘点 Warning 是决定性防线（TestEntityUnscannedWarns）；③写回破坏——SetContract 先 Validate 后写、失败不落盘（TestSetContract 断言）；④合并/吸收一致性——TestProjectionAbsorb 对 dead 连带剔除。
2. **序列化边界设问**：新增 wire 字段（projections/projScanned）从扫描产出到 entity 消费经 LoadGraph→Merge→EntityLookup，TestProjectionMerge+TestEntityBasic 穿这条链；kind 用字符串枚举、validate 收口取值。
3. **上下文预算**：三个派发 task 文件集各自有界（types/merge/validate/absorb；entity+sym 抽函数；resolve+contractset+cmd）。
4. **类型标注**：d_contract logic 域机内闭环；Task 4 的真机回归是显式清单。

## 自审三查

spec §5 覆盖：投影边→T1；entity→T2；resolve/--doc→T3；contract set→T3；charter 条款→T4-1；盘点派发→T4-2；真机回归→T4-3。占位符：无。跨 task 签名：T2/T3 消费的 View.Projections、SymMatch、Contract 与 T1/现状定义逐字一致。
