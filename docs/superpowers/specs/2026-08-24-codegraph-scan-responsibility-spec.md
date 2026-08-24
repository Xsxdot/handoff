# Spec：扫描补职责面——packages 节 + 类型 doc 抓取 + B220 盲区消化（B231）

> **状态：已批准（2026-08-24，用户「批准」）**
> 级别/档位：**L3 轻档**——动跨仓 wire schema（charter graph 库定义 baseline.json、handoff 配方产出），两侧工作量均小于流程固定成本。
> 来源：C1.3 一期走查（用户问「执行器适配这一组是干什么的/每个容器职责边界」，查看器无数据可答）；charter roadmap 10a。

## 问题陈述

三笔账并一趟重扫解决：

1. **职责数据零源**：baseline 没有包这一级（包 doc 注释无处落），容器只有 `label/kind/domain/entry` 四字段；「这一组是干什么的」在查看器/CLI 都答不出。
2. **类型 doc 没抓**：node.summary 对 `model` 节点覆盖仅 225/707 ≈ 32%（func 55%）——`claudecode.Adapter` 这类容器的职责其实写在类型 doc 上，扫描配方没收。
3. **B220 盲区 + 基线失鲜**：盘→图完整性自检 2026-08-23 已写进配方（docs/codegraph-scan-recipe.md），但**基线从未按新配方重扫**：cmd/ 实测 9 文件零节点、24 个 card 命令零 entry、gap 少报约 18%、当前基线 820 节点疑似失鲜（commit b5427188，已落后 main 多个合并）。

B233（架构物理化）的全部进度计量压在图上——先修尺子，后开刀。

## 现状读数（事实核查，2026-08-24）

- Schema canonical：charter `graph/codegraph/types.go#Graph`（handoff go.mod 钉版）；additive-only 新键有先例（`lifecycle`，v0.3.0，旧消费方安全忽略）。
- 配方：`docs/codegraph-scan-recipe.md`（派发 plan 模板，codex + --new-worktree），B220 完整性自检已在「扫描范围与完整性」节。
- 现基线：`codegraph/baseline.json` meta.generator=codex-codegraph-b223-callgraph，branch=cards/B223-implement。
- 词表善后依据：best.json 容器归属映射 233 条（C1.6）；预算棘轮 36 方向共 602（钉在盲区未计入的旧实测值上）。

## 方案（含弃选）

**packages 节进 baseline（事实层）**：包=目录，是代码事实，摘要来自包 doc 注释——归 baseline。**弃选**：写进 best.json/domains 声明（那是应然层的职责宣言，与「代码里实际写着什么」是两回事，混层正是 C1.6 拆掉的病）；只改配方不改 schema（包 doc 无处落盘）。

**善后政策（用户 2026-08-24 拍板）**：重扫翻出盲区直调导致超预算的方向，按新实测值**一次性重定标**，理由字段统一「B220 测量修正」，`budget-raised` 留痕可审计。弃选：逐条人工过堂（盲区文件全是存量代码，几乎必然全是测量修正；不重定标则 CI 永久红，棘轮反而失效）。棘轮「只减不增」防的是新增债务，不挡尺子修正。

## 用户故事

1. baseline.json 新增顶层 `packages` 节：key=包目录路径（与 node.file 的目录一致），value 含 `summary`（包 doc 注释一句话）；出现在任一节点 file 上的目录都必须有条目；**无 doc 注释的包 summary 为空串，不编造**。
2. `model` 节点的 summary 收录类型 doc 注释：有注释的类型 100% 收录（拉满的是「有注释的收录率」，不是编造覆盖率）；func 同规则复核。
3. 重扫消化 B220 盲区：cmd/ 9 个零节点文件入图、24 个 card 命令 entry 建齐；文件级完整性自检「盘上文件数 = 图中文件数」两数相等，差集逐个说明。
4. 重扫后 `graph stale` 报告清零（当前 820）；meta.branch/commit 指向 main 最新。
5. 善后三步曲：新容器补进 best.json 归属（C1.6 词表机械延伸）；超预算方向按拍板政策重定标并留痕；最终 `codegraph check` fails 0、未归属 0。
6. 兼容：旧消费方（旧版 CLI/查看器）忽略 packages 键；charter webui `types.ts` 镜像补键（**消费归三期**，本刀不改任何查看器渲染）。

## 契约语义与接缝（L3 段——只定语义）

- **唯一承重接缝：baseline.json wire schema**（charter graph 库定义与执法，handoff 配方产出）。增量语义：新增**可选** `packages` 顶层键，additive-only，缺席合法；`Package.summary` 为纯文本一句话，不携源码正文。精确字段名/类型/validate 规则归 contract 节点对 types.go 落地。
- **摘要是事实转录不是生成**：packages.summary 与 node.summary 只许来自源码 doc 注释的转录/紧缩，禁止扫描 agent 自行概括无注释代码——配方红线，防「图里写着一个不存在的意图」。
- 版本：charter graph 库按 additive 惯例升版并打 tag，handoff go.mod 随之钉版。

## 实现决定

- charter 侧收口在 types.go + validate（packages 目录引用完整性：键必须是图中出现过的目录，warn 档）+ 版本 tag；
- handoff 侧收口在配方文档增补（packages 抓取规则、类型 doc 必抓、示例），重扫走既有派发路径（linux-01 / codex / --new-worktree）；
- 善后（best 补归属、预算重定标）由协调者本地执行——动 best.json/target.json 属契约物，不交给扫描 agent。

## 测试决定（接缝清单）

- **charter 库缝**：packages 键的 round-trip / omitempty / validate 引用完整性单测，变异验证（引用检查失效必须能红）。
- **配方缝（无自动测试，全走真机清单）**：重扫后 validate 通过；完整性自检两数相等；packages 条目数=图中目录数；model summary 收录率统计（有 doc 的 100%）；stale 0；check fails 0；budget-raised 留痕条数与超预算方向数一致。

## Out of Scope

- 查看器消费 packages/职责展示（三期，roadmap 第 10 条 A 组）。
- 大杂烩容器拆分（B232 另卡）。
- 领域/子系统的应然职责（已在 best.json responsibility，不动）。
- TS 侧（web/src）包摘要——TS 无包 doc 惯例，本期只做 Go 目录，TS 目录 summary 留空不编造；差距落 roadmap。

## 备注

- 与 B228（基线落后 main）同因不同卡：本刀的全量重扫顺带刷新基线，但「合并分支必产视图、absorb 常态化」的流程债仍归 B228。
- 重定标拍板满足三重闸门（难逆转：预算上调回不去；无上下文会惊讶：棘轮竟然升了；真取舍：逐条过堂被否），contract 节点落拍板记录。
