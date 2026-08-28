# handoff 后端 Go `flows` 扫描

> **状态：已批准（2026-08-28）**
> 卡：B277；级别 **L2**；项目 handoff。
> 上游：charter C17 契约 §2.1-7（承重 `flows` 键 = 对外入缝 ∪ 紫框下钻目标 ∪ 右栏实现方法）。
> 用户裁决：只扫后端 Go；TS/React 不做。

## 问题陈述

查看器和 `codegraph flow` 已经按 C17 消费 `baseline.flows`。现役 main 基线（`codegraph/baseline.json`，generator `codex-codegraph-b231-full-rescan`，2026-08-24）**flows 段为空**：3636 节点 / 4735 边 / 0 flows。缺这段时行为轴只能 degraded，菱形/卫语句/紫框没有数据。

C12 曾派全量重扫（task `05a8a66f`，分支 `codegraph-scan-2026-08-27`）产出 530 条 flow，其中 162 条键是 `kind=entry`。该产物未合入 main，且主语按入口，与 C17 冲突。本卡不合并那份数据，按 C17 重扫 Go 承重函数。

## 级别与档位

**L2。** 单仓（handoff）数据面 + 一条机械抽取器；不改 charter 查询契约（对接已冻结的 C17 wire，但不在本卡改 charter 代码）。plan 写得出抽取步骤，不是 L1 三行。

## 方案

**推荐：在现役基线上增量写入 `flows`，用 Go AST 从源码抽控制流，禁止再手填几百条 JSON。**

1. 承重集合只含 **Go** 符号：未折叠跨域入缝 ∪ 这些 flow 里 `iface:true` 调用的实现方法。不给 `kind=entry` 建 flow。不给 `.ts`/`.tsx` 建 flow。
2. 不重扫 `nodes`/`edges`/`implements`/`containers`。`call.to` 必须已在现图；解析不到就跳过该 step，不造节点、不补边。
3. 抽取器用 `go/ast` 走函数体；callee 优先 `go/types`，对不上现图就丢。`if err != nil { return }` 必须是 `branch.then=[return]`，return 不得再当未引用 sequential root。
4. 配方 `docs/codegraph-scan-recipe.md` 的承重四条改成 C17 三元并集，并写明「Go only、entry 不是默认键」。

弃选：再派一次「读源码填 JSON」的数据任务——C12 已证明会空转或按错主语。弃选：先做 charter 永久扫描器（roadmap 32）——本卡只要 handoff 基线有可消费的 Go flows。

## 用户故事

1. 打开结构轴「对外面」一个未折叠 Go 入缝，行为轴画出该函数的步骤树（有 call/branch/return），不是 degraded 空页。
2. `if err != nil { return }` 在图上是菱形侧甩，不是主干上的第二个 return。
3. 接口调用步骤 `iface: true`，`to` 是接口节点；右栏实现可打开且实现方法自己有 flow 或显式无流程。
4. `codegraph flow <入缝方法>` 对有 flow 的符号 `degraded=false`；对 entry 通道名不把通道当成主语。
5. TS 符号仍然没有 `flows[id]`，查看器对它们保持 degraded。

## 实现决定

- 写面：`codegraph/baseline.json` 的 `flows` 与 `meta`（`scannedAt`/`generator`/`commit`）；配方正文；可保留的抽取器与其测试。
- 只读：`target.json` / `best.json` / `domains/*.json` / 业务源码。
- 种子（未折叠入缝）：跨域边的 `to` 为 `kind=func`、`file` 以 `.go` 结尾、且 **不是**（容器 kind ∈ {函数组, TypeScript 函数组} ∧ 从 entry BFS 复用度 ≥ 10）。现状读数约 160 个。
- 再并上 `implements` 里这些 flow 实际点到的接口的实现方法（现图 7 条实现边，Go）。
- 不把「所有 call.to」扩进集合：非承重 callee 画成普通矩形、不紫、不下钻。
- `meta.generator` 写 `codegraph-flows-b277-go`。

## 测试决定（接缝）

1. **抽取器入口**（新建，调用方=本卡 `go test` + 扫描命令）：给定迷你 baseline + 含 `if err != nil { return }` 和一次图内调用的 Go 函数，输出的 steps 里 return 被 `branch.then` 引用、且不在未引用 root 里；`call.to` 为图内节点。
2. **基线 JSON 边界**（`baseline.json` 被 `encoding/json` 读回）：`flows` 非空；每个键是已有 Go `func` 节点；没有任何 `kind=entry` 键；每个 `call.to` 存在；卫语句 return 均被引用。用脚本对真实产物跑，不能只看抽取器单测。

## Out of Scope

- TS/React flows（用户 2026-08-28 裁掉）。
- 全函数覆盖、SSA 级 CFG、goto/defer 还原（roadmap 27/32）。
- 重扫节点/边、改入口容器拓扑、合并 C12 未入主线的 530 条 entry 主语 flow。
- 改 charter `graph/codegraph` 查询实现或 webui。
- 把抽取器升级成替代 AI 全量扫节点的永久扫描器。

## 备注

C12 未合入扫描已 `handoff done`（`05a8a66f`），说明里写清不采用。本卡验收后 flows 才允许合 main，合并仍归人。
